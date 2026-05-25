/*-------------------------------------------------------------------------
 *
 * sentinel_skill.go
 *	  Package ai — DM 的 AI 类 skill (sentinel 持续异常采集 +
 *	  diag /llm).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/ai/sentinel_skill.go
 *
 *-------------------------------------------------------------------------
 */
// Package ai — DM 的 AI 类 skill (sentinel 持续异常采集 + diag /llm).
package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// SentinelSkill — DM 异常持续采集入口 (MVP 版).
//
// 与 Oracle sentinel 的差异:
// - Oracle sentinel 用 baseline (avg+std) 做异常检测, 8700+ 行
// - DM 这版用简单阈值: 阻塞 > thr_blocked / 长 SQL > thr_long / 死锁数变化 > 0
// - 采集间隔: 默认 30s
// - alert 发到 AlertCh, REPL 显示
// - 后续可升级为 baseline + anomaly_signals 算法
type SentinelSkill struct {
	driver db.Driver

	mu         sync.Mutex
	running    bool
	stopCh     chan struct{}
	alertCh    chan alert.Event
	lastDLCnt  int64 // 上次采到的累计死锁数
	startedAt  time.Time
	tickCount  int
	alertCount int

	// 阈值
	thrBlocked  int
	thrLongSQL  int
	intervalSec int
}

// NewSentinelSkill 创建 DM Sentinel (默认阈值).
func NewSentinelSkill(driver db.Driver) *SentinelSkill {
	return &SentinelSkill{
		driver:      driver,
		alertCh:     make(chan alert.Event, 16),
		thrBlocked:  3,  // 阻塞会话 > 3 触发
		thrLongSQL:  50, // 长 SQL > 50 触发
		intervalSec: 30,
	}
}

// WithThresholds 覆盖默认阈值. 任意参数 > 0 才生效, 0 或负值保留默认.
// 通常 register.go 从 config.SentinelConfig.Thresholds 读取后调用.
func (s *SentinelSkill) WithThresholds(blocked, longSQL int) *SentinelSkill {
	if blocked > 0 {
		s.thrBlocked = blocked
	}
	if longSQL > 0 {
		s.thrLongSQL = longSQL
	}
	return s
}

// WithInterval 覆盖默认采集间隔(秒). 0 或负值保留默认.
func (s *SentinelSkill) WithInterval(seconds int) *SentinelSkill {
	if seconds > 0 {
		s.intervalSec = seconds
	}
	return s
}

func (s *SentinelSkill) Name() string                       { return "sentinel" }
func (s *SentinelSkill) Description() string                { return "持续异常采集 (V$LOCK + V$LONG_EXEC_SQLS + V$DEADLOCK_HISTORY)" }
func (s *SentinelSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SentinelSkill) Validate(_ skill.Params) error      { return nil }

func (s *SentinelSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sentinel", Description: "Show or control DM sentinel status"}
}
func (s *SentinelSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "sentinel",
		Usage:          "/sentinel [start|stop|status]",
		Examples:       []string{"/sentinel", "/sentinel start", "/sentinel stop"},
		ArgCompletions: []string{"start", "stop", "status"},
	}
}

// AlertCh 实现 ui.SentinelAlertSource.
func (s *SentinelSkill) AlertCh() <-chan alert.Event { return s.alertCh }

// IsRunning 实现 ui.SentinelAlertSource.
func (s *SentinelSkill) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// AutoStart 实现 ui.SentinelAlertSource. 启动后台采集 goroutine.
func (s *SentinelSkill) AutoStart(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.startedAt = time.Now()
	s.tickCount = 0
	s.alertCount = 0
	s.lastDLCnt = -1
	s.stopCh = make(chan struct{})
	stopCh := s.stopCh
	s.mu.Unlock()

	go s.loop(ctx, stopCh)
	return nil
}

// StopSentinel 实现 ui.SentinelAlertSource.
func (s *SentinelSkill) StopSentinel() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()
}

