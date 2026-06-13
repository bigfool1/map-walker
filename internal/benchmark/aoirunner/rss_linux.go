//go:build linux

package aoirunner

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func readProcessRSS() (bytes int64, available bool, source string) {
	current, ok := linuxCurrentRSS()
	if ok {
		return current, true, "linux_proc_status_vm_rss"
	}
	peak, ok := linuxPeakRSS()
	if ok {
		return peak, true, "linux_getrusage_maxrss"
	}
	return 0, false, "linux_unavailable"
}

func linuxCurrentRSS() (int64, bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kilobytes * 1024, true
	}
	return 0, false
}

func linuxPeakRSS() (int64, bool) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, false
	}
	return int64(usage.Maxrss) * 1024, true
}

func cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func totalMemoryBytes() *int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil
		}
		kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil
		}
		value := kilobytes * 1024
		return &value
	}
	return nil
}

func totalMemoryAvailable() bool {
	data, err := os.ReadFile("/proc/meminfo")
	return err == nil && len(data) > 0
}
