/*-------------------------------------------------------------------------
 *
 * types.go
 *	  TraceResult holds the output of a single stack trace capture
 *	  session.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/types.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import "time"

// TraceResult holds the output of a single stack trace capture session.
type TraceResult struct {
	Collapsed string    `json:"collapsed"`
	SVGPath   string    `json:"svg_path"`
	TopFuncs  []HotFunc `json:"top_funcs"`
	PID       int       `json:"pid"`
	Duration  int       `json:"duration"`
	DBType    string    `json:"db_type"`
	Timestamp time.Time `json:"timestamp"`
	RawScript string    `json:"-"`
}

// HotFunc represents a hot function extracted from collapsed stack frames.
type HotFunc struct {
	Name       string  `json:"name"`
	Samples    int     `json:"samples"`
	Percentage float64 `json:"percentage"`
	Stack      string  `json:"stack"`
}

// CaptureOpts configures a stack trace capture session.
type CaptureOpts struct {
	PID      int
	Duration time.Duration
	TopN     int
	OutDir   string
	Freq     int
}

// DefaultCaptureOpts returns CaptureOpts with sensible defaults.
func DefaultCaptureOpts(pid int, outDir string) CaptureOpts {
	return CaptureOpts{
		PID:      pid,
		Duration: 3 * time.Second,
		TopN:     20,
		OutDir:   outDir,
		Freq:     99,
	}
}

// FuncSource holds a source code snippet for a hot function.
type FuncSource struct {
	FuncName string `json:"func_name"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Snippet  string `json:"snippet"`
}
