package synthetic

import (
	"math"
	"time"

	"map-walker/internal/game"
)

const (
	BehaviorTickInterval = 100 * time.Millisecond
	softBoundaryMeters   = 1350
)

type BehaviorConfig struct {
	Seed      uint64
	Placement PlacementConfig
	Movement  game.Config
}

func DefaultBehaviorConfig() BehaviorConfig {
	return BehaviorConfig{
		Seed:      0,
		Placement: DefaultPlacementConfig(),
		Movement:  game.DefaultConfig(),
	}
}

type Behavior struct {
	accountNumber int
	cfg           BehaviorConfig

	localX float64
	localY float64

	current        game.InputState
	sequence       uint64
	directionIndex int

	tickCount               int
	nextFirstInputTick      int
	nextDirectionChangeTick int
	started                 bool
}

func NewBehavior(accountNumber int, cfg BehaviorConfig, lat, lng float64) *Behavior {
	localX, localY := LatLngToLocal(cfg.Placement, lat, lng)
	firstInputTick := ActivationStaggerTicks(accountNumber, cfg.Seed) + FirstInputStaggerTicks(accountNumber, cfg.Seed)

	return &Behavior{
		accountNumber:           accountNumber,
		cfg:                     cfg,
		localX:                  localX,
		localY:                  localY,
		nextFirstInputTick:      firstInputTick,
		nextDirectionChangeTick: firstInputTick + DirectionIntervalTicks(accountNumber, cfg.Seed, 0),
	}
}

func ActivationStaggerTicks(accountNumber int, seed uint64) int {
	return int(behaviorRoll(accountNumber, seed, 0, saltActivation) % 10)
}

func FirstInputStaggerTicks(accountNumber int, seed uint64) int {
	return int(behaviorRoll(accountNumber, seed, 0, saltFirstInput) % 10)
}

func ActivationStagger(accountNumber int, seed uint64) time.Duration {
	return time.Duration(ActivationStaggerTicks(accountNumber, seed)) * BehaviorTickInterval
}

func FirstInputStagger(accountNumber int, seed uint64) time.Duration {
	return time.Duration(FirstInputStaggerTicks(accountNumber, seed)) * BehaviorTickInterval
}

func (b *Behavior) EstimatedLocal() (localX, localY float64) {
	return b.localX, b.localY
}

func (b *Behavior) CurrentInput() game.InputState {
	return b.current
}

func (b *Behavior) OnTick() (input game.InputState, changed bool) {
	b.tickCount++

	if b.tickCount < b.nextFirstInputTick {
		return game.InputState{}, false
	}

	if !b.started {
		b.started = true
		b.current = b.selectDirection(0)
		b.sequence = 1
		b.current.Sequence = 1
		b.directionIndex = 0
		return b.current, true
	}

	b.localX, b.localY = AdvanceEstimatedLocalPosition(
		b.localX,
		b.localY,
		b.current,
		b.cfg.Movement,
		BehaviorTickInterval,
	)

	if b.tickCount < b.nextDirectionChangeTick {
		return b.current, false
	}

	b.directionIndex++
	b.nextDirectionChangeTick += DirectionIntervalTicks(b.accountNumber, b.cfg.Seed, b.directionIndex)

	next := b.selectDirection(b.directionIndex)
	if inputStatesEqual(next, b.current) {
		return b.current, false
	}

	b.sequence++
	next.Sequence = b.sequence
	b.current = next
	return b.current, true
}

func (b *Behavior) selectDirection(index int) game.InputState {
	raw := ScheduledDirection(b.accountNumber, b.cfg.Seed, index)
	return ApplySoftBoundaryFilter(raw, b.localX, b.localY)
}

func ScheduledDirection(accountNumber int, seed uint64, index int) game.InputState {
	roll := int(behaviorRoll(accountNumber, seed, index, saltDirection) % 100)
	return directionForRoll(roll)
}

func DirectionInterval(accountNumber int, seed uint64, index int) time.Duration {
	seconds := 1 + int(behaviorRoll(accountNumber, seed, index, saltInterval)%5)
	return time.Duration(seconds) * time.Second
}

func DirectionIntervalTicks(accountNumber int, seed uint64, index int) int {
	return int(DirectionInterval(accountNumber, seed, index) / BehaviorTickInterval)
}

func directionForRoll(roll int) game.InputState {
	switch {
	case roll < 20:
		return game.InputState{}
	case roll < 30:
		return game.InputState{Up: true}
	case roll < 40:
		return game.InputState{Down: true}
	case roll < 50:
		return game.InputState{Left: true}
	case roll < 60:
		return game.InputState{Right: true}
	case roll < 70:
		return game.InputState{Up: true, Right: true}
	case roll < 80:
		return game.InputState{Up: true, Left: true}
	case roll < 90:
		return game.InputState{Down: true, Right: true}
	default:
		return game.InputState{Down: true, Left: true}
	}
}

func ApplySoftBoundaryFilter(input game.InputState, localX, localY float64) game.InputState {
	filtered := input
	if localX > softBoundaryMeters {
		filtered.Right = false
	}
	if localX < -softBoundaryMeters {
		filtered.Left = false
	}
	if localY > softBoundaryMeters {
		filtered.Up = false
	}
	if localY < -softBoundaryMeters {
		filtered.Down = false
	}
	return filtered
}

func AdvanceEstimatedLocalPosition(
	localX, localY float64,
	input game.InputState,
	movement game.Config,
	delta time.Duration,
) (float64, float64) {
	x := boolNumber(input.Right) - boolNumber(input.Left)
	y := boolNumber(input.Up) - boolNumber(input.Down)
	if x == 0 && y == 0 {
		return localX, localY
	}

	length := hypot(x, y)
	x /= length
	y /= length
	distance := movement.SpeedMetersPerSecond * delta.Seconds()
	return localX + x*distance, localY + y*distance
}

func LatLngToLocal(cfg PlacementConfig, lat, lng float64) (localX, localY float64) {
	const metersPerDegreeLatitude = 111_320.0
	localY = (lat - cfg.SpawnLat) * metersPerDegreeLatitude
	localX = (lng - cfg.SpawnLng) * metersPerDegreeLongitude(cfg.SpawnLat)
	return localX, localY
}

func LocalToLatLng(cfg PlacementConfig, localX, localY float64) (lat, lng float64) {
	aoiCfg := game.AOIConfigFromWorld(game.Config{
		SpawnLat: cfg.SpawnLat,
		SpawnLng: cfg.SpawnLng,
	})
	return aoiCfg.LocalToLatLng(localX, localY)
}

func inputStatesEqual(a, b game.InputState) bool {
	return a.Up == b.Up && a.Down == b.Down && a.Left == b.Left && a.Right == b.Right
}

func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func hypot(x, y float64) float64 {
	return math.Hypot(x, y)
}

func metersPerDegreeLongitude(latitude float64) float64 {
	const metersPerDegreeLatitude = 111_320.0
	return metersPerDegreeLatitude * math.Cos(latitude*math.Pi/180)
}
