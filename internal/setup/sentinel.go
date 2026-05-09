/*-------------------------------------------------------------------------
 *
 * sentinel.go
 *	  SentinelStep introduces Sentinel and configures auto-start and
 *	  probe interval
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/sentinel.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type sentinelPhase int

const (
	sentinelAutoStart sentinelPhase = iota
	sentinelInterval
)

// SentinelStep introduces Sentinel and configures auto-start and probe interval
type SentinelStep struct {
	cfg       *SetupConfig
	phase     sentinelPhase
	autoStart *SelectModel
	interval  *SelectModel
	done      bool
}

func NewSentinelStep(cfg *SetupConfig) *SentinelStep {
	return &SentinelStep{
		cfg: cfg,
		autoStart: NewSelectModel("Enable Sentinel auto-start?", []SelectItem{
			{Label: "Yes", Value: "yes", Desc: "recommended"},
			{Label: "No", Value: "no", Desc: "手动启动"},
		}, "yes"),
		interval: NewSelectModel("Probe interval", []SelectItem{
			{Label: "1 second", Value: "1s", Desc: "default, recommended"},
			{Label: "3 seconds", Value: "3s"},
			{Label: "5 seconds", Value: "5s"},
			{Label: "10 seconds", Value: "10s"},
		}, "1s"),
	}
}

func (s *SentinelStep) Title() string { return "Sentinel" }
func (s *SentinelStep) Done() bool    { return s.done }

func (s *SentinelStep) Summary() string {
	autoStart := "Off"
	if s.cfg.Sentinel.AutoStart {
		autoStart = "On"
	}
	return CompletedLine("Sentinel", "Auto-start: "+autoStart+" · Interval: "+s.interval.SelectedLabel())
}

func (s *SentinelStep) Init() tea.Cmd { return nil }

func (s *SentinelStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case sentinelAutoStart:
		updated, cmd := s.autoStart.Update(msg)
		s.autoStart = updated
		if s.autoStart.Done() {
			s.cfg.Sentinel.AutoStart = s.autoStart.Selected() == "yes"
			s.phase = sentinelInterval
			return s, nil
		}
		return s, cmd
	case sentinelInterval:
		updated, cmd := s.interval.Update(msg)
		s.interval = updated
		if s.interval.Done() {
			switch s.interval.Selected() {
			case "3s":
				s.cfg.Sentinel.ProbeInterval = 3 * time.Second
			case "5s":
				s.cfg.Sentinel.ProbeInterval = 5 * time.Second
			case "10s":
				s.cfg.Sentinel.ProbeInterval = 10 * time.Second
			default:
				s.cfg.Sentinel.ProbeInterval = 1 * time.Second
			}
			s.done = true
			return s, emitDone
		}
		return s, cmd
	}
	return s, nil
}

func (s *SentinelStep) View() string {
	var b strings.Builder

	intro := strings.Join([]string{
		"Sentinel 是 OpenDB 的实时异常检测引擎。",
		"它在后台以每秒一次的频率轻量采集数据库核心指标:",
		"",
		StyleSuccess.Render("·") + " 活跃会话 / CPU 会话 / IO 会话 / 锁等待",
		StyleSuccess.Render("·") + " 慢 SQL / Redo 速率 / 硬解析率",
		"",
		"当指标异常冲高时，Sentinel 自动进入高频采集模式，",
		"200ms 一次，捕获问题现场，并生成根因分析报告。",
		"",
		"对数据库性能影响 < 0.1%，可安全用于生产环境。",
	}, "\n")

	b.WriteString("\n" + InfoPanel("Sentinel — Real-time Anomaly Detection", intro) + "\n")

	switch s.phase {
	case sentinelAutoStart:
		b.WriteString(s.autoStart.View())
	case sentinelInterval:
		b.WriteString(SuccessLine("Auto-start: " + s.autoStart.SelectedLabel()) + "\n")
		b.WriteString(s.interval.View())
	}

	return b.String()
}
