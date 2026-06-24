package realtime

import (
	"log"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"map-walker/internal/game"
)

const (
	simulationInterval  = 50 * time.Millisecond
	broadcastInterval   = 100 * time.Millisecond
	persistenceInterval = 5 * time.Second
	statsInterval       = time.Second
)

type ClientSender interface {
	ID() int64
	Username() string
	Send([]byte) bool
	CloseSend()
}

type disconnectRequest struct {
	userID int64
	done   chan struct{}
}

type inputEvent struct {
	client ClientSender
	input  game.InputState
}

type collectEvent struct {
	client        ClientSender
	collectibleID uint64
}

// LeaderboardEntry 排行榜条目（在线玩家）
type LeaderboardEntry struct {
	PlayerID int64  `json:"playerId,omitempty"`
	Username string `json:"username,omitempty"`
	Score    int64  `json:"score"`
	Rank     int    `json:"rank,omitempty"`
}

// LeaderboardResponse 排行榜查询响应
type LeaderboardResponse struct {
	Top  []LeaderboardEntry `json:"top"`
	Self *LeaderboardEntry  `json:"self,omitempty"`
}

type leaderboardRequest struct {
	requesterID int64
	reply       chan LeaderboardResponse
}

type appearanceUpdateRequest struct {
	userID     int64
	appearance game.Appearance
	done       chan struct{}
}

type Hub struct {
	world *game.World
	aoi   *game.AOIIndex

	channels     hubChannels
	players      hubPlayers
	persistence  hubPersistence
	replication  hubReplication
	collectibles hubCollectibles
	clock        hubClock
	stats        hubStatsState

	stopOnce sync.Once
}

type hubChannels struct {
	register          chan ClientSender
	unregister        chan ClientSender
	inputs            chan inputEvent
	appearanceUpdates chan appearanceUpdateRequest
	collects          chan collectEvent
	leaderboards      chan leaderboardRequest
	disconnectUser    chan disconnectRequest
	stop              chan struct{}
	done              chan struct{}
}

type hubPlayers struct {
	clients      map[int64]ClientSender
	scores       map[int64]int64
	syntheticIDs map[int64]struct{}
}

type hubPersistence struct {
	loadSavedPlayer SavedPlayerLoader
	positions       PositionPersister
	dirty           map[int64]struct{}
	seq             map[int64]uint64
}

type hubReplication struct {
	dispatcher         *ReplicationDispatcher
	pendingEntered     map[int64]game.PlayerState
	pendingLeft        map[int64][]int64
	pendingAppearances map[int64]game.Appearance
}

type hubCollectibles struct {
	field            *game.CollectibleField
	scorePersister   ScorePersister
	visibleIDs       map[int64]map[uint64]struct{}
	pendingEntered   map[int64][]CollectibleEnteredItem
	pendingLeftIDs   map[int64][]uint64
	pendingSpawned   map[int64][]CollectibleSpawnedItem
	pendingCollected map[int64][]uint64
	cooldowns        map[int64]time.Time
}

type hubClock struct {
	simulationTick  <-chan time.Time
	broadcastTick   <-chan time.Time
	persistenceTick <-chan time.Time
	statsTick       <-chan time.Time
	stopTickers     func()
}

type hubStatsState struct {
	interval intervalStats
	snapshot atomic.Pointer[HubSnapshot]
}

// Leaderboard 同步查询在线排行榜（阻塞等待 Hub actor 响应）
func (h *Hub) Leaderboard(requesterID int64) LeaderboardResponse {
	reply := make(chan LeaderboardResponse, 1)
	select {
	case h.channels.leaderboards <- leaderboardRequest{requesterID: requesterID, reply: reply}:
		return <-reply
	case <-h.channels.done:
		return LeaderboardResponse{}
	}
}

// SubmitCollect 提交拾取意图到 Hub actor（非阻塞）
func (h *Hub) SubmitCollect(client ClientSender, collectibleID uint64) {
	select {
	case h.channels.collects <- collectEvent{client: client, collectibleID: collectibleID}:
	default:
	}
}

func (h *Hub) Snapshot() *HubSnapshot {
	return h.stats.snapshot.Load()
}

type intervalStats struct {
	acceptedInputs            uint64
	simulationTicks           uint64
	movedPlayers              uint64
	aoiCandidatePairs         uint64
	aoiDistanceChecks         uint64
	aoiFullEnterScans         uint64
	aoiSkippedEnterScans      uint64
	aoiLeaveChecks            uint64
	aoiStableRelationships    uint64
	aoiRelationshipsEntered   uint64
	aoiRelationshipsLeft      uint64
	replicationMessages       uint64
	replicationRecipients     uint64
	replicationBytes          uint64
	builderCalls              uint64
	builderJobs               uint64
	builderRecipients         uint64
	builderAccumDuration      int64 // nanoseconds
	builderCopyDuration       int64 // nanoseconds
	builderTotalDuration      int64 // nanoseconds
	aoiDetailedMoveDuration   int64 // nanoseconds
	collectibleRecalcDuration int64 // nanoseconds
}

