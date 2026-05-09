/*-------------------------------------------------------------------------
 *
 * trigger_test.go
 *	  Test cases for trigger.go (memory package):
 *	  TestShouldTrigger_ProblemKeywords, TestShouldTrigger_Mode,
 *	  TestShouldTrigger_TurnCount.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/trigger_test.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import "testing"

func TestShouldTrigger_ProblemKeywords(t *testing.T) {
	if !ShouldTriggerMemoryRound([]string{"数据库变慢了"}, 1, nil, "playbook") {
		t.Error("should trigger on '慢'")
	}
	if !ShouldTriggerMemoryRound([]string{"CPU冲高"}, 1, nil, "playbook") {
		t.Error("should trigger on '冲高'")
	}
	if ShouldTriggerMemoryRound([]string{"查看当前版本号"}, 1, nil, "playbook") {
		t.Error("should NOT trigger on simple query")
	}
}

func TestShouldTrigger_Mode(t *testing.T) {
	if !ShouldTriggerMemoryRound(nil, 1, nil, "assist") {
		t.Error("should trigger on assist mode")
	}
	if !ShouldTriggerMemoryRound(nil, 1, nil, "auto") {
		t.Error("should trigger on auto mode")
	}
	if ShouldTriggerMemoryRound(nil, 1, nil, "playbook") {
		t.Error("should NOT trigger on playbook mode alone")
	}
}

func TestShouldTrigger_TurnCount(t *testing.T) {
	if !ShouldTriggerMemoryRound(nil, 5, nil, "playbook") {
		t.Error("should trigger on 5+ turns")
	}
	if ShouldTriggerMemoryRound(nil, 2, nil, "playbook") {
		t.Error("should NOT trigger on 2 turns")
	}
}

func TestShouldTrigger_DiagnosticTools(t *testing.T) {
	if !ShouldTriggerMemoryRound(nil, 1, []string{"topsql"}, "playbook") {
		t.Error("should trigger on topsql")
	}
	if !ShouldTriggerMemoryRound(nil, 1, []string{"explain", "waits"}, "playbook") {
		t.Error("should trigger on explain")
	}
	if ShouldTriggerMemoryRound(nil, 1, []string{"show_variables"}, "playbook") {
		t.Error("should NOT trigger on non-diagnostic tool")
	}
}

func TestShouldTrigger_Preference(t *testing.T) {
	if !ShouldTriggerMemoryRound([]string{"不要加索引"}, 1, nil, "playbook") {
		t.Error("should trigger on '不要'")
	}
	if !ShouldTriggerMemoryRound([]string{"好的，按这个来"}, 1, nil, "playbook") {
		t.Error("should trigger on '按这个'")
	}
}

func TestShouldTrigger_Workload(t *testing.T) {
	if !ShouldTriggerMemoryRound([]string{"每月1号是账单日"}, 1, nil, "playbook") {
		t.Error("should trigger on '每月' and '账单日'")
	}
	if !ShouldTriggerMemoryRound([]string{"维护窗口在凌晨2点"}, 1, nil, "playbook") {
		t.Error("should trigger on '维护窗口'")
	}
}

func TestShouldTrigger_NoMatch(t *testing.T) {
	if ShouldTriggerMemoryRound([]string{"你好"}, 1, nil, "playbook") {
		t.Error("should NOT trigger on greeting")
	}
}
