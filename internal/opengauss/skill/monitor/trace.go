/*-------------------------------------------------------------------------
 *
 * trace.go
 *	  TraceSkill captures OS-level stack traces and generates flame
 *	  graphs for openGauss-compatible engines. The server process name is
 *	  gaussdb (see internal/trace/hostcheck.go), and trace.IsLocal enforces
 *	  the "OpenDB must run on the DB host" rule.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/trace.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/diagtrace"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/trace"
	"github.com/sqlrush/opendb/internal/version"
)

// TraceSkill captures OS-level stack traces and generates flame graphs for
// openGauss-compatible engines. The server process name is gaussdb (see
// internal/trace/hostcheck.go), and trace.IsLocal enforces the "OpenDB must run
// on the DB host" rule.
//
// Structure mirrors PostgreSQL's trace skill — the heavy lifting (perf record,
// perf script, collapse, flamegraph SVG, source lookup) lives in the shared
// internal/trace package.
type TraceSkill struct {
	dbType      string
	displayName string
	connHost    string
	traceCfg    *config.TraceConfig
	collector   *trace.Collector
}

// NewTraceSkill creates a TraceSkill for openGauss. connHost comes from the
// active connection so we can refuse to trace a remote process (perf only
// works on the local host). traceCfg carries duration / top-N / output dir
// defaults from opendb config.
func NewTraceSkill(connHost string, traceCfg *config.TraceConfig) *TraceSkill {
	return NewTraceSkillForDB("opengauss", "openGauss", connHost, traceCfg)
}

// NewTraceSkillForDB creates a TraceSkill for an openGauss-compatible product.
// dbType is passed to internal/trace for process discovery and source mapping;
// displayName is used only in user-facing text.
func NewTraceSkillForDB(dbType, displayName, connHost string, traceCfg *config.TraceConfig) *TraceSkill {
	dbType = strings.TrimSpace(strings.ToLower(dbType))
	if dbType == "" {
		dbType = "opengauss"
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = defaultTraceDisplayName(dbType)
	}
	return &TraceSkill{
		dbType:      dbType,
		displayName: displayName,
		connHost:    connHost,
		traceCfg:    traceCfg,
		collector:   &trace.Collector{},
	}
}

func (s *TraceSkill) Name() string { return "trace" }
func (s *TraceSkill) Description() string {
	return fmt.Sprintf("OS 堆栈采集 + 火焰图 (%s)", s.displayName)
}
func (s *TraceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TraceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "trace",
		Description: fmt.Sprintf("采集 %s 进程 (gaussdb) OS 堆栈，生成火焰图，返回热点函数和折叠栈帧", s.displayName),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration": map[string]any{
					"type":        "integer",
					"description": "采集秒数 (1-10, 默认 3)",
				},
			},
		},
	}
}

func (s *TraceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "trace",
		Usage:    "/trace [duration]  (兼容: /trace last|history)",
		Examples: []string{"/trace", "/trace 5", "/trace last", "/trace history", "/diagtrace last"},
	}
}

func (s *TraceSkill) Validate(params skill.Params) error {
	arg := strings.ToLower(strings.TrimSpace(params.StringOr("args", "")))
	if isTraceLastArg(arg) || isTraceHistoryArg(arg) {
		return nil
	}
	dur := params.IntOr("duration", 3)
	if dur < 1 || dur > 10 {
		return fmt.Errorf("采集时长范围 1-10 秒 (当前: %d)", dur)
	}
	return nil
}

func (s *TraceSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	arg := strings.ToLower(strings.TrimSpace(params.StringOr("args", "")))
	if isTraceLastArg(arg) {
		rendered := diagtraceCompatibilityNotice("last") + diagtrace.RenderLast()
		if traceJSON(arg) {
			rendered = diagtraceCompatibilityNotice("last") + diagtrace.RenderLastJSON()
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: rendered,
			Summary:  "diagnosis trace last",
		}, nil
	}
	if isTraceHistoryArg(arg) {
		rendered := diagtraceCompatibilityNotice("history") + diagtrace.RenderHistory(traceHistoryLimit(arg))
		if traceJSON(arg) {
			rendered = diagtraceCompatibilityNotice("history") + diagtrace.RenderHistoryJSON(traceHistoryLimit(arg))
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: rendered,
			Summary:  "diagnosis trace history",
		}, nil
	}

	pid, err := trace.IsLocal(ctx, s.dbType, s.connHost)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("trace 不可用: %s\n\n说明: /trace 是 OS/%s 进程堆栈采集命令。如需查看最近一次 DBAA 诊断链路，请使用 /diagtrace last。", err, s.displayName),
			Summary:  "trace unavailable",
		}, nil
	}

	dur := params.IntOr("duration", s.defaultDuration())
	outDir := s.outputDir()
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("创建输出目录失败: %s", err),
			Summary:  "mkdir failed",
		}, nil
	}

	opts := trace.CaptureOpts{
		PID:      pid,
		Duration: time.Duration(dur) * time.Second,
		TopN:     s.topN(),
		OutDir:   outDir,
		Freq:     99,
	}
	result, err := s.collector.Capture(ctx, opts)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("采集失败: %s", err),
			Summary:  "capture failed",
		}, nil
	}
	result.DBType = s.dbType

	sources := s.lookupSources(result.TopFuncs)
	rendered := s.formatOutput(result, sources)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("trace: PID %d, %ds, %d hot funcs", pid, dur, len(result.TopFuncs)),
	}, nil
}

// DiagTraceSkill shows DBAA diagnosis-chain traces (router, tool calls, LLM
// rounds). It is intentionally separate from /trace, which profiles the local
// database OS process. /trace last/history remain compatibility aliases.
type DiagTraceSkill struct {
	modelMgr *model.Manager
}

func NewDiagTraceSkill(managers ...*model.Manager) *DiagTraceSkill {
	var mgr *model.Manager
	if len(managers) > 0 {
		mgr = managers[0]
	}
	return &DiagTraceSkill{modelMgr: mgr}
}

func (s *DiagTraceSkill) Name() string                       { return "diagtrace" }
func (s *DiagTraceSkill) Description() string                { return "DBAA 诊断链路 trace (路由/工具/LLM)" }
func (s *DiagTraceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *DiagTraceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "diagtrace",
		Description: "查看最近一次或历史 DBAA 诊断链路 trace，包含路由、工具调用、LLM 轮次和错误原因",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "默认显示最近一次详情；list 显示最近10次摘要；数字编号显示对应历史详情；export 导出证据包；支持 --json",
				},
			},
		},
	}
}

func (s *DiagTraceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "diagtrace",
		Usage:    "/diagtrace [list|编号|last|history|export] [N] [--json]",
		Examples: []string{"/diagtrace", "/diagtrace list", "/diagtrace 1", "/diagtrace 2 --json", "/diagtrace history 20", "/diagtrace export", "/diagtrace export --json"},
	}
}

func (s *DiagTraceSkill) Validate(params skill.Params) error {
	arg := strings.ToLower(strings.TrimSpace(params.StringOr("args", "")))
	if arg == "" || isTraceLastArg(arg) || isTraceHistoryArg(arg) || isTraceExportArg(arg) || diagtraceNumericArg(arg) > 0 {
		return nil
	}
	return fmt.Errorf("用法: /diagtrace [list|编号|last|history|export] [N] [--json]")
}

func (s *DiagTraceSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	arg := strings.ToLower(strings.TrimSpace(params.StringOr("args", "")))
	if isTraceExportArg(arg) {
		rendered, data := s.exportDiagTrace(arg)
		return &skill.Result{Type: skill.ResultText, Data: data, Rendered: rendered, Summary: "diagnosis trace export"}, nil
	}
	if arg == "" || isTraceLastArg(arg) {
		rendered := diagtrace.RenderLast()
		if traceJSON(arg) {
			rendered = diagtrace.RenderLastJSON()
		}
		return &skill.Result{Type: skill.ResultText, Rendered: rendered, Summary: "diagnosis trace last"}, nil
	}
	if idx := diagtraceNumericArg(arg); idx > 0 {
		rendered := diagtrace.RenderHistoryDetail(idx)
		if traceJSON(arg) {
			rendered = diagtrace.RenderHistoryDetailJSON(idx)
		}
		return &skill.Result{Type: skill.ResultText, Rendered: rendered, Summary: fmt.Sprintf("diagnosis trace #%d", idx)}, nil
	}
	rendered := diagtrace.RenderHistory(traceHistoryLimit(arg))
	if traceJSON(arg) {
		rendered = diagtrace.RenderHistoryJSON(traceHistoryLimit(arg))
	}
	return &skill.Result{Type: skill.ResultText, Rendered: rendered, Summary: "diagnosis trace history"}, nil
}

func diagtraceCompatibilityNotice(kind string) string {
	return fmt.Sprintf("提示: /trace %s 是诊断链路 trace 的兼容入口，推荐改用 /diagtrace %s。/trace 不带参数仍表示 OS/数据库进程堆栈采集。\n\n", kind, kind)
}

func isTraceLastArg(arg string) bool {
	fields := strings.Fields(arg)
	return len(fields) > 0 && fields[0] == "last"
}

func isTraceHistoryArg(arg string) bool {
	fields := strings.Fields(arg)
	return len(fields) > 0 && (fields[0] == "history" || fields[0] == "list")
}

func isTraceExportArg(arg string) bool {
	fields := strings.Fields(arg)
	return len(fields) > 0 && fields[0] == "export"
}

type diagTraceExportSummary struct {
	Status string `json:"status"`
	File   string `json:"file,omitempty"`
	Format string `json:"format"`
	Events int    `json:"events"`
	Size   int    `json:"size"`
	Error  string `json:"error,omitempty"`
}

func (s *DiagTraceSkill) exportDiagTrace(arg string) (string, diagTraceExportSummary) {
	limit := traceHistoryLimit(arg)
	events := diagtrace.History(limit)
	last, hasLast := diagtrace.Last()
	bundle := map[string]any{
		"version": map[string]any{
			"string":     version.String(),
			"version":    version.Version,
			"commit":     version.GitCommit,
			"build_date": version.BuildDate,
		},
		"model":             s.exportModel(),
		"diagtrace_last":    nil,
		"diagtrace_history": events,
		"modeltest_last":    diagtrace.LastModelTest(),
		"tooltest_last":     diagtrace.LastToolTest(),
		"routetest_last":    diagtrace.LastRouteTest(),
		"runtime": map[string]any{
			"os":          runtime.GOOS,
			"arch":        runtime.GOARCH,
			"time":        time.Now().Format(time.RFC3339),
			"opendb_dir":  config.DefaultOpenDBDir(),
			"history_len": len(events),
		},
	}
	if hasLast {
		bundle["diagtrace_last"] = last
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	summary := diagTraceExportSummary{Status: "ok", Format: "json", Events: len(events), Size: len(data)}
	if err != nil {
		summary.Status = "error"
		summary.Error = err.Error()
		return renderDiagTraceExport(summary), summary
	}
	dir := filepath.Join(config.DefaultOpenDBDir(), "diagtrace", "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		summary.Status = "error"
		summary.Error = err.Error()
		return renderDiagTraceExport(summary), summary
	}
	path := filepath.Join(dir, "dbaa-diagtrace-"+time.Now().Format("20060102-150405")+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		summary.Status = "error"
		summary.Error = err.Error()
		return renderDiagTraceExport(summary), summary
	}
	summary.File = path
	if traceJSON(arg) {
		out, err := json.MarshalIndent(summary, "", "  ")
		if err == nil {
			return string(out), summary
		}
	}
	return renderDiagTraceExport(summary), summary
}

func (s *DiagTraceSkill) exportModel() map[string]any {
	out := map[string]any{}
	if s.modelMgr == nil {
		return out
	}
	p := s.modelMgr.Active()
	if p == nil {
		out["active"] = ""
		out["status"] = "no active model"
		return out
	}
	out["active"] = p.Name
	out["provider"] = p.Provider
	out["vendor"] = p.DisplayVendor()
	out["base_url"] = p.BaseURL
	out["model"] = p.Model
	out["tool_mode"] = p.ToolMode
	out["capability"] = p.Capability
	out["strip_think"] = p.StripThink
	if p.APIKey != "" {
		out["api_key"] = "***"
	}
	return out
}

func renderDiagTraceExport(summary diagTraceExportSummary) string {
	var b strings.Builder
	b.WriteString("DiagTrace Export\n\n")
	writeTraceExportKV(&b, "status", summary.Status)
	if summary.File != "" {
		writeTraceExportKV(&b, "file", summary.File)
	}
	writeTraceExportKV(&b, "format", summary.Format)
	writeTraceExportKV(&b, "events", fmt.Sprintf("%d", summary.Events))
	writeTraceExportKV(&b, "size", fmt.Sprintf("%dB", summary.Size))
	if summary.Error != "" {
		writeTraceExportKV(&b, "error", summary.Error)
	}
	if summary.Status == "ok" {
		b.WriteString("\nincluded:\n")
		for _, item := range []string{
			"dbaa/opendb version",
			"active model config with api_key masked",
			"last diagtrace",
			"diagtrace history",
			"recent modeltest/tooltest/routetest results",
			"runtime summary",
		} {
			b.WriteString("- " + item + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeTraceExportKV(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString(key + ": " + value + "\n")
}

func diagtraceNumericArg(arg string) int {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return 0
	}
	if fields[0] == "show" && len(fields) > 1 {
		fields = fields[1:]
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n <= 0 || n > 100 {
		return 0
	}
	return n
}

func traceJSON(arg string) bool {
	for _, f := range strings.Fields(arg) {
		if f == "--json" || f == "json" {
			return true
		}
	}
	return false
}

func traceHistoryLimit(arg string) int {
	fields := strings.Fields(arg)
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || f == "json" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err == nil && n > 0 && n <= 100 {
			return n
		}
	}
	return 10
}

func (s *TraceSkill) formatOutput(result *trace.TraceResult, sources []trace.FuncSource) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OS 堆栈分析 (%s/gaussdb PID %d, %ds, 99Hz)\n\n", s.displayName, result.PID, result.Duration)
	b.WriteString(trace.FormatTopFuncsTable(result.TopFuncs))
	b.WriteString("\n")
	b.WriteString(trace.FormatTextFlame(result.Collapsed, 80))
	fmt.Fprintf(&b, "\n  详细火焰图 (浏览器打开): %s\n", result.SVGPath)
	if len(sources) > 0 {
		b.WriteString("\n  源码片段:\n")
		for _, src := range sources {
			fmt.Fprintf(&b, "\n  ── %s (%s:%d) ──\n%s\n", src.FuncName, src.FilePath, src.Line, src.Snippet)
		}
	}
	return b.String()
}

func (s *TraceSkill) defaultDuration() int {
	if s.traceCfg != nil && s.traceCfg.Duration > 0 {
		return s.traceCfg.Duration
	}
	return 3
}

func (s *TraceSkill) topN() int {
	if s.traceCfg != nil && s.traceCfg.TopN > 0 {
		return s.traceCfg.TopN
	}
	return 20
}

func (s *TraceSkill) outputDir() string {
	if s.traceCfg != nil && s.traceCfg.OutDir != "" {
		return s.traceCfg.OutDir
	}
	// Brand-aware: was hardcoded ~/.opendb/trace; v1.1.20 routes through
	// config.DefaultOpenDBDir() so dbaa writes to ~/.dbaa/trace.
	return filepath.Join(config.DefaultOpenDBDir(), "trace")
}

// lookupSources enriches the hot functions with source context if the user
// configured a local source dir or GitHub repo — Oracle/PG use the same
// pattern. Returns nil when no source config is present.
func (s *TraceSkill) lookupSources(funcs []trace.HotFunc) []trace.FuncSource {
	if s.traceCfg == nil {
		return nil
	}
	src := s.traceCfg.SourceFor(s.dbType)
	if isEmptyTraceSource(src) && s.dbType == "gaussdb" {
		src = s.traceCfg.SourceFor("opengauss")
	}
	if src.Dir != "" {
		lookup := &trace.SourceLookup{SourceDir: src.Dir}
		results, _ := lookup.Grep(funcs)
		if len(results) > 0 {
			return results
		}
	}
	if src.Repo != "" {
		gh := &trace.GitHubLookup{
			Repo:   src.Repo,
			Branch: src.Branch,
			Token:  src.Token,
		}
		results, _ := gh.Grep(funcs)
		return results
	}
	return nil
}

func defaultTraceDisplayName(dbType string) string {
	switch dbType {
	case "gaussdb":
		return "GaussDB"
	case "opengauss":
		return "openGauss"
	default:
		return dbType
	}
}

func isEmptyTraceSource(src config.TraceSourceConfig) bool {
	return src.Dir == "" && src.Repo == ""
}
