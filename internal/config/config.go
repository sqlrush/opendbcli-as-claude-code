/*-------------------------------------------------------------------------
 *
 * config.go
 *	  Config holds the top-level application configuration.
 *	  配置文件路径: ~/.opendb/config.yaml
 *	  支持单文件模式：connections 和 models 直接内联到
 *	  config.yaml
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/config/config.go
 *
 *-------------------------------------------------------------------------
 */
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sqlrush/opendb/internal/brand"
)

// ============================================================================
// Config — 顶层配置
// ============================================================================

// Config holds the top-level application configuration.
// 配置文件路径: ~/.opendb/config.yaml
// 支持单文件模式：connections 和 models 直接内联到 config.yaml
type Config struct {
	ConnectionsDir string               `yaml:"connections_dir,omitempty"` // 连接目录 (向后兼容，优先用 Connections)
	ModelsDir      string               `yaml:"models_dir,omitempty"`      // 模型目录 (向后兼容，优先用 Models)
	ActiveModel    string               `yaml:"active_model,omitempty"`    // 当前激活的模型名
	Security       SecurityConfig       `yaml:"security"`
	Output         OutputConfig         `yaml:"output"`
	LLM            LLMConfig            `yaml:"llm"`
	Session        SessionConfig        `yaml:"session"`
	Sentinel       SentinelConfig       `yaml:"sentinel"`
	Trace          TraceConfig          `yaml:"trace"`
	Scheduler      SchedulerConfig      `yaml:"scheduler"`
	ExternalSkills ExternalSkillsConfig `yaml:"external_skills"`
	MCP            MCPConfig            `yaml:"mcp"`
	Connections    []Connection         `yaml:"connections,omitempty"` // 内联连接配置 (单文件模式)
	Models         []ModelConfig        `yaml:"models,omitempty"`      // 内联模型配置 (单文件模式)
}

// ModelConfig defines an LLM model endpoint in config.yaml.
type ModelConfig struct {
	Name        string `yaml:"name"`
	Provider    string `yaml:"provider"`         // "ollama" | "openai"
	Vendor      string `yaml:"vendor,omitempty"` // "MiniMax" | "Moonshot" etc.
	BaseURL     string `yaml:"base_url"`
	Model       string `yaml:"model"`
	Capability  string `yaml:"capability,omitempty"` // "small" | "large"
	APIKey      string `yaml:"api_key,omitempty"`
	StripThink  bool   `yaml:"strip_think,omitempty"`
	Description string `yaml:"description,omitempty"`
	// ToolMode (v1.2.0): "" / "native" / "prompt".
	// Native (default) uses provider FC API; prompt injects tool descriptions
	// into the system prompt and parses JSON from the response (for LLMs/
	// deployments that don't expose function calling).
	ToolMode string `yaml:"tool_mode,omitempty"`
}

// ============================================================================
// 安全 (Security)
// ============================================================================

// SecurityConfig holds security-related settings.
type SecurityConfig struct {
	DefaultLevel       uint8 `yaml:"default_level"`        // 默认安全级别 (0=无限制)
	ConfirmOnDangerous bool  `yaml:"confirm_on_dangerous"` // 危险操作前确认 (默认 true)
}

// ============================================================================
// 输出 (Output)
// ============================================================================

// OutputConfig holds output formatting settings.
type OutputConfig struct {
	Format  string `yaml:"format"`   // 输出格式: "terminal" | "json" | "csv"
	MaxRows int    `yaml:"max_rows"` // 最大返回行数 (默认 1000)
}

// ============================================================================
// AI / LLM
// ============================================================================

// LLMConfig holds LLM provider settings.
type LLMConfig struct {
	Provider        string `yaml:"provider"`          // LLM 提供商: "ollama" | "none"
	Model           string `yaml:"model"`             // 模型名, 如 "qwen3.5:9b"
	BaseURL         string `yaml:"base_url"`          // API 地址, 如 "http://localhost:11434"
	Capability      string `yaml:"capability"`        // 模型能力: "small" (≤9B, guided) | "large" (≥27B, autonomous)
	DiagnoseMode    string `yaml:"diagnose_mode"`     // 诊断模式: "playbook" | "assist" | "auto"
	MaxRounds       int    `yaml:"max_rounds"`        // 最大轮次, 0=不限 (默认 0)
	MaxResultTokens int    `yaml:"max_result_tokens"` // 工具返回结果最大 token 数 (默认 4000)
}

