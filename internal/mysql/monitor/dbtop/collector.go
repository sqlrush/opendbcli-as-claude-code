/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  Collector queries MySQL via db.Driver and assembles Snapshot
 *	  values.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/monitor/dbtop/collector.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/odberr"
)

// debugLog writes debug info to /tmp/opendb-mysql-dbtop.log
func debugLog(format string, args ...any) {
	f, err := os.OpenFile("/tmp/opendb-mysql-dbtop.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", args...)
}

// SQL constants for MySQL queries.
const (
	roleSQL = `SELECT CASE WHEN @@read_only = 1 THEN 'REPLICA' ELSE 'PRIMARY' END AS role`

	bufferPoolUsedSQL = `SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME IN ('Innodb_buffer_pool_bytes_data','Innodb_buffer_pool_bytes_dirty')`

	bufferPoolMaxSQL = `SELECT @@innodb_buffer_pool_size AS bp_size`

	throughputSQL = `SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME IN ('Handler_commit','Handler_rollback','Queries','Innodb_os_log_written')`

	sessionCountSQL = `SELECT
  SUM(CASE WHEN TYPE = 'FOREGROUND' THEN 1 ELSE 0 END),
  SUM(CASE WHEN TYPE = 'FOREGROUND' AND PROCESSLIST_COMMAND NOT IN ('Sleep','Daemon','Binlog Dump','Binlog Dump GTID') THEN 1 ELSE 0 END),
  SUM(CASE WHEN TYPE = 'FOREGROUND' AND PROCESSLIST_COMMAND NOT IN ('Sleep','Daemon','Binlog Dump','Binlog Dump GTID')
    AND COALESCE(PROCESSLIST_STATE, '') NOT LIKE 'Waiting%'
    AND COALESCE(PROCESSLIST_STATE, '') NOT LIKE '%lock%'
    AND COALESCE(PROCESSLIST_STATE, '') NOT IN ('Sending to client','Reading from net','Writing to net')
    THEN 1 ELSE 0 END),
  SUM(CASE WHEN TYPE = 'FOREGROUND' AND PROCESSLIST_COMMAND NOT IN ('Sleep','Daemon','Binlog Dump','Binlog Dump GTID')
    AND PROCESSLIST_STATE IN ('Sending to client','Reading from net','Writing to net')
    THEN 1 ELSE 0 END),
  SUM(CASE WHEN TYPE = 'FOREGROUND' AND PROCESSLIST_COMMAND = 'Sleep' THEN 1 ELSE 0 END)
FROM performance_schema.threads`

	maxConnSQL = `SELECT @@max_connections AS max_conn`

	waitEventsSQL = `SELECT EVENT_NAME, COUNT_STAR, SUM_TIMER_WAIT FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME NOT LIKE 'wait/idle/%' AND COUNT_STAR > 0 ORDER BY SUM_TIMER_WAIT DESC LIMIT 10`

	activeSessionSQL = `SELECT
  p.ID,
  p.USER,
  IFNULL(LEFT(esc.DIGEST, 13), ''),
  CASE WHEN COALESCE(p.STATE, '') = '' THEN 'ON CPU' ELSE p.STATE END,
  CASE
    WHEN COALESCE(p.STATE, '') = '' THEN 'CPU'
    WHEN p.STATE IN ('Sending to client','Reading from net','Writing to net') THEN 'I/O'
    WHEN p.STATE LIKE '%lock%' OR p.STATE LIKE 'Waiting for table%' THEN 'Lock'
    WHEN p.STATE LIKE 'Waiting%' THEN 'Wait'
    ELSE 'CPU'
  END,
  p.TIME,
  LEFT(p.INFO, 80),
  IFNULL(attr.ATTR_VALUE, ''),
  p.COMMAND
FROM information_schema.PROCESSLIST p
LEFT JOIN performance_schema.threads t ON p.ID = t.PROCESSLIST_ID
LEFT JOIN performance_schema.events_statements_current esc ON t.THREAD_ID = esc.THREAD_ID
LEFT JOIN performance_schema.session_connect_attrs attr ON p.ID = attr.PROCESSLIST_ID AND attr.ATTR_NAME = 'program_name'
WHERE p.COMMAND != 'Sleep' AND p.ID != CONNECTION_ID()
ORDER BY p.TIME DESC
LIMIT 10`

	activeSessionFallbackSQL = `SELECT
  ID, USER, '',
  CASE WHEN COALESCE(STATE, '') = '' THEN 'ON CPU' ELSE STATE END,
  CASE
    WHEN COALESCE(STATE, '') = '' THEN 'CPU'
    WHEN STATE IN ('Sending to client','Reading from net','Writing to net') THEN 'I/O'
    WHEN STATE LIKE '%lock%' OR STATE LIKE 'Waiting for table%' THEN 'Lock'
    WHEN STATE LIKE 'Waiting%' THEN 'Wait'
    ELSE 'CPU'
  END,
  TIME, LEFT(INFO, 80), '', COMMAND
FROM information_schema.PROCESSLIST
WHERE COMMAND != 'Sleep' AND ID != CONNECTION_ID()
ORDER BY TIME DESC
LIMIT 10`
)

