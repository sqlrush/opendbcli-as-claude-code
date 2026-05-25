# Phase 1: Daemon 基础设施 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 OpenDB 能以 daemon 模式 7×24 常驻运行，具备基本的自治循环能力

**Architecture:** 在 `internal/drone/` 包中实现 daemon 生命周期管理，在 `cmd/opendb/main.go` 中新增 `agent` 子命令入口。daemon 启动后自动连接数据库、启动 Sentinel 监控、进入 Autonomy Loop。

**Tech Stack:** Go 1.26.1, 现有 Sentinel/config/connection/engine 包

---

### Task 1: agent 子命令入口

**Files:**
- Modify: `cmd/opendb/main.go`
- Create: `internal/drone/cmd.go`

- [ ] **Step 1: 写 drone/cmd.go 的 agent 子命令解析**

```go
// internal/drone/cmd.go
package drone

import (
	"fmt"
	"os"
	"strings"
)

// Subcommand represents an agent subcommand (start/stop/status).
type Subcommand string

const (
	SubcmdStart  Subcommand = "start"
	SubcmdStop   Subcommand = "stop"
	SubcmdStatus Subcommand = "status"
)

// AgentArgs holds parsed agent command arguments.
type AgentArgs struct {
	Subcmd   Subcommand
	Role     string // "worker", "memory", "manager"
	Listen   string // gRPC listen address
	Overlord string // Overlord address (for worker role)
	DBType   string // database type
	DBConn   string // database connection string
}

// ParseAgentArgs parses os.Args for "opendb agent <subcmd> [flags]".
// args should be os.Args[2:] (after "opendb" and "agent").
func ParseAgentArgs(args []string) (AgentArgs, error) {
	if len(args) == 0 {
		return AgentArgs{}, fmt.Errorf("usage: opendb agent <start|stop|status> [flags]")
	}

	subcmd := Subcommand(args[0])
	switch subcmd {
	case SubcmdStart, SubcmdStop, SubcmdStatus:
	default:
		return AgentArgs{}, fmt.Errorf("unknown agent subcommand: %s (use start, stop, or status)", args[0])
	}

	result := AgentArgs{
		Subcmd: subcmd,
		Role:   "worker", // default role
		Listen: "0.0.0.0:9300",
	}

	// Parse flags for start subcommand.
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--role" && i+1 < len(args):
			i++
			result.Role = args[i]
		case strings.HasPrefix(args[i], "--role="):
			result.Role = strings.TrimPrefix(args[i], "--role=")
		case args[i] == "--listen" && i+1 < len(args):
			i++
			result.Listen = args[i]
		case strings.HasPrefix(args[i], "--listen="):
			result.Listen = strings.TrimPrefix(args[i], "--listen=")
		case args[i] == "--overlord" && i+1 < len(args):
			i++
			result.Overlord = args[i]
		case strings.HasPrefix(args[i], "--overlord="):
			result.Overlord = strings.TrimPrefix(args[i], "--overlord=")
		case args[i] == "--db-type" && i+1 < len(args):
			i++
			result.DBType = args[i]
		case strings.HasPrefix(args[i], "--db-type="):
			result.DBType = strings.TrimPrefix(args[i], "--db-type=")
		case args[i] == "--db-conn" && i+1 < len(args):
			i++
			result.DBConn = args[i]
		case strings.HasPrefix(args[i], "--db-conn="):
			result.DBConn = strings.TrimPrefix(args[i], "--db-conn=")
		}
	}

	return result, nil
}
```

- [ ] **Step 2: 写测试**

