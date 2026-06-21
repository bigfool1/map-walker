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
	world              *game.World
	aoi                *game.AOIIndex
	loadSavedPlayer    SavedPlayerLoader
	persister          PositionPersister
	collectibleField   *game.CollectibleField
	scorePersister     ScorePersister
	playerScores             map[int64]int64
	syntheticPlayerIDs       map[int64]struct{}
	visibleCollectibleIDs    map[int64]map[uint64]struct{}
	pendingCollectEntered    map[int64][]CollectibleEnteredItem
	pendingCollectLeftIDs    map[int64][]uint64
	pendingCollectSpawned    map[int64][]CollectibleSpawnedItem
	pendingCollectCollected  map[int64][]uint64
	collectCooldowns         map[int64]time.Time
	collects                 chan collectEvent
	leaderboards             chan leaderboardRequest
	register                 chan ClientSender
	unregister         chan ClientSender
	inputs             chan inputEvent
	appearanceUpdates  chan appearanceUpdateRequest
	stop               chan struct{}
	done               chan struct{}
	stopOnce           sync.Once
	clients            map[int64]ClientSender
	persistDirty       map[int64]struct{}
	persistSeq         map[int64]uint64
	pendingEntered     map[int64]game.PlayerState
	pendingLeft        map[int64][]int64
	pendingAppearances map[int64]game.Appearance
	disconnectUser     chan disconnectRequest
	dispatcher         *ReplicationDispatcher
	simulationTick     <-chan time.Time
	broadcastTick      <-chan time.Time
	persistenceTick    <-chan time.Time
	statsTick          <-chan time.Time
	stopTickers        func()
	stats              intervalStats
	snapshot           atomic.Pointer[HubSnapshot]
}

// Leaderboard 同步查询在线排行榜（阻塞等待 Hub actor 响应）
func (h *Hub) Leaderboard(requesterID int64) LeaderboardResponse {
	reply := make(chan LeaderboardResponse, 1)
	select {
	case h.leaderboards <- leaderboardRequest{requesterID: requesterID, reply: reply}:
		return <-reply
	case <-h.done:
		return LeaderboardResponse{}
	}
}

// SubmitCollect 提交拾取意图到 Hub actor（非阻塞）
func (h *Hub) SubmitCollect(client ClientSender, collectibleID uint64) {
	select {
	case h.collects <- collectEvent{client: client, collectibleID: collectibleID}:
	default:
	}
}

func (h *Hub) Snapshot() *HubSnapshot {
	return h.snapshot.Load()
}

type intervalStats struct {
	acceptedInputs          uint64
	simulationTicks         uint64
	movedPlayers            uint64
	aoiCandidatePairs       uint64
	aoiDistanceChecks       uint64
	aoiRelationshipsEntered uint64
	aoiRelationshipsLeft    uint64
	replicationMessages     uint64
	replicationRecipients   uint64
	replicationBytes        uint64
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
		world:              world,
		aoi:                game.NewAOIIndex(game.AOIConfigFromWorld(world.Config())),
		loadSavedPlayer:    loadSavedPlayer,
		persister:          persister,
		collectibleField:   collectibleField,
		scorePersister:     scorePersister,
		playerScores:            map[int64]int64{},
		syntheticPlayerIDs:      map[int64]struct{}{},
		visibleCollectibleIDs:   map[int64]map[uint64]struct{}{},
		pendingCollectEntered:   map[int64][]CollectibleEnteredItem{},
		pendingCollectLeftIDs:   map[int64][]uint64{},
		pendingCollectSpawned:   map[int64][]CollectibleSpawnedItem{},
		pendingCollectCollected: map[int64][]uint64{},
		collectCooldowns:        map[int64]time.Time{},
		collects:                make(chan collectEvent),
		leaderboards:            make(chan leaderboardRequest),
		register:                make(chan ClientSender),
		unregister:         make(chan ClientSender),
		inputs:             make(chan inputEvent),
		appearanceUpdates:  make(chan appearanceUpdateRequest),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
		clients:            map[int64]ClientSender{},
		persistDirty:       map[int64]struct{}{},
		persistSeq:         map[int64]uint64{},
		pendingEntered:     map[int64]game.PlayerState{},
		pendingLeft:        map[int64][]int64{},
		pendingAppearances: map[int64]game.Appearance{},
		disconnectUser:     make(chan disconnectRequest),
		simulationTick:     simulationTick,
		broadcastTick:      broadcastTick,
		persistenceTick:    persistenceTick,
		statsTick:          statsTick,
		stopTickers:        stopTickers,
	}

	// dispatcher 将 per-recipient 编码/发送卸载到 worker goroutine
	n := runtime.GOMAXPROCS(0) / 2
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	h.dispatcher = NewReplicationDispatcher(n, 512, func(recipientID int64) {
		h.DisconnectUser(recipientID)
	})

	return h
}

