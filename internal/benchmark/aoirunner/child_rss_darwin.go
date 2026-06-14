//go:build darwin

package aoirunner

import (
	"os/exec"
	"strconv"
	"strings"
)

func readChildRSS(pid int) (int64, bool) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	kilobytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, false
	}
	return kilobytes * 1024, true
}
