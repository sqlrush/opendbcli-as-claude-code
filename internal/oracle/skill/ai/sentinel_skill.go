/*-------------------------------------------------------------------------
 *
 * sentinel_skill.go
 *	  MetricBaselineData holds baseline info for one metric.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/ai/sentinel_skill.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// MetricBaselineData holds baseline info for one metric.
type MetricBaselineData struct {
	Name  string  `json:"name"`
	Label string  `json:"label"`
	Avg   float64 `json:"avg"`
	Std   float64 `json:"std"`
}

// LatestAnomalyData holds summary info about the most recent anomaly.
type LatestAnomalyData struct {
	Cause       string  `json:"cause"`
	Confidence  float64 `json:"confidence"`
	PeakActive  int     `json:"peak_active"`
	DurationSec float64 `json:"duration_sec"`
}

// SentinelStatusData is the structured output for sentinel status.
type SentinelStatusData struct {
	Running       bool                 `json:"running"`
	State         string               `json:"state"`
	BaselineReady bool                 `json:"baseline_ready"`
	SampleCount   int                  `json:"sample_count"`
	AvgActive     float64              `json:"avg_active"`
	StdActive     float64              `json:"std_active"`
	Metrics       []MetricBaselineData `json:"metrics,omitempty"`
	ReportCount   int                  `json:"report_count"`
	Latest        *LatestAnomalyData   `json:"latest,omitempty"`
}

// SentinelSkill starts/stops/shows the sentinel background anomaly detector.
type SentinelSkill struct {
	driver         db.Driver
	collector      *dbtop.Collector
	probeCollector *sentinel.ProbeCollector
	sentinel       *sentinel.Sentinel
	sentinelCfg    config.SentinelConfig

	// reports stores recent burst reports in a ring buffer (newest last).
	// Protected by mu because it's written by sentinel goroutine
	// and read by REPL goroutine.
	mu      sync.Mutex
	reports []*sentinel.BurstReport
}

// maxReports is the maximum number of burst reports kept in memory.
const maxReports = 16

// NewSentinelSkill creates a SentinelSkill backed by the given driver and collector.
func NewSentinelSkill(driver db.Driver, collector *dbtop.Collector, cfg config.SentinelConfig) *SentinelSkill {
	longSQLThreshold := cfg.LongSQLThresholdSec
	if longSQLThreshold <= 0 {
		longSQLThreshold = 30
	}
	return &SentinelSkill{
		driver:         driver,
		collector:      collector,
		probeCollector: sentinel.NewProbeCollector(driver, longSQLThreshold),
		sentinelCfg:    cfg,
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

	triggerMode := sentinel.TriggerAdaptive
	if c.TriggerMode == "fixed" {
		triggerMode = sentinel.TriggerFixed
	}

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

	burstInterval := c.BurstInterval
	if burstInterval <= 0 {
		burstInterval = 200 * time.Millisecond
	}

	burstDuration := c.BurstMaxDuration
	if burstDuration <= 0 {
		burstDuration = 30 * time.Second
	}

	burstCalmDelay := c.BurstCalmDelay
	if burstCalmDelay <= 0 {
		burstCalmDelay = 5 * time.Second
	}

	cooldown := c.Cooldown
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}

	t := c.Thresholds
	fixedThresholds := map[sentinel.MetricName]sentinel.MetricThreshold{
		sentinel.MetricActive:    {Enabled: t.ActiveMultiplier > 0, Multiplier: t.ActiveMultiplier},
		sentinel.MetricCPU:       {Enabled: t.CPUMultiplier > 0, Multiplier: t.CPUMultiplier},
		sentinel.MetricIO:        {Enabled: t.IOMultiplier > 0, Multiplier: t.IOMultiplier},
		sentinel.MetricLock:      {Enabled: t.LockAbsolute > 0, Absolute: t.LockAbsolute},
		sentinel.MetricLongSQL:   {Enabled: t.LongSQLAbsolute > 0, Absolute: t.LongSQLAbsolute},
		sentinel.MetricRedoRate:  {Enabled: t.RedoMultiplier > 0, Multiplier: t.RedoMultiplier},
		sentinel.MetricHardParse: {Enabled: t.HardParseMultiplier > 0, Multiplier: t.HardParseMultiplier},
	}

	sustainedCount := c.SustainedCount
	if sustainedCount <= 0 {
		sustainedCount = 3
	}

	// Hardware profile from the connected Oracle instance.
	var hw sentinel.HardwareProfile
	if s.driver != nil {
		info := s.driver.ServerInfo()
		hw = sentinel.HardwareProfile{
			CPUCores: info.CPUCores,
			MemoryGB: info.MemoryGB,
		}
	}

	longSQLThreshold := c.LongSQLThresholdSec
	if longSQLThreshold <= 0 {
		longSQLThreshold = 30
	}

	return sentinel.Config{
		PollInterval:        pollInterval,
		BaselineWindow:      baselineWindow,
		MinSamples:          minSamples,
		LongSQLThresholdSec: longSQLThreshold,
		TriggerMode:         triggerMode,
		SigmaThreshold:      sigma,
		SustainedCount:      sustainedCount,
		FixedThresholds:     fixedThresholds,
		Hardware:            hw,
		BurstInterval:       burstInterval,
		BurstDuration:       burstDuration,
		BurstCalmDelay:      burstCalmDelay,
		CooldownPeriod:      cooldown,
	}
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
	fullProbe := func(probeCtx context.Context) dbtop.Snapshot {
		return s.collector.Collect(probeCtx)
	}

	snt := sentinel.New(cfg, s.probeCollector, fullProbe, s.collector)

	// Wire up medium/slow tier probes.
	if s.driver != nil {
		snt.SetMediumProbe(sentinel.NewMediumProbeCollector(s.driver))
		snt.SetSlowProbe(sentinel.NewSlowProbeCollector(s.driver))
	}

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

	modeDesc := "自适应 3σ"
	if cfg.TriggerMode == sentinel.TriggerFixed {
		modeDesc = "固定阈值"
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     "sentinel started",
		Rendered: fmt.Sprintf("Sentinel 哨兵模式已启动\n  探测模式: 轻探针 (2 SQL/秒, 7 项指标)\n  触发模式: %s\n  基线窗口: %d 样本\n  突发采集: %v/帧, 最长%v",
			modeDesc, cfg.BaselineWindow, cfg.BurstInterval, cfg.BurstDuration),
		Summary: "sentinel started",
	}, nil
}

// AutoStart starts sentinel silently (for auto-start on login).
// Returns nil if already running or successfully started.
// The sentinel loop uses context.Background() so it outlives the caller's context.
func (s *SentinelSkill) AutoStart(_ context.Context) error {
	if s.sentinel != nil {
		return nil
	}

	cfg := s.buildSentinelConfig()
	fullProbe := func(probeCtx context.Context) dbtop.Snapshot {
		return s.collector.Collect(probeCtx)
	}

	snt := sentinel.New(cfg, s.probeCollector, fullProbe, s.collector)

	// Wire up medium/slow tier probes.
	if s.driver != nil {
		snt.SetMediumProbe(sentinel.NewMediumProbeCollector(s.driver))
		snt.SetSlowProbe(sentinel.NewSlowProbeCollector(s.driver))
	}

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

// StopSentinel stops the sentinel loop if running. Safe to call when not running.
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
		data := SentinelStatusData{Running: false}
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     &data,
			Rendered: "Sentinel 状态: 未运行\n使用 /sentinel start 启动",
			Summary:  "not running",
		}, nil
	}

	state := s.sentinel.CurrentState()
	baseline := s.sentinel.CurrentBaseline()

	statusData := SentinelStatusData{
		Running:       true,
		State:         string(state),
		BaselineReady: baseline.Ready,
		SampleCount:   len(baseline.Samples),
		AvgActive:     baseline.AvgActive,
		StdActive:     baseline.StdActive,
	}

	var b strings.Builder
	stateLabel := string(state)
	readyLabel := "否"
	if baseline.Ready {
		readyLabel = "是"
	}

	b.WriteString("Sentinel 哨兵状态\n\n")

	// Overview table.
	overviewQR := &db.QueryResult{
		Columns: []string{"项目", "值"},
		Rows: [][]any{
			{"运行状态", stateLabel},
			{"基线就绪", readyLabel},
			{"样本数", len(baseline.Samples)},
			{"平均活跃会话", fmt.Sprintf("%.1f", baseline.AvgActive)},
			{"标准差", fmt.Sprintf("%.1f", baseline.StdActive)},
		},
	}
	b.WriteString(format.FormatTableOpts(overviewQR, format.TableOptions{MaxRows: 10, TermWidth: 120}))

	// Metric baselines table.
	if baseline.Ready && len(baseline.Metrics) > 0 {
		order := []sentinel.MetricName{
			sentinel.MetricActive, sentinel.MetricCPU, sentinel.MetricIO,
			sentinel.MetricLock, sentinel.MetricLongSQL,
			sentinel.MetricRedoRate, sentinel.MetricHardParse,
		}
		labels := map[sentinel.MetricName]string{
			sentinel.MetricActive:    "活跃会话",
			sentinel.MetricCPU:       "CPU会话",
			sentinel.MetricIO:        "I/O等待",
			sentinel.MetricLock:      "锁等待",
			sentinel.MetricLongSQL:   "慢SQL",
			sentinel.MetricRedoRate:  "Redo KB/s",
			sentinel.MetricHardParse: "硬解析/s",
		}

		var metricRows [][]any
		for _, m := range order {
			bl, ok := baseline.Metrics[m]
			if !ok {
				continue
			}
			label := labels[m]
			metricRows = append(metricRows, []any{label, fmt.Sprintf("%.1f", bl.Avg), fmt.Sprintf("%.1f", bl.Std)})
			statusData.Metrics = append(statusData.Metrics, MetricBaselineData{
				Name:  string(m),
				Label: label,
				Avg:   bl.Avg,
				Std:   bl.Std,
			})
		}

		if len(metricRows) > 0 {
			b.WriteString("\n指标基线:\n")
			metricQR := &db.QueryResult{
				Columns: []string{"指标", "平均值", "标准差"},
				Rows:    metricRows,
			}
			b.WriteString(format.FormatTableOpts(metricQR, format.TableOptions{MaxRows: 10, TermWidth: 120}))
		}
	}

	// Latest anomaly.
	s.mu.Lock()
	n := len(s.reports)
	var lr *sentinel.BurstReport
	if n > 0 {
		lr = s.reports[n-1]
	}
	s.mu.Unlock()
	statusData.ReportCount = n
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
		statusData.Latest = &LatestAnomalyData{
			Cause:       lr.Classification.Cause.String(),
			Confidence:  lr.Classification.Confidence,
			PeakActive:  lr.PeakActive,
			DurationSec: lr.DurationSec,
		}
	}

	text := b.String()

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &statusData,
		Rendered: text,
		Summary:  fmt.Sprintf("state=%s samples=%d", state, len(baseline.Samples)),
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
// Returns nil if index is out of range.
func (s *SentinelSkill) ReportAt(oneBasedIdx int) *sentinel.BurstReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if oneBasedIdx < 1 || oneBasedIdx > len(s.reports) {
		return nil
	}
	// 1=newest → last element, 2=second newest, etc.
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
