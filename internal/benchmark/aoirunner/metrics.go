package aoirunner

import (
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

type MemSnapshot struct {
	HeapAlloc  uint64
	HeapInuse  uint64
	HeapObjects uint64
	TotalAlloc uint64
	NumGC      uint32
	PauseTotalNs uint64
}

func CaptureMemSnapshot() MemSnapshot {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return MemSnapshot{
		HeapAlloc:    stats.HeapAlloc,
		HeapInuse:    stats.HeapInuse,
		HeapObjects:  stats.HeapObjects,
		TotalAlloc:   stats.TotalAlloc,
		NumGC:        stats.NumGC,
		PauseTotalNs: stats.PauseTotalNs,
	}
}

func HeapSnapshotFromMem(after MemSnapshot) HeapSnapshot {
	return HeapSnapshot{
		Class:           MetricPrimary,
		HeapAllocBytes:  after.HeapAlloc,
		HeapInuseBytes:  after.HeapInuse,
		HeapObjects:     after.HeapObjects,
		TotalAllocBytes: after.TotalAlloc,
	}
}

func HeapDelta(before, after MemSnapshot) HeapSnapshot {
	snapshot := HeapSnapshotFromMem(after)
	snapshot.DeltaHeapAllocBytes = subUint64(after.HeapAlloc, before.HeapAlloc)
	snapshot.DeltaTotalAllocBytes = subUint64(after.TotalAlloc, before.TotalAlloc)
	return snapshot
}

func GCSnapshotFromMem(after MemSnapshot) GCSnapshot {
	return GCSnapshot{
		Class:        MetricPrimary,
		NumGC:        after.NumGC,
		TotalPauseNs: after.PauseTotalNs,
		MaxPauseNs:   after.PauseTotalNs,
	}
}

func GCDelta(before, after MemSnapshot) GCSnapshot {
	snapshot := GCSnapshotFromMem(after)
	snapshot.DeltaNumGC = subUint32(after.NumGC, before.NumGC)
	snapshot.DeltaTotalPauseNs = subUint64(after.PauseTotalNs, before.PauseTotalNs)
	snapshot.MaxPauseNs = snapshot.DeltaTotalPauseNs
	return snapshot
}

func DurationStatsFromSamples(samples []time.Duration) DurationStats {
	if len(samples) == 0 {
		return DurationStats{Class: MetricPrimary}
	}
	sorted := append([]time.Duration(nil), samples...)
	sortDurations(sorted)
	return DurationStats{
		Class:    MetricPrimary,
		MedianNs: sorted[percentileIndex(len(sorted), 0.50)].Nanoseconds(),
		P95Ns:    sorted[percentileIndex(len(sorted), 0.95)].Nanoseconds(),
		P99Ns:    sorted[percentileIndex(len(sorted), 0.99)].Nanoseconds(),
		MaxNs:    sorted[len(sorted)-1].Nanoseconds(),
	}
}

func DurationStatsFromNanoseconds(samples []int64) DurationStats {
	durations := make([]time.Duration, len(samples))
	for i, sample := range samples {
		durations[i] = time.Duration(sample)
	}
	return DurationStatsFromSamples(durations)
}

func ThroughputFromMoves(moves int, elapsed time.Duration) ThroughputStats {
	if elapsed <= 0 {
		return ThroughputStats{Class: MetricPrimary}
	}
	seconds := elapsed.Seconds()
	return ThroughputStats{
		Class:          MetricPrimary,
		MovesPerSecond: float64(moves) / seconds,
	}
}

func NormalizeDuration(value time.Duration) int64 {
	return value.Nanoseconds()
}

func NormalizeBytes(value uint64) uint64 {
	return value
}

func subUint64(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

func subUint32(a, b uint32) uint32 {
	if a < b {
		return 0
	}
	return a - b
}

func percentileIndex(count int, percentile float64) int {
	if count == 0 {
		return 0
	}
	index := int(float64(count)*percentile+0.999999999) - 1
	if index < 0 {
		return 0
	}
	if index >= count {
		return count - 1
	}
	return index
}

func sortDurations(values []time.Duration) {
	for i := 1; i < len(values); i++ {
		j := i
		for j > 0 && values[j-1] > values[j] {
			values[j-1], values[j] = values[j], values[j-1]
			j--
		}
	}
}

type EnvironmentCaptureOptions struct {
	Arguments []string
	Now       time.Time
}

func CaptureEnvironment(opts EnvironmentCaptureOptions) EnvironmentMetadata {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	commitSHA, dirty := gitCommitMetadata()
	return EnvironmentMetadata{
		Timestamp:            opts.Now,
		BaselineKind:         SerialCoreBaselineKind,
		GoVersion:            runtime.Version(),
		GOOS:                 runtime.GOOS,
		GOARCH:               runtime.GOARCH,
		NumCPU:               runtime.NumCPU(),
		GOMAXPROCS:           runtime.GOMAXPROCS(0),
		GOGC:                 effectiveGOGC(),
		GOMEMLIMIT:           os.Getenv("GOMEMLIMIT"),
		CommitSHA:            commitSHA,
		DirtyWorktree:        dirty,
		Arguments:            append([]string(nil), opts.Arguments...),
		DependencyVersions:   moduleVersions(),
		CPUModel:             cpuModel(),
		TotalMemoryBytes:     totalMemoryBytes(),
		TotalMemoryAvailable: totalMemoryAvailable(),
	}
}

func effectiveGOGC() string {
	if value := strings.TrimSpace(os.Getenv("GOGC")); value != "" {
		return value
	}
	return "100"
}

func moduleVersions() map[string]string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	versions := map[string]string{
		"map-walker": info.Main.Version,
	}
	for _, dep := range info.Deps {
		if dep == nil {
			continue
		}
		versions[dep.Path] = dep.Version
	}
	return versions
}

func gitCommitMetadata() (commit string, dirty bool) {
	commitCmd := exec.Command("git", "rev-parse", "HEAD")
	commitOutput, err := commitCmd.Output()
	if err != nil {
		return "", false
	}
	commit = strings.TrimSpace(string(commitOutput))

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusOutput, err := statusCmd.Output()
	if err != nil {
		return commit, false
	}
	dirty = strings.TrimSpace(string(statusOutput)) != ""
	return commit, dirty
}

type RSSReader struct {
	peakBytes int64
	available bool
	source    string
}

func NewRSSReader() *RSSReader {
	bytes, available, source := readProcessRSS()
	return &RSSReader{
		peakBytes: bytes,
		available: available,
		source:    source,
	}
}

func (r *RSSReader) Sample() {
	bytes, available, source := readProcessRSS()
	if !available {
		r.available = false
		r.source = source
		return
	}
	r.available = true
	r.source = source
	if bytes > r.peakBytes {
		r.peakBytes = bytes
	}
}

func (r *RSSReader) Snapshot() RSSSnapshot {
	return RSSSnapshot{
		Class:     MetricPrimary,
		PeakBytes: r.peakBytes,
		Available: r.available,
		Source:    r.source,
	}
}

func NaturalGCRunPolicy() string {
	return "natural_gc_run"
}

func VerifyNaturalGCEnvironment() bool {
	return runtime.GOMAXPROCS(0) > 0
}
