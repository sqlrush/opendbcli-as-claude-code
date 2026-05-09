/*-------------------------------------------------------------------------
 *
 * types.go
 *	  DiagnoseMode represents the three diagnosis modes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/types.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import "strings"

// DiagnoseMode represents the three diagnosis modes.
type DiagnoseMode string

const (
	ModePlaybook DiagnoseMode = "playbook"
	ModeAssist   DiagnoseMode = "assist"
	ModeAuto     DiagnoseMode = "auto"
)

// DefaultMaxRounds is the hard safety cap.
const DefaultMaxRounds = 20

// MaxRounds returns the maximum LLM round-trips for this mode.
func (m DiagnoseMode) MaxRounds() int {
	switch m {
	case ModePlaybook:
		return 1
	default:
		return DefaultMaxRounds
	}
}

// IsValid returns true if the mode is recognized.
func (m DiagnoseMode) IsValid() bool {
	switch m {
	case ModePlaybook, ModeAssist, ModeAuto:
		return true
	default:
		return false
	}
}

// RoundInfo carries per-round metadata for progress display.
type RoundInfo struct {
	Round    int
	Thinking string
	Action   string
	Summary  string
}

// OnRoundFunc is called after each round completes.
type OnRoundFunc func(info RoundInfo)

// OnStreamFunc is called with each text delta during LLM streaming.
type OnStreamFunc func(delta string)

// MaxTurnsNote is appended when the agent loop exceeds maximum turns.
const MaxTurnsNote = "[Note: maximum turns exceeded]"

// IsMaxTurnsExceeded returns true if the text contains the max turns marker.
func IsMaxTurnsExceeded(text string) bool {
	return strings.Contains(text, MaxTurnsNote)
}

// SentinelAlertPrompt wraps a compressed report for LLM analysis.
func SentinelAlertPrompt(compressedReport string) string {
	return "以下是 Sentinel 哨兵模式检测到的性能异常报告。请分析根因并给出建议。\n\n" +
		compressedReport +
		"\n\n请按以下格式回复:\n## 根因分析\n[分析]\n## 紧急措施\n[止血方案]\n## 根因修复\n[长期方案]"
}

// SystemPromptForDiagnose returns the system prompt for the given mode.
// Engine V2 uses its own profile prompt; this is kept for backward compatibility
// with playbook.go, strategy.go, and the standalone AgentLoop/PromptLoop.
func SystemPromptForDiagnose(mode DiagnoseMode) string {
	base := "你是 OpenDB 数据库诊断专家。"
	switch mode {
	case ModePlaybook:
		return base + " 当前模式: playbook。单次分析，不使用工具。输出格式：## 根因分析\n## 紧急措施\n## 根因修复"
	case ModeAssist:
		return base + " 当前模式: assist。限制 3轮 交互，仅使用查询类工具收集信息后给出诊断。"
	case ModeAuto:
		return base + " 当前模式: auto。最多 10轮 自主推理，可使用所有工具。"
	default:
		return base
	}
}

// DiagnoseUserPrompt wraps user input with report context.
func DiagnoseUserPrompt(userInput string, compressedReport string) string {
	if compressedReport == "" {
		return "用户诊断请求: " + userInput + "\n\n请分析当前数据库状态并给出诊断建议。"
	}
	return "用户诊断请求: " + userInput + "\n\n当前异常报告:\n" + compressedReport + "\n\n请分析并给出诊断建议。"
}