// ============================================================================
// 可插拔 Skill
// ============================================================================

// ExternalSkillsConfig controls customer-provided script skills loaded from
// local directories. These skills are adapted into the normal skill.Skill
// registry and therefore still go through DBAA validation, security guard, and
// tracing.
type ExternalSkillsConfig struct {
	Enabled              bool          `yaml:"enabled"`
	Dirs                 []string      `yaml:"dirs"`
	AllowOverrideBuiltin bool          `yaml:"allow_override_builtin"`
	MaxTimeout           time.Duration `yaml:"max_timeout"`
	MaxOutputBytes       int64         `yaml:"max_output_bytes"`
	MaxStderrBytes       int64         `yaml:"max_stderr_bytes"`
	InheritEnv           bool          `yaml:"inherit_env"`
	EnvAllowlist         []string      `yaml:"env_allowlist"`
	ExposeDBCredentials  bool          `yaml:"expose_db_credentials"`
}

// MCPConfig is the P1 configuration surface for adapting explicitly allowlisted
// MCP tools into DBAA skills. MCP remains opt-in and deny-by-default.
type MCPConfig struct {
	Enabled bool              `yaml:"enabled"`
	Servers []MCPServerConfig `yaml:"servers"`
}

// MCPServerConfig describes one customer MCP server.
type MCPServerConfig struct {
	Name    string                       `yaml:"name"`
	Command []string                     `yaml:"command"`
	Timeout time.Duration                `yaml:"timeout"`
	Tools   map[string]MCPToolAllowEntry `yaml:"tools"`
}

