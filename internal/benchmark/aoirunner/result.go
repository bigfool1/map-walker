package aoirunner

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"map-walker/internal/benchmark/aoiworkload"
)

const SerialCoreBaselineKind = "Serial Core Baseline"

type Mode string

const (
	ModeBuild     Mode = "build"
	ModeCoreTick  Mode = "core_tick"
	ModeWorldAOI  Mode = "world_aoi"
)

type Phase string

const (
	PhaseGeneration     Phase = "generation"
	PhaseBuild          Phase = "build"
	PhaseWarmup         Phase = "warmup"
	PhaseMeasuredTicks  Phase = "measured_ticks"
)

type Status string

const (
	StatusSuccess       Status = "success"
	StatusTimeout       Status = "timeout"
	StatusMemoryLimit   Status = "memory_limit"
	StatusOOM           Status = "oom"
	StatusSignal        Status = "signal"
	StatusRuntimeError  Status = "runtime_error"
	StatusNotApplicable Status = "not_applicable"
)

type MetricClass string

const (
	MetricPrimary    MetricClass = "primary"
	MetricDiagnostic MetricClass = "diagnostic"
)

type ScenarioIdentity struct {
	Mode       Mode   `json:"mode"`
	Scale      int    `json:"scale"`
	MoverCount int    `json:"mover_count"`
	Density    string `json:"density"`
	Seed       int64  `json:"seed"`
	Repeat     int    `json:"repeat,omitempty"`
}

func ScenarioIdentityFromConfig(mode Mode, config aoiworkload.Config, repeat int) ScenarioIdentity {
	return ScenarioIdentity{
		Mode:       mode,
		Scale:      config.Scale,
		MoverCount: config.MoverCount,
		Density:    string(config.Density),
		Seed:       config.Seed,
		Repeat:     repeat,
	}
}

func (id ScenarioIdentity) Key() string {
	repeat := ""
	if id.Repeat > 0 {
		repeat = fmt.Sprintf("/repeat=%d", id.Repeat)
	}
	return fmt.Sprintf("%s/%d/%d/%s/seed=%d%s", id.Mode, id.Scale, id.MoverCount, id.Density, id.Seed, repeat)
}

type EnvironmentMetadata struct {
	Timestamp            time.Time         `json:"timestamp"`
	BaselineKind         string            `json:"baseline_kind"`
	GoVersion            string            `json:"go_version"`
	GOOS                 string            `json:"goos"`
	GOARCH               string            `json:"goarch"`
	NumCPU               int               `json:"num_cpu"`
	GOMAXPROCS           int               `json:"gomaxprocs"`
	GOGC                 string            `json:"gogc"`
	GOMEMLIMIT           string            `json:"gomemlimit"`
	CommitSHA            string            `json:"commit_sha"`
	DirtyWorktree        bool              `json:"dirty_worktree"`
	Arguments            []string          `json:"arguments"`
	DependencyVersions   map[string]string `json:"dependency_versions,omitempty"`
	CPUModel             string            `json:"cpu_model,omitempty"`
	TotalMemoryBytes     *int64            `json:"total_memory_bytes,omitempty"`
	TotalMemoryAvailable bool              `json:"total_memory_available"`
}

type DurationStats struct {
	Class    MetricClass `json:"class"`
	MedianNs int64       `json:"median_ns"`
	P95Ns    int64       `json:"p95_ns"`
	P99Ns    int64       `json:"p99_ns"`
	MaxNs    int64       `json:"max_ns"`
}

type ThroughputStats struct {
	Class           MetricClass `json:"class"`
	MovesPerSecond  float64     `json:"moves_per_second"`
}

type VisibilityChurnMetric struct {
	Class  MetricClass `json:"class"`
	Mean   float64     `json:"mean"`
	P50    float64     `json:"p50"`
	P95    float64     `json:"p95"`
	Max    int         `json:"max"`
}

type HeapSnapshot struct {
	Class               MetricClass `json:"class"`
	HeapAllocBytes      uint64      `json:"heap_alloc_bytes"`
	HeapInuseBytes      uint64      `json:"heap_inuse_bytes"`
	HeapObjects         uint64      `json:"heap_objects"`
	TotalAllocBytes     uint64      `json:"total_alloc_bytes"`
	DeltaHeapAllocBytes uint64      `json:"delta_heap_alloc_bytes,omitempty"`
	DeltaTotalAllocBytes uint64     `json:"delta_total_alloc_bytes,omitempty"`
}

