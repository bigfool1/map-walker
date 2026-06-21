package realtime

import (
	"time"

	"map-walker/internal/game"
)

// ReplicationBuildInput 包含 actor 已收集的值，builder 只读不修改。
type ReplicationBuildInput struct {
	Tick                uint64
	MovedIDs            []int64
	OldNeighborsByMover map[int64]map[int64]struct{}
	PendingEntered      []game.PlayerState
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
type ReplicationBuilder struct {
	stats BuilderStats
}

// Stats 返回最后一次 Build 调用的指标快照。
func (b *ReplicationBuilder) Stats() BuilderStats { return b.stats }

// Build 从 input 和 reader 构建不可变 replicationJob 切片。
func (b *ReplicationBuilder) Build(input ReplicationBuildInput, reader ReplicationBuildReader) []replicationJob {
	start := time.Now()
	byRecipient := make(map[int64]*ReplicationChanges)

	// 自位置：每个已连接的移动者
	for _, playerID := range input.MovedIDs {
		if !reader.Connected(playerID) {
			continue
		}
		if position, ok := reader.PlayerPosition(playerID); ok {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.SelfPosition = &SelfPosition{Lat: position.Lat, Lng: position.Lng}
		}
	}

	// 稳定关系位置：从移动者扇出到旧邻居（同时仍在最终可见集中）
	for _, moverID := range input.MovedIDs {
		position, ok := reader.PlayerPosition(moverID)
		if !ok {
			continue
		}
		oldNeighbors := input.OldNeighborsByMover[moverID]
		for _, neighborID := range reader.VisibleNeighbors(moverID) {
			if _, inOld := oldNeighbors[neighborID]; !inOld {
				continue
			}
			if !reader.Connected(neighborID) {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Positions = append(entry.Positions, position)
		}
	}

	// 待处理进入：从进入者扇出到当前可见的已连接邻居
	for _, state := range input.PendingEntered {
		for _, neighborID := range reader.VisibleNeighbors(state.ID) {
			if !reader.Connected(neighborID) {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Entered = append(entry.Entered, state)
		}
	}

	// 待处理离开（已按接收者 key 存储）
	for recipientID, leftIDs := range input.PendingLeft {
		if !reader.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.LeftPlayerIDs = append(entry.LeftPlayerIDs, leftIDs...)
	}

	// 待处理外观变更：变更者本人和其可见邻居
	for playerID, appearance := range input.PendingAppearances {
		if reader.Connected(playerID) {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
				PlayerID:   playerID,
				Appearance: appearance,
			})
		}
		for _, neighborID := range reader.VisibleNeighbors(playerID) {
			if reader.Connected(neighborID) {
				entry := getOrCreateRecipient(byRecipient, neighborID)
				entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
					PlayerID:   playerID,
					Appearance: appearance,
				})
			}
		}
	}

	// 收集品进入：按接收者累积
	for recipientID, items := range input.CollectEntered {
		if !reader.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectiblesEntered = append(entry.CollectiblesEntered, items...)
	}

	// 收集品离开：按接收者 key 存储
	for recipientID, ids := range input.CollectLeft {
		if !reader.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectibleIDsLeft = append(entry.CollectibleIDsLeft, ids...)
	}

	// 收集品生成：按接收者累积
	for recipientID, items := range input.CollectSpawned {
		if !reader.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectiblesSpawned = append(entry.CollectiblesSpawned, items...)
	}

	// 收集品被拾取：按接收者累积
	for recipientID, ids := range input.CollectCollected {
		if !reader.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectibleIDsCollected = append(entry.CollectibleIDsCollected, ids...)
	}

	// 转换为不可变 job 切片
	accumDone := time.Now()
	copyStart := time.Now()
	jobs := make([]replicationJob, 0, len(byRecipient))
	for recipientID, changes := range byRecipient {
		client, connected := reader.Client(recipientID)
		if !connected {
			continue
		}
		jobs = append(jobs, replicationJob{
			recipientID: recipientID,
			tick:        input.Tick,
			client:      client,
			changes:     copyReplicationChanges(*changes),
		})
	}
	copyDuration := time.Since(copyStart)

	b.stats = BuilderStats{
		Recipients:           len(byRecipient),
		Jobs:                 len(jobs),
		AccumulationDuration: accumDone.Sub(start),
		CopyDuration:         copyDuration,
		TotalDuration:        time.Since(start),
	}
	return jobs
}

