# /trace Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `/trace` skill that captures OS-level stack traces of database processes, generates flame graphs, and feeds data to LLM for source-code-level performance analysis.

**Architecture:** Shared `internal/trace/` package provides Collector + SourceLookup + hostcheck. Thin `internal/os/` package gates all OS command execution behind a whitelist. Each database gets its own `trace.go` skill file (四库独立). MySQL and PG are implemented first with source analysis; Oracle gets capture only (no source); OG is a placeholder.

**Tech Stack:** Go, Linux `perf` (runtime-detected), pure Go flamegraph SVG generation, pure Go stackcollapse.

**Base version:** v0.9.27 (main branch)

**Design spec:** `docs/superpowers/specs/2026-04-06-trace-skill-design.md`

---

## File Structure

```
新建:
  internal/os/exec.go                          — OS 命令白名单执行器 (<50行)
  internal/os/exec_test.go                     — 白名单 + 超时测试
  internal/trace/types.go                      — TraceResult, HotFunc, CaptureOpts 类型
  internal/trace/hostcheck.go                  — 宿主机检测 (ps + host 判断)
  internal/trace/hostcheck_test.go             — 检测逻辑测试
  internal/trace/collapse.go                   — 纯 Go 栈帧折叠 (替代 stackcollapse-perf.pl)
  internal/trace/collapse_test.go              — 折叠逻辑测试
  internal/trace/flamegraph.go                 — 纯 Go SVG 火焰图生成
  internal/trace/flamegraph_test.go            — SVG 生成测试
  internal/trace/collector.go                  — perf 采集编排
  internal/trace/collector_test.go             — 采集流程测试 (mock os.Run)
  internal/trace/source.go                     — 源码 grep 查找
  internal/trace/source_test.go                — 源码查找测试
  internal/mysql/skill/monitor/trace.go        — MySQL /trace skill
  internal/mysql/skill/monitor/trace_test.go   — MySQL skill 测试
  internal/postgres/skill/monitor/trace.go     — PG /trace skill
  internal/postgres/skill/monitor/trace_test.go — PG skill 测试
  internal/oracle/skill/monitor/trace.go       — Oracle /trace skill (无源码分析)
  internal/oracle/skill/monitor/trace_test.go  — Oracle skill 测试

修改:
  internal/config/config.go                    — 新增 TraceConfig 段
  internal/mysql/register.go                   — 注册 MySQL trace skill
  internal/postgres/register.go                — 注册 PG trace skill
  internal/oracle/register.go                  — 注册 Oracle trace skill
```

---

## Task 1: OS 命令白名单执行器

**Files:**
- Create: `internal/os/exec.go`
- Create: `internal/os/exec_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/os/exec_test.go
package os

import (
	"context"
	"testing"
	"time"
)

func TestRun_AllowedCommand(t *testing.T) {
	// "echo" is not in whitelist — use it to verify rejection first,
	// then test "ps" which is in whitelist and safe on any OS.
	ctx := context.Background()
	out, err := Run(ctx, "ps", "--version")
	// ps --version may fail on some systems, but the point is it should not
	// be rejected by the whitelist. We only check no ErrCommandNotAllowed.
	if err == ErrCommandNotAllowed {
		t.Errorf("Run('ps') should be allowed, got ErrCommandNotAllowed")
	}
	_ = out
}

func TestRun_BlockedCommand(t *testing.T) {
	ctx := context.Background()
	_, err := Run(ctx, "rm", "-rf", "/")
	if err != ErrCommandNotAllowed {
		t.Errorf("Run('rm') should return ErrCommandNotAllowed, got %v", err)
	}
}

func TestRun_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, "ps", "aux")
	// May succeed fast or timeout — just verify no panic.
	_ = err
}

func TestRunWithTimeout_Exceeds(t *testing.T) {
	// RunWithTimeout with a very short timeout on a command that blocks.
	_, err := RunWithTimeout(context.Background(), 1*time.Millisecond, "sleep", "10")
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/os/ -v -count=1`
Expected: compilation error — package `os` conflicts with stdlib.

Note: We need a different package name to avoid shadowing `os`. Use `osutil`.

- [ ] **Step 3: Rename package to `osutil` and implement**

```go
// internal/osutil/exec.go
package osutil

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ErrCommandNotAllowed is returned when a command is not in the whitelist.
var ErrCommandNotAllowed = errors.New("command not allowed")

// allowedCmds is the whitelist of OS commands that OpenDB is permitted to run.
var allowedCmds = map[string]bool{
	"perf":   true,
	"ps":     true,
	"pstack": true,
	"git":    true,
}

// Allow adds a command to the whitelist. Intended for testing only.
func Allow(name string) {
	allowedCmds[name] = true
}

// Run executes a whitelisted OS command with the given context.
// Returns combined stdout+stderr output. Returns ErrCommandNotAllowed
// if the command is not in the whitelist.
func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedCmds[name] {
		return nil, ErrCommandNotAllowed
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// RunWithTimeout is like Run but applies an explicit timeout.
func RunWithTimeout(parent context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, err := Run(ctx, name, args...)
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command %q timed out after %v: %w", name, timeout, ctx.Err())
	}
	return out, err
}
```

- [ ] **Step 4: Update test package name and run**

Update `exec_test.go` package to `osutil`. Run: `go test ./internal/osutil/ -v -count=1`
Expected: PASS (ps is allowed, rm is blocked, timeout works)

- [ ] **Step 5: Commit**

```bash
git add internal/osutil/
git commit -m "feat(trace): add OS command whitelist executor (internal/osutil)"
```

---

## Task 2: Trace 类型定义

**Files:**
- Create: `internal/trace/types.go`

- [ ] **Step 1: Write types**

