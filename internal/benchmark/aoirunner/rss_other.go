//go:build !linux && !darwin

package aoirunner

func readProcessRSS() (int64, bool, string) {
	return 0, false, "unsupported"
}

func cpuModel() string {
	return ""
}

func totalMemoryBytes() *int64 {
	return nil
}

func totalMemoryAvailable() bool {
	return false
}
