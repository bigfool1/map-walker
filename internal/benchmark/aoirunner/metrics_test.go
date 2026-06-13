package aoirunner

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
)

func TestDurationPercentilesWithFixedSamples(t *testing.T) {
	samples := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		100 * time.Millisecond,
	}
	stats := DurationStatsFromSamples(samples)
	if stats.MedianNs != (30 * time.Millisecond).Nanoseconds() {
		t.Fatalf("median=%d want %d", stats.MedianNs, (30 * time.Millisecond).Nanoseconds())
	}
	if stats.P95Ns != (100 * time.Millisecond).Nanoseconds() {
		t.Fatalf("p95=%d want %d", stats.P95Ns, (100 * time.Millisecond).Nanoseconds())
	}
	if stats.P99Ns != (100 * time.Millisecond).Nanoseconds() {
		t.Fatalf("p99=%d want %d", stats.P99Ns, (100 * time.Millisecond).Nanoseconds())
	}
	if stats.MaxNs != (100 * time.Millisecond).Nanoseconds() {
		t.Fatalf("max=%d want %d", stats.MaxNs, (100 * time.Millisecond).Nanoseconds())
	}
	if stats.Class != MetricPrimary {
		t.Fatalf("class=%s want primary", stats.Class)
	}
}

func TestDurationAndByteNormalization(t *testing.T) {
	if got := NormalizeDuration(1500 * time.Microsecond); got != 1_500_000 {
		t.Fatalf("duration normalization=%d", got)
	}
	if got := NormalizeBytes(4096); got != 4096 {
		t.Fatalf("byte normalization=%d", got)
	}
}

func TestHeapAndGCDeltaDoNotUnderflow(t *testing.T) {
	before := MemSnapshot{
		HeapAlloc:    100,
		TotalAlloc:   500,
		NumGC:        5,
		PauseTotalNs: 1_000,
	}
	after := MemSnapshot{
		HeapAlloc:    50,
		TotalAlloc:   450,
		NumGC:        4,
		PauseTotalNs: 900,
	}
	heap := HeapDelta(before, after)
	if heap.DeltaHeapAllocBytes != 0 || heap.DeltaTotalAllocBytes != 0 {
		t.Fatalf("heap delta underflowed: %+v", heap)
	}
	gc := GCDelta(before, after)
	if gc.DeltaNumGC != 0 || gc.DeltaTotalPauseNs != 0 {
		t.Fatalf("gc delta underflowed: %+v", gc)
	}
}

func TestHeapAndGCDeltaPositive(t *testing.T) {
	before := MemSnapshot{HeapAlloc: 100, TotalAlloc: 500, NumGC: 4, PauseTotalNs: 900}
	after := MemSnapshot{HeapAlloc: 150, TotalAlloc: 700, NumGC: 6, PauseTotalNs: 1_500}
	heap := HeapDelta(before, after)
	if heap.DeltaHeapAllocBytes != 50 || heap.DeltaTotalAllocBytes != 200 {
		t.Fatalf("heap delta=%+v", heap)
	}
	gc := GCDelta(before, after)
	if gc.DeltaNumGC != 2 || gc.DeltaTotalPauseNs != 600 {
		t.Fatalf("gc delta=%+v", gc)
	}
}

func TestNaturalGCEnvironmentDoesNotMutateRuntimeSettings(t *testing.T) {
	beforeGOMAXPROCS := runtime.GOMAXPROCS(0)
	before := CaptureMemSnapshot()
	if !VerifyNaturalGCEnvironment() {
		t.Fatal("expected natural GC environment")
	}
	after := CaptureMemSnapshot()
	if runtime.GOMAXPROCS(0) != beforeGOMAXPROCS {
		t.Fatal("GOMAXPROCS changed during capture")
	}
	_ = before
	_ = after
	if NaturalGCRunPolicy() != "natural_gc_run" {
		t.Fatal("unexpected natural GC policy label")
	}
}

func TestCaptureEnvironmentMetadata(t *testing.T) {
	env := CaptureEnvironment(EnvironmentCaptureOptions{
		Arguments: []string{"aoi-bench", "--seed", "42"},
		Now:       time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	})
	if env.BaselineKind != SerialCoreBaselineKind {
		t.Fatalf("baseline kind=%q", env.BaselineKind)
	}
	if env.GoVersion == "" || env.GOOS == "" || env.GOARCH == "" {
		t.Fatal("expected runtime metadata")
	}
	if env.GOMAXPROCS <= 0 {
		t.Fatalf("gomaxprocs=%d", env.GOMAXPROCS)
	}
	if env.GOGC == "" {
		t.Fatal("expected effective GOGC")
	}
	if env.CommitSHA == "" {
		t.Fatal("expected commit sha in git repo")
	}
	if env.Arguments[0] != "aoi-bench" {
		t.Fatalf("arguments=%v", env.Arguments)
	}
}

func TestGOMAXPROCSRecordedWithoutOverride(t *testing.T) {
	current := runtime.GOMAXPROCS(0)
	env := CaptureEnvironment(EnvironmentCaptureOptions{})
	if env.GOMAXPROCS != current {
		t.Fatalf("gomaxprocs=%d want %d", env.GOMAXPROCS, current)
	}
}

