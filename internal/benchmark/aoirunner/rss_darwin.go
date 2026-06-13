//go:build darwin

package aoirunner

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func readProcessRSS() (bytes int64, available bool, source string) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, false, "darwin_unavailable"
	}
	return int64(usage.Maxrss), true, "darwin_getrusage_maxrss"
}

func cpuModel() string {
	output, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func totalMemoryBytes() *int64 {
	output, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

func totalMemoryAvailable() bool {
	output, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	return err == nil && len(output) > 0
}
