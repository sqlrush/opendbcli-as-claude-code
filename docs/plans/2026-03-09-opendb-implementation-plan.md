# OpenDB MVP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a database-specialized interactive CLI (like Claude Code) for Oracle DBA operations, with 24 slash commands, real-time monitoring, rich TUI, and optional LLM integration.

**Architecture:** Single-binary Go CLI using bubbletea for TUI, go-ora for Oracle, layered architecture with interfaces for DB driver, LLM provider, and skill system. All skills support dual invocation (AI JSON + human CLI).

**Tech Stack:** Go 1.22+, bubbletea/lipgloss (TUI), go-ora (Oracle), YAML configs

**Design doc:** `docs/plans/2026-03-09-opendb-design.md`

---

## Wave 1: Foundation

## Phase 1: Project Scaffold & Core Types

### Task 1.1: Initialize Go module and directory structure

**Files:**
- Create: `go.mod`
- Create: `cmd/opendb/main.go`
- Create: `Makefile`
- Create: `.golangci.yml`

**Step 1: Initialize Go module**

```bash
cd /Users/yingjiewang/opendb
go mod init github.com/sqlrush/opendb
```

**Step 2: Create entry point**

Create `cmd/opendb/main.go`:
```go
package main

import (
	"fmt"
	"os"

	"github.com/sqlrush/opendb/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version.String())
		os.Exit(0)
	}
	fmt.Println("OpenDB - Database CLI")
}
```

**Step 3: Create version package**

Create `internal/version/version.go`:
```go
package version

import "fmt"

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func String() string {
	return fmt.Sprintf("opendb %s (commit: %s, built: %s)", Version, GitCommit, BuildDate)
}
```

**Step 4: Create Makefile**

Create `Makefile`:
```makefile
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X github.com/sqlrush/opendb/internal/version.Version=$(VERSION) \
           -X github.com/sqlrush/opendb/internal/version.GitCommit=$(GIT_COMMIT) \
           -X github.com/sqlrush/opendb/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build test lint clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/opendb ./cmd/opendb

test:
	go test ./... -v -race -cover

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/
```

**Step 5: Create linter config**

Create `.golangci.yml`:
```yaml
run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - ineffassign
    - gosimple
```

**Step 6: Build and verify**

```bash
make build
./bin/opendb --version
```
Expected: `opendb dev (commit: xxxx, built: xxxx)`

**Step 7: Commit**

```bash
git add -A
git commit -m "chore: initialize Go project scaffold with build system"
```

---

### Task 1.2: Define core types, interfaces, and custom error types

**Files:**
- Create: `internal/db/driver.go`
- Create: `internal/db/types.go`
- Create: `internal/skill/skill.go`
- Create: `internal/skill/params.go`
- Create: `internal/skill/types.go`
- Create: `internal/llm/provider.go`
- Create: `internal/llm/types.go`
- Create: `internal/security/level.go`
- Create: `internal/errors/errors.go`

**Step 1: Write tests for Params**

Create `internal/skill/params_test.go`:
```go
package skill

import (
	"testing"
)

func TestParamsFromJSON(t *testing.T) {
	p, err := ParamsFromJSON([]byte(`{"db_type":"oracle","instance":"prod-01","threshold_ms":5000}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := p.StringOr("db_type", ""); got != "oracle" {
		t.Errorf("db_type = %q, want %q", got, "oracle")
	}
	if got, err := p.Int("threshold_ms"); err != nil || got != 5000 {
		t.Errorf("threshold_ms = %d, err = %v, want 5000", got, err)
	}
}

func TestParamsStringOr(t *testing.T) {
	p, _ := ParamsFromJSON([]byte(`{}`))
	if got := p.StringOr("missing", "default"); got != "default" {
		t.Errorf("got %q, want %q", got, "default")
	}
}

func TestParamsIntOr(t *testing.T) {
	p, _ := ParamsFromJSON([]byte(`{"limit":10}`))
	if got := p.IntOr("limit", 20); got != 10 {
		t.Errorf("got %d, want 10", got)
	}
	if got := p.IntOr("missing", 20); got != 20 {
		t.Errorf("got %d, want 20", got)
	}
}

