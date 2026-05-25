/*-------------------------------------------------------------------------
 *
 * probe.go
 *	  FastProbeCollector runs lightweight probes against PostgreSQL
 *	  every second.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/probe.go
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

// fastProbeSQL queries pg_stat_activity for session-based metrics (6 metrics, 1 SQL).
const fastProbeSQL = `SELECT
  COUNT(*) FILTER (WHERE state = 'active') AS active,
  COUNT(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'Lock') AS lock_waits,
  COUNT(*) FILTER (WHERE state = 'active' AND EXTRACT(EPOCH FROM clock_timestamp()-query_start) > 30) AS long_queries,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type IS NULL) AS on_cpu,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'IO') AS io_wait
FROM pg_stat_activity WHERE backend_type = 'client backend'`

// FastProbeCollector runs lightweight probes against PostgreSQL every second.
type FastProbeCollector struct {
	driver db.Driver
}

// NewFastProbeCollector creates a new fast-tier probe collector.
func NewFastProbeCollector(driver db.Driver) *FastProbeCollector {
	return &FastProbeCollector{driver: driver}
}

// Driver returns the underlying database driver.
func (p *FastProbeCollector) Driver() db.Driver { return p.driver }

// Probe runs a single lightweight SQL to collect session-based metrics.
func (p *FastProbeCollector) Probe(ctx context.Context) (map[MetricName]float64, error) {
	metrics := make(map[MetricName]float64)

	rows, err := p.driver.Query(ctx, fastProbeSQL)
	if err != nil {
		return metrics, err
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 6 {
		row := rows.Rows[0]
		metrics[MetricActiveSessions] = toFloat64(row[0])
		metrics[MetricIdleInTransaction] = toFloat64(row[1])
		metrics[MetricLockWaitSessions] = toFloat64(row[2])
		metrics[MetricLongQueries] = toFloat64(row[3])
		metrics[MetricCPUSessions] = toFloat64(row[4])
		metrics[MetricIOWaitSessions] = toFloat64(row[5])
	}

	return metrics, nil
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
	case string:
		var result float64
		var divisor float64
		decimal := false
		for _, c := range val {
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
		return result
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

// nowFunc is a variable for time.Now, overridable in tests.
var nowFunc = time.Now
