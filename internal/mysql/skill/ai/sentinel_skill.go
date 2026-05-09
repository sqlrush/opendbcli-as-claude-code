/*-------------------------------------------------------------------------
 *
 * sentinel_skill.go
 *	  Package ai provides MySQL AI-powered skills: sentinel, diag, and
 *	  rule.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/ai/sentinel_skill.go
 *
 *-------------------------------------------------------------------------
 */
// Package ai provides MySQL AI-powered skills: sentinel, diag, and rule.
package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/mysql/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// SentinelSkill starts/stops/shows the MySQL sentinel background anomaly detector.
type SentinelSkill struct {
	driver      db.Driver
	sentinelCfg config.SentinelConfig
	sentinel    *sentinel.Sentinel

	mu      sync.Mutex
	reports []*sentinel.BurstReport
}

const maxReports = 16

// NewSentinelSkill creates a SentinelSkill backed by the given driver.
func NewSentinelSkill(driver db.Driver, cfg config.SentinelConfig) *SentinelSkill {
	return &SentinelSkill{
		driver:      driver,
		sentinelCfg: cfg,
	}
}

func (s *SentinelSkill) Name() string                       { return "sentinel" }
func (s *SentinelSkill) Description() string                { return "Background anomaly detection (start/stop/status)" }
func (s *SentinelSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SentinelSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sentinel",
		Description: "Background anomaly detection (start/stop/status)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: start, stop, or status",
					"enum":        []string{"start", "stop", "status"},
				},
			},
		},
	}
}

func (s *SentinelSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "sentinel",
		Aliases:        []string{"snt"},
		Usage:          "/sentinel [start|stop|status]",
		ArgCompletions: []string{"start", "stop", "status"},
		Examples: []string{
			"/sentinel start",
			"/sentinel stop",
			"/sentinel status",
			"/sentinel",
		},
	}
}

func (s *SentinelSkill) Validate(params skill.Params) error {
	action := params.StringOr("action", params.StringOr("args", "status"))
	switch action {
	case "start", "stop", "status", "":
		return nil
	default:
		return fmt.Errorf("unknown action %q, use: start, stop, status", action)
	}
}

func (s *SentinelSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	action := params.StringOr("action", params.StringOr("args", "status"))
	if action == "" {
		action = "status"
	}

	switch action {
	case "start":
		return s.start(ctx)
	case "stop":
		return s.stop()
	case "status":
		return s.status()
	default:
		return &skill.Result{
			Type: skill.ResultError,
			Data: fmt.Sprintf("unknown action: %s", action),
		}, nil
	}
}

// buildSentinelConfig converts config.SentinelConfig to sentinel.Config.
func (s *SentinelSkill) buildSentinelConfig() sentinel.Config {
	c := s.sentinelCfg
	cfg := sentinel.DefaultConfig()

	if c.ProbeInterval > 0 {
		cfg.PollInterval = c.ProbeInterval
	}
	if c.BaselineWindow > 0 {
		cfg.BaselineWindow = c.BaselineWindow
	}
	if c.MinSamples > 0 {
		cfg.MinSamples = c.MinSamples
	}
	if c.Sigma > 0 {
		cfg.SigmaThreshold = c.Sigma
	}
	if c.SustainedCount > 0 {
		cfg.SustainedCount = c.SustainedCount
	}
	if c.BurstMaxDuration > 0 {
		cfg.BurstDuration = c.BurstMaxDuration
	}
	if c.Cooldown > 0 {
		cfg.CooldownPeriod = c.Cooldown
	}

	return cfg
}

func (s *SentinelSkill) start(_ context.Context) (*skill.Result, error) {
	if s.sentinel != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "already running",
			Rendered: "Sentinel 已在运行中",
			Summary:  "already running",
		}, nil
	}

	cfg := s.buildSentinelConfig()
	snt := sentinel.New(cfg, s.driver)

	snt.SetOnReport(func(report sentinel.BurstReport) {
		s.mu.Lock()
		s.reports = append(s.reports, &report)
		if len(s.reports) > maxReports {
			s.reports = s.reports[len(s.reports)-maxReports:]
		}
		s.mu.Unlock()
	})

	s.sentinel = snt
	snt.Start(context.Background())

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     "sentinel started",
		Rendered: fmt.Sprintf("Sentinel 哨兵模式已启动\n  探测模式: 三层探针 (1s/10s/30s, 15项指标)\n  触发模式: 自适应 3σ\n  基线窗口: %d 样本\n  突发采集: 200ms/帧, 最长%v",
			cfg.BaselineWindow, cfg.BurstDuration),
		Summary: "sentinel started",
	}, nil
}

// AutoStart starts sentinel silently (for auto-start on login).
func (s *SentinelSkill) AutoStart(_ context.Context) error {
	if s.sentinel != nil {
		return nil
	}

	cfg := s.buildSentinelConfig()
	snt := sentinel.New(cfg, s.driver)

	snt.SetOnReport(func(report sentinel.BurstReport) {
		s.mu.Lock()
		s.reports = append(s.reports, &report)
		if len(s.reports) > maxReports {
			s.reports = s.reports[len(s.reports)-maxReports:]
		}
		s.mu.Unlock()
	})

	s.sentinel = snt
	snt.Start(context.Background())
	return nil
}

