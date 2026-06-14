package aoirunner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"map-walker/internal/benchmark/aoirunner"
	"map-walker/internal/benchmark/aoiworkload"
)

type mockBenchRunner struct {
	responses []mockResponse
	calls     int
}

type mockResponse struct {
	stdout       []byte
	stderr       string
	exitCode     int
	signal       string
	elapsed      time.Duration
	terminatedBy string
}

func (m *mockBenchRunner) Run(ctx context.Context, binary string, args []string, maxRSS int64) aoirunner.BenchRunOutcome {
	if m.calls >= len(m.responses) {
		return aoirunner.BenchRunOutcome{ExitCode: 1, Stderr: "unexpected call"}
	}
	response := m.responses[m.calls]
	m.calls++
	return aoirunner.BenchRunOutcome{
		Stdout:       response.stdout,
		Stderr:       response.stderr,
		ExitCode:     response.exitCode,
		Signal:       response.signal,
		Elapsed:      response.elapsed,
		TerminatedBy: response.terminatedBy,
	}
}

func TestBaselineMatrixMembership(t *testing.T) {
	defs := aoirunner.BaselineMatrixDefinitions(42)
	if len(defs) != 14 {
		t.Fatalf("baseline definitions=%d want 14", len(defs))
	}
	var builds, core, world int
	for _, def := range defs {
		switch def.Mode {
		case aoirunner.ModeBuild:
			builds++
		case aoirunner.ModeCoreTick:
			core++
		case aoirunner.ModeWorldAOI:
			world++
		}
	}
	if builds != 4 || core != 5 || world != 5 {
		t.Fatalf("build=%d core=%d world=%d", builds, core, world)
	}
	if defs[0].Config.Scale > defs[len(defs)-1].Config.Scale {
		t.Fatal("expected ascending scale order")
	}
}

func TestFullMatrixExpansion(t *testing.T) {
	defs := aoirunner.FullMatrixDefinitions(42)
	if len(defs) == 0 {
		t.Fatal("expected full matrix definitions")
	}
	if len(defs) <= len(aoirunner.BaselineMatrixDefinitions(42)) {
		t.Fatal("full matrix should exceed baseline")
	}
}

func TestDefaultMatrixTimeoutAndMemoryGuard(t *testing.T) {
	if aoirunner.DefaultMatrixTimeout != 15*time.Minute {
		t.Fatalf("timeout=%v", aoirunner.DefaultMatrixTimeout)
	}
	if aoirunner.ResolveMemoryGuard(-1) != 0 {
		t.Fatal("expected disabled memory guard")
	}
	if guard := aoirunner.ResolveMemoryGuard(0); aoirunner.DefaultMemoryGuardBytes() > 0 && guard != aoirunner.DefaultMemoryGuardBytes() {
		t.Fatalf("resolve default guard=%d want %d", guard, aoirunner.DefaultMemoryGuardBytes())
	}
	if aoirunner.ResolveMemoryGuard(4096) != 4096 {
		t.Fatal("expected explicit max rss")
	}
}

func TestAggregateRepeatMinMedianMax(t *testing.T) {
	config := aoiworkload.Config{Scale: 128, MoverCount: 16, Density: aoiworkload.DensityNormal, Seed: 1}
	def := aoirunner.ScenarioDefinition{Mode: aoirunner.ModeCoreTick, Config: config}
	records := []aoirunner.MatrixRepeatRecord{
		makeRepeatRecord(def, 1, aoirunner.StatusSuccess, 10, 1000),
		makeRepeatRecord(def, 2, aoirunner.StatusSuccess, 20, 2000),
		makeRepeatRecord(def, 3, aoirunner.StatusTimeout, 30, 3000),
	}
	aggregates := aoirunner.AggregateMatrixRepeats(records)
	if len(aggregates) != 1 {
		t.Fatalf("aggregates=%d", len(aggregates))
	}
	stats := aggregates[0]
	if stats.RepeatMediansNs.Min != 10 || stats.RepeatMediansNs.Median != 20 || stats.RepeatMediansNs.Max != 30 {
		t.Fatalf("median stats=%+v", stats.RepeatMediansNs)
	}
	if stats.RepeatPeakRSS.Min != 1000 || stats.RepeatPeakRSS.Max != 3000 {
		t.Fatalf("peak rss=%+v", stats.RepeatPeakRSS)
	}
	if len(stats.RepeatStatuses) != 3 || stats.RepeatStatuses[2] != aoirunner.StatusTimeout {
		t.Fatalf("statuses=%v", stats.RepeatStatuses)
	}
}

