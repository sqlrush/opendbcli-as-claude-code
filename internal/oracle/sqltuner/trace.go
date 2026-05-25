/*-------------------------------------------------------------------------
 *
 * trace.go
 *	  Oracle 10053 CBO decision trace lifecycle.
 *
 *	  10053 is THE Oracle CBO trace — it dumps the cost-based optimizer's
 *	  full reasoning: base statistics, access path candidates, join
 *	  order enumeration, peeked bind values, all cost calculations.
 *	  Comparable to GaussDB GS_PLAN_TRACE but for Oracle's much richer
 *	  CBO. This is the gold-standard CBO trace in the SQL world.
 *
 *	  Implementation challenges that make Oracle the hardest dialect:
 *
 *	    1. **Hard parse required.** 10053 only fires during hard parse
 *	       (cursor cache hit = no trace). We force a hard parse by
 *	       appending a unique opt_param hint comment to the SQL via
 *	       hardParseHintWrap() before EXPLAIN PLAN.
 *
 *	    2. **Per-session isolation.** Multiple concurrent sessions write
 *	       to the same Default Trace File path (instance trace dir). We
 *	       set TRACEFILE_IDENTIFIER per /sqltune so the file name has a
 *	       unique suffix and CollectTrace can find it.
 *
 *	    3. **OS file access.** The trace lands as an OS file in the
 *	       DIAG trace directory. Three paths to read it:
 *	         a. V$DIAG_TRACE_FILE_CONTENTS (19c+) — pure SQL, no OS access
 *	         b. UTL_FILE.GET_LINE — needs DBA-granted DIRECTORY object
 *	         c. External table — needs DBA setup
 *	       We try (a) only. If unavailable, return Available:false with
 *	       a Notes guide telling the user how to manually extract.
 *
 *	    4. **Cleanup.** We turn the event back off in closeFn so the
 *	       session doesn't keep writing trace data.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/trace.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// maxOracleTraceBytes caps the trace body to keep LLM token budget
// tractable. 10053 traces for complex queries can be 200KB-5MB; we
// truncate beyond 1 MB.
const maxOracleTraceBytes = 1 * 1024 * 1024 // 1 MB

// hardParseHintMarker is a magic comment we use to force hard parse +
// identify our SQL in V$SQL. Includes a random component so each
// EXPLAIN gets a new cursor.
//
// We use a comment (not a hint) because invalid hint syntax would
// cause silent ignoring, which doesn't reliably force hard parse.
// Random comment text changes the SQL hash → guaranteed hard parse.
func hardParseHintWrap(sql, tag string) string {
	return fmt.Sprintf("/* opendb_sqltune_%s */ %s", tag, sql)
}

// EnableTrace sets up tracefile_identifier + 10053 event for the
// current session. closeFn turns the event off + clears the identifier
// so the session doesn't leak trace state.
//
// queryTag is informational; we generate our own random tag internally
// since Oracle's TRACEFILE_IDENTIFIER must be valid as part of an OS
// filename (alphanumeric + underscore).
func (p *oraclePlanner) EnableTrace(ctx context.Context, queryTag string) (func() error, *sqltune.TraceData, error) {
	tag := generateTraceTag()
	p.traceTag = tag

	// 1. Set tracefile identifier first — this affects the FILENAME
	//    of subsequent trace writes.
	if _, err := p.driver.Exec(ctx,
		fmt.Sprintf("ALTER SESSION SET TRACEFILE_IDENTIFIER = '%s'", tag)); err != nil {
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "oracle_10053",
			Notes:     "ALTER SESSION SET TRACEFILE_IDENTIFIER 失败: " + err.Error() + "。可能权限不足。",
		}, nil
	}

	// 2. Enable 10053 event (level 1 = full trace).
	if _, err := p.driver.Exec(ctx,
		"ALTER SESSION SET EVENTS '10053 trace name context forever, level 1'"); err != nil {
		// Clear tracefile_identifier to avoid leaving session in weird state.
		_, _ = p.driver.Exec(ctx, "ALTER SESSION SET TRACEFILE_IDENTIFIER = ''")
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "oracle_10053",
			Notes:     "ALTER SESSION SET EVENTS '10053 ... forever' 失败: " + err.Error() + "。需 ALTER SESSION 权限。",
		}, nil
	}

	// Mark active so closeFn knows to clean up.
	var active atomic.Bool
	active.Store(true)
	close := func() error {
		if !active.CompareAndSwap(true, false) {
			return nil
		}
		// Best-effort cleanup. Use background context — closeFn often
		// runs after the parent ctx is canceled.
		_, _ = p.driver.Exec(context.Background(),
			"ALTER SESSION SET EVENTS '10053 trace name context off'")
		_, _ = p.driver.Exec(context.Background(),
			"ALTER SESSION SET TRACEFILE_IDENTIFIER = ''")
		p.traceTag = ""
		return nil
	}

	return close, &sqltune.TraceData{
		Available: true,
		Format:    "oracle_10053",
		Notes: fmt.Sprintf("10053 event enabled, tracefile_identifier=%s. "+
			"务必在 CollectTrace 前对 SQL 跑一次硬解析（hardParseHintWrap 已加随机注释强制 hard parse）。", tag),
	}, nil
}