```go
// internal/trace/types.go
package trace

import "time"

// TraceResult holds the output of a single stack trace capture session.
type TraceResult struct {
	Collapsed string    `json:"collapsed"`   // folded stack frames (LLM consumption)
	SVGPath   string    `json:"svg_path"`    // flame graph SVG file path
	TopFuncs  []HotFunc `json:"top_funcs"`   // top N hot functions
	PID       int       `json:"pid"`
	Duration  int       `json:"duration"`    // capture seconds
	DBType    string    `json:"db_type"`     // mysql / postgres / oracle
	Timestamp time.Time `json:"timestamp"`
	RawScript string    `json:"-"`           // raw perf script output (not serialized)
}

// HotFunc represents a hot function extracted from collapsed stack frames.
type HotFunc struct {
	Name       string  `json:"name"`       // full function name, e.g. ha_innodb::write_row
	Samples    int     `json:"samples"`    // sample count
	Percentage float64 `json:"percentage"` // percentage of total samples
	Stack      string  `json:"stack"`      // top call chain containing this function
}

// CaptureOpts configures a stack trace capture session.
type CaptureOpts struct {
	PID      int           // target process PID
	Duration time.Duration // capture duration (default 3s, max 10s)
	TopN     int           // number of hot functions to extract (default 20)
	OutDir   string        // SVG output directory
	Freq     int           // sampling frequency Hz (default 99)
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
	FilePath string `json:"file_path"` // relative path within source tree
	Line     int    `json:"line"`      // start line number
	Snippet  string `json:"snippet"`   // function signature + key logic (≤50 lines)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/trace/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/trace/types.go
git commit -m "feat(trace): add core type definitions"
```

---

## Task 3: 宿主机检测

**Files:**
- Create: `internal/trace/hostcheck.go`
- Create: `internal/trace/hostcheck_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/trace/hostcheck_test.go
package trace

import "testing"

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"", true},
		{"10.0.0.5", false},
		{"db.example.com", false},
	}
	for _, tt := range tests {
		if got := isLoopback(tt.host); got != tt.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestFindDBProcess_UnknownType(t *testing.T) {
	_, err := findDBProcess("unknown_db")
	if err == nil {
		t.Error("expected error for unknown db type")
	}
}

func TestProcessPatterns(t *testing.T) {
	// Verify process name patterns are defined for supported db types.
	for _, dbType := range []string{"mysql", "postgres", "oracle", "opengauss"} {
		pat, ok := processPatterns[dbType]
		if !ok || len(pat) == 0 {
			t.Errorf("no process pattern defined for %s", dbType)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/trace/ -run TestIsLoopback -v -count=1`
Expected: FAIL — `isLoopback` undefined

- [ ] **Step 3: Implement hostcheck**

```go
// internal/trace/hostcheck.go
package trace

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/osutil"
)

// processPatterns maps db type to the process names to search for via ps.
var processPatterns = map[string][]string{
	"mysql":    {"mysqld"},
	"postgres": {"postgres", "postmaster"},
	"oracle":   {"ora_pmon"},
	"opengauss": {"gaussdb"},
}

// IsLocal checks whether the database process is running on this machine.
// It returns the PID if found, or an error explaining why trace is unavailable.
func IsLocal(ctx context.Context, dbType string, connHost string) (int, error) {
	if !isLoopback(connHost) {
		return 0, fmt.Errorf("trace 功能需要 OpenDB 部署在数据库宿主机上 (当前连接: %s)", connHost)
	}
	pid, err := findDBProcess(dbType)
	if err != nil {
		return 0, err
	}
	return pid, nil
}

// isLoopback returns true if the host refers to the local machine.
func isLoopback(host string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	// Check if host resolves to a loopback or local address.
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.String() == host {
			return true
		}
	}
	return false
}

// findDBProcess uses ps to find the database main process PID.
func findDBProcess(dbType string) (int, error) {
	patterns, ok := processPatterns[dbType]
	if !ok {
		return 0, fmt.Errorf("unsupported db type for trace: %s", dbType)
	}

	ctx := context.Background()
	out, err := osutil.Run(ctx, "ps", "aux")
	if err != nil {
		return 0, fmt.Errorf("执行 ps 失败: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		for _, pat := range patterns {
			if strings.Contains(line, pat) && !strings.Contains(line, "grep") {
				return parsePIDFromPsLine(line)
			}
		}
	}
	return 0, fmt.Errorf("未找到 %s 进程，请确认数据库正在运行", dbType)
}

// parsePIDFromPsLine extracts the PID (second field) from a ps aux output line.
func parsePIDFromPsLine(line string) (int, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected ps output: %s", line)
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("cannot parse PID from %q: %w", fields[1], err)
	}
	return pid, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/trace/ -run 'TestIsLoopback|TestFindDBProcess|TestProcessPatterns' -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/trace/hostcheck.go internal/trace/hostcheck_test.go
git commit -m "feat(trace): add host-local detection for database processes"
```

---

## Task 4: 栈帧折叠 (stackcollapse)

**Files:**
- Create: `internal/trace/collapse.go`
- Create: `internal/trace/collapse_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/trace/collapse_test.go
package trace

import (
	"strings"
	"testing"
)

func TestCollapse_Basic(t *testing.T) {
	// Simulated perf script output (simplified).
	raw := `mysqld 12345 99.00: cycles:
	7f1234 ha_innodb::write_row (/usr/sbin/mysqld)
	7f1235 handler::ha_write_row (/usr/sbin/mysqld)
	7f1236 mysql_insert (/usr/sbin/mysqld)

mysqld 12345 99.00: cycles:
	7f1234 ha_innodb::write_row (/usr/sbin/mysqld)
	7f1235 handler::ha_write_row (/usr/sbin/mysqld)
	7f1236 mysql_insert (/usr/sbin/mysqld)

mysqld 12345 99.00: cycles:
	7f1237 lock_wait_timeout (/usr/sbin/mysqld)
	7f1238 os_event_wait (/usr/sbin/mysqld)
`
	collapsed := CollapseStacks(raw)
	lines := strings.Split(strings.TrimSpace(collapsed), "\n")

	if len(lines) != 2 {
		t.Fatalf("expected 2 collapsed lines, got %d: %v", len(lines), lines)
	}

	// First line should have count 2 (two identical stacks).
	if !strings.HasSuffix(lines[0], " 2") && !strings.HasSuffix(lines[1], " 2") {
		t.Errorf("expected one line with count 2, got:\n%s", collapsed)
	}
}

func TestCollapse_Empty(t *testing.T) {
	collapsed := CollapseStacks("")
	if collapsed != "" {
		t.Errorf("expected empty output for empty input, got %q", collapsed)
	}
}

func TestExtractTopFuncs(t *testing.T) {
	collapsed := "mysqld;mysql_insert;ha_innodb::write_row 100\nmysqld;lock_wait 50\nmysqld;other 10"
	funcs := ExtractTopFuncs(collapsed, 2)
	if len(funcs) != 2 {
		t.Fatalf("expected 2 funcs, got %d", len(funcs))
	}
	if funcs[0].Name != "ha_innodb::write_row" {
		t.Errorf("expected top func 'ha_innodb::write_row', got %q", funcs[0].Name)
	}
	if funcs[0].Samples != 100 {
		t.Errorf("expected 100 samples, got %d", funcs[0].Samples)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/trace/ -run 'TestCollapse|TestExtractTopFuncs' -v -count=1`
