package aoiworkload

import (
	"math"
	"sort"

	"map-walker/internal/game"
)

type CompactMovementSchedule struct {
	MoverCount  int
	UpdateCount int
	Updates     []UpdateInstructions
}

type UpdateInstructions struct {
	DeltaX   []float64
	DeltaY   []float64
	Patterns []MovementPattern
}

type ExpandedUpdate struct {
	CoreTargets []game.PlayerPosition
	WorldInputs []MoverWorldInputs
}

type MoverWorldInputs struct {
	Tick0 game.InputState
	Tick1 game.InputState
}

type ScheduleExpander struct {
	scenario      *Scenario
	moverLocals   []localPosition
	moverWorldLat []float64
	moverWorldLng []float64
	coreTargets   []game.PlayerPosition
	worldInputs   []MoverWorldInputs
}

func NewScheduleExpander(scenario *Scenario) *ScheduleExpander {
	moverLocals := make([]localPosition, len(scenario.MoverIndices))
	moverWorldLat := make([]float64, len(scenario.MoverIndices))
	moverWorldLng := make([]float64, len(scenario.MoverIndices))
	for slot, playerIdx := range scenario.MoverIndices {
		p := scenario.Players[playerIdx]
		x, y := latLngToLocal(scenario.AOIConfig, p.Lat, p.Lng)
		moverLocals[slot] = localPosition{X: x, Y: y}
		moverWorldLat[slot] = p.Lat
		moverWorldLng[slot] = p.Lng
	}
	return &ScheduleExpander{
		scenario:      scenario,
		moverLocals:   moverLocals,
		moverWorldLat: moverWorldLat,
		moverWorldLng: moverWorldLng,
		coreTargets:   make([]game.PlayerPosition, len(scenario.MoverIndices)),
		worldInputs:   make([]MoverWorldInputs, len(scenario.MoverIndices)),
	}
}

func (e *ScheduleExpander) ExpandUpdate(updateIdx int) ExpandedUpdate {
	instructions := e.scenario.Schedule.Updates[updateIdx]
	cfg := e.scenario.AOIConfig
	for slot, playerIdx := range e.scenario.MoverIndices {
		e.moverLocals[slot].X += instructions.DeltaX[slot]
		e.moverLocals[slot].Y += instructions.DeltaY[slot]
		targetLat, targetLng := cfg.LocalToLatLng(e.moverLocals[slot].X, e.moverLocals[slot].Y)
		player := e.scenario.Players[playerIdx]
		e.coreTargets[slot] = game.PlayerPosition{ID: player.ID, Lat: targetLat, Lng: targetLng}
		e.worldInputs[slot] = worldInputsForTarget(
			e.scenario.WorldConfig,
			e.scenario.AOIConfig,
			e.moverWorldLat[slot], e.moverWorldLng[slot],
			targetLat, targetLng,
			uint64(updateIdx)*2,
		)
		endLat, endLng, _, _ := WorldPositionAfterInputs(
			e.scenario.WorldConfig,
			e.scenario.AOIConfig,
			e.moverWorldLat[slot], e.moverWorldLng[slot],
			e.worldInputs[slot],
		)
		e.moverWorldLat[slot] = endLat
		e.moverWorldLng[slot] = endLng
	}
	return ExpandedUpdate{
		CoreTargets: append([]game.PlayerPosition(nil), e.coreTargets...),
		WorldInputs: append([]MoverWorldInputs(nil), e.worldInputs...),
	}
}

func (e *ScheduleExpander) MoverLocalPosition(slot int) (float64, float64) {
	return e.moverLocals[slot].X, e.moverLocals[slot].Y
}

func deltaToInput(deltaX, deltaY, stepDistance float64, sequence uint64) game.InputState {
	magnitude := math.Hypot(deltaX, deltaY)
	if magnitude < 1e-9 {
		return game.InputState{Sequence: sequence}
	}
	scale := math.Min(stepDistance, magnitude) / magnitude
	stepX := deltaX * scale
	stepY := deltaY * scale
	return game.InputState{
		Sequence: sequence,
		Right:    stepX > 1e-9,
		Left:     stepX < -1e-9,
		Up:       stepY > 1e-9,
		Down:     stepY < -1e-9,
	}
}