// CollectTrace finds the most recent tracefile matching our tag and
// reads its contents via V$DIAG_TRACE_FILE_CONTENTS (19c+). On older
// versions / permission issues, returns Available:false with a guide
// for manual extraction.
//
// Strategy:
//   1. Get this session's Default Trace File path via V$DIAG_INFO
//   2. The file actually written has our tag in its name — derive the
//      tagged filename by appending _<tag> before the .trc extension
//   3. SELECT line contents from V$DIAG_TRACE_FILE_CONTENTS
//   4. Concatenate, cap to 1 MB
func (p *oraclePlanner) CollectTrace(ctx context.Context, queryTag string) (*sqltune.TraceData, error) {
	tag := p.traceTag
	if tag == "" {
		return &sqltune.TraceData{
			Available: false,
			Format:    "oracle_10053",
			Notes:     "未启用 trace（EnableTrace 未调或已 close）",
		}, nil
	}

	// Step 1: get base trace file path from this session.
	pathRes, err := p.driver.Query(ctx,
		"SELECT value FROM V$DIAG_INFO WHERE name = 'Default Trace File'")
	if err != nil || len(pathRes.Rows) == 0 {
		return &sqltune.TraceData{
			Available: false,
			Format:    "oracle_10053",
			Notes: "无法读取 V$DIAG_INFO（权限不足或非 11g+ 版本）。Trace 文件应在 ADR diag trace 目录下，文件名包含 tracefile_identifier='" +
				tag + "'。请联系 DBA 手动取出 trace。",
		}, nil
	}
	basePath := toString(pathRes.Rows[0][0])

	// Step 2: derive the tagged filename. Format is
	// <prefix>_ora_<spid>_<tag>.trc — Oracle inserts our identifier
	// before .trc. We pass the base path and let V$DIAG_TRACE_FILE_CONTENTS
	// match by trace_filename LIKE pattern.
	taggedPattern := tagInPath(basePath, tag)

	// Step 3: read contents via V$DIAG_TRACE_FILE_CONTENTS (19c+).
	// payload is per-line; line_number gives order.
	q := `SELECT payload, LENGTH(payload) AS line_len
	        FROM V$DIAG_TRACE_FILE_CONTENTS
	       WHERE trace_filename = :1
	       ORDER BY line_number`
	contRes, err := p.driver.Query(ctx, q, taggedPattern)
	if err != nil {
		return &sqltune.TraceData{
			Available: false,
			Format:    "oracle_10053",
			Notes: "查询 V$DIAG_TRACE_FILE_CONTENTS 失败（11g/12c 无此 view）: " + err.Error() +
				"。Trace 文件应在: " + basePath + " 同目录下含标识 '" + tag +
				"' 的 .trc 文件。请联系 DBA 手动取出。",
		}, nil
	}
	if len(contRes.Rows) == 0 {
		return &sqltune.TraceData{
			Available: false,
			Format:    "oracle_10053",
			Notes: "V$DIAG_TRACE_FILE_CONTENTS 无记录。trace_filename=" + taggedPattern +
				"。可能：① SQL 走了 cursor cache 没硬解析 ② trace 写入还没刷盘 ③ 权限不够。" +
				"硬解析触发: hardParseHintWrap 已加随机注释强制 hard parse，但仍可能 SOFT parse 命中。",
		}, nil
	}

	// Step 4: concat with size cap.
	var b strings.Builder
	truncated := false
	for _, row := range contRes.Rows {
		line := toString(row[0])
		if b.Len()+len(line)+1 > maxOracleTraceBytes {
			truncated = true
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	body := b.String()

	notes := fmt.Sprintf("Oracle 10053 trace from %s. 行数=%d, bytes=%d.",
		taggedPattern, len(contRes.Rows), len(body))
	if truncated {
		notes += fmt.Sprintf(" 已截断到首 %d bytes 控制 LLM token 预算。", maxOracleTraceBytes)
	}

	return &sqltune.TraceData{
		Available: true,
		Format:    "oracle_10053",
		Body:      body,
		Bytes:     len(body),
		Truncated: truncated,
		Notes:     notes,
	}, nil
}

// tagInPath inserts the tracefile_identifier into the standard Oracle
// trace filename pattern. Default Trace File is e.g.:
//   /opt/oracle/diag/rdbms/orcl/orcl/trace/orcl_ora_12345.trc
// With tracefile_identifier='opendb_abc123' it becomes:
//   /opt/oracle/diag/rdbms/orcl/orcl/trace/orcl_ora_12345_opendb_abc123.trc
//
// We construct the expected tagged path by inserting _<tag> before .trc.
func tagInPath(basePath, tag string) string {
	const suffix = ".trc"
	if strings.HasSuffix(basePath, suffix) {
		stem := basePath[:len(basePath)-len(suffix)]
		return stem + "_" + tag + suffix
	}
	// Defensive: if the path doesn't end in .trc (shouldn't happen),
	// just append the tag — let V$DIAG_TRACE_FILE_CONTENTS exact match
	// fail with the clear "no rows" path.
	return basePath + "_" + tag
}

// generateTraceTag returns a unique tag suitable for Oracle's
// TRACEFILE_IDENTIFIER (alphanumeric + underscore, ≤48 chars).
func generateTraceTag() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "opendb_fallback"
	}
	return "opendb_" + hex.EncodeToString(b[:]) // 7+8=15 chars
}

func noopClose() error { return nil }
