# AGENTS.md — map-walker

Go 1.26 项目，服务端权威的 WebSocket 实时移动服务。

## 目录结构

```
cmd/map-walker/    — 服务入口
internal/game/     — World、输入状态、移动模拟和增量状态
internal/realtime/ — Hub actor loop、tick 调度、连接和消息协议
internal/server/   — HTTP 路由、静态文件和 WebSocket 接入
web/               — Leaflet 地图、键盘和虚拟摇杆界面
```

- `messages.go` 定义 `input` / `world_snapshot` / `players_delta` 协议
- `game.World` 拥有玩家坐标；客户端只能发送输入状态
- `Hub.Run()` 是唯一 actor loop，按 20 Hz 模拟、10 Hz 增量广播

## 命令

```bash
go test ./...              # 跑所有测试
go run ./cmd/map-walker    # 启动服务，访问 http://localhost:8080
```

## 编码约定

- 不做提前抽象，一个函数一个职责
- Hub actor 模式下，状态修改集中在 `Run()` loop 里，不要在其他地方加锁
- 删代码 > 加代码
- 没有显式要求的验证/错误处理/fallback 不要加
- 不确定时先问，不要猜
- Hub tick 测试的并发陷阱见 [docs/concurrency-debugging.md](docs/concurrency-debugging.md)
