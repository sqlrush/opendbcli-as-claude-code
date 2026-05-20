/*-------------------------------------------------------------------------
 *
 * wdranalyze_skill.go
 *	  WDRAnalyzeSkill: parse + interpret an openGauss / GaussDB WDR
 *	  (Workload Diagnosis Report). M1 milestone: collector + parser +
 *	  basic renderer. Phase 3 (rules), Phase 4 (sqltune), Phase 5 (LLM)
 *	  will be added in M2-M4. Design doc: docs/wdr/plan-wdranalyze.md.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/wdranalyze_skill.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/opengauss/wdranalyze"
	"github.com/sqlrush/opendb/internal/skill"
)

// WDRAnalyzeSkill implements /wdranalyze.
//
// Dependencies:
//   - driver: og DB connection (Phase 1 collect, Phase 4 sqltune EXPLAIN)
//   - sqlfetch: sibling skill, exposed for SQL_ID resolution in Phase 4
//   - modelMgr: LLM provider for Phase 4 sqltune Round 1 + Phase 5 synthesis
//   - memStore: cross-wdranalyze fingerprint cache + Phase 5 memory injection
//
// All Phase 4 / Phase 5 deps are optional. nil values cleanly degrade:
//   - no sqlfetch / no LLM → skip TopSQL drill
//   - no LLM → skip synthesis (output fallback findings + sqlfetch headers only)
//   - no memStore → no cache, run sqltune every time
type WDRAnalyzeSkill struct {
	driver   db.Driver
	sqlfetch *SQLFetchSkill
	modelMgr *model.Manager
	memStore *memory.Store
}

// NewWDRAnalyzeSkill constructs the skill with all dependencies. Pass nil
// for sqlfetch / modelMgr / memStore to disable optional phases.
func NewWDRAnalyzeSkill(
	driver db.Driver,
	sqlfetch *SQLFetchSkill,
	modelMgr *model.Manager,
	memStore *memory.Store,
) *WDRAnalyzeSkill {
	return &WDRAnalyzeSkill{
		driver:   driver,
		sqlfetch: sqlfetch,
		modelMgr: modelMgr,
		memStore: memStore,
	}
}

func (s *WDRAnalyzeSkill) Name() string                       { return "wdranalyze" }
func (s *WDRAnalyzeSkill) Description() string                { return "解读 WDR 报告 + 风险分级 + Top SQL 优化建议" }
func (s *WDRAnalyzeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *WDRAnalyzeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name: "wdranalyze",
		Description: "Analyze an openGauss / GaussDB WDR (Workload Diagnosis Report) for a time " +
			"window. Returns a structured markdown report with: workload characterization, severity-" +
			"graded risk findings, Top SQL list, and (when Phase 3+ enabled) sqltune-verified " +
			"optimization candidates and LLM synthesis. " +
			"Use modes: 'latest' (most recent snapshot pair), '<snapA> <snapB>' (specific snapshots), " +
			"'last1h/last24h' (time window), 'file <path>' (parse existing WDR HTML/text).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Mode + args. Examples: 'latest', '15234 15247', 'last1h', 'file /tmp/wdr.html'",
				},
			},
			"required": []string{"args"},
		},
	}
}

func (s *WDRAnalyzeSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "wdranalyze",
		Aliases:     []string{"wdra"},
		Usage:       "/wdranalyze <mode> [args]",
		Description: "解读 WDR 报告（风险分级 + Top SQL 优化建议）",
		Examples: []string{
			"/wdranalyze latest",
			"/wdranalyze 15234 15247",
			"/wdranalyze last1h",
			"/wdranalyze last24h",
			"/wdranalyze file /tmp/wdr.html",
		},
	}
}

func (s *WDRAnalyzeSkill) Validate(params skill.Params) error {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		return fmt.Errorf("需要提供模式参数 (latest / <snapA> <snapB> / lastNh / file <path>)")
	}
	return nil
}

func (s *WDRAnalyzeSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	opts, err := parseArgs(args)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /wdranalyze 参数错误: " + err.Error() + "\n  示例: /wdranalyze latest",
			Summary:  err.Error(),
		}, nil
	}

	start := time.Now()

	// Phase 1: Collect
	collector := wdranalyze.NewCollector(s.driver)
	raw, snapA, snapB, err := collector.Fetch(ctx, opts)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /wdranalyze 采集失败: " + err.Error(),
			Summary:  err.Error(),
		}, nil
	}

	// Phase 2: Parse
	report, err := wdranalyze.Parse(raw)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: "  /wdranalyze 解析失败: " + err.Error() + "\n  (内容可能不是合法 WDR 报告)",
			Summary:  err.Error(),
		}, nil
	}
	if report.Header.SnapshotIDStart < 0 && snapA > 0 {
		report.Header.SnapshotIDStart = snapA
		report.Header.SnapshotIDEnd = snapB
	}

	// Phase 3: Fallback rules (5 hard-coded must-report findings)
	fallbackFindings := wdranalyze.RunFallbackRules(report)

	// Phase 4: TopSQL drill-down (M3, optional — needs sqlfetch + LLM + driver)
	var sqlTunes []wdranalyze.SQLTuneResult
	if !opts.SkipSQLTune && s.sqlfetch != nil && s.modelMgr != nil && s.modelMgr.Provider() != nil {
		sqlTunes = wdranalyze.DrillTopSQLs(
			ctx, report, opts.TopN,
			&sqlfetchAdapter{inner: s.sqlfetch},
			s.modelMgr.Provider(),
			s.memStore,
			s.driver,
		)
	}

	// Phase 5: LLM synthesis (M4, optional — needs LLM)
	var synthesis string
	if !opts.SkipLLM && s.modelMgr != nil && s.modelMgr.Provider() != nil {
		if out, err := wdranalyze.Synthesize(ctx, s.modelMgr.Provider(), report, fallbackFindings, sqlTunes); err == nil {
			// Phase 5.5: post-validation. Lenient checks — strong models
			// usually pass without patching; weak models that miss required
			// sections or fail to mention fallback findings get the gap
			// filled in by deterministic code.
			synthesis = wdranalyze.ValidateAndPatch(out, fallbackFindings)
		}
		// On error: silently skip synthesis section; fallback findings + sqltune
		// outputs still render. Don't surface the error in the report — the
		// renderer's emptiness is signal enough.
	}

	analysis := &wdranalyze.Analysis{
		Report:       report,
		Findings:     fallbackFindings,
		SQLTunes:     sqlTunes,
		LLMSynthesis: synthesis,
		GeneratedAt:  start,
		Duration:     time.Since(start),
	}

	// Phase 6: Render
	markdown := wdranalyze.Render(analysis)

	// Phase 7: Persist
	if reportPath, err := persistReport(opts.OutputDir, analysis, markdown); err == nil {
		markdown += "\n> 报告已保存到 `" + reportPath + "`\n"
	}

	// v1.1.51: prepend a directive when agent loop will see this tool result.
	// Without it the agent's LLM re-summarizes the WDR analysis using its own
	// generic prompt, dropping the 3-layer structure. The directive tells the
	// outer agent to pass through verbatim.
	const agentDirective = "<!-- WDR_REPORT_BEGIN: Below is a complete, structured WDR analysis report (Layer 1 / Layer 2 / Layer 3) generated by /wdranalyze. Return it to the user verbatim — do not re-summarize, do not reformat, do not extract subsections. -->\n\n"
	rendered := agentDirective + markdown

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary: fmt.Sprintf("WDR 分析完成（窗口 %s，%d 条 Top SQL，%d 个风险点）。已生成 3 层完整报告（总览/详解/方案），请将上述 Rendered 内容原样返回用户。",
			formatWindowSummary(report), len(report.TopSQLs), len(analysis.Findings)),
	}, nil
}

// parseArgs converts the CLI args string into Options. Examples:
//
//	"latest"                 → Mode=latest
//	"15234 15247"            → Mode=snapshot, A=15234, B=15247
//	"last1h" / "last24h"     → Mode=timerange
//	"file /tmp/wdr.html"     → Mode=file, FilePath=...
//	"latest --top-n 10"      → Mode=latest, TopN=10
func parseArgs(args string) (wdranalyze.Options, error) {
	tokens := strings.Fields(args)
	if len(tokens) == 0 {
		return wdranalyze.Options{}, fmt.Errorf("empty args")
	}

	opts := wdranalyze.Options{TopN: 5}

	// First token determines mode
	first := tokens[0]
	switch {
	case first == "latest":
		opts.Mode = "latest"
		tokens = tokens[1:]
	case first == "file":
		opts.Mode = "file"
		if len(tokens) < 2 {
			return opts, fmt.Errorf("file mode requires path: /wdranalyze file <path>")
		}
		opts.FilePath = tokens[1]
		tokens = tokens[2:]
	case strings.HasPrefix(first, "last") && len(first) > 4:
		// last1h / last30m / last24h / last7d
		dur, err := parseLastDuration(first)
		if err != nil {
			return opts, err
		}
		opts.Mode = "timerange"
		opts.Window = dur
		tokens = tokens[1:]
	default:
		// Try snapshot ID pair
		a, errA := strconv.ParseInt(first, 10, 64)
		if errA != nil {
			return opts, fmt.Errorf("invalid mode %q (use: latest / <snapA> <snapB> / lastNh / file <path>)", first)
		}
		if len(tokens) < 2 {
			return opts, fmt.Errorf("snapshot mode requires two IDs: /wdranalyze <snapA> <snapB>")
		}
		b, errB := strconv.ParseInt(tokens[1], 10, 64)
		if errB != nil {
			return opts, fmt.Errorf("invalid second snapshot ID %q", tokens[1])
		}
		opts.Mode = "snapshot"
		opts.SnapshotA = a
		opts.SnapshotB = b
		tokens = tokens[2:]
	}

	// Parse remaining flags
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "--top-n":
			if i+1 >= len(tokens) {
				return opts, fmt.Errorf("--top-n requires a value")
			}
			n, err := strconv.Atoi(tokens[i+1])
			if err != nil || n <= 0 {
				return opts, fmt.Errorf("--top-n must be a positive integer")
			}
			opts.TopN = n
			i++
		case "--no-sql":
			opts.SkipSQLTune = true
		case "--no-llm":
			opts.SkipLLM = true
		default:
			return opts, fmt.Errorf("unknown flag: %s", tokens[i])
		}
	}

	return opts, nil
}

// parseLastDuration parses "last1h" / "last30m" / "last24h" / "last7d".
func parseLastDuration(s string) (time.Duration, error) {
	body := strings.TrimPrefix(s, "last")
	if body == "" {
		return 0, fmt.Errorf("missing duration after 'last'")
	}
	// Go's time.ParseDuration handles "1h", "30m" but not "7d"
	if strings.HasSuffix(body, "d") {
		num, err := strconv.Atoi(strings.TrimSuffix(body, "d"))
		if err != nil || num <= 0 {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(num) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(body)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration: %s (use last1h / last24h / last7d)", s)
	}
	return d, nil
}

// persistReport writes the markdown to ~/.opendb/wdr_reports/<ts>-<window>.md.
func persistReport(outputDir string, a *wdranalyze.Analysis, markdown string) (string, error) {
	if outputDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		outputDir = filepath.Join(home, ".opendb", "wdr_reports")
	}
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return "", err
	}
	ts := a.GeneratedAt.Format("20060102-150405")
	window := "unknown"
	if d := a.Report.Header.WindowDuration(); d > 0 {
		window = strings.ReplaceAll(d.String(), " ", "")
	}
	filename := fmt.Sprintf("%s-%s.md", ts, window)
	path := filepath.Join(outputDir, filename)
	if err := os.WriteFile(path, []byte(markdown), 0640); err != nil {
		return "", err
	}
	return path, nil
}

func formatWindowSummary(r *wdranalyze.WDRReport) string {
	d := r.Header.WindowDuration()
	if d == 0 {
		return "unknown"
	}
	return d.String()
}

// sqlfetchAdapter bridges SQLFetchSkill (its Resolve method returns
// *query.ResolvedSQL) to wdranalyze.SQLResolver (which expects its own
// *wdranalyze.ResolvedSQL). The two types are field-identical; this is
// trivial copy. Defined here to keep the wdranalyze package free of an
// import cycle on skill/query.
type sqlfetchAdapter struct {
	inner *SQLFetchSkill
}

func (a *sqlfetchAdapter) Resolve(ctx context.Context, sqlID int64) (*wdranalyze.ResolvedSQL, error) {
	r, err := a.inner.Resolve(ctx, sqlID)
	if err != nil || r == nil {
		return nil, err
	}
	return &wdranalyze.ResolvedSQL{
		SQL:          r.SQL,
		OriginalSQL:  r.OriginalSQL,
		Schema:       r.Schema,
		Source:       r.Source,
		HasLiterals:  r.HasLiterals,
		Substituted:  r.Substituted,
		Placeholders: r.Placeholders,
		Notes:        r.Notes,
	}, nil
}
