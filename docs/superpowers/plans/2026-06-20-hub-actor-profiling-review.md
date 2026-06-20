# Review: 2026-06-20-hub-actor-profiling.md

评审对象：`docs/superpowers/plans/2026-06-20-hub-actor-profiling.md`
评审日期：2026-06-20
评审范围：动工前的可行性、术语准确性、与现状代码的对齐、验证手段是否闭环。

> 本地与生产均为 MySQL，SQLite 仅保留用于纯单元测试。下面所有建议默认 backend = MySQL。

---

## 总评

方向正确：本阶段只测量、不改架构，用 pprof + 自维度指标交叉验证，最后给硬决策规则。
"Bottleneck Decision Rule" 把"Hub 是下一个瓶颈"写成可证伪条件，是这份 plan 最大的价值。

但有几处具体定义、术语和落地点应在动工前先收紧，否则实现者会按字面去找不存在的阶段、或做完没接出读取面。

---

## 任务级修正

### Task 1（pprof）：默认 mux 与项目 mux 不一致，block/mutex rate 没开

现状：`internal/server/server.go` 用自建 `http.NewServeMux()`，而 `net/http/pprof` 的 `init()` 注册到 `http.DefaultServeMux`。所以仅 `import _ "net/http/pprof"` 不会让端点出现在 `/debug/pprof/`。

修正建议：

- 任务边界二选一并写进 plan：
  - 方案 A：在 `Server.Routes()` 里手动挂 `pprof.Index`、`pprof.Profile`、`pprof.Heap`、`pprof.Cmdline`、`pprof.Symbol`、`pprof.Trace`、`pprof.Handler("goroutine"|"block"|"mutex")`；或
  - 方案 B：单独起一个 loopback only 的内部端口的 `http.DefaultServeMux` server。
- 在 `main.go` 启动早期：
  - `runtime.SetBlockProfileRate(...)`
  - `runtime.SetMutexProfileFraction(...)`
  - 否则验证步骤里的 `/debug/pprof/block` 与 `/debug/pprof/mutex` 永远是空。
- 一个 flag 控制开关，默认关，跑 profile 时显式开。

### Task 2（tick 阶段指标）：百分位算法和承载位置都没定

修正建议：

- **算法选定**：每秒 reset 的固定桶直方图（如 0.1ms 步长到 200ms），零分配、actor 内单写、足够本阶段使用。HDR/t-digest 在这个数据量下都属于过度工程。
- **承载位置**：扩展 `HubSnapshot`（或新增 `HubProfileSnapshot`），接入 `/api/stats/synthetic`（或新增 `/api/stats/profile`）。否则做完没有读出来的方式。
- **写入约束**：直方图自身的写入必须 lock-free，且只在 actor goroutine 内写，避免引入新同步点。
- 把 "Add focused tests for percentile aggregation **if a new helper is introduced**" 里的 "if" 去掉——一旦选了固定桶直方图，就必须有单测覆盖。

### Task 3（broadcast 子阶段）：术语与现状代码对不上

> 记录 visible entity snapshot construction duration

`broadcastReplication` 早已不再构建全可见快照，只在 `sendInitialization` 时构建一次性 snapshot；广播路径走 per-recipient `ReplicationChanges` 增量。

修正建议——子阶段改为与实际代码对应的命名：

- AOI move/insert 应用（`applyMovementAOIChanges`）
- mover 旧邻居快照（`snapshotMoverVisibility`）
- 收集品可见性重算（`recalcCollectibleVisibility`）
- 收集品 spawn 反向扇出（`advanceCollectibleReplacements`）
- per-recipient `ReplicationChanges` 累积（`byRecipient` 段）
- JSON 编码（`TryEncodeReplicationUpdate`）
- 客户端入队/发送（`client.Send`）

另外补一条验证手段：利用已有的 `internal/realtime/replication_benchmark_test.go` 做 micro-benchmark + pprof 对照——micro-bench 下的火焰图比合成负载下干净得多。

### Task 4（队列延迟）：unbuffered channel 下"queue"语义易误导

现状所有 Hub channel 都是 unbuffered（`inputs`、`collects`、`register`、`unregister`、`appearanceUpdates`、`leaderboards`、`disconnectUser`）。"enqueue 时间戳"在 unbuffered 下等价于"sender block 时间"。

修正建议：

- 任务名改为 **"actor handoff latency"**：发送方 send 前打 `time.Now()`，Hub 收到立刻 `time.Since` 记录。明确写明"本阶段不引入 channel buffer，handoff latency = sender wait time"。
- `SubmitCollect` 有 `default: drop`。**必须额外加 drop counter**，否则丢失的拾取意图在指标上完全隐形。
- `leaderboards` 是同步 reply 模式，低 QPS 下 handoff latency 意义不大，列为可选，避免在低价值路径上耗时。

### Task 5（外部指标）：拆 a/b 子任务，指定 runtime API

修正建议：