func NewHub() *Hub {
	return NewHubWithSavedPositions(nil, nil, nil, nil)
}

func NewHubWithSavedPositions(loader SavedPlayerLoader, persister PositionPersister, collectibleField *game.CollectibleField, scorePersister ScorePersister) *Hub {
	simulationTicker := time.NewTicker(simulationInterval)
	broadcastTicker := time.NewTicker(broadcastInterval)
	persistenceTicker := time.NewTicker(persistenceInterval)
	statsTicker := time.NewTicker(statsInterval)

	return newHub(
		game.NewWorld(game.DefaultConfig()),
		loader,
		persister,
		collectibleField,
		scorePersister,
		simulationTicker.C,
		broadcastTicker.C,
		persistenceTicker.C,
		statsTicker.C,
		func() {
			simulationTicker.Stop()
			broadcastTicker.Stop()
			persistenceTicker.Stop()
			statsTicker.Stop()
		},
	)
}

func newHub(
	world *game.World,
	loadSavedPlayer SavedPlayerLoader,
	persister PositionPersister,
	collectibleField *game.CollectibleField,
	scorePersister ScorePersister,
	simulationTick <-chan time.Time,
	broadcastTick <-chan time.Time,
	persistenceTick <-chan time.Time,
	statsTick <-chan time.Time,
	stopTickers func(),
) *Hub {
	h := &Hub{
		world: world,
		aoi:   game.NewAOIIndex(game.AOIConfigFromWorld(world.Config())),
		channels: hubChannels{
			register:          make(chan ClientSender),
			unregister:        make(chan ClientSender),
			inputs:            make(chan inputEvent),
			appearanceUpdates: make(chan appearanceUpdateRequest),
			collects:          make(chan collectEvent),
			leaderboards:      make(chan leaderboardRequest),
			disconnectUser:    make(chan disconnectRequest),
			stop:              make(chan struct{}),
			done:              make(chan struct{}),
		},
		players: hubPlayers{
			clients:      map[int64]ClientSender{},
			scores:       map[int64]int64{},
			syntheticIDs: map[int64]struct{}{},
		},
		persistence: hubPersistence{
			loadSavedPlayer: loadSavedPlayer,
			positions:       persister,
			dirty:           map[int64]struct{}{},
			seq:             map[int64]uint64{},
		},
		replication: hubReplication{
			pendingEntered:     map[int64]game.PlayerState{},
			pendingLeft:        map[int64][]int64{},
			pendingAppearances: map[int64]game.Appearance{},
		},
		collectibles: hubCollectibles{
			field:            collectibleField,
			scorePersister:   scorePersister,
			visibleIDs:       map[int64]map[uint64]struct{}{},
			pendingEntered:   map[int64][]CollectibleEnteredItem{},
			pendingLeftIDs:   map[int64][]uint64{},
			pendingSpawned:   map[int64][]CollectibleSpawnedItem{},
			pendingCollected: map[int64][]uint64{},
			cooldowns:        map[int64]time.Time{},
		},
		clock: hubClock{
			simulationTick:  simulationTick,
			broadcastTick:   broadcastTick,
			persistenceTick: persistenceTick,
			statsTick:       statsTick,
			stopTickers:     stopTickers,
		},
	}

	// dispatcher 将 per-recipient 编码/发送卸载到 worker goroutine
	n := runtime.GOMAXPROCS(0) / 2
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	h.replication.dispatcher = NewReplicationDispatcher(n, 512, func(recipientID int64) {
		h.DisconnectUser(recipientID)
	})

	return h
}

// Run is the single owner of both connections and authoritative world state.
func (h *Hub) Run() {
	defer close(h.channels.done)
	defer h.clock.stopTickers()

	for {
		select {
		case client := <-h.channels.register:
			h.registerClient(client)
		case client := <-h.channels.unregister:
			h.removeClient(client)
		case event := <-h.channels.inputs:
			if h.players.clients[event.client.ID()] == event.client &&
				h.world.ApplyInput(event.client.ID(), event.input) {
				h.stats.interval.acceptedInputs++
			}
		case <-h.clock.simulationTick:
			moved := h.world.Step(simulationInterval)
			for _, playerID := range moved {
				h.persistence.dirty[playerID] = struct{}{}
			}
			h.stats.interval.simulationTicks++
		case <-h.clock.broadcastTick:
			h.broadcastReplication()
		case <-h.clock.persistenceTick:
			h.persistDirtyPositions()
		case <-h.clock.statsTick:
			h.logStats()
		case req := <-h.channels.disconnectUser:
			if client, ok := h.players.clients[req.userID]; ok {
				h.removeClient(client)
			}
			if d, ok := h.persistence.positions.(PositionDrainer); ok {
				d.Drain()
			}
			close(req.done)
		case req := <-h.channels.appearanceUpdates:
			h.applyAppearanceUpdate(req)
		case evt := <-h.channels.collects:
			h.processCollect(evt)
		case req := <-h.channels.leaderboards:
			req.reply <- h.buildLeaderboard(req.requesterID)
		case <-h.channels.stop:
			h.replication.dispatcher.Stop()
			for _, client := range h.players.clients {
				h.submitFinalPosition(client.ID())
			}
			if d, ok := h.persistence.positions.(PositionDrainer); ok {
				d.Drain()
			}
			if h.collectibles.scorePersister != nil {
				h.collectibles.scorePersister.Drain()
			}
			for _, client := range h.players.clients {
				client.CloseSend()
			}
			return
		}
		if h.collectibles.scorePersister != nil {
			h.collectibles.scorePersister.Drain()
		}
	}
}

