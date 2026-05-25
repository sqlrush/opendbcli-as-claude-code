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
 *	  internal/opengauss/monitor/dbtop/types.go
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

// Snapshot holds all data for one frame of the OpenGauss dbtop dashboard.
type Snapshot struct {
	// DB Info (cached, queried once)
	Version      string
	InstanceName string
	DBRole       string // PRIMARY or STANDBY
	Timestamp    time.Time

	// Memory
	SBufSizeMB float64 // shared_buffers in MB
	CacheHitPct float64 // cache hit ratio percentage

	// Session counts
	TotalSessions int
	ActiveCount   int
	IdleCount     int
	WaitingCount  int

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
// OpenGauss wait events come from pg_stat_activity snapshot (session counts),
// unlike Oracle's cumulative counters.
type WaitEvent struct {
	WaitType string // wait_event_type or "CPU"
	Event    string // wait_event or "On CPU"
	Sessions int    // number of active sessions in this state
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

	Initialized bool
}
