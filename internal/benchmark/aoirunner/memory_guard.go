package aoirunner

import "time"

const DefaultScenarioRepeats = 3

var DefaultMatrixTimeout = 15 * time.Minute

func DefaultMemoryGuardBytes() int64 {
	if !totalMemoryAvailable() {
		return 0
	}
	total := totalMemoryBytes()
	if total == nil {
		return 0
	}
	return (*total * 75) / 100
}
