# AGENTS.md — map-walker

Go 1.26 项目，服务端权威的 WebSocket 实时移动服务。支持用户账户、会话认证和异步位置持久化。

## 目录结构

```
cmd/map-walker/    — 服务入口、信号处理和优雅关闭
internal/game/     — World、输入状态、移动模拟和增量状态
internal/realtime/ — Hub actor loop、tick 调度、连接、消息协议、持久化接口
internal/server/   — HTTP 路由、静态文件、WebSocket 接入、认证端点
internal/auth/     — 用户注册/登录、会话管理、bcrypt 密码
internal/storage/  — SQLite/MySQL、迁移、用户/会话/位置持久化
web/               — Leaflet 地图、认证卡片、键盘和虚拟摇杆界面
```

- `messages.go` 定义 `input` / `world_snapshot` / `players_delta` 协议
- `game.World` 拥有玩家坐标；客户端只能发送输入状态
- `Hub.Run()` 是唯一 actor loop，按 20 Hz 模拟、10 Hz 增量广播、每 5s 持久化位置
- 用户身份通过 `map_walker_session` cookie（SHA-256 哈希 token）传递到 WebSocket

## 命令

```bash
go test ./...              # 跑所有测试
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
- 删代码 > 加代码
- 没有显式要求的验证/错误处理/fallback 不要加
- 不确定时先问，不要猜
- Hub tick 测试的并发陷阱见 [docs/concurrency-debugging.md](docs/concurrency-debugging.md)
- 数据库迁移文件在 `internal/storage/migrations/`，按序号命名，仅向前迁移
