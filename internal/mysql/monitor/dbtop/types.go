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
 *	  internal/mysql/monitor/dbtop/types.go
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

// CollectMode controls the collector's sampling behavior.
type CollectMode uint8

const (
	ModeNormal CollectMode = iota
	ModeBurst
)

// Snapshot holds all data for one frame of the MySQL dbtop dashboard.
type Snapshot struct {
	// DB Info (cached, queried once)
	Version      string
	InstanceName string
	DBRole       string // PRIMARY or REPLICA
	Timestamp    time.Time

	// InnoDB Buffer Pool
	BPUsedMB float64
	BPMaxMB  float64

	// CPU/Wait ratios
	DBPercent  float64 // Active / max_connections * 100
	WTRPercent float64 // (Active - ActiveCPU) / Active * 100
	HasDelta   bool

	// Session counts (aligned with Oracle)
	TotalSessions int
	ActiveCount   int
	ActiveCPU     int
	ActiveIO      int
	IdleCount     int

	// Throughput (delta-based)
	TPS     float64
	QPS     float64
	RedoKBs float64

	// Top wait events (up to 5)
	Events []WaitEvent

	// Active sessions (sorted by ElapsedSec DESC)
	Sessions []SessionRow

	// Health
	Health HealthLevel
	Alerts []string
}

// WaitEvent represents one row in the Top Wait Events panel.
type WaitEvent struct {
	Event   string
	Count   int64
	TimeSec float64
	PCT     float64

	RawTimePico int64

	// Delta values
	DCount  int64
	DTimeMs float64
	DPCT    float64
}

// SessionRow represents one row in the Active Sessions panel.
// Field names align with Oracle dbtop for consistency.
type SessionRow struct {
	SID        int
	Username   string
	SQLID      string
	Event      string
	WaitClass  string
	ElapsedSec float64
	SQLText    string
	Program    string
	Status     string
}

// DeltaState stores previous-frame values for delta calculations.
type DeltaState struct {
	PrevCommits   int64
	PrevRollbacks int64
	PrevQueries   int64
	PrevRedoBytes int64
	PrevTimestamp  time.Time
	PrevTPS       float64

	PrevEventCount map[string]int64
	PrevEventTime  map[string]int64

	Initialized bool
}
