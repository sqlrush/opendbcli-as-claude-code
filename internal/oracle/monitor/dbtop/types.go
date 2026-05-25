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
 *	  internal/oracle/monitor/dbtop/types.go
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

// Snapshot holds all data for one frame of the dbtop dashboard.
type Snapshot struct {
	// DB Info (cached, queried once)
	Version      string
	InstanceName string
	DBRole       string
	Timestamp    time.Time

	// Memory
	SGAUsedMB float64
	SGAMaxMB  float64
	PGAUsedMB float64
	PGAMaxMB  float64

	// CPU/Wait ratios (delta-based)
	DBPercent  float64
	WTRPercent float64
	HasDelta   bool

	// Session counts
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
	Event     string
	Waits     int64   // cumulative waits
	TimeSec   float64 // cumulative time (seconds)
	PCT       float64 // cumulative PCT
	WaitClass string

	RawTimeMicro int64 // raw microseconds for lossless delta computation

	// Delta (differential) values: current - previous snapshot
	DWaits  int64   // differential waits
	DTimeMs float64 // differential time (milliseconds)
	DPCT    float64 // differential PCT
}

// CollectMode controls the collector's sampling behavior.
type CollectMode uint8

const (
	ModeNormal CollectMode = iota // 1s interval, standard fields
	ModeBurst                     // 200ms interval, extended fields (blocking, plan hash)
)

// SessionRow represents one row in the Active Sessions panel.
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

	// Burst-mode extended fields (nil in normal mode)
	Burst *BurstDetail
}

// BurstDetail holds extra fields only collected in burst mode.
type BurstDetail struct {
	BlockingSID   int
	PlanHashValue int64
	Machine       string
	WaitTimeSec   float64
	SQLChildNum   int
}

// DeltaState stores previous-frame values for delta calculations.
type DeltaState struct {
	PrevCPUTime   int64
	PrevDBTime    int64
	PrevCommits   int64
	PrevRollbacks int64
	PrevExecutes  int64
	PrevRedoSize  int64
	PrevTimestamp time.Time
	PrevTPS       float64

	PrevEventWaits     map[string]int64
	PrevEventTime      map[string]int64
	PrevTotalEventTime int64
	PrevCPUTimeEvent   int64

	Initialized bool
}
