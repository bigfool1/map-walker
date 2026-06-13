package aoiworkload

import (
	"math"
	"sort"
)

type scenarioRand struct {
	source int64
	state  uint64
}

func newScenarioRand(seed int64) *scenarioRand {
	return &scenarioRand{source: seed, state: uint64(seed)}
}

func (r *scenarioRand) uniform(min, max float64) float64 {
	return min + r.nextFloat()*(max-min)
}

func (r *scenarioRand) nextFloat() float64 {
	r.state = r.state*6364136223846793005 + 1
	return float64(r.state>>11) / float64(1<<53)
}

func (r *scenarioRand) shuffleInts(values []int) {
	for i := len(values) - 1; i > 0; i-- {
		j := int(r.nextFloat() * float64(i+1))
		values[i], values[j] = values[j], values[i]
	}
}

func (r *scenarioRand) pickInt(max int) int {
	if max <= 0 {
		return 0
	}
	return int(r.nextFloat() * float64(max))
}

func (r *scenarioRand) pickDirection() (dx, dy float64) {
	angle := r.uniform(0, 2*math.Pi)
	return math.Cos(angle), math.Sin(angle)
}

func sortedIntKeys(values map[int]struct{}) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func sortedStringKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
