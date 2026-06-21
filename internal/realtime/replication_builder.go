package realtime

import "map-walker/internal/game"

// ReplicationBuildInput 包含 actor 已收集的值，builder 只读不修改。
type ReplicationBuildInput struct {
	Tick                uint64
	MovedIDs            []int64
	OldNeighborsByMover map[int64]map[int64]struct{}
	PendingEntered      map[int64]game.PlayerState
	PendingLeft         map[int64][]int64
	PendingAppearances  map[int64]game.Appearance
	CollectEntered      map[int64][]CollectibleEnteredItem
	CollectLeft         map[int64][]uint64
	CollectSpawned      map[int64][]CollectibleSpawnedItem
	CollectCollected    map[int64][]uint64
}

// ReplicationBuildReader 仅在 Build 调用期间有效，builder 不得存储它。
type ReplicationBuildReader interface {
	Connected(playerID int64) bool
	Client(playerID int64) (ClientSender, bool)
	VisibleNeighbors(playerID int64) []int64
	PlayerPosition(playerID int64) (game.PlayerPosition, bool)
}

// ReplicationBuilder 同步构建 replicationJob，不编码、不发送、不修改 Hub 状态。
type ReplicationBuilder struct{}

// hubReader 将 Hub 当前字段适配为 ReplicationBuildReader，仅在 broadcastReplication 内同步使用。
type hubReader struct {
	clients map[int64]ClientSender
	aoi     *game.AOIIndex
	world   *game.World
}

func (r *hubReader) Connected(playerID int64) bool {
	_, ok := r.clients[playerID]
	return ok
}

func (r *hubReader) Client(playerID int64) (ClientSender, bool) {
	c, ok := r.clients[playerID]
	return c, ok
}

func (r *hubReader) VisibleNeighbors(playerID int64) []int64 {
	return r.aoi.VisibleNeighbors(playerID)
}

func (r *hubReader) PlayerPosition(playerID int64) (game.PlayerPosition, bool) {
	return r.world.PlayerPosition(playerID)
}

// concreteReader 用于 benchmark 对比，避免 interface 调用开销。
// 与 hubReader 实现相同逻辑，但 benchmark 可作为具体类型传入。
type concreteReader struct {
	clients map[int64]ClientSender
	aoi     *game.AOIIndex
	world   *game.World
}

func (r *concreteReader) Connected(playerID int64) bool {
	_, ok := r.clients[playerID]
	return ok
}

func (r *concreteReader) Client(playerID int64) (ClientSender, bool) {
	c, ok := r.clients[playerID]
	return c, ok
}

func (r *concreteReader) VisibleNeighbors(playerID int64) []int64 {
	return r.aoi.VisibleNeighbors(playerID)
}

func (r *concreteReader) PlayerPosition(playerID int64) (game.PlayerPosition, bool) {
	return r.world.PlayerPosition(playerID)
}

// 编译期接口满足检查
var (
	_ ReplicationBuildReader = (*hubReader)(nil)
	_ ReplicationBuildReader = (*concreteReader)(nil)
)
