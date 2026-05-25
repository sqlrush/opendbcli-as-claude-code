/*-------------------------------------------------------------------------
 *
 * trigger.go
 *	  ShouldTriggerMemoryRound checks whether a memory-write extra round
 *	  should be triggered after the main diagnosis loop. Any single
 *	  condition match is sufficient. This is the fallback path for less
 *	  capable LLMs that don't proactively write memories during
 *	  diagnosis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/trigger.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import "strings"

// ShouldTriggerMemoryRound checks whether a memory-write extra round should
// be triggered after the main diagnosis loop. Any single condition match
// is sufficient. This is the fallback path for less capable LLMs that
// don't proactively write memories during diagnosis.
func ShouldTriggerMemoryRound(
	userMessages []string, // user-role message contents
	turnCount int,
	toolsCalled []string,
	mode string, // "assist" / "auto" / "playbook"
) bool {
	// Condition 1: User messages contain problem keywords
	if matchAnyKeyword(userMessages, problemKeywords) {
		return true
	}

	// Condition 2: User invoked LLM diagnosis (/llm command, assist or auto mode)
	if mode == "assist" || mode == "auto" {
		return true
	}

	// Condition 3: Diagnosis took >= 5 turns (simple queries finish in 1-2)
	if turnCount >= 5 {
		return true
	}

	// Condition 4: Diagnostic tools were called
	if matchAnyTool(toolsCalled, diagnosticTools) {
		return true
	}

	// Condition 5: User expressed preference signals
	if matchAnyKeyword(userMessages, preferenceKeywords) {
		return true
	}

	// Condition 6: User provided workload background info
	if matchAnyKeyword(userMessages, workloadKeywords) {
		return true
	}

	return false
}

// ── Keyword lists ──

var problemKeywords = []string{
	// 故障类
	"故障", "问题", "异常", "报错", "报警", "告警", "宕机", "崩溃", "挂了", "不可用",
	// 性能类
	"慢", "卡", "超时", "延迟", "冲高", "飙升", "打满", "瓶颈", "阻塞", "等待",
	// 资源类
	"OOM", "内存不足", "空间满", "磁盘满", "CPU高", "IO高", "连接数",
	// 分析类
	"top", "topsql", "根因", "分析", "诊断", "排查",
	// 锁/并发类
	"死锁", "锁等待", "锁冲突", "阻塞链", "kill session",
}

var preferenceKeywords = []string{
	"好的", "可以", "按这个", "同意", "就这样",
	"不要", "不行", "别", "不用", "不建议", "换一个",
	"我倾向", "我偏好", "我们一般", "我们公司",
}

var workloadKeywords = []string{
	"高峰期", "低峰", "业务量", "每天", "每周", "每月",
	"白天", "晚上", "凌晨", "维护窗口", "跑批", "账单日",
}

var diagnosticTools = []string{
	"topsql", "explain", "waits", "activesessions",
	"alert", "deadlock", "health", "os", "ash",
	"blocktree", "locks", "slowsql",
}

// ── Helpers ──

func matchAnyKeyword(messages []string, keywords []string) bool {
	for _, msg := range messages {
		lower := strings.ToLower(msg)
		for _, kw := range keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return true
			}
		}
	}
	return false
}

func matchAnyTool(called []string, targets []string) bool {
	targetSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		targetSet[t] = true
	}
	for _, c := range called {
		if targetSet[c] {
			return true
		}
	}
	return false
}
