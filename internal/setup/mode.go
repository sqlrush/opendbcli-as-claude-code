/*-------------------------------------------------------------------------
 *
 * mode.go
 *	  mode — ModeStep plus helpers (NewModeStep) used by the setup
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/mode.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import tea "github.com/charmbracelet/bubbletea"

type ModeStep struct {
	cfg *SetupConfig
	sel *SelectModel
}

func NewModeStep(cfg *SetupConfig) *ModeStep {
	items := []SelectItem{
		{
			Label: "QuickStart",
			Value: "quickstart",
			Desc:  "只配数据库连接和 LLM，其余用默认值（推荐）",
		},
		{
			Label: "Custom",
			Value: "custom",
			Desc:  "完整配置（数据库 + Sentinel + Model + Rule + 安全）",
		},
	}
	return &ModeStep{
		cfg: cfg,
		sel: NewSelectModel("Setup mode", items, "quickstart"),
	}
}

func (s *ModeStep) Title() string { return "Mode" }
func (s *ModeStep) Done() bool    { return s.sel.Done() }

func (s *ModeStep) Summary() string {
	return CompletedLine("Setup mode", s.sel.SelectedLabel())
}

func (s *ModeStep) Init() tea.Cmd { return s.sel.Init() }

func (s *ModeStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.sel.Update(msg)
	s.sel = updated
	if s.sel.Done() {
		s.cfg.Mode = s.sel.Selected()
	}
	return s, cmd
}

func (s *ModeStep) View() string {
	panel := InfoPanel("Setup Mode",
		"QuickStart 模式只配置数据库连接和 LLM，\n"+
			"其余使用安全的默认值，快速让你用起来。\n\n"+
			"Custom 模式可以配置所有选项：\n"+
			"Sentinel 监控、Rule Engine 等。")
	return "\n" + panel + "\n" + s.sel.View()
}
