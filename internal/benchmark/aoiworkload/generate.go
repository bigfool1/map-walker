package aoiworkload

import (
	"fmt"
	"math"
	"sort"

	"map-walker/internal/game"
)

const (
	hotspotFraction      = 0.01
	cellLocalFraction    = 0.80
	maxMovePerAOIUpdate  = 6.0
	perturbationFraction = 0.15
)

type localPosition struct {
	X float64
	Y float64
}

func Generate(config Config) (*Scenario, error) {
	if config.Scale <= 0 {
		return nil, fmt.Errorf("scale must be positive")
	}
	if config.MoverCount <= 0 {
		return nil, fmt.Errorf("mover count must be positive")
	}
	if config.MoverCount > config.Scale {
		return nil, fmt.Errorf("mover count %d exceeds scale %d", config.MoverCount, config.Scale)
	}

	worldConfig := DefaultWorldConfig()
	aoiConfig := game.AOIConfigFromWorld(worldConfig)
	maxStep := worldConfig.SpeedMetersPerSecond * float64(SimTickIntervalMs) / 1000.0 * 2
	if maxStep > maxMovePerAOIUpdate+1e-9 {
		return nil, fmt.Errorf("world max move per AOI update %v exceeds workload limit %v", maxStep, maxMovePerAOIUpdate)
	}

	rng := newScenarioRand(config.Seed)
	players, hotspotCount := generatePlayers(config, aoiConfig, rng)
	buildOrder := generateBuildOrder(config.Scale, rng)
	moverIndices := selectMovers(config.Scale, config.MoverCount, rng)

	localPositions := make([]localPosition, len(players))
	for i, p := range players {
		x, y := latLngToLocal(aoiConfig, p.Lat, p.Lng)
		localPositions[i] = localPosition{X: x, Y: y}
	}

	schedule, moverLocals := generateMovementSchedule(
		config,
		aoiConfig,
		moverIndices,
		localPositions,
		rng,
	)

	initialDensity := measureDensitySample(players, buildOrder, aoiConfig, nil)
	steadyPositions := applyTrajectoryPositions(aoiConfig, players, moverIndices, moverLocals, schedule, WarmupAOIUpdates)
	steadyDensity := measureDensitySample(players, buildOrder, aoiConfig, steadyPositions)
	churn := measureVisibilityChurn(players, buildOrder, moverIndices, aoiConfig, schedule, moverLocals)

	return &Scenario{
		Config:             config,
		WorldConfig:        worldConfig,
		AOIConfig:          aoiConfig,
		Players:            players,
		BuildOrder:         buildOrder,
		MoverIndices:       moverIndices,
		HotspotCount:       hotspotCount,
		Schedule:           schedule,
		InitialDensity:     initialDensity,
		SteadyStateDensity: steadyDensity,
		VisibilityChurn:    churn,
	}, nil
}

func generateBuildOrder(count int, rng *scenarioRand) []int {
	order := make([]int, count)
	for i := range order {
		order[i] = i
	}
	rng.shuffleInts(order)
	return order
}

func selectMovers(scale, moverCount int, rng *scenarioRand) []int {
	indices := make([]int, scale)
	for i := range indices {
		indices[i] = i
	}
	rng.shuffleInts(indices)
	selected := indices[:moverCount]
	sort.Ints(selected)
	return selected
}

func gridSpacing(density Density, hotspot bool) float64 {
	if hotspot {
		return 47.0
	}
	switch density {
	case DensitySparse:
		return 280.0
	case DensityNormal:
		return 125.0
	default:
		return 125.0
	}
}

func generatePlayers(config Config, aoiConfig game.AOIConfig, rng *scenarioRand) ([]PlayerPlacement, int) {
	spacing := gridSpacing(config.Density, false)
	players := make([]PlayerPlacement, config.Scale)
	perturb := spacing * perturbationFraction
	gridWidth := gridWidthForScale(config.Scale)

	for i := 0; i < config.Scale; i++ {
		row := i / gridWidth
		col := i % gridWidth
		baseX := float64(col) * spacing
		baseY := float64(row) * spacing
		localX := baseX + rng.uniform(-perturb, perturb)
		localY := baseY + rng.uniform(-perturb, perturb)
		lat, lng := aoiConfig.LocalToLatLng(localX, localY)
		players[i] = PlayerPlacement{
			ID:  playerID(i),
			Lat: lat,
			Lng: lng,
		}
	}

	hotspotCount := 0
	if config.Density == DensityHotspot && config.Scale >= 100 {
		hotspotCount = config.Scale / 100
	}
	if hotspotCount == 0 {
		return players, 0
	}

	indices := make([]int, config.Scale)
	for i := range indices {
		indices[i] = i
	}
	rng.shuffleInts(indices)
	hotspotIndices := indices[:hotspotCount]

	centers := generateHotspotCenters(hotspotCount, config, rng)
	hotspotSpacing := gridSpacing(config.Density, true)
	for i, playerIdx := range hotspotIndices {
		center := centers[i]
		localX := center.X + rng.uniform(-hotspotSpacing, hotspotSpacing)
		localY := center.Y + rng.uniform(-hotspotSpacing, hotspotSpacing)
		lat, lng := aoiConfig.LocalToLatLng(localX, localY)
		players[playerIdx] = PlayerPlacement{
			ID:  playerID(playerIdx),
			Lat: lat,
			Lng: lng,
		}
	}

	return players, hotspotCount
}