func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.channels.stop)
	})
	<-h.channels.done
	if h.persistence.positions != nil {
		h.persistence.positions.Stop()
	}
}

func (h *Hub) DisconnectUser(userID int64) {
	done := make(chan struct{})
	select {
	case h.channels.disconnectUser <- disconnectRequest{userID: userID, done: done}:
		<-done
	case <-h.channels.done:
	}
}

func (h *Hub) Register(client ClientSender) bool {
	select {
	case h.channels.register <- client:
		return true
	case <-h.channels.done:
		return false
	}
}

func (h *Hub) Unregister(client ClientSender) {
	select {
	case h.channels.unregister <- client:
	case <-h.channels.done:
	}
}

func (h *Hub) ApplyInput(client ClientSender, input game.InputState) bool {
	select {
	case h.channels.inputs <- inputEvent{client: client, input: input}:
		return true
	case <-h.channels.done:
		return false
	}
}

func (h *Hub) UpdateAppearance(userID int64, appearance game.Appearance) bool {
	done := make(chan struct{})
	select {
	case h.channels.appearanceUpdates <- appearanceUpdateRequest{
		userID:     userID,
		appearance: appearance,
		done:       done,
	}:
		<-done
		return true
	case <-h.channels.done:
		return false
	}
}

func (h *Hub) registerClient(client ClientSender) {
	if existing, exists := h.players.clients[client.ID()]; exists && existing != client {
		existing.CloseSend()
		h.world.ResetInput(client.ID())
		h.players.clients[client.ID()] = client
		h.clearPendingReplicationFor(client.ID())
		h.sendInitialization(client)
		return
	}

	if !h.world.HasPlayer(client.ID()) {
		h.addPlayer(client.ID(), client.Username())
		h.insertPlayerIntoAOI(client.ID())
		if len(h.players.clients) > 0 {
			if state, ok := h.world.PlayerState(client.ID()); ok {
				h.replication.pendingEntered[client.ID()] = state
				h.clearPendingLeftForPlayer(client.ID())
			}
		}
	}

	h.players.clients[client.ID()] = client
	h.clearPendingReplicationFor(client.ID())
	h.sendInitialization(client)
}

func (h *Hub) insertPlayerIntoAOI(playerID int64) {
	position, ok := h.world.PlayerPosition(playerID)
	if !ok {
		return
	}
	h.aoi.Insert(playerID, position.Lat, position.Lng)
}

func (h *Hub) clearPendingReplicationFor(playerID int64) {
	delete(h.replication.pendingAppearances, playerID)
	delete(h.replication.pendingLeft, playerID)
	delete(h.collectibles.pendingEntered, playerID)
	delete(h.collectibles.pendingLeftIDs, playerID)
	delete(h.collectibles.pendingSpawned, playerID)
	delete(h.collectibles.pendingCollected, playerID)
	delete(h.collectibles.visibleIDs, playerID)
}

