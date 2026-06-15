# Map Walker

[English](README.md)

基于 Go 和 Leaflet 的服务端权威多人游戏 Demo。玩家注册账号后在共享世界中移动，
拾取金色收集品获得永久分数，通过在线排行榜竞争排名。Go 服务端独占所有状态——
位置、收集品、分数。移动以 20 Hz 模拟，AOI 过滤复制以 10 Hz 广播，位置每 5 秒
持久化。后端使用 MySQL。

## 快速开始

```bash
go run ./cmd/map-walker
# 打开 http://localhost:8080 — 注册或登录，开始移动
```

命令行参数和环境变量：

```bash
go run ./cmd/map-walker -host 127.0.0.1 -port 3000
go run ./cmd/map-walker -db-driver mysql -db-dsn 'user:pass@tcp(localhost:3306)/mapwalker'
go run ./cmd/map-walker -collectible-regions my-regions.json
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-host` | `0.0.0.0` | 监听地址 |
| `-port` | `8080` | 监听端口 |
| `-db-driver` | `mysql`（或 `$DB_DRIVER`） | 数据库驱动（`sqlite` / `mysql`） |
| `-db-dsn` | 无默认值（`$DB_DSN`） | 数据库 DSN |
| `-collectible-regions` | `config/collectible-regions.json` | 收集品区域配置文件 |
| `-synthetic-clients` | `0` | 合成客户端数量（0 = 禁用） |
| `-synthetic-ramp-rate` | `10` | 合成客户端每秒激活速率 |
| `-synthetic-auto-provision` | `false` | 自动注册合成客户端账号 |

环境变量（优先级低于命令行参数）：

| 变量 | 说明 |
|------|------|
| `DB_DRIVER` | `-db-driver` 的默认值 |
| `DB_DSN` | `-db-dsn` 的默认值 |
| `MAP_WALKER_SYNTHETIC_PASSWORD` | 合成客户端自动注册时的密码 |

首次运行自动创建数据库表结构。按 **Ctrl+C** 优雅关闭——所有在线玩家的位置会
在退出前保存。

打开两个浏览器窗口，用不同账号登录即可看到多人效果（同一账号两个窗口可用于
重连测试）。

### 合成客户端

预先创建的机器人账号，通过 WebSocket 连接后在地图上漫游，无需真实用户即可
大规模测试 AOI 和复制：

```bash
# 50 个机器人，以每秒 10 个的速率上线（使用数据库中已有的账号）
go run ./cmd/map-walker -synthetic-clients 50 -synthetic-ramp-rate 10

# 50 个机器人，自动创建账号
MAP_WALKER_SYNTHETIC_PASSWORD=secret go run ./cmd/map-walker \
  -synthetic-clients 50 -synthetic-auto-provision
```

### 管理页面

只读运维面板，展示 Hub 和合成客户端的实时指标，位于 `/stats`：

```bash
go run ./cmd/map-walker -synthetic-clients 50
# 打开 http://localhost:8080/stats
```

页面每秒钟轮询一次 `/api/stats/synthetic`。

## Docker 部署

```bash
# 构建并启动（含 MySQL）
./build.sh

# 或手动
docker compose up -d
```

`docker-compose.yml` 启动三个服务：`map-walker`（Go 应用）、`tester`（负载测试）
和 `mysql`（MySQL 8.0）。数据库凭据通过环境变量配置：

```bash
MYSQL_ROOT_PASSWORD=secret MYSQL_PASSWORD=secret DB_DSN=mapwalker:secret@tcp(mysql:3306)/mapwalkerdb docker compose up -d
```

## 收集品玩法

地图上展示半透明金色圆形区域。每个区域内散布着发光金色收集品。走到收集品
10 米范围内——最近的会自动高亮——按 `J` 键（桌面端）或点击右下角圆形拾取按钮
（触屏）即可收集。

每次成功拾取获得**永久性的 1 分**。服务端验证每次拾取：收集品必须存在、可见，
且权威距离 ≤10 米。分数异步持久化不阻塞游戏；断开连接或关闭时最终分数同步提交。

被拾取的道具在 5-15 秒内随机重生在同一区域。收集品实例仅存在于内存中——重启
服务器会生成新道具，同时保留玩家分数。

### 分数与排行榜

分数显示在连接状态栏和地图之间。点击**排行**按钮查看当前在线 Top 5 及自己的
排名。排行榜按需排名——无轮询、无缓存、无推送。合成（机器人）账号不计入排名。

### 外观

注册后会自动弹出欢迎弹窗，引导你选择标记形状和颜色。之后可随时通过右上角
账户菜单更改外观。