func TestProcessRSSReturnsBytesOrUnavailable(t *testing.T) {
	reader := NewRSSReader()
	reader.Sample()
	snapshot := reader.Snapshot()
	if snapshot.Class != MetricPrimary {
		t.Fatalf("class=%s", snapshot.Class)
	}
	if snapshot.Available {
		if snapshot.PeakBytes <= 0 {
			t.Fatalf("peak rss=%d", snapshot.PeakBytes)
		}
		if snapshot.Source == "" {
			t.Fatal("expected rss source")
		}
	} else if snapshot.Source == "" {
		t.Fatal("expected explicit unavailable source")
	}
}

func TestJSONRoundTripForAllStatuses(t *testing.T) {
	exitCode := 1
	memoryLimit := int64(1024)
	for _, status := range AllStatuses() {
		t.Run(string(status), func(t *testing.T) {
			original := sampleResult(status, &exitCode, &memoryLimit)
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := ParseJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Status != status {
				t.Fatalf("status=%s want %s", decoded.Status, status)
			}
			if decoded.Identity.Key() != original.Identity.Key() {
				t.Fatalf("identity key mismatch")
			}
			if decoded.TickDuration == nil || decoded.TickDuration.MedianNs != original.TickDuration.MedianNs {
				t.Fatal("tick duration lost in json round trip")
			}
			if decoded.Heap == nil || decoded.Heap.Class != MetricPrimary {
				t.Fatal("heap classification lost in json round trip")
			}
			if decoded.AOI == nil || decoded.AOI.Class != MetricDiagnostic {
				t.Fatal("aoi classification lost in json round trip")
			}
		})
	}
}

func TestMetricClassificationSurvivesCSV(t *testing.T) {
	result := sampleResult(StatusSuccess, nil, nil)
	var buffer bytes.Buffer
	if err := WriteCSV(&buffer, []Result{result}); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&buffer).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("rows=%d want 2", len(records))
	}
	header := records[0]
	row := records[1]
	if !strings.Contains(strings.Join(header, ","), "tick_median_class") {
		t.Fatal("csv missing metric class columns")
	}
	medianClassIdx := indexOf(header, "tick_median_class")
	if row[medianClassIdx] != string(MetricPrimary) {
		t.Fatalf("tick class=%s", row[medianClassIdx])
	}
	diagnosticIdx := indexOf(header, "candidate_pairs_class")
	if row[diagnosticIdx] != string(MetricDiagnostic) {
		t.Fatalf("aoi class=%s", row[diagnosticIdx])
	}
}

func TestVisibilityChurnFromWorkload(t *testing.T) {
	metric := VisibilityChurnFromWorkload(aoiworkload.VisibilityChurnStats{
		Mean: 3,
		P50:  2,
		P95:  8,
		Max:  10,
	})
	if metric.Class != MetricPrimary || metric.Max != 10 {
		t.Fatalf("metric=%+v", metric)
	}
}

func TestThroughputFromMoves(t *testing.T) {
	stats := ThroughputFromMoves(1000, time.Second)
	if stats.MovesPerSecond != 1000 {
		t.Fatalf("throughput=%v", stats.MovesPerSecond)
	}
	if stats.Class != MetricPrimary {
		t.Fatalf("class=%s", stats.Class)
	}
}

func sampleResult(status Status, exitCode *int, memoryLimit *int64) Result {
	return Result{
		Identity: ScenarioIdentityFromConfig(ModeCoreTick, aoiworkload.Config{
			Scale:      100_000,
			MoverCount: 10_000,
			Density:    aoiworkload.DensityNormal,
			Seed:       42,
		}, 1),
		Environment: CaptureEnvironment(EnvironmentCaptureOptions{
			Arguments: []string{"aoi-bench"},
			Now:       time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		}),
		Status:       status,
		Phase:        PhaseMeasuredTicks,
		ErrorSummary: "sample",
		ElapsedNs:    int64(time.Second),
		TickDuration: &DurationStats{
			Class:    MetricPrimary,
			MedianNs: int64(50 * time.Millisecond),
			P95Ns:    int64(80 * time.Millisecond),
			P99Ns:    int64(90 * time.Millisecond),
			MaxNs:    int64(100 * time.Millisecond),
		},
		Throughput: &ThroughputStats{Class: MetricPrimary, MovesPerSecond: 100_000},
		VisibilityChurn: &VisibilityChurnMetric{
			Class: MetricPrimary,
			Mean:  2.5,
			P50:   2,
			P95:   6,
			Max:   8,
		},
		Heap: &HeapSnapshot{Class: MetricPrimary, HeapAllocBytes: 128},
		GC:   &GCSnapshot{Class: MetricPrimary, TotalPauseNs: 1_000},
		RSS:  &RSSSnapshot{Class: MetricPrimary, PeakBytes: 4096, Available: true, Source: "test"},
		AOI: &AOICounters{
			Class:          MetricDiagnostic,
			CandidatePairs: 100,
			DistanceChecks: 200,
		},
		ExitCode:         exitCode,
		MemoryLimitBytes: memoryLimit,
	}
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}
