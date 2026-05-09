/*-------------------------------------------------------------------------
 *
 * rule.go
 *	  RuleStep introduces Rule Engine and configures on/off
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/rule.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// RuleStep introduces Rule Engine and configures on/off
type RuleStep struct {
	cfg *SetupConfig
	sel *SelectModel
}

func NewRuleStep(cfg *SetupConfig) *RuleStep {
	return &RuleStep{
		cfg: cfg,
		sel: NewSelectModel("Enable Rule Engine?", []SelectItem{
			{Label: "Yes", Value: "yes", Desc: "recommended — Model 不可用时自动兜底"},
			{Label: "No", Value: "no"},
		}, "yes"),
	}
}

func (s *RuleStep) Title() string { return "Rule Engine" }
func (s *RuleStep) Done() bool    { return s.sel.Done() }

func (s *RuleStep) Summary() string {
	val := "Disabled"
	if s.cfg.RuleEngine {
		val = "Enabled"
	}
	return CompletedLine("Rule Engine", val)
}

func (s *RuleStep) Init() tea.Cmd { return nil }

func (s *RuleStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.sel.Update(msg)
	s.sel = updated
	if s.sel.Done() {
		s.cfg.RuleEngine = s.sel.Selected() == "yes"
	}
	return s, cmd
}

func (s *RuleStep) View() string {
	intro := strings.Join([]string{
		"Rule Engine 是 OpenDB 的规则决策引擎。",
		"当 Model 无法工作时（网络不通、模型不可用），",
		"由 Rule Engine 承担诊断决策。",
		"",
		"内置数十条经过验证的数据库诊断规则，",
		"覆盖常见性能问题，无需外部依赖即可工作。",
		"",
		StyleDim.Render("诊断三层架构: 探针 → Rule Engine → Model 推理"),
	}, "\n")

	return "\n" + InfoPanel("Rule Engine — 兜底决策引擎", intro) + "\n" + s.sel.View()
}
