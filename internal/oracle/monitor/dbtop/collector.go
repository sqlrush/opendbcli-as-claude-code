/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  Collector queries Oracle via db.Driver and assembles Snapshot
 *	  values.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/collector.go
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

// debugLog writes debug info to /tmp/opendb-dbtop.log
func debugLog(format string, args ...any) {
	f, err := os.OpenFile("/tmp/opendb-dbtop.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", args...)
}

// SQL constants for Oracle data dictionary queries.
const (
	dbRoleSQL = `SELECT database_role FROM v$database`

	sgaUsedSQL = `SELECT ROUND(SUM(bytes)/1024/1024, 0) FROM v$sgastat`
	sgaMaxSQL  = `SELECT ROUND(SUM(value)/1024/1024, 0) FROM v$sga`

	pgaUsedSQL = `SELECT ROUND(value/1024/1024, 0) FROM v$pgastat WHERE name = 'total PGA allocated'`
	pgaMaxSQL  = `SELECT ROUND(value/1024/1024, 0) FROM v$parameter WHERE name = 'pga_aggregate_limit'`

	timeModelSQL = `SELECT stat_name, value FROM v$sys_time_model WHERE stat_name IN ('DB CPU', 'DB time')`

	sysstatSQL2 = `SELECT name, value FROM v$sysstat WHERE name IN ('user commits', 'user rollbacks', 'execute count', 'redo size')`

	sessionCountSQL = `SELECT
  COUNT(*) AS SN,
  SUM(CASE WHEN status = 'ACTIVE' AND type = 'USER' THEN 1 ELSE 0 END) AS AN,
  SUM(CASE WHEN status = 'ACTIVE' AND wait_class NOT IN ('Idle','User I/O','System I/O') THEN 1 ELSE 0 END) AS ACPU,
  SUM(CASE WHEN status = 'ACTIVE' AND wait_class IN ('User I/O','System I/O') THEN 1 ELSE 0 END) AS AIO,
  SUM(CASE WHEN status <> 'ACTIVE' OR type <> 'USER' THEN 1 ELSE 0 END) AS IDL
FROM v$session`

	eventSQL = `SELECT event, total_waits, time_waited_micro, wait_class
FROM (
  SELECT * FROM (
    SELECT 'DB CPU' AS event, 0 AS total_waits,
      (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB CPU') AS time_waited_micro,
      'CPU' AS wait_class
    FROM dual
    UNION ALL
    SELECT event, total_waits, time_waited_micro, wait_class
    FROM v$system_event
    WHERE wait_class <> 'Idle'
  )
  ORDER BY time_waited_micro DESC
)
WHERE ROWNUM <= 10`

	activeSessionSQL = `SELECT
  s.SID, s.USERNAME, s.SQL_ID,
  NVL(s.EVENT, 'ON CPU') AS event,
  NVL(s.WAIT_CLASS, 'CPU') AS wait_class,
  NVL(ROUND((SYSDATE - s.SQL_EXEC_START) * 86400, 1), 0) AS elapsed_sec,
  NVL(SUBSTR(q.SQL_TEXT, 1, 80), ' ') AS sql_text,
  NVL(s.PROGRAM, ' ') AS program,
  s.STATUS
FROM v$session s
LEFT JOIN v$sql q ON s.SQL_ID = q.SQL_ID AND s.SQL_CHILD_NUMBER = q.CHILD_NUMBER
WHERE s.STATUS = 'ACTIVE' AND s.TYPE = 'USER' AND s.WAIT_CLASS <> 'Idle'
ORDER BY elapsed_sec DESC`

	// burstSessionSQL extends activeSessionSQL with blocking/plan/machine info.
	burstSessionSQL = `SELECT
  s.SID, s.USERNAME, s.SQL_ID,
  NVL(s.EVENT, 'ON CPU') AS event,
  NVL(s.WAIT_CLASS, 'CPU') AS wait_class,
  NVL(ROUND((SYSDATE - s.SQL_EXEC_START) * 86400, 1), 0) AS elapsed_sec,
  NVL(SUBSTR(q.SQL_TEXT, 1, 200), ' ') AS sql_text,
  NVL(s.PROGRAM, ' ') AS program,
  s.STATUS,
  NVL(s.BLOCKING_SESSION, 0) AS blocking_sid,
  NVL(q.PLAN_HASH_VALUE, 0) AS plan_hash_value,
  NVL(s.MACHINE, ' ') AS machine,
  NVL(s.SECONDS_IN_WAIT, 0) AS wait_time_sec,
  NVL(s.SQL_CHILD_NUMBER, 0) AS sql_child_number
FROM v$session s
LEFT JOIN v$sql q ON s.SQL_ID = q.SQL_ID AND s.SQL_CHILD_NUMBER = q.CHILD_NUMBER
WHERE s.STATUS = 'ACTIVE' AND s.TYPE = 'USER' AND s.WAIT_CLASS <> 'Idle'
ORDER BY elapsed_sec DESC`
)

