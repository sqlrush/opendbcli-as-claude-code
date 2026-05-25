/*-------------------------------------------------------------------------
 *
 * collector.go
 *	  Collector orchestrates the full capture pipeline: perf record →
 *	  perf script → collapse → SVG → extract top funcs.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/collector.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/osutil"
)

// Collector orchestrates the full capture pipeline:
// perf record → perf script → collapse → SVG → extract top funcs.
type Collector struct{}

// Capture runs the full stack trace capture pipeline for a given process.
func (c *Collector) Capture(ctx context.Context, opts CaptureOpts) (*TraceResult, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("trace 仅支持 Linux 平台 (当前: %s)", runtime.GOOS)
	}
	if err := c.validate(opts); err != nil {
		return nil, err
	}

	// Apply defaults
	if opts.Freq == 0 {
		opts.Freq = 99
	}
	if opts.TopN == 0 {
		opts.TopN = 20
	}
	durSec := int(opts.Duration.Seconds())
	if durSec < 1 {
		durSec = 1
	}

	// Step 1: perf record
	perfDataPath := filepath.Join(opts.OutDir, fmt.Sprintf("perf-%d.data", opts.PID))
	_, err := osutil.RunWithTimeout(ctx, opts.Duration+10*time.Second,
		"perf", "record", "-F", strconv.Itoa(opts.Freq),
		"-g", "-p", strconv.Itoa(opts.PID),
		"-o", perfDataPath,
		"--", "sleep", strconv.Itoa(durSec))
	if err != nil {
		return nil, fmt.Errorf("perf record 失败: %w (需要 root 权限或 CAP_SYS_ADMIN)", err)
	}
	defer os.Remove(perfDataPath)

	// Step 2: perf script
	rawScript, err := osutil.RunWithTimeout(ctx, 30*time.Second,
		"perf", "script", "-i", perfDataPath)
	if err != nil {
		return nil, fmt.Errorf("perf script 失败: %w", err)
	}

	// Step 3: Collapse stacks
	collapsed := CollapseStacks(string(rawScript))
	if collapsed == "" {
		return nil, fmt.Errorf("采集到的堆栈为空，可能进程在采集期间没有活动")
	}

	// Step 4: Generate SVG
	ts := time.Now()
	svgName := fmt.Sprintf("flame-%s.svg", ts.Format("20060102-150405"))
	svgPath := filepath.Join(opts.OutDir, svgName)
	title := fmt.Sprintf("PID %d, %ds @ %dHz", opts.PID, durSec, opts.Freq)
	if err := GenerateSVG(collapsed, svgPath, title); err != nil {
		return nil, fmt.Errorf("生成火焰图失败: %w", err)
	}

	// Step 5: Extract top functions
	topFuncs := ExtractTopFuncs(collapsed, opts.TopN)

	return &TraceResult{
		Collapsed: collapsed,
		SVGPath:   svgPath,
		TopFuncs:  topFuncs,
		PID:       opts.PID,
		Duration:  durSec,
		Timestamp: ts,
		RawScript: string(rawScript),
	}, nil
}

// validate checks that CaptureOpts are valid before starting capture.
func (c *Collector) validate(opts CaptureOpts) error {
	if opts.PID <= 0 {
		return fmt.Errorf("invalid PID: %d", opts.PID)
	}
	if opts.Duration > 10*time.Second {
		return fmt.Errorf("采集时长不能超过 10 秒 (当前: %v)", opts.Duration)
	}
	if opts.OutDir == "" {
		return fmt.Errorf("output directory is required")
	}
	return nil
}

// FormatTopFuncsTable renders a HotFunc slice as a human-readable table string.
func FormatTopFuncsTable(funcs []HotFunc) string {
	if len(funcs) == 0 {
		return "  (no hot functions detected)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %-4s %-50s %8s %7s\n", "#", "函数", "采样数", "占比"))
	b.WriteString(fmt.Sprintf("  %-4s %-50s %8s %7s\n",
		strings.Repeat("─", 4), strings.Repeat("─", 50),
		strings.Repeat("─", 8), strings.Repeat("─", 7)))
	for i, f := range funcs {
		name := f.Name
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		b.WriteString(fmt.Sprintf("  %-4d %-50s %8d %6.1f%%\n",
			i+1, name, f.Samples, f.Percentage))
	}
	return b.String()
}