```go
// internal/drone/cmd_test.go
package drone

import "testing"

func TestParseAgentArgs_Start(t *testing.T) {
	args := []string{"start", "--role", "worker", "--overlord", "10.0.2.1:9200"}
	got, err := ParseAgentArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subcmd != SubcmdStart {
		t.Errorf("subcmd = %q, want %q", got.Subcmd, SubcmdStart)
	}
	if got.Role != "worker" {
		t.Errorf("role = %q, want %q", got.Role, "worker")
	}
	if got.Overlord != "10.0.2.1:9200" {
		t.Errorf("overlord = %q, want %q", got.Overlord, "10.0.2.1:9200")
	}
}

func TestParseAgentArgs_Empty(t *testing.T) {
	_, err := ParseAgentArgs(nil)
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestParseAgentArgs_InvalidSubcmd(t *testing.T) {
	_, err := ParseAgentArgs([]string{"invalid"})
	if err == nil {
		t.Fatal("expected error for invalid subcommand")
	}
}

func TestParseAgentArgs_StatusAndStop(t *testing.T) {
	for _, sub := range []string{"status", "stop"} {
		got, err := ParseAgentArgs([]string{sub})
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", sub, err)
		}
		if string(got.Subcmd) != sub {
			t.Errorf("subcmd = %q, want %q", got.Subcmd, sub)
		}
	}
}
```

- [ ] **Step 3: 运行测试确认通过**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/drone/ -v -run TestParseAgentArgs`
Expected: All 4 tests PASS

- [ ] **Step 4: 在 main.go 中添加 agent 子命令路由**

在 `cmd/opendb/main.go` 的 `main()` 函数中，在 `configure` 检查之后添加 agent 子命令处理：

```go
// Agent mode: opendb agent <start|stop|status> [flags]
if len(os.Args) > 1 && os.Args[1] == "agent" {
	agentArgs, err := drone.ParseAgentArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := drone.RunAgent(agentArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Agent error: %v\n", err)
		os.Exit(1)
	}
	return
}
```

需要在 import 中添加：`"github.com/sqlrush/opendb/internal/drone"`

- [ ] **Step 5: 创建 RunAgent 占位函数**

```go
// internal/drone/agent.go
package drone

import "fmt"

// RunAgent dispatches to the appropriate agent subcommand.
func RunAgent(args AgentArgs) error {
	switch args.Subcmd {
	case SubcmdStart:
		return runStart(args)
	case SubcmdStop:
		return runStop(args)
	case SubcmdStatus:
		return runStatus(args)
	default:
		return fmt.Errorf("unknown subcommand: %s", args.Subcmd)
	}
}

func runStart(args AgentArgs) error {
	fmt.Printf("Starting agent in %s mode...\n", args.Role)
	return fmt.Errorf("not implemented yet")
}

func runStop(args AgentArgs) error {
	fmt.Println("Stopping agent...")
	return fmt.Errorf("not implemented yet")
}

func runStatus(args AgentArgs) error {
	fmt.Println("Checking agent status...")
	return fmt.Errorf("not implemented yet")
}
```

- [ ] **Step 6: 编译验证**

Run: `cd /Users/yingjiewang/opendb && go build -tags full -o opendb ./cmd/opendb/`
Expected: BUILD SUCCESS

- [ ] **Step 7: 提交**

```bash
cd /Users/yingjiewang/opendb
git add internal/drone/cmd.go internal/drone/cmd_test.go internal/drone/agent.go cmd/opendb/main.go
git commit -m "feat(drone): add agent subcommand parsing and routing"
```

---

### Task 2: PID 文件管理 + daemon 进程

**Files:**
- Create: `internal/drone/pidfile.go`
- Create: `internal/drone/pidfile_test.go`

- [ ] **Step 1: 写 PID 文件管理**

```go
// internal/drone/pidfile.go
package drone

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// defaultPIDDir returns the directory for PID files.
func defaultPIDDir() string {
	if dir := os.Getenv("OPENDB_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".opendb"
	}
	return filepath.Join(home, ".opendb")
}

// pidFilePath returns the PID file path for the given role.
func pidFilePath(role string) string {
	return filepath.Join(defaultPIDDir(), fmt.Sprintf("agent-%s.pid", role))
}

// writePID writes the current process PID to the PID file.
func writePID(role string) error {
	path := pidFilePath(role)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create pid dir: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// readPID reads the PID from the PID file. Returns 0 if not found.
func readPID(role string) (int, error) {
	data, err := os.ReadFile(pidFilePath(role))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid file content: %w", err)
	}
	return pid, nil
}

// removePID removes the PID file.
func removePID(role string) error {
	return os.Remove(pidFilePath(role))
}