// Collector queries Oracle via db.Driver and assembles Snapshot values.
type Collector struct {
	driver         db.Driver
	state          DeltaState
	cachedRole     string
	cachedVersion  string
	cachedInstance string
	mode           CollectMode
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

// Collect runs all Oracle queries in parallel and returns a Snapshot.
func (c *Collector) Collect(ctx context.Context) Snapshot {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		snap Snapshot
	)

	snap.Version = c.cachedVersion
	snap.InstanceName = c.cachedInstance
	snap.Timestamp = time.Now()

	// Group 1: DB role + SGA + PGA + time model + sysstat
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		role := c.queryDBRole(ctx)
		sgaUsed := c.queryScalar(ctx, sgaUsedSQL, "SGA_USED")
		sgaMax := c.queryScalar(ctx, sgaMaxSQL, "SGA_MAX")
		pgaUsed := c.queryScalar(ctx, pgaUsedSQL, "PGA_USED")
		pgaMax := c.queryScalar(ctx, pgaMaxSQL, "PGA_MAX")
		cpuTime, dbTime := c.queryTimeModel(ctx)
		commits, rollbacks, executes, redoSize := c.querySysstat(ctx)

		mu.Lock()
		c.cachedRole = role
		snap.DBRole = role
		snap.SGAUsedMB = sgaUsed
		snap.SGAMaxMB = sgaMax
		snap.PGAUsedMB = pgaUsed
		snap.PGAMaxMB = pgaMax

		raw := RawSample{
			CPUTime:   cpuTime,
			DBTime:    dbTime,
			Commits:   commits,
			Rollbacks: rollbacks,
			Executes:  executes,
			RedoSize:  redoSize,
			Timestamp: snap.Timestamp,
		}
		delta := ComputeDeltas(&c.state, raw)
		snap.HasDelta = delta.HasDelta
		snap.TPS = delta.TPS
		snap.QPS = delta.QPS
		snap.RedoKBs = delta.RedoKBs
		snap.DBPercent = delta.DBPercent
		snap.WTRPercent = delta.WTRPercent
		mu.Unlock()
	})

	// Group 2: Session counts
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		total, active, activeCPU, activeIO, idle := c.querySessionCounts(ctx)

		mu.Lock()
		snap.TotalSessions = int(total)
		snap.ActiveCount = int(active)
		snap.ActiveCPU = int(activeCPU)
		snap.ActiveIO = int(activeIO)
		snap.IdleCount = int(idle)
		mu.Unlock()
	})

	// Group 3: Wait events (with delta computation)
	wg.Add(1)
	odberr.SafeGo(odberr.ErrSentinelCollect, func() {
		defer wg.Done()
		events := c.queryEvents(ctx)
		events = c.computeEventDeltas(events)

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

// queryTimeModel returns DB CPU and DB time in microseconds.
func (c *Collector) queryTimeModel(ctx context.Context) (cpuTime, dbTime int64) {
	res, err := c.driver.Query(ctx, timeModelSQL)
	if err != nil {
		return 0, 0
	}
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		name, _ := row[0].(string)
		val := toInt(row[1])
		switch name {
		case "DB CPU":
			cpuTime = val
		case "DB time":
			dbTime = val
		}
	}
	return cpuTime, dbTime
}

// querySysstat returns commits, rollbacks, executes, and redo size.
func (c *Collector) querySysstat(ctx context.Context) (commits, rollbacks, executes, redoSize int64) {
	res, err := c.driver.Query(ctx, sysstatSQL2)
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		name, _ := row[0].(string)
		val := toInt(row[1])
		switch name {
		case "user commits":
			commits = val
		case "user rollbacks":
			rollbacks = val
		case "execute count":
			executes = val
		case "redo size":
			redoSize = val
		}
	}
	return commits, rollbacks, executes, redoSize
}

// querySessionCounts returns total, active, activeCPU, activeIO, idle counts.
func (c *Collector) querySessionCounts(ctx context.Context) (total, active, activeCPU, activeIO, idle int64) {
	res, err := c.driver.Query(ctx, sessionCountSQL)
	if err != nil {
		debugLog("[SESSION] query error: %v", err)
		return 0, 0, 0, 0, 0
	}
	if len(res.Rows) == 0 {
		debugLog("[SESSION] no rows returned")
		return 0, 0, 0, 0, 0
	}
	row := res.Rows[0]
	debugLog("[SESSION] cols=%d row types: %T %T %T %T %T values: %v %v %v %v %v",
		len(row), row[0], row[1], row[2], row[3], row[4], row[0], row[1], row[2], row[3], row[4])
	if len(row) < 5 {
		return 0, 0, 0, 0, 0
	}
	return toInt(row[0]), toInt(row[1]), toInt(row[2]), toInt(row[3]), toInt(row[4])
}

