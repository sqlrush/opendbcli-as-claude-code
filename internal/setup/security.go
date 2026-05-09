/*-------------------------------------------------------------------------
 *
 * security.go
 *	  SecurityStep configures dangerous operation confirmation
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/security.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

// SecurityStep configures dangerous operation confirmation
type SecurityStep struct {
	cfg *SetupConfig
	sel *SelectModel
}

func NewSecurityStep(cfg *SetupConfig) *SecurityStep {
	return &SecurityStep{
		cfg: cfg,
		sel: NewSelectModel("Confirm before dangerous operations?", []SelectItem{
			{Label: "Yes", Value: "yes", Desc: "recommended — DROP/DELETE/TRUNCATE 需要二次确认"},
			{Label: "No", Value: "no"},
		}, "yes"),
	}
}

func (s *SecurityStep) Title() string { return "Security" }
func (s *SecurityStep) Done() bool    { return s.sel.Done() }

func (s *SecurityStep) Summary() string {
	val := "Off"
	if s.cfg.Security.ConfirmOnDangerous {
		val = "Confirm on dangerous"
	}
	return CompletedLine("Security", val)
}

func (s *SecurityStep) Init() tea.Cmd { return nil }

func (s *SecurityStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.sel.Update(msg)
	s.sel = updated
	if s.sel.Done() {
		s.cfg.Security.ConfirmOnDangerous = s.sel.Selected() == "yes"
	}
	return s, cmd
}

func (s *SecurityStep) View() string {
	intro := strings.Join([]string{
		brand.Current().AppName + " 提供操作安全保护:",
		StyleSuccess.Render("·") + " DROP / DELETE / TRUNCATE 等危险操作",
		"  执行前要求二次确认，防止误操作。",
	}, "\n")

	return "\n" + InfoPanel("Security", intro) + "\n" + s.sel.View()
}
