package aoirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
)

type ScenarioDefinition struct {
	Mode   Mode
	Config aoiworkload.Config
}

func (d ScenarioDefinition) Identity(repeat int) ScenarioIdentity {
	return ScenarioIdentityFromConfig(d.Mode, d.Config, repeat)
}

func (d ScenarioDefinition) AggregateKey() string {
	return ScenarioIdentityFromConfig(d.Mode, d.Config, 0).Key()
}

type MatrixOptions struct {
	Full        bool
	Seed        int64
	Repeats     int
	Timeout     time.Duration
	MaxRSS      int64
	BenchBinary string
	Definitions []ScenarioDefinition
}

type MatrixRepeatRecord struct {
	Definition   ScenarioDefinition `json:"definition"`
	WallClockNs  int64              `json:"wall_clock_ns"`
	ExitCode     int                `json:"exit_code"`
	Signal       string             `json:"signal,omitempty"`
	StderrSummary string            `json:"stderr_summary,omitempty"`
	Result       Result             `json:"result"`
}

type RepeatMinMedianMax struct {
	Min    int64 `json:"min"`
	Median int64 `json:"median"`
	Max    int64 `json:"max"`
}

type MatrixAggregateRecord struct {
	Definition       ScenarioDefinition `json:"definition"`
	RepeatCount      int                `json:"repeat_count"`
	RepeatMediansNs  RepeatMinMedianMax `json:"repeat_medians_ns"`
	RepeatPeakRSS    RepeatMinMedianMax `json:"repeat_peak_rss_bytes"`
	RepeatStatuses   []Status           `json:"repeat_statuses"`
}

type MatrixReport struct {
	GeneratedAt      time.Time              `json:"generated_at"`
	MemoryGuardBytes int64                  `json:"memory_guard_bytes"`
	TimeoutNs        int64                  `json:"timeout_ns"`
	FullMatrix       bool                   `json:"full_matrix"`
	Repeats          []MatrixRepeatRecord   `json:"repeats"`
	Aggregates       []MatrixAggregateRecord `json:"aggregates"`
}

type BenchRunner interface {
	Run(ctx context.Context, binary string, args []string, maxRSS int64) BenchRunOutcome
}

type BenchRunOutcome struct {
	Stdout        []byte
	Stderr        string
	ExitCode      int
	Signal        string
	Elapsed       time.Duration
	TerminatedBy  string
}

type ExecBenchRunner struct {
	PollInterval time.Duration
}

func BaselineMatrixDefinitions(seed int64) []ScenarioDefinition {
	defs := make([]ScenarioDefinition, 0)
	seenBuild := map[string]struct{}{}

	for _, config := range aoiworkload.BaselineMatrixConfigs(seed) {
		defs = append(defs,
			ScenarioDefinition{Mode: ModeCoreTick, Config: config},
			ScenarioDefinition{Mode: ModeWorldAOI, Config: config},
		)
		buildKey := fmt.Sprintf("%d/%s", config.Scale, config.Density)
		if _, ok := seenBuild[buildKey]; ok {
			continue
		}
		seenBuild[buildKey] = struct{}{}
		defs = append(defs, ScenarioDefinition{Mode: ModeBuild, Config: config})
	}

	sortScenarioDefinitions(defs)
	return defs
}

func FullMatrixDefinitions(seed int64) []ScenarioDefinition {
	defs := make([]ScenarioDefinition, 0)
	modes := []Mode{ModeBuild, ModeCoreTick, ModeWorldAOI}
	for _, config := range aoiworkload.AllFrozenConfigs(seed) {
		if !config.IsApplicable() {
			continue
		}
		for _, mode := range modes {
			defs = append(defs, ScenarioDefinition{Mode: mode, Config: config})
		}
	}
	sortScenarioDefinitions(defs)
	return defs
}

func sortScenarioDefinitions(defs []ScenarioDefinition) {
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Config.Scale != defs[j].Config.Scale {
			return defs[i].Config.Scale < defs[j].Config.Scale
		}
		if defs[i].Mode != defs[j].Mode {
			return defs[i].Mode < defs[j].Mode
		}
		if defs[i].Config.MoverCount != defs[j].Config.MoverCount {
			return defs[i].Config.MoverCount < defs[j].Config.MoverCount
		}
		return defs[i].Config.Density < defs[j].Config.Density
	})
}