type GCSnapshot struct {
	Class               MetricClass `json:"class"`
	NumGC               uint32      `json:"num_gc"`
	TotalPauseNs        uint64      `json:"total_pause_ns"`
	MaxPauseNs          uint64      `json:"max_pause_ns"`
	DeltaNumGC          uint32      `json:"delta_num_gc,omitempty"`
	DeltaTotalPauseNs   uint64      `json:"delta_total_pause_ns,omitempty"`
}

type RSSSnapshot struct {
	Class     MetricClass `json:"class"`
	PeakBytes int64       `json:"peak_bytes"`
	Available bool        `json:"available"`
	Source    string      `json:"source"`
}

type AOICounters struct {
	Class                MetricClass `json:"class"`
	CandidatePairs       uint64      `json:"candidate_pairs"`
	DistanceChecks       uint64      `json:"distance_checks"`
	RelationshipsEntered uint64      `json:"relationships_entered"`
	RelationshipsLeft    uint64      `json:"relationships_left"`
}

type BuildCheckpoint struct {
	PercentComplete int     `json:"percent_complete"`
	ElapsedNs       int64   `json:"elapsed_ns"`
	RSSBytes        *int64  `json:"rss_bytes,omitempty"`
	RSSAvailable    bool    `json:"rss_available"`
	RSSSource       string  `json:"rss_source,omitempty"`
}

type Result struct {
	Identity    ScenarioIdentity    `json:"identity"`
	Environment EnvironmentMetadata `json:"environment"`
	Status      Status              `json:"status"`
	Phase       Phase               `json:"phase,omitempty"`
	ErrorSummary string             `json:"error_summary,omitempty"`

	ElapsedNs int64 `json:"elapsed_ns,omitempty"`

	TickDuration      *DurationStats       `json:"tick_duration,omitempty"`
	BuildDurationNs   int64                `json:"build_duration_ns,omitempty"`
	BuildCheckpoints  []BuildCheckpoint    `json:"build_checkpoints,omitempty"`
	Throughput        *ThroughputStats     `json:"throughput,omitempty"`
	VisibilityChurn   *VisibilityChurnMetric `json:"visibility_churn,omitempty"`
	Heap              *HeapSnapshot        `json:"heap,omitempty"`
	GC                *GCSnapshot          `json:"gc,omitempty"`
	RSS               *RSSSnapshot         `json:"rss,omitempty"`
	AOI               *AOICounters         `json:"aoi,omitempty"`

	ProfileExecution          bool `json:"profile_execution,omitempty"`
	ProfileRequiredExplicitGC bool `json:"profile_required_explicit_gc,omitempty"`

	ExitCode         *int   `json:"exit_code,omitempty"`
	Signal           string `json:"signal,omitempty"`
	MemoryLimitBytes *int64 `json:"memory_limit_bytes,omitempty"`
	StderrSummary    string `json:"stderr_summary,omitempty"`
}

func (r Result) MarshalJSON() ([]byte, error) {
	type alias Result
	return json.Marshal(alias(r))
}

func (r *Result) UnmarshalJSON(data []byte) error {
	type alias Result
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = Result(decoded)
	return nil
}

