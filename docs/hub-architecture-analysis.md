# Hub 架构分析

2026-06-15

## 现状

`internal/realtime/hub.go` 中 Hub struct 共 **37 个字段**，按职责分组：

### 领域引用 (5)
world, aoi, collectibleField, loadSavedPlayer, persister

### 分数 (2)
scorePersister, playerScores

### 机器人标记 (1)
syntheticPlayerIDs

### 收集品可见性 (6)
visibleCollectibleIDs, pendingCollectEntered/LeftIDs/Spawned/Collected, collectCooldowns

### 通道 (7)
collects, register, unregister, inputs, appearanceUpdates, disconnectUser, stop

### 生命周期 (3)
done, stopOnce, clients

### 持久化跟踪 (2)
persistDirty, persistSeq

### AOI 复制缓冲 (3)
pendingEntered, pendingLeft, pendingAppearances

### 定时器 (5)
simulationTick, broadcastTick, persistenceTick, statsTick, stopTickers

### 统计 (2)
stats, snapshot

---

## 问题一：是否需要重构？

AGENTS.md 第 147 行："Hub struct fields > 30 时考虑按职责打包成子 struct"

### 推荐方案

只提取两组最独立的部分，Hub 从 37 降到 ~19 字段：

**第一组：hubChannels（12 字段）— 纯接线层**

```
collects, register, unregister, inputs, appearanceUpdates,
disconnectUser, stop,
simulationTick, broadcastTick, persistenceTick, statsTick, stopTickers
```

这些字段只在三处出现：构造函数、Run() 的 select、公开的 Submit/Register 等方法。没有业务逻辑访问它们。单独成 `hubChannels` struct，不影响任何业务代码的可读性。

**第二组：collectibleState（6 字段）— 收集品子系统**

```
visibleCollectibleIDs, pendingCollectEntered, pendingCollectLeftIDs,
pendingCollectSpawned, pendingCollectCollected, collectCooldowns
```

有清晰的专属方法：recalcCollectibleVisibility、advanceCollectibleReplacements、takePending*、processCollect。方法随数据一起迁到 `collectible_state.go`，Hub 不需要知道内部实现。

### 不建议提取的组

| 组 | 理由 |
|----|------|
| AOI 复制缓冲 (pendingEntered/Left/Appearances) | broadcastReplication 是 160 行的最热路径，额外一层间接寻址降低可读性 |
| 持久化跟踪 (persistDirty/Seq) | 仅 2 个简单 map，没有封装逻辑 |
| 领域引用 (world/aoi/collectibleField/persister) | Hub 的核心身份，提取后反而不直观 |

### 不做全量重组的理由

broadcastReplication 跨越 10+ 组字段访问。如果每组都变成 `h.xxx.field`，最需要可读性的代码反而被降级。只提取真正独立的接线和子系统。

---

## 问题二：哪些事件应脱离 actor 循环？

### actor 循环的价值

Hub.Run() 的 select 是单一 goroutine，保证所有状态修改串行、无需锁。操作只有在一种情况下才应脱离循环：**不依赖 Hub 独占状态 + 脱离后不需额外同步**。

### 候选逐项分析

| 候选 | 频率 | 瓶颈 | 可脱离 | 理由 |
|------|------|------|--------|------|
| broadcastReplication 中的 removeClient | 低频（仅 send 满时） | **是** — 内含同步 DB 写 | **可以** | 改一行：`go h.DisconnectUser(recipientID)` |
| persistDirtyPositions | 0.2Hz | 否 | 不划算 | Submit 已异步，5 秒间隔无所谓 |
| logStats | 1Hz | 否 | 否 | aoi.TakeStats() 修改 AOI 内部状态 |
| TryEncodeReplicationUpdate 编码 | 10Hz per 接收者 | 待观察 | 技术可行，成本高 | 需要 worker channel + 同步点 + 关闭协调 |
| byRecipient map 构建 | 10Hz | 否 | 否 | 读 world/AOI/clients（actor 独占），并发有 data race |

### 唯一推荐：removeClient 异步化

位置 `hub.go:911-913`：

```go
// 当前：
if sendOK := client.Send(data); !sendOK {
    h.removeClient(client)
}

// 改为：
if sendOK := client.Send(data); !sendOK {
    go h.DisconnectUser(recipientID)
}
```

DisconnectUser 已存在（hub.go:260-267），发 disconnectUser channel + 等 done。异步化后，广播 tick 不会因单个客户端的 DB 写而被阻塞。

**安全性：**
- 同一客户端多次失败：第一条 goroutine 使 actor 移除后，后续 goroutine 的 `h.clients[userID]` 为 nil → no-op
- Hub 停服时：goroutine 在 DisconnectUser 中走 `case <-h.done` 返回
- 生命周期：actor 处理 disconnect 后关闭 done，goroutine 随之退出

---

## 其他发现

### scorePersister.Drain() 已经在 select 之外

hub.go:244-246，每轮 select 返回后调用：

```go
if h.scorePersister != nil {
    h.scorePersister.Drain()
}
```

这是唯一脱离 select case 的操作。它可行因为 Drain() 不读 Hub 状态、内部有自己的 goroutine、调用是幂等的。这是一个有效的模式 —— 将 I/O 排空操作挂在 select 后，而非作为一个 case。
