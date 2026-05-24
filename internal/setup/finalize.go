/*-------------------------------------------------------------------------
 *
 * finalize.go
 *	  GenerateConfig writes a single config.yaml with everything inline
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/finalize.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/credential"
	"github.com/sqlrush/opendb/internal/model"
)

// GenerateConfig writes a single config.yaml with everything inline
func GenerateConfig(cfg *SetupConfig) error {
	// Create base dir and history dir
	if err := os.MkdirAll(cfg.BaseDir, 0o750); err != nil {
		return fmt.Errorf("create base dir: %w", err)
	}
	histDir := filepath.Join(cfg.BaseDir, "history")
	if err := os.MkdirAll(histDir, 0o750); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}

	// Build single config with inline connections and models.
	// v1.1.20: model entry name uses "default" alias (not the raw model ID)
	// so users can switch via "/model default" without typing the full ID
	// like "deepseek-v4-pro". active_model points at this alias.
	// All models go inline in config.yaml; models_dir is no longer written
	// (existing installs keep working — manager merges dir + inline).
	const setupModelEntryName = "default"
	appConfig := config.Config{
		Security: cfg.Security,
		Output: config.OutputConfig{
			Format:  "terminal",
			MaxRows: 1000,
		},
		LLM: cfg.LLM,
		Session: config.SessionConfig{
			RestoreOnSwitch: true,
			HistoryDir:      histDir,
		},
		Sentinel:    cfg.Sentinel,
		ActiveModel: setupModelEntryName,
		Connections: []config.Connection{cfg.Connection},
	}

	// Add model config if LLM was configured. Capability is inferred from the
	// model ID and provider so the user gets the right diagnosis mode (Auto
	// for large cloud models, Assist for small local ones) without having to
	// understand the small/large taxonomy upfront.
	if cfg.LLM.Provider != "" && cfg.LLM.Provider != "none" {
		provider := "openai"
		if cfg.LLM.Provider == "ollama" {
			provider = "ollama"
		}
		appConfig.Models = []config.ModelConfig{{
			Name:       setupModelEntryName, // "default" alias, not raw model ID
			Provider:   provider,
			Vendor:     cfg.LLMVendor,
			BaseURL:    cfg.LLM.BaseURL,
			Model:      cfg.LLM.Model, // actual model ID like "deepseek-v4-pro"
			Capability: model.InferCapability(provider, cfg.LLM.Model),
			APIKey:     cfg.LLMApiKey,
		}}
	}

	configPath := filepath.Join(cfg.BaseDir, "config.yaml")
	configData := renderConfigWithComments(appConfig)
	if err := os.WriteFile(configPath, []byte(configData), 0o640); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Save encrypted password if auth_mode is "save"
	if cfg.Connection.AuthMode == "save" && cfg.Password != "" {
		credDir := filepath.Join(cfg.BaseDir, "credentials")
		provider := credential.NewEncryptedProvider(credDir)
		connName := cfg.Connection.Name
		if connName == "" {
			connName = "default"
		}
		if err := provider.Store(connName, cfg.Password); err != nil {
			return fmt.Errorf("save credential: %w", err)
		}
	}

	return nil
}

type generateDoneMsg struct{ err error }

// FinalizeStep generates config files and shows completion summary
type FinalizeStep struct {
	cfg       *SetupConfig
	generated bool
	err       error
	done      bool
}

func NewFinalizeStep(cfg *SetupConfig) *FinalizeStep {
	return &FinalizeStep{cfg: cfg}
}

func (s *FinalizeStep) Title() string { return "Finalize" }
func (s *FinalizeStep) Done() bool    { return s.done }

func (s *FinalizeStep) Summary() string {
	return "" // Final step — no folded summary needed
}

func (s *FinalizeStep) Init() tea.Cmd {
	return func() tea.Msg {
		err := GenerateConfig(s.cfg)
		return generateDoneMsg{err: err}
	}
}

func (s *FinalizeStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case generateDoneMsg:
		s.generated = true
		s.err = msg.err
		if s.err != nil {
			s.done = true
			return s, emitDone
		}
		return s, nil
	case tea.KeyMsg:
		if msg.String() == "enter" && s.generated {
			s.done = true
			return s, emitDone
		}
	}
	return s, nil
}

func (s *FinalizeStep) View() string {
	if !s.generated {
		return "\n" + BulletLine("Generating configuration...") + "\n"
	}

	if s.err != nil {
		return "\n" + ErrorLine("Configuration failed: "+s.err.Error()) + "\n"
	}

	var b strings.Builder

	// ── Configuration Summary ──
	conn := s.cfg.Connection
	connAddr := conn.Host
	if conn.Port > 0 {
		connAddr += fmt.Sprintf(":%d", conn.Port)
	}
	svc := conn.Service
	if svc == "" {
		svc = conn.Database
	}
	connStatus := "configured"
	if s.cfg.ConnTestOK {
		connStatus = "✓ Connected (" + s.cfg.ConnVersion + ")"
	}

	sentinelStatus := "Off"
	if s.cfg.Sentinel.AutoStart {
		sentinelStatus = fmt.Sprintf("Auto-start · %s interval", s.cfg.Sentinel.ProbeInterval)
	}

	llmStatus := "Not configured"
	if s.cfg.LLM.Provider != "none" {
		llmStatus = s.cfg.LLMProvider + " · " + s.cfg.LLM.Model
		if s.cfg.LLMTestOK {
			llmStatus += " · ✓ Connected"
		}
	}

	ruleStatus := "Disabled"
	if s.cfg.RuleEngine {
		ruleStatus = "Enabled"
	}

	securityStatus := "Off"
	if s.cfg.Security.ConfirmOnDangerous {
		securityStatus = "Confirm on dangerous"
	}

	configPath := filepath.Join(s.cfg.BaseDir, "config.yaml")
	connName := conn.Name
	if connName == "" {
		connName = "default"
	}

	// Connection + model are now inline in config.yaml (post-v1.1.20). Don't
	// print a fake `connections/<name>.yaml` path that doesn't exist on disk.
	summaryContent := strings.Join([]string{
		StyleBrand.Render("Database") + ":  " + DBDisplayName(s.cfg.DBType) +
			" · " + connAddr + "/" + svc + " · " + conn.User,
		"            " + connStatus,
		StyleBrand.Render("Sentinel") + ":  " + sentinelStatus,
		StyleBrand.Render("Model") + ":     " + llmStatus,
		StyleBrand.Render("Rule") + ":      " + ruleStatus,
		StyleBrand.Render("Security") + ":  " + securityStatus,
		"",
		StyleDim.Render("Config:     " + configPath + " (连接 / 模型 inline 在此)"),
	}, "\n")
	b.WriteString("\n" + InfoPanel("Configuration Summary", summaryContent) + "\n")

	// ── Getting Started ──
	gettingStarted := strings.Join([]string{
		StyleTitle.Render("常用 Skill:"),
		padCmdDesc(brand.Current().BinaryName, "启动交互式 REPL"),
		padCmdDesc(brand.Current().BinaryName+" configure", "添加更多连接或修改配置"),
		padCmdDesc("/login", "登录数据库连接"),
		padCmdDesc("/model", "选择模型"),
		padCmdDesc("/health", "数据库健康检查"),
		padCmdDesc("/dbtop", "秒级监控大盘"),
		padCmdDesc("/llm", "启动 Model 诊断问题"),
		padCmdDesc("/rule", "启动 Rule 诊断问题"),
		"",
		StyleDim.Render("Home:   https://opendbcli.org"),
		StyleDim.Render("GitHub: https://github.com/sqlrush/opendbcli"),
	}, "\n")
	b.WriteString("\n" + InfoPanel("Getting Started", gettingStarted) + "\n")

	// ── Welcome message ──
	b.WriteString("\n" + StyleBrand.Render("  └  欢迎来到 "+brand.Current().AppName+" 的世界，祝使用愉快！") + "\n")
	b.WriteString("\n" + StyleDim.Render("  Press Enter to exit setup.") + "\n")

	return b.String()
}

func padCmdDesc(cmd, desc string) string {
	styled := StyleBrand.Render(cmd)
	pad := 20 - len(cmd)
	if pad < 2 {
		pad = 2
	}
	return styled + strings.Repeat(" ", pad) + StyleDim.Render("— "+desc)
}

func formatRow(label, status, detail string) string {
	if label == "" {
		return "             " + detail
	}
	padded := label
	for len(padded) < 10 {
		padded += " "
	}
	return StyleBrand.Render(padded) + status + "  " + detail
}

func enableLabel(enabled bool) string {
	if enabled {
		return StyleSuccess.Render(" Enable ")
	}
	return StyleDim.Render("Disable")
}

// renderConfigWithComments generates config.yaml with Chinese+English comments
func renderConfigWithComments(cfg config.Config) string {
	var b strings.Builder

	b.WriteString("# " + brand.Current().AppName + " Configuration / " + brand.Current().AppName + " 配置文件\n")
	b.WriteString("# Docs: https://www.opendbcli.org\n")
	b.WriteString("# GitHub: https://github.com/sqlrush/opendb\n\n")

	// Security
	b.WriteString("# Security settings / 安全配置\n")
	b.WriteString("security:\n")
	b.WriteString("  # Confirm before dangerous operations (DROP/DELETE/TRUNCATE)\n")
	b.WriteString("  # 执行危险操作前是否需要二次确认\n")
	b.WriteString(fmt.Sprintf("  confirm_on_dangerous: %v\n", cfg.Security.ConfirmOnDangerous))
	b.WriteString("\n")

	// Output
	b.WriteString("# Output settings / 输出配置\n")
	b.WriteString("output:\n")
	b.WriteString("  # Output format: terminal | json | csv\n")
	b.WriteString("  # 输出格式: terminal(终端表格) | json | csv\n")
	b.WriteString(fmt.Sprintf("  format: %s\n", cfg.Output.Format))
	b.WriteString("  # Maximum rows returned per query / 每次查询最大返回行数\n")
	b.WriteString(fmt.Sprintf("  max_rows: %d\n", cfg.Output.MaxRows))
	b.WriteString("\n")

	// Session
	b.WriteString("# Session settings / 会话配置\n")
	b.WriteString("session:\n")
	b.WriteString("  # Restore session variables when switching connections\n")
	b.WriteString("  # 切换连接时是否恢复会话变量\n")
	b.WriteString(fmt.Sprintf("  restore_on_switch: %v\n", cfg.Session.RestoreOnSwitch))
	b.WriteString("  # Command history directory / 命令历史目录\n")
	b.WriteString(fmt.Sprintf("  history_dir: %s\n", cfg.Session.HistoryDir))
	b.WriteString("\n")

	// Sentinel
	b.WriteString("# Sentinel — Real-time anomaly detection / 实时异常检测引擎\n")
	b.WriteString("sentinel:\n")
	b.WriteString("  # Auto-start after connecting to database / 连接数据库后自动启动\n")
	b.WriteString(fmt.Sprintf("  auto_start: %v\n", cfg.Sentinel.AutoStart))
	b.WriteString("  # Probe interval (e.g. 1s, 3s, 5s) / 探针采集间隔\n")
	b.WriteString(fmt.Sprintf("  probe_interval: %s\n", cfg.Sentinel.ProbeInterval))
	b.WriteString("  # Baseline window size (samples) / 基线滑动窗口大小（样本数）\n")
	b.WriteString(fmt.Sprintf("  baseline_window: %d\n", cfg.Sentinel.BaselineWindow))
	b.WriteString("  # Minimum samples before detection starts / 最少样本数才开始检测\n")
	b.WriteString(fmt.Sprintf("  min_samples: %d\n", cfg.Sentinel.MinSamples))
	b.WriteString("  # Trigger mode: adaptive (3σ) | fixed (threshold)\n")
	b.WriteString("  # 触发模式: adaptive(自适应3σ) | fixed(固定阈值)\n")
	b.WriteString(fmt.Sprintf("  trigger_mode: %s\n", cfg.Sentinel.TriggerMode))
	b.WriteString("  # Sigma multiplier for adaptive mode / 自适应模式的 σ 倍数\n")
	b.WriteString(fmt.Sprintf("  sigma: %v\n", cfg.Sentinel.Sigma))
	b.WriteString("  # Consecutive ticks above threshold to trigger / 连续超标次数才触发\n")
	b.WriteString(fmt.Sprintf("  sustained_count: %d\n", cfg.Sentinel.SustainedCount))
	b.WriteString("  # Burst collection interval / 突发采集间隔\n")
	b.WriteString(fmt.Sprintf("  burst_interval: %s\n", cfg.Sentinel.BurstInterval))
	b.WriteString("  # Max burst duration / 突发采集最长持续时间\n")
	b.WriteString(fmt.Sprintf("  burst_max_duration: %s\n", cfg.Sentinel.BurstMaxDuration))
	b.WriteString("  # Cooldown between triggers / 两次触发间最小间隔\n")
	b.WriteString(fmt.Sprintf("  cooldown: %s\n", cfg.Sentinel.Cooldown))
	b.WriteString("\n")

	// LLM
	b.WriteString("# LLM settings / 大模型基础配置\n")
	b.WriteString("llm:\n")
	b.WriteString("  # Provider: openai | ollama | none\n")
	b.WriteString("  # 提供商: openai(兼容所有云端API) | ollama(本地) | none(禁用)\n")
	b.WriteString(fmt.Sprintf("  provider: %s\n", cfg.LLM.Provider))
	b.WriteString("  # Model name / 模型名称\n")
	b.WriteString(fmt.Sprintf("  model: %s\n", cfg.LLM.Model))
	b.WriteString("  # API base URL / API 地址\n")
	b.WriteString(fmt.Sprintf("  base_url: %s\n", cfg.LLM.BaseURL))
	b.WriteString("  # Diagnose mode: playbook | assist | auto\n")
	b.WriteString("  # 诊断模式: playbook(编排式) | assist(辅助式) | auto(自动选择)\n")
	b.WriteString(fmt.Sprintf("  diagnose_mode: %s\n", cfg.LLM.DiagnoseMode))
	b.WriteString("\n")

	// Active model
	b.WriteString("# Active model name (from models list below)\n")
	b.WriteString("# 当前激活的模型名（对应下方 models 列表中的 name）\n")
	if cfg.ActiveModel != "" {
		b.WriteString(fmt.Sprintf("active_model: %s\n", cfg.ActiveModel))
	} else {
		b.WriteString("active_model: \"\"\n")
	}
	b.WriteString("\n")

	// Models directory — additional model yaml files dropped here are loaded
	// into /model picker alongside the inline `models:` below. Set during
	// setup so users can extend the model list without editing config.yaml.
	if cfg.ModelsDir != "" {
		b.WriteString("# Directory containing additional model YAML files\n")
		b.WriteString("# 存放额外模型 YAML 文件的目录（与下方 inline models 合并）\n")
		b.WriteString(fmt.Sprintf("models_dir: %s\n\n", cfg.ModelsDir))
	}

	// Connections
	b.WriteString("# Database connections / 数据库连接配置\n")
	b.WriteString("# Multiple connections supported, switch with /login\n")
	b.WriteString("# 支持配置多个连接，通过 /login 切换\n")
	b.WriteString("#\n")
	b.WriteString("# auth_mode options / 认证方式:\n")
	b.WriteString("#   prompt  — Ask password each time / 每次输入密码（推荐）\n")
	b.WriteString("#   save    — AES-256-GCM encrypted storage / 加密存储（需通过 " + brand.Current().BinaryName + " setup/configure 配置）\n")
	b.WriteString("#   wallet  — Oracle Wallet / Oracle 钱包\n")
	b.WriteString("#   os      — OS authentication / 操作系统认证\n")
	b.WriteString("#   ldap    — LDAP/AD authentication / LDAP 认证\n")
	b.WriteString("#   kerberos — Kerberos authentication / Kerberos 认证\n")
	b.WriteString("#   token   — OAuth2 token / OAuth2 令牌\n")
	b.WriteString("#\n")
	b.WriteString("# credential.value — Plaintext password (dev/test ONLY, security risk!)\n")
	b.WriteString("# credential.value — 明文密码（仅限开发测试，生产环境有安全风险！）\n")
	b.WriteString("connections:\n")
	for _, conn := range cfg.Connections {
		b.WriteString(fmt.Sprintf("  - name: %s\n", conn.Name))
		if conn.DBType != "" {
			b.WriteString(fmt.Sprintf("    db_type: %s\n", conn.DBType))
		}
		b.WriteString(fmt.Sprintf("    host: %s\n", conn.Host))
		b.WriteString(fmt.Sprintf("    port: %d\n", conn.Port))
		if conn.Service != "" {
			b.WriteString(fmt.Sprintf("    service: %s\n", conn.Service))
		}
		if conn.Database != "" {
			b.WriteString(fmt.Sprintf("    database: %s\n", conn.Database))
		}
		b.WriteString(fmt.Sprintf("    user: %s\n", conn.User))
		if conn.AuthMode != "" {
			b.WriteString(fmt.Sprintf("    auth_mode: %s\n", conn.AuthMode))
		}
		if conn.Credential.Provider != "" {
			b.WriteString("    credential:\n")
			b.WriteString(fmt.Sprintf("      provider: %s\n", conn.Credential.Provider))
		}
	}
	b.WriteString("\n")

	// Models
	b.WriteString("# Model configurations / 大模型配置\n")
	b.WriteString("# Multiple models supported, switch with /model\n")
	b.WriteString("# 支持配置多个模型，通过 /model 切换\n")
	b.WriteString("#\n")
	b.WriteString("# Supports any OpenAI-compatible API (Kimi, DeepSeek, MiniMax, Ollama, etc.)\n")
	b.WriteString("# 支持所有 OpenAI 兼容 API（Kimi、DeepSeek、MiniMax、Ollama 等）\n")
	if len(cfg.Models) > 0 {
		b.WriteString("models:\n")
		for _, m := range cfg.Models {
			b.WriteString(fmt.Sprintf("  - name: %s\n", m.Name))
			b.WriteString(fmt.Sprintf("    provider: %s\n", m.Provider))
			if m.Vendor != "" {
				b.WriteString(fmt.Sprintf("    vendor: %s\n", m.Vendor))
			}
			b.WriteString(fmt.Sprintf("    base_url: %s\n", m.BaseURL))
			b.WriteString(fmt.Sprintf("    model: %s\n", m.Model))
			if m.Capability != "" {
				b.WriteString("    # Capability: small (≤9B, 3轮辅助) | large (≥27B, 10轮自主)\n")
				b.WriteString(fmt.Sprintf("    capability: %s\n", m.Capability))
			}
			if m.APIKey != "" {
				b.WriteString(fmt.Sprintf("    api_key: %s\n", m.APIKey))
			}
			if m.Description != "" {
				b.WriteString(fmt.Sprintf("    description: %s\n", m.Description))
			}
			if m.ToolMode != "" {
				b.WriteString(fmt.Sprintf("    tool_mode: %s\n", m.ToolMode))
			}
		}
	} else {
		b.WriteString("models: []\n")
	}

	return b.String()
}
