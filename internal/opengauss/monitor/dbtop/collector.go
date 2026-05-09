/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  Collector queries OpenGauss via db.Driver and assembles Snapshot
 *	  values.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/monitor/dbtop/collector.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/odberr"
)

// debugLog writes debug info to /tmp/opendb-og-dbtop.log
func debugLog(format string, args ...any) {
	f, err := os.OpenFile("/tmp/opendb-og-dbtop.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", args...)
}

// SQL constants for OpenGauss data dictionary queries.
const (
	dbRoleSQL = `SELECT CASE WHEN pg_is_in_recovery() THEN 'STANDBY' ELSE 'PRIMARY' END`

	sharedBufSQL = `SELECT setting::bigint * 8192 / 1048576 FROM pg_settings WHERE name = 'shared_buffers'`

	cacheHitSQL = `SELECT
  COALESCE(SUM(blks_hit), 0) AS hit,
  COALESCE(SUM(blks_read), 0) AS read
FROM pg_stat_database`

	sessionCountSQL = `SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE state = 'active'),
  COUNT(*) FILTER (WHERE state = 'idle'),
  COUNT(*) FILTER (WHERE waiting = true AND state = 'active')
FROM pg_stat_activity
WHERE pid != pg_backend_pid()`

	throughputSQL = `SELECT
  xact_commit, xact_rollback,
  tup_returned + tup_fetched + tup_inserted + tup_updated + tup_deleted
FROM pg_stat_database
WHERE datname = current_database()`

	// OpenGauss: use pg_current_xlog_location() to compute WAL position in bytes.
	walLSNSQL = `SELECT pg_xlog_location_diff(pg_current_xlog_location(), '0/0')::bigint`

	waitEventsSQL = `SELECT
  CASE WHEN waiting THEN 'Lock Wait' WHEN state = 'active' THEN 'On CPU' ELSE state END AS event,
  COUNT(*) AS sessions
FROM pg_stat_activity
WHERE pid != pg_backend_pid()
  AND state IS NOT NULL
GROUP BY event
ORDER BY sessions DESC
LIMIT 10`

	activeSessionsSQL = `SELECT
  pid, usename,
  CASE WHEN waiting THEN 'lock_wait' WHEN enqueue != '' THEN enqueue ELSE 'On CPU' END AS event,
  EXTRACT(EPOCH FROM clock_timestamp() - query_start)::numeric(10,1) AS elapsed_sec,
  LEFT(query, 80) AS sql_text
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()
ORDER BY query_start
LIMIT 10`
)

// Collector queries OpenGauss via db.Driver and assembles Snapshot values.
type Collector struct {
	driver         db.Driver
	state          DeltaState
	cachedRole     string
	cachedVersion  string
	cachedInstance string
	mode           CollectMode
	hasWALStats    *bool // nil = unknown, true/false after first check
}

// SetMode switches between normal and burst collection modes.
func (c *Collector) SetMode(m CollectMode) { c.mode = m }

// Mode returns the current collection mode.
func (c *Collector) Mode() CollectMode { return c.mode }

// NewCollector creates a Collector and caches static server info.
func NewCollector(driver db.Driver) *Collector {
	info := driver.ServerInfo()
	return &Collector{
		driver:         driver,
		cachedVersion:  info.Version,
		cachedInstance: info.InstanceName,
	}
}

// Collect runs all OpenGauss queries in parallel and returns a Snapshot.
func (c *Collector) Collect(ctx context.Context) Snapshot {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		snap Snapshot
	)

	snap.Version = c.cachedVersion
	snap.InstanceName = c.cachedInstance
	snap.Timestamp = time.Now()

	// Group 1: DB role + shared_buffers + cache hit + throughput + WAL
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		role := c.queryDBRole(ctx)
		sBuf := c.queryScalar(ctx, sharedBufSQL, "SBUF")
		cacheHit := c.queryCacheHit(ctx)
		commits, rollbacks, queries := c.queryThroughput(ctx)
		walBytes := c.queryWALBytes(ctx)

		mu.Lock()
		c.cachedRole = role
		snap.DBRole = role
		snap.SBufSizeMB = sBuf
		snap.CacheHitPct = cacheHit

		raw := RawSample{
			Commits:   commits,
			Rollbacks: rollbacks,
			Queries:   queries,
			WALBytes:  walBytes,
			Timestamp: snap.Timestamp,
		}
		delta := ComputeDeltas(&c.state, raw)
		snap.TPS = delta.TPS
		snap.QPS = delta.QPS
		snap.WALKBs = delta.WALKBs
		mu.Unlock()
	})

	// Group 2: Session counts
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		total, active, idle, waiting := c.querySessionCounts(ctx)

		mu.Lock()
		snap.TotalSessions = int(total)
		snap.ActiveCount = int(active)
		snap.IdleCount = int(idle)
		snap.WaitingCount = int(waiting)
		mu.Unlock()
	})

	// Group 3: Wait events
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		events := c.queryWaitEvents(ctx)

		mu.Lock()
		snap.Events = events
		mu.Unlock()
	})

	// Group 4: Active sessions
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		sessions := c.queryActiveSessions(ctx)

		mu.Lock()
		snap.Sessions = sessions
		mu.Unlock()
	})

	wg.Wait()

	// Fill role from cache if it was already set
	if snap.DBRole == "" && c.cachedRole != "" {
		snap.DBRole = c.cachedRole
	}

	EvaluateHealth(&snap, c.state.PrevTPS)

	return snap
}