func TestMatrixAggregatesFailureWithoutMasking(t *testing.T) {
	config := aoiworkload.Config{Scale: 128, MoverCount: 16, Density: aoiworkload.DensityNormal, Seed: 1}
	def := aoirunner.ScenarioDefinition{Mode: aoirunner.ModeCoreTick, Config: config}
	records := []aoirunner.MatrixRepeatRecord{
		makeRepeatRecord(def, 1, aoirunner.StatusSuccess, 10, 1000),
		makeRepeatRecord(def, 2, aoirunner.StatusRuntimeError, 0, 0),
	}
	report := aoirunner.MatrixReport{
		Repeats:    records,
		Aggregates: aoirunner.AggregateMatrixRepeats(records),
	}
	if len(report.Repeats) != 2 {
		t.Fatal("failed repeat should remain visible")
	}
	if report.Aggregates[0].RepeatStatuses[1] != aoirunner.StatusRuntimeError {
		t.Fatalf("statuses=%v", report.Aggregates[0].RepeatStatuses)
	}
}

func TestRunMatrixThreeRepeatsPerScenario(t *testing.T) {
	config := aoiworkload.Config{Scale: 128, MoverCount: 16, Density: aoiworkload.DensityNormal, Seed: 1}
	success := mustJSONResult(t, aoirunner.Result{
		Identity:     aoirunner.ScenarioIdentityFromConfig(aoirunner.ModeCoreTick, config, 1),
		Status:       aoirunner.StatusSuccess,
		TickDuration: &aoirunner.DurationStats{Class: aoirunner.MetricPrimary, MedianNs: 1},
	})
	runner := &mockBenchRunner{responses: []mockResponse{
		{stdout: success, exitCode: 0, elapsed: time.Millisecond},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond},
	}}
	report, err := aoirunner.RunMatrix(aoirunner.MatrixOptions{
		BenchBinary: "aoi-bench",
		Repeats:     3,
		Definitions: []aoirunner.ScenarioDefinition{{Mode: aoirunner.ModeCoreTick, Config: config}},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Repeats) != 3 {
		t.Fatalf("repeats=%d want 3", len(report.Repeats))
	}
	if runner.calls != 3 {
		t.Fatalf("calls=%d want 3", runner.calls)
	}
}

func TestRunMatrixContinuesAfterFailures(t *testing.T) {
	config := aoiworkload.Config{Scale: 128, MoverCount: 16, Density: aoiworkload.DensityNormal, Seed: 1}
	success := mustJSONResult(t, aoirunner.Result{
		Identity: aoirunner.ScenarioIdentityFromConfig(aoirunner.ModeCoreTick, config, 1),
		Status:   aoirunner.StatusSuccess,
		Phase:    aoirunner.PhaseMeasuredTicks,
		TickDuration: &aoirunner.DurationStats{
			Class: aoirunner.MetricPrimary, MedianNs: 100,
		},
	})
	runner := &mockBenchRunner{responses: []mockResponse{
		{stdout: []byte("{"), stderr: "broken", exitCode: 1, elapsed: time.Millisecond},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond, terminatedBy: "timeout"},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond, terminatedBy: "memory_limit"},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond, terminatedBy: "oom"},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond, signal: "SIGTERM", terminatedBy: "signal"},
		{stdout: success, exitCode: 0, elapsed: time.Millisecond},
	}}
	report, err := aoirunner.RunMatrix(aoirunner.MatrixOptions{
		BenchBinary: "aoi-bench",
		Repeats:     1,
		Definitions: []aoirunner.ScenarioDefinition{
			{Mode: aoirunner.ModeCoreTick, Config: config},
			{Mode: aoirunner.ModeCoreTick, Config: config},
			{Mode: aoirunner.ModeCoreTick, Config: config},
			{Mode: aoirunner.ModeCoreTick, Config: config},
			{Mode: aoirunner.ModeCoreTick, Config: config},
			{Mode: aoirunner.ModeCoreTick, Config: config},
		},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Repeats) != 6 {
		t.Fatalf("repeats=%d", len(report.Repeats))
	}
	if report.Repeats[0].Result.Status != aoirunner.StatusRuntimeError {
		t.Fatalf("malformed json status=%s", report.Repeats[0].Result.Status)
	}
	if report.Repeats[1].Result.Status != aoirunner.StatusTimeout {
		t.Fatalf("timeout status=%s", report.Repeats[1].Result.Status)
	}
	if report.Repeats[2].Result.Status != aoirunner.StatusMemoryLimit {
		t.Fatalf("memory status=%s", report.Repeats[2].Result.Status)
	}
	if report.Repeats[3].Result.Status != aoirunner.StatusOOM {
		t.Fatalf("oom status=%s", report.Repeats[3].Result.Status)
	}
	if report.Repeats[4].Result.Status != aoirunner.StatusSignal {
		t.Fatalf("signal status=%s", report.Repeats[4].Result.Status)
	}
}

