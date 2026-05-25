/*-------------------------------------------------------------------------
 *
 * sentinel_skill_test.go
 *	  Test cases for sentinel_skill.go (ai package):
 *	  TestSentinelSkill_Metadata, TestSentinelSkill_Status_Stopped,
 *	  TestSentinelSkill_StartStop.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/ai/sentinel_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func scalarRow(v any) *db.QueryResult {
	return &db.QueryResult{Columns: []string{"V"}, Rows: [][]any{{v}}}
}

func TestSentinelSkill_Metadata(t *testing.T) {
	s := NewSentinelSkill(mock.NewMockDriver())
	if s.Name() != "sentinel" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
	if s.IsRunning() {
		t.Error("new skill should not be running")
	}
	if s.AlertCh() == nil {
		t.Error("AlertCh() = nil")
	}
}

func TestSentinelSkill_Status_Stopped(t *testing.T) {
	s := NewSentinelSkill(mock.NewMockDriver())
	r, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(r.Rendered, "状态:           已停止") {
		t.Errorf("Rendered missing stopped status:\n%s", r.Rendered)
	}
	if !strings.Contains(r.Rendered, "running: false") {
		t.Errorf("Rendered missing running:false:\n%s", r.Rendered)
	}
}

func TestSentinelSkill_StartStop(t *testing.T) {
	s := NewSentinelSkill(mock.NewMockDriver())

	// start
	r, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "start"}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !s.IsRunning() {
		t.Error("after start, IsRunning() = false")
	}
	if !strings.Contains(r.Rendered, "running: true") {
		t.Errorf("start Rendered missing running:true:\n%s", r.Rendered)
	}

	// 二次 start 应幂等 (不报错)
	if _, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "start"})); err != nil {
		t.Errorf("idempotent start: %v", err)
	}

	// stop
	r, err = s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "stop"}))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s.IsRunning() {
		t.Error("after stop, IsRunning() = true")
	}
	if !strings.Contains(r.Rendered, "running: false") {
		t.Errorf("stop Rendered missing running:false:\n%s", r.Rendered)
	}

	// 二次 stop 应幂等
	s.StopSentinel()
}

func TestSentinelSkill_UnknownAction(t *testing.T) {
	s := NewSentinelSkill(mock.NewMockDriver())
	_, err := s.Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "doomsday"}))
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

// tick() 直接测试: 模拟阻塞超阈值 → 触发 alert.
func TestSentinelSkill_Tick_TriggersBlockedAlert(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		// blocked=10, longSQL=0, dlTotal=0
		return &db.QueryResult{
			Columns: []string{"BLOCKED", "LONG_SQL", "DEADLOCK_TOTAL"},
			Rows:    [][]any{{int64(10), int64(0), int64(0)}},
		}, nil
	}
	s := NewSentinelSkill(drv)
	s.lastDLCnt = 0 // 初始化避免首次死锁告警

	s.tick(context.Background())

	select {
	case ev := <-s.AlertCh():
		if !strings.Contains(ev.Description, "阻塞会话=10") {
			t.Errorf("Alert.Description = %q, want contains '阻塞会话=10'", ev.Description)
		}
		if ev.CauseName != "锁阻塞冲高" {
			t.Errorf("Alert.CauseName = %q, want '锁阻塞冲高'", ev.CauseName)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected alert event, got timeout")
	}
}

// tick(): 死锁数变化触发 alert + classified as 死锁冲高.
// AutoStart 通常会把 lastDLCnt 置 -1, 让首次 tick 仅记录 baseline 不报警.
// 这里手动设 -1 模拟 AutoStart 后的状态.
func TestSentinelSkill_Tick_TriggersDeadlockAlert(t *testing.T) {
	drv := mock.NewMockDriver()
	// 第一次 tick 拿到 dlTotal=10 (上一次=-1, 不报警, 仅记录基线)
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns: []string{"BLOCKED", "LONG_SQL", "DEADLOCK_TOTAL"},
			Rows:    [][]any{{int64(0), int64(0), int64(10)}},
		}, nil
	}
	s := NewSentinelSkill(drv)
	s.lastDLCnt = -1 // 模拟 AutoStart 后的初始状态
	s.tick(context.Background()) // 第一次, lastDLCnt -1 → 10, 不报警 (只记录基线)

	// 排空可能的 alert (没有该有的)
	select {
	case <-s.AlertCh():
		t.Error("first tick should not alert (only baseline)")
	default:
	}

	// 第二次 tick 拿到 dlTotal=15 (变化 +5)
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns: []string{"BLOCKED", "LONG_SQL", "DEADLOCK_TOTAL"},
			Rows:    [][]any{{int64(0), int64(0), int64(15)}},
		}, nil
	}
	s.tick(context.Background())

	select {
	case ev := <-s.AlertCh():
		if !strings.Contains(ev.Description, "新增死锁=5") {
			t.Errorf("Alert.Description = %q, want '新增死锁=5'", ev.Description)
		}
		if ev.CauseName != "死锁冲高" {
			t.Errorf("Alert.CauseName = %q, want '死锁冲高'", ev.CauseName)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected deadlock alert")
	}
}

// 全部低于阈值时 tick 不应该产生 alert.
func TestSentinelSkill_Tick_NoTrigger(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns: []string{"BLOCKED", "LONG_SQL", "DEADLOCK_TOTAL"},
			Rows:    [][]any{{int64(1), int64(10), int64(5)}}, // 全部低于阈值
		}, nil
	}
	s := NewSentinelSkill(drv)
	s.lastDLCnt = 5 // 死锁数没变化 → 不触发
	s.tick(context.Background())

	select {
	case ev := <-s.AlertCh():
		t.Errorf("unexpected alert: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// OK, no alert
	}
}

// WithThresholds / WithInterval: 配置覆盖默认值, 0/负数保留默认.
func TestSentinelSkill_WithThresholds(t *testing.T) {
	tests := []struct {
		name        string
		blocked     int
		longSQL     int
		wantBlocked int
		wantLongSQL int
	}{
		{"both override", 5, 100, 5, 100},
		{"only blocked", 10, 0, 10, 50},        // 0 保留默认 50
		{"only longSQL", 0, 200, 3, 200},       // 0 保留默认 3
		{"negative keeps default", -1, -1, 3, 50},
		{"both keep default", 0, 0, 3, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSentinelSkill(mock.NewMockDriver()).
				WithThresholds(tt.blocked, tt.longSQL)
			if s.thrBlocked != tt.wantBlocked {
				t.Errorf("thrBlocked = %d, want %d", s.thrBlocked, tt.wantBlocked)
			}
			if s.thrLongSQL != tt.wantLongSQL {
				t.Errorf("thrLongSQL = %d, want %d", s.thrLongSQL, tt.wantLongSQL)
			}
		})
	}
}

func TestSentinelSkill_WithInterval(t *testing.T) {
	s1 := NewSentinelSkill(mock.NewMockDriver()).WithInterval(60)
	if s1.intervalSec != 60 {
		t.Errorf("intervalSec = %d, want 60", s1.intervalSec)
	}
	// 0 保留默认 (30)
	s2 := NewSentinelSkill(mock.NewMockDriver()).WithInterval(0)
	if s2.intervalSec != 30 {
		t.Errorf("intervalSec(0) = %d, want default 30", s2.intervalSec)
	}
	// 负数保留默认
	s3 := NewSentinelSkill(mock.NewMockDriver()).WithInterval(-5)
	if s3.intervalSec != 30 {
		t.Errorf("intervalSec(-5) = %d, want default 30", s3.intervalSec)
	}
}

// toInt64 是 sentinel tick 的核心 helper, DM 驱动可能返回多种数值类型 + 字符串.
// fallback 路径 (default 分支) 处理 string 输入解析为 int64.
func TestToInt64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int64
	}{
		{"nil", nil, 0},
		{"int64", int64(140304100), 140304100},
		{"int", 42, 42},
		{"int32", int32(99), 99},
		{"float64", 3.99, 3}, // 截断
		{"string fallback decimal", "1234", 1234},
		{"string fallback negative", "-5", -5},
		{"string unparseable returns 0", "abc", 0},
		{"empty string returns 0", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toInt64(tt.in)
			if got != tt.want {
				t.Errorf("toInt64(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestClassifyDMTrigger(t *testing.T) {
	tests := []struct {
		triggers []string
		want     string
	}{
		{[]string{"新增死锁=3"}, "死锁冲高"},
		{[]string{"阻塞会话=10"}, "锁阻塞冲高"},
		{[]string{"长SQL=200"}, "长SQL冲高"},
		// 死锁优先级最高 (即使有阻塞也归为死锁冲高)
		{[]string{"阻塞会话=5", "新增死锁=2"}, "死锁冲高"},
	}
	for _, tt := range tests {
		got := classifyDMTrigger(tt.triggers)
		if got != tt.want {
			t.Errorf("classifyDMTrigger(%v) = %q, want %q", tt.triggers, got, tt.want)
		}
	}
}