// Collector queries MySQL via db.Driver and assembles Snapshot values.
type Collector struct {
	driver         db.Driver
	state          DeltaState
	cachedRole     string
	cachedVersion  string
	cachedInstance string
	mode           CollectMode
	hasRichSession *bool
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

// Collect runs all MySQL queries in parallel and returns a Snapshot.
func (c *Collector) Collect(ctx context.Context) Snapshot {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		snap Snapshot
	)

	snap.Version = c.cachedVersion
	snap.InstanceName = c.cachedInstance
	snap.Timestamp = time.Now()

	// Group 1: DB role + Buffer Pool + throughput + session counts
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		role := c.queryRole(ctx)
		bpUsed := c.queryBPUsed(ctx)
		bpMax := c.queryScalar(ctx, bufferPoolMaxSQL, "BP_MAX")
		commits, rollbacks, queries, redoBytes := c.queryThroughput(ctx)
		maxConn := c.queryScalar(ctx, maxConnSQL, "MAX_CONN")
		total, active, activeCPU, activeIO, idle := c.querySessionCounts(ctx)

		mu.Lock()
		c.cachedRole = role
		snap.DBRole = role
		snap.BPUsedMB = bpUsed / 1024.0 / 1024.0
		snap.BPMaxMB = bpMax / 1024.0 / 1024.0
		snap.TotalSessions = int(total)
		snap.ActiveCount = int(active)
		snap.ActiveCPU = int(activeCPU)
		snap.ActiveIO = int(activeIO)
		snap.IdleCount = int(idle)

		raw := RawSample{
			Commits:   commits,
			Rollbacks: rollbacks,
			Queries:   queries,
			RedoBytes: redoBytes,
			Timestamp: snap.Timestamp,
		}
		delta := ComputeDeltas(&c.state, raw)
		snap.HasDelta = delta.HasDelta
		snap.TPS = delta.TPS
		snap.QPS = delta.QPS
		snap.RedoKBs = delta.RedoKBs

		// db% = active / maxConn * 100
		if maxConn > 0 {
			snap.DBPercent = float64(active) / maxConn * 100
		}

		// WTR% = (active - activeCPU) / active * 100
		if active > 0 {
			snap.WTRPercent = float64(active-activeCPU) / float64(active) * 100
		}

		mu.Unlock()
	})

	// Group 2: Wait events (with delta computation)
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		events := c.queryEvents(ctx)
		events = c.computeEventDeltas(events)

		mu.Lock()
		snap.Events = events
		mu.Unlock()
	})

	// Group 3: Active sessions
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		sessions := c.queryActiveSessions(ctx)

		mu.Lock()
		snap.Sessions = sessions
		mu.Unlock()
	})

	wg.Wait()

	// Fill role from cache if it was already set.
	if snap.DBRole == "" && c.cachedRole != "" {
		snap.DBRole = c.cachedRole
	}

	EvaluateHealth(&snap, c.state.PrevTPS)

	return snap
}

// queryRole returns the database role (PRIMARY or REPLICA).
func (c *Collector) queryRole(ctx context.Context) string {
	res, err := c.driver.Query(ctx, roleSQL)
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

// queryBPUsed returns the total buffer pool data bytes (data + dirty).
func (c *Collector) queryBPUsed(ctx context.Context) float64 {
	res, err := c.driver.Query(ctx, bufferPoolUsedSQL)
	if err != nil {
		debugLog("[BP_USED] query error: %v", err)
		return 0
	}
	var total float64
	for _, row := range res.Rows {
		if len(row) > 0 {
			total += toFloat(row[0])
		}
	}
	return total
}

// queryThroughput returns key global status counters.
func (c *Collector) queryThroughput(ctx context.Context) (commits, rollbacks, queries, redoBytes int64) {
	res, err := c.driver.Query(ctx, throughputSQL)
	if err != nil {
		debugLog("[THROUGHPUT] query error: %v", err)
		return 0, 0, 0, 0
	}
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		name, _ := row[0].(string)
		val := toInt(row[1])
		switch name {
		case "Handler_commit":
			commits = val
		case "Handler_rollback":
			rollbacks = val
		case "Queries":
			queries = val
		case "Innodb_os_log_written":
			redoBytes = val
		}
	}
	return commits, rollbacks, queries, redoBytes
}

// querySessionCounts returns session counts from performance_schema.threads.
func (c *Collector) querySessionCounts(ctx context.Context) (total, active, activeCPU, activeIO, idle int64) {
	res, err := c.driver.Query(ctx, sessionCountSQL)
	if err != nil || len(res.Rows) == 0 || len(res.Rows[0]) < 5 {
		return 0, 0, 0, 0, 0
	}
	row := res.Rows[0]
	return toInt(row[0]), toInt(row[1]), toInt(row[2]), toInt(row[3]), toInt(row[4])
}

