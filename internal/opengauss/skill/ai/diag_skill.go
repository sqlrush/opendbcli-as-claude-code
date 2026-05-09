/*-------------------------------------------------------------------------
 *
 * diag_skill.go
 *	  DiagnoseSkill provides OpenGauss database diagnosis in two tiers:
 *	  - Rule-based (always available): classification + evidence +
 *	  remediation - AI-powered (requires LLM): deeper analysis via LLM
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/ai/diag_skill.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/engine/policy"
	"github.com/sqlrush/opendb/internal/engine/session"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/opengauss/agent"
	"github.com/sqlrush/opendb/internal/opengauss/ruleengine"
	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// DiagnoseSkill provides OpenGauss database diagnosis in two tiers:
//   - Rule-based (always available): classification + evidence + remediation
//   - AI-powered (requires LLM): deeper analysis via LLM
type DiagnoseSkill struct {
	modelMgr      *model.Manager
	executor      *skill.Executor
	registry      *skill.Registry
	sentinelSkill *SentinelSkill
	ruleSkill     *RuleSkill
	onProgress    agent.DiagProgressFunc
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
	// Resume the most recent active session for this instance rather than
	// minting a brand-new ID — keeps a continuous conversation across
	// multiple /llm invocations, including batch-mode runs where the
	// process exits between calls. See session.ResumeOrNew for the 24h
	// freshness cap.
	s.sessionID = session.ResumeOrNew(context.Background(), s.sessionStore, instance)
}

func (s *DiagnoseSkill) NewSession(instance string) {
	if s.memoryStore != nil {
		s.memoryStore.SetActiveInstance(instance)
	}
	s.sessionID = session.NewSessionID(instance)
}

// NewDiagnoseSkill creates a DiagnoseSkill. ruleSkill provides the fallback
// rule engine when LLM is not available.
func NewDiagnoseSkill(
	modelMgr *model.Manager,
	executor *skill.Executor,
	registry *skill.Registry,
	sentinelSkill *SentinelSkill,
	ruleSkill *RuleSkill,
) *DiagnoseSkill {
	return &DiagnoseSkill{
		modelMgr:      modelMgr,
		executor:      executor,
		registry:      registry,
		sentinelSkill: sentinelSkill,
		ruleSkill:     ruleSkill,
	}
}

// SetOnProgress sets the progress callback for async REPL display.
func (s *DiagnoseSkill) SetOnProgress(fn func(phase, message string, elapsed time.Duration, result *skill.Result, err error)) {
	s.onProgress = fn
}

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
func (s *DiagnoseSkill) Description() string                { return "OpenGauss diagnosis (rule-based + optional AI)" }
func (s *DiagnoseSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *DiagnoseSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "diag",
		Description: "OpenGauss diagnosis (rule-based + optional AI)",
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
		Aliases: []string{},
		Usage:   "/llm",
		Examples: []string{"/llm"},
	}
}

func (s *DiagnoseSkill) Validate(params skill.Params) error {
	modeStr := params.StringOr("mode", "")
	if modeStr == "" {
		return nil
	}
	mode := agent.DiagnoseMode(modeStr)
	if !mode.IsValid() {
		return fmt.Errorf("invalid mode %q, use: playbook, assist, auto", mode)
	}
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
				Rendered: "暂无异常记录 (Sentinel 未启动).",
				Summary:  "no report",
			}, nil
		}
		return s.executeDiagnose(ctx, params, idx)
	}

	// "/llm current" — on-demand health check.
	if args == "current" {
		params = params.With("question", "当前数据库有没有性能问题？请全面检查并给出诊断。")
		return s.executeOnDemand(ctx, params)
	}

	// "/llm" or "/llm history" — show history list.
	if args == "" || args == "history" || args == "hist" {
		if s.sentinelSkill == nil {
			return &skill.Result{
				Type:     skill.ResultText,
				Rendered: "暂无异常记录 (Sentinel 未启动).",
				Summary:  "no history",
			}, nil
		}
		return s.executeHistory()
	}

	// "/diag <question>" — on-demand LLM diagnosis.
	return s.executeOnDemand(ctx, params)
}

// executeHistory returns a summary of all stored reports.
func (s *DiagnoseSkill) executeHistory() (*skill.Result, error) {
	reports := s.sentinelSkill.Reports()
	text := sentinel.FormatReportHistory(reports)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: text,
		Summary:  fmt.Sprintf("%d reports", len(reports)),
	}, nil
}

// executeDiagnose diagnoses a specific report.
func (s *DiagnoseSkill) executeDiagnose(ctx context.Context, params skill.Params, idx int) (*skill.Result, error) {
	modeStr := params.StringOr("mode", "")

	report := s.sentinelSkill.ReportAt(idx)
	if report == nil {
		count := s.sentinelSkill.ReportCount()
		if count == 0 {
			return &skill.Result{
				Type:     skill.ResultText,
				Rendered: "暂无异常数据.\n  Sentinel 未检测到异常.\n  使用 /sentinel status 查看哨兵状态.",
				Summary:  "no report",
			}, nil
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("编号 %d 超出范围, 当前共 %d 条记录.\n  使用 /llm history 查看所有记录.", idx, count),
			Summary:  "out of range",
		}, nil
	}

	// Emit start progress.
	s.emitProgress("start", fmt.Sprintf("正在诊断异常 #%d...", idx), 0, nil, nil)

	// Monitoring data (rule-based).
	monitorOutput := sentinel.FormatRuleDiagnosis(*report)

	// Emit rule analysis progress.
	causeStr := report.Classification.Cause.String()
	ruleMsg := fmt.Sprintf("规则分析: %s (%.0f%%)", causeStr, report.Classification.Confidence*100)
	s.emitProgress("rule", ruleMsg, 0, nil, nil)

	// Record count hint.
	count := s.sentinelSkill.ReportCount()
	if count > 1 {
		monitorOutput += fmt.Sprintf("\n\n  共 %d 条异常记录, /llm 选择分析", count)
	}

	// If LLM not configured, fallback to rule engine.
	provider := s.modelMgr.Provider()
	if provider == nil {
		return s.fallbackRuleEngine(ctx, report, monitorOutput, idx)
	}

	// Build a focused diagnosis prompt — guide the model to the trigger
	// metric without rigid section enforcement; let the model organize.
	var question string
	if !report.StartTime.IsZero() {
		triggerDesc := report.TriggerEvent.Metric
		if label := sentinel.TriggerMetricLabel(report.TriggerEvent.Metric); label != "" {
			triggerDesc = label
		}
		question = fmt.Sprintf(`以上是 Sentinel 在 %s 捕获的异常报告。
触发告警指标: %s (基线 %.0f → 当前 %.0f, %.1f倍)

请分析:
1. %s 冲高的根因（用工具查证据，给可执行的原生 SQL 修复方案）
2. 报告中其他问题与主线的关联性
3. 当前数据库状态（用工具查）vs burst 时的变化
4. 综合评估和优先级排序`,
			report.StartTime.Format("2006-01-02 15:04:05"),
			triggerDesc, report.TriggerEvent.Baseline, report.TriggerEvent.Current, report.TriggerEvent.Multiplier,
			triggerDesc)
	} else {
		question = "请分析并给出诊断建议，同时调用工具查询当前状态进行对比。"
	}

	roundStart := time.Now()
	onRound := func(info agent.RoundInfo) {
		elapsed := time.Since(roundStart)
		s.emitProgress("ai_round",
			fmt.Sprintf("第%d轮: %s", info.Round, info.Summary),
			elapsed, nil, nil)
		roundStart = time.Now()
	}
	onStream := func(delta string) {
		s.emitProgress("ai_streaming", delta, 0, nil, nil)
	}

	// Strategy selection mirrors Oracle: large model → autonomous w/ guided
	// fallback; small model → guided. When the user explicitly passed a mode
	// via /llm <mode>, honor it directly (back-compat).
	var (
		llmResult agent.DiagnoseResult
		err       error
	)
	if modeStr != "" {
		mode := agent.DiagnoseMode(modeStr)
		s.emitProgress("ai_start", fmt.Sprintf("AI 深度分析中 (%s, 最多%d轮)...", mode, mode.MaxRounds()), 0, nil, nil)
		diagnoser := agent.NewDiagnoser(provider, s.executor, s.registry)
		if s.sessionStore != nil {
			diagnoser.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
		}
		diagnoser.SetCapability(s.modelMgr.Capability())
		diagnoser.SetOnRound(onRound)
		diagnoser.SetOnStream(onStream)
		llmResult, err = diagnoser.Diagnose(ctx, mode, report, question)
	} else {
		capability := agent.ModelCapability(s.modelMgr.Capability())
		s.emitProgress("ai_start", fmt.Sprintf("AI 分析 (auto, 最多%d轮)...", agent.ModeAuto.MaxRounds()), 0, nil, nil)
		strategy := agent.SelectStrategy(capability, provider, s.executor, s.registry)
		// A throwaway diagnoser carries the stores we want to propagate.
		var src *agent.Diagnoser
		if s.sessionStore != nil {
			src = agent.NewDiagnoser(provider, s.executor, s.registry)
			src.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
		}
		if as, ok := strategy.(*agent.AutonomousStrategy); ok {
			if src != nil {
				as.SetContextStoresFrom(src)
			}
			as.SetOnRound(onRound)
			as.SetOnStream(onStream)
		} else if gs, ok := strategy.(*agent.GuidedStrategy); ok {
			if src != nil {
				gs.SetContextStoresFrom(src)
			}
			gs.SetOnRound(onRound)
			gs.SetOnStream(onStream)
		}
		llmResult, err = strategy.Diagnose(ctx, report, question)
	}
	if err != nil {
		result := &skill.Result{
			Type:     skill.ResultText,
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
		Rendered: combined,
		Summary:  fmt.Sprintf("monitor+AI #%d: %s, %d rounds", idx, llmResult.Mode, llmResult.RoundsUsed),
	}
	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}

// executeOnDemand runs AI diagnosis without a Sentinel report.
func (s *DiagnoseSkill) executeOnDemand(ctx context.Context, params skill.Params) (*skill.Result, error) {
	provider := s.modelMgr.Provider()
	if provider == nil {
		// No LLM: fallback to rule engine with live data.
		return s.fallbackRuleLive(ctx)
	}

	question := strings.TrimSpace(params.StringOr("args", ""))
	userProvided := question != ""
	if question == "" {
		question = "请分析当前 OpenGauss 数据库状态, 检查是否有性能问题"
	}

	s.emitProgress("start", "主动诊断: 查询当前数据库状态...", 0, nil, nil)

	capability := agent.ModelCapability(s.modelMgr.Capability())
	s.emitProgress("ai_start", fmt.Sprintf("AI 分析 (auto, 最多%d轮)...", agent.ModeAuto.MaxRounds()), 0, nil, nil)

	roundStart2 := time.Now()
	onRound := func(info agent.RoundInfo) {
		elapsed := time.Since(roundStart2)
		s.emitProgress("ai_round",
			fmt.Sprintf("第%d轮: %s", info.Round, info.Summary),
			elapsed, nil, nil)
		roundStart2 = time.Now()
	}
	onStream := func(delta string) {
		s.emitProgress("ai_streaming", delta, 0, nil, nil)
	}

	var prompt string
	if userProvided {
		prompt = fmt.Sprintf(`用户问题: %s

请直接回答用户的问题。可以调用工具查询数据库获取所需信息。
不要偏离用户的问题去分析其他告警或等待事件。`, question)
	} else {
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
请主动查询数据库状态来判断是否存在问题。`, question, sentinelPart)
	}

	// Strategy + fallback (mirrors Oracle).
	strategy := agent.SelectStrategy(capability, provider, s.executor, s.registry)
	var src *agent.Diagnoser
	if s.sessionStore != nil {
		src = agent.NewDiagnoser(provider, s.executor, s.registry)
		src.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
	}
	if as, ok := strategy.(*agent.AutonomousStrategy); ok {
		if src != nil {
			as.SetContextStoresFrom(src)
		}
		as.SetOnRound(onRound)
		as.SetOnStream(onStream)
	} else if gs, ok := strategy.(*agent.GuidedStrategy); ok {
		if src != nil {
			gs.SetContextStoresFrom(src)
		}
		gs.SetOnRound(onRound)
		gs.SetOnStream(onStream)
	}
	llmResult, err := strategy.Diagnose(ctx, nil, prompt)
	if err != nil {
		result := &skill.Result{
			Type:     skill.ResultText,
			Rendered: "AI 诊断失败: " + err.Error(),
			Summary:  "on-demand failed",
		}
		s.emitProgress("error", "", 0, nil, err)
		return result, nil
	}

	output := agent.FormatDiagnoseResult(llmResult)
	result := &skill.Result{
		Type:     skill.ResultText,
		Rendered: output,
		Summary:  fmt.Sprintf("on-demand AI: %d rounds", llmResult.RoundsUsed),
	}
	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}

// fallbackRuleEngine runs the rule engine on a sentinel report when LLM is unavailable.
func (s *DiagnoseSkill) fallbackRuleEngine(ctx context.Context, report *sentinel.BurstReport, monitorOutput string, idx int) (*skill.Result, error) {
	if s.ruleSkill == nil || s.ruleSkill.engine == nil {
		result := &skill.Result{
			Type:     skill.ResultText,
			Rendered: monitorOutput,
			Summary:  fmt.Sprintf("monitor #%d", idx),
		}
		s.emitProgress("done", "", 0, result, nil)
		return result, nil
	}

	s.emitProgress("rule_engine", "LLM 未配置, 自动降级到规则引擎...", 0, nil, nil)

	// Classify for OG (sentinel-level classification).
	classification := sentinel.Classify(*report)
	report.Classification = classification

	input := &ruleengine.DiagInput{
		Type:   ruleengine.InputBurstReport,
		Report: report,
	}
	output := s.ruleSkill.engine.Diagnose(input)

	ruleOutput := ruleengine.FormatDiagOutput(output, ruleengine.Config{
		OutputMode: ruleengine.OutputSkill,
	})

	combined := monitorOutput + "\n" + ruleOutput

	summary := fmt.Sprintf("规则诊断 #%d", idx)
	if output.Primary != nil {
		summary = fmt.Sprintf("规则诊断 #%d: %s (%d%%)",
			idx, output.Primary.Cause, int(output.Primary.Confidence*100))
	}

	result := &skill.Result{
		Type:     skill.ResultText,
		Rendered: combined,
		Summary:  summary,
	}
	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}

// fallbackRuleLive runs the rule engine on live data when LLM is unavailable
// and no sentinel reports exist.
func (s *DiagnoseSkill) fallbackRuleLive(ctx context.Context) (*skill.Result, error) {
	if s.ruleSkill == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "暂无异常数据, 且未配置 LLM.\n  使用 /model <name> 激活模型后可直接用 /llm 主动诊断当前数据库状态.",
			Summary:  "no report, no llm",
		}, nil
	}

	s.emitProgress("start", "LLM 未配置, 自动降级到规则引擎 (实时采集)...", 0, nil, nil)

	// Delegate to rule skill's Execute with "live" arg.
	params := skill.ParamsFromMap(map[string]any{"args": "live"})
	result, err := s.ruleSkill.Execute(ctx, params)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "规则引擎实时分析失败: " + err.Error(),
			Summary:  "rule fallback failed",
		}, nil
	}

	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}