func WriteJSON(w io.Writer, result Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func ParseJSON(data []byte) (Result, error) {
	var result Result
	err := json.Unmarshal(data, &result)
	return result, err
}

var csvHeaders = []string{
	"identity_key",
	"mode",
	"scale",
	"mover_count",
	"density",
	"seed",
	"repeat",
	"status",
	"phase",
	"baseline_kind",
	"commit_sha",
	"dirty_worktree",
	"go_version",
	"goos",
	"goarch",
	"gomaxprocs",
	"tick_median_ns",
	"tick_median_class",
	"tick_p95_ns",
	"tick_p99_ns",
	"tick_max_ns",
	"throughput_moves_per_second",
	"throughput_class",
	"peak_rss_bytes",
	"rss_class",
	"rss_available",
	"heap_alloc_bytes",
	"heap_class",
	"gc_total_pause_ns",
	"gc_class",
	"visibility_churn_mean",
	"visibility_churn_class",
	"candidate_pairs",
	"candidate_pairs_class",
	"distance_checks",
	"distance_checks_class",
	"build_duration_ns",
	"elapsed_ns",
	"error_summary",
}

func WriteCSV(w io.Writer, results []Result) error {
	writer := csv.NewWriter(w)
	if err := writer.Write(csvHeaders); err != nil {
		return err
	}
	for _, result := range results {
		if err := writer.Write(result.csvRow()); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func (r Result) csvRow() []string {
	return []string{
		r.Identity.Key(),
		string(r.Identity.Mode),
		strconv.Itoa(r.Identity.Scale),
		strconv.Itoa(r.Identity.MoverCount),
		r.Identity.Density,
		strconv.FormatInt(r.Identity.Seed, 10),
		strconv.Itoa(r.Identity.Repeat),
		string(r.Status),
		string(r.Phase),
		r.Environment.BaselineKind,
		r.Environment.CommitSHA,
		strconv.FormatBool(r.Environment.DirtyWorktree),
		r.Environment.GoVersion,
		r.Environment.GOOS,
		r.Environment.GOARCH,
		strconv.Itoa(r.Environment.GOMAXPROCS),
		durationField(r.TickDuration, func(s DurationStats) int64 { return s.MedianNs }),
		durationClass(r.TickDuration),
		durationField(r.TickDuration, func(s DurationStats) int64 { return s.P95Ns }),
		durationField(r.TickDuration, func(s DurationStats) int64 { return s.P99Ns }),
		durationField(r.TickDuration, func(s DurationStats) int64 { return s.MaxNs }),
		throughputField(r.Throughput),
		throughputClass(r.Throughput),
		rssField(r.RSS),
		rssClass(r.RSS),
		rssAvailable(r.RSS),
		heapField(r.Heap),
		heapClass(r.Heap),
		gcPauseField(r.GC),
		gcClass(r.GC),
		churnMeanField(r.VisibilityChurn),
		churnClass(r.VisibilityChurn),
		aoiField(r.AOI, func(c AOICounters) uint64 { return c.CandidatePairs }),
		aoiClass(r.AOI),
		aoiField(r.AOI, func(c AOICounters) uint64 { return c.DistanceChecks }),
		aoiClass(r.AOI),
		strconv.FormatInt(r.BuildDurationNs, 10),
		strconv.FormatInt(r.ElapsedNs, 10),
		r.ErrorSummary,
	}
}

func durationField(stats *DurationStats, pick func(DurationStats) int64) string {
	if stats == nil {
		return ""
	}
	return strconv.FormatInt(pick(*stats), 10)
}

func durationClass(stats *DurationStats) string {
	if stats == nil {
		return ""
	}
	return string(stats.Class)
}

func throughputField(stats *ThroughputStats) string {
	if stats == nil {
		return ""
	}
	return strconv.FormatFloat(stats.MovesPerSecond, 'f', -1, 64)
}

func throughputClass(stats *ThroughputStats) string {
	if stats == nil {
		return ""
	}
	return string(stats.Class)
}

func rssField(snapshot *RSSSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strconv.FormatInt(snapshot.PeakBytes, 10)
}

func rssClass(snapshot *RSSSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return string(snapshot.Class)
}

func rssAvailable(snapshot *RSSSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strconv.FormatBool(snapshot.Available)
}

func heapField(snapshot *HeapSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strconv.FormatUint(snapshot.HeapAllocBytes, 10)
}

func heapClass(snapshot *HeapSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return string(snapshot.Class)
}

func gcPauseField(snapshot *GCSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strconv.FormatUint(snapshot.TotalPauseNs, 10)
}

func gcClass(snapshot *GCSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return string(snapshot.Class)
}

func churnMeanField(metric *VisibilityChurnMetric) string {
	if metric == nil {
		return ""
	}
	return strconv.FormatFloat(metric.Mean, 'f', -1, 64)
}

func churnClass(metric *VisibilityChurnMetric) string {
	if metric == nil {
		return ""
	}
	return string(metric.Class)
}

func aoiField(counters *AOICounters, pick func(AOICounters) uint64) string {
	if counters == nil {
		return ""
	}
	return strconv.FormatUint(pick(*counters), 10)
}

func aoiClass(counters *AOICounters) string {
	if counters == nil {
		return ""
	}
	return string(counters.Class)
}

func VisibilityChurnFromWorkload(stats aoiworkload.VisibilityChurnStats) VisibilityChurnMetric {
	return VisibilityChurnMetric{
		Class: MetricPrimary,
		Mean:  stats.Mean,
		P50:   stats.P50,
		P95:   stats.P95,
		Max:   stats.Max,
	}
}

func AllStatuses() []Status {
	return []Status{
		StatusSuccess,
		StatusTimeout,
		StatusMemoryLimit,
		StatusOOM,
		StatusSignal,
		StatusRuntimeError,
		StatusNotApplicable,
	}
}

func ValidateStatus(value Status) bool {
	switch value {
	case StatusSuccess, StatusTimeout, StatusMemoryLimit, StatusOOM, StatusSignal, StatusRuntimeError, StatusNotApplicable:
		return true
	default:
		return strings.TrimSpace(string(value)) == ""
	}
}