- 拆成两个子任务：
  - **5a**：在 `internal/storage/PersistenceWorker` 与 `ScorePersister` 上**新增**最小 introspection（queue 长度、最近一次写入耗时、失败计数）。`internal/storage/` 内的纯加字段，独立 agent 可做。
  - **5b**：在 Hub snapshot 里拉取并汇总。
- 把 "if the current persistence worker exposes these surfaces" 去掉，否则会被解读为可选跳过。
- 运行时指标用 **`runtime/metrics`**，不要用 `runtime.ReadMemStats`：
  - `/gc/pauses:seconds`
  - `/sched/goroutines:goroutines`
  - `/cpu/classes/...`
  - `runtime.ReadMemStats` 是 STW，频繁调用会影响测量本身。

### Task 6（负载矩阵）：MySQL 假设要写明，矩阵成本要给出

修正建议：

- **backend 假设写明**：本地 + 生产均为 MySQL，跑矩阵默认 MySQL DSN。SQLite 不参与负载矩阵。
- **DB 配置写进环境元数据**：MySQL 版本、连接池大小、`max_connections`、是否与服务端同机。这些是后续对比的前提。
- **成本提示**：5 × 4 = 20 个稳定窗口，每个含 ramp-up + 30s CPU profile + heap/goroutine 抓取，全跑一轮 > 1 小时。Plan 应允许"先跑 100/500/2000 × 1/4/8 的对角矩阵"作为快速第一轮，全矩阵作为可选完整轮。
- **profile 文件不入 git**：直接落实为 `.gitignore` 一行（如 `docs/benchmarks/profiles/*.pprof`），plan 里写明就行。
- **报告字段补充**：增加 "backend"、"hardware"、"synthetic process collocation" 三列，否则跨提交对比缺前提。

---

## 全局缺失项

### 1. 观察者效应回归

增加指标的代码本身会动 hot path。建议加一条总验证：

> Task 2-5 完成后，重跑 `aoi_scale_test`、`collectible_scale_test`、`replication_benchmark_test`，确保关键数字未明显回归（AOI 候选对数量、replication 字节数、benchmark allocs/op）。

不写这条，AGENTS.md 里"性能必须先有 benchmark"的约定就被本次新增破坏。

### 2. Synthetic 客户端是被测者

合成客户端本身吃 CPU，与被测进程同进程时会争核，`GOMAXPROCS=1` 下尤其严重。Plan 应：

- 要求矩阵记录"同进程合成"或"独立进程合成"；
- 或干脆声明本阶段只测同进程，结果中扣除合成开销不在本阶段范畴。

### 3. Multi-Agent 并行约束

按 AGENTS.md "Multi-Agent 协作指引"，"修改 Hub actor" 必须单 agent。Task 2、3、4 都改 `Hub.Run()` 周边，必须串行。在 "Scope Guardrails" 下补一句：

> Task 2-4 共享 Hub 改动面，必须由单一 agent 顺序完成；Task 1、5a（storage 部分）、6 可与之并行。

### 4. 文档同步

Plan 末尾加一行：

> 完成后更新 `docs/map-walker-handoff.md`，记录新指标、pprof 端点、profile flag。

否则按 CLAUDE.md "每次完成一个 plan 后只保留这个 phase 的详情"的约定会漏掉。

---

## 修正项汇总（实现者快速列表）

| # | 位置 | 修正 |
|---|------|------|
| 1 | Task 1 任务边界 | 显式列出要挂的 pprof handler；加 `SetBlockProfileRate`/`SetMutexProfileFraction`；加开关 flag |
| 2 | Task 2 任务边界 | 定下固定桶直方图；定下 snapshot 接入点；去掉 "if a new helper" 的 if |
| 3 | Task 3 子阶段命名 | 改为与现状代码对齐的 7 个阶段（见上）；验证加 replication_benchmark_test |
| 4 | Task 4 命名与计数 | 改名为 actor handoff latency；显式说明 unbuffered 语义；加 `collects` drop counter；`leaderboards` 列为可选 |
| 5 | Task 5 拆分 | 5a：storage introspection 字段；5b：Hub 汇总。runtime 指标用 `runtime/metrics` |
| 6 | Task 6 backend 假设 | 写明 MySQL，记录 DB 配置与硬件；允许对角矩阵作为快速一轮；profile 文件落实 `.gitignore` |
| 7 | Scope Guardrails 末尾 | 加并行约束（Task 2-4 单 agent 串行） |
| 8 | 新增总验证 | 观察者效应回归：重跑 aoi/collectible/replication 三个 scale/benchmark test |
| 9 | Plan 末尾 | 完成后更新 `docs/map-walker-handoff.md` |

---

## 不需要改的部分

- 6 步任务划分与依赖顺序合理（pprof 先行，actor 内部细分指标后行）。
- "Scope Guardrails" 列出的禁止项准确。
- "Bottleneck Decision Rule" 的 6 条判定条件完整，覆盖了 GOMAXPROCS 扩展性、单核饱和、profile 归因、tick 预算、queue 延迟、外部瓶颈排除。保留原样。
