/*-------------------------------------------------------------------------
 *
 * wait_profile.go
 *	  CollectWaitProfile queries performance_schema for the top wait
 *	  events. Returns nil on error (graceful degradation).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/wait_profile.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// waitProfileQuery queries performance_schema for the top 10 non-idle wait events
// grouped by event name, ordered by occurrence count descending.
const waitProfileQuery = `SELECT
    EVENT_NAME,
    LEFT(EVENT_NAME, LOCATE('/', EVENT_NAME, 6) - 1) AS wait_class,
    COUNT_STAR AS count,
    ROUND(SUM_TIMER_WAIT / 1e9, 2) AS total_ms,
    ROUND(COUNT_STAR * 100.0 / NULLIF(
        (SELECT SUM(COUNT_STAR)
         FROM performance_schema.events_waits_summary_global_by_event_name
         WHERE COUNT_STAR > 0 AND EVENT_NAME NOT LIKE 'idle%'), 0), 1) AS pct
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE COUNT_STAR > 0 AND EVENT_NAME NOT LIKE 'idle%'
ORDER BY COUNT_STAR DESC
LIMIT 10`

// CollectWaitProfile queries performance_schema for the top wait events.
// Returns nil on error (graceful degradation).
func CollectWaitProfile(ctx context.Context, driver db.Driver) []WaitBucket {
	if driver == nil {
		return nil
	}

	result, err := driver.Query(ctx, waitProfileQuery)
	if err != nil {
		return nil
	}

	buckets := make([]WaitBucket, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 5 {
			continue
		}
		buckets = append(buckets, WaitBucket{
			EventName:  toString(row[0]),
			WaitClass:  toString(row[1]),
			Count:      int(toFloat64(row[2])),
			TotalMs:    toFloat64(row[3]),
			Percentage: toFloat64(row[4]),
		})
	}
	return buckets
}
