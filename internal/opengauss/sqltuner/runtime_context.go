/*-------------------------------------------------------------------------
 *
 * runtime_context.go
 *	  RuntimeCollector snapshots wait events + locks at tuner start.
 *	  Uses graceful degradation when user lacks privileges to see other
 *	  sessions' query text.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/runtime_context.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
)

// RuntimeCollector snapshots wait events + locks at tuner start.
// Uses graceful degradation when user lacks privileges to see other sessions' query text.
type RuntimeCollector struct {
	driver db.Driver
}

func NewRuntimeCollector(d db.Driver) *RuntimeCollector { return &RuntimeCollector{driver: d} }

func (r *RuntimeCollector) Snapshot(ctx context.Context, involvedTables []string) (*RuntimeInfo, error) {
	info := &RuntimeInfo{}

	// Wait events: aggregate query, doesn't need query-text privilege
	waitsQ := `
SELECT COALESCE(wait_event_type, 'CPU') AS wet,
       COALESCE(wait_event, 'On CPU') AS we,
       count(*) AS cnt
FROM pg_stat_activity
WHERE state = 'active'
  AND pid != pg_backend_pid()
GROUP BY 1, 2
ORDER BY 3 DESC
LIMIT 20`
	if res, err := r.driver.Query(ctx, waitsQ); err == nil {
		for _, row := range res.Rows {
			if len(row) < 3 {
				continue
			}
			info.WaitEvents = append(info.WaitEvents, WaitEventBucket{
				WaitEventType: asString(row[0]),
				WaitEvent:     asString(row[1]),
				Count:         int(asInt64(row[2])),
			})
		}
	}

	// Detect privilege limitation: try to see other session's query field
	// If we get "<insufficient privilege>" markers, set Degraded=true
	probeQ := `
SELECT query FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()
LIMIT 5`
	if res, err := r.driver.Query(ctx, probeQ); err == nil {
		for _, row := range res.Rows {
			q := asString(row[0])
			if strings.Contains(strings.ToLower(q), "insufficient privilege") {
				info.Degraded = true
				break
			}
		}
	}

	// Locks against involved tables (best-effort; pg_locks usually visible to all)
	if len(involvedTables) > 0 {
		locksQ := `
SELECT
  COALESCE(c.relname, '?') AS relation,
  l.locktype,
  l.mode,
  l.granted
FROM pg_locks l
LEFT JOIN pg_class c ON c.oid = l.relation
WHERE c.relname IN (` + sqlInList(involvedTables) + `)
LIMIT 100`
		if res, err := r.driver.Query(ctx, locksQ); err == nil {
			for _, row := range res.Rows {
				if len(row) < 4 {
					continue
				}
				info.Locks = append(info.Locks, LockEntry{
					Relation: asString(row[0]),
					LockType: asString(row[1]),
					Mode:     asString(row[2]),
					Granted:  asBool(row[3]),
				})
			}
		}
	}

	return info, nil
}

// PromptBlock returns runtime context formatted for the LLM user message.
func (r *RuntimeInfo) PromptBlock() string {
	var b strings.Builder
	if r.Degraded {
		b.WriteString("Runtime 上下文（部分降级 — 用户权限不足，看不到其他会话 query 文本）:\n")
	} else {
		b.WriteString("Runtime 上下文:\n")
	}
	if len(r.WaitEvents) == 0 {
		b.WriteString("- 当前无活跃会话\n")
	} else {
		b.WriteString("- 当前等待事件分布:\n")
		for _, w := range r.WaitEvents {
			b.WriteString("    " + w.WaitEventType + " / " + w.WaitEvent + ": " + intToStr(w.Count) + "\n")
		}
	}
	if len(r.Locks) > 0 {
		b.WriteString("- 涉及表的锁:\n")
		for _, l := range r.Locks {
			grant := "granted"
			if !l.Granted {
				grant = "WAITING"
			}
			b.WriteString("    " + l.Relation + " " + l.LockType + " " + l.Mode + " (" + grant + ")\n")
		}
	} else {
		b.WriteString("- 涉及表无锁等待\n")
	}
	return b.String()
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	// Simple int → string without strconv (kept light for prompt formatting)
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