func TestSecurityLevelString(t *testing.T) {
	tests := []struct {
		level SecurityLevel
		want  string
	}{
		{LevelReadOnly, "readonly"},
		{LevelOperator, "operator"},
		{LevelAdmin, "admin"},
		{LevelDangerous, "dangerous"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("SecurityLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestSecurityLevelRequiresConfirmation(t *testing.T) {
	if LevelReadOnly.RequiresConfirmation() {
		t.Error("LevelReadOnly should not require confirmation")
	}
	if LevelOperator.RequiresConfirmation() {
		t.Error("LevelOperator should not require confirmation by default")
	}
	if !LevelDangerous.RequiresConfirmation() {
		t.Error("LevelDangerous must require confirmation")
	}
}

func TestSecurityLevelCanDisableConfirmation(t *testing.T) {
	if LevelDangerous.CanDisableConfirmation() {
		t.Error("LevelDangerous must NOT allow disabling confirmation")
	}
	if !LevelOperator.CanDisableConfirmation() {
		t.Error("LevelOperator should allow disabling confirmation")
	}
	if !LevelAdmin.CanDisableConfirmation() {
		t.Error("LevelAdmin should allow disabling confirmation")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/skill/ -v
```
Expected: FAIL (types not defined yet)

**Step 3: Implement security levels**

Create `internal/security/level.go`:
```go
package security

type Level uint8

const (
	LevelReadOnly  Level = 0
	LevelOperator  Level = 1
	LevelAdmin     Level = 2
	LevelDangerous Level = 3
)

func (l Level) String() string {
	switch l {
	case LevelReadOnly:
		return "readonly"
	case LevelOperator:
		return "operator"
	case LevelAdmin:
		return "admin"
	case LevelDangerous:
		return "dangerous"
	default:
		return "unknown"
	}
}

func (l Level) RequiresConfirmation() bool {
	return l >= LevelAdmin
}

func (l Level) CanDisableConfirmation() bool {
	return l < LevelDangerous
}
```

**Step 4: Implement custom error types**

Create `internal/errors/errors.go`:
```go
package errors

import "fmt"

// NotConnectedError indicates no active database connection.
type NotConnectedError struct {
	Message string
}

func (e *NotConnectedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "not connected to any database instance"
}

// PermissionDeniedError indicates insufficient security level.
type PermissionDeniedError struct {
	Required string
	Current  string
	Action   string
}

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf("permission denied: action %q requires %s level, current level is %s",
		e.Action, e.Required, e.Current)
}

// QueryTimeoutError indicates a query exceeded its context deadline.
type QueryTimeoutError struct {
	SQL     string
	Timeout string
}

func (e *QueryTimeoutError) Error() string {
	return fmt.Sprintf("query timed out after %s", e.Timeout)
}

// SkillNotFoundError indicates an unknown skill name.
type SkillNotFoundError struct {
	Name string
}

func (e *SkillNotFoundError) Error() string {
	return fmt.Sprintf("unknown skill: %q", e.Name)
}

// InvalidParamsError indicates skill parameter validation failure.
type InvalidParamsError struct {
	Skill   string
	Message string
}

func (e *InvalidParamsError) Error() string {
	return fmt.Sprintf("invalid params for skill %q: %s", e.Skill, e.Message)
}
```

Write `internal/errors/errors_test.go` to verify error messages and type assertions.

**Step 5: Implement Params**

Create `internal/skill/params.go`:
```go
package skill

import (
	"encoding/json"
	"fmt"
)

type Params struct {
	raw map[string]any
}

func ParamsFromJSON(data []byte) (Params, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Params{}, fmt.Errorf("invalid JSON params: %w", err)
	}
	return Params{raw: raw}, nil
}

func ParamsFromMap(m map[string]any) Params {
	return Params{raw: m}
}

func (p Params) String(key string) (string, error) {
	v, ok := p.raw[key]
	if !ok {
		return "", fmt.Errorf("param %q not found", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("param %q is not a string", key)
	}
	return s, nil
}

func (p Params) StringOr(key, fallback string) string {
	s, err := p.String(key)
	if err != nil {
		return fallback
	}
	return s
}

func (p Params) Int(key string) (int, error) {
	v, ok := p.raw[key]
	if !ok {
		return 0, fmt.Errorf("param %q not found", key)
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, fmt.Errorf("param %q is not a number", key)
	}
}

func (p Params) IntOr(key string, fallback int) int {
	n, err := p.Int(key)
	if err != nil {
		return fallback
	}
	return n
}

func (p Params) Bool(key string) (bool, error) {
	v, ok := p.raw[key]
	if !ok {
		return false, fmt.Errorf("param %q not found", key)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("param %q is not a bool", key)
	}
	return b, nil
}

func (p Params) BoolOr(key string, fallback bool) bool {
	b, err := p.Bool(key)
	if err != nil {
		return fallback
	}
	return b
}

func (p Params) Has(key string) bool {
	_, ok := p.raw[key]
	return ok
}

func (p Params) Raw() map[string]any {
	cp := make(map[string]any, len(p.raw))
	for k, v := range p.raw {
		cp[k] = v
	}
	return cp
}
```

Note: `SecurityLevel` in the skill package should be a type alias. Create `internal/skill/types.go`:
```go
package skill

import "github.com/sqlrush/opendb/internal/security"

type SecurityLevel = security.Level

const (
	LevelReadOnly  = security.LevelReadOnly
	LevelOperator  = security.LevelOperator
	LevelAdmin     = security.LevelAdmin
	LevelDangerous = security.LevelDangerous
)
```

**Step 6: Implement Skill interface and Result**

Create `internal/skill/skill.go`:
```go
package skill

import "context"

type ResultType uint8

const (
	ResultTable   ResultType = iota // Tabular data
	ResultText                      // Plain/rich text
	ResultRefresh                   // Real-time refresh stream
	ResultError                     // Error display
)

type Result struct {
	Type     ResultType
	Data     any
	Summary  string
	Metadata map[string]string
}

type CLIDef struct {
	Command  string
	Aliases  []string
	Flags    []FlagDef
	Usage    string
	Examples []string
}

type FlagDef struct {
	Name         string
	Short        string
	Description  string
	DefaultValue string
	Required     bool
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Skill interface {
	Name() string
	Description() string
	ToolDef() ToolDef
	CLIDef() CLIDef
	Validate(params Params) error
	Execute(ctx context.Context, params Params) (*Result, error)
	SecurityLevel() SecurityLevel
}
```

**Step 7: Implement DB Driver interface**

Create `internal/db/types.go`:
```go
package db

import "time"

type ConnectionConfig struct {
	DBType   string
	Host     string
	Port     int
	Service  string
	User     string
	Password string
	Database string
}

type ServerInfo struct {
	Version      string
	InstanceName string
	Hostname     string
	Platform     string
	StartupTime  time.Time
}

type QueryResult struct {
	Columns  []string
	Rows     [][]any
	Duration time.Duration
}

type ExecResult struct {
	RowsAffected int64
	Duration     time.Duration
}

type TxOptions struct {
	ReadOnly bool
}

type TableInfo struct {
	Schema    string
	Name      string
	RowCount  int64
	SizeBytes int64
}

type ColumnInfo struct {
	Name     string
	Type     string
	Nullable bool
	Default  string
}
```

Create `internal/db/driver.go`:
```go
package db

import "context"

type Driver interface {
	Close() error
	Query(ctx context.Context, sql string, args ...any) (*QueryResult, error)
	Exec(ctx context.Context, sql string, args ...any) (*ExecResult, error)
	BeginTx(ctx context.Context, opts *TxOptions) (Tx, error)
	Ping(ctx context.Context) error
	ServerInfo() ServerInfo
}

type Tx interface {
	Query(ctx context.Context, sql string, args ...any) (*QueryResult, error)
	Exec(ctx context.Context, sql string, args ...any) (*ExecResult, error)
	Commit() error
	Rollback() error
}

type Inspector interface {
	Databases(ctx context.Context) ([]string, error)
	Schemas(ctx context.Context) ([]string, error)
	Tables(ctx context.Context, schema string) ([]TableInfo, error)
	Columns(ctx context.Context, schema, table string) ([]ColumnInfo, error)
}
```

**Step 8: Implement LLM Provider interface**

Create `internal/llm/types.go`:
```go
package llm

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequest struct {
	Messages    []Message
	Tools       []any
	MaxTokens   int
	Temperature *float64
}

type Response struct {
	Content    string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type StreamEventType uint8

const (
	StreamTextDelta     StreamEventType = iota
	StreamToolCallDelta
	StreamDone
)

type StreamEvent struct {
	Type    StreamEventType
	Content string
	ToolCall *ToolCall
}
```

Create `internal/llm/provider.go`:
```go
package llm

import "context"

type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*Response, error)
	ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
	Name() string
}

type Stream interface {
	Next() (StreamEvent, error)
	Close() error
}
```

**Step 9: Run all tests**

```bash
go test ./... -v
```
Expected: All PASS

**Step 10: Commit**

```bash
git add -A
git commit -m "feat: define core interfaces for db driver, skill, llm provider, and custom error types"
```

---

## Wave 2: Infrastructure (ALL PARALLEL)

> Phase 2, Phase 3, Phase 5, Phase 6, and Phase 8.1 can all be implemented in parallel since they depend only on Phase 1.

## Phase 2: Configuration & Logger

### Task 2.1: Configuration system

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/config/connection.go`
- Create: `internal/config/connection_test.go`
- Create: `configs/default.yaml`

**Step 1: Write config tests**

Create `internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	cfg := Default()
	if cfg.ConnectionsDir == "" {
		t.Error("ConnectionsDir should have a default value")
	}
	if cfg.Security.DefaultLevel != 0 {
		t.Errorf("default security level = %d, want 0", cfg.Security.DefaultLevel)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
connections_dir: /custom/path
security:
  default_level: 1
`), 0644)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ConnectionsDir != "/custom/path" {
		t.Errorf("ConnectionsDir = %q, want %q", cfg.ConnectionsDir, "/custom/path")
	}
	if cfg.Security.DefaultLevel != 1 {
		t.Errorf("DefaultLevel = %d, want 1", cfg.Security.DefaultLevel)
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/config/ -v
```

**Step 3: Implement config**

Create `internal/config/config.go` with `Config` struct, `Default()`, `LoadFromFile()`, `Load()` (auto-discover config location). Use `gopkg.in/yaml.v3` for parsing.

Create `internal/config/connection.go` with `ConnectionGroup` and `Connection` structs matching the YAML format from the design doc.

**Step 4: Run tests**

```bash
go test ./internal/config/ -v
```

**Step 5: Create default config template**

Create `configs/default.yaml`:
```yaml
# OpenDB default configuration
connections_dir: ~/.opendb/connections
security:
  default_level: 0
  confirm_on_dangerous: true
output:
  format: terminal
  max_rows: 1000
llm:
  provider: none
session:
  restore_on_switch: true
  history_dir: ~/.opendb/history
```

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: add configuration system with YAML loading and defaults"
```

---

### Task 2.2: Structured logger

**Files:**
- Create: `internal/logger/logger.go`
- Create: `internal/logger/logger_test.go`

**Step 1: Implement logger wrapper around log/slog**

Thin wrapper providing `Info()`, `Warn()`, `Error()`, `Debug()` with structured fields. Log to `~/.opendb/logs/opendb.log` with rotation. Tests verify log output contains expected fields.

**Step 2: Commit**

```bash
git add -A
git commit -m "feat: add structured logger using log/slog"
```

---

## Phase 3: Database Layer (Oracle)

### Task 3.1: Oracle driver implementation

**Files:**
- Create: `internal/db/oracle/driver.go`
- Create: `internal/db/oracle/driver_test.go`
- Create: `internal/db/oracle/queries.go`
- Create: `internal/db/oracle/typeconv.go`

**Step 1: Write Oracle driver tests**

Create `internal/db/oracle/driver_test.go`. Tests use build tag `//go:build integration` since they need a real Oracle instance. Also write unit tests for query building and result parsing that don't need a DB.

```go
package oracle

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db"
)

func TestNewOracleDriver_InvalidConfig(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host: "",
		Port: 0,
	}
	_, err := NewDriver(cfg)
	if err == nil {
		t.Error("expected error for empty config")
	}
}

func TestBuildDSN(t *testing.T) {
	cfg := db.ConnectionConfig{
		Host:     "10.0.1.100",
		Port:     1521,
		Service:  "orcl",
		User:     "dbadmin",
		Password: "secret",
	}
	dsn := buildDSN(cfg)
	if dsn == "" {
		t.Error("DSN should not be empty")
	}
}
```

**Step 2: Implement Oracle driver**

Create `internal/db/oracle/driver.go` implementing `db.Driver` using go-ora. Key methods:
- `NewDriver(cfg ConnectionConfig) (db.Driver, error)` — factory, validates config, connects
- `Query()` — execute SELECT, return `QueryResult`
- `Exec()` — execute DML/DDL, return `ExecResult`
- `ServerInfo()` — cache version/instance info at connect time
- `Ping()` — connection health check

Create `internal/db/oracle/queries.go` with Oracle-specific SQL constants:
```go
package oracle

const (
	queryServerInfo = `SELECT version_full, instance_name, host_name, startup_time FROM v$instance`
	querySlowSQL    = `SELECT sql_id, elapsed_time/1000 as elapsed_ms, executions, sql_text
	                   FROM v$sql WHERE elapsed_time/1000 > :threshold ORDER BY elapsed_time DESC`
	// ... more Oracle-specific queries
)
```

**Step 3: Implement Oracle type conversion**

Create `internal/db/oracle/typeconv.go` for handling Oracle-specific types that don't map cleanly to Go types:
- `TIMESTAMP WITH TIME ZONE` -> `time.Time`
- `TIMESTAMP WITH LOCAL TIME ZONE` -> `time.Time`
- `INTERVAL DAY TO SECOND` -> `time.Duration`
- `NUMBER` (with varying precision) -> `int64`, `float64`, or `string` depending on scale
- `CLOB` / `NCLOB` -> `string` (with size limit)
- `RAW` -> `[]byte`

Write `internal/db/oracle/typeconv_test.go` for round-trip conversion tests.

**Step 4: Add go-ora dependency**

```bash
go get github.com/sijms/go-ora/v2
```

**Step 5: Run unit tests**

```bash
go test ./internal/db/oracle/ -v -short
```

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: implement Oracle driver with go-ora and type conversion"
```

---

### Task 3.2: Oracle Inspector implementation

**Files:**
- Create: `internal/db/oracle/inspector.go`
- Create: `internal/db/oracle/inspector_test.go`

Implement `db.Inspector` interface for Oracle: list schemas (DBA_USERS), tables (DBA_TABLES), columns (DBA_TAB_COLUMNS). Unit tests for query construction, integration tests with build tag.

**Commit:** `feat: add Oracle inspector for schema/table/column metadata`

---

### Task 3.3: Mock driver for unit testing

**Files:**
- Create: `internal/db/mock/driver.go`
- Create: `internal/db/mock/driver_test.go`

Implement a mock implementation of `db.Driver` for unit testing skills without requiring a real Oracle instance.

```go
package mock

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// Driver is a mock db.Driver for unit testing.
// Callers set the QueryFunc / ExecFunc / etc. fields to control behavior.
type Driver struct {
	QueryFunc      func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
	ExecFunc       func(ctx context.Context, sql string, args ...any) (*db.ExecResult, error)
	PingFunc       func(ctx context.Context) error
	CloseFunc      func() error
	ServerInfoVal  db.ServerInfo
}

func (d *Driver) Query(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
	if d.QueryFunc != nil {
		return d.QueryFunc(ctx, sql, args...)
	}
	return &db.QueryResult{}, nil
}

func (d *Driver) Exec(ctx context.Context, sql string, args ...any) (*db.ExecResult, error) {
	if d.ExecFunc != nil {
		return d.ExecFunc(ctx, sql, args...)
	}
	return &db.ExecResult{}, nil
}

func (d *Driver) Ping(ctx context.Context) error {
	if d.PingFunc != nil {
		return d.PingFunc(ctx)
	}
	return nil
}

func (d *Driver) Close() error {
	if d.CloseFunc != nil {
		return d.CloseFunc()
	}
	return nil
}

func (d *Driver) ServerInfo() db.ServerInfo {
	return d.ServerInfoVal
}

func (d *Driver) BeginTx(ctx context.Context, opts *db.TxOptions) (db.Tx, error) {
	return nil, nil // extend as needed for testing
}
```

Test: verify mock driver satisfies `db.Driver` interface, verify QueryFunc/ExecFunc are called correctly.

**Commit:** `feat: add mock db.Driver for unit testing skills`

---

## Phase 5: Security Framework

### Task 5.1: Security guard and SQL guard

**Files:**
- Create: `internal/security/guard.go`
- Create: `internal/security/sqlguard.go`
- Create: `internal/security/audit.go`
- Create: `internal/security/guard_test.go`
- Create: `internal/security/sqlguard_test.go`

**Step 1: Write SQL guard tests (30+ cases)**

```go
func TestClassifySQL(t *testing.T) {
	tests := []struct {
		sql  string
		want Level
	}{
		// Basic reads
		{"SELECT * FROM users", LevelReadOnly},
		{"SHOW PARAMETER sga_target", LevelReadOnly},
		{"select count(*) from orders", LevelReadOnly},

		// DML
		{"INSERT INTO logs VALUES (1, 'test')", LevelAdmin},
		{"UPDATE users SET name = 'test' WHERE id = 1", LevelAdmin},
		{"DELETE FROM temp_table WHERE expired = 1", LevelAdmin},

		// Session management
		{"ALTER SESSION KILL SESSION '142,38'", LevelOperator},
		{"ALTER SESSION SET nls_date_format = 'YYYY-MM-DD'", LevelOperator},

		// DDL
		{"ALTER TABLE users ADD COLUMN email VARCHAR2(100)", LevelAdmin},
		{"CREATE TABLE test_data (id NUMBER)", LevelAdmin},
		{"CREATE INDEX idx_user_name ON users(name)", LevelAdmin},

		// Dangerous operations
		{"DROP TABLE users", LevelDangerous},
		{"DROP DATABASE", LevelDangerous},
		{"TRUNCATE TABLE orders", LevelDangerous},
		{"DROP INDEX idx_test", LevelDangerous},

		// WITH CTE + dangerous DML
		{"WITH cte AS (SELECT id FROM old) DELETE FROM users WHERE id IN (SELECT id FROM cte)", LevelDangerous},
		{"WITH cte AS (SELECT 1) UPDATE users SET status = 0", LevelAdmin},

		// Comments wrapping dangerous operations
		{"/* just a query */ DROP TABLE users", LevelDangerous},
		{"-- test\nDROP TABLE users", LevelDangerous},
		{"/* comment */ /* another */ TRUNCATE TABLE orders", LevelDangerous},

		// PL/SQL blocks
		{"BEGIN EXECUTE IMMEDIATE 'DROP TABLE users'; END;", LevelDangerous},
		{"BEGIN NULL; END;", LevelDangerous},  // PL/SQL blocks default to dangerous

		// MERGE INTO
		{"MERGE INTO target USING source ON (target.id = source.id) WHEN MATCHED THEN UPDATE SET val = source.val", LevelAdmin},

		// Case insensitivity
		{"drop table USERS", LevelDangerous},
		{"Drop Table Users", LevelDangerous},
		{"SELECT * FROM dual", LevelReadOnly},

		// Leading whitespace
		{"   SELECT 1 FROM dual", LevelReadOnly},
		{"  \t  DROP TABLE users", LevelDangerous},
		{"\n\nTRUNCATE TABLE orders", LevelDangerous},

		// Multiple statements (conservative: highest level wins)
		{"SELECT 1; DROP TABLE users", LevelDangerous},
		{"SELECT 1; SELECT 2", LevelReadOnly},

		// CONSERVATIVE default: unknown statements -> LevelDangerous
		{"UNKNOWN_COMMAND foo bar", LevelDangerous},
		{"", LevelDangerous},  // empty -> dangerous (conservative)
	}
	for _, tt := range tests {
		got := ClassifySQL(tt.sql)
		if got != tt.want {
			t.Errorf("ClassifySQL(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}
```

**Step 2: Implement**

- `ClassifySQL(sql string) Level` — parse SQL keywords to determine security level. CONSERVATIVE default: unknown statements classified as `LevelDangerous`.
- `Guard` struct implementing the Guard interface from design
- `AuditLog` — append-only log of all executed commands with timestamps

**Step 3: Run tests**

```bash
go test ./internal/security/ -v
```

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add security guard with SQL classification (30+ test cases) and audit logging"
```

---

## Phase 6: Output Formatting

### Task 6.1: Table formatter

**Files:**
- Create: `internal/format/table.go`
- Create: `internal/format/table_test.go`
- Create: `internal/format/json.go`
- Create: `internal/format/csv.go`
- Create: `internal/format/format.go`

**Step 1: Write table formatter tests**

```go
func TestFormatTable(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"SID", "USERNAME", "STATUS"},
		Rows: [][]any{
			{142, "APP_USER", "ACTIVE"},
			{287, "BATCH", "INACTIVE"},
		},
	}
	out := FormatTable(result)
	if !strings.Contains(out, "SID") {
		t.Error("output should contain column headers")
	}
	if !strings.Contains(out, "APP_USER") {
		t.Error("output should contain row data")
	}
}

func TestSmartTruncate(t *testing.T) {
	// 100 rows, max_rows=10 -> show first 10 + "... and 90 more rows"
	result := &db.QueryResult{
		Columns: []string{"ID"},
		Rows:    make([][]any, 100),
	}
	for i := range result.Rows {
		result.Rows[i] = []any{i}
	}
	out := FormatTableWithLimit(result, 10)
	if !strings.Contains(out, "90 more") {
		t.Error("should show truncation message")
	}
}
```

**Step 2: Implement formatters**

- `FormatTable()` — rich table with lipgloss styling, auto column width
- `FormatTableWithLimit()` — smart truncation
- `FormatJSON()` — JSON output
- `FormatCSV()` — CSV output
- `Formatter` interface with `Format(result *db.QueryResult, opts FormatOptions) string`

**Step 3: Commit**

```bash
git add -A
git commit -m "feat: add output formatters (table, JSON, CSV) with smart truncation"
```

---

## Phase 8.1: TUI Shell (Skeleton)

### Task 8.1: Basic bubbletea app with status bar and input

**Files:**
- Create: `internal/ui/app.go`
- Create: `internal/ui/statusbar.go`
- Create: `internal/ui/input.go`
- Create: `internal/ui/output.go`
- Create: `internal/ui/styles.go`
- Create: `internal/ui/app_test.go`

**Step 1: Add bubbletea dependencies**

```bash
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/lipgloss
go get github.com/charmbracelet/bubbles
```

**Step 2: Implement main app model**

Create `internal/ui/app.go` — bubbletea `Model` with:
- Status bar at top (connection info or "Not connected")
- Scrollable output area in middle
- Input line at bottom with `>` prompt
- `/` triggers fuzzy match dropdown

Create `internal/ui/statusbar.go` — renders:
```
┌─ prod-core-01 | 10.0.1.100:1521 | Oracle 19c EE | dbadmin | readonly ─┐
```

Create `internal/ui/input.go` — text input with:
- `/` prefix detection for command mode
- Fuzzy match dropdown from skill registry
- Up/Down arrow for dropdown navigation
- Enter to execute

Create `internal/ui/output.go` — scrollable output area:
- Append command results
- Support lipgloss styled tables
- Smart scroll to bottom on new output

Create `internal/ui/styles.go` — lipgloss style definitions:
- Status bar style (bold, colored background)
- Table header style
- Error style (red)
- Warning style (yellow)
- Success style (green)

**Step 3: Write teatest-based TUI tests**

Create `internal/ui/app_test.go` using `github.com/charmbracelet/x/exp/teatest`:

```go
func TestHelpOutput(t *testing.T) {
	// Verify /help renders the command list correctly
}

func TestSlashTriggersFuzzyMatch(t *testing.T) {
	// Verify typing "/" triggers fuzzy match dropdown
}

func TestStatusBarUpdatesOnConnect(t *testing.T) {
	// Verify status bar shows connection info after /login
}

func TestConfirmationDialogYesNo(t *testing.T) {
	// Verify confirmation dialog handles yes/no correctly for dangerous operations
}
```

**Step 4: Build and manually test**

```bash
make build && ./bin/opendb
```
Expected: Interactive TUI with status bar, input line, and output area.

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: implement TUI shell skeleton with bubbletea (status bar, input, output, teatest)"
```

---

## Wave 3: Core Systems (ALL PARALLEL)

> Phase 4, Phase 7, Phase 17.1, and Phase 9.5 can all be implemented in parallel.

## Phase 4: Connection Management

### Task 4.1: Credential providers

**Files:**
- Create: `internal/credential/credential.go`
- Create: `internal/credential/prompt.go`
- Create: `internal/credential/credential_test.go`

**Step 1: Define Provider interface and implement**

```go
package credential

type Provider interface {
	Resolve(connName string) (string, error)
}
```

- `PromptProvider`: read password from terminal (stdin)

> **MVP scope:** Only the `PromptProvider` is implemented for MVP. `EncryptedProvider` (AES-256) and `VaultProvider` are post-MVP.

Tests: prompt mock.

**Step 2: Commit**

```bash
git add -A
git commit -m "feat: add credential provider (prompt for MVP)"
```

---

### Task 4.2: Connection manager

**Files:**
- Create: `internal/connection/manager.go`
- Create: `internal/connection/manager_test.go`

**Step 1: Implement ConnectionManager**

Key responsibilities:
- Load connection groups from YAML files in connections dir
- Resolve credentials via credential.Provider
- Create db.Driver instances
- Track current active connection
- Support switching (store previous connection context)
- Auto-reconnect logic (silent for readonly, confirm for write)

Tests: load YAML fixtures, switch connections, verify reconnect behavior.

**Step 2: Commit**

```bash
git add -A
git commit -m "feat: add connection manager with group loading and switching"
```

---

## Phase 7: Skill Framework

### Task 7.1: Skill registry and executor

**Files:**
- Create: `internal/skill/registry.go`
- Create: `internal/skill/executor.go`
- Create: `internal/skill/registry_test.go`

**Step 1: Write registry tests**

```go
func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	mock := &mockSkill{name: "slowsql"}
	r.Register(mock)

	s, ok := r.Get("slowsql")
	if !ok || s.Name() != "slowsql" {
		t.Error("should find registered skill")
	}
}

func TestRegistryFuzzyMatch(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "slowsql"})
	r.Register(&mockSkill{name: "sessions"})
	r.Register(&mockSkill{name: "space"})

	matches := r.Match("sl")
	if len(matches) != 1 || matches[0].Name() != "slowsql" {
		t.Errorf("Match('sl') = %v, want [slowsql]", matches)
	}

	matches = r.Match("s")
	if len(matches) != 3 {
		t.Errorf("Match('s') should return 3, got %d", len(matches))
	}
}
```

**Step 2: Implement registry**

```go
type Registry struct {
	skills map[string]Skill
	names  []string // sorted for fuzzy match
}

func NewRegistry() *Registry
func (r *Registry) Register(s Skill)
func (r *Registry) Get(name string) (Skill, bool)
func (r *Registry) Match(prefix string) []Skill  // fuzzy match for dropdown
func (r *Registry) All() []Skill
```

**Step 3: Implement executor**

The executor uses custom error types from `internal/errors`:

```go
type Executor struct {
	registry *Registry
	guard    security.Guard
}

func (e *Executor) Execute(ctx context.Context, name string, params Params) (*Result, error) {
	skill, ok := e.registry.Get(name)
	if !ok {
		return nil, &errors.SkillNotFoundError{Name: name}
	}
	// 1. Validate params
	if err := skill.Validate(params); err != nil {
		return nil, err
	}
	// 2. Check security level
	if err := e.guard.Authorize(ctx, skill.SecurityLevel(), name); err != nil {
		return nil, err
	}
	// 3. Execute
	return skill.Execute(ctx, params)
}
```

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add skill registry with fuzzy matching and executor with security check"
```

---

## Phase 17.1: Ollama Provider

### Task 17.1: Ollama provider

**Files:**
- Create: `internal/llm/ollama/ollama.go`
- Create: `internal/llm/ollama/ollama_test.go`

Implement `llm.Provider` for Ollama (AilinkDB):
- `Chat()` — POST to `/v1/chat/completions`
- `ChatStream()` — SSE streaming from same endpoint
- Tool definitions from skill registry's `ToolDef()`

**Commit:** `feat: add Ollama LLM provider for AilinkDB integration`

---

## Phase 9.5: Session Infrastructure

> Session history save/load is required by `/login`'s restore feature and must be in place before skills are implemented.

### Task 9.5: Session history core

**Files:**
- Create: `internal/session/history.go`
- Create: `internal/session/restore.go`
- Create: `internal/session/history_test.go`

Key capabilities:
- Save command history entries to `~/.opendb/history/<instance>/<timestamp>.json`
- Load command history for a given instance
- `/history` command reads from this store

> **MVP scope:** Session restore only saves command history (input commands), not output. Full output persistence is post-MVP.

Tests: save/load round-trip, history pruning, edge cases (empty history, corrupt file).

**Commit:** `feat: add session history persistence (save/load command history)`

---

## Wave 4: Integration

## Phase 8.2: TUI Integration

### Task 8.2: Real-time refresh component

**Files:**
- Create: `internal/ui/refresh.go`
- Modify: `internal/ui/app.go`

Implement a refresh component for `/dbtop` and similar commands:
- Ticker-based refresh (configurable interval, default 3s)
- Renders in a bounded region of the output area
- Press `q` to stop refresh, freezes last snapshot as history
- Uses bubbletea `tea.Tick` for periodic updates

**Goroutine lifecycle management:** Use `errgroup.Group` for managing concurrent refresh goroutines, propagate `context.Context` for cancellation, and ensure explicit cleanup on component unmount. All background goroutines must respect context cancellation to prevent leaks.

**Commit:** `feat: add real-time refresh component for dbtop-style commands`

---

## Phase 9: Dispatch & Input Routing

### Task 9.1: Command dispatcher

**Files:**
- Create: `internal/dispatch/dispatcher.go`
- Create: `internal/dispatch/classifier.go`
- Create: `internal/dispatch/dispatcher_test.go`
- Create: `internal/dispatch/classifier_test.go`

**Step 1: Write classifier tests**

```go
func TestClassifyInput(t *testing.T) {
	tests := []struct {
		input string
		want  InputType
	}{
		{"/slowsql", InputSlashCommand},
		{"/login", InputSlashCommand},
		{"SELECT * FROM users", InputSQL},
		{"select count(*) from orders", InputSQL},
		{"INSERT INTO logs VALUES (1)", InputSQL},
		{"show parameter sga", InputSQL},
		{"帮我看看慢查询", InputNaturalLanguage},
		{"what are the slow queries", InputNaturalLanguage},
		{"", InputEmpty},
	}
	for _, tt := range tests {
		got := ClassifyInput(tt.input)
		if got != tt.want {
			t.Errorf("ClassifyInput(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
```

**Step 2: Implement classifier**

```go
type InputType uint8
const (
	InputEmpty InputType = iota
	InputSlashCommand
	InputSQL
	InputNaturalLanguage
)

func ClassifyInput(input string) InputType
```

Logic:
- Starts with `/` -> SlashCommand
- Starts with SQL keyword (SELECT, INSERT, UPDATE, DELETE, ALTER, CREATE, DROP, TRUNCATE, GRANT, REVOKE, SHOW, DESCRIBE, EXPLAIN, MERGE, WITH) -> SQL
- Otherwise -> NaturalLanguage (or prompt if no LLM)

**Step 3: Implement dispatcher**

Routes classified input to the right handler:
- SlashCommand -> parse command name + args -> skill executor
- SQL -> sqlguard classify -> security check -> db.Driver.Query/Exec
- NaturalLanguage -> LLM provider (or error if no LLM)

**Context cancellation strategy:** All dispatcher operations propagate `context.Context`. When the user presses Ctrl+C in the TUI, the context is cancelled, which propagates to the running database query or LLM call. The dispatcher listens for context cancellation and returns immediately with an appropriate error.

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add input classifier and command dispatcher"
```

---

## Wave 5: Skills (ALL PARALLEL)

> Phase 10, Phase 11, Phase 12, Phase 13, and Phase 14 can all be implemented in parallel after Wave 4.

## Phase 10: Basic Commands

### Task 10.1: /help, /clear, /history, /config

**Files:**
- Create: `internal/skill/builtin/admin/help.go`
- Create: `internal/skill/builtin/admin/clear.go`
- Create: `internal/skill/builtin/admin/history.go`
- Create: `internal/skill/builtin/admin/config.go`
- Create: tests for each

These are the simplest skills — implement them first as templates for all other skills.

Each skill implements the `Skill` interface:
- `Name()`, `Description()`, `ToolDef()`, `CLIDef()`
- `Validate()` — minimal
- `Execute()` — return Result
- `SecurityLevel()` — all L0

**Commit:** `feat: add basic commands (/help, /clear, /history, /config)`

---

### Task 10.2: /login, /logout

**Files:**
- Create: `internal/skill/builtin/admin/login.go`
- Create: `internal/skill/builtin/admin/logout.go`
- Create: tests for each

`/login`:
- If no args: show recent connections list for selection
- If args (connection name): connect directly
- If already connected: switch (save current session context)
- Interactive guided setup if no connections configured
- Updates status bar on success

`/logout`:
- Disconnect current connection
- Clear status bar

**Commit:** `feat: add /login and /logout commands with connection switching`

---

## Phase 11: SQL Execution & Query Skills

### Task 11.1: Direct SQL execution

**Files:**
- Modify: `internal/dispatch/dispatcher.go`
- Create: `internal/skill/builtin/query/sql.go`
- Create: `internal/skill/builtin/query/sql_test.go`

When input is classified as SQL:
1. `sqlguard.ClassifySQL()` determines security level
2. If L1+, security guard checks + confirmation
3. SELECT/SHOW -> `driver.Query()` -> format as table
4. DML/DDL -> `driver.Exec()` -> show affected rows
5. Multiple statements (separated by `;`) -> execute sequentially

**Multi-statement transaction semantics:** Multiple DML statements default to **auto-commit per statement**. Each statement is executed and committed independently. If the user wants transactional atomicity, they must wrap statements in an explicit `BEGIN` / `COMMIT` block using `driver.BeginTx()`. This is documented in `/help` output.

**Context cancellation:** All SQL execution propagates the context from the TUI. Ctrl+C cancels the running query via `context.Cancel()`.

**Commit:** `feat: add direct SQL execution with security classification`

---

### Task 11.2: /slowsql

**Files:**
- Create: `internal/skill/builtin/query/slowsql.go`
- Create: `internal/skill/builtin/query/slowsql_test.go`

Oracle SQL:
```sql
SELECT sql_id, elapsed_time/1000 as elapsed_ms, executions,
       rows_processed, buffer_gets, sql_text
FROM v$sql
WHERE elapsed_time/1000 > :threshold
ORDER BY elapsed_time DESC
FETCH FIRST :limit ROWS ONLY
```

CLI: `/slowsql` (default 1000ms) or `/slowsql 5000`

**Commit:** `feat: add /slowsql command`

---

### Task 11.3: /explain

**Files:**
- Create: `internal/skill/builtin/query/explain.go`
- Create: `internal/skill/builtin/query/explain_test.go`

Two modes:
- `/explain SELECT ...` -> `EXPLAIN PLAN FOR <sql>` then `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY)`
- `/explain abc123` (SQL ID) -> `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR(:sql_id))`

**Commit:** `feat: add /explain command (SQL text and SQL ID)`

---

## Phase 12: Monitor Skills

### Task 12.1: /sessions and /activesessions

**Files:**
- Create: `internal/skill/builtin/monitor/sessions.go`
- Create: `internal/skill/builtin/monitor/activesessions.go`
- Create: tests

`/sessions`:
```sql
SELECT sid, serial#, username, status, osuser, machine, program,
       sql_id, event, wait_class, seconds_in_wait
FROM v$session
ORDER BY status, username
```

`/activesessions` — same but `WHERE status = 'ACTIVE' AND username IS NOT NULL`

**Commit:** `feat: add /sessions and /activesessions commands`

---

### Task 12.2: /waits, /locks, /latches, /mutexes

**Files:**
- Create: `internal/skill/builtin/monitor/waits.go`
- Create: `internal/skill/builtin/monitor/locks.go`
- Create: `internal/skill/builtin/monitor/latches.go`
- Create: `internal/skill/builtin/monitor/mutexes.go`
- Create: tests for each

`/waits` — non-idle wait events:
```sql
SELECT event, wait_class, total_waits, time_waited_micro/1000000 as time_waited_sec,
       average_wait/100 as avg_wait_ms
FROM v$system_event
WHERE wait_class != 'Idle'
ORDER BY time_waited_micro DESC
```

`/locks` — row/table locks:
```sql
SELECT l.sid, l.type, l.lmode, l.request, l.block,
       s.username, s.program, o.object_name
FROM v$lock l
JOIN v$session s ON l.sid = s.sid
LEFT JOIN dba_objects o ON l.id1 = o.object_id
WHERE l.type IN ('TX','TM')
```

`/latches`:
```sql
SELECT name, gets, misses, sleeps, spin_gets,
       ROUND(misses/NULLIF(gets,0)*100, 2) as miss_ratio
FROM v$latch
WHERE misses > 0
ORDER BY sleeps DESC
```

`/mutexes`:
```sql
SELECT mutex_type, location, sleeps, wait_time
FROM v$mutex_sleep
WHERE sleeps > 0
ORDER BY sleeps DESC
```

**Commit:** `feat: add /waits, /locks, /latches, /mutexes commands`

---

## Phase 13: Admin Skills

### Task 13.1: /kill

**Files:**
- Create: `internal/skill/builtin/admin/kill.go`
- Create: tests

Security Level: L1 (requires confirmation, can be disabled in config)

`/kill 142` -> `ALTER SYSTEM KILL SESSION '<sid>,<serial#>'`
`/kill 142 immediate` -> `ALTER SYSTEM KILL SESSION '<sid>,<serial#>' IMMEDIATE`

Must look up serial# from v$session before killing.

**Commit:** `feat: add /kill command with L1 security confirmation`

---

### Task 13.2: /space, /params, /alert, /backup, /standby

**Files:**
- Create one file per skill in `internal/skill/builtin/admin/`
- Create tests for each

`/space`:
```sql
SELECT tablespace_name,
       ROUND(used_space * 8192 / 1024/1024, 2) as used_mb,
       ROUND(tablespace_size * 8192 / 1024/1024, 2) as total_mb,
       ROUND(used_percent, 2) as used_pct
FROM dba_tablespace_usage_metrics
ORDER BY used_percent DESC
```

`/params` — search database parameters:
```sql
SELECT name, value, description FROM v$parameter
WHERE UPPER(name) LIKE UPPER(:pattern)
```

> **MVP scope:** `/params` provides basic keyword search only. Smart parameter association (e.g., `sga` automatically showing `memory_target`, `pga_aggregate_target`) is V2.

`/alert` — read alert log with context:
```sql
SELECT originating_timestamp, message_text, message_level
FROM v$diag_alert_ext
WHERE originating_timestamp > SYSDATE - :hours/24
ORDER BY originating_timestamp DESC
```

> **MVP scope:** `/alert` shows chronological log entries. Error convergence (grouping same ORA- errors with count) is V2.

`/backup` — RMAN backup status:
```sql
SELECT start_time, end_time, status, input_type,
       output_bytes_display, time_taken_display
FROM v$rman_backup_job_details
WHERE start_time > SYSDATE - :days
ORDER BY start_time DESC
```

`/standby` — Data Guard status:
```sql
SELECT database_role, db_unique_name, protection_mode,
       switchover_status, dataguard_broker
FROM v$database
```
Plus gap detection from `v$archive_dest_status`.

**Commit:** `feat: add /space, /params, /alert, /backup, /standby commands`

---

## Phase 14: Schema Skills

### Task 14.1: /tableinfo and /indexadvise

**Files:**
- Create: `internal/skill/builtin/schema/tableinfo.go`
- Create: `internal/skill/builtin/schema/indexadvise.go`
- Create: tests

`/tableinfo ORDERS`:
- Table structure (columns, types, nullable, defaults)
- Indexes (name, columns, uniqueness)
- Statistics (row count, blocks, avg row length, last analyzed)
- Constraints (PK, FK, UK, CHECK)

`/indexadvise SELECT ...` or `/indexadvise <sql_id>`:
- Get execution plan
- Analyze full table scans
- Check filter predicates vs existing indexes

> **MVP scope:** `/indexadvise` only identifies full table scans and lists candidate columns from WHERE/JOIN predicates. Smart composite index recommendations and cost-based analysis are V2.

**Commit:** `feat: add /tableinfo and /indexadvise commands`

---

## Wave 6: Advanced Features (ALL PARALLEL)

> Phase 15, Phase 16, and Phase 17.2 can all be implemented in parallel.

## Phase 15: /health (Comprehensive Check)

### Task 15.1: Health check

**Files:**
- Create: `internal/skill/builtin/monitor/health.go`
- Create: `internal/skill/builtin/monitor/health_test.go`

Aggregates data from multiple sources into a single report:

```
Health Report — prod-core-01 (Oracle 19c)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

OK  Instance      UP (running 42 days 3 hours)
WARN Space        USERS tablespace 87% (above 85% threshold)
OK  Performance   CPU 23%, TPS 1,542, QPS 8,231
OK  Connections   142 / 500 (28%)
WARN Slow SQL     3 queries above 5s
OK  Wait Events   No anomalies
OK  Backup        Last full backup 2h ago, success
FAIL Alert Log    ORA-04031 appeared 12 times (last 24h)
OK  Standby       In sync, lag 0.3s
```

Each check runs independently, results aggregated with status icons.

**Commit:** `feat: add /health comprehensive check`

---

## Phase 16: /dbtop (Real-time Monitor)

### Task 16.1: dbtop implementation

**Files:**
- Create: `internal/skill/builtin/monitor/dbtop.go`
- Create: `internal/skill/builtin/monitor/dbtop_test.go`
- Modify: `internal/ui/refresh.go`

Real-time refreshing view (default 3s interval):

```
┌─ dbtop — prod-core-01 (Oracle 19c) ── refresh: 3s ── press q to exit ─┐
│                                                                         │
│ CPU: 23%  MEM: 68%  TPS: 1542  QPS: 8231  Connections: 142/500        │
│                                                                         │
│ Top Wait Events:                                                        │
│ db file sequential read    45.2%  ████████████                          │
│ log file sync              12.8%  ███                                   │
│ db file scattered read      8.1%  ██                                    │
│                                                                         │
│ Top SQL (by elapsed):                                                   │
│ abc123  15.2s  SELECT * FROM orders WHERE...                            │
│ def456   8.7s  UPDATE inventory SET qty...                              │
│                                                                         │
│ Active Sessions: 23                                                     │
└─────────────────────────────────────────────────────────────────────────┘
```

Implementation:
- Uses `internal/ui/refresh.go` component
- Queries: `v$sysstat`, `v$system_event`, `v$sql`, `v$session`
- On quit (`q`): freeze last snapshot, append to output history

**Goroutine lifecycle management:** Use `errgroup.Group` for concurrent data-fetching goroutines (sysstat, events, sql, sessions). All goroutines receive a shared `context.Context` that is cancelled when the user presses `q` or Ctrl+C. Explicit cleanup of tickers and goroutines on component teardown.

**Commit:** `feat: add /dbtop real-time monitoring view`

---

## Phase 17.2: Agentic Loop

### Task 17.2: Agentic loop

**Files:**
- Create: `internal/llm/agent/loop.go`
- Create: `internal/llm/agent/loop_test.go`
- Modify: `internal/dispatch/dispatcher.go`

The core agent loop:
```
1. User message + tool definitions -> LLM
2. LLM returns text or tool_use
3. If tool_use: execute skill, collect result
4. Send tool_result back to LLM
5. Repeat 2-4 until LLM returns text only
6. Render final response
```

**Commit:** `feat: add agentic loop for LLM-driven skill execution`

---

## Wave 7: Polish & Release

## Phase 19: First-time Setup & Polish

### Task 19.1: Interactive guided setup

**Files:**
- Modify: `internal/ui/app.go`
- Create: `internal/ui/wizard.go`

When no config exists:
1. Welcome message
2. Ask database type (Oracle for now)
3. Connection details (host, port, service, user)
4. Password strategy (prompt each time for MVP)
5. Test connection
6. Save to `~/.opendb/connections/<name>.yaml`

**Commit:** `feat: add first-time interactive setup wizard`

---

### Task 19.2: E2E tests

Write E2E tests covering critical user flows:
- First-time setup wizard flow
- `/login` -> `/sessions` -> `/logout` flow
- SQL execution with security confirmation flow
- `/help` displays all commands

---

### Task 19.3: Build, test, and release prep

**Files:**
- Modify: `Makefile`
- Create: `scripts/build.sh`

- Add `make release` target for cross-compilation (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64)
- Run full test suite: `make test`
- Run linter: `make lint`
- Verify single binary deployment: copy to temp dir, run, confirm it works

**Commit:** `chore: add release build targets and final test pass`

---

## MVP Scope Notes

The following features are explicitly scoped for MVP simplicity. Advanced versions are planned for V2.

| Feature | MVP Scope | V2 (Post-MVP) |
|---------|-----------|----------------|
| `/indexadvise` | Shows full table scans + candidate columns | Smart composite index recommendations, cost-based analysis |
| Session restore | Saves command history only (not output) | Full output persistence, per-instance session snapshots |
| `/alert` | Chronological log display | Error convergence (group same ORA- errors with count + context) |
| `/params` | Basic keyword search | Smart parameter association (e.g., sga -> memory_target) |
| Plugin system | **NOT in MVP** | Plugin manager, manifest loading, external plugin execution |
| Credential | Prompt provider only | Encrypted provider (AES-256), Vault provider |

---

## Execution Order Summary

| Wave | Phases | Parallel? | Description |
|------|--------|-----------|-------------|
| **Wave 1** | Phase 1 (1.1-1.2) | Sequential | Project scaffold + core types + custom error types |
| **Wave 2** | Phase 2 + Phase 3 + Phase 5 + Phase 6 + Phase 8.1 | **ALL PARALLEL** | Config+logger, Oracle driver+mock, security, formatters, TUI skeleton |
| **Wave 3** | Phase 4 + Phase 7 + Phase 17.1 + Phase 9.5 | **ALL PARALLEL** | Connection mgr, skill framework, Ollama provider, session infrastructure |
| **Wave 4** | Phase 8.2 + Phase 9 | Sequential | TUI integration (refresh) + dispatcher |
| **Wave 5** | Phase 10 + Phase 11 + Phase 12 + Phase 13 + Phase 14 | **ALL PARALLEL** | Basic commands, SQL+query skills, monitor skills, admin skills, schema skills |
| **Wave 6** | Phase 15 + Phase 16 + Phase 17.2 | **ALL PARALLEL** | /health, /dbtop, agentic loop |
| **Wave 7** | Phase 19 | Sequential | Wizard + E2E tests + release |

---

## Risk Notes

### 1. bubbletea TUI complexity
**Risk:** bubbletea's Elm-architecture can become complex with nested components (status bar, input with dropdown, output scroll, refresh overlay, confirmation dialogs). State management across components may lead to subtle bugs.
**Mitigation:** Keep each component (statusbar, input, output, refresh) as an independent bubbletea model with clear message passing. Write teatest-based tests early (Phase 8.1). Avoid deeply nested Update() chains.

### 2. go-ora type mapping
**Risk:** Oracle has many complex types (TIMESTAMP WITH TIME ZONE, INTERVAL, CLOB, NUMBER with varying precision) that don't map cleanly to Go types. go-ora may have edge cases or missing conversions.
**Mitigation:** Dedicated `typeconv.go` (Phase 3.1) with comprehensive round-trip tests. Fallback to `string` representation for unknown types. Test against real Oracle instance in integration tests.

### 3. SQL classification security
**Risk:** SQL classification via keyword matching can be bypassed (e.g., comments, dynamic SQL in PL/SQL, creative whitespace). A false negative could allow dangerous operations without confirmation.
**Mitigation:** CONSERVATIVE default — any unrecognized SQL is classified as `LevelDangerous`. 30+ test cases covering edge cases (CTE+DML, comment wrapping, PL/SQL blocks, MERGE). The confirmation dialog for L3 operations cannot be disabled regardless of config.

### 4. Goroutine lifecycle management
**Risk:** `/dbtop` refresh, auto-reconnect, and LLM streaming all spawn goroutines. Leaked goroutines can cause memory leaks, stale connections, or panic on closed channels.
**Mitigation:** All goroutines use `errgroup.Group` with a shared `context.Context`. Every goroutine must select on `ctx.Done()`. Explicit cleanup in component teardown. Test goroutine leak detection in tests using `goleak`.

### 5. Context cancellation propagation
**Risk:** If context cancellation doesn't propagate correctly from TUI to database queries, Ctrl+C may not cancel long-running queries, leaving the user stuck.
**Mitigation:** All `db.Driver` methods accept `context.Context`. The TUI creates a cancellable context per command execution. Ctrl+C triggers `context.Cancel()` which propagates through the dispatcher to the database driver. go-ora supports context cancellation natively. Test with artificially slow queries and verify cancellation.