// StopSentinel stops the sentinel loop if running.
func (s *SentinelSkill) StopSentinel() {
	if s.sentinel != nil {
		s.sentinel.Stop()
		s.sentinel = nil
	}
}

func (s *SentinelSkill) stop() (*skill.Result, error) {
	if s.sentinel == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "not running",
			Rendered: "Sentinel 未在运行",
			Summary:  "not running",
		}, nil
	}

	s.sentinel.Stop()
	s.sentinel = nil

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     "sentinel stopped",
		Rendered: "Sentinel 已停止",
		Summary:  "sentinel stopped",
	}, nil
}

func (s *SentinelSkill) status() (*skill.Result, error) {
	if s.sentinel == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "not running",
			Rendered: "Sentinel 状态: 未运行\n使用 /sentinel start 启动",
			Summary:  "not running",
		}, nil
	}

	state := s.sentinel.CurrentState()
	baselines := s.sentinel.CurrentBaselines()

	var b strings.Builder
	readyLabel := "否"
	totalSamples := len(baselines)
	for _, bl := range baselines {
		if bl.Ready {
			readyLabel = "是"
			break
		}
	}

	b.WriteString("Sentinel 哨兵状态\n\n")

	overviewQR := &db.QueryResult{
		Columns: []string{"项目", "值"},
		Rows: [][]any{
			{"运行状态", state.String()},
			{"基线就绪", readyLabel},
			{"样本数", totalSamples},
		},
	}
	b.WriteString(format.FormatTableOpts(overviewQR, format.TableOptions{MaxRows: 10, TermWidth: 120}))

	// Metric baselines table.
	order := []sentinel.MetricName{
		sentinel.MetricThreadsRunning, sentinel.MetricThreadsConnected,
		sentinel.MetricLockWaits, sentinel.MetricLongQueries,
		sentinel.MetricTPS, sentinel.MetricQPS, sentinel.MetricRedoRate,
		sentinel.MetricBufferPoolHit, sentinel.MetricHistoryList,
		sentinel.MetricConnectionsPct, sentinel.MetricReplicationLag,
	}

	var metricRows [][]any
	for _, m := range order {
		bl, ok := baselines[m]
		if !ok || !bl.Ready {
			continue
		}
		metricRows = append(metricRows, []any{
			sentinel.MetricLabel(m),
			fmt.Sprintf("%.1f", bl.Avg),
			fmt.Sprintf("%.1f", bl.Std),
		})
	}

	if len(metricRows) > 0 {
		b.WriteString("\n指标基线:\n")
		metricQR := &db.QueryResult{
			Columns: []string{"指标", "平均值", "标准差"},
			Rows:    metricRows,
		}
		b.WriteString(format.FormatTableOpts(metricQR, format.TableOptions{MaxRows: 20, TermWidth: 120}))
	}

	// Latest anomaly.
	s.mu.Lock()
	n := len(s.reports)
	var lr *sentinel.BurstReport
	if n > 0 {
		lr = s.reports[n-1]
	}
	s.mu.Unlock()

	if lr != nil {
		b.WriteString(fmt.Sprintf("\n最近一次异常 (共 %d 条记录):\n", n))
		anomalyQR := &db.QueryResult{
			Columns: []string{"项目", "值"},
			Rows: [][]any{
				{"根因分类", lr.Classification.Cause.String()},
				{"置信度", fmt.Sprintf("%.0f%%", lr.Classification.Confidence*100)},
				{"峰值活跃", lr.PeakActive},
				{"持续时间", fmt.Sprintf("%.1fs", lr.DurationSec)},
			},
		}
		b.WriteString(format.FormatTableOpts(anomalyQR, format.TableOptions{MaxRows: 10, TermWidth: 120}))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     "status",
		Rendered: b.String(),
		Summary:  fmt.Sprintf("state=%s samples=%d", state, totalSamples),
	}, nil
}

// LastReport returns the most recent burst report, if any.
func (s *SentinelSkill) LastReport() *sentinel.BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == 0 {
		return nil
	}
	return s.reports[len(s.reports)-1]
}

// ReportAt returns the report at 1-based index (1=newest).
func (s *SentinelSkill) ReportAt(oneBasedIdx int) *sentinel.BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if oneBasedIdx < 1 || oneBasedIdx > len(s.reports) {
		return nil
	}
	return s.reports[len(s.reports)-oneBasedIdx]
}

// ReportCount returns the number of stored reports.
func (s *SentinelSkill) ReportCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

// Reports returns a copy of all stored reports (newest last).
func (s *SentinelSkill) Reports() []*sentinel.BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]*sentinel.BurstReport, len(s.reports))
	copy(cp, s.reports)
	return cp
}

// IsRunning returns true if sentinel is currently active.
func (s *SentinelSkill) IsRunning() bool {
	return s.sentinel != nil
}

// AlertCh returns the sentinel's alert channel, or nil if not running.
func (s *SentinelSkill) AlertCh() <-chan alert.Event {
	if s.sentinel == nil {
		return nil
	}
	return s.sentinel.AlertCh()
}
