/*-------------------------------------------------------------------------
 *
 * fallback_rules.go
 *	  Five hard-coded rules that MUST be reported regardless of LLM
 *	  availability. These are unambiguous, fact-level conditions where
 *	  silence would be a serious diagnostic failure:
 *
 *	    1. autovacuum = off            → bloat will grow unchecked
 *	    2. deadlock_count > 0          → real deadlocks occurred
 *	    3. replication_lag > 60s       → standby drift critical
 *	    4. buffer_hit < 80%            → catastrophic cache miss
 *	    5. single_sql > 50% DB Time    → one query dominates the workload
 *
 *	  Everything else (warnings, soft thresholds, structural insights)
 *	  is delegated to M4 LLM synthesizer. The fallback rules are the
 *	  safety net: even if the LLM is offline / hallucinates / misses
 *	  context, these five always surface.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/fallback_rules.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"fmt"
	"strings"
)

// RunFallbackRules evaluates the 5 hard-coded rules against a parsed WDR
// and returns the findings that triggered. Empty slice if all healthy.
// Pure function — no I/O, no LLM, runs in microseconds.
func RunFallbackRules(r *WDRReport) []Finding {
	var findings []Finding

	if f := ruleAutovacuumOff(r); f != nil {
		findings = append(findings, *f)
	}
	if f := ruleDeadlockPresent(r); f != nil {
		findings = append(findings, *f)
	}
	if f := ruleReplicationLagHigh(r); f != nil {
		findings = append(findings, *f)
	}
	if f := ruleBufferHitCritical(r); f != nil {
		findings = append(findings, *f)
	}
	if f := ruleSingleSQLDominant(r); f != nil {
		findings = append(findings, *f)
	}

	return findings
}

// ── Rule 1: autovacuum off ─────────────────────────────────────────────

func ruleAutovacuumOff(r *WDRReport) *Finding {
	v, ok := r.Settings["autovacuum"]
	if !ok {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(v), "off") {
		return nil
	}
	return &Finding{
		ID:       "autovacuum_off",
		Severity: SeverityCritical,
		Category: "config",
		Title:    "autovacuum 已关闭",
		Evidence: []string{
			"pg_settings: autovacuum = off",
			"dead tuples 持续累积, CBO 估算会偏差, 性能会持续退化",
		},
		Suggestion: "立即开启: ALTER SYSTEM SET autovacuum = on; SELECT pg_reload_conf();",
		EvidenceData: map[string]any{
			"current_value": v,
		},
	}
}

// ── Rule 2: deadlock present ───────────────────────────────────────────

func ruleDeadlockPresent(r *WDRReport) *Finding {
	if r.Locks.DeadlockCount <= 0 {
		return nil
	}
	return &Finding{
		ID:       "deadlock_present",
		Severity: SeverityCritical,
		Category: "lock",
		Title:    fmt.Sprintf("时段内发生 %d 次死锁", r.Locks.DeadlockCount),
		Evidence: []string{
			fmt.Sprintf("deadlock_count = %d (任何非零值都是严重事件)", r.Locks.DeadlockCount),
			"死锁意味着事务级别的资源竞争问题, og 自动回滚但应用会看到失败",
		},
		Suggestion: "查 og 日志的 deadlock 详情, 找涉及的对象 + 事务, 优化加锁顺序或缩短事务",
		EvidenceData: map[string]any{
			"deadlock_count": r.Locks.DeadlockCount,
		},
	}
}

// ── Rule 3: replication lag high ───────────────────────────────────────

const replicationLagCriticalSec = 60.0

func ruleReplicationLagHigh(r *WDRReport) *Finding {
	if r.Replication.StandbyCount == 0 {
		return nil // no standby configured
	}
	if r.Replication.MaxLagSeconds < replicationLagCriticalSec {
		return nil
	}
	return &Finding{
		ID:       "replication_lag_high",
		Severity: SeverityCritical,
		Category: "replication",
		Title:    fmt.Sprintf("主备延迟 %.1fs (阈值 %ds)", r.Replication.MaxLagSeconds, int(replicationLagCriticalSec)),
		Evidence: []string{
			fmt.Sprintf("max_lag_seconds = %.1f", r.Replication.MaxLagSeconds),
			fmt.Sprintf("standby_count = %d, sync_mode = %s", r.Replication.StandbyCount, r.Replication.SyncMode),
			"延迟过大: 读写分离场景下备库读到的是旧数据; 主库故障时切换将丢失这段数据",
		},
		Suggestion: "检查主库 WAL 写入速率 + 备库消费速率, 网络带宽, 备库 IO 子系统",
		EvidenceData: map[string]any{
			"lag_seconds": r.Replication.MaxLagSeconds,
			"threshold":   replicationLagCriticalSec,
		},
	}
}

// ── Rule 4: buffer hit catastrophically low ────────────────────────────

const bufferHitCriticalThreshold = 0.80

func ruleBufferHitCritical(r *WDRReport) *Finding {
	ratio := r.IO.BufferHitRatio()
	if ratio == 0 {
		return nil // no IO data — can't judge
	}
	if ratio >= bufferHitCriticalThreshold {
		return nil
	}
	return &Finding{
		ID:       "buffer_hit_critical",
		Severity: SeverityCritical,
		Category: "buffer",
		Title:    fmt.Sprintf("缓冲池命中率 %.1f%% 极低 (< %.0f%% 严重阈值)", ratio*100, bufferHitCriticalThreshold*100),
		Evidence: []string{
			fmt.Sprintf("blks_hit = %d, blks_read = %d → hit ratio %.2f%%", r.IO.BlocksHit, r.IO.BlocksRead, ratio*100),
			fmt.Sprintf("shared_buffers = %d MB (cf. RAM 配置)", r.Memory.SharedBuffersMB),
			"低于 80% 说明 hot page 严重溢出 shared_buffers, 大量物理 IO",
		},
		Suggestion: fmt.Sprintf("shared_buffers 大幅提升 (建议 %dMB → %dMB), 同时检查是否有 SQL 做大表扫描污染缓存",
			r.Memory.SharedBuffersMB, r.Memory.SharedBuffersMB*2),
		EvidenceData: map[string]any{
			"hit_ratio":         ratio,
			"threshold":         bufferHitCriticalThreshold,
			"shared_buffers_mb": r.Memory.SharedBuffersMB,
		},
	}
}

// ── Rule 5: single SQL dominates DB Time ───────────────────────────────

const singleSQLDominantThreshold = 0.50 // 50% of DB Time

func ruleSingleSQLDominant(r *WDRReport) *Finding {
	if r.TimeModel.DBTimeSec == 0 || len(r.TopSQLs) == 0 {
		return nil
	}
	top := r.TopSQLs[0] // already sorted by TotalTimeMS desc
	pct := top.PctOfDBTime(r.TimeModel.DBTimeSec)
	if pct < singleSQLDominantThreshold*100 {
		return nil
	}
	return &Finding{
		ID:       "single_sql_dominant",
		Severity: SeverityCritical,
		Category: "sql",
		Title:    fmt.Sprintf("单 SQL 占总 DB Time %.1f%% (阈值 %.0f%%)", pct, singleSQLDominantThreshold*100),
		Evidence: []string{
			fmt.Sprintf("SQL_ID = %s", top.SQLID),
			fmt.Sprintf("调用 %d 次 · 平均 %s · 总耗时 %s", top.Calls, formatMS(top.AvgTimeMS), formatMS(top.TotalTimeMS)),
			fmt.Sprintf("占 DB Time %.1f%% (整个数据库瓶颈集中在这一条 SQL)", pct),
		},
		Suggestion: "见 Top SQL 深度分析章节, sqltune 已自动生成 5 维度优化方案",
		EvidenceData: map[string]any{
			"sql_id":       top.SQLID,
			"pct_db_time":  pct,
			"threshold":    singleSQLDominantThreshold * 100,
			"calls":        top.Calls,
			"total_ms":     top.TotalTimeMS,
		},
	}
}
