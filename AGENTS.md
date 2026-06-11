# AGENTS.md — map-walker

Go 1.26 项目，WebSocket 实时位置共享服务。

## 目录结构

```
cmd/server/       — 入口
internal/game/    — 玩家状态（State, PlayerPosition）
internal/realtime/ — Hub actor 模式，WebSocket 消息定义
```

- `messages.go` 定义消息类型，用 `type` 字段做路由（`position_update` / `players_snapshot`）
- `hub.go` 的 `Hub.Run()` 是唯一的 actor loop，所有状态修改通过 channel 串行化

## 命令

```bash
go test ./...          # 跑所有测试
go run ./cmd/server    # 启动服务
```

## 编码约定

- 不做提前抽象，一个函数一个职责
- Hub actor 模式下，状态修改集中在 `Run()` loop 里，不要在其他地方加锁
- 删代码 > 加代码
- 没有显式要求的验证/错误处理/fallback 不要加
- 不确定时先问，不要猜
