# AGENTS.md — map-walker

Go 1.26 项目，服务端权威的 WebSocket 实时移动+收集服务。支持用户账户、会话认证、位置/分数持久化、外观同步、AOI 空间索引、按可见性复制、收集品拾取、在线排行榜和合成客户端负载。

## 目录结构

```
cmd/map-walker/      — 服务入口、信号处理、优雅关闭、合成客户端 flags
config/              — 收集品区域 JSON 配置
internal/game/       — World、输入状态、移动模拟、外观、AOI 空间索引、CollectibleField、区域配置
internal/realtime/   — Hub actor loop、tick 调度、连接、消息协议、复制、持久化接口、HubSnapshot、排行榜、拾取处理
internal/server/     — HTTP 路由、静态文件、WebSocket 接入、认证/外观/排行榜/admin 端点
internal/auth/       — 用户注册/登录、会话管理、外观校验、bcrypt 密码、合成身份传递
internal/storage/    — SQLite/MySQL、迁移、用户/会话/位置/外观/分数持久化、ScorePersister
internal/storage/migrations/ — 按序号命名的向前 SQL 迁移文件
internal/synthetic/  — 合成客户端：Client、Manager、Provisioner、SyntheticSnapshot
web/                 — Leaflet 地图、认证卡片、账户菜单、外观编辑器、虚拟摇杆、admin 页面、收集品 UI、排行榜
```

- `messages.go` 定义 `input` / `collect` / `self_state` / `visible_entities_snapshot` / `collectible_regions` / `visible_collectibles_snapshot` / `replication_update` / `collect_result` 协议
- `game.World` 拥有玩家坐标、外观、moved/removed 集合；客户端只能发送输入状态和拾取意图
- `game.CollectibleField` 拥有收集品实例、600m 网格空间索引、替换调度；纯逻辑无锁
- `game.AOIIndex` 600m 网格空间索引，500m 进入/600m 离开迟滞，九格候选扫描；`QueryPlayerIDsNearPoint` 用于收集品反向扇出
- `Hub.Run()` 是唯一 actor loop，按 20 Hz 模拟、10 Hz AOI 复制、推进收集品替换、处理拾取、每 5s 持久化位置
- 用户身份和合成标记通过 `map_walker_session` cookie（SHA-256 哈希 token）传递到 WebSocket；合成身份是服务端信任的持久属性
- `synthetic.Manager` 管理合成客户端生命周期、ramp-up 和聚合统计（`SyntheticSnapshot`）
- `Hub.Snapshot()` / `Manager.Snapshot()` 返回不可变的统计快照，供 admin API 消费
- admin 页面 (`/admin`) 和 stats API (`/api/admin/synthetic-stats`) 仅在 `MAP_WALKER_ADMIN_TOKEN` 设置时激活
- 收集品分数通过 `ScorePersister` 异步持久化，合并去重，失败指数退避重试（上限 30s），断连/登出/关闭时同步提交
- 排行榜通过 Hub actor 请求/响应按需计算，无缓存/轮询/推送；过滤合成和离线用户

## 命令

```bash
go test ./...              # 跑所有测试
go fmt ./...               # 格式化（等效 gofmt -w .）
go run ./cmd/map-walker    # 启动服务，访问 http://localhost:8080
```

启动选项：

```bash
go run ./cmd/map-walker -db-driver sqlite -db-dsn data/map-walker.db
go run ./cmd/map-walker -db-driver mysql -db-dsn 'user:pass@tcp(localhost:3306)/mapwalker'

# 合成客户端（需 DB 里已有账户，或加 -synthetic-auto-provision）
go run ./cmd/map-walker -synthetic-clients 50 -synthetic-ramp-rate 5

# admin 监控页面
MAP_WALKER_ADMIN_TOKEN=secret go run ./cmd/map-walker -synthetic-clients 50
# 访问 http://localhost:8080/admin，输入 token
```

## 存储后端

- **MySQL 是生产目标后端**。SQLite 保留用于本地开发和测试。
- 新的性能敏感存储特性（如批量位置持久化）不需要提供 SQLite 等价实现。
- `internal/storage/position_batch.go`：MySQL `UPDATE ... JOIN` 批量更新，每 chunk ≤500 行，独立事务。
- `PersistenceWorker` 按 `db.Driver()` 自动路由：MySQL 走 `applyBulk`，SQLite 走 `applyPerRow`。

## 编码约定

