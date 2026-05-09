/*-------------------------------------------------------------------------
 *
 * collect.go
 *	  CollectTopSQL queries pg_stat_activity for active queries grouped
 *	  by query_id. Returns nil on error or when no active queries are
 *	  found.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/collect.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// ── SQL queries ─────────────────────────────────────────────────────────────

const topSQLQuery = `SELECT
    COALESCE(query_id::text, left(md5(query), 16)) AS query_id,
    left(query, 100) AS query,
    COUNT(*) AS active_count,
    COALESCE(wait_event_type, 'CPU') AS wait_event_type,
    COALESCE(wait_event, 'Running') AS wait_event
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid() AND backend_type = 'client backend'
GROUP BY query_id, left(query, 100), wait_event_type, wait_event
ORDER BY COUNT(*) DESC
FETCH FIRST 10 ROWS ONLY`

const waitProfileQuery = `SELECT
    COALESCE(wait_event_type, 'CPU') AS wait_event_type,
    COALESCE(wait_event, 'Running') AS wait_event,
    COUNT(*) AS cnt,
    ROUND(COUNT(*) * 100.0 / NULLIF(SUM(COUNT(*)) OVER(), 0), 1) AS pct
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid() AND backend_type = 'client backend'
GROUP BY wait_event_type, wait_event
ORDER BY cnt DESC
FETCH FIRST 10 ROWS ONLY`

const blockingChainsQuery = `SELECT
    kl.pid AS blocker_pid,
    ka.usename AS blocker_user,
    left(ka.query, 100) AS blocker_query,
    COUNT(DISTINCT bl.pid) AS victim_count,
    MAX(ba.wait_event) AS wait_event
FROM pg_locks bl
JOIN pg_locks kl ON kl.transactionid = bl.transactionid AND kl.pid != bl.pid AND kl.granted
JOIN pg_stat_activity ka ON ka.pid = kl.pid
JOIN pg_stat_activity ba ON ba.pid = bl.pid
WHERE NOT bl.granted
GROUP BY kl.pid, ka.usename, left(ka.query, 100)
ORDER BY victim_count DESC
FETCH FIRST 10 ROWS ONLY`

// ── Collection functions ────────────────────────────────────────────────────

// CollectTopSQL queries pg_stat_activity for active queries grouped by query_id.
// Returns nil on error or when no active queries are found.
func CollectTopSQL(ctx context.Context, driver db.Driver) []SQLProfile {
	if driver == nil {
		return nil
	}

	result, err := driver.Query(ctx, topSQLQuery)
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
// Returns nil on error or when no active sessions are found.
func CollectWaitProfile(ctx context.Context, driver db.Driver) []WaitBucket {
	if driver == nil {
		return nil
	}

	result, err := driver.Query(ctx, waitProfileQuery)
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
// Returns nil on error or when no blocking chains are found.
func CollectBlockingChains(ctx context.Context, driver db.Driver) []BlockingChain {
	if driver == nil {
		return nil
	}

	result, err := driver.Query(ctx, blockingChainsQuery)
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

// Type conversion helpers (toString, toFloat64, toInt64) are defined in probe.go.