func ResolveMemoryGuard(maxRSSFlag int64) int64 {
	switch {
	case maxRSSFlag < 0:
		return 0
	case maxRSSFlag == 0:
		return DefaultMemoryGuardBytes()
	default:
		return maxRSSFlag
	}
}

func RunMatrix(opts MatrixOptions, runner BenchRunner) (MatrixReport, error) {
	if opts.Repeats <= 0 {
		opts.Repeats = DefaultScenarioRepeats
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultMatrixTimeout
	}
	if opts.Seed == 0 {
		opts.Seed = BenchmarkSeed
	}
	if runner == nil {
		runner = ExecBenchRunner{PollInterval: 100 * time.Millisecond}
	}
	if opts.BenchBinary == "" {
		return MatrixReport{}, fmt.Errorf("missing bench binary")
	}

	definitions := opts.Definitions
	if len(definitions) == 0 {
		if opts.Full {
			definitions = FullMatrixDefinitions(opts.Seed)
		} else {
			definitions = BaselineMatrixDefinitions(opts.Seed)
		}
	}

	memoryGuard := ResolveMemoryGuard(opts.MaxRSS)

	report := MatrixReport{
		GeneratedAt:      time.Now().UTC(),
		MemoryGuardBytes: memoryGuard,
		TimeoutNs:        opts.Timeout.Nanoseconds(),
		FullMatrix:       opts.Full,
		Repeats:          make([]MatrixRepeatRecord, 0),
	}

	for _, definition := range definitions {
		for repeat := 1; repeat <= opts.Repeats; repeat++ {
			record := runMatrixRepeat(opts, runner, definition, repeat, memoryGuard)
			report.Repeats = append(report.Repeats, record)
		}
	}

	report.Aggregates = AggregateMatrixRepeats(report.Repeats)
	return report, nil
}

func runMatrixRepeat(
	opts MatrixOptions,
	runner BenchRunner,
	definition ScenarioDefinition,
	repeat int,
	memoryGuard int64,
) MatrixRepeatRecord {
	args := []string{
		"-mode", string(definition.Mode),
		"-scale", fmt.Sprintf("%d", definition.Config.Scale),
		"-movers", fmt.Sprintf("%d", definition.Config.MoverCount),
		"-density", string(definition.Config.Density),
		"-seed", fmt.Sprintf("%d", definition.Config.Seed),
		"-repeat", fmt.Sprintf("%d", repeat),
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	outcome := runner.Run(ctx, opts.BenchBinary, args, memoryGuard)
	record := MatrixRepeatRecord{
		Definition:    definition,
		WallClockNs:   outcome.Elapsed.Nanoseconds(),
		ExitCode:      outcome.ExitCode,
		Signal:        outcome.Signal,
		StderrSummary: summarizeStderr(outcome.Stderr),
	}

	result, parseErr := ParseJSON(outcome.Stdout)
	if parseErr != nil {
		result = syntheticMatrixResult(definition, repeat, StatusRuntimeError, PhaseGeneration, parseErr.Error())
	} else {
		result.Identity.Repeat = repeat
	}

	record.Result = finalizeMatrixResult(result, outcome, memoryGuard, parseErr == nil)
	return record
}

func finalizeMatrixResult(result Result, outcome BenchRunOutcome, memoryGuard int64, parsed bool) Result {
	switch outcome.TerminatedBy {
	case "timeout":
		result.Status = StatusTimeout
		if result.ErrorSummary == "" {
			result.ErrorSummary = "matrix timeout"
		}
	case "memory_limit":
		result.Status = StatusMemoryLimit
		if memoryGuard > 0 {
			limit := memoryGuard
			result.MemoryLimitBytes = &limit
		}
		if result.ErrorSummary == "" {
			result.ErrorSummary = "memory guard exceeded"
		}
	case "oom":
		result.Status = StatusOOM
		if result.ErrorSummary == "" {
			result.ErrorSummary = "operating-system oom"
		}
	case "signal":
		if result.Status == "" || result.Status == StatusSuccess {
			result.Status = StatusSignal
		}
		result.Signal = outcome.Signal
		if result.ErrorSummary == "" {
			result.ErrorSummary = "terminated by signal"
		}
	}

	exitCode := outcome.ExitCode
	result.ExitCode = &exitCode
	result.StderrSummary = summarizeStderr(outcome.Stderr)
	if outcome.Signal != "" {
		result.Signal = outcome.Signal
	}

	if !parsed {
		return result
	}
	if outcome.TerminatedBy == "" && outcome.ExitCode != 0 && result.Status == StatusSuccess {
		result.Status = StatusRuntimeError
	}
	if result.ProfileExecution {
		return result
	}
	return result
}

func syntheticMatrixResult(
	definition ScenarioDefinition,
	repeat int,
	status Status,
	phase Phase,
	summary string,
) Result {
	return Result{
		Identity:     definition.Identity(repeat),
		Environment:  CaptureEnvironment(EnvironmentCaptureOptions{}),
		Status:       status,
		Phase:        phase,
		ErrorSummary: summary,
	}
}

func AggregateMatrixRepeats(records []MatrixRepeatRecord) []MatrixAggregateRecord {
	byKey := map[string][]MatrixRepeatRecord{}
	order := make([]string, 0)
	for _, record := range records {
		if record.Result.ProfileExecution {
			continue
		}
		key := record.Definition.AggregateKey()
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], record)
	}

	aggregates := make([]MatrixAggregateRecord, 0, len(order))
	for _, key := range order {
		group := byKey[key]
		if len(group) == 0 {
			continue
		}
		medians := make([]int64, 0, len(group))
		peaks := make([]int64, 0, len(group))
		statuses := make([]Status, 0, len(group))
		for _, record := range group {
			statuses = append(statuses, record.Result.Status)
			if median := primaryMedianNs(record.Result); median >= 0 {
				medians = append(medians, median)
			}
			if record.Result.RSS != nil && record.Result.RSS.Available {
				peaks = append(peaks, record.Result.RSS.PeakBytes)
			}
		}
		aggregates = append(aggregates, MatrixAggregateRecord{
			Definition:      group[0].Definition,
			RepeatCount:       len(group),
			RepeatMediansNs:   repeatMinMedianMax(medians),
			RepeatPeakRSS:     repeatMinMedianMax(peaks),
			RepeatStatuses:    statuses,
		})
	}
	return aggregates
}

