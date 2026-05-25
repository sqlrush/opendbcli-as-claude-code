/*-------------------------------------------------------------------------
 *
 * collect.go
 *	  CollectTopSQL queries performance_schema for the top SQL during
 *	  burst.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/collect.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// CollectTopSQL queries performance_schema for the top SQL during burst.
func CollectTopSQL(ctx context.Context, driver db.Driver) []SQLProfile {
	const topSQLQuery = `SELECT
  DIGEST AS digest,
  LEFT(DIGEST_TEXT, 200) AS sql_text,
  COUNT_STAR AS exec_count,
  AVG_TIMER_WAIT/1000000000 AS avg_latency_ms,
  MAX_TIMER_WAIT/1000000000 AS max_latency_ms,
  SUM_LOCK_TIME/1000000000 AS lock_time_ms
FROM performance_schema.events_statements_summary_by_digest
WHERE DIGEST IS NOT NULL AND LAST_SEEN > DATE_SUB(NOW(), INTERVAL 1 MINUTE)
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 5`

	result, err := driver.Query(ctx, topSQLQuery)
	if err != nil {
		return nil
	}

	profiles := make([]SQLProfile, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		profiles = append(profiles, SQLProfile{
			Digest:       toString(row[0]),
			SQLText:      toString(row[1]),
			ExecCount:    int64(toFloat64(row[2])),
			AvgLatencyMs: toFloat64(row[3]),
			MaxLatencyMs: toFloat64(row[4]),
			LockTimeMs:   toFloat64(row[5]),
		})
	}
	return profiles
}

// CollectBlockingChains queries performance_schema for current lock blocking.
func CollectBlockingChains(ctx context.Context, driver db.Driver) []BlockingChain {
	const blockQuery = `SELECT
  b.BLOCKING_THREAD_ID,
  (SELECT USER FROM performance_schema.threads WHERE THREAD_ID = b.BLOCKING_THREAD_ID) AS blocker_user,
  (SELECT LEFT(INFO, 200) FROM information_schema.PROCESSLIST WHERE ID = b.BLOCKING_THREAD_ID) AS blocker_query,
  'row_lock' AS wait_type,
  COUNT(*) AS victims
FROM performance_schema.data_lock_waits b
GROUP BY b.BLOCKING_THREAD_ID
ORDER BY COUNT(*) DESC
LIMIT 10`

	result, err := driver.Query(ctx, blockQuery)
	if err != nil {
		return nil
	}

	chains := make([]BlockingChain, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 5 {
			continue
		}
		chains = append(chains, BlockingChain{
			BlockerThreadID: int64(toFloat64(row[0])),
			BlockerUser:     toString(row[1]),
			BlockerQuery:    toString(row[2]),
			WaitType:        toString(row[3]),
			VictimCount:     int(toFloat64(row[4])),
		})
	}
	return chains
}
