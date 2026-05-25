/*-------------------------------------------------------------------------
 *
 * sentinel_skill.go
 *	  SentinelSkill starts/stops/shows the PG sentinel background
 *	  anomaly detector.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/ai/sentinel_skill.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// SentinelSkill starts/stops/shows the PG sentinel background anomaly detector.
type SentinelSkill struct {
	driver      db.Driver
	sentinel    *sentinel.Sentinel
	sentinelCfg config.SentinelConfig
}

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

	sigma := c.Sigma
	if sigma <= 0 {
		sigma = 3.0
	}

	pollInterval := c.ProbeInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	baselineWindow := c.BaselineWindow
	if baselineWindow <= 0 {
		baselineWindow = 60
	}

	minSamples := c.MinSamples
	if minSamples <= 0 {
		minSamples = 10
	}

	burstDuration := c.BurstMaxDuration
	if burstDuration <= 0 {
		burstDuration = 30 * time.Second
	}

	cooldown := c.Cooldown
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}

	sustainedCount := c.SustainedCount
	if sustainedCount <= 0 {
		sustainedCount = 3
	}

	return sentinel.Config{
		PollInterval:   pollInterval,
		BaselineWindow: baselineWindow,
		MinSamples:     minSamples,
		SigmaThreshold: sigma,
		SustainedCount: sustainedCount,
		BurstDuration:  burstDuration,
		CooldownPeriod: cooldown,
	}
}

func (s *SentinelSkill) start(_ context.Context) (*skill.Result, error) {
	if s.sentinel != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     "already running",
			Rendered: "OG Sentinel 已在运行中",
			Summary:  "already running",
		}, nil
	}

	cfg := s.buildSentinelConfig()
	fast := sentinel.NewFastProbeCollector(s.driver)
	med := sentinel.NewMediumProbeCollector(s.driver)
	slow := sentinel.NewSlowProbeCollector(s.driver)

	snt := sentinel.New(cfg, fast, med, slow)
	s.sentinel = snt
	snt.Start(context.Background())

	return &skill.Result{
		Type: skill.ResultText,
		Data: "sentinel started",
		Rendered: fmt.Sprintf("OG Sentinel 哨兵模式已启动\n  探测: 15 项指标 (1s/10s/30s 三层探针)\n  触发: 自适应 3-sigma\n  基线: %d 样本\n  突发采集: 最长%v",
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
	fast := sentinel.NewFastProbeCollector(s.driver)
	med := sentinel.NewMediumProbeCollector(s.driver)
	slow := sentinel.NewSlowProbeCollector(s.driver)

	snt := sentinel.New(cfg, fast, med, slow)
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
			Rendered: "OG Sentinel 未在运行",
			Summary:  "not running",
		}, nil
	}

	s.sentinel.Stop()
	s.sentinel = nil

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     "sentinel stopped",
		Rendered: "OG Sentinel 已停止",
		Summary:  "sentinel stopped",
	}, nil
}

func (s *SentinelSkill) status() (*skill.Result, error) {
	if s.sentinel == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "OG Sentinel 状态: 未运行\n使用 /sentinel start 启动",
			Summary:  "not running",
		}, nil
	}

	state := s.sentinel.CurrentState()
	baseline := s.sentinel.CurrentBaseline()

	var b strings.Builder
	stateLabel := state.String()
	readyLabel := "否"
	if baseline.Ready {
		readyLabel = "是"
	}

	b.WriteString("OG Sentinel 哨兵状态\n\n")

	overviewQR := &db.QueryResult{
		Columns: []string{"项目", "值"},
		Rows: [][]any{
			{"运行状态", stateLabel},
			{"基线就绪", readyLabel},
			{"样本数", len(baseline.Samples)},
		},
	}
	b.WriteString(format.FormatTableOpts(overviewQR, format.TableOptions{MaxRows: 10, TermWidth: 120}))

	// Metric baselines.
	if baseline.Ready && len(baseline.Metrics) > 0 {
		order := []sentinel.MetricName{
			sentinel.MetricActiveSessions, sentinel.MetricIdleInTransaction,
			sentinel.MetricLockWaits, sentinel.MetricLongQueries,
			sentinel.MetricXactCommitRate, sentinel.MetricCacheHitPct,
			sentinel.MetricDeadTupleRatio, sentinel.MetricXIDAgeRatio,
			sentinel.MetricConnectionsPct, sentinel.MetricReplicationLag,
		}

		var metricRows [][]any
		for _, m := range order {
			bl, ok := baseline.Metrics[m]
			if !ok {
				continue
			}
			label := sentinel.MetricLabel(m)
			metricRows = append(metricRows, []any{
				label,
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
			b.WriteString(format.FormatTableOpts(metricQR, format.TableOptions{MaxRows: 15, TermWidth: 120}))
		}
	}

	// Latest anomaly.
	reportCount := s.sentinel.ReportCount()
	if reportCount > 0 {
		lr := s.sentinel.LastReport()
		if lr != nil {
			b.WriteString(fmt.Sprintf("\n最近一次异常 (共 %d 条记录):\n", reportCount))
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
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("state=%s samples=%d", state, len(baseline.Samples)),
	}, nil
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

// LastReport returns the most recent burst report, if any.
func (s *SentinelSkill) LastReport() *sentinel.BurstReport {
	if s.sentinel == nil {
		return nil
	}
	return s.sentinel.LastReport()
}

// ReportAt returns the report at 1-based index (1=newest).
func (s *SentinelSkill) ReportAt(idx int) *sentinel.BurstReport {
	if s.sentinel == nil {
		return nil
	}
	return s.sentinel.ReportAt(idx)
}

// ReportCount returns the number of stored reports.
func (s *SentinelSkill) ReportCount() int {
	if s.sentinel == nil {
		return 0
	}
	return s.sentinel.ReportCount()
}

// Reports returns a copy of all stored reports (newest last).
func (s *SentinelSkill) Reports() []*sentinel.BurstReport {
	if s.sentinel == nil {
		return nil
	}
	return s.sentinel.Reports()
}