- writing-plans时只写任务边界、行为目标、涉及模块和验证方式
- 不做提前抽象，一个函数一个职责
- Hub actor 模式下，状态修改集中在 `Run()` loop 里，不要在其他地方加锁
- 周期性持久化用 async `Submit`（不阻塞 Hub）；final save 用 `SubmitSync`（同步保证已提交）
- AOI 变更缓冲（pendingEntered/pendingLeft/pendingAppearances）在 broadcast tick 统一消费和清零
- `replication_update` 是每个客户端独立构建的——不要假设全局广播
- 删代码 > 加代码
- 没有显式要求的验证/错误处理/fallback 不要加
- 不确定时先问，不要猜
- Hub tick 测试的并发陷阱见 [docs/concurrency-debugging.md](docs/concurrency-debugging.md)
- 数据库迁移文件按序号命名，仅向前迁移

## Multi-Agent 协作指引

### 模块独立性

以下模块可以独立并行开发，互不阻塞：

| 模块 | 文件范围 | 依赖 |
|------|---------|------|
| `game/` | `internal/game/*.go` | 无外部依赖，纯逻辑 |
| `auth/` | `internal/auth/*.go` | 仅依赖 `storage/` 接口 |
| `storage/` | `internal/storage/*.go`（含 migrations） | 无内部依赖 |
| `web/` | `web/*` | 仅通过 HTTP/WS 协议与后端通信 |

`internal/realtime/` 和 `internal/server/` 依赖上面所有模块，改动时需要注意。

### Agent 分配策略

| 场景 | 使用方式 |
|------|---------|
| 新增 game/ 逻辑（如 AOI、World 新方法） | 独立 agent，`game/` 范围内改完 + 测试 |
| 新增 storage/ 迁移或查询 | 独立 agent，`storage/` 范围内改完 + 测试 |
| 新增 HTTP 端点 | agent 改 `server/` + `auth/`，另一 agent 可同时改前端 `web/` |
| 修改 Hub actor（realtime/） | **单独一个 agent**，Hub 是核心耦合点 |
| 协议变更（messages.go） | **单独一个 agent**，改完同步前端和后端消费方 |
| 纯前端（auth 卡片、外观编辑器） | 独立 agent，`web/` 范围内改完 |

### 禁止并行的情况

- 两个 agent 同时改 `Hub.Run()` select loop
- 两个 agent 同时改同一个文件的同一区域
- 一个 agent 改协议定义，另一个 agent 同时改协议的消费方——协议 agent 先完成再分配消费方 agent
- 涉及 `web/` 的 agent 同时改动后端路由——前端依赖后端 API 契约，后端先定

### 测试边界

- `game/` 测试是纯单元测试，不需要数据库/网络
- `realtime/` 测试用 `testClient` 模拟 ClientSender，用 channel 驱动 tick
- `server/` 测试用 `httptest.NewServer`，需要 SQLite 临时文件
- `storage/` 测试需要 SQLite 临时文件
- scale test（`aoi_scale_test.go`）耗时较长，放在单独的 `_test.go` 文件中

### 按任务类型的 Agent 规则

不同任务类型有不同的最佳工作方式，分配 agent 时按类型选择策略。

#### Debug / 排查故障

- 先用 Explore agent 读代码定位，不要上来就改
- 涉及 Hub tick 测试失败时，优先检查并发时序（select 随机性、channel 语义、异步调用）——见 [docs/concurrency-debugging.md](docs/concurrency-debugging.md)
- 复现 → 最小化 → 修复 → 回归测试，不要跳过复现步骤
- 错误归因时先排除时序问题，不要先怀疑业务逻辑

#### 新 Feature 开发

- 先出 spec/plan 文档（`docs/superpowers/specs/` 和 `docs/superpowers/plans/`）
- 按依赖关系自底向上实现：`game/` → `storage/` → `realtime/` → `server/` → `web/`
- 每一层改完跑该层的测试，确认绿了再进下一层
- 协议变更必须先定 `messages.go`，再同步改服务端和前端
- 不要在同一轮同时改协议定义和消费方——协议先提交，消费方后提交

#### 性能优化

- 必须先有 benchmark 数字，优化后对比，禁止凭直觉优化
- AOI benchmark 场景在 `internal/benchmark/aoiworkload/`

#### Bug Fix

- Diff 最小化——只改必要的行，不顺手重构无关代码
- 每个 fix 至少跟一个回归测试
- 如果 bug 涉及 channel/select/goroutine 时序，补充到 `docs/concurrency-debugging.md`

#### 重构

- 先确保测试全绿，再开始重构
- 小步提交，每步可独立 revert
- 重构方向参考 AGENTS.md 编码约定：减少抽象、删除死代码、合并重复逻辑
- Hub struct 字段数 > 30 时考虑按职责打包成子 struct

#### 文档

- 完成一个 phase 后更新 `docs/map-walker-handoff.md`
- README 的 WebSocket 协议节、HTTP API 表必须与实际代码一致
- AGENTS.md 的目录结构和消息类型列表随代码变更同步更新