// BuildConcrete 与 Build 相同，但接受 *concreteReader 以避免 interface 派发开销。
// 仅用于 benchmark 对比；生产代码仍使用 Build + ReplicationBuildReader。
func (b *ReplicationBuilder) BuildConcrete(input ReplicationBuildInput, r *concreteReader) []replicationJob {
	start := time.Now()
	byRecipient := make(map[int64]*ReplicationChanges)

	for _, playerID := range input.MovedIDs {
		if !r.Connected(playerID) {
			continue
		}
		if position, ok := r.PlayerPosition(playerID); ok {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.SelfPosition = &SelfPosition{Lat: position.Lat, Lng: position.Lng}
		}
	}

	for _, moverID := range input.MovedIDs {
		position, ok := r.PlayerPosition(moverID)
		if !ok {
			continue
		}
		oldNeighbors := input.OldNeighborsByMover[moverID]
		for _, neighborID := range r.VisibleNeighbors(moverID) {
			if _, inOld := oldNeighbors[neighborID]; !inOld {
				continue
			}
			if !r.Connected(neighborID) {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Positions = append(entry.Positions, position)
		}
	}

	for _, state := range input.PendingEntered {
		for _, neighborID := range r.VisibleNeighbors(state.ID) {
			if !r.Connected(neighborID) {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Entered = append(entry.Entered, state)
		}
	}

	for recipientID, leftIDs := range input.PendingLeft {
		if !r.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.LeftPlayerIDs = append(entry.LeftPlayerIDs, leftIDs...)
	}

	for playerID, appearance := range input.PendingAppearances {
		if r.Connected(playerID) {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
				PlayerID:   playerID,
				Appearance: appearance,
			})
		}
		for _, neighborID := range r.VisibleNeighbors(playerID) {
			if r.Connected(neighborID) {
				entry := getOrCreateRecipient(byRecipient, neighborID)
				entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
					PlayerID:   playerID,
					Appearance: appearance,
				})
			}
		}
	}

	for recipientID, items := range input.CollectEntered {
		if !r.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectiblesEntered = append(entry.CollectiblesEntered, items...)
	}

	for recipientID, ids := range input.CollectLeft {
		if !r.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectibleIDsLeft = append(entry.CollectibleIDsLeft, ids...)
	}

	for recipientID, items := range input.CollectSpawned {
		if !r.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectiblesSpawned = append(entry.CollectiblesSpawned, items...)
	}

	for recipientID, ids := range input.CollectCollected {
		if !r.Connected(recipientID) {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectibleIDsCollected = append(entry.CollectibleIDsCollected, ids...)
	}

	accumDone := time.Now()
	copyStart := time.Now()
	jobs := make([]replicationJob, 0, len(byRecipient))
	for recipientID, changes := range byRecipient {
		client, connected := r.Client(recipientID)
		if !connected {
			continue
		}
		jobs = append(jobs, replicationJob{
			recipientID: recipientID,
			tick:        input.Tick,
			client:      client,
			changes:     copyReplicationChanges(*changes),
		})
	}
	copyDuration := time.Since(copyStart)

	b.stats = BuilderStats{
		Recipients:           len(byRecipient),
		Jobs:                 len(jobs),
		AccumulationDuration: accumDone.Sub(start),
		CopyDuration:         copyDuration,
		TotalDuration:        time.Since(start),
	}
	return jobs
}

// getOrCreateRecipient 在广播本地累积 map 中获取或创建接收者条目。
func getOrCreateRecipient(byRecipient map[int64]*ReplicationChanges, recipientID int64) *ReplicationChanges {
	entry, ok := byRecipient[recipientID]
	if !ok {
		entry = &ReplicationChanges{}
		byRecipient[recipientID] = entry
	}
	return entry
}

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
