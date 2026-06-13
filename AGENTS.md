# AGENTS.md — map-walker

Go 1.26 项目，服务端权威的 WebSocket 实时移动服务。支持用户账户、会话认证、位置持久化、外观同步、AOI 空间索引和按可见性复制。

## 目录结构

```
cmd/map-walker/    — 服务入口、信号处理和优雅关闭
internal/game/     — World、输入状态、移动模拟、外观、AOI 空间索引
internal/realtime/ — Hub actor loop、tick 调度、连接、消息协议、复制、持久化接口
internal/server/   — HTTP 路由、静态文件、WebSocket 接入、认证/外观端点
internal/auth/     — 用户注册/登录、会话管理、外观校验、bcrypt 密码
internal/storage/  — SQLite/MySQL、迁移、用户/会话/位置/外观持久化
internal/storage/migrations/ — 按序号命名的向前 SQL 迁移文件
web/               — Leaflet 地图、认证卡片、账户菜单、外观编辑器、虚拟摇杆
```

- `messages.go` 定义 `input` / `self_state` / `visible_entities_snapshot` / `replication_update` / `appearance_changed` 协议
- `game.World` 拥有玩家坐标、外观、moved/removed 集合；客户端只能发送输入状态
- `game.AOIIndex` 600m 网格空间索引，500m 进入/600m 离开迟滞，九格候选扫描
- `Hub.Run()` 是唯一 actor loop，按 20 Hz 模拟、10 Hz AOI 复制、每 5s 持久化位置
- 用户身份通过 `map_walker_session` cookie（SHA-256 哈希 token）传递到 WebSocket

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
```

## 编码约定

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