func (s *SentinelSkill) loop(ctx context.Context, stopCh <-chan struct{}) {
	t := time.NewTicker(time.Duration(s.intervalSec) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return
		case <-stopCh:
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

const sentinelProbeSQL = `SELECT
       (SELECT COUNT(*) FROM V$LOCK WHERE BLOCKED = 1) AS BLOCKED,
       (SELECT COUNT(*) FROM V$LONG_EXEC_SQLS) AS LONG_SQL,
       (SELECT COUNT(*) FROM V$DEADLOCK_HISTORY) AS DEADLOCK_TOTAL
FROM DUAL`

func (s *SentinelSkill) tick(ctx context.Context) {
	s.mu.Lock()
	s.tickCount++
	s.mu.Unlock()

	r, err := s.driver.Query(ctx, sentinelProbeSQL)
	if err != nil || r == nil || len(r.Rows) == 0 || len(r.Rows[0]) < 3 {
		return
	}
	row := r.Rows[0]
	blocked := toInt64(row[0])
	longSQL := toInt64(row[1])
	dlTotal := toInt64(row[2])

	var triggers []string
	if blocked > int64(s.thrBlocked) {
		triggers = append(triggers, fmt.Sprintf("阻塞会话=%d (阈值 %d)", blocked, s.thrBlocked))
	}
	if longSQL > int64(s.thrLongSQL) {
		triggers = append(triggers, fmt.Sprintf("长SQL=%d (阈值 %d)", longSQL, s.thrLongSQL))
	}

	s.mu.Lock()
	prev := s.lastDLCnt
	s.lastDLCnt = dlTotal
	s.mu.Unlock()

	if prev >= 0 && dlTotal > prev {
		triggers = append(triggers, fmt.Sprintf("新增死锁=%d (累计 %d→%d)", dlTotal-prev, prev, dlTotal))
	}

	if len(triggers) == 0 {
		return
	}

	cause := classifyDMTrigger(triggers)
	desc := strings.Join(triggers, "; ")

	s.mu.Lock()
	s.alertCount++
	s.mu.Unlock()

	ev := alert.Event{
		Timestamp:   time.Now(),
		Description: desc,
		CauseName:   cause,
	}
	select {
	case s.alertCh <- ev:
	default:
		// 缓冲满则丢弃，不阻塞采集 loop
	}
}

func classifyDMTrigger(triggers []string) string {
	all := strings.Join(triggers, " ")
	switch {
	case strings.Contains(all, "新增死锁"):
		return "死锁冲高"
	case strings.Contains(all, "阻塞会话"):
		return "锁阻塞冲高"
	case strings.Contains(all, "长SQL"):
		return "长SQL冲高"
	default:
		return "DM 异常"
	}
}

// Execute 处理 /sentinel CLI 命令: start / stop / status / "" (= status).
func (s *SentinelSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(strings.ToLower(params.StringOr("args", "")))

	switch args {
	case "start":
		if err := s.AutoStart(ctx); err != nil {
			return nil, fmt.Errorf("dm sentinel start: %w", err)
		}
		return s.statusResult("started")
	case "stop":
		s.StopSentinel()
		return s.statusResult("stopped")
	case "", "status":
		return s.statusResult("query")
	default:
		return nil, fmt.Errorf("unknown sentinel action %q (use start|stop|status)", args)
	}
}

func (s *SentinelSkill) statusResult(action string) (*skill.Result, error) {
	s.mu.Lock()
	running := s.running
	uptime := time.Duration(0)
	if !s.startedAt.IsZero() {
		uptime = time.Since(s.startedAt).Truncate(time.Second)
	}
	tickCount := s.tickCount
	alertCount := s.alertCount
	thrBlocked := s.thrBlocked
	thrLongSQL := s.thrLongSQL
	intervalSec := s.intervalSec
	s.mu.Unlock()

	var b strings.Builder
	b.WriteString("=== DM Sentinel 状态 ===\n")
	if running {
		b.WriteString(fmt.Sprintf("  状态:           运行中 (uptime %s)\n", uptime))
		b.WriteString(fmt.Sprintf("  采集间隔:       %d 秒\n", intervalSec))
		b.WriteString(fmt.Sprintf("  采集次数:       %d\n", tickCount))
		b.WriteString(fmt.Sprintf("  累计告警:       %d 次\n", alertCount))
	} else {
		b.WriteString("  状态:           已停止\n")
	}
	b.WriteString(fmt.Sprintf("  阈值-阻塞会话:  > %d\n", thrBlocked))
	b.WriteString(fmt.Sprintf("  阈值-长SQL数:    > %d\n", thrLongSQL))
	b.WriteString(fmt.Sprintf("  阈值-死锁:      累计数变化 > 0\n"))
	b.WriteString("\n操作: /sentinel start | stop | status\n")

	rendered := b.String()
	rendered += fmt.Sprintf("\n[summary]\nrunning: %v\naction: %s\nuptime_sec: %v\ntick_count: %d\nalert_count: %d\n",
		running, action, int(uptime.Seconds()), tickCount, alertCount)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("Sentinel %s", action),
	}, nil
}

func toInt64(v any) int64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float64:
		return int64(x)
	default:
		var n int64
		fmt.Sscanf(fmt.Sprintf("%v", v), "%d", &n)
		return n
	}
}