func (h *Hub) clearPendingLeftForPlayer(playerID int64) {
	for clientID, leftIDs := range h.replication.pendingLeft {
		filtered := leftIDs[:0]
		for _, id := range leftIDs {
			if id != playerID {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(h.replication.pendingLeft, clientID)
		} else {
			h.replication.pendingLeft[clientID] = filtered
		}
	}
}

func (h *Hub) isVisibleTo(clientID, playerID int64) bool {
	return h.aoi.IsVisible(clientID, playerID)
}

func (h *Hub) addPlayer(userID int64, username string) {
	if h.persistence.loadSavedPlayer != nil {
		if state, ok := h.persistence.loadSavedPlayer(userID); ok {
			lat, lng := state.Lat, state.Lng
			if !state.HasPosition {
				lat, lng = h.world.SpawnLatLng()
			}
			playerUsername := state.Username
			if playerUsername == "" {
				playerUsername = username
			}
			h.world.AddPlayerWithState(userID, playerUsername, lat, lng, state.Appearance)
			// 维护分数和合成身份
			if h.collectibles.field != nil {
				h.players.scores[userID] = state.Score
			}
			if state.IsSynthetic {
				h.players.syntheticIDs[userID] = struct{}{}
			}
			return
		}
	}
	lat, lng := h.world.SpawnLatLng()
	h.world.AddPlayerWithState(userID, username, lat, lng, game.DefaultAppearance())
}

func (h *Hub) applyAppearanceUpdate(req appearanceUpdateRequest) {
	defer close(req.done)

	if !h.world.HasPlayer(req.userID) {
		return
	}

	changed, ok := h.world.UpdatePlayerAppearance(req.userID, req.appearance)
	if !ok || !changed {
		return
	}

	h.replication.pendingAppearances[req.userID] = req.appearance
}

func (h *Hub) removeClient(client ClientSender) {
	current, exists := h.players.clients[client.ID()]
	if !exists || current != client {
		return
	}

	delete(h.players.clients, client.ID())
	h.submitFinalPosition(client.ID())

	// 提交最新分数（同步）
	if h.collectibles.scorePersister != nil {
		if score, ok := h.players.scores[client.ID()]; ok {
			h.collectibles.scorePersister.SubmitSync(client.ID(), score)
		}
	}

	changes := h.aoi.Remove(client.ID())
	for _, neighborID := range changes.Left {
		if _, connected := h.players.clients[neighborID]; connected {
			h.replication.pendingLeft[neighborID] = append(h.replication.pendingLeft[neighborID], client.ID())
		}
	}

	h.world.RemovePlayer(client.ID())
	delete(h.replication.pendingEntered, client.ID())
	h.clearPendingReplicationFor(client.ID())
	delete(h.persistence.dirty, client.ID())
	delete(h.persistence.seq, client.ID())
	client.CloseSend()
}

func (h *Hub) persistDirtyPositions() {
	if h.persistence.positions == nil || len(h.persistence.dirty) == 0 {
		return
	}

	updates := make([]PositionUpdate, 0, len(h.persistence.dirty))
	for userID := range h.persistence.dirty {
		position, ok := h.world.PlayerPosition(userID)
		if !ok {
			continue
		}
		h.persistence.seq[userID]++
		updates = append(updates, PositionUpdate{
			UserID: userID,
			Lat:    position.Lat,
			Lng:    position.Lng,
			Seq:    h.persistence.seq[userID],
		})
	}
	clear(h.persistence.dirty)
	h.persistence.positions.Submit(updates)
}

func (h *Hub) submitFinalPosition(userID int64) {
	if h.persistence.positions == nil {
		return
	}
	position, ok := h.world.PlayerPosition(userID)
	if !ok {
		return
	}
	h.persistence.seq[userID]++
	update := []PositionUpdate{{
		UserID: userID,
		Lat:    position.Lat,
		Lng:    position.Lng,
		Seq:    h.persistence.seq[userID],
	}}
	if syncSub, ok := h.persistence.positions.(interface{ SubmitSync([]PositionUpdate) }); ok {
		syncSub.SubmitSync(update)
	} else {
		h.persistence.positions.Submit(update)
	}
}

func (h *Hub) sendInitialization(client ClientSender) {
	tick := h.world.Tick()
	self, ok := h.world.PlayerState(client.ID())
	if !ok {
		h.removeClient(client)
		return
	}

	score := h.playerScore(client.ID())
	selfData, err := EncodeSelfState(tick, self, score)
	if err != nil {
		log.Printf("encode self state failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(selfData); !ok {
		h.removeClient(client)
		return
	}

	visibleIDs := h.aoi.VisibleNeighbors(client.ID())
	visibleData, err := EncodeVisibleEntitiesSnapshot(tick, h.world.PlayerStates(visibleIDs))
	if err != nil {
		log.Printf("encode visible entities snapshot failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(visibleData); !ok {
		h.removeClient(client)
		return
	}

	// 发送收集品初始化消息（始终发送，无收集品时发送空消息以保证协议一致性）
	var regions []game.CollectibleRegion
	var visibleCollectibles []game.Collectible
	if h.collectibles.field != nil {
		regions = h.collectibles.field.Regions()
		position, _ := h.world.PlayerPosition(client.ID())
		visibleCollectibles = h.collectibles.field.CollectiblesWithinRadius(position.Lat, position.Lng, 500)
	}

	regionsData, err := EncodeCollectibleRegions(tick, regions)
	if err != nil {
		log.Printf("encode collectible regions failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(regionsData); !ok {
		h.removeClient(client)
		return
	}

	collectibleData, err := EncodeVisibleCollectiblesSnapshot(tick, visibleCollectibles)
	if err != nil {
		log.Printf("encode visible collectibles snapshot failed: %v", err)
		h.removeClient(client)
		return
	}
	if ok := client.Send(collectibleData); !ok {
		h.removeClient(client)
		return
	}

	// 跟踪初始可见收集品 ID
	if h.collectibles.field != nil && len(visibleCollectibles) > 0 {
		visible := make(map[uint64]struct{}, len(visibleCollectibles))
		for _, c := range visibleCollectibles {
			visible[c.ID] = struct{}{}
		}
		h.collectibles.visibleIDs[client.ID()] = visible
	}
}

const collectCooldown = 300 * time.Millisecond

// buildLeaderboard 构建在线排行榜（在 Hub actor 内调用，无需额外同步）
func (h *Hub) buildLeaderboard(requesterID int64) LeaderboardResponse {
	type entry struct {
		playerID int64
		username string
		score    int64
	}
	entries := make([]entry, 0, len(h.players.clients))
	for playerID := range h.players.clients {
		if _, synthetic := h.players.syntheticIDs[playerID]; synthetic {
			continue
		}
		score := h.players.scores[playerID]
		username := ""
		if state, ok := h.world.PlayerState(playerID); ok {
			username = state.Username
		}
		entries = append(entries, entry{playerID: playerID, username: username, score: score})
	}

	// 按分数降序、playerID 升序
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return entries[i].playerID < entries[j].playerID
	})

	top := make([]LeaderboardEntry, 0, 5)
	for i, e := range entries {
		if i >= 5 {
			break
		}
		top = append(top, LeaderboardEntry{PlayerID: e.playerID, Username: e.username, Score: e.score})
	}

	var self *LeaderboardEntry
	for i, e := range entries {
		if e.playerID == requesterID {
			self = &LeaderboardEntry{PlayerID: e.playerID, Username: e.username, Score: e.score, Rank: i + 1}
			break
		}
	}

	return LeaderboardResponse{Top: top, Self: self}
}

// processCollect 在 Hub actor 内串行处理拾取请求
func (h *Hub) processCollect(evt collectEvent) {
	client := evt.client
	playerID := client.ID()

	// 连接必须仍然是当前连接
	if h.players.clients[playerID] != client {
		return
	}

	// 合成账户不能拾取
	if _, synthetic := h.players.syntheticIDs[playerID]; synthetic {
		return
	}

	// 服务端冷却
	now := time.Now()
	if last, ok := h.collectibles.cooldowns[playerID]; ok && now.Sub(last) < collectCooldown {
		return
	}
	h.collectibles.cooldowns[playerID] = now

	// 收集品必须存在且对玩家可见
	if h.collectibles.field == nil {
		return
	}
	collectible, ok := h.collectibles.field.Collectible(evt.collectibleID)
	if !ok {
		return
	}
	visibleIDs := h.collectibles.visibleIDs[playerID]
	if _, visible := visibleIDs[collectible.ID]; !visible {
		return
	}

	// 权威距离检查（10 米内）
	playerPos, ok := h.world.PlayerPosition(playerID)
	if !ok {
		return
	}
	dx, dy := h.collectibleLocalDiff(playerPos.Lat, playerPos.Lng, collectible.Lat, collectible.Lng)
	if dx*dx+dy*dy > 400 { // 20米²
		return
	}

	// 移除收集品并调度替换
	if !h.collectibles.field.Remove(evt.collectibleID) {
		return
	}

	// 增加分数并异步持久化
	h.players.scores[playerID]++
	newScore := h.players.scores[playerID]
	if h.collectibles.scorePersister != nil {
		h.collectibles.scorePersister.Submit(ScoreUpdate{UserID: playerID, Score: newScore})
	}

	// 发送 collect_result 仅给获胜者
	resultData, err := EncodeCollectResult(evt.collectibleID, newScore)
	if err != nil {
		log.Printf("encode collect result failed: %v", err)
		return
	}
	client.Send(resultData)

	// 反向扇出：从可见玩家中移除已被拾取的收集品
	nearbyPlayerIDs := h.aoi.QueryPlayerIDsNearPoint(collectible.Lat, collectible.Lng)
	for _, nearbyID := range nearbyPlayerIDs {
		if _, connected := h.players.clients[nearbyID]; !connected {
			continue
		}
		if vis, ok := h.collectibles.visibleIDs[nearbyID]; ok {
			delete(vis, collectible.ID)
		}
		h.collectibles.pendingCollected[nearbyID] = append(h.collectibles.pendingCollected[nearbyID], evt.collectibleID)
	}
}

// collectibleLocalDiff 计算两点局部坐标差
func (h *Hub) collectibleLocalDiff(lat1, lng1, lat2, lng2 float64) (dx, dy float64) {
	aoiConfig := game.AOIConfigFromWorld(h.world.Config())
	x1, y1 := aoiConfig.LatLngToLocal(lat1, lng1)
	x2, y2 := aoiConfig.LatLngToLocal(lat2, lng2)
	return x1 - x2, y1 - y2
}

// advanceCollectibleReplacements 推进到期替换，反向扇出生成通知
func (h *Hub) advanceCollectibleReplacements() {
	spawned := h.collectibles.field.AdvanceReplacements()
	for _, c := range spawned {
		nearbyPlayerIDs := h.aoi.QueryPlayerIDsNearPoint(c.Lat, c.Lng)
		item := CollectibleSpawnedItem{ID: c.ID, RegionID: c.RegionID, Lat: c.Lat, Lng: c.Lng}
		for _, playerID := range nearbyPlayerIDs {
			if _, connected := h.players.clients[playerID]; !connected {
				continue
			}
			if vis, ok := h.collectibles.visibleIDs[playerID]; ok {
				vis[c.ID] = struct{}{}
			}
			h.collectibles.pendingSpawned[playerID] = append(h.collectibles.pendingSpawned[playerID], item)
		}
	}
}

// recalcCollectibleVisibility 为移动的玩家重新计算收集品可见性
func (h *Hub) recalcCollectibleVisibility(movedIDs []int64) {
	start := time.Now()
	defer func() { h.stats.interval.collectibleRecalcDuration += time.Since(start).Nanoseconds() }()
	for _, playerID := range movedIDs {
		if _, connected := h.players.clients[playerID]; !connected {
			continue
		}
		position, ok := h.world.PlayerPosition(playerID)
		if !ok {
			continue
		}

		within500 := h.collectibles.field.CollectiblesWithinRadius(position.Lat, position.Lng, 500)
		within600Set := make(map[uint64]struct{})
		within600List := h.collectibles.field.CollectiblesWithinRadius(position.Lat, position.Lng, 600)
		for _, c := range within600List {
			within600Set[c.ID] = struct{}{}
		}

		prevVisible := h.collectibles.visibleIDs[playerID]
		if prevVisible == nil {
			prevVisible = map[uint64]struct{}{}
		}

		// 计算进入的（在 500m 内但之前不可见）
		var entered []CollectibleEnteredItem
		newVisible := make(map[uint64]struct{}, len(within500))
		for _, c := range within500 {
			newVisible[c.ID] = struct{}{}
			if _, wasVisible := prevVisible[c.ID]; !wasVisible {
				entered = append(entered, CollectibleEnteredItem{
					ID: c.ID, RegionID: c.RegionID, Lat: c.Lat, Lng: c.Lng,
				})
			}
		}

		// 计算离开的（之前可见但不在 600m 内）
		var leftIDs []uint64
		for id := range prevVisible {
			if _, stillVisible := within600Set[id]; !stillVisible {
				leftIDs = append(leftIDs, id)
			}
		}

		h.collectibles.visibleIDs[playerID] = newVisible

		if len(entered) > 0 {
			h.collectibles.pendingEntered[playerID] = append(h.collectibles.pendingEntered[playerID], entered...)
		}
		if len(leftIDs) > 0 {
			h.collectibles.pendingLeftIDs[playerID] = append(h.collectibles.pendingLeftIDs[playerID], leftIDs...)
		}
	}
}

func (h *Hub) takePendingCollectEntered() map[int64][]CollectibleEnteredItem {
	result := h.collectibles.pendingEntered
	h.collectibles.pendingEntered = map[int64][]CollectibleEnteredItem{}
	return result
}

func (h *Hub) takePendingCollectLeftIDs() map[int64][]uint64 {
	result := h.collectibles.pendingLeftIDs
	h.collectibles.pendingLeftIDs = map[int64][]uint64{}
	return result
}

func (h *Hub) takePendingCollectSpawned() map[int64][]CollectibleSpawnedItem {
	result := h.collectibles.pendingSpawned
	h.collectibles.pendingSpawned = map[int64][]CollectibleSpawnedItem{}
	return result
}

func (h *Hub) takePendingCollectCollected() map[int64][]uint64 {
	result := h.collectibles.pendingCollected
	h.collectibles.pendingCollected = map[int64][]uint64{}
	return result
}

// playerScore 返回玩家当前内存分数
func (h *Hub) playerScore(userID int64) int64 {
	if h.players.scores == nil {
		return 0
	}
	return h.players.scores[userID]
}

func (h *Hub) broadcastReplication() {
	movedIDs := h.world.TakeMovedPlayerIDs()
	h.world.TakeRemovedPlayerIDs()

	moveStart := time.Now()
	movementDeltas := h.applyMovementAOIDeltas(movedIDs)
	h.stats.interval.aoiDetailedMoveDuration += time.Since(moveStart).Nanoseconds()
	h.stats.interval.movedPlayers += uint64(len(movedIDs))

	pendingEntered := h.takePendingEntered()
	pendingLeft := h.takePendingLeft()
	pendingAppearances := h.takePendingAppearances()

	// 推进收集品替换并计算可见性变化
	if h.collectibles.field != nil {
		h.advanceCollectibleReplacements()
		h.recalcCollectibleVisibility(movedIDs)
	}

	collectEntered := h.takePendingCollectEntered()
	collectLeft := h.takePendingCollectLeftIDs()
	collectSpawned := h.takePendingCollectSpawned()
	collectCollected := h.takePendingCollectCollected()

	if len(movedIDs) == 0 && len(pendingEntered) == 0 && len(pendingLeft) == 0 && len(pendingAppearances) == 0 &&
		len(collectEntered) == 0 && len(collectLeft) == 0 && len(collectSpawned) == 0 && len(collectCollected) == 0 {
		return
	}

	tick := h.world.Tick()
	input := ReplicationBuildInput{
		Tick:               tick,
		MovementDeltas:     movementDeltas,
		PendingEntered:     pendingEntered,
		PendingLeft:        pendingLeft,
		PendingAppearances: pendingAppearances,
		CollectEntered:     collectEntered,
		CollectLeft:        collectLeft,
		CollectSpawned:     collectSpawned,
		CollectCollected:   collectCollected,
	}
	reader := &hubReader{clients: h.players.clients, aoi: h.aoi, world: h.world}
	var builder ReplicationBuilder
	jobs := builder.Build(input, reader)
	bs := builder.Stats()
	h.stats.interval.builderCalls++
	h.stats.interval.builderJobs += uint64(bs.Jobs)
	h.stats.interval.builderRecipients += uint64(bs.Recipients)
	h.stats.interval.builderAccumDuration += int64(bs.AccumulationDuration)
	h.stats.interval.builderCopyDuration += int64(bs.CopyDuration)
	h.stats.interval.builderTotalDuration += int64(bs.TotalDuration)

	// 提交 encode/send 到 dispatcher（异步，不阻塞广播 tick）
	for _, job := range jobs {
		h.stats.interval.replicationRecipients++
		h.replication.dispatcher.Submit(job)
	}
}

// applyMovementAOIDeltas 更新 AOI 位置，收集 entered/left event，返回 movement deltas。
func (h *Hub) applyMovementAOIDeltas(movedIDs []int64) []game.MovementDelta {
	deltas := make([]game.MovementDelta, 0, len(movedIDs))
	for _, playerID := range movedIDs {
		position, ok := h.world.PlayerPosition(playerID)
		if !ok {
			continue
		}
		delta := h.aoi.MoveDetailed(playerID, position.Lat, position.Lng)
		state, ok := h.world.PlayerState(playerID)
		if !ok {
			continue
		}
		for _, neighborID := range delta.Entered {
			if _, connected := h.players.clients[neighborID]; !connected {
				continue
			}
			if _, already := h.replication.pendingEntered[playerID]; !already {
				h.replication.pendingEntered[playerID] = state
			}
		}
		for _, neighborID := range delta.Left {
			if _, connected := h.players.clients[neighborID]; !connected {
				continue
			}
			h.replication.pendingLeft[neighborID] = append(h.replication.pendingLeft[neighborID], playerID)
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func (h *Hub) takePendingLeft() map[int64][]int64 {
	if len(h.replication.pendingLeft) == 0 {
		return nil
	}
	left := make(map[int64][]int64, len(h.replication.pendingLeft))
	for clientID, playerIDs := range h.replication.pendingLeft {
		left[clientID] = append([]int64(nil), playerIDs...)
	}
	clear(h.replication.pendingLeft)
	return left
}

func (h *Hub) takePendingEntered() []game.PlayerState {
	if len(h.replication.pendingEntered) == 0 {
		return nil
	}
	states := make([]game.PlayerState, 0, len(h.replication.pendingEntered))
	for playerID := range h.replication.pendingEntered {
		if state, ok := h.world.PlayerState(playerID); ok {
			states = append(states, state)
		}
	}
	clear(h.replication.pendingEntered)
	return states
}

func (h *Hub) takePendingAppearances() map[int64]game.Appearance {
	if len(h.replication.pendingAppearances) == 0 {
		return nil
	}
	appearances := make(map[int64]game.Appearance, len(h.replication.pendingAppearances))
	for playerID, appearance := range h.replication.pendingAppearances {
		appearances[playerID] = appearance
	}
	clear(h.replication.pendingAppearances)
	return appearances
}

func (h *Hub) logStats() {
	aoiStats := h.aoi.TakeStats()
	h.stats.interval.aoiCandidatePairs += aoiStats.CandidatePairs
	h.stats.interval.aoiDistanceChecks += aoiStats.DistanceChecks
	h.stats.interval.aoiRelationshipsEntered += aoiStats.RelationshipsEntered
	h.stats.interval.aoiRelationshipsLeft += aoiStats.RelationshipsLeft
	h.stats.interval.aoiFullEnterScans += aoiStats.FullEnterScans
	h.stats.interval.aoiSkippedEnterScans += aoiStats.SkippedEnterScans
	h.stats.interval.aoiLeaveChecks += aoiStats.LeaveChecks
	h.stats.interval.aoiStableRelationships += aoiStats.StableRelationships

	// 派生 enter scan skip rate
	var enterScanSkipRate float64
	total := h.stats.interval.aoiFullEnterScans + h.stats.interval.aoiSkippedEnterScans
	if total > 0 {
		enterScanSkipRate = float64(h.stats.interval.aoiSkippedEnterScans) / float64(total)
	}

	snap := &HubSnapshot{
		ConnectedClients:       len(h.players.clients),
		AcceptedInputs:         h.stats.interval.acceptedInputs,
		SimulationTicks:        h.stats.interval.simulationTicks,
		MovedPlayers:           h.stats.interval.movedPlayers,
		AOICandidatePairs:      h.stats.interval.aoiCandidatePairs,
		AOIDistanceChecks:      h.stats.interval.aoiDistanceChecks,
		AOIFullEnterScans:      h.stats.interval.aoiFullEnterScans,
		AOISkippedEnterScans:   h.stats.interval.aoiSkippedEnterScans,
		AOILeaveChecks:         h.stats.interval.aoiLeaveChecks,
		AOIStableRelationships: h.stats.interval.aoiStableRelationships,
		EnterScanSkipRate:      enterScanSkipRate,
		RelationshipsEntered:   h.stats.interval.aoiRelationshipsEntered,
		RelationshipsLeft:      h.stats.interval.aoiRelationshipsLeft,
		ReplicationMessages:    h.stats.interval.replicationMessages,
		ReplicationRecipients:  h.stats.interval.replicationRecipients,
		ReplicationBytes:       h.stats.interval.replicationBytes,
		Builder: BuilderStats{
			Recipients:           int(h.stats.interval.builderRecipients),
			Jobs:                 int(h.stats.interval.builderJobs),
			AccumulationDuration: time.Duration(h.stats.interval.builderAccumDuration),
			CopyDuration:         time.Duration(h.stats.interval.builderCopyDuration),
			TotalDuration:        time.Duration(h.stats.interval.builderTotalDuration),
		},
		AOIDetailedMoveDuration:   time.Duration(h.stats.interval.aoiDetailedMoveDuration),
		CollectibleRecalcDuration: time.Duration(h.stats.interval.collectibleRecalcDuration),
		SampledAt:                 time.Now(),
	}
	// 从 dispatcher 读取编码/字节统计（替换旧的 actor 内联统计）
	ds := h.replication.dispatcher.Stats()
	snap.Dispatcher = ds
	snap.ReplicationMessages = ds.Encoded
	snap.ReplicationBytes = ds.EncodedBytes

	h.stats.snapshot.Store(snap)

	log.Printf(
		"realtime stats clients=%d inputs=%d simulation_ticks=%d moved_players=%d aoi_candidates=%d aoi_distance_checks=%d aoi_entered=%d aoi_left=%d aoi_full_enter_scans=%d aoi_skipped_enter_scans=%d aoi_leave_checks=%d aoi_stable_relationships=%d aoi_skip_rate=%.4f replication_messages=%d replication_recipients=%d replication_bytes=%d aoi_detailed_move_us=%d collectible_recalc_us=%d dispatched_submitted=%d dispatched_encoded=%d dispatched_skipped=%d dispatched_errors=%d dispatched_dropped=%d dispatched_send_failures=%d dispatched_queued=%d dispatched_workers=%d dispatched_bytes=%d builder_calls=%d builder_recipients=%d builder_jobs=%d builder_accum_us=%d builder_copy_us=%d builder_total_us=%d",
		snap.ConnectedClients,
		snap.AcceptedInputs,
		snap.SimulationTicks,
		snap.MovedPlayers,
		snap.AOICandidatePairs,
		snap.AOIDistanceChecks,
		snap.RelationshipsEntered,
		snap.RelationshipsLeft,
		snap.AOIFullEnterScans,
		snap.AOISkippedEnterScans,
		snap.AOILeaveChecks,
		snap.AOIStableRelationships,
		snap.EnterScanSkipRate,
		snap.ReplicationMessages,
		snap.ReplicationRecipients,
		snap.ReplicationBytes,
		snap.AOIDetailedMoveDuration.Microseconds(),
		snap.CollectibleRecalcDuration.Microseconds(),
		ds.Submitted,
		ds.Encoded,
		ds.SkippedEmpty,
		ds.EncodeErrors,
		ds.Dropped,
		ds.SendFailures,
		ds.QueueDepth,
		ds.WorkerCount,
		ds.EncodedBytes,
		h.stats.interval.builderCalls,
		h.stats.interval.builderRecipients,
		h.stats.interval.builderJobs,
		snap.Builder.AccumulationDuration.Microseconds(),
		snap.Builder.CopyDuration.Microseconds(),
		snap.Builder.TotalDuration.Microseconds(),
	)
	h.stats.interval = intervalStats{}
}