### 操作方式

- **WASD / 方向键** — 移动
- **J** — 拾取最近的收集品
- 移动端支持触屏摇杆和拾取按钮

### 合成客户端排除

合成客户端在地图上可见、可同世界移动，但不能拾取道具、不计入排行榜。
`is_synthetic` 标记是创建账号时设置的服务端受信持久属性。

## 架构

```text
cmd/map-walker/      — 入口、优雅关闭
internal/server/     — 路由、静态文件、WebSocket 升级、认证/外观接口
internal/realtime/   — 连接生命周期、Actor 循环、定时器、协议、持久化、复制
internal/game/       — 权威世界状态、移动规则、AOI 空间索引、外观
internal/auth/       — 用户注册/登录、会话令牌、bcrypt
internal/storage/    — MySQL、数据库迁移、用户/会话/位置/外观持久化
internal/synthetic/  — 机器人管理、自动注册、行为、WebSocket 客户端
internal/tester/     — 独立 WebSocket 负载测试工具
web/                 — Leaflet/高德地图前端、认证卡片、账户菜单、键盘+虚拟摇杆
```

### 身份流程

用户注册或登录 → 服务端设置 `map_walker_session` Cookie（HttpOnly，30 天有效，
数据库中 SHA-256 哈希存储）→ WebSocket 升级时从 Cookie 认证 → Hub 使用认证
后的用户 ID 作为玩家 ID。退出登录时断开 WebSocket、保存最终位置、撤销会话、
清除 Cookie。

### 位置持久化

Hub 每 5 秒将发生移动的玩家提交给后台 `PersistenceWorker`，由独立 goroutine
写入数据库——模拟和广播永不阻塞。Worker 对每个用户的更新折叠保留最高序列号，
然后以 500 行一批通过 `UPDATE ... JOIN` 批量写入，每批独立事务。真实断线和退出
会触发同步的最终保存。重连时恢复已保存的位置。同账号顶替（如页面刷新）保留
内存中的位置。

### 兴趣区域（AOI）

基于 600m 网格单元的空间索引，采用 500m 进入 / 600m 离开磁滞避免边界闪烁。
每个玩家仅接收可见邻居的更新——10 Hz 的复制开销随可见玩家数而非总人口数增长。
移动时仅检查邻近 9 个网格单元，而非全部玩家。

## WebSocket 协议

客户端 → 服务端：

```json
{"type":"input","sequence":42,"up":true,"down":false,"left":false,"right":true}
{"type":"collect","collectibleId":123}
```

服务端 → 新连接客户端（按顺序，4 条消息）：

```json
{"type":"self_state","tick":1280,"player":{...},"score":42}
{"type":"visible_entities_snapshot","tick":1280,"players":[...]}
{"type":"collectible_regions","tick":1280,"regions":[{"id":"region-1","centerLat":31.2304,"centerLng":121.4737,"radiusMeters":200}]}
{"type":"visible_collectibles_snapshot","tick":1280,"collectibles":[{"id":1,"regionId":"region-1","lat":31.2305,"lng":121.4738}]}
```

服务端 → 已连接客户端（10 Hz，按客户端）：

```json
{"type":"replication_update","tick":1282,"positions":[...],"entered":[...],"leftPlayerIds":[...],"appearances":[...],"collectiblesEntered":[...],"collectibleIdsLeft":[...],"collectiblesSpawned":[...],"collectibleIdsCollected":[...]}
```

服务端 → 拾取成功者：

```json
{"type":"collect_result","collectibleId":123,"score":43}
```

玩家 ID 为数据库 BIGINT 用户 ID——服务端忽略客户端提交的玩家 ID、位置、分数和
合成身份。

## HTTP API

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| `POST` | `/api/register` | 无 | 注册并获取会话 Cookie |
| `POST` | `/api/login` | 无 | 登录并获取会话 Cookie |
| `POST` | `/api/logout` | 无 | 撤销会话，清除 Cookie |
| `GET` | `/api/session` | 无 | 返回当前用户或 401 |
| `PUT` | `/api/appearance` | 会话 | 更新标记颜色/形状 |
| `GET` | `/api/leaderboard/online` | 会话 | 在线 Top 5 + 自身排名 |
| `GET` | `/ws` | 会话 | WebSocket 升级 |
| `GET` | `/healthz` | 无 | 健康检查 |
| `GET` | `/stats` | — | 统计面板 |
| `GET` | `/api/stats/synthetic` | — | Hub + 合成客户端指标 JSON |

## 运行测试

```bash
go test ./...
```