// Run is the single owner of both connections and authoritative world state.
func (h *Hub) Run() {
	defer close(h.done)
	defer h.stopTickers()

	for {
		select {
		case client := <-h.register:
			h.registerClient(client)
		case client := <-h.unregister:
			h.removeClient(client)
		case event := <-h.inputs:
			if h.clients[event.client.ID()] == event.client &&
				h.world.ApplyInput(event.client.ID(), event.input) {
				h.stats.acceptedInputs++
			}
		case <-h.simulationTick:
			moved := h.world.Step(simulationInterval)
			for _, playerID := range moved {
				h.persistDirty[playerID] = struct{}{}
			}
			h.stats.simulationTicks++
		case <-h.broadcastTick:
			h.broadcastReplication()
		case <-h.persistenceTick:
			h.persistDirtyPositions()
		case <-h.statsTick:
			h.logStats()
		case req := <-h.disconnectUser:
			if client, ok := h.clients[req.userID]; ok {
				h.removeClient(client)
			}
			if d, ok := h.persister.(PositionDrainer); ok {
				d.Drain()
			}
			close(req.done)
		case req := <-h.appearanceUpdates:
			h.applyAppearanceUpdate(req)
		case evt := <-h.collects:
			h.processCollect(evt)
		case req := <-h.leaderboards:
			req.reply <- h.buildLeaderboard(req.requesterID)
		case <-h.stop:
			h.dispatcher.Stop()
			for _, client := range h.clients {
				h.submitFinalPosition(client.ID())
			}
			if d, ok := h.persister.(PositionDrainer); ok {
				d.Drain()
			}
			if h.scorePersister != nil {
				h.scorePersister.Drain()
			}
			for _, client := range h.clients {
				client.CloseSend()
			}
			return
		}
		if h.scorePersister != nil {
			h.scorePersister.Drain()
		}
	}
}

func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
	<-h.done
	if h.persister != nil {
		h.persister.Stop()
	}
}

func (h *Hub) DisconnectUser(userID int64) {
	done := make(chan struct{})
	select {
	case h.disconnectUser <- disconnectRequest{userID: userID, done: done}:
		<-done
	case <-h.done:
	}
}

