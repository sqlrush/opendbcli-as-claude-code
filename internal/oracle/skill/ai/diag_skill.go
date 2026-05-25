/*-------------------------------------------------------------------------
 *
 * diag_skill.go
 *	  DiagnoseHistoryEntry is one entry in the diagnosis history.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/ai/diag_skill.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/engine/policy"
	"github.com/sqlrush/opendb/internal/engine/session"
	"github.com/sqlrush/opendb/internal/oracle/agent"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// DiagnoseHistoryEntry is one entry in the diagnosis history.
type DiagnoseHistoryEntry struct {
	Index       int     `json:"index"`
	Timestamp   string  `json:"timestamp,omitempty"`
	Metric      string  `json:"metric,omitempty"`
	MetricLabel string  `json:"metric_label,omitempty"`
	Baseline    float64 `json:"baseline"`
	Current     float64 `json:"current"`
	Threshold   float64 `json:"threshold"`
	DurationSec float64 `json:"duration_sec"`
	PeakActive  int     `json:"peak_active"`
}

// DiagnoseHistoryData is the structured output for /diag history.
type DiagnoseHistoryData struct {
	Reports []DiagnoseHistoryEntry `json:"reports"`
}

// DiagnoseReportData is the structured output for /diag (single report).
type DiagnoseReportData struct {
	Index          int                   `json:"index"`
	Report         *sentinel.BurstReport `json:"report"`
	HasLLMAnalysis bool                  `json:"has_llm_analysis"`
}

// DiagProgressFunc is the callback type for streaming progress to the REPL.
// The REPL sets this before async dispatch; the function writes to a channel.
// Defined as a type alias so the ui package interface can match by signature.
type DiagProgressFunc = func(phase, message string, elapsed time.Duration, result *skill.Result, err error)

// DiagnoseSkill provides database diagnosis in two tiers:
//   - Rule-based (always available): classification + evidence + remediation SQL
//   - AI-powered (requires LLM): deeper analysis via Ollama/qwen
type DiagnoseSkill struct {
	modelMgr      *model.Manager          // runtime model manager (hot-swap)
	executor      *skill.Executor
	registry      *skill.Registry
	sentinelSkill *SentinelSkill          // to access last report
	onProgress    DiagProgressFunc        // set by REPL for async progress
	sessionStore  session.SessionStore
	memoryStore   *memory.Store
	policyLoader  *policy.Loader
	sessionID     session.SessionID
}

func (s *DiagnoseSkill) SetContextStores(baseDir, instance string) {
	s.sessionStore = session.NewFileSessionStore(baseDir + "/sessions")
	s.memoryStore = memory.NewStore(baseDir + "/memory")
	s.memoryStore.SetActiveInstance(instance)
	s.policyLoader = policy.NewLoader(baseDir + "/policies")
	s.sessionID = session.NewSessionID(instance)
}

func (s *DiagnoseSkill) NewSession(instance string) {
	if s.memoryStore != nil {
		s.memoryStore.SetActiveInstance(instance)
	}
	s.sessionID = session.NewSessionID(instance)
}

// NewDiagnoseSkill creates a DiagnoseSkill. modelMgr provides the active LLM
// provider at execution time (supports hot-swap via /model). If modelMgr is nil
// or has no active model, operates in rule-based mode only.
func NewDiagnoseSkill(
	modelMgr *model.Manager,
	executor *skill.Executor,
	registry *skill.Registry,
	sentinelSkill *SentinelSkill,
) *DiagnoseSkill {
	return &DiagnoseSkill{
		modelMgr:      modelMgr,
		executor:      executor,
		registry:      registry,
		sentinelSkill: sentinelSkill,
	}
}

// SetOnProgress sets the progress callback for async REPL display.
func (s *DiagnoseSkill) SetOnProgress(fn DiagProgressFunc) { s.onProgress = fn }

// ClearOnProgress removes the progress callback.
func (s *DiagnoseSkill) ClearOnProgress() { s.onProgress = nil }

// HasLLM returns true if an LLM provider is currently active.
func (s *DiagnoseSkill) HasLLM() bool { return s.modelMgr != nil && s.modelMgr.Provider() != nil }

// emitProgress sends a progress event if a callback is set.
func (s *DiagnoseSkill) emitProgress(phase, message string, elapsed time.Duration, result *skill.Result, err error) {
	if s.onProgress != nil {
		s.onProgress(phase, message, elapsed, result, err)
	}
}

func (s *DiagnoseSkill) Name() string                       { return "llm" }
func (s *DiagnoseSkill) Description() string                { return "Database diagnosis (rule-based + optional AI)" }
func (s *DiagnoseSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *DiagnoseSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "diag",
		Description: "Database diagnosis (rule-based + optional AI)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode": map[string]any{
					"type":        "string",
					"description": "Diagnosis mode: playbook (1 call), assist (max 3 rounds), auto (max 10 rounds)",
					"enum":        []string{"playbook", "assist", "auto"},
				},
				"question": map[string]any{
					"type":        "string",
					"description": "What to diagnose",
				},
			},
		},
	}
}

func (s *DiagnoseSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "llm",
		Aliases: []string{"diag", "diagnose"},
		Usage:   "/llm [question]",
		Examples: []string{"/llm 数据库很慢"},
	}
}

func (s *DiagnoseSkill) Validate(params skill.Params) error {
	modeStr := params.StringOr("mode", "")
	if modeStr == "" {
		// Strategy auto-selects mode based on model capability.
		return nil
	}
	mode := agent.DiagnoseMode(modeStr)
	if !mode.IsValid() {
		return fmt.Errorf("invalid mode %q, use: playbook, assist, auto", mode)
	}
	// assist/auto modes require LLM.
	if !s.HasLLM() && mode != agent.ModePlaybook {
		return fmt.Errorf("模式 %q 需要配置 LLM. 请用 /model <name> 激活模型, 或使用 /llm (默认规则诊断)", mode)
	}
	return nil
}

func (s *DiagnoseSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	// "/diag N" — diagnose specific report by 1-based index.
	if idx, err := strconv.Atoi(args); err == nil && idx >= 1 {
		if s.sentinelSkill == nil {
			return &skill.Result{
				Type:     skill.ResultText,
				Data:     "no report",
				Rendered: "暂无异常记录 (Sentinel 未启动).",
				Summary:  "no report",
			}, nil
		}
		return s.executeDiagnose(ctx, params, idx)
	}

	// "/llm current" — on-demand health check (no specific alert).
	if args == "current" {
		params = params.With("question", "当前数据库有没有性能问题？请全面检查并给出诊断。")
		return s.executeOnDemand(ctx, params)
	}

	// "/llm" or "/llm history" — show history list for user to pick.
	if args == "" || args == "history" || args == "hist" {
		if s.sentinelSkill == nil {
			return &skill.Result{
				Type:     skill.ResultText,
				Data:     "no history",
				Rendered: "暂无异常记录 (Sentinel 未启动).",
				Summary:  "no history",
			}, nil
		}
		return s.executeHistory()
	}

	// "/diag <question>" — on-demand LLM diagnosis with a specific question.
	return s.executeOnDemand(ctx, params)
}

// executeHistory returns a summary of all stored reports.
func (s *DiagnoseSkill) executeHistory() (*skill.Result, error) {
	reports := s.sentinelSkill.Reports()
	text := sentinel.FormatReportHistory(reports)

	// Build structured data.
	var histData DiagnoseHistoryData
	for i := 0; i < len(reports); i++ {
		r := reports[len(reports)-1-i] // newest first
		entry := DiagnoseHistoryEntry{
			Index:       i + 1,
			DurationSec: r.DurationSec,
			PeakActive:  r.PeakActive,
		}
		if !r.StartTime.IsZero() {
			entry.Timestamp = r.StartTime.Format("01-02 15:04:05")
		}
		if r.TriggerEvent.Metric != "" {
			entry.Metric = r.TriggerEvent.Metric
			entry.MetricLabel = sentinel.TriggerMetricLabel(r.TriggerEvent.Metric)
			entry.Baseline = r.TriggerEvent.Baseline
			entry.Current = r.TriggerEvent.Current
			entry.Threshold = r.TriggerEvent.Threshold
		}
		histData.Reports = append(histData.Reports, entry)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &histData,
		Rendered: text,
		Summary:  fmt.Sprintf("%d reports", len(reports)),
	}, nil
}

// executeDiagnose diagnoses a specific report by 1-based index (1=newest).
func (s *DiagnoseSkill) executeDiagnose(ctx context.Context, params skill.Params, idx int) (*skill.Result, error) {
	modeStr := params.StringOr("mode", "")
	args := strings.TrimSpace(params.StringOr("args", ""))

	// Determine question: skip if args is a number (report index).
	question := params.StringOr("question", "数据库性能诊断")
	if _, err := strconv.Atoi(args); err != nil && args != "" {
		question = args
	}

	report := s.sentinelSkill.ReportAt(idx)

	// Build four-layer diagnosis prompt focusing on the trigger metric.
	if report != nil && !report.StartTime.IsZero() {
		triggerDesc := report.TriggerEvent.Metric
		if label := sentinel.TriggerMetricLabel(report.TriggerEvent.Metric); label != "" {
			triggerDesc = label
		}
		question = fmt.Sprintf(`以上是 Sentinel 在 %s 捕获的异常报告。
**触发告警的指标是: %s (基线 %.0f → 当前 %.0f, %.1f倍)**

请严格按以下四层结构分析并输出:

## 一、触发告警分析
重点分析 "%s" 为什么冲高。给出根因和针对性修复方案（可直接执行的原生 SQL）。

## 二、关联问题
报告中的其他问题（等待事件、Top SQL、阻塞链等）与 "%s" 冲高是同一根因的不同表现, 还是独立问题? 逐一说明关联性。

## 三、当前状态对比
调用工具查询当前数据库状态, 判断:
- "%s" 冲高是否仍在持续?
- 当前整体状况与 burst 时有何变化?

## 四、综合评估与优先级
基于以上分析, 给出当前所有问题的优先级排名:
- 每个问题标明严重程度或占比
- 如果 "%s" 冲高已恢复正常, 明确说明"当前无需关注"
- 总结: 先处理什么, 后处理什么`,
			report.StartTime.Format("2006-01-02 15:04:05"),
			triggerDesc, report.TriggerEvent.Baseline, report.TriggerEvent.Current, report.TriggerEvent.Multiplier,
			triggerDesc, triggerDesc, triggerDesc, triggerDesc)
	}
	if report == nil {
		count := s.sentinelSkill.ReportCount()
		if count == 0 {
			return &skill.Result{
				Type:     skill.ResultText,
				Data:     "no report",
				Rendered: "暂无异常数据.\n  Sentinel 未检测到异常, 或尚未触发 burst 采集.\n  使用 /sentinel status 查看哨兵状态.",
				Summary:  "no report",
			}, nil
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "out of range",
			Rendered: fmt.Sprintf("编号 %d 超出范围, 当前共 %d 条记录.\n  使用 /llm history 查看所有记录.", idx, count),
			Summary:  "out of range",
		}, nil
	}

	// Emit start progress.
	s.emitProgress("start", fmt.Sprintf("正在诊断异常 #%d...", idx), 0, nil, nil)

	// Header for non-latest reports.
	prefix := ""
	if idx > 1 {
		prefix = fmt.Sprintf("── 异常 #%d ──\n\n", idx)
	}

	// Monitoring data: only facts, no judgment.
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		termWidth = w
	}
	monitorOutput := prefix + sentinel.FormatRuleDiagnosisWidth(*report, termWidth)

	// Emit rule analysis progress.
	causeStr := report.Classification.Cause.String()
	ruleMsg := fmt.Sprintf("规则分析: %s (%.0f%%)", causeStr, report.Classification.Confidence*100)
	if report.TriggerEvent.Metric != "" {
		label := sentinel.TriggerMetricLabel(report.TriggerEvent.Metric)
		ruleMsg = fmt.Sprintf("规则分析: %s (基线 %.0f → 当前 %.0f)",
			label, report.TriggerEvent.Baseline, report.TriggerEvent.Current)
	}
	s.emitProgress("rule", ruleMsg, 0, nil, nil)

	// Show record count hint.
	count := s.sentinelSkill.ReportCount()
	if count > 1 {
		monitorOutput += fmt.Sprintf("\n\n  共 %d 条异常记录, /llm 选择分析", count)
	}

	// Snapshot the current provider/capability (may change via /model).
	provider := s.modelMgr.Provider()
	capability := agent.ModelCapability(s.modelMgr.Capability())

	// If LLM is not configured, return monitoring data only.
	if provider == nil {
		result := &skill.Result{
			Type:     skill.ResultText,
			Data:     &DiagnoseReportData{Index: idx, Report: report, HasLLMAnalysis: false},
			Rendered: monitorOutput,
			Summary:  fmt.Sprintf("monitor #%d", idx),
		}
		s.emitProgress("done", "", 0, result, nil)
		return result, nil
	}

	// LLM configured: monitoring data + AI diagnosis.
	var llmResult agent.DiagnoseResult
	var err error

	// Emit AI start progress.
	aiMode := "auto"
	maxRounds := agent.ModeAuto.MaxRounds()
	if modeStr != "" {
		aiMode = modeStr
		maxRounds = agent.DiagnoseMode(modeStr).MaxRounds()
	}
	s.emitProgress("ai_start", fmt.Sprintf("AI 深度分析中 (%s, 最多%d轮)...", aiMode, maxRounds), 0, nil, nil)

	// Wire OnRound callback for per-round progress.
	roundStart := time.Now()
	onRound := func(info agent.RoundInfo) {
		elapsed := time.Since(roundStart)
		s.emitProgress("ai_round",
			fmt.Sprintf("第%d轮: %s", info.Round, info.Summary),
			elapsed, nil, nil)
		roundStart = time.Now()
	}

	// Wire streaming callback for character-by-character output.
	onStream := func(delta string) {
		s.emitProgress("ai_streaming", delta, 0, nil, nil)
	}

	if modeStr != "" {
		diagnoser := agent.NewDiagnoser(provider, s.executor, s.registry)
		if s.sessionStore != nil {
			diagnoser.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
		}
		diagnoser.SetCapability(s.modelMgr.Capability())
		diagnoser.SetOnRound(onRound)
		diagnoser.SetOnStream(onStream)
		llmResult, err = diagnoser.Diagnose(ctx, agent.DiagnoseMode(modeStr), report, question)
	} else {
		strategy := agent.SelectStrategy(capability, provider, s.executor, s.registry)
		// Wire onRound and onStream through strategy's diagnoser.
		if gs, ok := strategy.(*agent.GuidedStrategy); ok {
			gs.SetOnRound(onRound)
			gs.SetOnStream(onStream)
		} else if as, ok := strategy.(*agent.AutonomousStrategy); ok {
			as.SetOnRound(onRound)
			as.SetOnStream(onStream)
		}
		llmResult, err = strategy.Diagnose(ctx, report, question)
	}

	if err != nil {
		// LLM failed — skip the verbose monitoring panel, just report the error.
		// Users can use /rule for rule-based diagnosis without LLM.
		result := &skill.Result{
			Type:     skill.ResultText,
			Data:     &DiagnoseReportData{Index: idx, Report: report, HasLLMAnalysis: false},
			Rendered: "⚠ AI 分析失败: " + err.Error(),
			Summary:  "LLM failed",
		}
		s.emitProgress("error", "", 0, result, err)
		return result, nil
	}

	llmOutput := agent.FormatDiagnoseResult(llmResult)
	combined := monitorOutput + "\n\n" + llmOutput

	result := &skill.Result{
		Type:     skill.ResultText,
		Data:     &DiagnoseReportData{Index: idx, Report: report, HasLLMAnalysis: true},
		Rendered: combined,
		Summary:  fmt.Sprintf("monitor+AI #%d: %s, %d rounds", idx, llmResult.Mode, llmResult.RoundsUsed),
	}
	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}

// executeOnDemand runs AI diagnosis without a Sentinel report.
// The LLM queries live data via skills to assess current database health.
func (s *DiagnoseSkill) executeOnDemand(ctx context.Context, params skill.Params) (*skill.Result, error) {
	provider := s.modelMgr.Provider()
	if provider == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "no llm",
			Rendered: "暂无异常数据, 且未配置 LLM.\n  使用 /model <name> 激活模型后可直接用 /llm 主动诊断当前数据库状态.",
			Summary:  "no report, no llm",
		}, nil
	}

	question := strings.TrimSpace(params.StringOr("args", ""))
	userProvided := question != ""
	if question == "" {
		question = "请分析当前数据库状态, 检查是否有性能问题"
	}

	s.emitProgress("start", "主动诊断: 查询当前数据库状态...", 0, nil, nil)
	s.emitProgress("ai_start", fmt.Sprintf("AI 分析 (auto, 最多%d轮)...", agent.ModeAuto.MaxRounds()), 0, nil, nil)

	var prompt string
	if userProvided {
		// User asked a specific question — answer it directly without Sentinel context.
		prompt = fmt.Sprintf(`用户问题: %s

请直接回答用户的问题。可以调用工具查询数据库获取所需信息。
不要偏离用户的问题去分析其他告警或等待事件。`, question)
	} else {
		// No specific question — include Sentinel context for proactive health check.
		var sentinelPart string
		if s.sentinelSkill != nil && s.sentinelSkill.ReportCount() > 0 {
			latestReport := s.sentinelSkill.ReportAt(1)
			if latestReport != nil {
				sentinelPart = fmt.Sprintf("Sentinel 最近检测到异常:\n%s\n请结合此异常信息进行分析。",
					agent.CompressReport(*latestReport))
			}
		}
		if sentinelPart == "" {
			sentinelPart = "当前没有 Sentinel 异常报告。"
		}
		prompt = fmt.Sprintf(`用户请求数据库诊断: %s

%s
请主动查询数据库状态来判断是否存在问题。
建议先查询 active_sessions 和 health 获取概览, 再根据发现深入排查。`, question, sentinelPart)
	}

	roundStart := time.Now()
	diagnoser := agent.NewDiagnoser(provider, s.executor, s.registry)
	if s.sessionStore != nil {
		diagnoser.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
	}
	diagnoser.SetCapability(s.modelMgr.Capability())
	diagnoser.SetOnRound(func(info agent.RoundInfo) {
		elapsed := time.Since(roundStart)
		s.emitProgress("ai_round",
			fmt.Sprintf("第%d轮: %s", info.Round, info.Summary),
			elapsed, nil, nil)
		roundStart = time.Now()
	})
	diagnoser.SetOnStream(func(delta string) {
		s.emitProgress("ai_streaming", delta, 0, nil, nil)
	})
	llmResult, err := diagnoser.Diagnose(ctx, agent.ModeAuto, nil, prompt)
	if err != nil {
		result := &skill.Result{
			Type:     skill.ResultText,
			Data:     "llm failed",
			Rendered: "AI 诊断失败: " + err.Error(),
			Summary:  "on-demand failed",
		}
		s.emitProgress("error", "", 0, nil, err)
		return result, nil
	}

	output := agent.FormatDiagnoseResult(llmResult)

	result := &skill.Result{
		Type:     skill.ResultText,
		Data:     &DiagnoseReportData{HasLLMAnalysis: true},
		Rendered: output,
		Summary:  fmt.Sprintf("on-demand AI: %d rounds", llmResult.RoundsUsed),
	}
	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}
