/*-------------------------------------------------------------------------
 *
 * evidence_builder.go
 *    Deterministic evidence package for WDR analysis.
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *    internal/opengauss/wdranalyze/evidence_builder.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"fmt"
	"sort"
	"strings"
)

type EvidenceRow struct {
	Name     string
	Value    string
	Baseline string
	Class    string
	Source   string
}

type EvidenceAction struct {
	Priority string
	Action   string
	Reason   string
}

type EvidenceBundle struct {
	Window    []EvidenceRow
	Load      []EvidenceRow
	TopWaits  []EvidenceRow
	TopSQLs   []EvidenceRow
	Classes   []EvidenceRow
	RootChain []string
	Actions   []EvidenceAction
}

func BuildEvidenceBundle(report *WDRReport, findings []Finding, sqlTunes []SQLTuneResult) EvidenceBundle {
	var e EvidenceBundle
	if report == nil {
		return e
	}
	e.Window = []EvidenceRow{{
		Name:     "分析窗口",
		Value:    fmt.Sprintf("%s ~ %s", formatTime(report.Header.WindowStart), formatTime(report.Header.WindowEnd)),
		Baseline: formatDuration(report.Header.WindowDuration()),
		Class:    "时间线",
		Source:   "WDR header",
	}}
	if report.Header.SnapshotIDStart > 0 || report.Header.SnapshotIDEnd > 0 {
		e.Window = append(e.Window, EvidenceRow{Name: "Snapshot", Value: fmt.Sprintf("%d -> %d", report.Header.SnapshotIDStart, report.Header.SnapshotIDEnd), Class: "时间线", Source: "WDR header"})
	}
	e.Load = buildLoadEvidence(report)
	e.TopWaits = buildWaitEvidence(report)
	e.TopSQLs = buildTopSQLEvidence(report)
	e.Classes = buildClassEvidence(report)
	e.RootChain = buildWDRRootChain(report, findings)
	e.Actions = buildWDRActions(report, findings, sqlTunes)
	return e
}

func RenderEvidenceMarkdown(e EvidenceBundle) string {
	var b strings.Builder
	b.WriteString("## 结构化证据\n\n")
	renderEvidenceTable(&b, "时间窗口", e.Window)
	renderEvidenceTable(&b, "负载变化与资源分类", e.Load)
	renderEvidenceTable(&b, "Top Wait", e.TopWaits)
	renderEvidenceTable(&b, "Top SQL", e.TopSQLs)
	renderEvidenceTable(&b, "IO / CPU / Lock / WAL 分类", e.Classes)
	if len(e.RootChain) > 0 {
		b.WriteString("### 根因链\n\n")
		for i, item := range e.RootChain {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, item))
		}
		b.WriteString("\n")
	}
	if len(e.Actions) > 0 {
		b.WriteString("### 建议动作\n\n")
		b.WriteString("| 优先级 | 动作 | 依据 |\n")
		b.WriteString("|---|---|---|\n")
		for _, a := range e.Actions {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", esc(a.Priority), esc(a.Action), esc(a.Reason)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func EvidencePromptBlock(report *WDRReport, findings []Finding, sqlTunes []SQLTuneResult) string {
	e := BuildEvidenceBundle(report, findings, sqlTunes)
	return RenderEvidenceMarkdown(e)
}

func renderEvidenceTable(b *strings.Builder, title string, rows []EvidenceRow) {
	if len(rows) == 0 {
		return
	}
	b.WriteString("### " + title + "\n\n")
	b.WriteString("| 项 | 实测值 | 阈值/基线 | 分类 | 来源 |\n")
	b.WriteString("|---|---:|---:|---|---|\n")
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", esc(r.Name), esc(r.Value), esc(r.Baseline), esc(r.Class), esc(r.Source)))
	}
	b.WriteString("\n")
}

func buildLoadEvidence(r *WDRReport) []EvidenceRow {
	var rows []EvidenceRow
	if r.TimeModel.DBTimeSec > 0 {
		rows = append(rows, EvidenceRow{Name: "DB Time", Value: formatSeconds(r.TimeModel.DBTimeSec), Class: classifyDBTime(r), Source: "Time Model"})
		if r.TimeModel.CPUTimeSec > 0 {
			rows = append(rows, EvidenceRow{Name: "CPU Time", Value: fmt.Sprintf("%s (%.1f%%)", formatSeconds(r.TimeModel.CPUTimeSec), safePct(r.TimeModel.CPUTimeSec, r.TimeModel.DBTimeSec)), Baseline: "of DB Time", Class: "CPU", Source: "Time Model"})
		}
		if r.TimeModel.WaitTimeSec > 0 {
			rows = append(rows, EvidenceRow{Name: "Wait Time", Value: fmt.Sprintf("%s (%.1f%%)", formatSeconds(r.TimeModel.WaitTimeSec), safePct(r.TimeModel.WaitTimeSec, r.TimeModel.DBTimeSec)), Baseline: "of DB Time", Class: "Wait", Source: "Time Model"})
		}
	}
	if r.IO.BlocksHit > 0 || r.IO.BlocksRead > 0 {
		rows = append(rows, EvidenceRow{Name: "Buffer Hit", Value: fmt.Sprintf("%.2f%%", r.IO.BufferHitRatio()*100), Baseline: ">=90%", Class: classifyBufferHit(r.IO.BufferHitRatio()), Source: "IO Profile"})
	}
	if r.IO.TempFilesMB > 0 {
		rows = append(rows, EvidenceRow{Name: "Temp Files", Value: fmt.Sprintf("%.1fMB", r.IO.TempFilesMB), Baseline: "越低越好", Class: "IO/temp", Source: "IO Profile"})
	}
	if r.IO.WALWritesMB > 0 {
		rows = append(rows, EvidenceRow{Name: "WAL Written", Value: fmt.Sprintf("%.1fMB", r.IO.WALWritesMB), Baseline: "结合业务写入量", Class: "WAL", Source: "IO Profile"})
	}
	if r.Locks.DeadlockCount > 0 || r.Locks.LockWaitCount > 0 {
		rows = append(rows, EvidenceRow{Name: "Lock/Deadlock", Value: fmt.Sprintf("wait=%d deadlock=%d", r.Locks.LockWaitCount, r.Locks.DeadlockCount), Baseline: "deadlock=0", Class: "Lock", Source: "Lock Stats"})
	}
	if r.Replication.StandbyCount > 0 {
		rows = append(rows, EvidenceRow{Name: "Replication Lag", Value: fmt.Sprintf("%.1fs", r.Replication.MaxLagSeconds), Baseline: "<60s", Class: "Replication", Source: "Replication"})
	}
	return rows
}

func buildWaitEvidence(r *WDRReport) []EvidenceRow {
	if len(r.Waits) == 0 {
		return nil
	}
	var rows []EvidenceRow
	n := minLocal(len(r.Waits), 8)
	for i := 0; i < n; i++ {
		w := r.Waits[i]
		rows = append(rows, EvidenceRow{
			Name:     w.Name,
			Value:    fmt.Sprintf("%s / %.1f%% DB Time", formatMS(w.WaitTimeMS), w.PctOfDBTime),
			Baseline: fmt.Sprintf("avg %s", formatMS(w.AvgWaitMS)),
			Class:    nonEmptyLocal(w.Category, classifyWaitName(w.Name)),
			Source:   "Top Wait Events",
		})
	}
	return rows
}

func buildTopSQLEvidence(r *WDRReport) []EvidenceRow {
	if len(r.TopSQLs) == 0 {
		return nil
	}
	var rows []EvidenceRow
	n := minLocal(len(r.TopSQLs), 8)
	for i := 0; i < n; i++ {
		s := r.TopSQLs[i]
		rows = append(rows, EvidenceRow{
			Name:     "SQL_ID " + s.SQLID,
			Value:    fmt.Sprintf("calls=%d avg=%s total=%s", s.Calls, formatMS(s.AvgTimeMS), formatMS(s.TotalTimeMS)),
			Baseline: fmt.Sprintf("%.1f%% DB Time", s.PctOfDBTime(r.TimeModel.DBTimeSec)),
			Class:    classifyTopSQL(s),
			Source:   strings.Join(s.Sources, ","),
		})
	}
	return rows
}

func buildClassEvidence(r *WDRReport) []EvidenceRow {
	if len(r.SectionScores) == 0 {
		return nil
	}
	rows := make([]EvidenceRow, 0, len(r.SectionScores))
	for _, s := range r.SectionScores {
		rows = append(rows, EvidenceRow{Name: s.Name, Value: formatKeyMetrics(s.KeyMetrics), Baseline: sectionRuleSummary(s.Rules), Class: string(s.Level), Source: "section_evaluator"})
	}
	return rows
}

func buildWDRRootChain(r *WDRReport, findings []Finding) []string {
	var chain []string
	if len(r.SectionScores) > 0 {
		for _, s := range r.SectionScores {
			if s.Level != SectionGood && s.Summary != "" {
				chain = append(chain, fmt.Sprintf("%s: %s", s.Name, s.Summary))
			}
		}
	}
	for _, f := range findings {
		chain = append(chain, fmt.Sprintf("%s: %s", f.Category, f.Title))
	}
	if len(r.Waits) > 0 && r.Waits[0].PctOfDBTime > 20 {
		chain = append(chain, fmt.Sprintf("Top wait %s 占 DB Time %.1f%%", r.Waits[0].Name, r.Waits[0].PctOfDBTime))
	}
	if len(r.TopSQLs) > 0 {
		top := r.TopSQLs[0]
		if pct := top.PctOfDBTime(r.TimeModel.DBTimeSec); pct > 20 {
			chain = append(chain, fmt.Sprintf("Top SQL %s 占 DB Time %.1f%%", top.SQLID, pct))
		}
	}
	return dedupeStrings(chain, 8)
}

func buildWDRActions(r *WDRReport, findings []Finding, sqlTunes []SQLTuneResult) []EvidenceAction {
	var actions []EvidenceAction
	for _, f := range findings {
		if f.Suggestion != "" {
			actions = append(actions, EvidenceAction{Priority: severityPriority(f.Severity), Action: f.Suggestion, Reason: f.Title})
		}
	}
	for _, s := range r.SectionScores {
		for _, rule := range s.Rules {
			actions = append(actions, EvidenceAction{Priority: sectionPriority(rule.Level), Action: actionForSectionRule(rule), Reason: fmt.Sprintf("%s/%s 实测 %s", s.Name, rule.Metric, rule.Observed)})
		}
	}
	for _, st := range sqlTunes {
		if st.Error == "" && !st.Skipped && st.SQLID != "" {
			actions = append(actions, EvidenceAction{Priority: "P1", Action: "对 SQL_ID " + st.SQLID + " 执行 sqltune 推荐的已验证/低风险方案", Reason: fmt.Sprintf("sqltune cost %.0f -> %.0f best=%.1fx", st.OriginalCost, st.BestNewCost, st.BestSpeedup)})
		}
	}
	return dedupeActions(actions, 10)
}

func classifyDBTime(r *WDRReport) string {
	if r.Header.WindowDuration() <= 0 || r.TimeModel.DBTimeSec <= 0 {
		return "负载"
	}
	dbTimePerSec := r.TimeModel.DBTimeSec / r.Header.WindowDuration().Seconds()
	if dbTimePerSec >= 0.8 {
		return "高负载"
	}
	return "正常/低负载"
}

func classifyBufferHit(ratio float64) string {
	switch {
	case ratio == 0:
		return "未知"
	case ratio < 0.8:
		return "IO风险"
	case ratio < 0.9:
		return "IO警告"
	default:
		return "正常"
	}
}

func classifyWaitName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "lock"):
		return "Lock"
	case strings.Contains(lower, "wal"):
		return "WAL"
	case strings.Contains(lower, "io") || strings.Contains(lower, "read") || strings.Contains(lower, "write"):
		return "IO"
	case strings.Contains(lower, "cpu"):
		return "CPU"
	default:
		return "Other"
	}
}

func classifyTopSQL(s TopSQLEntry) string {
	if hasSource(s.Sources, "cpu") {
		return "CPU SQL"
	}
	if hasSource(s.Sources, "io") || s.TotalIO > 0 {
		return "IO SQL"
	}
	if s.Calls > 10000 {
		return "高频 SQL"
	}
	return "耗时 SQL"
}

func hasSource(sources []string, want string) bool {
	for _, src := range sources {
		if strings.Contains(strings.ToLower(src), want) {
			return true
		}
	}
	return false
}

func sectionRuleSummary(rules []SectionRule) string {
	if len(rules) == 0 {
		return "无触发规则"
	}
	parts := make([]string, 0, minLocal(len(rules), 3))
	for i, r := range rules {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("另 %d 条", len(rules)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", r.Metric, r.Threshold))
	}
	return strings.Join(parts, "; ")
}

func actionForSectionRule(rule SectionRule) string {
	id := strings.ToLower(rule.ID)
	switch {
	case strings.Contains(id, "temp"):
		return "定位产生临时文件的 Top SQL，优先优化排序/HASH；必要时按会话提升 work_mem"
	case strings.Contains(id, "deadlock"):
		return "查数据库日志中的 deadlock 详情，统一业务事务加锁顺序并缩短事务"
	case strings.Contains(id, "p95") || strings.Contains(id, "sql"):
		return "按 Top SQL 排名逐条 /sqltune，优先处理占 DB Time 最高的 SQL"
	case strings.Contains(id, "buffer") || strings.Contains(id, "physical"):
		return "核对 shared_buffers 与热数据集；优先消除大表顺序扫描"
	case strings.Contains(id, "wal"):
		return "核对 WAL 写入量、checkpoint 与存储延迟，降低批量写入峰值"
	default:
		return "按该模块触发指标做专项排查，并复测同窗口 WDR"
	}
}

func severityPriority(s Severity) string {
	switch s {
	case SeverityCritical:
		return "P0"
	case SeverityWarning:
		return "P1"
	default:
		return "P2"
	}
}

func sectionPriority(l SectionLevel) string {
	switch l {
	case SectionRisk:
		return "P0"
	case SectionWarning:
		return "P1"
	default:
		return "P2"
	}
}

func dedupeStrings(in []string, limit int) []string {
	seen := make(map[string]bool)
	var out []string
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func dedupeActions(in []EvidenceAction, limit int) []EvidenceAction {
	priorityRank := map[string]int{"P0": 0, "P1": 1, "P2": 2}
	sort.SliceStable(in, func(i, j int) bool { return priorityRank[in[i].Priority] < priorityRank[in[j].Priority] })
	seen := make(map[string]bool)
	var out []EvidenceAction
	for _, a := range in {
		key := a.Priority + ":" + a.Action
		if strings.TrimSpace(a.Action) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func minLocal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func nonEmptyLocal(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