// queryEvents returns the top wait events from performance_schema.
func (c *Collector) queryEvents(ctx context.Context) []WaitEvent {
	res, err := c.driver.Query(ctx, waitEventsSQL)
	if err != nil {
		debugLog("[EVENTS] query error: %v", err)
		return nil
	}
	if len(res.Rows) == 0 {
		return nil
	}

	// Calculate total time for percentage computation.
	// performance_schema timer is in picoseconds.
	var totalTime int64
	for _, row := range res.Rows {
		if len(row) >= 3 {
			totalTime += toInt(row[2])
		}
	}

	events := make([]WaitEvent, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 3 {
			continue
		}
		eventName, _ := row[0].(string)
		count := toInt(row[1])
		timePico := toInt(row[2])

		var pct float64
		if totalTime > 0 {
			pct = float64(timePico) / float64(totalTime) * 100
		}

		events = append(events, WaitEvent{
			Event:       eventName,
			Count:       count,
			TimeSec:     float64(timePico) / 1e12, // picoseconds to seconds
			PCT:         pct,
			RawTimePico: timePico,
		})
	}
	return events
}

// computeEventDeltas calculates differential values for each event,
// stores current cumulative values for next frame, and sorts by DTimeMs DESC.
//
// Uses RawTimePico (int64) for lossless delta computation.  The previous
// implementation round-tripped through float64 (pico -> seconds -> pico),
// which loses precision for large cumulative counters and produces zero deltas.
func (c *Collector) computeEventDeltas(events []WaitEvent) []WaitEvent {
	if c.state.PrevEventCount == nil {
		// First frame: initialize maps, no delta yet.
		c.state.PrevEventCount = make(map[string]int64, len(events))
		c.state.PrevEventTime = make(map[string]int64, len(events))
		for _, ev := range events {
			c.state.PrevEventCount[ev.Event] = ev.Count
			c.state.PrevEventTime[ev.Event] = ev.RawTimePico
		}
		return events
	}

	// Compute delta for each event using raw int64 picoseconds.
	var totalDeltaTime int64
	for i := range events {
		prevCount := c.state.PrevEventCount[events[i].Event]
		prevTime := c.state.PrevEventTime[events[i].Event]

		dCount := events[i].Count - prevCount
		dTimePico := events[i].RawTimePico - prevTime
		if dCount < 0 {
			dCount = 0
		}
		if dTimePico < 0 {
			dTimePico = 0
		}

		events[i].DCount = dCount
		events[i].DTimeMs = float64(dTimePico) / 1e9 // picoseconds -> milliseconds
		totalDeltaTime += dTimePico
	}

	// Compute DPCT based on total delta time.
	for i := range events {
		if totalDeltaTime > 0 {
			events[i].DPCT = float64(events[i].RawTimePico-c.state.PrevEventTime[events[i].Event]) /
				float64(totalDeltaTime) * 100
			if events[i].DPCT < 0 {
				events[i].DPCT = 0
			}
		}
	}

	// Update previous values for next frame using raw int64 values.
	newPrevCount := make(map[string]int64, len(events))
	newPrevTime := make(map[string]int64, len(events))
	for _, ev := range events {
		newPrevCount[ev.Event] = ev.Count
		newPrevTime[ev.Event] = ev.RawTimePico
	}
	c.state.PrevEventCount = newPrevCount
	c.state.PrevEventTime = newPrevTime

	// Sort by DTimeMs descending.
	sort.Slice(events, func(i, j int) bool {
		return events[i].DTimeMs > events[j].DTimeMs
	})

	return events
}

// queryActiveSessions returns active session rows sorted by elapsed time.
func (c *Collector) queryActiveSessions(ctx context.Context) []SessionRow {
	sqlStr := activeSessionSQL
	if c.hasRichSession != nil && !*c.hasRichSession {
		sqlStr = activeSessionFallbackSQL
	}
	res, err := c.driver.Query(ctx, sqlStr)
	if err != nil {
		if c.hasRichSession == nil {
			f := false
			c.hasRichSession = &f
			res, err = c.driver.Query(ctx, activeSessionFallbackSQL)
			if err != nil || len(res.Rows) == 0 {
				return nil
			}
		} else {
			return nil
		}
	}
	if len(res.Rows) == 0 {
		return nil
	}
	if c.hasRichSession == nil {
		t := true
		c.hasRichSession = &t
	}

	sessions := make([]SessionRow, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 9 {
			continue
		}
		sessions = append(sessions, SessionRow{
			SID:        int(toInt(row[0])),
			Username:   strVal(row[1]),
			SQLID:      strVal(row[2]),
			Event:      strVal(row[3]),
			WaitClass:  strVal(row[4]),
			ElapsedSec: float64(toInt(row[5])),
			SQLText:    strVal(row[6]),
			Program:    strVal(row[7]),
			Status:     strVal(row[8]),
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
	case uint64:
		return int64(val)
	case uint32:
		return int64(val)
	case string:
		// Use strconv.ParseInt for large integers to preserve precision
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			return n
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0
		}
		return int64(f)
	case []byte:
		s := string(val)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return int64(f)
	default:
		// Fallback: fmt.Sprint then parse
		s := fmt.Sprint(val)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return 0
	}
}
