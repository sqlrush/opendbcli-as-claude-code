/*-------------------------------------------------------------------------
 *
 * types.go
 *	  HealthLevel represents the overall database health.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/monitor/dbtop/types.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "time"

// HealthLevel represents the overall database health.
type HealthLevel uint8

const (
	Healthy  HealthLevel = iota
	Warning
	Critical
)

func (h HealthLevel) String() string {
	switch h {
	case Warning:
		return "WARNING"
	case Critical:
		return "CRITICAL"
	default:
		return "HEALTHY"
	}
}

// Snapshot holds all data for one frame of the PostgreSQL dbtop dashboard.
type Snapshot struct {
	// DB Info (cached, queried once)
	Version      string
	InstanceName string
	DBRole       string // PRIMARY or STANDBY
	Timestamp    time.Time

	// Memory
	SBufSizeMB  float64 // shared_buffers in MB
	CacheHitPct float64 // cache hit ratio percentage

	// CPU/Wait ratios
	DBTimePct  float64 // active sessions as db% indicator (like Oracle AAS)
	WaitRatio  float64 // waiting / active * 100
	HasDelta   bool    // true after first delta computed

	// Session counts
	TotalSessions  int
	ActiveCount    int
	ActiveCPUCount int // active sessions on CPU (wait_event IS NULL)
	ActiveIOCount  int // active sessions in IO wait (wait_event_type = 'IO')
	IdleCount      int
	WaitingCount   int

	// Throughput (delta-based)
	TPS    float64
	QPS    float64
	WALKBs float64

	// Top wait events (snapshot from pg_stat_activity, not cumulative)
	Events []WaitEvent

	// Active sessions (sorted by elapsed_sec DESC)
	Sessions []SessionRow

	// Health
	Health HealthLevel
	Alerts []string
}

// WaitEvent represents one row in the Top Wait Events panel.
// PostgreSQL wait events come from pg_stat_activity snapshot (session counts),
// unlike Oracle's cumulative counters. CumulSessions is built up by the
// collector across frames (sum of per-frame session counts).
type WaitEvent struct {
	WaitType       string  // wait_event_type or "CPU"
	Event          string  // wait_event or "On CPU"
	Sessions       int     // number of active sessions in this state (current frame)
	Percentage     float64 // % of total active sessions (current frame)
	CumulSessions  int64   // accumulated session-count across all frames
	CumulPct       float64 // % of total cumul across all events
}

// CollectMode controls the collector's sampling behavior.
type CollectMode uint8

const (
	ModeNormal CollectMode = iota // 1s interval
	ModeBurst                     // 200ms interval
)

// SessionRow represents one row in the Active Sessions panel.
type SessionRow struct {
	PID        int
	Username   string
	QueryID    string // query_id from PG14+ (empty if unavailable)
	WaitType   string // wait_event_type or "CPU"
	Event      string
	ElapsedSec float64
	SQLText    string
}

// DeltaState stores previous-frame values for delta calculations.
type DeltaState struct {
	PrevCommits   int64
	PrevRollbacks int64
	PrevQueries   int64
	PrevWALBytes  int64
	PrevTimestamp time.Time
	PrevTPS       float64

	// Cumulative session-count per wait event (accumulated across frames).
	CumulEventSessions map[string]int64

	Initialized bool
}