// isProcessRunning checks if a process with the given PID is still running.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Use Signal(0) to check.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
```

- [ ] **Step 2: 写测试**

```go
// internal/drone/pidfile_test.go
package drone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadPID(t *testing.T) {
	// Use temp dir to avoid polluting real config.
	tmpDir := t.TempDir()
	t.Setenv("OPENDB_HOME", tmpDir)

	if err := writePID("worker"); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	pid, err := readPID("worker")
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}

	// Verify file exists.
	path := filepath.Join(tmpDir, "agent-worker.pid")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("PID file does not exist")
	}

	// Remove and verify.
	if err := removePID("worker"); err != nil {
		t.Fatalf("removePID: %v", err)
	}
	pid, err = readPID("worker")
	if err != nil {
		t.Fatalf("readPID after remove: %v", err)
	}
	if pid != 0 {
		t.Errorf("pid after remove = %d, want 0", pid)
	}
}

func TestIsProcessRunning(t *testing.T) {
	// Current process should be running.
	if !isProcessRunning(os.Getpid()) {
		t.Error("current process should be running")
	}
	// PID 0 should not be "running" from our perspective.
	if isProcessRunning(0) {
		t.Error("PID 0 should not be running")
	}
}
```

- [ ] **Step 3: 运行测试**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/drone/ -v -run TestWriteAndReadPID`
Run: `cd /Users/yingjiewang/opendb && go test ./internal/drone/ -v -run TestIsProcessRunning`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/drone/pidfile.go internal/drone/pidfile_test.go
git commit -m "feat(drone): add PID file management for daemon lifecycle"
```

---

### Task 3: daemon start/stop/status 实现

**Files:**
- Modify: `internal/drone/agent.go`

- [ ] **Step 1: 实现 runStart — daemon 前台运行（Phase 1 先不后台化）**

```go
// internal/drone/agent.go
package drone

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// RunAgent dispatches to the appropriate agent subcommand.
func RunAgent(args AgentArgs) error {
	switch args.Subcmd {
	case SubcmdStart:
		return runStart(args)
	case SubcmdStop:
		return runStop(args)
	case SubcmdStatus:
		return runStatus(args)
	default:
		return fmt.Errorf("unknown subcommand: %s", args.Subcmd)
	}
}

func runStart(args AgentArgs) error {
	// Check if already running.
	pid, err := readPID(args.Role)
	if err != nil {
		return fmt.Errorf("check existing agent: %w", err)
	}
	if pid > 0 && isProcessRunning(pid) {
		return fmt.Errorf("agent already running (PID %d). Use 'opendb agent stop' first", pid)
	}

	// Write PID file.
	if err := writePID(args.Role); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	defer removePID(args.Role)

	fmt.Printf("OpenDB Agent starting (role=%s, pid=%d)\n", args.Role, os.Getpid())

	// Setup signal handling for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %v, shutting down gracefully...\n", sig)
		cancel()
	}()

	// Run the main agent loop.
	return runAgentLoop(ctx, args)
}

// runAgentLoop is the main daemon loop. It will be expanded in later tasks
// to include Sentinel, Autonomy Loop, etc.
func runAgentLoop(ctx context.Context, args AgentArgs) error {
	fmt.Printf("Agent running (role=%s). Press Ctrl+C to stop.\n", args.Role)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Agent stopped.")
			return nil
		case t := <-ticker.C:
			fmt.Printf("[%s] Agent heartbeat (role=%s)\n", t.Format("15:04:05"), args.Role)
		}
	}
}

func runStop(args AgentArgs) error {
	pid, err := readPID(args.Role)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}
	if pid == 0 {
		fmt.Println("No agent running.")
		return nil
	}
	if !isProcessRunning(pid) {
		fmt.Printf("Agent (PID %d) is not running. Cleaning up PID file.\n", pid)
		removePID(args.Role)
		return nil
	}

	// Send SIGTERM for graceful shutdown.
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
	}
	fmt.Printf("Sent SIGTERM to agent (PID %d). Waiting for shutdown...\n", pid)

	// Wait up to 10 seconds for process to exit.
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if !isProcessRunning(pid) {
			fmt.Println("Agent stopped.")
			removePID(args.Role)
			return nil
		}
	}

	return fmt.Errorf("agent (PID %d) did not stop within 10 seconds", pid)
}

