/*-------------------------------------------------------------------------
 *
 * diag_skill.go
 *	  DiagProgressFunc is the callback type for streaming progress to
 *	  the REPL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/ai/diag_skill.go
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
	"github.com/sqlrush/opendb/internal/mysql/agent"
	"github.com/sqlrush/opendb/internal/mysql/ruleengine"
	"github.com/sqlrush/opendb/internal/mysql/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// DiagProgressFunc is the callback type for streaming progress to the REPL.
type DiagProgressFunc = func(phase, message string, elapsed time.Duration, result *skill.Result, err error)

// DiagnoseSkill provides MySQL database diagnosis in two tiers:
//   - Rule-based (always available): classification + evidence + remediation
//   - AI-powered (requires LLM): deeper analysis via configured model
type DiagnoseSkill struct {
	modelMgr      *model.Manager
	executor      *skill.Executor
	registry      *skill.Registry
	sentinelSkill *SentinelSkill
	ruleSkill     *RuleSkill
	onProgress    DiagProgressFunc
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

func (s *DiagnoseSkill) Validate(_ skill.Params) error { return nil }

func (s *DiagnoseSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	// "/diag N" - diagnose specific report.
	if idx, err := strconv.Atoi(args); err == nil && idx >= 1 {
		if s.sentinelSkill == nil {
			return &skill.Result{
				Type:     skill.ResultText,
				Data:     "no report",
				Rendered: "暂无异常记录 (Sentinel 未启动).",
				Summary:  "no report",
			}, nil
		}
		return s.executeDiagnose(ctx, idx)
	}

	// "/llm current" — on-demand health check.
	if args == "current" {
		return s.executeOnDemand(ctx, "当前数据库有没有性能问题？请全面检查并给出诊断。")
	}

	// "/llm" or "/llm history" - show history.
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

	// "/diag <question>" - on-demand diagnosis.
	return s.executeOnDemand(ctx, args)
}

func (s *DiagnoseSkill) executeHistory() (*skill.Result, error) {
	reports := s.sentinelSkill.Reports()
	text := sentinel.FormatReportHistory(reports)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     "history",
		Rendered: text,
		Summary:  fmt.Sprintf("%d reports", len(reports)),
	}, nil
}

func (s *DiagnoseSkill) executeDiagnose(ctx context.Context, idx int) (*skill.Result, error) {
	report := s.sentinelSkill.ReportAt(idx)
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

	s.emitProgress("start", fmt.Sprintf("正在诊断异常 #%d...", idx), 0, nil, nil)

	// Build monitoring output.
	monitorOutput := formatMonitorOutput(idx, report)

	// Rule analysis.
	causeStr := report.Classification.Cause.String()
	ruleMsg := fmt.Sprintf("规则分析: %s (%.0f%%)", causeStr, report.Classification.Confidence*100)
	s.emitProgress("rule", ruleMsg, 0, nil, nil)

	// Show record count hint.
	count := s.sentinelSkill.ReportCount()
	if count > 1 {
		monitorOutput += fmt.Sprintf("\n\n  共 %d 条异常记录, /llm 选择分析", count)
	}

	// If LLM is not available, fallback to rule engine.
	provider := s.modelMgr.Provider()
	if provider == nil {
		return s.fallbackRuleEngine(ctx, report, monitorOutput, idx)
	}

	// LLM diagnosis: use auto mode so the LLM can call tools to check current state.
	mode := agent.SelectDiagnoseMode(s.modelMgr.Capability())
	s.emitProgress("ai_start", fmt.Sprintf("AI 深度分析中 (%s, 最多%d轮)...", mode, mode.MaxRounds()), 0, nil, nil)

	// Build four-layer diagnosis prompt focusing on the trigger metric.
	var question string
	if !report.StartTime.IsZero() {
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
	} else {
		question = "请分析并给出诊断建议, 同时调用工具查询当前状态进行对比。"
	}

	diagnoser := agent.NewDiagnoser(provider, s.executor, s.registry)
	if s.sessionStore != nil {
		diagnoser.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
	}
	roundStart := time.Now()
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

	// Use DiagnoseOnDemand with compressed report in question for tool-enabled analysis.
	compressed := agent.CompressReport(*report)
	enrichedQuestion := fmt.Sprintf("%s\n\n%s", compressed, question)
	llmResult, err := diagnoser.DiagnoseOnDemand(ctx, enrichedQuestion)
	if err != nil {
		result := &skill.Result{
			Type:     skill.ResultText,
			Data:     "LLM failed",
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
		Data:     "monitor+AI",
		Rendered: combined,
		Summary:  fmt.Sprintf("monitor+AI #%d: %s, %d rounds", idx, llmResult.Mode, llmResult.RoundsUsed),
	}
	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}

func (s *DiagnoseSkill) executeOnDemand(ctx context.Context, question string) (*skill.Result, error) {
	provider := s.modelMgr.Provider()
	if provider == nil {
		// No LLM: fallback to rule engine with live data.
		return s.fallbackRuleLive(ctx)
	}

	s.emitProgress("start", "主动诊断: 查询当前数据库状态...", 0, nil, nil)
	s.emitProgress("ai_start", "AI 分析中...", 0, nil, nil)

	// Determine if this is a user-specific question or a general health check.
	// "current" callers pass a generic question; direct "/llm <text>" passes user text.
	userProvided := question != "" &&
		question != "当前数据库有没有性能问题？请全面检查并给出诊断。" &&
		question != "请分析当前数据库状态, 检查是否有性能问题"

	var enhancedQuestion string
	if userProvided {
		// User asked a specific question — answer it directly without Sentinel context.
		enhancedQuestion = fmt.Sprintf(`用户问题: %s

请直接回答用户的问题。可以调用工具查询数据库获取所需信息。
不要偏离用户的问题去分析其他告警或等待事件。`, question)
	} else {
		// General health check — include Sentinel context.
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
		enhancedQuestion = fmt.Sprintf(`%s

%s
请主动查询数据库状态来判断是否存在问题。
建议先查询 active_sessions 和 health 获取概览, 再根据发现深入排查。`, question, sentinelPart)
	}

	diagnoser := agent.NewDiagnoser(provider, s.executor, s.registry)
	if s.sessionStore != nil {
		diagnoser.SetContextStoresFrom(s.sessionStore, s.memoryStore, s.policyLoader, s.sessionID)
	}
	roundStart2 := time.Now()
	diagnoser.SetOnRound(func(info agent.RoundInfo) {
		elapsed := time.Since(roundStart2)
		s.emitProgress("ai_round",
			fmt.Sprintf("第%d轮: %s", info.Round, info.Summary),
			elapsed, nil, nil)
		roundStart2 = time.Now()
	})
	diagnoser.SetOnStream(func(delta string) {
		s.emitProgress("ai_streaming", delta, 0, nil, nil)
	})
	llmResult, err := diagnoser.DiagnoseOnDemand(ctx, enhancedQuestion)
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
		Data:     "on-demand AI",
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
			Data:     "monitor only",
			Rendered: monitorOutput,
			Summary:  fmt.Sprintf("monitor #%d", idx),
		}
		s.emitProgress("done", "", 0, result, nil)
		return result, nil
	}

	s.emitProgress("rule_engine", "LLM 未配置, 自动降级到规则引擎...", 0, nil, nil)

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
		Data:     "rule fallback",
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
			Data:     "no llm",
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
			Data:     "rule fallback failed",
			Rendered: "规则引擎实时分析失败: " + err.Error(),
			Summary:  "rule fallback failed",
		}, nil
	}

	s.emitProgress("done", "", 0, result, nil)
	return result, nil
}

// formatMonitorOutput builds the monitoring data display for a report.
func formatMonitorOutput(idx int, report *sentinel.BurstReport) string {
	var b strings.Builder

	if idx > 1 {
		b.WriteString(fmt.Sprintf("── 异常 #%d ──\n\n", idx))
	}

	// Trigger info.
	trigger := report.TriggerEvent
	if trigger.Metric != "" {
		label := sentinel.MetricLabel(sentinel.MetricName(trigger.Metric))
		unit := sentinel.MetricUnit(sentinel.MetricName(trigger.Metric))
		if trigger.Baseline > 0 {
			b.WriteString(fmt.Sprintf("  触发: %s  %.1f -> %.1f%s\n", label, trigger.Baseline, trigger.Current, unit))
		} else {
			b.WriteString(fmt.Sprintf("  当前: %s = %.0f%s\n", label, trigger.Current, unit))
		}
	}
	if report.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("  持续: %.1fs, 峰值活跃: %d\n", report.DurationSec, report.PeakActive))
	}

	// Classification.
	b.WriteString(fmt.Sprintf("\n  根因分类: %s (置信度 %.0f%%)\n",
		report.Classification.Cause.String(),
		report.Classification.Confidence*100))
	for _, ev := range report.Classification.Evidence {
		b.WriteString(fmt.Sprintf("    - %s\n", ev))
	}

	// Top SQL.
	if len(report.TopSQLs) > 0 {
		b.WriteString("\n  Top SQL:\n")
		for i, sq := range report.TopSQLs {
			if i >= 5 {
				break
			}
			text := sq.SQLText
			if len(text) > 60 {
				text = text[:60] + "..."
			}
			b.WriteString(fmt.Sprintf("    %s: avg=%.1fms max=%.1fms exec=%d\n",
				sq.Digest, sq.AvgLatencyMs, sq.MaxLatencyMs, sq.ExecCount))
			if text != "" {
				b.WriteString(fmt.Sprintf("      %s\n", text))
			}
		}
	}

	// Blocking chains.
	if len(report.BlockingChains) > 0 {
		b.WriteString("\n  阻塞链:\n")
		for _, c := range report.BlockingChains {
			b.WriteString(fmt.Sprintf("    Thread %d -> %d 个受害者 (%s)\n",
				c.BlockerThreadID, c.VictimCount, c.WaitType))
		}
	}

	return b.String()
}
