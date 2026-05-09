/*-------------------------------------------------------------------------
 *
 * probe.go
 *	  FastProbeCollector runs lightweight probes against MySQL every
 *	  second.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// fastProbeSQL is the lightweight query executed every 1 second.
const fastProbeSQL = `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') AS total,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_running') AS active,
  (SELECT COUNT(*) FROM performance_schema.data_lock_waits) AS lock_waits,
  (SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND != 'Sleep' AND TIME > 30) AS long_queries,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_cached') AS cached`

// FastProbeCollector runs lightweight probes against MySQL every second.
type FastProbeCollector struct {
	driver db.Driver

	// Delta tracking for aborted connects.
	prevAbortedConnects int64
	prevTime            time.Time
	initialized         bool
}

// NewFastProbeCollector creates a new fast-tier probe collector.
func NewFastProbeCollector(driver db.Driver) *FastProbeCollector {
	return &FastProbeCollector{driver: driver}
}

// Driver returns the underlying database driver.
func (p *FastProbeCollector) Driver() db.Driver { return p.driver }

// Probe runs the fast probe and returns metric values.
func (p *FastProbeCollector) Probe(ctx context.Context) (map[MetricName]float64, error) {
	metrics := make(map[MetricName]float64)

	rows, err := p.driver.Query(ctx, fastProbeSQL)
	if err != nil {
		return metrics, err
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 5 {
		row := rows.Rows[0]
		metrics[MetricThreadsConnected] = toFloat64(row[0])
		metrics[MetricThreadsRunning] = toFloat64(row[1])
		metrics[MetricLockWaits] = toFloat64(row[2])
		metrics[MetricLongQueries] = toFloat64(row[3])
		metrics[MetricThreadsCached] = toFloat64(row[4])
	}

	// Aborted connects rate (delta).
	p.collectAbortedConnects(ctx, metrics)

	return metrics, nil
}

const abortedConnectsSQL = `SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Aborted_connects'`

func (p *FastProbeCollector) collectAbortedConnects(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := p.driver.Query(ctx, abortedConnectsSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}
	now := nowFunc()
	cur := int64(toFloat64(rows.Rows[0][0]))
	if p.initialized {
		elapsed := now.Sub(p.prevTime).Seconds()
		if elapsed > 0 {
			delta := cur - p.prevAbortedConnects
			if delta > 0 {
				metrics[MetricAbortedConnectsRate] = float64(delta) / elapsed
			}
		}
	}
	p.prevAbortedConnects = cur
	p.prevTime = now
	p.initialized = true
}

// toFloat64 converts any value to float64 (best-effort).
func toFloat64(v any) float64 {
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
	case []byte:
		return parseBytesFloat(val)
	case string:
		return parseStringFloat(val)
	default:
		return 0
	}
}

func parseBytesFloat(b []byte) float64 { return parseStringFloat(string(b)) }

func parseStringFloat(s string) float64 {
	var result float64
	var divisor float64
	decimal := false
	negative := false
	for i, c := range s {
		if c == '-' && i == 0 {
			negative = true
			continue
		}
		if c == '.' {
			decimal = true
			divisor = 1
			continue
		}
		if c >= '0' && c <= '9' {
			if decimal {
				divisor *= 10
				result += float64(c-'0') / divisor
			} else {
				result = result*10 + float64(c-'0')
			}
		}
	}
	if negative {
		return -result
	}
	return result
}

// toString converts any value to string.
func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return ""
	}
}

// nowFunc is a variable for time.Now, overridable in tests.
var nowFunc = time.Now