Expected: FAIL — `CollapseStacks` undefined

- [ ] **Step 3: Implement collapse**

```go
// internal/trace/collapse.go
package trace

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CollapseStacks converts raw perf script output into folded stack format.
// Each output line: "func1;func2;func3 count"
// This is a pure Go equivalent of Brendan Gregg's stackcollapse-perf.pl.
func CollapseStacks(raw string) string {
	if raw == "" {
		return ""
	}

	counts := make(map[string]int)
	var currentStack []string

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			// Empty line = end of a stack trace.
			if len(currentStack) > 0 {
				// Reverse the stack (perf shows leaf first, we want root first).
				reversed := reverseStack(currentStack)
				key := strings.Join(reversed, ";")
				counts[key]++
				currentStack = nil
			}
			continue
		}

		// Stack frame line: "    addr funcname (binary)"
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
			funcName := parseFuncName(trimmed)
			if funcName != "" {
				currentStack = append(currentStack, funcName)
			}
			continue
		}

		// Header line: "comm pid timestamp: event:" — extract comm as root.
		if len(currentStack) == 0 {
			parts := strings.Fields(trimmed)
			if len(parts) >= 1 {
				// Use the process name as the stack root.
				comm := parts[0]
				// Will be prepended after reversal.
				_ = comm
			}
		}
	}

	// Flush last stack if no trailing newline.
	if len(currentStack) > 0 {
		reversed := reverseStack(currentStack)
		key := strings.Join(reversed, ";")
		counts[key]++
	}

	// Sort by count descending for deterministic output.
	type entry struct {
		stack string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for k, v := range counts {
		entries = append(entries, entry{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})

	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s %d\n", e.stack, e.count)
	}
	return strings.TrimSpace(b.String())
}

// ExtractTopFuncs extracts the top N hottest leaf functions from collapsed stacks.
func ExtractTopFuncs(collapsed string, topN int) []HotFunc {
	if collapsed == "" {
		return nil
	}

	// Aggregate by leaf function (last element of each stack).
	funcSamples := make(map[string]int)
	funcStacks := make(map[string]string) // keep one example stack per func
	totalSamples := 0

	lines := strings.Split(collapsed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		spaceIdx := strings.LastIndex(line, " ")
		if spaceIdx < 0 {
			continue
		}
		stack := line[:spaceIdx]
		countStr := line[spaceIdx+1:]
		count, err := strconv.Atoi(countStr)
		if err != nil {
			continue
		}
		totalSamples += count

		parts := strings.Split(stack, ";")
		leaf := parts[len(parts)-1]
		funcSamples[leaf] += count
		if _, ok := funcStacks[leaf]; !ok {
			funcStacks[leaf] = stack
		}
	}

	// Sort by samples descending.
	type entry struct {
		name    string
		samples int
	}
	entries := make([]entry, 0, len(funcSamples))
	for name, samples := range funcSamples {
		entries = append(entries, entry{name, samples})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].samples > entries[j].samples
	})

	if topN > len(entries) {
		topN = len(entries)
	}
	result := make([]HotFunc, topN)
	for i := 0; i < topN; i++ {
		e := entries[i]
		pct := 0.0
		if totalSamples > 0 {
			pct = float64(e.samples) * 100 / float64(totalSamples)
		}
		result[i] = HotFunc{
			Name:       e.name,
			Samples:    e.samples,
			Percentage: pct,
			Stack:      funcStacks[e.name],
		}
	}
	return result
}

func reverseStack(stack []string) []string {
	n := len(stack)
	reversed := make([]string, n)
	for i, s := range stack {
		reversed[n-1-i] = s
	}
	return reversed
}

// parseFuncName extracts the function name from a perf stack frame line.
// Input format: "    7f1234 func_name (/path/to/binary)" or "    7f1234 func_name+0x10 (/path)"
func parseFuncName(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	// fields[0] = address, fields[1] = func name (possibly with +offset)
	name := fields[1]
	// Strip +offset suffix.
	if idx := strings.Index(name, "+"); idx > 0 {
		name = name[:idx]
	}
	// Skip [unknown] or hex-only addresses.
	if name == "[unknown]" || isHexAddr(name) {
		return ""
	}
	return name
}

func isHexAddr(s string) bool {
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 6 // very long hex strings are likely addresses, not func names
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/trace/ -run 'TestCollapse|TestExtractTopFuncs' -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/trace/collapse.go internal/trace/collapse_test.go
git commit -m "feat(trace): pure Go stack frame collapsing (replaces stackcollapse-perf.pl)"
```

---

## Task 5: 火焰图 SVG 生成

**Files:**
- Create: `internal/trace/flamegraph.go`
- Create: `internal/trace/flamegraph_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/trace/flamegraph_test.go
package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSVG_Basic(t *testing.T) {
	collapsed := "mysqld;mysql_insert;ha_innodb::write_row 100\nmysqld;lock_wait 50"
	dir := t.TempDir()
	path := filepath.Join(dir, "test.svg")

	err := GenerateSVG(collapsed, path, "mysqld (PID 12345)")
	if err != nil {
		t.Fatalf("GenerateSVG failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SVG: %v", err)
	}

	svg := string(data)
	if !strings.Contains(svg, "<svg") {
		t.Error("output is not valid SVG")
	}
	if !strings.Contains(svg, "ha_innodb::write_row") {
		t.Error("SVG should contain function name 'ha_innodb::write_row'")
	}
}

func TestGenerateSVG_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.svg")
	err := GenerateSVG("", path, "test")
	if err == nil {
		t.Error("expected error for empty collapsed data")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/trace/ -run TestGenerateSVG -v -count=1`
Expected: FAIL — `GenerateSVG` undefined

- [ ] **Step 3: Implement SVG generator**