// queryDBRole returns the database role or empty string on error.
func (c *Collector) queryDBRole(ctx context.Context) string {
	res, err := c.driver.Query(ctx, dbRoleSQL)
	if err != nil || len(res.Rows) == 0 {
		return ""
	}
	role, _ := res.Rows[0][0].(string)
	return role
}

// queryScalar returns a single numeric value or 0 on error.
func (c *Collector) queryScalar(ctx context.Context, sqlStr, label string) float64 {
	res, err := c.driver.Query(ctx, sqlStr)
	if err != nil {
		debugLog("[%s] query error: %v", label, err)
		return 0
	}
	if len(res.Rows) == 0 {
		debugLog("[%s] no rows returned", label)
		return 0
	}
	return toFloat(res.Rows[0][0])
}

// queryCacheHit returns the buffer cache hit ratio percentage.
func (c *Collector) queryCacheHit(ctx context.Context) float64 {
	res, err := c.driver.Query(ctx, cacheHitSQL)
	if err != nil || len(res.Rows) == 0 {
		return 0
	}
	hit := toFloat(res.Rows[0][0])
	read := toFloat(res.Rows[0][1])
	total := hit + read
	if total <= 0 {
		return 0
	}
	return hit / total * 100
}

// queryThroughput returns commits, rollbacks, and query count from pg_stat_database.
func (c *Collector) queryThroughput(ctx context.Context) (commits, rollbacks, queries int64) {
	res, err := c.driver.Query(ctx, throughputSQL)
	if err != nil || len(res.Rows) == 0 {
		return 0, 0, 0
	}
	row := res.Rows[0]
	if len(row) < 3 {
		return 0, 0, 0
	}
	return toInt(row[0]), toInt(row[1]), toInt(row[2])
}

// queryWALBytes returns current WAL position in bytes using pg_current_xlog_location().
// Gracefully returns 0 if the function is not available.
func (c *Collector) queryWALBytes(ctx context.Context) int64 {
	if c.hasWALStats != nil && !*c.hasWALStats {
		return 0
	}
	res, err := c.driver.Query(ctx, walLSNSQL)
	if err != nil {
		f := false
		c.hasWALStats = &f
		return 0
	}
	t := true
	c.hasWALStats = &t
	if len(res.Rows) == 0 {
		return 0
	}
	return toInt(res.Rows[0][0])
}

// querySessionCounts returns total, active, idle, waiting counts.
func (c *Collector) querySessionCounts(ctx context.Context) (total, active, idle, waiting int64) {
	res, err := c.driver.Query(ctx, sessionCountSQL)
	if err != nil {
		debugLog("[SESSION] query error: %v", err)
		return 0, 0, 0, 0
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) < 4 {
		return 0, 0, 0, 0
	}
	row := res.Rows[0]
	return toInt(row[0]), toInt(row[1]), toInt(row[2]), toInt(row[3])
}

// queryWaitEvents returns the top wait events from pg_stat_activity snapshot.
func (c *Collector) queryWaitEvents(ctx context.Context) []WaitEvent {
	res, err := c.driver.Query(ctx, waitEventsSQL)
	if err != nil {
		debugLog("[EVENTS] query error: %v", err)
		return nil
	}
	if len(res.Rows) == 0 {
		return nil
	}

	events := make([]WaitEvent, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		event, _ := row[0].(string)
		sessions := int(toInt(row[1]))

		events = append(events, WaitEvent{
			WaitType: event,
			Event:    event,
			Sessions: sessions,
		})
	}
	return events
}

// queryActiveSessions returns active session rows sorted by query_start.
func (c *Collector) queryActiveSessions(ctx context.Context) []SessionRow {
	res, err := c.driver.Query(ctx, activeSessionsSQL)
	if err != nil || len(res.Rows) == 0 {
		return nil
	}

	sessions := make([]SessionRow, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 5 {
			continue
		}
		sessions = append(sessions, SessionRow{
			PID:        int(toInt(row[0])),
			Username:   strVal(row[1]),
			Event:      strVal(row[2]),
			ElapsedSec: toFloat(row[3]),
			SQLText:    strVal(row[4]),
		})
	}
	return sessions
}

// strVal safely extracts a string from an any value.
func strVal(v any) string {
	s, _ := v.(string)
	return s
}

// toFloat converts various numeric types to float64.
func toFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

// toInt converts various numeric types to int64.
func toInt(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	case float32:
		return int64(val)
	case int32:
		return int64(val)
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0
		}
		return int64(f)
	default:
		return 0
	}
}