func generateHotspotCenters(count int, config Config, rng *scenarioRand) []localPosition {
	if count == 0 {
		return nil
	}
	normalSpacing := gridSpacing(config.Density, false)
	regionCount := int(math.Max(4, math.Ceil(math.Sqrt(float64(count)/100.0))))
	centers := make([]localPosition, count)
	for i := range centers {
		region := i % regionCount
		regionRow := region / int(math.Ceil(math.Sqrt(float64(regionCount))))
		regionCol := region % int(math.Ceil(math.Sqrt(float64(regionCount))))
		baseX := float64(regionCol+1) * normalSpacing * float64(gridWidthForScale(config.Scale))
		baseY := float64(regionRow+1) * normalSpacing * float64(gridWidthForScale(config.Scale))
		centers[i] = localPosition{
			X: baseX + rng.uniform(-normalSpacing, normalSpacing),
			Y: baseY + rng.uniform(-normalSpacing, normalSpacing),
		}
	}
	return centers
}

func gridWidthForScale(scale int) int {
	return int(math.Ceil(math.Sqrt(float64(scale))))
}

func playerID(index int) string {
	return fmt.Sprintf("bench-player-%09d", index)
}

func measureDensitySample(
	players []PlayerPlacement,
	buildOrder []int,
	aoiConfig game.AOIConfig,
	overridePositions map[int]localPosition,
) DensitySample {
	aoi := game.NewAOIIndex(aoiConfig)
	for _, idx := range buildOrder {
		p := players[idx]
		lat, lng := p.Lat, p.Lng
		if overridePositions != nil {
			if pos, ok := overridePositions[idx]; ok {
				lat, lng = aoiConfig.LocalToLatLng(pos.X, pos.Y)
			}
		}
		aoi.Insert(p.ID, lat, lng)
	}

	sampleIndices := densitySampleIndices(len(players))
	counts := make([]int, 0, len(sampleIndices))
	for _, idx := range sampleIndices {
		id := players[idx].ID
		counts = append(counts, len(aoi.VisibleNeighbors(id)))
	}
	return densityStats(counts)
}

func densitySampleIndices(count int) []int {
	if count <= 32 {
		indices := make([]int, count)
		for i := range indices {
			indices[i] = i
		}
		return indices
	}
	step := count / 32
	if step < 1 {
		step = 1
	}
	indices := make([]int, 0, 32)
	for i := 0; i < count && len(indices) < 32; i += step {
		indices = append(indices, i)
	}
	return indices
}

func densityStats(counts []int) DensitySample {
	if len(counts) == 0 {
		return DensitySample{}
	}
	sum := 0.0
	minVal := counts[0]
	maxVal := counts[0]
	for _, c := range counts {
		sum += float64(c)
		if c < minVal {
			minVal = c
		}
		if c > maxVal {
			maxVal = c
		}
	}
	return DensitySample{
		Mean: sum / float64(len(counts)),
		Min:  float64(minVal),
		Max:  float64(maxVal),
	}
}

func applyTrajectoryPositions(
	aoiConfig game.AOIConfig,
	players []PlayerPlacement,
	moverIndices []int,
	startLocals []localPosition,
	schedule CompactMovementSchedule,
	updates int,
) map[int]localPosition {
	positions := make(map[int]localPosition, len(players))
	for i, p := range players {
		x, y := latLngToLocal(aoiConfig, p.Lat, p.Lng)
		positions[i] = localPosition{X: x, Y: y}
	}
	moverPos := append([]localPosition(nil), startLocals...)
	for update := 0; update < updates; update++ {
		instructions := schedule.Updates[update]
		for moverSlot, playerIdx := range moverIndices {
			moverPos[moverSlot].X += instructions.DeltaX[moverSlot]
			moverPos[moverSlot].Y += instructions.DeltaY[moverSlot]
			positions[playerIdx] = moverPos[moverSlot]
		}
	}
	return positions
}

func measureVisibilityChurn(
	players []PlayerPlacement,
	buildOrder []int,
	moverIndices []int,
	aoiConfig game.AOIConfig,
	schedule CompactMovementSchedule,
	startLocals []localPosition,
) VisibilityChurnStats {
	aoi := game.NewAOIIndex(aoiConfig)
	for _, idx := range buildOrder {
		p := players[idx]
		aoi.Insert(p.ID, p.Lat, p.Lng)
	}

	moverPos := append([]localPosition(nil), startLocals...)
	perMoverChurn := make([]int, len(moverIndices))
	for update := 0; update < TotalAOIUpdates; update++ {
		instructions := schedule.Updates[update]
		for slot, playerIdx := range moverIndices {
			moverPos[slot].X += instructions.DeltaX[slot]
			moverPos[slot].Y += instructions.DeltaY[slot]
			lat, lng := aoiConfig.LocalToLatLng(moverPos[slot].X, moverPos[slot].Y)
			changes := aoi.Move(players[playerIdx].ID, lat, lng)
			perMoverChurn[slot] += len(changes.Entered) + len(changes.Left)
		}
	}
	return percentileChurnStats(perMoverChurn)
}

func percentileChurnStats(values []int) VisibilityChurnStats {
	if len(values) == 0 {
		return VisibilityChurnStats{}
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)

	sum := 0
	maxVal := sorted[len(sorted)-1]
	for _, v := range sorted {
		sum += v
	}
	return VisibilityChurnStats{
		Mean: float64(sum) / float64(len(sorted)),
		P50:  float64(sorted[percentileIndex(len(sorted), 0.50)]),
		P95:  float64(sorted[percentileIndex(len(sorted), 0.95)]),
		Max:  maxVal,
	}
}

func percentileIndex(count int, p float64) int {
	if count == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(count))) - 1
	if idx < 0 {
		return 0
	}
	if idx >= count {
		return count - 1
	}
	return idx
}