func runStatus(args AgentArgs) error {
	pid, err := readPID(args.Role)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}
	if pid == 0 {
		fmt.Printf("Agent (%s): not running\n", args.Role)
		return nil
	}
	if !isProcessRunning(pid) {
		fmt.Printf("Agent (%s): stale PID file (PID %d not running)\n", args.Role, pid)
		return nil
	}
	fmt.Printf("Agent (%s): running (PID %d)\n", args.Role, pid)
	return nil
}
```

- [ ] **Step 2: 编译验证**

Run: `cd /Users/yingjiewang/opendb && go build -tags full -o opendb ./cmd/opendb/`
Expected: BUILD SUCCESS

- [ ] **Step 3: 手动测试**

```bash
# 启动（前台运行，Ctrl+C 停止）
./opendb agent start --role worker

# 另一个终端查看状态
./opendb agent status

# 停止
./opendb agent stop
```

- [ ] **Step 4: 提交**

```bash
git add internal/drone/agent.go
git commit -m "feat(drone): implement agent start/stop/status with PID management and signal handling"
```

---

### Task 4: 审计日志

**Files:**
- Create: `internal/drone/audit.go`
- Create: `internal/drone/audit_test.go`

- [ ] **Step 1: 实现审计日志 writer**

```go
// internal/drone/audit.go
package drone

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditLogger writes append-only audit log entries for database write operations.
type AuditLogger struct {
	mu   sync.Mutex
	path string
	file *os.File
}

// NewAuditLogger creates an audit logger at the given path.
func NewAuditLogger(baseDir string) (*AuditLogger, error) {
	path := filepath.Join(baseDir, "audit.log")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	return &AuditLogger{path: path, file: f}, nil
}

// Log writes a single audit entry. Thread-safe.
func (a *AuditLogger) Log(role, target, operation, reason, result string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry := fmt.Sprintf("%s | %s | %s | %s | reason: %s | result: %s\n",
		time.Now().Format(time.RFC3339),
		role,
		target,
		operation,
		reason,
		result,
	)
	_, err := a.file.WriteString(entry)
	return err
}

// Close closes the underlying file.
func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		return a.file.Close()
	}
	return nil
}
```

- [ ] **Step 2: 写测试**

```go
// internal/drone/audit_test.go
package drone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logger, err := NewAuditLogger(tmpDir)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	if err := logger.Log("worker", "Oracle-A-037", "KILL SESSION '472,38291'", "TEMP 93%, LLM诊断", "OK"); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := logger.Log("worker", "Oracle-A-037", "CREATE INDEX idx_order_date", "同上", "OK"); err != nil {
		t.Fatalf("Log: %v", err)
	}

	// Read and verify.
	data, err := os.ReadFile(filepath.Join(tmpDir, "audit.log"))
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "KILL SESSION") {
		t.Errorf("line 0 missing KILL SESSION: %s", lines[0])
	}
	if !strings.Contains(lines[1], "CREATE INDEX") {
		t.Errorf("line 1 missing CREATE INDEX: %s", lines[1])
	}
}
```

- [ ] **Step 3: 运行测试**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/drone/ -v -run TestAuditLogger`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/drone/audit.go internal/drone/audit_test.go
git commit -m "feat(drone): add append-only audit logger for write operations"
```

---

### Task 5: 编译和全量测试验证

- [ ] **Step 1: 完整编译**

Run: `cd /Users/yingjiewang/opendb && go build -tags full -o opendb ./cmd/opendb/`
Expected: BUILD SUCCESS

- [ ] **Step 2: go vet**

Run: `cd /Users/yingjiewang/opendb && go vet -tags full ./internal/drone/...`
Expected: No issues

- [ ] **Step 3: 全量测试 drone 包**

Run: `cd /Users/yingjiewang/opendb && go test -tags full -race ./internal/drone/ -v`
Expected: All tests PASS, no race conditions

- [ ] **Step 4: 确认不影响现有功能**

Run: `cd /Users/yingjiewang/opendb && go build -tags full -o opendb ./cmd/opendb/ && ./opendb --version`
Expected: 正常输出版本号，现有功能不受影响

- [ ] **Step 5: 提交最终状态（如有修复）**

```bash
git add -A
git commit -m "chore(drone): fix any issues from full build and test verification"
```