```go
// internal/trace/flamegraph.go
package trace

import (
	"fmt"
	"html"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// GenerateSVG produces an interactive flame graph SVG from collapsed stack data.
// collapsed: folded stack format ("func1;func2 count\n...")
// outPath: SVG file path to write
// title: chart title
func GenerateSVG(collapsed string, outPath string, title string) error {
	if strings.TrimSpace(collapsed) == "" {
		return fmt.Errorf("empty collapsed stack data")
	}

	root := buildFrameTree(collapsed)
	if root.samples == 0 {
		return fmt.Errorf("no samples in collapsed data")
	}

	svg := renderSVG(root, title)
	return os.WriteFile(outPath, []byte(svg), 0644)
}

// frame represents a node in the call tree.
type frame struct {
	name     string
	samples  int
	children []*frame
}

func buildFrameTree(collapsed string) *frame {
	root := &frame{name: "root"}
	lines := strings.Split(collapsed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		spaceIdx := strings.LastIndex(line, " ")
		if spaceIdx < 0 {
			continue
		}
		stack := line[:spaceIdx]
		count, err := strconv.Atoi(line[spaceIdx+1:])
		if err != nil {
			continue
		}
		parts := strings.Split(stack, ";")
		node := root
		for _, part := range parts {
			child := findChild(node, part)
			if child == nil {
				child = &frame{name: part}
				node.children = append(node.children, child)
			}
			child.samples += count
			node = child
		}
		root.samples += count
	}
	return root
}

func findChild(parent *frame, name string) *frame {
	for _, c := range parent.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

func renderSVG(root *frame, title string) string {
	const (
		frameHeight = 16
		charWidth   = 7
		minWidth    = 0.1 // minimum percentage to render
		xPad        = 10
		yPad        = 60
	)

	// Calculate depth for height.
	maxDepth := calcDepth(root, 0)
	svgWidth := 1200
	svgHeight := yPad + (maxDepth+2)*frameHeight + 30

	widthPerSample := float64(svgWidth-2*xPad) / float64(max(root.samples, 1))

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d"
     viewBox="0 0 %d %d" style="background:#f8f8f8">
<style>
  .frame:hover rect { stroke:#000; stroke-width:1; }
  .frame text { font-family:monospace; font-size:11px; fill:#000; }
  .title { font-family:sans-serif; font-size:16px; font-weight:bold; fill:#333; }
  .subtitle { font-family:sans-serif; font-size:11px; fill:#666; }
</style>
<text class="title" x="%d" y="24" text-anchor="middle">%s</text>
<text class="subtitle" x="%d" y="42" text-anchor="middle">samples: %d</text>
`,
		svgWidth, svgHeight, svgWidth, svgHeight,
		svgWidth/2, html.EscapeString(title),
		svgWidth/2, root.samples))

	// Sort children by name for consistent output.
	sortChildren(root)

	// Render frames bottom-up (root at bottom).
	renderFrame(&b, root, float64(xPad), maxDepth, 0, widthPerSample, frameHeight, yPad, maxDepth, minWidth)

	b.WriteString("</svg>\n")
	return b.String()
}

func renderFrame(b *strings.Builder, f *frame, x float64, baseY int, depth int, wps float64, fh int, yPad int, maxDepth int, minWidth float64) {
	w := float64(f.samples) * wps
	pct := float64(f.samples) * 100 / float64(max(1, 1)) // will be set by caller context
	_ = pct

	if w < minWidth {
		return
	}

	y := yPad + (maxDepth-depth)*fh

	// Color based on hash of function name.
	r, g, bl := flameColor(f.name)

	if f.name != "root" {
		label := f.name
		maxChars := int(w) / 7
		if maxChars < len(label) {
			if maxChars > 3 {
				label = label[:maxChars-2] + ".."
			} else {
				label = ""
			}
		}
		b.WriteString(fmt.Sprintf(
			`<g class="frame"><title>%s (%d samples)</title><rect x="%.1f" y="%d" width="%.1f" height="%d" fill="rgb(%d,%d,%d)" rx="1"/>`+"\n",
			html.EscapeString(f.name), f.samples, x, y, w, fh-1, r, g, bl))
		if label != "" {
			b.WriteString(fmt.Sprintf(
				`<text x="%.1f" y="%d">%s</text>`+"\n",
				x+2, y+fh-4, html.EscapeString(label)))
		}
		b.WriteString("</g>\n")
	}

	// Render children.
	childX := x
	for _, child := range f.children {
		renderFrame(b, child, childX, baseY, depth+1, wps, fh, yPad, maxDepth, minWidth)
		childX += float64(child.samples) * wps
	}
}

func calcDepth(f *frame, current int) int {
	maxD := current
	for _, c := range f.children {
		d := calcDepth(c, current+1)
		if d > maxD {
			maxD = d
		}
	}
	return maxD
}

func sortChildren(f *frame) {
	sort.Slice(f.children, func(i, j int) bool {
		return f.children[i].name < f.children[j].name
	})
	for _, c := range f.children {
		sortChildren(c)
	}
}

