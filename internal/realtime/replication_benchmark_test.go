package realtime

import (
	"fmt"
	"testing"
	"time"

	"map-walker/internal/game"
)

const (
	benchActivityHalfExtent = 5000
	benchMovementRatio      = 0.8
	benchClientBuffer       = 256
	benchOriginLat          = 31.2304
	benchOriginLng          = 121.4737
)

func benchClientPosition(index int) (localX, localY float64) {
	fx := benchPlacementFraction(index, 1)
	fy := benchPlacementFraction(index, 2)
	localX = fx*2*benchActivityHalfExtent - benchActivityHalfExtent
	localY = fy*2*benchActivityHalfExtent - benchActivityHalfExtent
	return
}

func benchPlacementFraction(index, salt int) float64 {
	v := uint64(index)*0x9E3779B97F4A7C15 + uint64(salt)*0xBF58476D1CE4E5B9
	v ^= v >> 33
	v *= 0xff51afd7ed558ccd
	v ^= v >> 33
	return float64(v%10000) / 10000
}

func benchOriginCfg() game.Config {
	return game.Config{
		SpawnLat:             benchOriginLat,
		SpawnLng:             benchOriginLng,
		SpeedMetersPerSecond: 3000,
	}
}

func benchLoader(numClients int) SavedPlayerLoader {
	aoiCfg := game.AOIConfigFromWorld(benchOriginCfg())
	return func(userID int64) (SavedPlayerLoad, bool) {
		idx := int(userID) - 1
		if idx < 0 || idx >= numClients {
			return SavedPlayerLoad{}, false
		}
		localX, localY := benchClientPosition(idx)
		lat, lng := aoiCfg.LocalToLatLng(localX, localY)
		return SavedPlayerLoad{
			Username:    fmt.Sprintf("p%d", idx),
			Lat:         lat,
			Lng:         lng,
			HasPosition: true,
		}, true
	}
}

// BenchmarkHubReplication 测量确定性客户端放置下的广播复制性能。
// 直接调用 Step 和 broadcastReplication（消除 Hub select loop 的随机性），
// 逻辑计数器在重复运行间保持稳定。
func BenchmarkHubReplication(b *testing.B) {
	for _, numClients := range []int{2000, 3000} {
		b.Run(fmt.Sprintf("%d", numClients), func(b *testing.B) {
			benchHubReplication(b, numClients)
		})
	}
}

func benchHubReplication(b *testing.B, numClients int) {
	hub, clients := setupDirectBenchHub(b, numClients)
	moveCount := int(float64(numClients) * benchMovementRatio)

	// 预热：多轮移动建立稳定的 AOI 关系
	var warmupSeq uint64 = 1000
	for range 3 {
		warmupSeq = benchDirectApplyInputs(hub, clients, moveCount, warmupSeq, 1.0)
		hub.world.Step(simulationInterval)
		hub.broadcastReplication()
		hub.dispatcher.WaitIdle()
		benchDrainAllDirect(clients)
		warmupSeq = benchDirectApplyInputs(hub, clients, moveCount, warmupSeq, -1.0)
		hub.world.Step(simulationInterval)
		hub.broadcastReplication()
		hub.dispatcher.WaitIdle()
		benchDrainAllDirect(clients)
	}

	var globalSeq uint64 = 1000
	direction := 1.0

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		b.StopTimer()
		globalSeq = benchDirectApplyInputs(hub, clients, moveCount, globalSeq, direction)
		// 清空 AOI 统计，只测量当前这一步
		hub.aoi.TakeStats()
		b.StartTimer()

		hub.world.Step(simulationInterval)
		hub.broadcastReplication()
		hub.dispatcher.WaitIdle()

		stats := hub.aoi.TakeStats()
		msgs, bytes := benchDrainAllDirect(clients)

		b.ReportMetric(float64(msgs), "msgs/op")
		b.ReportMetric(float64(bytes), "bytes/op")
		b.ReportMetric(float64(moveCount), "moved/op")
		b.ReportMetric(float64(stats.RelationshipsEntered), "entered/op")
		b.ReportMetric(float64(stats.RelationshipsLeft), "left/op")

		direction *= -1
	}
}

// setupDirectBenchHub 创建 Hub 并直接注册客户端（不启动 goroutine）
func setupDirectBenchHub(b *testing.B, numClients int) (*Hub, []*testClient) {
	b.Helper()

	world := game.NewWorld(benchOriginCfg())
	loader := benchLoader(numClients)

	hub := newHub(
		world, loader, nil, nil, nil,
		make(chan time.Time),  // simCh — 不使用（直接调用 Step）
		make(chan time.Time),  // broadcastCh — 不使用
		make(chan time.Time),  // persistenceCh — 不使用
		nil,                   // statsTick — 不用
		func() {},             // stopTickers — 不用
	)

	clients := make([]*testClient, numClients)
	for i := 0; i < numClients; i++ {
		clients[i] = NewTestClient(int64(i+1), benchClientBuffer)
		hub.registerClient(clients[i])
	}

	// 排空所有初始化消息
	for i := 0; i < numClients; i++ {
		benchDrainInit(b, clients[i])
	}

	return hub, clients
}

// benchDirectApplyInputs 直接调用 World.ApplyInput（绕过 Hub select loop）
func benchDirectApplyInputs(hub *Hub, clients []*testClient, moveCount int, baseSeq uint64, direction float64) uint64 {
	seq := baseSeq
	right := direction > 0
	for i := 0; i < moveCount; i++ {
		seq++
		hub.world.ApplyInput(clients[i].ID(), game.InputState{
			Sequence: seq,
			Right:    right,
			Left:     !right,
		})
	}
	return seq
}

func benchDrainInit(b *testing.B, c *testClient) {
	b.Helper()
	for range 2 {
		select {
		case <-c.send:
		case <-time.After(time.Second):
			b.Fatal("timeout waiting for init message")
		}
	}
}

func benchDrainAllDirect(clients []*testClient) (msgCount int, byteCount int) {
	for _, c := range clients {
		for {
			select {
			case d := <-c.send:
				msgCount++
				byteCount += len(d)
			default:
				goto drainNext
			}
		}
	drainNext:
	}
	return
}
