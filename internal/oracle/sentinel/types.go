/*-------------------------------------------------------------------------
 *
 * types.go
 *	  RootCauseType classifies the probable root cause of a performance
 *	  anomaly.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/types.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"time"

	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/sqladvisor"
)

// RootCauseType classifies the probable root cause of a performance anomaly.
type RootCauseType string

const (
	CauseBadSQL         RootCauseType = "bad_sql"          // single SQL causing concurrent contention
	CauseIOSubsystem    RootCauseType = "io_subsystem"     // storage I/O latency spike
	CauseLatchStorm     RootCauseType = "latch_storm"      // latch/mutex contention (hard parse storm)
	CauseRedoBottleneck RootCauseType = "redo_bottleneck"  // log file sync bottleneck
	CauseLockContention RootCauseType = "lock_contention"  // row/table lock blocking chain
	CauseTrafficStorm   RootCauseType = "traffic_storm"    // sudden influx of connections

	CausePlanDrift         RootCauseType = "plan_drift"         // 执行计划漂移
	CauseMemoryPressure    RootCauseType = "memory_pressure"    // 内存冲高
	CauseConnectionExhaust RootCauseType = "connection_exhaust" // 连接数冲高
	CauseArchiveDelay      RootCauseType = "archive_delay"      // 归档冲高
	CauseDGSyncDelay       RootCauseType = "dg_sync_delay"      // DG同步冲高
	CauseUndoPressure      RootCauseType = "undo_pressure"      // Undo冲高
	CauseTempPressure      RootCauseType = "temp_pressure"      // TEMP冲高
	CauseDatabaseHang      RootCauseType = "database_hang"      // 数据库Hang

	CauseUnknown RootCauseType = "unknown"
)

// String returns the Chinese display name for the root cause type.
func (r RootCauseType) String() string {
	switch r {
	case CauseBadSQL:
		return "SQL并发冲高"
	case CauseIOSubsystem:
		return "存储I/O冲高"
	case CauseLatchStorm:
		return "Latch争用冲高"
	case CauseRedoBottleneck:
		return "Redo冲高"
	case CauseLockContention:
		return "锁等待阻塞"
	case CauseTrafficStorm:
		return "流量冲高"
	case CausePlanDrift:
		return "执行计划漂移"
	case CauseMemoryPressure:
		return "内存冲高"
	case CauseConnectionExhaust:
		return "连接数冲高"
	case CauseArchiveDelay:
		return "归档冲高"
	case CauseDGSyncDelay:
		return "DG同步冲高"
	case CauseUndoPressure:
		return "Undo冲高"
	case CauseTempPressure:
		return "TEMP冲高"
	case CauseDatabaseHang:
		return "数据库Hang"
	default:
		return "未知"
	}
}

// IsValid returns true if the root cause type is a known value.
func (r RootCauseType) IsValid() bool {
	switch r {
	case CauseBadSQL, CauseIOSubsystem, CauseLatchStorm,
		CauseRedoBottleneck, CauseLockContention, CauseTrafficStorm,
		CausePlanDrift, CauseMemoryPressure, CauseConnectionExhaust,
		CauseArchiveDelay, CauseDGSyncDelay, CauseUndoPressure,
		CauseTempPressure, CauseDatabaseHang,
		CauseUnknown:
		return true
	default:
		return false
	}
}

// SentinelSample is the lightweight probe result collected every second.
type SentinelSample struct {
	Timestamp     time.Time `json:"timestamp"`
	ActiveNonIdle int       `json:"active_non_idle"`

	// Multi-metric probe fields (from ProbeCollector).
	OnCPU           int     `json:"on_cpu"`            // sessions on CPU
	IOWait          int     `json:"io_wait"`            // sessions in I/O wait
	LockWait        int     `json:"lock_wait"`          // sessions in Application wait
	LongSQL         int     `json:"long_sql"`           // sessions with SQL > 10s
	RedoKBPerSec    float64 `json:"redo_kb_per_sec"`    // redo generation rate KB/s
	HardParsePerSec float64 `json:"hard_parse_per_sec"` // hard parse count /s

	// Values holds all metric values keyed by MetricName.
	// Fast-tier metrics are populated every tick; medium/slow tiers
	// are populated only on their respective probe intervals.
	Values map[MetricName]float64 `json:"values,omitempty"`
}

// PopulateValues copies the legacy fields into the Values map so that
// detection logic can use a single code path for all metrics.
func (s *SentinelSample) PopulateValues() {
	if s.Values == nil {
		s.Values = make(map[MetricName]float64)
	}
	s.Values[MetricActive] = float64(s.ActiveNonIdle)
	s.Values[MetricCPU] = float64(s.OnCPU)
	s.Values[MetricIO] = float64(s.IOWait)
	s.Values[MetricLock] = float64(s.LockWait)
	s.Values[MetricLongSQL] = float64(s.LongSQL)
	s.Values[MetricRedoRate] = s.RedoKBPerSec
	s.Values[MetricHardParse] = s.HardParsePerSec
}

// MetricName identifies a probe metric for threshold detection.
type MetricName string

const (
	MetricActive    MetricName = "active_sessions"
	MetricCPU       MetricName = "cpu_sessions"
	MetricIO        MetricName = "io_sessions"
	MetricLock      MetricName = "lock_sessions"
	MetricLongSQL   MetricName = "long_sql"
	MetricRedoRate  MetricName = "redo_rate"
	MetricHardParse MetricName = "hard_parse_rate"
)

// MetricBaseline holds rolling stats for a single metric.
type MetricBaseline struct {
	Avg   float64 `json:"avg"`
	Std   float64 `json:"std"`
	Ready bool    `json:"ready"`
}

// Baseline holds rolling statistics for anomaly detection.
type Baseline struct {
	Samples   []SentinelSample            `json:"-"`           // ring buffer, capped at Window
	Window    int                         `json:"window"`      // max samples (default 60)
	AvgActive float64                     `json:"avg_active"`  // rolling mean of ActiveNonIdle
	StdActive float64                     `json:"std_active"`  // rolling stddev of ActiveNonIdle
	Ready     bool                        `json:"ready"`       // true when Samples >= minSamples (10)

	// Multi-metric baselines.
	Metrics map[MetricName]*MetricBaseline `json:"metrics"`
}

// AlertEvent is pushed to the REPL when sentinel detects an anomaly.
type AlertEvent struct {
	Timestamp      time.Time       `json:"timestamp"`
	Cause          RootCauseType   `json:"cause"`
	Confidence     float64         `json:"confidence"`
	BaselineActive float64         `json:"baseline_active"`
	CurrentActive  float64         `json:"current_active"`
	PeakActive     int             `json:"peak_active"`
	DurationSec    float64         `json:"duration_sec"`
	TopBlockers    []BlockingChain `json:"top_blockers"`    // top 3
	TriggerMetric  MetricName      `json:"trigger_metric"`  // which metric triggered
	Report         *BurstReport    `json:"-"`               // full report for /diag
}

// TriggerEvent is emitted when an anomaly exceeds the 3σ threshold.
type TriggerEvent struct {
	Timestamp  time.Time    `json:"timestamp"`
	Metric     string       `json:"metric"`              // "active_sessions" | "cpu_sessions" etc.
	Baseline   float64      `json:"baseline"`            // mean at trigger time
	Current    float64      `json:"current"`             // observed value
	Threshold  float64      `json:"threshold"`           // mean + 3*stddev
	Multiplier float64      `json:"multiplier"`          // current / baseline
	Strategy   StrategyType `json:"strategy,omitempty"`  // which strategy triggered (T1-T9)
}

// BurstFrame is one high-frequency snapshot (every 200ms in burst mode).
type BurstFrame struct {
	Seq       int            `json:"seq"`       // frame sequence number within burst
	Snapshot  dbtop.Snapshot `json:"-"`          // full dbtop snapshot (with BurstDetail if burst mode)
	Timestamp time.Time      `json:"timestamp"`
}

// BurstResult is the output of a burst collection window.
type BurstResult struct {
	Trigger     TriggerEvent `json:"trigger"`
	Frames      []BurstFrame `json:"-"`
	StartTime   time.Time    `json:"start_time"`
	EndTime     time.Time    `json:"end_time"`
	PeakActive  int          `json:"peak_active"`
	RecoverTime time.Time    `json:"recover_time"` // zero if not recovered within burst window
}

// SQLProfile summarizes one SQL's behavior during the burst window.
type SQLProfile struct {
	SQLID          string  `json:"sql_id"`
	SQLText        string  `json:"sql_text"`
	PlanHashValue  int64   `json:"plan_hash_value"`
	OccurrenceRate float64 `json:"occurrence_rate"`  // fraction of frames containing this SQL (0.0~1.0)
	AvgElapsedSec  float64 `json:"avg_elapsed_sec"`
	MaxElapsedSec  float64 `json:"max_elapsed_sec"`
	Event          string  `json:"event"`            // dominant wait event (e.g., "enq: TX - row lock contention")
	WaitClass      string  `json:"wait_class"`       // dominant wait class
	MaxConcurrent  int     `json:"max_concurrent"`   // max concurrent sessions running this SQL
	// Execution plan characteristics (from v$sql_plan).
	HasFullScan   bool  `json:"has_full_scan,omitempty"`
	HasSort       bool  `json:"has_sort,omitempty"`
	HasHashJoin   bool  `json:"has_hash_join,omitempty"`
	HasIndex      bool  `json:"has_index,omitempty"`
	// v$sqlarea statistics.
	Invalidations int   `json:"invalidations,omitempty"`
	VersionCount  int   `json:"version_count,omitempty"`
	DiskReads     int64 `json:"disk_reads,omitempty"`
	BufferGets    int64 `json:"buffer_gets,omitempty"`
	Executions    int64 `json:"executions,omitempty"`
	RowsProcessed int64 `json:"rows_processed,omitempty"`
	// Plan drift: multiple plan_hash_values for same sql_id.
	PlanCount         int     `json:"plan_count,omitempty"`          // distinct plan_hash_value count
	BestPlanAvgSec    float64 `json:"best_plan_avg_sec,omitempty"`   // fastest plan's avg elapsed
	CurrentPlanAvgSec float64 `json:"current_plan_avg_sec,omitempty"` // current (most recent) plan's avg elapsed
	// SQL Advisor findings (not serialized).
	AdvisorReport *sqladvisor.SQLReport `json:"-"`
}

// BlockingChain represents a lock blocking tree found during burst.
type BlockingChain struct {
	RootSID       int    `json:"root_sid"`
	RootUser      string `json:"root_user"`
	RootSQLID     string `json:"root_sql_id"`
	RootEvent     string `json:"root_event"`       // dominant wait event of victims (describes lock type)
	VictimCount   int    `json:"victim_count"`
	MaxDepth      int    `json:"max_depth"`
	BlockerEvent   string `json:"blocker_event,omitempty"`   // blocker's own wait event
	BlockerStatus  string `json:"blocker_status,omitempty"`  // blocker's session status (ACTIVE/INACTIVE)
	BlockerCommand int    `json:"blocker_command,omitempty"` // blocker's command type (e.g., 2=INSERT, 6=UPDATE, 15=ALTER TABLE)
}

// WaitBucket groups wait time by event during the burst window.
type WaitBucket struct {
	Event      string  `json:"event"`      // specific wait event name (e.g., "enq: TX - row lock contention")
	WaitClass  string  `json:"wait_class"` // wait class (e.g., "Application")
	TotalMs    float64 `json:"total_ms"`
	Percentage float64 `json:"percentage"` // 0.0~100.0
}

// MetricSummary holds aggregated stats for one metric across burst frames.
type MetricSummary struct {
	Avg   float64 `json:"avg"`
	Max   float64 `json:"max"`
	Min   float64 `json:"min"`
	Trend string  `json:"trend"` // "rising" | "falling" | "stable" | "spike" | "drop"
}

// Classification is the output of rule-based root cause analysis.
type Classification struct {
	Cause      RootCauseType `json:"cause"`
	Confidence float64       `json:"confidence"` // 0.0~1.0
	Evidence   []string      `json:"evidence"`   // human-readable evidence strings
}

// BurstReport is the aggregated analysis of a burst collection.
type BurstReport struct {
	TriggerEvent   TriggerEvent `json:"trigger_event"`
	DurationSec    float64      `json:"duration_sec"`
	PeakActive     int          `json:"peak_active"`
	BaselineActive float64      `json:"baseline_active"`

	TopSQLs        []SQLProfile             `json:"top_sqls"`
	BlockingChains []BlockingChain          `json:"blocking_chains"`
	WaitProfile    []WaitBucket             `json:"wait_profile"`
	Metrics        map[string]MetricSummary `json:"metrics"`

	PlanHistory    map[string][]int64     `json:"plan_history,omitempty"`    // sql_id → plan_hash_value list
	ResourceLimits []ResourceLimitEntry   `json:"resource_limits,omitempty"` // sessions/processes limits

	// Block G: space details (storage scenarios).
	SpaceDetails []SpaceDetail `json:"space_details,omitempty"`
	// Block H: related DB parameters (memory/cache, redo, etc.).
	ParamDetails []ParamDetail `json:"param_details,omitempty"`

	Classification Classification `json:"classification"`

	RawFrameCount int       `json:"raw_frame_count"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`

	// Extended fields for JSON rule engine support.
	DBVersion    string `json:"db_version,omitempty"`    // e.g., "19.21.0.0.0"
	WorkloadType string `json:"workload_type,omitempty"` // "oltp" or "olap" (inferred)
}

// SpaceDetail represents one space usage entry (tablespace, temp, undo, FRA, ASM).
type SpaceDetail struct {
	Name    string  `json:"name"`
	UsedMB  float64 `json:"used_mb"`
	TotalMB float64 `json:"total_mb"`
	UsedPct float64 `json:"used_pct"`
	Extra   string  `json:"extra,omitempty"` // autoextend, retention, file_type, etc.
}

// ResourceLimitEntry represents a v$resource_limit row for sessions/processes.
type ResourceLimitEntry struct {
	ResourceName       string  `json:"resource_name"`
	CurrentUtilization float64 `json:"current_utilization"`
	MaxUtilization     float64 `json:"max_utilization"`
	LimitValue         float64 `json:"limit_value"`
}

// ParamDetail represents a database parameter relevant to the scenario.
type ParamDetail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Remediation holds pre-built investigation and fix templates.
type Remediation struct {
	InvestigateSQL []string `json:"investigate_sql"` // SQL commands or skill names to run
	Suggestions    []string `json:"suggestions"`     // human-readable fix suggestions
	Parameters     []string `json:"parameters"`      // related DB parameters to check
}

// TriggerMode controls how anomaly thresholds are calculated.
type TriggerMode string

const (
	TriggerAdaptive TriggerMode = "adaptive" // 3σ auto-threshold (default)
	TriggerFixed    TriggerMode = "fixed"    // fixed multiplier/absolute thresholds
)

// MetricThreshold defines the fixed-mode threshold for one metric.
type MetricThreshold struct {
	Enabled    bool    `json:"enabled"`    // whether this metric triggers detection
	Multiplier float64 `json:"multiplier"` // trigger when current > baseline * multiplier (0 = use absolute)
	Absolute   float64 `json:"absolute"`   // trigger when current > absolute value (0 = use multiplier)
}

// HardwareProfile holds the host's hardware capabilities.
// Used to scale absolute trigger thresholds dynamically.
// Zero values mean unknown (fallback to fixed minimums).
type HardwareProfile struct {
	CPUCores int     `json:"cpu_cores"` // from V$OSSTAT NUM_CPU_CORES
	MemoryGB float64 `json:"memory_gb"` // from V$OSSTAT PHYSICAL_MEMORY_BYTES
}

// Config controls sentinel behavior.
type Config struct {
	// ── Probe ──
	PollInterval   time.Duration `json:"poll_interval"`   // probe interval (default 1s)
	BaselineWindow int           `json:"baseline_window"` // rolling window size (default 60)
	MinSamples     int           `json:"min_samples"`     // minimum samples before detection (default 10)

	// ── Probe tuning ──
	LongSQLThresholdSec int `json:"long_sql_threshold_sec"` // seconds for long SQL detection (default 30)

	// ── Trigger ──
	TriggerMode    TriggerMode `json:"trigger_mode"`    // "adaptive" (3σ) or "fixed" (multiplier/absolute)
	SigmaThreshold float64     `json:"sigma_threshold"` // σ multiplier for adaptive mode (default 3.0)
	SustainedCount int         `json:"sustained_count"` // consecutive ticks required to trigger (default 3)

	// Fixed-mode thresholds (only used when TriggerMode = "fixed").
	FixedThresholds map[MetricName]MetricThreshold `json:"fixed_thresholds"`

	// ── Hardware-based absolute thresholds ──
	Hardware HardwareProfile `json:"hardware"` // populated at runtime from Oracle

	// ── Burst ──
	BurstInterval  time.Duration `json:"burst_interval"`  // burst frame interval (default 200ms)
	BurstDuration  time.Duration `json:"burst_duration"`  // max burst window (default 30s)
	BurstCalmDelay time.Duration `json:"burst_calm_delay"` // confirm recovery duration (default 5s)
	CooldownPeriod time.Duration `json:"cooldown_period"` // min gap between triggers (default 5min)
}

// DefaultConfig returns production default sentinel configuration.
func DefaultConfig() Config {
	return Config{
		PollInterval:        time.Second,
		BaselineWindow:      60,
		MinSamples:          10,
		LongSQLThresholdSec: 30,
		TriggerMode:         TriggerAdaptive,
		SigmaThreshold:      3.0,
		SustainedCount:      3,
		FixedThresholds: map[MetricName]MetricThreshold{
			MetricActive:    {Enabled: true, Multiplier: 2.0},
			MetricCPU:       {Enabled: true, Multiplier: 2.0},
			MetricIO:        {Enabled: true, Multiplier: 3.0},
			MetricLock:      {Enabled: true, Absolute: 5},
			MetricLongSQL:   {Enabled: true, Absolute: 3},
			MetricRedoRate:  {Enabled: true, Multiplier: 5.0},
			MetricHardParse: {Enabled: true, Multiplier: 3.0},
		},
		BurstInterval:  200 * time.Millisecond,
		BurstDuration:  30 * time.Second,
		BurstCalmDelay: 5 * time.Second,
		CooldownPeriod: 5 * time.Minute,
	}
}