func (h *Hub) Register(client ClientSender) bool {
	select {
	case h.register <- client:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) Unregister(client ClientSender) {
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

func (h *Hub) ApplyInput(client ClientSender, input game.InputState) bool {
	select {
	case h.inputs <- inputEvent{client: client, input: input}:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) UpdateAppearance(userID int64, appearance game.Appearance) bool {
	done := make(chan struct{})
	select {
	case h.appearanceUpdates <- appearanceUpdateRequest{
		userID:     userID,
		appearance: appearance,
		done:       done,
	}:
		<-done
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) registerClient(client ClientSender) {
	if existing, exists := h.clients[client.ID()]; exists && existing != client {
		existing.CloseSend()
		h.world.ResetInput(client.ID())
		h.clients[client.ID()] = client
		h.clearPendingReplicationFor(client.ID())
		h.sendInitialization(client)
		return
	}

	if !h.world.HasPlayer(client.ID()) {
		h.addPlayer(client.ID(), client.Username())
		h.insertPlayerIntoAOI(client.ID())
		if len(h.clients) > 0 {
			if state, ok := h.world.PlayerState(client.ID()); ok {
				h.pendingEntered[client.ID()] = state
				h.clearPendingLeftForPlayer(client.ID())
			}
		}
	}

	h.clients[client.ID()] = client
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
	delete(h.pendingAppearances, playerID)
	delete(h.pendingLeft, playerID)
	delete(h.pendingCollectEntered, playerID)
	delete(h.pendingCollectLeftIDs, playerID)
	delete(h.pendingCollectSpawned, playerID)
	delete(h.pendingCollectCollected, playerID)
	delete(h.visibleCollectibleIDs, playerID)
}

func (h *Hub) clearPendingLeftForPlayer(playerID int64) {
	for clientID, leftIDs := range h.pendingLeft {
		filtered := leftIDs[:0]
		for _, id := range leftIDs {
			if id != playerID {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(h.pendingLeft, clientID)
		} else {
			h.pendingLeft[clientID] = filtered
		}
	}
}

func (h *Hub) isVisibleTo(clientID, playerID int64) bool {
	return h.aoi.IsVisible(clientID, playerID)
}

func (h *Hub) addPlayer(userID int64, username string) {
	if h.loadSavedPlayer != nil {
		if state, ok := h.loadSavedPlayer(userID); ok {
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
			if h.collectibleField != nil {
				h.playerScores[userID] = state.Score
			}
			if state.IsSynthetic {
				h.syntheticPlayerIDs[userID] = struct{}{}
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

	h.pendingAppearances[req.userID] = req.appearance
}

func (h *Hub) removeClient(client ClientSender) {
	current, exists := h.clients[client.ID()]
	if !exists || current != client {
		return
	}

	delete(h.clients, client.ID())
	h.submitFinalPosition(client.ID())

	// 提交最新分数（同步）
	if h.scorePersister != nil {
		if score, ok := h.playerScores[client.ID()]; ok {
			h.scorePersister.SubmitSync(client.ID(), score)
		}
	}

	changes := h.aoi.Remove(client.ID())
	for _, neighborID := range changes.Left {
		if _, connected := h.clients[neighborID]; connected {
			h.pendingLeft[neighborID] = append(h.pendingLeft[neighborID], client.ID())
		}
	}

	h.world.RemovePlayer(client.ID())
	delete(h.pendingEntered, client.ID())
	h.clearPendingReplicationFor(client.ID())
	delete(h.persistDirty, client.ID())
	delete(h.persistSeq, client.ID())
	client.CloseSend()
}

func (h *Hub) persistDirtyPositions() {
	if h.persister == nil || len(h.persistDirty) == 0 {
		return
	}

	updates := make([]PositionUpdate, 0, len(h.persistDirty))
	for userID := range h.persistDirty {
		position, ok := h.world.PlayerPosition(userID)
		if !ok {
			continue
		}
		h.persistSeq[userID]++
		updates = append(updates, PositionUpdate{
			UserID: userID,
			Lat:    position.Lat,
			Lng:    position.Lng,
			Seq:    h.persistSeq[userID],
		})
	}
	clear(h.persistDirty)
	h.persister.Submit(updates)
}

func (h *Hub) submitFinalPosition(userID int64) {
	if h.persister == nil {
		return
	}
	position, ok := h.world.PlayerPosition(userID)
	if !ok {
		return
	}
	h.persistSeq[userID]++
	update := []PositionUpdate{{
		UserID: userID,
		Lat:    position.Lat,
		Lng:    position.Lng,
		Seq:    h.persistSeq[userID],
	}}
	if syncSub, ok := h.persister.(interface{ SubmitSync([]PositionUpdate) }); ok {
		syncSub.SubmitSync(update)
	} else {
		h.persister.Submit(update)
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
	if h.collectibleField != nil {
		regions = h.collectibleField.Regions()
		position, _ := h.world.PlayerPosition(client.ID())
		visibleCollectibles = h.collectibleField.CollectiblesWithinRadius(position.Lat, position.Lng, 500)
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
	if h.collectibleField != nil && len(visibleCollectibles) > 0 {
		visible := make(map[uint64]struct{}, len(visibleCollectibles))
		for _, c := range visibleCollectibles {
			visible[c.ID] = struct{}{}
		}
		h.visibleCollectibleIDs[client.ID()] = visible
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
	entries := make([]entry, 0, len(h.clients))
	for playerID := range h.clients {
		if _, synthetic := h.syntheticPlayerIDs[playerID]; synthetic {
			continue
		}
		score := h.playerScores[playerID]
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
	if h.clients[playerID] != client {
		return
	}

	// 合成账户不能拾取
	if _, synthetic := h.syntheticPlayerIDs[playerID]; synthetic {
		return
	}

	// 服务端冷却
	now := time.Now()
	if last, ok := h.collectCooldowns[playerID]; ok && now.Sub(last) < collectCooldown {
		return
	}
	h.collectCooldowns[playerID] = now

	// 收集品必须存在且对玩家可见
	if h.collectibleField == nil {
		return
	}
	collectible, ok := h.collectibleField.Collectible(evt.collectibleID)
	if !ok {
		return
	}
	visibleIDs := h.visibleCollectibleIDs[playerID]
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
	if !h.collectibleField.Remove(evt.collectibleID) {
		return
	}

	// 增加分数并异步持久化
	h.playerScores[playerID]++
	newScore := h.playerScores[playerID]
	if h.scorePersister != nil {
		h.scorePersister.Submit(ScoreUpdate{UserID: playerID, Score: newScore})
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
		if _, connected := h.clients[nearbyID]; !connected {
			continue
		}
		if vis, ok := h.visibleCollectibleIDs[nearbyID]; ok {
			delete(vis, collectible.ID)
		}
		h.pendingCollectCollected[nearbyID] = append(h.pendingCollectCollected[nearbyID], evt.collectibleID)
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
	spawned := h.collectibleField.AdvanceReplacements()
	for _, c := range spawned {
		nearbyPlayerIDs := h.aoi.QueryPlayerIDsNearPoint(c.Lat, c.Lng)
		item := CollectibleSpawnedItem{ID: c.ID, RegionID: c.RegionID, Lat: c.Lat, Lng: c.Lng}
		for _, playerID := range nearbyPlayerIDs {
			if _, connected := h.clients[playerID]; !connected {
				continue
			}
			if vis, ok := h.visibleCollectibleIDs[playerID]; ok {
				vis[c.ID] = struct{}{}
			}
			h.pendingCollectSpawned[playerID] = append(h.pendingCollectSpawned[playerID], item)
		}
	}
}

// recalcCollectibleVisibility 为移动的玩家重新计算收集品可见性
func (h *Hub) recalcCollectibleVisibility(movedIDs []int64) {
	for _, playerID := range movedIDs {
		if _, connected := h.clients[playerID]; !connected {
			continue
		}
		position, ok := h.world.PlayerPosition(playerID)
		if !ok {
			continue
		}

		within500 := h.collectibleField.CollectiblesWithinRadius(position.Lat, position.Lng, 500)
		within600Set := make(map[uint64]struct{})
		within600List := h.collectibleField.CollectiblesWithinRadius(position.Lat, position.Lng, 600)
		for _, c := range within600List {
			within600Set[c.ID] = struct{}{}
		}

		prevVisible := h.visibleCollectibleIDs[playerID]
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

		h.visibleCollectibleIDs[playerID] = newVisible

		if len(entered) > 0 {
			h.pendingCollectEntered[playerID] = append(h.pendingCollectEntered[playerID], entered...)
		}
		if len(leftIDs) > 0 {
			h.pendingCollectLeftIDs[playerID] = append(h.pendingCollectLeftIDs[playerID], leftIDs...)
		}
	}
}

func (h *Hub) takePendingCollectEntered() map[int64][]CollectibleEnteredItem {
	result := h.pendingCollectEntered
	h.pendingCollectEntered = map[int64][]CollectibleEnteredItem{}
	return result
}

func (h *Hub) takePendingCollectLeftIDs() map[int64][]uint64 {
	result := h.pendingCollectLeftIDs
	h.pendingCollectLeftIDs = map[int64][]uint64{}
	return result
}

func (h *Hub) takePendingCollectSpawned() map[int64][]CollectibleSpawnedItem {
	result := h.pendingCollectSpawned
	h.pendingCollectSpawned = map[int64][]CollectibleSpawnedItem{}
	return result
}

func (h *Hub) takePendingCollectCollected() map[int64][]uint64 {
	result := h.pendingCollectCollected
	h.pendingCollectCollected = map[int64][]uint64{}
	return result
}

// playerScore 返回玩家当前内存分数
func (h *Hub) playerScore(userID int64) int64 {
	if h.playerScores == nil {
		return 0
	}
	return h.playerScores[userID]
}

func (h *Hub) broadcastReplication() {
	movedIDs := h.world.TakeMovedPlayerIDs()
	h.world.TakeRemovedPlayerIDs()

	oldNeighborsByMover := h.snapshotMoverVisibility(movedIDs)
	h.applyMovementAOIChanges(movedIDs)
	h.stats.movedPlayers += uint64(len(movedIDs))

	pendingEntered := h.takePendingEntered()
	pendingLeft := h.takePendingLeft()
	pendingAppearances := h.takePendingAppearances()

	// 推进收集品替换并计算可见性变化
	if h.collectibleField != nil {
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
	byRecipient := make(map[int64]*ReplicationChanges)

	// 自位置：每个已连接的移动者
	for _, playerID := range movedIDs {
		if _, connected := h.clients[playerID]; !connected {
			continue
		}
		if position, ok := h.world.PlayerPosition(playerID); ok {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.SelfPosition = &SelfPosition{Lat: position.Lat, Lng: position.Lng}
		}
	}

	// 稳定关系位置：从移动者扇出到旧邻居（同时仍在最终可见集中）
	for _, moverID := range movedIDs {
		position, ok := h.world.PlayerPosition(moverID)
		if !ok {
			continue
		}
		oldNeighbors := oldNeighborsByMover[moverID]
		for _, neighborID := range h.aoi.VisibleNeighbors(moverID) {
			if _, inOld := oldNeighbors[neighborID]; !inOld {
				continue
			}
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Positions = append(entry.Positions, position)
		}
	}

	// 待处理进入：从进入者扇出到当前可见的已连接邻居
	for _, state := range pendingEntered {
		for _, neighborID := range h.aoi.VisibleNeighbors(state.ID) {
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			entry := getOrCreateRecipient(byRecipient, neighborID)
			entry.Entered = append(entry.Entered, state)
		}
	}

	// 待处理离开（已按接收者 key 存储）
	for recipientID, leftIDs := range pendingLeft {
		if _, connected := h.clients[recipientID]; !connected {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.LeftPlayerIDs = append(entry.LeftPlayerIDs, leftIDs...)
	}

	// 待处理外观变更：变更者本人和其可见邻居
	for playerID, appearance := range pendingAppearances {
		if _, connected := h.clients[playerID]; connected {
			entry := getOrCreateRecipient(byRecipient, playerID)
			entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
				PlayerID:   playerID,
				Appearance: appearance,
			})
		}
		for _, neighborID := range h.aoi.VisibleNeighbors(playerID) {
			if _, connected := h.clients[neighborID]; connected {
				entry := getOrCreateRecipient(byRecipient, neighborID)
				entry.Appearances = append(entry.Appearances, PlayerAppearanceUpdate{
					PlayerID:   playerID,
					Appearance: appearance,
				})
			}
		}
	}

	// 收集品进入：按接收者累积
	for recipientID, items := range collectEntered {
		if _, connected := h.clients[recipientID]; !connected {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectiblesEntered = append(entry.CollectiblesEntered, items...)
	}

	// 收集品离开：按接收者 key 存储
	for recipientID, ids := range collectLeft {
		if _, connected := h.clients[recipientID]; !connected {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectibleIDsLeft = append(entry.CollectibleIDsLeft, ids...)
	}

	// 收集品生成：按接收者累积
	for recipientID, items := range collectSpawned {
		if _, connected := h.clients[recipientID]; !connected {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectiblesSpawned = append(entry.CollectiblesSpawned, items...)
	}

	// 收集品被拾取：按接收者累积
	for recipientID, ids := range collectCollected {
		if _, connected := h.clients[recipientID]; !connected {
			continue
		}
		entry := getOrCreateRecipient(byRecipient, recipientID)
		entry.CollectibleIDsCollected = append(entry.CollectibleIDsCollected, ids...)
	}

	// 提交 encode/send 到 dispatcher（异步，不阻塞广播 tick）
	for recipientID, changes := range byRecipient {
		client, connected := h.clients[recipientID]
		if !connected {
			continue
		}
		h.stats.replicationRecipients++
		h.dispatcher.Submit(replicationJob{
			recipientID: recipientID,
			tick:        tick,
			client:      client,
			changes:     copyReplicationChanges(*changes),
		})
	}
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

// snapshotMoverVisibility 只为移动者捕获旧可见邻居集，不再复制全部已连接客户端。
func (h *Hub) snapshotMoverVisibility(movedIDs []int64) map[int64]map[int64]struct{} {
	snapshot := make(map[int64]map[int64]struct{}, len(movedIDs))
	for _, playerID := range movedIDs {
		neighbors := h.aoi.VisibleNeighbors(playerID)
		set := make(map[int64]struct{}, len(neighbors))
		for _, neighborID := range neighbors {
			set[neighborID] = struct{}{}
		}
		snapshot[playerID] = set
	}
	return snapshot
}

// moverHadNeighbor 检查 observerID 在 mover 移动前是否在 mover 的可见邻居集中。
// 利用 AOI 对称性：旧可见集 key=clientID 含 playerID ⇔ key=playerID 含 clientID。
func (h *Hub) moverHadNeighbor(oldNeighborsByMover map[int64]map[int64]struct{}, moverID, observerID int64) bool {
	neighbors, ok := oldNeighborsByMover[moverID]
	if !ok {
		return false
	}
	_, visible := neighbors[observerID]
	return visible
}

func (h *Hub) applyMovementAOIChanges(movedIDs []int64) {
	for _, playerID := range movedIDs {
		position, ok := h.world.PlayerPosition(playerID)
		if !ok {
			continue
		}
		changes := h.aoi.Move(playerID, position.Lat, position.Lng)
		state, ok := h.world.PlayerState(playerID)
		if !ok {
			continue
		}
		for _, neighborID := range changes.Entered {
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			if _, already := h.pendingEntered[playerID]; !already {
				h.pendingEntered[playerID] = state
			}
		}
		for _, neighborID := range changes.Left {
			if _, connected := h.clients[neighborID]; !connected {
				continue
			}
			h.pendingLeft[neighborID] = append(h.pendingLeft[neighborID], playerID)
		}
	}
}

func (h *Hub) takePendingLeft() map[int64][]int64 {
	if len(h.pendingLeft) == 0 {
		return nil
	}
	left := make(map[int64][]int64, len(h.pendingLeft))
	for clientID, playerIDs := range h.pendingLeft {
		left[clientID] = append([]int64(nil), playerIDs...)
	}
	clear(h.pendingLeft)
	return left
}

func (h *Hub) takePendingEntered() []game.PlayerState {
	if len(h.pendingEntered) == 0 {
		return nil
	}
	states := make([]game.PlayerState, 0, len(h.pendingEntered))
	for playerID := range h.pendingEntered {
		if state, ok := h.world.PlayerState(playerID); ok {
			states = append(states, state)
		}
	}
	clear(h.pendingEntered)
	return states
}

func (h *Hub) takePendingAppearances() map[int64]game.Appearance {
	if len(h.pendingAppearances) == 0 {
		return nil
	}
	appearances := make(map[int64]game.Appearance, len(h.pendingAppearances))
	for playerID, appearance := range h.pendingAppearances {
		appearances[playerID] = appearance
	}
	clear(h.pendingAppearances)
	return appearances
}

func (h *Hub) logStats() {
	aoiStats := h.aoi.TakeStats()
	h.stats.aoiCandidatePairs += aoiStats.CandidatePairs
	h.stats.aoiDistanceChecks += aoiStats.DistanceChecks
	h.stats.aoiRelationshipsEntered += aoiStats.RelationshipsEntered
	h.stats.aoiRelationshipsLeft += aoiStats.RelationshipsLeft

	snap := &HubSnapshot{
		ConnectedClients:      len(h.clients),
		AcceptedInputs:        h.stats.acceptedInputs,
		SimulationTicks:       h.stats.simulationTicks,
		MovedPlayers:          h.stats.movedPlayers,
		AOICandidatePairs:     h.stats.aoiCandidatePairs,
		AOIDistanceChecks:     h.stats.aoiDistanceChecks,
		RelationshipsEntered:  h.stats.aoiRelationshipsEntered,
		RelationshipsLeft:     h.stats.aoiRelationshipsLeft,
		ReplicationMessages:   h.stats.replicationMessages,
		ReplicationRecipients: h.stats.replicationRecipients,
		ReplicationBytes:      h.stats.replicationBytes,
		SampledAt:             time.Now(),
	}
	// 从 dispatcher 读取编码/字节统计（替换旧的 actor 内联统计）
	ds := h.dispatcher.Stats()
	snap.Dispatcher = ds
	snap.ReplicationMessages = ds.Encoded
	snap.ReplicationBytes = ds.EncodedBytes

	h.snapshot.Store(snap)

	log.Printf(
		"realtime stats clients=%d inputs=%d simulation_ticks=%d moved_players=%d aoi_candidates=%d aoi_distance_checks=%d aoi_entered=%d aoi_left=%d replication_messages=%d replication_recipients=%d replication_bytes=%d dispatched_submitted=%d dispatched_encoded=%d dispatched_skipped=%d dispatched_errors=%d dispatched_dropped=%d dispatched_send_failures=%d dispatched_queued=%d dispatched_workers=%d dispatched_bytes=%d",
		snap.ConnectedClients,
		snap.AcceptedInputs,
		snap.SimulationTicks,
		snap.MovedPlayers,
		snap.AOICandidatePairs,
		snap.AOIDistanceChecks,
		snap.RelationshipsEntered,
		snap.RelationshipsLeft,
		snap.ReplicationMessages,
		snap.ReplicationRecipients,
		snap.ReplicationBytes,
		ds.Submitted,
		ds.Encoded,
		ds.SkippedEmpty,
		ds.EncodeErrors,
		ds.Dropped,
		ds.SendFailures,
		ds.QueueDepth,
		ds.WorkerCount,
		ds.EncodedBytes,
	)
	h.stats = intervalStats{}
}