// MCPToolAllowEntry overlays DBAA skill metadata onto an MCP tool.
type MCPToolAllowEntry struct {
	Enabled     bool     `yaml:"enabled"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Security    string   `yaml:"security"`
	DBTypes     []string `yaml:"db_types"`
	Triggers    []string `yaml:"triggers"`
}

// ============================================================================
// 会话 (Session)
// ============================================================================

// SessionConfig holds session management settings.
type SessionConfig struct {
	RestoreOnSwitch bool   `yaml:"restore_on_switch"` // 切换连接时恢复会话变量 (默认 true)
	HistoryDir      string `yaml:"history_dir"`       // 命令历史目录
}

// ============================================================================
// 调度器 (Scheduler) — 定时巡检引擎
// ============================================================================

// SchedulerConfig controls the background scheduled task engine.
type SchedulerConfig struct {
	AutoStart bool               `yaml:"auto_start"` // 登录后自动启动 (默认 true)
	Tasks     []SchedulerTaskDef `yaml:"tasks"`      // 巡检任务列表
}

// SchedulerTaskDef defines a scheduled task in config.yaml.
type SchedulerTaskDef struct {
	Name     string         `yaml:"name"`
	Schedule string         `yaml:"schedule"`
	Skill    string         `yaml:"skill"`
	Params   map[string]any `yaml:"params,omitempty"`
	OnWarn   string         `yaml:"on_warn"` // "notify" | "auto_fix"
	Enabled  *bool          `yaml:"enabled"` // nil = true
}

// ============================================================================
// 哨兵 (Sentinel) — 后台实时异常检测
// ============================================================================

// SentinelConfig controls the background anomaly detection engine.
type SentinelConfig struct {
	// ── 基本行为 ──
	AutoStart           bool          `yaml:"auto_start"`             // 登录后自动启动 (默认 true)
	ProbeInterval       time.Duration `yaml:"probe_interval"`         // 轻探针间隔 (默认 1s)
	BaselineWindow      int           `yaml:"baseline_window"`        // 基线滑动窗口大小 (默认 60 样本)
	MinSamples          int           `yaml:"min_samples"`            // 最少样本数才开始检测 (默认 10)
	LongSQLThresholdSec int           `yaml:"long_sql_threshold_sec"` // 慢SQL判定阈值秒数 (默认 30)

	// ── 触发模式 ──
	// "adaptive": 自适应 3σ，根据基线均值+标准差自动算阈值 (默认)
	// "fixed":    固定阈值，使用下方各指标的倍数/绝对值
	TriggerMode    string  `yaml:"trigger_mode"`    // "adaptive" | "fixed"
	Sigma          float64 `yaml:"sigma"`           // adaptive 模式的 σ 倍数 (默认 3.0)
	SustainedCount int     `yaml:"sustained_count"` // 连续 N 个采样点超标才触发 (默认 3)

	// ── 固定模式阈值 (trigger_mode: fixed 时生效) ──
	Thresholds ThresholdConfig `yaml:"thresholds"`

	// ── 突发采集 (Burst) ──
	BurstInterval    time.Duration `yaml:"burst_interval"`     // burst 帧间隔 (默认 200ms)
	BurstMaxDuration time.Duration `yaml:"burst_max_duration"` // burst 最长持续 (默认 30s)
	BurstCalmDelay   time.Duration `yaml:"burst_calm_delay"`   // 确认恢复的等待时间 (默认 5s)

	// ── 冷却 ──
	Cooldown time.Duration `yaml:"cooldown"` // 两次触发间最小间隔 (默认 5min)
}

// ThresholdConfig defines fixed-mode thresholds for each probe metric.
// 每个指标可以设 multiplier (基线的倍数) 或 absolute (绝对值)。
// 设为 0 或不设表示不启用该指标的固定阈值检测。
type ThresholdConfig struct {
	// ── 会话类指标 ──
	ActiveMultiplier float64 `yaml:"active_multiplier"` // 活跃会话超过基线 N 倍触发 (默认 2.0)
	CPUMultiplier    float64 `yaml:"cpu_multiplier"`    // CPU 会话超过基线 N 倍触发 (默认 2.0)
	IOMultiplier     float64 `yaml:"io_multiplier"`     // I/O 等待会话超过基线 N 倍触发 (默认 3.0)
	LockAbsolute     float64 `yaml:"lock_absolute"`     // 锁等待会话 ≥ N 个触发 (默认 5)
	LongSQLAbsolute  float64 `yaml:"long_sql_absolute"` // 慢SQL(>10s) ≥ N 个触发 (默认 3)

	// ── 吞吐类指标 ──
	RedoMultiplier      float64 `yaml:"redo_multiplier"`       // Redo 速率超过基线 N 倍触发 (默认 5.0)
	HardParseMultiplier float64 `yaml:"hard_parse_multiplier"` // 硬解析速率超过基线 N 倍触发 (默认 3.0)
}

// ============================================================================
// 堆栈追踪 (Trace) — OS 级火焰图分析
// ============================================================================

// TraceConfig controls the OS-level stack trace and flame graph feature.
type TraceConfig struct {
	Auto     bool                         `yaml:"auto"`                 // Sentinel 联动自动采集 (默认 false)
	Duration int                          `yaml:"duration,omitempty"`   // 默认采集秒数 (默认 3, 范围 1-10)
	TopN     int                          `yaml:"top_n,omitempty"`      // 热点函数数量 (默认 20)
	OutDir   string                       `yaml:"output_dir,omitempty"` // SVG 输出目录 (默认 ~/.opendb/trace/)
	Source   TraceSourceConfig            `yaml:"source,omitempty"`     // 全局源码配置 (兜底)
	Sources  map[string]TraceSourceConfig `yaml:"sources,omitempty"`    // 按数据库类型配置: mysql/postgres/oracle
}

// SourceFor returns the source config for a given database type.
// Priority: sources[dbType] > source (global fallback).
func (c *TraceConfig) SourceFor(dbType string) TraceSourceConfig {
	if c.Sources != nil {
		if src, ok := c.Sources[dbType]; ok {
			return src
		}
	}
	return c.Source
}

// TraceSourceConfig specifies where to find database source code.
type TraceSourceConfig struct {
	Dir    string `yaml:"dir,omitempty"`    // 本地源码路径 (优先)
	Repo   string `yaml:"repo,omitempty"`   // GitHub 仓库, 如 "mysql/mysql-server"
	Branch string `yaml:"branch,omitempty"` // 分支 (默认 main)
	Token  string `yaml:"token,omitempty"`  // GitHub API token (可选, 提高频率限制)
}

// ============================================================================
// 默认值
// ============================================================================

// DefaultOpenDBDir returns the base directory for the active brand's
// configuration. Brand-aware:
//   - opendb (default): $OPENDB_HOME or ~/.opendb
//   - dbaa:             $DBAA_HOME or ~/.dbaa
//
// All read paths (config, models, connections, history, sessions,
// memory, policies) MUST resolve through here so dbaa and opendb stay
// fully isolated. Bug discovered v1.1.20: this used to be hardcoded
// to ~/.opendb, causing dbaa to silently read opendb's connections /
// models from /login and /model.
func DefaultOpenDBDir() string {
	br := brand.Current()
	if dir := os.Getenv(br.ConfigEnvVar); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return br.ConfigDirName
	}
	return filepath.Join(home, br.ConfigDirName)
}

// Default returns a Config populated with sensible default values.
func Default() Config {
	baseDir := DefaultOpenDBDir()
	return Config{
		ConnectionsDir: filepath.Join(baseDir, "connections"),
		ModelsDir:      filepath.Join(baseDir, "models"),
		Security: SecurityConfig{
			DefaultLevel:       0,
			ConfirmOnDangerous: true,
		},
		Output: OutputConfig{
			Format:  "terminal",
			MaxRows: 1000,
		},
		LLM: LLMConfig{
			Provider:        "none",
			Model:           "qwen3.5:9b",
			BaseURL:         "http://localhost:11434",
			Capability:      "large",
			DiagnoseMode:    "auto",
			MaxRounds:       0,
			MaxResultTokens: 4000,
		},
		Session: SessionConfig{
			RestoreOnSwitch: true,
			HistoryDir:      filepath.Join(baseDir, "history"),
		},
		Scheduler: SchedulerConfig{
			AutoStart: true,
			Tasks: []SchedulerTaskDef{
				{Name: "space-watch", Schedule: "@every 10m", Skill: "space", OnWarn: "notify"},
				{Name: "alert-scan", Schedule: "@every 15m", Skill: "alert", Params: map[string]any{"hours": 0.25}, OnWarn: "notify"},
				{Name: "stats-check", Schedule: "@daily 02:00", Skill: "health", OnWarn: "notify"},
				{Name: "backup-check", Schedule: "@daily 08:00", Skill: "backup", OnWarn: "notify"},
				{Name: "password-check", Schedule: "@daily 09:00", Skill: "sessions", OnWarn: "notify"},
			},
		},
		ExternalSkills: ExternalSkillsConfig{
			Enabled:              true,
			Dirs:                 []string{filepath.Join(baseDir, "skills")},
			AllowOverrideBuiltin: false,
			MaxTimeout:           60 * time.Second,
			MaxOutputBytes:       256 * 1024,
			MaxStderrBytes:       64 * 1024,
			InheritEnv:           false,
			EnvAllowlist:         []string{"PATH", "LANG"},
			ExposeDBCredentials:  false,
		},
		MCP: MCPConfig{
			Enabled: false,
			Servers: nil,
		},
		Sentinel: SentinelConfig{
			AutoStart:           true,
			ProbeInterval:       time.Second,
			BaselineWindow:      60,
			MinSamples:          10,
			LongSQLThresholdSec: 30,
			TriggerMode:         "adaptive",
			Sigma:               3.0,
			SustainedCount:      3,
			Thresholds: ThresholdConfig{
				ActiveMultiplier:    2.0,
				CPUMultiplier:       2.0,
				IOMultiplier:        3.0,
				LockAbsolute:        5,
				LongSQLAbsolute:     3,
				RedoMultiplier:      5.0,
				HardParseMultiplier: 3.0,
			},
			BurstInterval:    200 * time.Millisecond,
			BurstMaxDuration: 30 * time.Second,
			BurstCalmDelay:   5 * time.Second,
			Cooldown:         5 * time.Minute,
		},
	}
}

// ============================================================================
// 加载
// ============================================================================

// LoadFromFile reads a YAML configuration file at the given path and returns
// a Config. Fields not specified in the file retain their zero values.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	return &cfg, nil
}

// Load auto-discovers configuration from:
//  1. The OPENDB_CONFIG environment variable (if set)
//  2. ~/.opendb/config.yaml (if it exists)
//  3. Falls back to Default() if neither is found
func Load() (*Config, error) {
	if envPath := os.Getenv("OPENDB_CONFIG"); envPath != "" {
		return LoadFromFile(envPath)
	}

	defaultPath := filepath.Join(DefaultOpenDBDir(), "config.yaml")
	if _, err := os.Stat(defaultPath); err == nil {
		return LoadFromFile(defaultPath)
	}

	cfg := Default()
	return &cfg, nil
}

// DefaultConfigPath returns the path Load() would use to read config.yaml:
// OPENDB_CONFIG env var if set, else <DefaultOpenDBDir>/config.yaml.
// Used by writers (e.g. /model add wizard) so they target the same file
// the loader reads from.
func DefaultConfigPath() string {
	if envPath := os.Getenv("OPENDB_CONFIG"); envPath != "" {
		return envPath
	}
	return filepath.Join(DefaultOpenDBDir(), "config.yaml")
}
