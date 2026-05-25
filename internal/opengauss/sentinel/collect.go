/*-------------------------------------------------------------------------
 *
 * collect.go
 *	  CollectTopSQLs queries pg_stat_activity for the top active SQL
 *	  statements. Returns nil slice on error (graceful degradation for
 *	  OpenGauss versions that may not support all columns).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/collect.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// ─── SQL for OpenGauss burst collection ──────────────────────────────────────
//
// OpenGauss differences from PostgreSQL:
// - No query_id in pg_stat_activity — use left(md5(query), 16) as fallback
// - Uses wait_status instead of wait_event_type/wait_event
// - No pg_blocking_pids() — use pg_locks self-join instead
// - No backend_type column — filter by pid != pg_backend_pid()
// - Use LIMIT N instead of FETCH FIRST N ROWS ONLY

const collectTopSQLSQL = `SELECT
    COALESCE(left(md5(query), 16), 'unknown') AS query_id,
    left(query, 100) AS query,
    COUNT(*) AS active_count,
    COALESCE(wait_status, 'Running') AS wait_event_type,
    COALESCE(wait_status, 'Running') AS wait_event
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()
GROUP BY left(md5(query), 16), left(query, 100), wait_status
ORDER BY COUNT(*) DESC
LIMIT 10`

const collectWaitProfileSQL = `SELECT
    COALESCE(wait_status, 'CPU') AS wait_event_type,
    COALESCE(wait_status, 'Running') AS wait_event,
    COUNT(*) AS cnt,
    ROUND(COUNT(*) * 100.0 / NULLIF((SELECT COUNT(*) FROM pg_stat_activity WHERE state = 'active' AND pid != pg_backend_pid()), 0), 1) AS pct
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()
GROUP BY wait_status
ORDER BY cnt DESC
LIMIT 10`

const collectBlockingChainsSQL = `SELECT
    kl.pid AS blocker_pid,
    ka.usename AS blocker_user,
    left(ka.query, 100) AS blocker_query,
    COUNT(DISTINCT bl.pid) AS victim_count,
    MAX(CASE WHEN ba.waiting THEN 'Lock' ELSE 'none' END) AS wait_event
FROM pg_locks bl
JOIN pg_locks kl ON kl.transactionid = bl.transactionid AND kl.pid != bl.pid AND kl.granted
JOIN pg_stat_activity ka ON ka.pid = kl.pid
JOIN pg_stat_activity ba ON ba.pid = bl.pid
WHERE NOT bl.granted
GROUP BY kl.pid, ka.usename, left(ka.query, 100)
ORDER BY victim_count DESC
LIMIT 10`

// CollectTopSQLs queries pg_stat_activity for the top active SQL statements.
// Returns nil slice on error (graceful degradation for OpenGauss versions
// that may not support all columns).
func CollectTopSQLs(ctx context.Context, driver db.Driver) []SQLProfile {
	result, err := driver.Query(ctx, collectTopSQLSQL)
	if err != nil {
		return nil
	}

	var profiles []SQLProfile
	for _, row := range result.Rows {
		if len(row) < 5 {
			continue
		}
		profiles = append(profiles, SQLProfile{
			QueryID:       toString(row[0]),
			Query:         toString(row[1]),
			ActiveCount:   int(toInt64(row[2])),
			WaitEventType: toString(row[3]),
			WaitEvent:     toString(row[4]),
		})
	}
	return profiles
}

// CollectWaitProfile queries pg_stat_activity for wait event distribution.
// Returns nil slice on error (graceful degradation).
func CollectWaitProfile(ctx context.Context, driver db.Driver) []WaitBucket {
	result, err := driver.Query(ctx, collectWaitProfileSQL)
	if err != nil {
		return nil
	}

	var buckets []WaitBucket
	for _, row := range result.Rows {
		if len(row) < 4 {
			continue
		}
		buckets = append(buckets, WaitBucket{
			WaitEventType: toString(row[0]),
			WaitEvent:     toString(row[1]),
			Count:         int(toInt64(row[2])),
			Percentage:    toFloat64(row[3]),
		})
	}
	return buckets
}

// CollectBlockingChains queries pg_locks for blocking relationships.
// Returns nil slice on error (graceful degradation).
func CollectBlockingChains(ctx context.Context, driver db.Driver) []BlockingChain {
	result, err := driver.Query(ctx, collectBlockingChainsSQL)
	if err != nil {
		return nil
	}

	var chains []BlockingChain
	for _, row := range result.Rows {
		if len(row) < 5 {
			continue
		}
		chains = append(chains, BlockingChain{
			BlockerPID:   int(toInt64(row[0])),
			BlockerUser:  toString(row[1]),
			BlockerQuery: toString(row[2]),
			VictimCount:  int(toInt64(row[3])),
			WaitEvent:    toString(row[4]),
		})
	}
	return chains
}
