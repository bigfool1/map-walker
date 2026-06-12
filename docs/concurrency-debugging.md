# Hub Tick 测试并发陷阱

Task 6（异步位置持久化）踩坑记录。Hub 测试用真实 channel 驱动 tick，Hub `Run()` 内部是 `select` 多路复用。新增 persistence tick 后，select case 从 4 个变 5 个，以下陷阱反复出现。

## 陷阱

### 1. select 随机竞争

两个 tick channel 同时 ready 时，Go 随机选 case。测试连续 `simulations <-` → `broadcasts <-`，Hub 可能先消费 broadcast 再消费 simulation。

**修法**：发 tick 后加 `assertNoMessage(client)` 确认已处理，不要假设 send 顺序 == 处理顺序。

### 2. channel send 返回 ≠ case body 执行完

无缓冲 channel 的 send 在 receive 取走值时就返回，不保证 case body 已跑完。测试主 goroutine 与 Hub goroutine 并发。

**修法**：轮询等待可观测副作用（如 `waitForPersistBatches`），不要以 `persistence <-` 返回作为持久化完成的依据。

### 3. Unregister 是异步的

`hub.Unregister()` 只把事件丢进 channel，不等待 `removeClient` 执行完。

**修法**：Unregister 后 `waitForClientClose(client)`（removeClient 会 CloseSend 关闭 client.done）。

### 4. Submit 不能同步阻塞 Hub actor

`persistDirtyPositions()` 在 Run() loop 里同步调用 Submit。如果 Submit 阻塞（DB 写等），整个 actor loop 卡死。

**修法**：测试 mock（如 blockingPersister）的 Submit 必须在独立 goroutine 里阻塞；生产代码的 Submit 通过 `go func() { channel <- batch }()` 不阻塞 Hub。

### 5. 异步 Submit 需保 FIFO

多个 `go func() { queue <- batch }()` 并发 send 会乱序。

**修法**：单 consumer goroutine + 有序 channel。

### 6. Drain 测试有竞态窗口

Submit 异步投递，Drain 时 batch 可能还没到达 worker。

**修法**：Drain 测试直接往 `worker.ordered <- batch` 同步入队。

### 7. 错误归因先排除时序

有 persister 的测试失败时，先检查 channel/select/异步时序。别先改业务代码——运行时逻辑通常是对的。

## 关键场景速查

| 场景 | 行为 |
|------|------|
| 周期性 save | 只保存自上次 interval 以来移动过的玩家；addPlayer 不要标记 persistDirty |
| 真正 disconnect | submitFinalPosition → RemovePlayer |
| 同账号 replacement | obsolete Unregister 因 `current != client` 直接 return，不 final save |
| World | 无 SQL 依赖；Hub 是唯一 actor |

## 测试 helper 签名

新增 tick channel 时 `newTestHub()` 返回值会变（当前返回 4 个 channel）。所有调用点需**一次性全部更新**，涉及 `hub_test.go` 和 `client_test.go`。Go 编译器不允许未使用变量或返回值数量不匹配，漏一个都编不过。
