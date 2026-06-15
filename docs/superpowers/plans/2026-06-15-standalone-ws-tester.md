# Standalone WebSocket Tester 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增独立运行的 WebSocket tester 模块，通过真实 WS 连接 map-walker 进行压测，替代 in-process synthetic client 的网络层。

**Architecture:** `cmd/tester/` 入口 + `internal/tester/` 库。复用 `internal/synthetic`（behavior/placement/naming/provisioner）和 `internal/realtime`（消息类型）。Tester 直连 DB 批量生成 session，然后逐一建 WS 连接。Manager 集中 tick 驱动输入发送（时间分散避免尖峰）。单 Dockerfile multi-stage，docker-compose 新增 tester 服务。

**Tech Stack:** Go 1.26, `github.com/coder/websocket`, 现有 `internal/*` 包

**设计决策（grill 结论）：**
- 复用 `synthetic.Provisioner` 做账户 provisioning
- 跳过 HTTP 登录，直接 DB 插入 session（无 bcrypt）
- 新写 WS 客户端壳，不复用 `realtime.Client`
- Manager 集中 tick + 时间分散（stagger = tickInterval / N）
- 按 RampRate 逐个建联
- 读取纯丢弃，只计消息数和字节数
- Stats 最简：在线数、写失败数
- 不清理 session 残留

---

### Task 1: WS 客户端壳

**文件:** 新建 `internal/tester/client.go`, `internal/tester/client_test.go`

**行为目标:**
- `Client` 结构体持有 `*websocket.Conn`、send channel、done channel、消息/字节/写失败计数器
- `Send(data []byte)` 非阻塞写入 send channel，满则记录失败
- `Close()` 关闭 send channel（触发 writeLoop 退出）
- `readLoop` 循环读取 WS 消息，丢弃内容，累加计数器
- `writeLoop` 从 send channel 取消息写入 WS
- `Run(ctx)` 启动 `writeLoop` goroutine + 同步 `readLoop`
- `WaitDone()` 阻塞等待 done channel 关闭

**验证:**
- 单元测试用 `httptest.NewServer` + `websocket.Accept` 模拟服务端
- 验证 readLoop 正确计数 N 条消息
- 验证 writeLoop 正确发送 JSON 消息到对端

---

### Task 2: Manager 生命周期管理

**文件:** 新建 `internal/tester/manager.go`

**涉及模块:** `internal/synthetic` (Provisioner/Naming/Placement/Behavior), `internal/auth` (NewSessionToken/HashSessionToken/CookieName/SessionDuration), `internal/storage` (DB/CreateSession/LoadSyntheticUsers), `internal/realtime` (InputMessage/MessageTypeInput)

**行为目标:**
- `Config` 含 TargetCount、RampRate、Host、Port、Password
- `Manager` 持有 clients/behaviors/userIDs/sessions 数组（按 accountNumber-1 索引）
- `provision()` — 调 `synthetic.Provisioner.Provision(N, 0, password)` 确保账户存在
- `generateSessions()` — `LoadSyntheticUsers` 拿 userID 列表，为每个 `synthetic_N` 生成随机 token、SHA256 hash、插入 sessions 表（expiresAt = now + SessionDuration）
- `rampUp()` — 按 RampRate 间隔（`time.Second / RampRate`）逐个调 `connectOne`
- `connectOne(accountNumber)` — 用 session token 拼 cookie header，`websocket.Dial` 连 `/ws`，创建 Client 并启动 `go client.Run(ctx)`
- `tickLoop(ctx)` — 100ms ticker，遍历所有 client：调 `Behavior.OnTick()`、`json.Marshal(InputMessage{...})`、`client.Send(data)`，每步间 sleep `tickInterval / N` 摊平负载
- `statsLoop(ctx)` — 每秒 log 在线数、消息速率、写失败数
- `Run()` — 串联 provision → generateSessions → rampUp → tickLoop + statsLoop → 等 SIGINT → shutdown
- `shutdown()` — 逐个 `client.Close()` + `WaitDone()`

**验证:**
- 本地启动 `go run ./cmd/map-walker`，运行 `go run ./cmd/tester -target 10 -ramp-rate 5`
- 确认日志输出 provision → sessions → ramp-up → steady stats → Ctrl+C shutdown
- 确认服务端无 panic

---

### Task 3: CLI 入口

**文件:** 新建 `cmd/tester/main.go`

**行为目标:**
- 6 个 flag：`-target`（必填）、`-ramp-rate`（默认 10）、`-host`（默认 localhost）、`-port`（默认 8080）、`-db-driver`（env fallback）、`-db-dsn`（env fallback）
- `-password` 默认读 `MAP_WALKER_SYNTHETIC_PASSWORD` 环境变量
- `.env` 文件存在则加载
- 构建 `tester.Config`，创建 `Manager`，调 `Run()`

**验证:**
- `go build ./cmd/tester/` 编译通过
- `go run ./cmd/tester --help` 查看参数说明

---

### Task 4: Docker 部署

**文件:** 修改 `Dockerfile`、`docker-compose.yml`

**行为目标:**
- Dockerfile 在 builder stage 末尾增加 `RUN go build -o tester ./cmd/tester`
- 新增 `FROM alpine:3.21 AS tester` stage，COPY tester 二进制，ENTRYPOINT `./tester`
- docker-compose 新增 `tester` 服务：
  - `build.target: tester`
  - `depends_on: mysql (healthy) + map-walker (started)`
  - 继承 `DB_DRIVER`/`DB_DSN`/`MAP_WALKER_SYNTHETIC_PASSWORD` 环境变量
  - 默认 CMD: `-target 100 -host map-walker -port 8080`

**验证:**
- `docker build --target tester -t tester .` 构建成功
- `docker compose up` 三个服务正常启动

---

### 自检

- Spec 覆盖: provision → session → ramp-up → tick → stats → shutdown 全链路
- 无占位符/TBD
- CLI flag、Config 字段、Manager 字段命名一致
