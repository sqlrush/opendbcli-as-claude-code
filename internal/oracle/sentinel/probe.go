/*-------------------------------------------------------------------------
 *
 * probe.go
 *	  ProbeResult holds the lightweight probe metrics from a single SQL
 *	  query.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"fmt"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// ProbeResult holds the lightweight probe metrics from a single SQL query.
type ProbeResult struct {
	Timestamp    time.Time
	Active       int // active non-idle sessions
	OnCPU        int // sessions on CPU (no wait)
	IOWait       int // sessions in User I/O or System I/O wait
	LockWait     int // sessions in Application wait (enq: TX etc.)
	LongSQL      int // sessions with SQL running > 10s
}

const probeSQL = `SELECT
  COUNT(*) AS active,
  SUM(CASE WHEN WAIT_CLASS IS NULL THEN 1 ELSE 0 END) AS on_cpu,
  SUM(CASE WHEN WAIT_CLASS IN ('User I/O','System I/O') THEN 1 ELSE 0 END) AS io_wait,
  SUM(CASE WHEN WAIT_CLASS = 'Application' THEN 1 ELSE 0 END) AS lock_wait,
  SUM(CASE WHEN SQL_EXEC_START IS NOT NULL AND (SYSDATE - SQL_EXEC_START)*86400 > 10 THEN 1 ELSE 0 END) AS long_sql
FROM v$session
WHERE STATUS = 'ACTIVE' AND TYPE = 'USER' AND WAIT_CLASS <> 'Idle'`

// sysstatProbeSQL fetches cumulative counters for delta-based metrics.
const sysstatProbeSQL = `SELECT name, value FROM v$sysstat
WHERE name IN ('redo size', 'parse count (hard)')`

// ProbeCollector runs lightweight probes against the database.
type ProbeCollector struct {
	driver   db.Driver
	probeSQL string // generated with configurable long_sql threshold

	// Previous sysstat values for delta calculation.
	prevRedoSize  int64
	prevHardParse int64
	prevTime      time.Time
	initialized   bool
}

// NewProbeCollector creates a new lightweight probe collector.
// longSQLThresholdSec sets the seconds threshold for long SQL detection (default 30).
func NewProbeCollector(driver db.Driver, longSQLThresholdSec ...int) *ProbeCollector {
	threshold := 30
	if len(longSQLThresholdSec) > 0 && longSQLThresholdSec[0] > 0 {
		threshold = longSQLThresholdSec[0]
	}
	return &ProbeCollector{
		driver: driver,
		probeSQL: fmt.Sprintf(`SELECT
  COUNT(*) AS active,
  SUM(CASE WHEN WAIT_CLASS IS NULL THEN 1 ELSE 0 END) AS on_cpu,
  SUM(CASE WHEN WAIT_CLASS IN ('User I/O','System I/O') THEN 1 ELSE 0 END) AS io_wait,
  SUM(CASE WHEN WAIT_CLASS = 'Application' THEN 1 ELSE 0 END) AS lock_wait,
  SUM(CASE WHEN SQL_EXEC_START IS NOT NULL AND (SYSDATE - SQL_EXEC_START)*86400 > %d THEN 1 ELSE 0 END) AS long_sql
FROM v$session
WHERE STATUS = 'ACTIVE' AND TYPE = 'USER' AND WAIT_CLASS <> 'Idle'`, threshold),
	}
}

// Driver returns the underlying database driver for post-burst queries.
func (p *ProbeCollector) Driver() db.Driver { return p.driver }

// Probe runs a single lightweight SQL to collect session-based metrics,
// plus a sysstat query for redo/parse rate deltas.
func (p *ProbeCollector) Probe(ctx context.Context) (ProbeResult, SysstatDelta, error) {
	result := ProbeResult{Timestamp: time.Now()}
	var delta SysstatDelta

	// Session-based metrics (1 SQL).
	rows, err := p.driver.Query(ctx, p.probeSQL)
	if err != nil {
		return result, delta, err
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 5 {
		row := rows.Rows[0]
		result.Active = toInt(row[0])
		result.OnCPU = toInt(row[1])
		result.IOWait = toInt(row[2])
		result.LockWait = toInt(row[3])
		result.LongSQL = toInt(row[4])
	}

	// Sysstat counters for delta metrics (1 SQL).
	sysRows, err := p.driver.Query(ctx, sysstatProbeSQL)
	if err != nil {
		// Non-fatal: return session metrics without sysstat.
		return result, delta, nil
	}

	var curRedoSize, curHardParse int64
	for _, row := range sysRows.Rows {
		if len(row) < 2 {
			continue
		}
		name := toString(row[0])
		val := toInt64(row[1])
		switch name {
		case "redo size":
			curRedoSize = val
		case "parse count (hard)":
			curHardParse = val
		}
	}

	now := time.Now()
	if p.initialized {
		elapsed := now.Sub(p.prevTime).Seconds()
		if elapsed > 0 {
			delta.RedoKBPerSec = float64(curRedoSize-p.prevRedoSize) / 1024 / elapsed
			delta.HardParsePerSec = float64(curHardParse-p.prevHardParse) / elapsed
			delta.Valid = true
		}
	}

	p.prevRedoSize = curRedoSize
	p.prevHardParse = curHardParse
	p.prevTime = now
	p.initialized = true

	return result, delta, nil
}

// SysstatDelta holds computed per-second rates from v$sysstat deltas.
type SysstatDelta struct {
	RedoKBPerSec    float64 // redo generation rate in KB/s
	HardParsePerSec float64 // hard parse count per second
	Valid           bool    // false on first probe (no previous data)
}

// toInt converts any value to int (best-effort).
func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case string:
		n := 0
		for _, c := range val {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		return n
	default:
		return 0
	}
}

// toInt64 converts any value to int64 (best-effort).
func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case string:
		var n int64
		for _, c := range val {
			if c >= '0' && c <= '9' {
				n = n*10 + int64(c-'0')
			}
		}
		return n
	default:
		return 0
	}
}

// toString converts any value to string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
