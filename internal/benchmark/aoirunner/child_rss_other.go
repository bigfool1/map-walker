//go:build !linux && !darwin

package aoirunner

func readChildRSS(pid int) (int64, bool) {
	return 0, false
}