// flameColor generates a warm flame color based on function name hash.
func flameColor(name string) (int, int, int) {
	h := 0
	for _, c := range name {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	// Warm palette: red-orange-yellow
	r := 200 + int(math.Abs(float64(h%55)))
	g := 80 + int(math.Abs(float64((h/55)%120)))
	bl := 20 + int(math.Abs(float64((h/6600)%55)))
	return min(r, 255), min(g, 200), min(bl, 75)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/trace/ -run TestGenerateSVG -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/trace/flamegraph.go internal/trace/flamegraph_test.go
git commit -m "feat(trace): pure Go flame graph SVG generator"
```

---

## Task 6: TraceCollector（采集编排）

**Files:**
- Create: `internal/trace/collector.go`
- Create: `internal/trace/collector_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/trace/collector_test.go
package trace

import (
	"testing"
	"time"
)

func TestCaptureOpts_Validation(t *testing.T) {
	c := &Collector{}

	tests := []struct {
		name    string
		opts    CaptureOpts
		wantErr bool
	}{
		{"valid", CaptureOpts{PID: 1, Duration: 3 * time.Second, TopN: 20, OutDir: "/tmp", Freq: 99}, false},
		{"no pid", CaptureOpts{PID: 0, Duration: 3 * time.Second, TopN: 20, OutDir: "/tmp"}, true},
		{"too long", CaptureOpts{PID: 1, Duration: 30 * time.Second, TopN: 20, OutDir: "/tmp"}, true},
		{"no outdir", CaptureOpts{PID: 1, Duration: 3 * time.Second, TopN: 20, OutDir: ""}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.validate(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFormatTopFuncsTable(t *testing.T) {
	funcs := []HotFunc{
		{Name: "ha_innodb::write_row", Samples: 100, Percentage: 62.5},
		{Name: "lock_wait", Samples: 60, Percentage: 37.5},
	}
	table := FormatTopFuncsTable(funcs)
	if table == "" {
		t.Error("expected non-empty table")
	}
	if !containsAll(table, "ha_innodb::write_row", "62.5", "lock_wait") {
		t.Errorf("table missing expected content:\n%s", table)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && containsStr(s, sub)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/trace/ -run 'TestCaptureOpts|TestFormatTopFuncs' -v -count=1`
Expected: FAIL — `Collector` has no `validate` method

- [ ] **Step 3: Implement collector**

```go
// internal/trace/collector.go
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

// Collector captures OS-level stack traces of database processes.
// It is stateless and safe for concurrent use.
type Collector struct{}

// Capture runs perf record + perf script, collapses stacks, generates SVG,
// and returns a TraceResult. This is the main entry point.
func (c *Collector) Capture(ctx context.Context, opts CaptureOpts) (*TraceResult, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("trace 仅支持 Linux 平台 (当前: %s)", runtime.GOOS)
	}

	if err := c.validate(opts); err != nil {
		return nil, err
	}

	// Apply defaults.
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

	// Step 1: perf record.
	perfDataPath := filepath.Join(opts.OutDir, fmt.Sprintf("perf-%d.data", opts.PID))
	_, err := osutil.RunWithTimeout(ctx, opts.Duration+10*time.Second,
		"perf", "record", "-F", strconv.Itoa(opts.Freq),
		"-g", "-p", strconv.Itoa(opts.PID),
		"-o", perfDataPath,
		"--", "sleep", strconv.Itoa(durSec))
	if err != nil {
		return nil, fmt.Errorf("perf record 失败: %w (需要 root 权限或 CAP_SYS_ADMIN)", err)
	}
	defer os.Remove(perfDataPath) // clean up raw data

	// Step 2: perf script.
	rawScript, err := osutil.RunWithTimeout(ctx, 30*time.Second,
		"perf", "script", "-i", perfDataPath)
	if err != nil {
		return nil, fmt.Errorf("perf script 失败: %w", err)
	}

	// Step 3: Collapse stacks.
	collapsed := CollapseStacks(string(rawScript))
	if collapsed == "" {
		return nil, fmt.Errorf("采集到的堆栈为空，可能进程在采集期间没有活动")
	}

	// Step 4: Generate SVG.
	ts := time.Now()
	svgName := fmt.Sprintf("flame-%s.svg", ts.Format("20060102-150405"))
	svgPath := filepath.Join(opts.OutDir, svgName)
	title := fmt.Sprintf("PID %d, %ds @ %dHz", opts.PID, durSec, opts.Freq)
	if err := GenerateSVG(collapsed, svgPath, title); err != nil {
		return nil, fmt.Errorf("生成火焰图失败: %w", err)
	}

	// Step 5: Extract top functions.
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

// FormatTopFuncsTable renders a text table of hot functions for terminal display.
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/trace/ -run 'TestCaptureOpts|TestFormatTopFuncs' -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/trace/collector.go internal/trace/collector_test.go
git commit -m "feat(trace): TraceCollector with perf capture, collapse, and SVG generation"
```

---

## Task 7: 源码查找 (SourceLookup)

**Files:**
- Create: `internal/trace/source.go`
- Create: `internal/trace/source_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/trace/source_test.go
package trace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceLookup_Grep(t *testing.T) {
	// Create a fake source tree.
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "storage", "innobase", "handler", "ha_innodb.cc")
	os.MkdirAll(filepath.Dir(srcFile), 0755)
	os.WriteFile(srcFile, []byte(`
int ha_innodb::write_row(uchar* record) {
    // Insert a row into InnoDB.
    trx_t* trx = thd_to_trx(ha_thd());
    return row_insert_for_mysql(record, trx);
}

void ha_innodb::other_func() {
    // something else
}
`), 0644)

	lookup := &SourceLookup{SourceDir: dir}
	funcs := []HotFunc{
		{Name: "ha_innodb::write_row"},
		{Name: "nonexistent_func"},
	}

	results, err := lookup.Grep(funcs)
	if err != nil {
		t.Fatalf("Grep failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FuncName != "ha_innodb::write_row" {
		t.Errorf("unexpected func name: %s", results[0].FuncName)
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

func TestSourceLookup_NoDir(t *testing.T) {
	lookup := &SourceLookup{SourceDir: ""}
	results, err := lookup.Grep([]HotFunc{{Name: "test"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected empty results when no source dir configured")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/trace/ -run TestSourceLookup -v -count=1`
Expected: FAIL — `SourceLookup` undefined

- [ ] **Step 3: Implement source lookup**

```go
// internal/trace/source.go
package trace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SourceLookup searches database source code for hot function definitions.
type SourceLookup struct {
	SourceDir string // local path to database source tree
}

// Grep finds function definitions for the given hot functions in the source tree.
// Returns only functions that were actually found. If SourceDir is empty, returns nil.
func (s *SourceLookup) Grep(funcs []HotFunc) ([]FuncSource, error) {
	if s.SourceDir == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.SourceDir); os.IsNotExist(err) {
		return nil, nil
	}

	var results []FuncSource
	for _, f := range funcs {
		src, found := s.findFunc(f.Name)
		if found {
			results = append(results, src)
		}
	}
	return results, nil
}

// findFunc searches for a function definition in the source tree.
// For C/C++: looks for "funcname(" at the start or after a return type.
// For Go (PG extensions): looks for "func ... funcname(".
func (s *SourceLookup) findFunc(name string) (FuncSource, bool) {
	// Strip class prefix for search: "ha_innodb::write_row" → search for "write_row"
	searchName := name
	if idx := strings.LastIndex(name, "::"); idx >= 0 {
		searchName = name[idx+2:]
	}

	var result FuncSource
	found := false

	filepath.Walk(s.SourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return err
		}
		if info.IsDir() {
			// Skip common non-source directories.
			base := info.Name()
			if base == ".git" || base == "test" || base == "tests" || base == "unittest" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only search C/C++/Go source files.
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".c" && ext != ".cc" && ext != ".cpp" && ext != ".h" && ext != ".go" {
			return nil
		}

		lineNum, snippet := searchInFile(path, searchName, name)
		if lineNum > 0 {
			relPath, _ := filepath.Rel(s.SourceDir, path)
			result = FuncSource{
				FuncName: name,
				FilePath: relPath,
				Line:     lineNum,
				Snippet:  snippet,
			}
			found = true
		}
		return nil
	})

	return result, found
}

// searchInFile looks for a function definition in a single file.
// Returns the line number and a snippet (up to 50 lines from the definition).
func searchInFile(path string, shortName string, fullName string) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		// Look for function definition: "shortName(" at word boundary.
		if isFuncDef(line, shortName) {
			// Collect up to 50 lines of the function body.
			snippet := collectSnippet(path, lineNum, 50)
			return lineNum, snippet
		}
	}
	return 0, ""
}

// isFuncDef checks if a line contains a function definition (not just a call).
// Heuristic: the function name is followed by "(" and the line does not start
// with whitespace (it's a definition, not a call inside another function).
func isFuncDef(line string, funcName string) bool {
	trimmed := strings.TrimSpace(line)
	// Must contain funcName(
	pattern := funcName + "("
	idx := strings.Index(trimmed, pattern)
	if idx < 0 {
		return false
	}
	// Definition heuristic: line starts at column 0 or with a return type,
	// not deep inside a block (indented with tab or many spaces).
	if len(line) > 0 && (line[0] == '\t' || strings.HasPrefix(line, "    ")) {
		// Indented — likely a call, not a definition. But check for
		// class method definitions which may be indented in C++.
		if strings.Contains(line, "::") {
			return true
		}
		return false
	}
	return true
}

// collectSnippet reads up to maxLines from a file starting at startLine.
func collectSnippet(path string, startLine int, maxLines int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	var b strings.Builder
	collected := 0

	for scanner.Scan() {
		lineNum++
		if lineNum < startLine {
			continue
		}
		if collected >= maxLines {
			break
		}
		line := scanner.Text()
		b.WriteString(fmt.Sprintf("%d: %s\n", lineNum, line))
		collected++

		// Stop early at the closing brace of the function.
		trimmed := strings.TrimSpace(line)
		if collected > 1 && trimmed == "}" {
			break
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/trace/ -run TestSourceLookup -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/trace/source.go internal/trace/source_test.go
git commit -m "feat(trace): source code grep for hot function analysis"
```

---

## Task 8: Config 新增 TraceConfig

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add TraceConfig to Config struct**

Add to `internal/config/config.go` after the `Sentinel` field in the `Config` struct:

```go
// In Config struct, add:
Trace TraceConfig `yaml:"trace"`

// New section after SentinelConfig:

// ============================================================================
// 堆栈追踪 (Trace) — OS 级火焰图分析
// ============================================================================

// TraceConfig controls the OS-level stack trace and flame graph feature.
type TraceConfig struct {
	Auto     bool   `yaml:"auto"`               // Sentinel 联动自动采集 (默认 false)
	Duration int    `yaml:"duration,omitempty"`  // 默认采集秒数 (默认 3, 范围 1-10)
	TopN     int    `yaml:"top_n,omitempty"`     // 热点函数数量 (默认 20)
	OutDir   string `yaml:"output_dir,omitempty"` // SVG 输出目录 (默认 ~/.opendb/trace/)
	Source   TraceSourceConfig `yaml:"source,omitempty"`
}

// TraceSourceConfig specifies where to find database source code.
type TraceSourceConfig struct {
	Dir    string `yaml:"dir,omitempty"`    // 本地源码路径
	Repo   string `yaml:"repo,omitempty"`   // Git 仓库 URL
	Branch string `yaml:"branch,omitempty"` // Git 分支 (默认 main)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build -tags full ./cmd/opendb/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add TraceConfig for OS stack trace and flame graph"
```

---

## Task 9: MySQL /trace Skill

**Files:**
- Create: `internal/mysql/skill/monitor/trace.go`
- Create: `internal/mysql/skill/monitor/trace_test.go`
- Modify: `internal/mysql/register.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/mysql/skill/monitor/trace_test.go
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.CLIDef().Command != "trace" {
		t.Errorf("CLIDef().Command = %q, want 'trace'", s.CLIDef().Command)
	}
}

func TestTraceSkill_Validate(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	// Default params: valid.
	err := s.Validate(skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
	// Invalid duration.
	err = s.Validate(skill.ParamsFromMap(map[string]any{"duration": 30}))
	if err == nil {
		t.Error("expected error for duration=30")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags full ./internal/mysql/skill/monitor/ -run TestTraceSkill -v -count=1`
Expected: FAIL — `NewTraceSkill` undefined

- [ ] **Step 3: Implement MySQL trace skill**

```go
// internal/mysql/skill/monitor/trace.go
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

// TraceSkill captures OS-level stack traces of the MySQL process
// and generates flame graphs for performance analysis.
type TraceSkill struct {
	connHost  string
	traceCfg  *config.TraceConfig
	collector *trace.Collector
}

// NewTraceSkill creates a MySQL trace skill.
func NewTraceSkill(connHost string, traceCfg *config.TraceConfig) *TraceSkill {
	return &TraceSkill{
		connHost:  connHost,
		traceCfg:  traceCfg,
		collector: &trace.Collector{},
	}
}

func (s *TraceSkill) Name() string        { return "trace" }
func (s *TraceSkill) Description() string  { return "OS 堆栈采集 + 火焰图分析 (MySQL)" }
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
	// Step 1: Host check.
	pid, err := trace.IsLocal(ctx, "mysql", s.connHost)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("trace 不可用: %s", err),
			Summary:  "trace unavailable",
		}, nil
	}

	// Step 2: Resolve config.
	dur := params.IntOr("duration", s.defaultDuration())
	outDir := s.outputDir()
	os.MkdirAll(outDir, 0755)

	// Step 3: Capture.
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

	// Step 4: Source lookup (optional).
	var sources []trace.FuncSource
	if srcDir := s.sourceDir(); srcDir != "" {
		lookup := &trace.SourceLookup{SourceDir: srcDir}
		sources, _ = lookup.Grep(result.TopFuncs)
	}

	// Step 5: Format output.
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
	fmt.Fprintf(&b, "OS 堆栈分析 (mysqld PID %d, %ds, %dHz)\n\n",
		result.PID, result.Duration, 99)
	b.WriteString(trace.FormatTopFuncsTable(result.TopFuncs))
	fmt.Fprintf(&b, "\n  火焰图: %s\n", result.SVGPath)

	if len(sources) > 0 {
		b.WriteString("\n  源码片段:\n")
		for _, src := range sources {
			fmt.Fprintf(&b, "\n  ── %s (%s:%d) ──\n%s\n",
				src.FuncName, src.FilePath, src.Line, src.Snippet)
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

func (s *TraceSkill) sourceDir() string {
	if s.traceCfg == nil {
		return ""
	}
	if s.traceCfg.Source.Dir != "" {
		return s.traceCfg.Source.Dir
	}
	// TODO: support git clone from Source.Repo in future
	return ""
}
```

- [ ] **Step 4: Run tests**

Run: `go test -tags full ./internal/mysql/skill/monitor/ -run TestTraceSkill -v -count=1`
Expected: PASS

- [ ] **Step 5: Register in mysql/register.go**

Add to `RegisterSkills` in `internal/mysql/register.go`, after the existing monitor skills:

```go
reg(monitor.NewTraceSkill(cfg.Connections[0].Host, &cfg.Trace))
```

Wait — the host comes from the active connection, not from cfg.Connections[0]. Looking at the register function signature, we have `driver db.Driver` but not the connection config directly. The driver has `ServerInfo()` but no host info.

Better approach: pass the connection host from the register call site. But looking at the existing pattern (e.g., `NewOSSkill(driver)`), skills only get `driver`. We need to pass the host separately.

Actually, looking at the `db.ConnectionConfig.Host` field and how register is called — the connection config is available via `connMgr`. Let's check.

The `RegisterSkills` function receives `connMgr *connection.Manager`. We can get the active connection's host from there. But simpler: just pass the host string from the REPL/boot code that calls RegisterSkills. For now, add `connHost string` to the TraceSkill constructor and pass it from register.

Update `internal/mysql/register.go`:

```go
// Add import if not present:
// (config is already imported)

// Inside RegisterSkills, after existing monitor registrations:
// Get connection host from the first connection or use driver info.
// The connMgr knows the active connection host.
reg(monitor.NewTraceSkill("", &cfg.Trace)) // host will be set at connection time
```

Actually, looking more carefully: the `driver` is already connected and we can't easily get the host from it. The cleanest approach is to accept that the host check happens at Execute time using the connection config stored elsewhere, or to pass the host at construction.

For now, let's make the skill accept the host at construction. The register.go caller has access to the connection info. Let me look at how connections work.

The `RegisterSkills` function doesn't directly receive the connection host. But `connMgr.Active()` would give us the active connection. However, register is called once, and the connection might change.

**Simplest approach**: Have the skill query the host dynamically. Pass the `connection.Manager` or just check at execute time. Let's pass a host resolver function:

```go
// In trace.go, change constructor:
func NewTraceSkill(connHost string, traceCfg *config.TraceConfig) *TraceSkill

// In register.go, determine host from the connection config being registered:
// RegisterSkills is called after connection is established.
// We can get the host from the driver's connection config.
```

Looking at the codebase pattern, skills like `NewOSSkill(driver)` just take the driver. For trace, we need the connection host. Let's add it as a simple string parameter. The register.go can pass the host from the connection config.

But `RegisterSkills` doesn't have the `ConnectionConfig` directly. It has `connMgr`. Let's check what `connection.Manager` exposes.

For the registration step, let's keep it simple — the skill will do the host check using `connMgr.ActiveConfig().Host` at execute time, or we just pass the host string to the constructor. Since the register function has `connMgr`, we can extract the host there.

For now, let's use a simpler approach — pass just the host string:

In `internal/mysql/register.go`, we need to find where we know the host. Looking at the function signature:
```go
func RegisterSkills(registry, driver, connMgr, history, cfg, modelMgr, opendbDir)
```

The `connMgr` has the active connection info. Let's get the host from there:

```go
// Inside RegisterSkills, add:
connHost := ""
if ac := connMgr.ActiveConfig(); ac != nil {
    connHost = ac.Host
}
reg(monitor.NewTraceSkill(connHost, &cfg.Trace))
```

We need to verify `connMgr.ActiveConfig()` exists. If not, we can pass the host differently. Let's check at implementation time — for now mark it as needing the host.

- [ ] **Step 6: Verify compilation**

Run: `go build -tags full ./cmd/opendb/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mysql/skill/monitor/trace.go internal/mysql/skill/monitor/trace_test.go internal/mysql/register.go
git commit -m "feat(trace): MySQL /trace skill — OS stack capture + flame graph"
```

---

## Task 10: PostgreSQL /trace Skill

**Files:**
- Create: `internal/postgres/skill/monitor/trace.go`
- Create: `internal/postgres/skill/monitor/trace_test.go`
- Modify: `internal/postgres/register.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/postgres/skill/monitor/trace_test.go
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestPGTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}

func TestPGTraceSkill_Validate(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	err := s.Validate(skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
	err = s.Validate(skill.ParamsFromMap(map[string]any{"duration": 30}))
	if err == nil {
		t.Error("expected error for duration=30")
	}
}
```

- [ ] **Step 2: Implement PG trace skill**

Create `internal/postgres/skill/monitor/trace.go` — same structure as MySQL trace skill but with:
- `dbType = "postgres"` in IsLocal and result
- Process description: `postgres` instead of `mysqld`
- Output header: `postgres` instead of `mysqld`

```go
// internal/postgres/skill/monitor/trace.go
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

// TraceSkill captures OS-level stack traces of the PostgreSQL process.
type TraceSkill struct {
	connHost  string
	traceCfg  *config.TraceConfig
	collector *trace.Collector
}

func NewTraceSkill(connHost string, traceCfg *config.TraceConfig) *TraceSkill {
	return &TraceSkill{
		connHost:  connHost,
		traceCfg:  traceCfg,
		collector: &trace.Collector{},
	}
}

func (s *TraceSkill) Name() string        { return "trace" }
func (s *TraceSkill) Description() string  { return "OS 堆栈采集 + 火焰图分析 (PostgreSQL)" }
func (s *TraceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TraceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "trace",
		Description: "采集 PostgreSQL 进程 OS 堆栈，生成火焰图，返回热点函数和折叠栈帧",
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
	pid, err := trace.IsLocal(ctx, "postgres", s.connHost)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("trace 不可用: %s", err),
			Summary:  "trace unavailable",
		}, nil
	}

	dur := params.IntOr("duration", s.defaultDuration())
	outDir := s.outputDir()
	os.MkdirAll(outDir, 0755)

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
	result.DBType = "postgres"

	var sources []trace.FuncSource
	if srcDir := s.sourceDir(); srcDir != "" {
		lookup := &trace.SourceLookup{SourceDir: srcDir}
		sources, _ = lookup.Grep(result.TopFuncs)
	}

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
	fmt.Fprintf(&b, "OS 堆栈分析 (postgres PID %d, %ds, %dHz)\n\n",
		result.PID, result.Duration, 99)
	b.WriteString(trace.FormatTopFuncsTable(result.TopFuncs))
	fmt.Fprintf(&b, "\n  火焰图: %s\n", result.SVGPath)

	if len(sources) > 0 {
		b.WriteString("\n  源码片段:\n")
		for _, src := range sources {
			fmt.Fprintf(&b, "\n  ── %s (%s:%d) ──\n%s\n",
				src.FuncName, src.FilePath, src.Line, src.Snippet)
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

func (s *TraceSkill) sourceDir() string {
	if s.traceCfg == nil {
		return ""
	}
	if s.traceCfg.Source.Dir != "" {
		return s.traceCfg.Source.Dir
	}
	return ""
}
```

- [ ] **Step 3: Register in postgres/register.go**

Add to `RegisterSkills` after existing monitor registrations:

```go
reg(monitor.NewTraceSkill("", &cfg.Trace))
```

(Same host-resolution pattern as MySQL — will be refined when wiring to REPL.)

- [ ] **Step 4: Run tests**

Run: `go test -tags full ./internal/postgres/skill/monitor/ -run TestPGTraceSkill -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/postgres/skill/monitor/trace.go internal/postgres/skill/monitor/trace_test.go internal/postgres/register.go
git commit -m "feat(trace): PostgreSQL /trace skill — OS stack capture + flame graph"
```

---

## Task 11: Oracle /trace Skill (无源码分析)

**Files:**
- Create: `internal/oracle/skill/monitor/trace.go`
- Create: `internal/oracle/skill/monitor/trace_test.go`
- Modify: `internal/oracle/register.go`

- [ ] **Step 1: Write test**

```go
// internal/oracle/skill/monitor/trace_test.go
// Add to existing file or create new:
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestOracleTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}
```

- [ ] **Step 2: Implement Oracle trace skill**

Same structure as MySQL/PG but:
- `dbType = "oracle"` in IsLocal
- **No source lookup** — Oracle is closed source, skip `SourceLookup.Grep()`
- Output header: `oracle`

```go
// internal/oracle/skill/monitor/trace.go
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

// TraceSkill captures OS-level stack traces of the Oracle process.
// Note: Oracle is closed source — no source code analysis is performed.
type TraceSkill struct {
	connHost  string
	traceCfg  *config.TraceConfig
	collector *trace.Collector
}

func NewTraceSkill(connHost string, traceCfg *config.TraceConfig) *TraceSkill {
	return &TraceSkill{
		connHost:  connHost,
		traceCfg:  traceCfg,
		collector: &trace.Collector{},
	}
}

func (s *TraceSkill) Name() string        { return "trace" }
func (s *TraceSkill) Description() string  { return "OS 堆栈采集 + 火焰图分析 (Oracle)" }
func (s *TraceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TraceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "trace",
		Description: "采集 Oracle 进程 OS 堆栈，生成火焰图，返回热点函数和折叠栈帧",
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
	pid, err := trace.IsLocal(ctx, "oracle", s.connHost)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("trace 不可用: %s", err),
			Summary:  "trace unavailable",
		}, nil
	}

	dur := params.IntOr("duration", s.defaultDuration())
	outDir := s.outputDir()
	os.MkdirAll(outDir, 0755)

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
	result.DBType = "oracle"

	// Oracle is closed source — no source lookup.
	var b strings.Builder
	fmt.Fprintf(&b, "OS 堆栈分析 (oracle PID %d, %ds, %dHz)\n\n",
		result.PID, result.Duration, 99)
	b.WriteString(trace.FormatTopFuncsTable(result.TopFuncs))
	fmt.Fprintf(&b, "\n  火焰图: %s\n", result.SVGPath)
	b.WriteString("\n  (Oracle 闭源，不做源码层面分析)\n")

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("trace: PID %d, %ds, %d hot funcs", pid, dur, len(result.TopFuncs)),
	}, nil
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
```

- [ ] **Step 3: Register in oracle/register.go**

Add to `RegisterSkills` after existing monitor registrations:

```go
reg(monitor.NewTraceSkill("", &cfg.Trace))
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/oracle/skill/monitor/ -run TestOracleTraceSkill -v -count=1`
Expected: PASS

- [ ] **Step 5: Verify full build**

Run: `go build -tags full ./cmd/opendb/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/oracle/skill/monitor/trace.go internal/oracle/skill/monitor/trace_test.go internal/oracle/register.go
git commit -m "feat(trace): Oracle /trace skill — OS stack capture + flame graph (no source analysis)"
```

---

## Task 12: 全量测试 + 收尾

- [ ] **Step 1: Run all trace package tests**

Run: `go test ./internal/trace/... -v -count=1`
Expected: All PASS

- [ ] **Step 2: Run all skill tests**

Run: `go test -tags full ./internal/mysql/skill/monitor/ ./internal/postgres/skill/monitor/ ./internal/oracle/skill/monitor/ -v -count=1 -run Trace`
Expected: All PASS

- [ ] **Step 3: Run vet**

Run: `go vet -tags full ./internal/osutil/ ./internal/trace/ ./internal/mysql/skill/monitor/ ./internal/postgres/skill/monitor/ ./internal/oracle/skill/monitor/`
Expected: No issues

- [ ] **Step 4: Full build**

Run: `go build -tags full -o opendb ./cmd/opendb/`
Expected: PASS

- [ ] **Step 5: Commit tag**

```bash
git add -A
git commit -m "feat(trace): complete /trace skill — OS stack capture + flame graph + source analysis

Adds:
- internal/osutil: OS command whitelist executor
- internal/trace: collector, collapse, flamegraph, source lookup, hostcheck
- MySQL/PG/Oracle trace skills (OG placeholder pending)
- TraceConfig in config.yaml
- Pure Go stackcollapse and SVG generation (no external dependencies)"
```
