package realtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"testing"
	"time"

	"map-walker/internal/game"
)

const (
	thousandClientCount = 1000
	denseClusterStart   = 950
)

type thousandClientMetrics struct {
	AoiCandidatePairs       uint64
	AoiDistanceChecks       uint64
	AoiRelationshipsEntered uint64
	AoiRelationshipsLeft    uint64
	ReplicationMessages     uint64
	ReplicationRecipients   uint64
	ReplicationBytes        uint64
}

func TestAOIThousandClientFunctionalScenario(t *testing.T) {
	metrics := runThousandClientScenario(t)
	if metrics.ReplicationMessages == 0 {
		t.Fatal("expected non-empty replication messages")
	}
	if metrics.ReplicationBytes == 0 {
		t.Fatal("expected non-zero replication payload bytes")
	}
	if metrics.AoiDistanceChecks == 0 {
		t.Fatal("expected AOI distance checks")
	}
}

func TestAOIThousandClientScenarioDeterministic(t *testing.T) {
	first := runThousandClientScenario(t)
	second := runThousandClientScenario(t)
	if first != second {
		t.Fatalf("scenario metrics differ:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func runThousandClientScenario(t *testing.T) thousandClientMetrics {
	t.Helper()

	var logOutput bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	statsTick := make(chan time.Time, 8)
	simulations := make(chan time.Time)
	broadcasts := make(chan time.Time)
	persistence := make(chan time.Time, 8)
	world := game.NewWorld(fastTestWorldConfig())
	hub := newHub(world, thousandPlayerLoader(), nil, simulations, broadcasts, persistence, statsTick, func() {})
	go hub.Run()
	defer hub.Stop()

	clients := make([]*testClient, thousandClientCount)
	for i := 0; i < denseClusterStart; i++ {
		clients[i] = NewTestClient(playerIDForIndex(i), 32)
		if !hub.Register(clients[i]) {
			t.Fatalf("register %d failed", clients[i].ID())
		}
	}
	for i := 0; i < denseClusterStart; i++ {
		mustReceiveInitialization(t, clients[i])
	}

	sparse := clients[0]
	for i := denseClusterStart; i < thousandClientCount; i++ {
		clients[i] = NewTestClient(playerIDForIndex(i), 32)
		if !hub.Register(clients[i]) {
			t.Fatalf("register %d failed", clients[i].ID())
		}
	}

	denseAnchor := clients[denseClusterStart]
	denseMover := clients[denseClusterStart+5]
	denseLeaver := clients[denseClusterStart+9]
	replacement := NewTestClient(playerIDForIndex(thousandClientCount-1), 32)

	for i := denseClusterStart; i < thousandClientCount; i++ {
		mustReceiveInitialization(t, clients[i])
	}

	assertVisibleSnapshotMatchesAOI(t, hub, sparse, 0)
	assertVisibleSnapshotMatchesAOI(t, hub, denseAnchor, countDenseNeighbors(denseClusterStart))

	broadcasts <- time.Now()
	joinedUpdate := mustReceiveReplicationUpdate(t, denseAnchor)
	if len(joinedUpdate.Entered) == 0 {
		t.Fatalf("expected dense anchor entered replication, got %+v", joinedUpdate)
	}
	for i := denseClusterStart + 1; i < thousandClientCount; i++ {
		drainReplicationUpdates(t, clients[i])
	}
	assertNoPendingMessages(t, denseAnchor)
	assertNoPendingMessages(t, sparse)

	hub.ApplyInput(denseMover, game.InputState{Sequence: 1, Right: true})
	hub.ApplyInput(denseLeaver, game.InputState{Sequence: 1, Right: true})
	simulations <- time.Now()
	hub.ApplyInput(denseLeaver, game.InputState{Sequence: 2, Right: true})
	simulations <- time.Now()
	assertNoPendingMessages(t, sparse)

	broadcasts <- time.Now()
	movementUpdate := mustReceiveReplicationUpdate(t, denseAnchor)
	if len(movementUpdate.LeftPlayerIDs) == 0 && len(movementUpdate.Positions) == 0 {
		t.Fatalf("expected movement replication for dense anchor, got %+v", movementUpdate)
	}
	assertNoPendingMessages(t, sparse)

	updated := game.Appearance{Color: "#ff6600", Shape: game.ShapeDiamond}
	if !hub.UpdateAppearance(denseAnchor.ID(), updated) {
		t.Fatal("appearance update failed")
	}
	broadcasts <- time.Now()
	appearanceUpdate := mustReceiveReplicationUpdate(t, denseAnchor)
	if len(appearanceUpdate.Appearances) != 1 || appearanceUpdate.Appearances[0].Appearance != updated {
		t.Fatalf("unexpected owner appearance replication: %+v", appearanceUpdate)
	}
	drainReplicationUpdates(t, denseMover)

	hub.Unregister(sparse)
	broadcasts <- time.Now()
	assertNoPendingMessages(t, denseAnchor)

	lastDense := clients[thousandClientCount-1]
	hub.Unregister(lastDense)
	broadcasts <- time.Now()
	disconnectUpdate := mustReceiveReplicationUpdate(t, denseAnchor)
	if !containsPlayerID(disconnectUpdate.LeftPlayerIDs, lastDense.ID()) {
		t.Fatalf("expected dense disconnect left replication, got %+v", disconnectUpdate)
	}

	hub.Register(replacement)
	mustReceiveInitialization(t, replacement)
	broadcasts <- time.Now()
	replacementUpdate := mustReceiveReplicationUpdate(t, denseAnchor)
	if len(replacementUpdate.Entered) == 0 {
		t.Fatalf("expected replacement entered replication, got %+v", replacementUpdate)
	}

	broadcasts <- time.Now()
	assertNoPendingMessages(t, denseAnchor)
	assertNoPendingMessages(t, replacement)

	assertAOISymmetry(t, hub)

	statsTick <- time.Now()
	waitForStatsLog(t, &logOutput, "replication_messages=")
	metrics := thousandClientMetrics{}
	parseStatsFromLog(t, logOutput.String(), &metrics)
	t.Logf("aoi scale metrics: %+v", metrics)
	return metrics
}

func thousandPlayerLoader() SavedPlayerLoader {
	config := testAOIConfig()
	return func(userID int64) (SavedPlayerLoad, bool) {
		idx := int(userID) - 1
		if idx < 0 || idx >= thousandClientCount {
			return SavedPlayerLoad{}, false
		}
		localX, localY := playerLocalPosition(idx)
		lat, lng := config.LocalToLatLng(localX, localY)
		return SavedPlayerLoad{
			Username:    fmt.Sprintf("p%04d", idx),
			Lat:         lat,
			Lng:         lng,
			HasPosition: true,
		}, true
	}
}

func playerIDForIndex(index int) int64 {
	return int64(index + 1)
}


func playerLocalPosition(index int) (float64, float64) {
	if index >= denseClusterStart {
		offset := index - denseClusterStart
		return float64(offset%10) * 40, float64(offset/10) * 40
	}
	const cols = 30
	const sparseOriginX = 10000.0
	return sparseOriginX + float64(index%cols)*700, float64(index/cols) * 700
}

func countDenseNeighbors(index int) int {
	selfX, selfY := playerLocalPosition(index)
	count := 0
	for i := denseClusterStart; i < thousandClientCount; i++ {
		if i == index {
			continue
		}
		otherX, otherY := playerLocalPosition(i)
		dx := selfX - otherX
		dy := selfY - otherY
		if dx*dx+dy*dy <= 500*500 {
			count++
		}
	}
	return count
}

func assertVisibleSnapshotMatchesAOI(t *testing.T, hub *Hub, client *testClient, wantOthers int) {
	t.Helper()
	got := len(hub.aoi.VisibleNeighbors(client.ID()))
	if got != wantOthers {
		t.Fatalf("client %d visible neighbors = %d, want %d", client.ID(), got, wantOthers)
	}
}

func assertAOISymmetry(t *testing.T, hub *Hub) {
	t.Helper()
	for clientID := range hub.clients {
		for _, neighborID := range hub.aoi.VisibleNeighbors(clientID) {
			if !hub.isVisibleTo(neighborID, clientID) {
				t.Fatalf("asymmetric visibility: %d sees %d but not vice versa", clientID, neighborID)
			}
		}
	}
}

func containsPlayerID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func drainReplicationUpdates(t *testing.T, client *testClient) {
	t.Helper()
	for {
		select {
		case data := <-client.send:
			var message ReplicationUpdateMessage
			if err := json.Unmarshal(data, &message); err != nil {
				t.Fatalf("decode replication update failed: %v", err)
			}
			if message.Type != MessageTypeReplicationUpdate {
				t.Fatalf("expected replication update, got %q", message.Type)
			}
		default:
			return
		}
	}
}

func assertNoPendingMessages(t *testing.T, client *testClient) {
	t.Helper()
	select {
	case data := <-client.send:
		t.Fatalf("unexpected message for %d: %s", client.ID(), data)
	default:
	}
}

func waitForStatsLog(t *testing.T, logOutput *bytes.Buffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logOutput.String(), needle) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for stats log containing %q:\n%s", needle, logOutput.String())
}

func parseStatsFromLog(t *testing.T, output string, metrics *thousandClientMetrics) {
	t.Helper()
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !strings.Contains(line, "realtime stats") {
			continue
		}
		for _, field := range strings.Fields(line) {
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 {
				continue
			}
			value, err := strconv.ParseUint(parts[1], 10, 64)
			if err != nil {
				continue
			}
			switch parts[0] {
			case "moved_players":
				_ = value
			case "aoi_candidates":
				metrics.AoiCandidatePairs = value
			case "aoi_distance_checks":
				metrics.AoiDistanceChecks = value
			case "aoi_entered":
				metrics.AoiRelationshipsEntered = value
			case "aoi_left":
				metrics.AoiRelationshipsLeft = value
			case "replication_messages":
				metrics.ReplicationMessages = value
			case "replication_recipients":
				metrics.ReplicationRecipients = value
			case "replication_bytes":
				metrics.ReplicationBytes = value
			}
		}
		return
	}
	t.Fatal("stats log line not found")
}
