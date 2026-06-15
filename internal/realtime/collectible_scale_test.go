package realtime

import (
	"log"
	"math/rand/v2"
	"testing"
	"time"

	"map-walker/internal/game"
)

const collectibleScalePlayerCount = 200

type collectibleScaleMetrics struct {
	AOICandidatePairs       uint64
	AOIDistanceChecks       uint64
	RelationshipsEntered    uint64
	RelationshipsLeft       uint64
	ReplicationMessages     uint64
	ReplicationRecipients   uint64
	ReplicationBytes        uint64
	MovedPlayers            uint64
}

func TestCollectibleScaleDeterministic(t *testing.T) {
	first := runCollectibleScaleScenario(t, false)
	second := runCollectibleScaleScenario(t, false)
	if first != second {
		t.Fatalf("collectible scale metrics differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestCollectibleScaleNoAOIRegression(t *testing.T) {
	withMetrics := runCollectibleScaleScenario(t, false)
	withoutMetrics := runCollectibleScaleScenario(t, true)

	t.Logf("with collectibles: %+v", withMetrics)
	t.Logf("without collectibles: %+v", withoutMetrics)

	if withMetrics.AOIDistanceChecks != withoutMetrics.AOIDistanceChecks {
		t.Fatalf("AOI distance checks drifted: with=%d without=%d",
			withMetrics.AOIDistanceChecks, withoutMetrics.AOIDistanceChecks)
	}
	if withMetrics.ReplicationMessages != withoutMetrics.ReplicationMessages {
		t.Fatalf("replication messages drifted: with=%d without=%d",
			withMetrics.ReplicationMessages, withoutMetrics.ReplicationMessages)
	}
}

func TestCollectibleScaleSyntheticPickupNoImpact(t *testing.T) {
	first := runCollectibleScaleScenario(t, false)
	second := runCollectibleScaleScenario(t, false)

	if first.AOIDistanceChecks != second.AOIDistanceChecks {
		t.Fatalf("AOI distance checks differ between runs")
	}
	if first.ReplicationMessages != second.ReplicationMessages {
		t.Fatalf("replication messages differ between runs")
	}
}

func runCollectibleScaleScenario(t *testing.T, skipCollectibles bool) collectibleScaleMetrics {
	t.Helper()

	var logOutput syncBuffer
	originalWriter := log.Writer()
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	config := fastTestWorldConfig()
	world := game.NewWorld(config)

	var field *game.CollectibleField
	if !skipCollectibles {
		rng := rand.New(rand.NewPCG(0, 0))
		aoiConfig := game.AOIConfigFromWorld(config)
		regions := []game.CollectibleRegion{
			{ID: "r1", CenterLat: config.SpawnLat, CenterLng: config.SpawnLng,
				RadiusMeters: 150, TargetCount: 20, RespawnMin: 5 * time.Second, RespawnMax: 10 * time.Second},
			{ID: "r2", CenterLat: config.SpawnLat + 0.003, CenterLng: config.SpawnLng + 0.003,
				RadiusMeters: 150, TargetCount: 20, RespawnMin: 5 * time.Second, RespawnMax: 10 * time.Second},
			{ID: "r3", CenterLat: config.SpawnLat - 0.003, CenterLng: config.SpawnLng - 0.003,
				RadiusMeters: 150, TargetCount: 20, RespawnMin: 5 * time.Second, RespawnMax: 10 * time.Second},
		}
		field = game.NewCollectibleField(aoiConfig, regions, nil, rng)
		field.Populate()
	}

	statsTick := make(chan time.Time, 8)
	simulations := make(chan time.Time)
	broadcasts := make(chan time.Time)
	persistence := make(chan time.Time, 8)

	hub := newHub(world, scalePlayerLoader(config, collectibleScalePlayerCount), nil, field, nil,
		simulations, broadcasts, persistence, statsTick, func() {})
	go hub.Run()
	defer hub.Stop()

	clients := make([]*testClient, collectibleScalePlayerCount)
	for i := range collectibleScalePlayerCount {
		clients[i] = NewTestClient(scalePlayerID(i), 32)
		if !hub.Register(clients[i]) {
			t.Fatalf("register %d failed", clients[i].ID())
		}
	}
	for i := range collectibleScalePlayerCount {
		mustReceiveInitialization(t, clients[i])
	}

	// 移动一半玩家
	for i := range collectibleScalePlayerCount / 2 {
		hub.ApplyInput(clients[i], game.InputState{Sequence: 1, Right: true})
	}
	simulations <- time.Now()

	// 广播
	broadcasts <- time.Now()

	// 消费所有客户端的复制消息
	for i := range collectibleScalePlayerCount {
		drainReplicationUpdates(t, clients[i])
	}

	// 第二轮：移动所有玩家
	for i := range collectibleScalePlayerCount {
		seq := uint64(1)
		if i < collectibleScalePlayerCount/2 {
			seq = 2
		}
		hub.ApplyInput(clients[i], game.InputState{Sequence: seq, Up: true})
	}
	simulations <- time.Now()
	broadcasts <- time.Now()

	for i := range collectibleScalePlayerCount {
		drainReplicationUpdates(t, clients[i])
	}

	// 收集统计
	statsTick <- time.Now()
	waitForStatsLog(t, &logOutput, "replication_messages=")
	metrics := collectibleScaleMetrics{}
	parseScaleStatsFromLog(t, logOutput.String(), &metrics)
	return metrics
}

func scalePlayerLoader(config game.Config, count int) SavedPlayerLoader {
	aoiCfg := game.AOIConfigFromWorld(config)
	return func(userID int64) (SavedPlayerLoad, bool) {
		idx := int(userID) - 1
		if idx < 0 || idx >= count {
			return SavedPlayerLoad{}, false
		}
		const gridCols = 20
		row := idx / gridCols
		col := idx % gridCols
		localX := float64(col-10) * 150
		localY := float64(row-5) * 150
		lat, lng := aoiCfg.LocalToLatLng(localX, localY)
		return SavedPlayerLoad{
			Username:    scalePlayerName(idx),
			Lat:         lat,
			Lng:         lng,
			HasPosition: true,
		}, true
	}
}

func scalePlayerID(index int) int64 {
	return int64(index + 1)
}

func scalePlayerName(index int) string {
	if index == 0 {
		return "p0"
	}
	s := ""
	for n := index; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return "p" + s
}

func parseScaleStatsFromLog(t *testing.T, output string, metrics *collectibleScaleMetrics) {
	t.Helper()
	for _, line := range splitLines(output) {
		if !contains(line, "realtime stats") {
			continue
		}
		kv := map[string]*uint64{
			"aoi_candidates":        &metrics.AOICandidatePairs,
			"aoi_distance_checks":   &metrics.AOIDistanceChecks,
			"aoi_entered":           &metrics.RelationshipsEntered,
			"aoi_left":              &metrics.RelationshipsLeft,
			"replication_messages":  &metrics.ReplicationMessages,
			"replication_recipients": &metrics.ReplicationRecipients,
			"replication_bytes":     &metrics.ReplicationBytes,
			"moved_players":         &metrics.MovedPlayers,
		}
		for key, ptr := range kv {
			*ptr = parseUintFromLine(line, key)
		}
	}
}

func parseUintFromLine(line, key string) uint64 {
	prefix := key + "="
	idx := indexOf(line, prefix)
	if idx < 0 {
		return 0
	}
	rest := line[idx+len(prefix):]
	var n uint64
	for i := 0; i < len(rest) && rest[i] >= '0' && rest[i] <= '9'; i++ {
		n = n*10 + uint64(rest[i]-'0')
	}
	return n
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func contains(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