// queryEvents returns the top wait events including DB CPU.
func (c *Collector) queryEvents(ctx context.Context) []WaitEvent {
	res, err := c.driver.Query(ctx, eventSQL)
	if err != nil {
		debugLog("[EVENTS] query error: %v", err)
		return nil
	}
	if len(res.Rows) == 0 {
		debugLog("[EVENTS] no rows returned")
		return nil
	}

	// Calculate total time for percentage computation.
	var totalTime int64
	for _, row := range res.Rows {
		if len(row) >= 3 {
			totalTime += toInt(row[2])
		}
	}

	events := make([]WaitEvent, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 4 {
			continue
		}
		eventName, _ := row[0].(string)
		waits := toInt(row[1])
		timeMicro := toInt(row[2])
		waitClass, _ := row[3].(string)

		var pct float64
		if totalTime > 0 {
			pct = float64(timeMicro) / float64(totalTime) * 100
		}

		events = append(events, WaitEvent{
			Event:        eventName,
			Waits:        waits,
			TimeSec:      float64(timeMicro) / 1e6,
			PCT:          pct,
			WaitClass:    waitClass,
			RawTimeMicro: timeMicro,
		})
	}
	return events
}

// computeEventDeltas calculates differential values for each event,
// stores current cumulative values for next frame, and sorts by DTimeMs DESC.
func (c *Collector) computeEventDeltas(events []WaitEvent) []WaitEvent {
	if c.state.PrevEventWaits == nil {
		// First frame: initialize maps, no delta yet.
		c.state.PrevEventWaits = make(map[string]int64, len(events))
		c.state.PrevEventTime = make(map[string]int64, len(events))
		for _, ev := range events {
			c.state.PrevEventWaits[ev.Event] = ev.Waits
			c.state.PrevEventTime[ev.Event] = ev.RawTimeMicro
		}
		return events
	}

	// Compute delta for each event.
	var totalDeltaTime int64
	for i := range events {
		prevWaits := c.state.PrevEventWaits[events[i].Event]
		prevTime := c.state.PrevEventTime[events[i].Event]

		dWaits := events[i].Waits - prevWaits
		dTimeMicro := events[i].RawTimeMicro - prevTime
		if dWaits < 0 {
			dWaits = 0
		}
		if dTimeMicro < 0 {
			dTimeMicro = 0
		}

		events[i].DWaits = dWaits
		events[i].DTimeMs = float64(dTimeMicro) / 1000.0 // microseconds -> milliseconds
		totalDeltaTime += dTimeMicro
	}

	// Compute DPCT based on total delta time.
	for i := range events {
		if totalDeltaTime > 0 {
			dTimeMicro := int64(events[i].DTimeMs * 1000.0)
			events[i].DPCT = float64(dTimeMicro) / float64(totalDeltaTime) * 100
		}
	}

	// Update previous values for next frame.
	c.state.PrevEventWaits = make(map[string]int64, len(events))
	c.state.PrevEventTime = make(map[string]int64, len(events))
	for _, ev := range events {
		c.state.PrevEventWaits[ev.Event] = ev.Waits
		c.state.PrevEventTime[ev.Event] = ev.RawTimeMicro
	}

	// Sort by DTimeMs descending.
	sort.Slice(events, func(i, j int) bool {
		return events[i].DTimeMs > events[j].DTimeMs
	})

	return events
}

// queryActiveSessions returns active session rows sorted by elapsed time.
// In burst mode, uses an extended SQL and populates BurstDetail.
func (c *Collector) queryActiveSessions(ctx context.Context) []SessionRow {
	sqlStr := activeSessionSQL
	if c.mode == ModeBurst {
		sqlStr = burstSessionSQL
	}

	res, err := c.driver.Query(ctx, sqlStr)
	if err != nil || len(res.Rows) == 0 {
		return nil
	}

	sessions := make([]SessionRow, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 9 {
			continue
		}
		sr := parseSessionRow(row)
		if c.mode == ModeBurst && len(row) >= 14 {
			sr.Burst = parseBurstDetail(row)
		}
		sessions = append(sessions, sr)
	}
	return sessions
}

// parseSessionRow extracts the base 9 columns common to both modes.
func parseSessionRow(row []any) SessionRow {
	sid := int(toInt(row[0]))
	username, _ := row[1].(string)
	sqlID, _ := row[2].(string)
	event, _ := row[3].(string)
	waitClass, _ := row[4].(string)
	elapsedSec := toFloat(row[5])
	sqlText, _ := row[6].(string)
	program, _ := row[7].(string)
	status, _ := row[8].(string)

	return SessionRow{
		SID:        sid,
		Username:   username,
		SQLID:      sqlID,
		Event:      event,
		WaitClass:  waitClass,
		ElapsedSec: elapsedSec,
		SQLText:    sqlText,
		Program:    program,
		Status:     status,
	}
}

// parseBurstDetail extracts columns 9-13 (burst-mode extended fields).
func parseBurstDetail(row []any) *BurstDetail {
	return &BurstDetail{
		BlockingSID:   int(toInt(row[9])),
		PlanHashValue: toInt(row[10]),
		Machine:       strVal(row[11]),
		WaitTimeSec:   toFloat(row[12]),
		SQLChildNum:   int(toInt(row[13])),
	}
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
		// go-ora returns Oracle NUMBER as string
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0
		}
		return int64(f)
	default:
		return 0
	}
}