func TestMatrixJSONAndCSVMatch(t *testing.T) {
	config := aoiworkload.Config{Scale: 128, MoverCount: 16, Density: aoiworkload.DensityNormal, Seed: 1}
	def := aoirunner.ScenarioDefinition{Mode: aoirunner.ModeCoreTick, Config: config}
	report := aoirunner.MatrixReport{
		GeneratedAt: time.Now().UTC(),
		Repeats: []aoirunner.MatrixRepeatRecord{
			makeRepeatRecord(def, 1, aoirunner.StatusSuccess, 10, 1000),
		},
	}
	report.Aggregates = aoirunner.AggregateMatrixRepeats(report.Repeats)

	var jsonBuf, csvBuf bytes.Buffer
	if err := aoirunner.WriteMatrixJSON(&jsonBuf, report); err != nil {
		t.Fatal(err)
	}
	if err := aoirunner.WriteMatrixCSV(&csvBuf, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvBuf.String(), "repeat") || !strings.Contains(csvBuf.String(), "aggregate") {
		t.Fatalf("csv=%s", csvBuf.String())
	}
	var decoded aoirunner.MatrixReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Repeats) != 1 {
		t.Fatal("json round trip failed")
	}
}

func TestRunMatrixEndToEndSmall(t *testing.T) {
	benchBin := buildBenchBinary(t)
	config := aoiworkload.Config{Scale: 128, MoverCount: 16, Density: aoiworkload.DensityNormal, Seed: 31}
	report, err := aoirunner.RunMatrix(aoirunner.MatrixOptions{
		BenchBinary: benchBin,
		Repeats:     1,
		Timeout:     2 * time.Minute,
		MaxRSS:      -1,
		Definitions: []aoirunner.ScenarioDefinition{
			{Mode: aoirunner.ModeBuild, Config: config},
			{Mode: aoirunner.ModeCoreTick, Config: config},
			{Mode: aoirunner.ModeWorldAOI, Config: config},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Repeats) != 3 {
		t.Fatalf("repeats=%d", len(report.Repeats))
	}
	for _, record := range report.Repeats {
		if record.Result.Status != aoirunner.StatusSuccess {
			t.Fatalf("status=%s stderr=%s", record.Result.Status, record.StderrSummary)
		}
	}
	if len(report.Aggregates) != 3 {
		t.Fatalf("aggregates=%d", len(report.Aggregates))
	}
}

func TestNotApplicableMatrixScenario(t *testing.T) {
	config := aoiworkload.Config{Scale: 100, MoverCount: 200, Density: aoiworkload.DensityNormal, Seed: 1}
	result := aoirunner.Result{
		Identity: aoirunner.ScenarioIdentityFromConfig(aoirunner.ModeCoreTick, config, 1),
		Status:   aoirunner.StatusNotApplicable,
	}
	runner := &mockBenchRunner{responses: []mockResponse{
		{stdout: mustJSONResult(t, result), exitCode: 0, elapsed: time.Millisecond},
	}}
	report, err := aoirunner.RunMatrix(aoirunner.MatrixOptions{
		BenchBinary: "aoi-bench",
		Repeats:     1,
		Definitions: []aoirunner.ScenarioDefinition{{Mode: aoirunner.ModeCoreTick, Config: config}},
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repeats[0].Result.Status != aoirunner.StatusNotApplicable {
		t.Fatalf("status=%s", report.Repeats[0].Result.Status)
	}
}

func makeRepeatRecord(
	def aoirunner.ScenarioDefinition,
	repeat int,
	status aoirunner.Status,
	medianNs int64,
	peakRSS int64,
) aoirunner.MatrixRepeatRecord {
	result := aoirunner.Result{
		Identity: def.Identity(repeat),
		Status:   status,
		Phase:    aoirunner.PhaseMeasuredTicks,
		RSS: &aoirunner.RSSSnapshot{
			Class:     aoirunner.MetricPrimary,
			PeakBytes: peakRSS,
			Available: peakRSS > 0,
		},
	}
	switch def.Mode {
	case aoirunner.ModeBuild:
		result.BuildDurationNs = medianNs
	case aoirunner.ModeWorldAOI:
		result.CombinedTickDuration = &aoirunner.DurationStats{Class: aoirunner.MetricPrimary, MedianNs: medianNs}
	default:
		result.TickDuration = &aoirunner.DurationStats{Class: aoirunner.MetricPrimary, MedianNs: medianNs}
	}
	return aoirunner.MatrixRepeatRecord{
		Definition:  def,
		Result:      result,
		WallClockNs: medianNs,
	}
}

func mustJSONResult(t *testing.T, result aoirunner.Result) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := aoirunner.WriteJSON(&buf, result); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildBenchBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "aoi-bench")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = benchPackageDir(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build aoi-bench: %v\n%s", err, output)
	}
	return binary
}

func benchPackageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", "..", "cmd", "aoi-bench"))
}
