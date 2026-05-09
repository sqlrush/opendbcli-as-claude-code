/*-------------------------------------------------------------------------
 *
 * types.go
 *	  MetricName identifies a OG sentinel probe metric.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/types.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "time"

// MetricName identifies a OG sentinel probe metric.
type MetricName string

const (
	MetricActiveSessions    MetricName = "active_sessions"
	MetricIdleInTransaction MetricName = "idle_in_transaction"
	MetricLockWaits         MetricName = "lock_waits"
	MetricLongQueries       MetricName = "long_queries"
	MetricXactCommitRate    MetricName = "xact_commit_rate"
	MetricCacheHitPct       MetricName = "cache_hit_pct"
	MetricDeadTupleRatio    MetricName = "dead_tuple_ratio"
	MetricXIDAgeRatio       MetricName = "xid_age_pct"
	MetricReplicationLag    MetricName = "replication_lag_sec"
	MetricTempBytesRate     MetricName = "temp_bytes_rate"
	MetricCheckpointsReq    MetricName = "checkpoints_req"
	MetricWALBytesRate      MetricName = "wal_bytes_rate"
	MetricConnectionsPct    MetricName = "connections_pct"
	MetricBlockerCount      MetricName = "blocker_count"
	MetricDeadlocks         MetricName = "deadlocks"
)

// RootCauseType classifies the probable root cause of a OG anomaly.
type RootCauseType string

const (
	CauseSlowQuery        RootCauseType = "slow_query"
	CauseLockContention   RootCauseType = "lock_contention"
	CauseVacuumLag        RootCauseType = "vacuum_lag"
	CauseXIDWraparound    RootCauseType = "xid_wraparound"
	CauseWALBottleneck    RootCauseType = "wal_bottleneck"
	CauseConnectionStorm  RootCauseType = "connection_storm"
	CauseReplicationLag   RootCauseType = "replication_lag"
	CauseCheckpointStorm  RootCauseType = "checkpoint_storm"
	CauseUnknown          RootCauseType = "unknown"
)

// String returns the Chinese display name for the root cause type.
func (r RootCauseType) String() string {
	switch r {
	case CauseSlowQuery:
		return "慢SQL冲高"
	case CauseLockContention:
		return "锁等待阻塞"
	case CauseVacuumLag:
		return "Vacuum滞后"
	case CauseXIDWraparound:
		return "XID回卷风险"
	case CauseWALBottleneck:
		return "WAL冲高"
	case CauseConnectionStorm:
		return "连接数冲高"
	case CauseReplicationLag:
		return "复制延迟"
	case CauseCheckpointStorm:
		return "Checkpoint冲高"
	default:
		return "未知"
	}
}

// IsValid returns true if the root cause type is a known value.
func (r RootCauseType) IsValid() bool {
	switch r {
	case CauseSlowQuery, CauseLockContention, CauseVacuumLag,
		CauseXIDWraparound, CauseWALBottleneck, CauseConnectionStorm,
		CauseReplicationLag, CauseCheckpointStorm, CauseUnknown:
		return true
	default:
		return false
	}
}

// MetricSample holds values from one probe cycle.
type MetricSample struct {
	Timestamp time.Time              `json:"timestamp"`
	Values    map[MetricName]float64 `json:"values"`
}

// MetricBaseline holds rolling stats for a single metric.
type MetricBaseline struct {
	Avg   float64 `json:"avg"`
	Std   float64 `json:"std"`
	Ready bool    `json:"ready"`
}

// Baseline holds rolling statistics for anomaly detection.
type Baseline struct {
	Samples []MetricSample                 `json:"-"`
	Window  int                            `json:"window"`
	Ready   bool                           `json:"ready"`
	Metrics map[MetricName]*MetricBaseline `json:"metrics"`
}

// TriggerEvent is emitted when an anomaly exceeds the 3-sigma threshold.
type TriggerEvent struct {
	Timestamp  time.Time  `json:"timestamp"`
	Metric     string     `json:"metric"`
	Baseline   float64    `json:"baseline"`
	Current    float64    `json:"current"`
	Threshold  float64    `json:"threshold"`
	Multiplier float64    `json:"multiplier"`
}

// SQLProfile represents a top SQL from pg_stat_activity.
type SQLProfile struct {
	QueryID       string  `json:"query_id"`
	Query         string  `json:"query"`
	Calls         int     `json:"calls"`
	MeanTimeSec   float64 `json:"mean_time_sec"`
	MaxTimeSec    float64 `json:"max_time_sec"`
	WaitEventType string  `json:"wait_event_type"`
	WaitEvent     string  `json:"wait_event"`
	ActiveCount   int     `json:"active_count"`
}

// WaitBucket represents one wait event in the wait profile.
type WaitBucket struct {
	WaitEventType string  `json:"wait_event_type"`
	WaitEvent     string  `json:"wait_event"`
	Count         int     `json:"count"`
	Percentage    float64 `json:"percentage"`
}

// BlockingChain represents a blocking relationship.
type BlockingChain struct {
	BlockerPID   int    `json:"blocker_pid"`
	BlockerUser  string `json:"blocker_user"`
	BlockerQuery string `json:"blocker_query"`
	VictimCount  int    `json:"victim_count"`
	WaitEvent    string `json:"wait_event"`
}

// BurstReport is the aggregated analysis of a burst collection.
type BurstReport struct {
	TriggerEvent   TriggerEvent             `json:"trigger_event"`
	DurationSec    float64                  `json:"duration_sec"`
	PeakActive     int                      `json:"peak_active"`
	BaselineActive float64                  `json:"baseline_active"`
	Metrics        map[string]MetricSummary `json:"metrics"`
	Classification Classification           `json:"classification"`
	Frames         []MetricSample           `json:"-"`
	StartTime      time.Time                `json:"start_time"`
	EndTime        time.Time                `json:"end_time"`
	TopSQLs        []SQLProfile             `json:"top_sqls,omitempty"`
	WaitProfile    []WaitBucket             `json:"wait_profile,omitempty"`
	BlockingChains []BlockingChain          `json:"blocking_chains,omitempty"`
}

// MetricSummary holds aggregated stats for one metric across burst frames.
type MetricSummary struct {
	Avg   float64 `json:"avg"`
	Max   float64 `json:"max"`
	Min   float64 `json:"min"`
	Trend string  `json:"trend"`
}

// Classification is the output of rule-based root cause analysis.
type Classification struct {
	Cause      RootCauseType `json:"cause"`
	Confidence float64       `json:"confidence"`
	Evidence   []string      `json:"evidence"`
}

// Config controls OG sentinel behavior.
type Config struct {
	PollInterval   time.Duration `json:"poll_interval"`
	BaselineWindow int           `json:"baseline_window"`
	MinSamples     int           `json:"min_samples"`
	SigmaThreshold float64      `json:"sigma_threshold"`
	SustainedCount int           `json:"sustained_count"`
	BurstDuration  time.Duration `json:"burst_duration"`
	CooldownPeriod time.Duration `json:"cooldown_period"`
}

// DefaultConfig returns production default OG sentinel configuration.
func DefaultConfig() Config {
	return Config{
		PollInterval:   time.Second,
		BaselineWindow: 60,
		MinSamples:     10,
		SigmaThreshold: 3.0,
		SustainedCount: 3,
		BurstDuration:  30 * time.Second,
		CooldownPeriod: 5 * time.Minute,
	}
}

// MetricLabel returns the Chinese display label for a metric.
func MetricLabel(metric MetricName) string {
	labels := map[MetricName]string{
		MetricActiveSessions:    "活跃会话",
		MetricIdleInTransaction: "Idle in Transaction",
		MetricLockWaits:         "锁等待",
		MetricLongQueries:       "慢查询(>30s)",
		MetricXactCommitRate:    "提交速率/s",
		MetricCacheHitPct:       "缓存命中率%",
		MetricDeadTupleRatio:    "死元组比例%",
		MetricXIDAgeRatio:       "XID年龄%",
		MetricReplicationLag:    "复制延迟秒",
		MetricTempBytesRate:     "临时空间速率",
		MetricCheckpointsReq:    "请求Checkpoint",
		MetricWALBytesRate:      "WAL速率",
		MetricConnectionsPct:    "连接使用%",
		MetricBlockerCount:      "阻塞者数",
		MetricDeadlocks:         "死锁数",
	}
	if label, ok := labels[metric]; ok {
		return label
	}
	return string(metric)
}

// MetricUnit returns the display unit for a metric.
func MetricUnit(metric MetricName) string {
	units := map[MetricName]string{
		MetricActiveSessions:    "个",
		MetricIdleInTransaction: "个",
		MetricLockWaits:         "个",
		MetricLongQueries:       "个",
		MetricXactCommitRate:    "/s",
		MetricCacheHitPct:       "%",
		MetricDeadTupleRatio:    "%",
		MetricXIDAgeRatio:       "%",
		MetricReplicationLag:    "s",
		MetricTempBytesRate:     "B/s",
		MetricCheckpointsReq:    "个",
		MetricWALBytesRate:      "B/s",
		MetricConnectionsPct:    "%",
		MetricBlockerCount:      "个",
		MetricDeadlocks:         "个",
	}
	if unit, ok := units[metric]; ok {
		return unit
	}
	return ""
}