func primaryMedianNs(result Result) int64 {
	switch result.Identity.Mode {
	case ModeBuild:
		return result.BuildDurationNs
	case ModeWorldAOI:
		if result.CombinedTickDuration != nil {
			return result.CombinedTickDuration.MedianNs
		}
	case ModeCoreTick:
		if result.TickDuration != nil {
			return result.TickDuration.MedianNs
		}
	}
	return -1
}

func repeatMinMedianMax(values []int64) RepeatMinMedianMax {
	if len(values) == 0 {
		return RepeatMinMedianMax{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return RepeatMinMedianMax{
		Min:    sorted[0],
		Median: sorted[percentileIndex(len(sorted), 0.50)],
		Max:    sorted[len(sorted)-1],
	}
}

func (r ExecBenchRunner) Run(ctx context.Context, binary string, args []string, maxRSS int64) BenchRunOutcome {
	if r.PollInterval <= 0 {
		r.PollInterval = 100 * time.Millisecond
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return BenchRunOutcome{
			Stderr:   err.Error(),
			ExitCode: 1,
			Elapsed:  time.Since(start),
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	terminatedBy := ""
	ticker := time.NewTicker(r.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			outcome := BenchRunOutcome{
				Stdout:   stdout.Bytes(),
				Stderr:   stderr.String(),
				Elapsed:  time.Since(start),
				ExitCode: exitCodeFromError(err),
				Signal:   signalFromError(err),
			}
			if terminatedBy != "" {
				outcome.TerminatedBy = terminatedBy
			} else if outcome.ExitCode != 0 && outcome.Signal == "" {
				outcome.TerminatedBy = classifyProcessFailure(outcome.ExitCode, outcome.Stderr, outcome.Signal)
			} else if outcome.Signal != "" {
				outcome.TerminatedBy = classifyProcessFailure(outcome.ExitCode, outcome.Stderr, outcome.Signal)
			}
			return outcome
		case <-ctx.Done():
			terminatedBy = "timeout"
			_ = cmd.Process.Kill()
		case <-ticker.C:
			if maxRSS <= 0 || cmd.Process == nil {
				continue
			}
			rss, ok := readChildRSS(cmd.Process.Pid)
			if ok && rss > maxRSS {
				terminatedBy = "memory_limit"
				_ = cmd.Process.Kill()
			}
		}
	}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func signalFromError(err error) string {
	if err == nil {
		return ""
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return ""
	}
	waitStatus, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() {
		return ""
	}
	return waitStatus.Signal().String()
}

func classifyProcessFailure(exitCode int, stderr, signal string) string {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "out of memory") || strings.Contains(lower, "oom") {
		return "oom"
	}
	if signal != "" {
		if exitCode == 137 && strings.Contains(lower, "killed") {
			return "oom"
		}
		return "signal"
	}
	if exitCode != 0 {
		return "runtime_error"
	}
	return ""
}

func summarizeStderr(stderr string) string {
	const limit = 512
	trimmed := strings.TrimSpace(stderr)
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit]
}

func WriteMatrixJSON(w io.Writer, report MatrixReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func WriteMatrixCSV(w io.Writer, report MatrixReport) error {
	writer := csvWriter{}
	return writer.writeMatrix(w, report)
}

type csvWriter struct{}

func (csvWriter) writeMatrix(w io.Writer, report MatrixReport) error {
	cw := newCSVWriter(w)
	headers := []string{
		"record_kind", "identity_key", "mode", "scale", "movers", "density", "seed", "repeat",
		"status", "phase", "wall_clock_ns", "exit_code", "signal", "median_ns", "peak_rss_bytes",
		"repeat_median_min_ns", "repeat_median_median_ns", "repeat_median_max_ns",
		"repeat_peak_rss_min", "repeat_peak_rss_median", "repeat_peak_rss_max",
		"stderr_summary",
	}
	if err := cw.write(headers); err != nil {
		return err
	}

	for _, record := range report.Repeats {
		if err := cw.write(repeatCSVRow("repeat", record)); err != nil {
			return err
		}
	}
	for _, aggregate := range report.Aggregates {
		if err := cw.write(aggregateCSVRow("aggregate", aggregate)); err != nil {
			return err
		}
	}
	return cw.flush()
}

func repeatCSVRow(kind string, record MatrixRepeatRecord) []string {
	median := primaryMedianNs(record.Result)
	peak := int64(0)
	if record.Result.RSS != nil {
		peak = record.Result.RSS.PeakBytes
	}
	id := record.Definition.Identity(record.Result.Identity.Repeat)
	return []string{
		kind, id.Key(), string(id.Mode), fmt.Sprintf("%d", id.Scale), fmt.Sprintf("%d", id.MoverCount),
		id.Density, fmt.Sprintf("%d", id.Seed), fmt.Sprintf("%d", id.Repeat),
		string(record.Result.Status), string(record.Result.Phase),
		fmt.Sprintf("%d", record.WallClockNs), fmt.Sprintf("%d", record.ExitCode), record.Signal,
		fmt.Sprintf("%d", median), fmt.Sprintf("%d", peak),
		"", "", "", "", "", "",
		record.StderrSummary,
	}
}

func aggregateCSVRow(kind string, aggregate MatrixAggregateRecord) []string {
	id := aggregate.Definition.Identity(0)
	return []string{
		kind, id.Key(), string(id.Mode), fmt.Sprintf("%d", id.Scale), fmt.Sprintf("%d", id.MoverCount),
		id.Density, fmt.Sprintf("%d", id.Seed), "0",
		"", "",
		"", "", "", "", "",
		fmt.Sprintf("%d", aggregate.RepeatMediansNs.Min),
		fmt.Sprintf("%d", aggregate.RepeatMediansNs.Median),
		fmt.Sprintf("%d", aggregate.RepeatMediansNs.Max),
		fmt.Sprintf("%d", aggregate.RepeatPeakRSS.Min),
		fmt.Sprintf("%d", aggregate.RepeatPeakRSS.Median),
		fmt.Sprintf("%d", aggregate.RepeatPeakRSS.Max),
		"",
	}
}

type matrixCSVFileWriter struct {
	out io.Writer
}

func newCSVWriter(w io.Writer) *matrixCSVFileWriter {
	return &matrixCSVFileWriter{out: w}
}

func (w *matrixCSVFileWriter) write(record []string) error {
	for i, value := range record {
		if i > 0 {
			if _, err := io.WriteString(w.out, ","); err != nil {
				return err
			}
		}
		if strings.ContainsAny(value, ",\"\n") {
			value = `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
		}
		if _, err := io.WriteString(w.out, value); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w.out, "\n")
	return err
}

func (w *matrixCSVFileWriter) flush() error {
	return nil
}
