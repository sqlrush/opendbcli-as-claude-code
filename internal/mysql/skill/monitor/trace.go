/*-------------------------------------------------------------------------
 *
 * trace.go
 *	  TraceSkill captures OS-level stack frames for mysqld and generates
 *	  a flame graph.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/trace.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/trace"
)

// TraceSkill captures OS-level stack frames for mysqld and generates a flame graph.
type TraceSkill struct {
	connHost  string
	traceCfg  *config.TraceConfig
	collector *trace.Collector
}

// NewTraceSkill creates a TraceSkill for the given connection host and trace config.
func NewTraceSkill(connHost string, traceCfg *config.TraceConfig) *TraceSkill {
	return &TraceSkill{
		connHost:  connHost,
		traceCfg:  traceCfg,
		collector: &trace.Collector{},
	}
}

func (s *TraceSkill) Name() string                       { return "trace" }
func (s *TraceSkill) Description() string                { return "OS 堆栈采集 + 火焰图分析 (MySQL)" }
func (s *TraceSkill) Category() string                   { return "monitor" }
func (s *TraceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TraceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "trace",
		Description: "采集 MySQL 进程 OS 堆栈，生成火焰图，返回热点函数和折叠栈帧",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration": map[string]any{
					"type":        "integer",
					"description": "采集秒数 (1-10, 默认 3)",
				},
			},
		},
	}
}

func (s *TraceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "trace",
		Usage:    "/trace [duration]",
		Examples: []string{"/trace", "/trace 5"},
	}
}

func (s *TraceSkill) Validate(params skill.Params) error {
	dur := params.IntOr("duration", 3)
	if dur < 1 || dur > 10 {
		return fmt.Errorf("采集时长范围 1-10 秒 (当前: %d)", dur)
	}
	return nil
}

func (s *TraceSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	pid, err := trace.IsLocal(ctx, "mysql", s.connHost)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("trace 不可用: %s", err),
			Summary:  "trace unavailable",
		}, nil
	}

	dur := params.IntOr("duration", s.defaultDuration())
	outDir := s.outputDir()
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("无法创建输出目录: %s", err),
			Summary:  "mkdir failed",
		}, nil
	}

	opts := trace.CaptureOpts{
		PID:      pid,
		Duration: time.Duration(dur) * time.Second,
		TopN:     s.topN(),
		OutDir:   outDir,
		Freq:     99,
	}
	result, err := s.collector.Capture(ctx, opts)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("采集失败: %s", err),
			Summary:  "capture failed",
		}, nil
	}
	result.DBType = "mysql"

	sources := s.lookupSources(result.TopFuncs)

	rendered := s.formatOutput(result, sources)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("trace: PID %d, %ds, %d hot funcs", pid, dur, len(result.TopFuncs)),
	}, nil
}

func (s *TraceSkill) formatOutput(result *trace.TraceResult, sources []trace.FuncSource) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OS 堆��分析 (mysqld PID %d, %ds, %dHz)\n\n", result.PID, result.Duration, 99)
	b.WriteString(trace.FormatTopFuncsTable(result.TopFuncs))
	b.WriteString("\n")
	b.WriteString(trace.FormatTextFlame(result.Collapsed, 80))
	fmt.Fprintf(&b, "\n  详细火焰图(浏览器打开): %s\n", result.SVGPath)
	if len(sources) > 0 {
		b.WriteString("\n  源码片段:\n")
		for _, src := range sources {
			fmt.Fprintf(&b, "\n  ── %s (%s:%d) ──\n%s\n", src.FuncName, src.FilePath, src.Line, src.Snippet)
		}
	}
	return b.String()
}

func (s *TraceSkill) defaultDuration() int {
	if s.traceCfg != nil && s.traceCfg.Duration > 0 {
		return s.traceCfg.Duration
	}
	return 3
}

func (s *TraceSkill) topN() int {
	if s.traceCfg != nil && s.traceCfg.TopN > 0 {
		return s.traceCfg.TopN
	}
	return 20
}

func (s *TraceSkill) outputDir() string {
	if s.traceCfg != nil && s.traceCfg.OutDir != "" {
		return s.traceCfg.OutDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".opendb", "trace")
}

func (s *TraceSkill) lookupSources(funcs []trace.HotFunc) []trace.FuncSource {
	if s.traceCfg == nil {
		return nil
	}
	src := s.traceCfg.SourceFor("mysql")
	// Priority 1: local source directory.
	if src.Dir != "" {
		lookup := &trace.SourceLookup{SourceDir: src.Dir}
		results, _ := lookup.Grep(funcs)
		if len(results) > 0 {
			return results
		}
	}
	// Priority 2: GitHub API search.
	if src.Repo != "" {
		gh := &trace.GitHubLookup{
			Repo:   src.Repo,
			Branch: src.Branch,
			Token:  src.Token,
		}
		results, _ := gh.Grep(funcs)
		return results
	}
	return nil
}
