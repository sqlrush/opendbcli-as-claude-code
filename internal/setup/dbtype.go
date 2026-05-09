/*-------------------------------------------------------------------------
 *
 * dbtype.go
 *	  DBTypeStep lets user select the target database type
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/dbtype.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

// minDBTypeViewDuration is the minimum amount of time the DB-type panel
// must be visible before Enter is allowed to submit. Prevents users from
// blowing past the screen and silently picking the default (oracle) when
// they actually wanted MySQL/PG/OG/GaussDB. Bypassed as soon as the user
// interacts with the selector (up/down keys), so people who are sure of
// the default don't have to wait.
const minDBTypeViewDuration = time.Second

// DBTypeStep lets user select the target database type
type DBTypeStep struct {
	cfg          *SetupConfig
	sel          *SelectModel
	startTime    time.Time
	interacted   bool // user pressed up/down at least once
}

func NewDBTypeStep(cfg *SetupConfig) *DBTypeStep {
	// openGauss and GaussDB are listed as separate types because they use
	// different drivers — pgx for openGauss (open source), gaussdb-go for
	// GaussDB (Huawei commercial, SCRAM-SHA256(10) auth). Selecting wrong
	// one will surface as an auth-handshake failure at conntest time.
	items := []SelectItem{
		{Label: "Oracle", Value: "oracle"},
		{Label: "MySQL", Value: "mysql"},
		{Label: "PostgreSQL", Value: "postgres"},
		{Label: "openGauss", Value: "opengauss", Desc: "开源社区版"},
		{Label: "GaussDB", Value: "gaussdb", Desc: "华为商业版（集中式 V2.0-8.x）"},
	}
	return &DBTypeStep{
		cfg: cfg,
		sel: NewSelectModel("Select your database type", items, "oracle"),
	}
}

func (s *DBTypeStep) Title() string { return "Database Type" }
func (s *DBTypeStep) Done() bool    { return s.sel.Done() }

func (s *DBTypeStep) Summary() string {
	return CompletedLine("Database type", s.sel.SelectedLabel())
}

func (s *DBTypeStep) Init() tea.Cmd {
	s.startTime = time.Now()
	return s.sel.Init()
}

func (s *DBTypeStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Anti-fly-by-default guard: silently swallow Enter for the first
	// second unless the user has already moved the cursor (which signals
	// they've actually engaged with the selector).
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.Type {
		case tea.KeyUp, tea.KeyDown:
			s.interacted = true
		case tea.KeyEnter:
			if !s.interacted && time.Since(s.startTime) < minDBTypeViewDuration {
				return s, nil
			}
		}
	}

	updated, cmd := s.sel.Update(msg)
	s.sel = updated
	if s.sel.Done() {
		s.cfg.DBType = s.sel.Selected()
	}
	return s, cmd
}

func (s *DBTypeStep) View() string {
	panel := InfoPanel("Multi-Database",
		brand.Current().AppName+" 用同一套交互体验覆盖主流数据库。\n"+
			"无论你用哪种数据库，诊断、监控、技能命令\n"+
			"都保持一致的操作方式。\n\n"+
			"提示：用 ↑↓ 切换选项，确认后按 Enter。\n"+
			"刚进入此页时 Enter 会暂停 1 秒，避免误过。")
	return "\n" + panel + "\n" + s.sel.View()
}
