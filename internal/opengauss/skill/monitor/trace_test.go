/*-------------------------------------------------------------------------
 *
 * trace_test.go
 *	  Test cases for trace.go (monitor package):
 *	  TestOGTraceSkill_Interface, TestOGTraceSkill_Validate,
 *	  TestOGTraceSkill_RefusesRemoteHost.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/trace_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/diagtrace"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/trace"
)

func TestOGTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.Description() == "" {
		t.Errorf("Description() is empty")
	}
	if s.ToolDef().Name != "trace" {
		t.Errorf("ToolDef().Name = %q, want 'trace'", s.ToolDef().Name)
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}

func TestTraceSkillForDB_GaussDBIdentity(t *testing.T) {
	s := NewTraceSkillForDB("gaussdb", "GaussDB", "10.0.0.5", nil)
	if got := s.Description(); !strings.Contains(got, "GaussDB") {
		t.Fatalf("Description()=%q, want GaussDB", got)
	}
	if got := s.ToolDef().Description; !strings.Contains(got, "GaussDB") {
		t.Fatalf("ToolDef().Description=%q, want GaussDB", got)
	}
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	for _, want := range []string{"OS/GaussDB 进程堆栈采集", "/diagtrace last"} {
		if !strings.Contains(res.Rendered, want) {
			t.Fatalf("GaussDB /trace unavailable output missing %q:\n%s", want, res.Rendered)
		}
	}
	if strings.Contains(res.Rendered, "OS/openGauss") {
		t.Fatalf("GaussDB /trace output leaked openGauss identity:\n%s", res.Rendered)
	}
}

func TestTraceSkillForDB_SourceFallbackFromGaussDBToOpenGauss(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "exec.cpp")
	if err := os.WriteFile(srcFile, []byte("int ExecQuery() {\n    return 0;\n}\n"), 0644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	s := NewTraceSkillForDB("gaussdb", "GaussDB", "localhost", &config.TraceConfig{
		Sources: map[string]config.TraceSourceConfig{
			"opengauss": {Dir: dir},
		},
	})
	sources := s.lookupSources([]trace.HotFunc{{Name: "ExecQuery"}})
	if len(sources) != 1 {
		t.Fatalf("lookupSources returned %d entries, want 1", len(sources))
	}
	if sources[0].FuncName != "ExecQuery" {
		t.Fatalf("FuncName=%q, want ExecQuery", sources[0].FuncName)
	}
}

func TestTraceSkillForDB_FormatOutputUsesDisplayName(t *testing.T) {
	s := NewTraceSkillForDB("gaussdb", "GaussDB", "localhost", nil)
	out := s.formatOutput(&trace.TraceResult{PID: 123, Duration: 2, SVGPath: "/tmp/trace.svg"}, nil)
	if !strings.Contains(out, "OS 堆栈分析 (GaussDB/gaussdb PID 123, 2s, 99Hz)") {
		t.Fatalf("formatOutput missing GaussDB identity:\n%s", out)
	}
}

func TestOGTraceSkill_Validate(t *testing.T) {
	s := NewTraceSkill("localhost", nil)

	// Default (no args) should pass — internal default is 3 seconds.
	if err := s.Validate(skill.ParamsFromMap(map[string]any{})); err != nil {
		t.Errorf("Validate({}) unexpected error: %v", err)
	}
	if err := s.Validate(skill.ParamsFromMap(map[string]any{"args": "last"})); err != nil {
		t.Errorf("Validate(args=last) unexpected error: %v", err)
	}
	// Valid range: 1..10.
	for _, dur := range []int{1, 5, 10} {
		if err := s.Validate(skill.ParamsFromMap(map[string]any{"duration": dur})); err != nil {
			t.Errorf("Validate(duration=%d) unexpected error: %v", dur, err)
		}
	}
	// Out-of-range must reject: perf over 10s starves the host and under 1s
	// produces too few samples to be meaningful.
	for _, dur := range []int{0, -1, 11, 30} {
		if err := s.Validate(skill.ParamsFromMap(map[string]any{"duration": dur})); err == nil {
			t.Errorf("expected error for duration=%d", dur)
		}
	}
}

// Execute on a non-loopback host must refuse without attempting perf — the
// skill assumes OpenDB runs on the DB host. This is the single most
// important safety invariant; if it regresses the skill will hang in perf
// trying to profile the wrong machine.
func TestOGTraceSkill_RefusesRemoteHost(t *testing.T) {
	s := NewTraceSkill("10.0.0.5", nil)
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Type != skill.ResultText {
		t.Errorf("expected ResultText (soft error), got %v", res.Type)
	}
	if res.Summary != "trace unavailable" {
		t.Errorf("expected summary 'trace unavailable', got %q", res.Summary)
	}
}

func TestOGTraceSkill_Last(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	diagtrace.SetLast(diagtrace.Event{
		Input:     "当前有哪几个wdr报告",
		Intent:    "wdr_list",
		Mode:      "direct_skill",
		Skill:     "wdr",
		LLMUsed:   false,
		Status:    "ok",
		StartedAt: time.Unix(1, 0),
		EndedAt:   time.Unix(2, 0),
	})
	s := NewTraceSkill("10.0.0.5", nil)
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{"args": "last"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	for _, want := range []string{"推荐改用 /diagtrace last", "intent: wdr_list"} {
		if !strings.Contains(res.Rendered, want) {
			t.Fatalf("/trace last output missing %q:\n%s", want, res.Rendered)
		}
	}
}

func TestOGTraceSkill_History(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	diagtrace.SetLast(diagtrace.Event{Input: "当前数据库存在什么问题", Intent: "current_db_diag", Mode: "evidence_then_llm", LLMUsed: true, Model: "opus", Status: "ok", StartedAt: time.Unix(3, 0), EndedAt: time.Unix(4, 0)})
	diagtrace.SetLast(diagtrace.Event{Input: "当前有哪几个wdr报告", Intent: "wdr_list", Mode: "direct_skill", Skill: "wdr", Status: "ok", StartedAt: time.Unix(5, 0), EndedAt: time.Unix(6, 0), ToolCalls: []diagtrace.ToolCall{{Name: "wdr", Status: "ok"}}})
	s := NewTraceSkill("10.0.0.5", nil)
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{"args": "history 2"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	for _, want := range []string{"推荐改用 /diagtrace history", "诊断 Trace History", "current_db_diag", "wdr_list", "tools:"} {
		if !strings.Contains(res.Rendered, want) {
			t.Fatalf("/trace history output missing %q:\n%s", want, res.Rendered)
		}
	}
}

func TestOGTraceSkill_LastJSON(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	diagtrace.SetLast(diagtrace.Event{Input: "sql id 581990336 如何优化", Intent: "sqltune", Mode: "evidence_then_llm", Model: "qwen3-32b-prompt", Status: "ok"})
	s := NewTraceSkill("10.0.0.5", nil)
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{"args": "last --json"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	for _, want := range []string{"推荐改用 /diagtrace last", `"Input": "sql id 581990336 如何优化"`, `"Intent": "sqltune"`, `"Model": "qwen3-32b-prompt"`} {
		if !strings.Contains(res.Rendered, want) {
			t.Fatalf("/trace last --json output missing %q:\n%s", want, res.Rendered)
		}
	}
}

func TestDiagTraceSkill_LastAndHistory(t *testing.T) {
	t.Setenv("OPENDB_DIAGTRACE_DIR", t.TempDir())
	diagtrace.SetLast(diagtrace.Event{Input: "当前数据库存在什么问题", Intent: "current_db_diag", Status: "ok", StartedAt: time.Unix(7, 0), EndedAt: time.Unix(8, 0)})
	s := NewDiagTraceSkill()
	if s.Name() != "diagtrace" {
		t.Fatalf("Name()=%q want diagtrace", s.Name())
	}
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Rendered, "intent: current_db_diag") {
		t.Fatalf("/diagtrace output missing current trace:\n%s", res.Rendered)
	}
	res, err = s.Execute(nil, skill.ParamsFromMap(map[string]any{"args": "history 1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Rendered, "诊断 Trace History") {
		t.Fatalf("/diagtrace history output missing history:\n%s", res.Rendered)
	}
}

func TestOGTraceUnavailableExplainsOSVsDiagTrace(t *testing.T) {
	s := NewTraceSkill("10.0.0.5", nil)
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	for _, want := range []string{"/trace 是 OS/openGauss 进程堆栈采集", "/diagtrace last"} {
		if !strings.Contains(res.Rendered, want) {
			t.Fatalf("/trace unavailable output missing %q:\n%s", want, res.Rendered)
		}
	}
}

func TestTraceHistoryLimit(t *testing.T) {
	for _, tt := range []struct {
		arg  string
		want int
	}{
		{"history", 10},
		{"history 3", 3},
		{"history --json 4", 4},
		{"history 999", 10},
	} {
		if got := traceHistoryLimit(tt.arg); got != tt.want {
			t.Fatalf("traceHistoryLimit(%q)=%d want %d", tt.arg, got, tt.want)
		}
	}
}