func generateMovementSchedule(
	config Config,
	aoiConfig game.AOIConfig,
	moverIndices []int,
	allLocals []localPosition,
	rng *scenarioRand,
) (CompactMovementSchedule, []localPosition) {
	moverCount := len(moverIndices)
	startLocals := make([]localPosition, moverCount)
	for slot, idx := range moverIndices {
		startLocals[slot] = allLocals[idx]
	}

	totalMoves := TotalAOIUpdates * moverCount
	crossingCount := int(math.Round(float64(totalMoves) * (1 - cellLocalFraction)))
	if crossingCount < 0 {
		crossingCount = 0
	}
	localCount := totalMoves - crossingCount

	patternSlots := make([]MovementPattern, 0, totalMoves)
	for i := 0; i < localCount; i++ {
		patternSlots = append(patternSlots, MovementCellLocal)
	}
	for i := 0; i < crossingCount; i++ {
		patternSlots = append(patternSlots, MovementCellCrossing)
	}
	rng.shuffleMovementPatterns(patternSlots)

	schedule := CompactMovementSchedule{
		MoverCount:  moverCount,
		UpdateCount: TotalAOIUpdates,
		Updates:     make([]UpdateInstructions, TotalAOIUpdates),
	}

	current := append([]localPosition(nil), startLocals...)
	slot := 0
	for update := 0; update < TotalAOIUpdates; update++ {
		instructions := UpdateInstructions{
			DeltaX:   make([]float64, moverCount),
			DeltaY:   make([]float64, moverCount),
			Patterns: make([]MovementPattern, moverCount),
		}
		for moverSlot := 0; moverSlot < moverCount; moverSlot++ {
			pattern := patternSlots[slot]
			slot++
			deltaX, deltaY := movementDelta(aoiConfig, current[moverSlot], pattern, rng)
			instructions.DeltaX[moverSlot] = deltaX
			instructions.DeltaY[moverSlot] = deltaY
			instructions.Patterns[moverSlot] = pattern
			current[moverSlot].X += deltaX
			current[moverSlot].Y += deltaY
		}
		schedule.Updates[update] = instructions
	}

	return schedule, startLocals
}

func (r *scenarioRand) shuffleMovementPatterns(patterns []MovementPattern) {
	for i := len(patterns) - 1; i > 0; i-- {
		j := int(r.nextFloat() * float64(i+1))
		patterns[i], patterns[j] = patterns[j], patterns[i]
	}
}

func movementDelta(
	aoiConfig game.AOIConfig,
	pos localPosition,
	pattern MovementPattern,
	rng *scenarioRand,
) (float64, float64) {
	switch pattern {
	case MovementCellCrossing:
		return crossingDelta(aoiConfig, pos, rng)
	default:
		return localDelta(aoiConfig, pos, rng)
	}
}

func localDelta(aoiConfig game.AOIConfig, pos localPosition, rng *scenarioRand) (float64, float64) {
	for attempt := 0; attempt < 16; attempt++ {
		dirX, dirY := rng.pickDirection()
		distance := rng.uniform(0.5, maxMovePerAOIUpdate)
		deltaX := dirX * distance
		deltaY := dirY * distance
		newCell := localToCell(aoiConfig, pos.X+deltaX, pos.Y+deltaY)
		oldCell := localToCell(aoiConfig, pos.X, pos.Y)
		if newCell == oldCell {
			return deltaX, deltaY
		}
	}
	return 0, 0
}

func crossingDelta(aoiConfig game.AOIConfig, pos localPosition, rng *scenarioRand) (float64, float64) {
	cell := localToCell(aoiConfig, pos.X, pos.Y)
	cellSize := aoiConfig.CellSizeMeters
	cellMinX := float64(cell.X) * cellSize
	cellMinY := float64(cell.Y) * cellSize
	cellMaxX := cellMinX + cellSize
	cellMaxY := cellMinY + cellSize

	distances := []struct {
		axis int
		sign float64
		dist float64
	}{
		{0, 1, cellMaxX - pos.X},
		{0, -1, pos.X - cellMinX},
		{1, 1, cellMaxY - pos.Y},
		{1, -1, pos.Y - cellMinY},
	}
	sort.Slice(distances, func(i, j int) bool {
		if distances[i].dist == distances[j].dist {
			return distances[i].axis < distances[j].axis
		}
		return distances[i].dist < distances[j].dist
	})

	for _, candidate := range distances {
		if candidate.dist <= 0 || candidate.dist > maxMovePerAOIUpdate {
			continue
		}
		step := candidate.dist + rng.uniform(0.1, math.Min(1.0, maxMovePerAOIUpdate-candidate.dist))
		if step > maxMovePerAOIUpdate {
			step = maxMovePerAOIUpdate
		}
		var deltaX, deltaY float64
		if candidate.axis == 0 {
			deltaX = candidate.sign * step
		} else {
			deltaY = candidate.sign * step
		}
		newCell := localToCell(aoiConfig, pos.X+deltaX, pos.Y+deltaY)
		if newCell != cell {
			return deltaX, deltaY
		}
	}
	return localDelta(aoiConfig, pos, rng)
}

func (s CompactMovementSchedule) PatternCounts() (local, crossing int) {
	for _, update := range s.Updates {
		for _, pattern := range update.Patterns {
			switch pattern {
			case MovementCellCrossing:
				crossing++
			default:
				local++
			}
		}
	}
	return local, crossing
}

func (s CompactMovementSchedule) TotalMoves() int {
	return s.UpdateCount * s.MoverCount
}
