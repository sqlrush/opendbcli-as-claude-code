/*-------------------------------------------------------------------------
 *
 * setup.go
 *	  SetupConfig accumulates user choices across all steps
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/setup.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
)

// SetupConfig accumulates user choices across all steps
type SetupConfig struct {
	BaseDir      string // ~/.opendb
	Mode         string // "quickstart" or "custom"
	DBType       string // "oracle", "mysql", "postgres"
	Connection   config.Connection
	Password     string // collected during form, used for test
	Sentinel     config.SentinelConfig
	LLM          config.LLMConfig
	LLMProvider  string // provider display name (e.g. "Moonshot (Kimi)")
	LLMVendor    string // vendor name for model config
	LLMApiKey    string // api key (cloud only)
	RuleEngine   bool
	Security     config.SecurityConfig
	ConnTestOK   bool   // connection test passed
	ConnVersion  string // database version from test
	LLMTestOK    bool   // LLM test passed
}

// Step is the interface each wizard step implements
type Step interface {
	tea.Model
	Title() string
	Done() bool
	Summary() string // one-line folded summary for completed state (clack ◇ style)
}

// DriverFactory creates a database driver from setup config (injected for testability)
type DriverFactory func(cfg *SetupConfig) (db.Driver, error)

// stepBuilder is a factory function that creates a fresh step
type stepBuilder func() Step

// Model is the main setup orchestrator
type Model struct {
	steps    []Step
	builders []stepBuilder
	current  int
	cfg      *SetupConfig
	width    int
	height   int
	quitting bool
}

func (m *Model) recreateStep(idx int) Step {
	if idx >= 0 && idx < len(m.builders) {
		return m.builders[idx]()
	}
	return m.steps[idx]
}

func (m *Model) Init() tea.Cmd {
	if len(m.steps) == 0 {
		return tea.Quit
	}
	return m.steps[m.current].Init()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	case stepBackMsg:
		target := m.current - msg.n
		if target < 0 {
			target = 0
		}
		m.current = target
		m.steps[m.current] = m.recreateStep(m.current)
		return m, m.steps[m.current].Init()
	case stepDoneMsg:
		// Push completed step's full view into terminal scrollback via tea.Println
		// (bubbletea never touches scrollback content — it persists like chat history)
		view := m.steps[m.current].View()
		summary := m.steps[m.current].Summary()
		m.current++
		for m.current < len(m.steps) && m.shouldSkip(m.steps[m.current]) {
			m.current++
		}
		// Print each line separately — tea.Println uses fmt.Sprint internally
		// which joins args with spaces, not newlines
		strLines := append(splitLines(view), summary, "")
		var cmds []tea.Cmd
		for _, line := range strLines {
			l := line
			cmds = append(cmds, tea.Println(l))
		}
		if m.current >= len(m.steps) {
			cmds = append(cmds, tea.Quit)
		} else {
			cmds = append(cmds, m.steps[m.current].Init())
		}
		return m, tea.Sequence(cmds...)
	}

	if m.current < len(m.steps) {
		updated, cmd := m.steps[m.current].Update(msg)
		m.steps[m.current] = updated.(Step)
		return m, cmd
	}
	return m, nil
}

func (m *Model) shouldSkip(step Step) bool {
	if m.cfg.Mode != "quickstart" {
		return false
	}
	title := step.Title()
	return title == "Sentinel" || title == "Rule Engine"
}

func (m *Model) View() string {
	if m.quitting {
		return "\n  Setup cancelled.\n\n"
	}
	if m.current >= len(m.steps) {
		return ""
	}
	return m.steps[m.current].View()
}

func splitLines(s string) []string {
	result := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// RunSetup is the entry point called from main.go for --setup
func RunSetup(baseDir string, driverFunc DriverFactory) error {
	defaults := config.Default()
	cfg := &SetupConfig{
		BaseDir:    baseDir,
		Sentinel:   defaults.Sentinel,
		Security:   defaults.Security,
		LLM:        defaults.LLM,
		RuleEngine: true,
	}

	builders := []stepBuilder{
		func() Step { return NewWelcomeStep() },
		// Existing-config check: silently passes through on fresh installs;
		// on re-runs it asks before clobbering and auto-backs up old config
		// to config.yaml.bak.<timestamp>. Without this, `dbaa setup` after
		// a year of use silently wiped accumulated connections + models.
		func() Step { return NewExistingConfigStep(cfg) },
		func() Step { return NewModeStep(cfg) },
		func() Step { return NewDBTypeStep(cfg) },
		func() Step { return NewPermissionStep(cfg) },
		func() Step { return NewConnFormStep(cfg) },
		func() Step { return NewConnTestStep(cfg, driverFunc) },
		func() Step { return NewSentinelStep(cfg) },
		func() Step { return NewLLMProviderStep(cfg) },
		func() Step { return NewLLMConfigureStep(cfg) },
		func() Step { return NewLLMTestStep(cfg) },
		func() Step { return NewRuleStep(cfg) },
		func() Step { return NewFinalizeStep(cfg) },
	}
	steps := make([]Step, len(builders))
	for i, b := range builders {
		steps[i] = b()
	}

	m := &Model{cfg: cfg, steps: steps, builders: builders}
	// No alt screen — completed steps stay visible above (clack style)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("setup wizard error: %w", err)
	}

	fm := finalModel.(*Model)
	if fm.quitting {
		return nil
	}
	return nil
}

// RunConfigure is the entry point for opendb configure
func RunConfigure(baseDir string, driverFunc DriverFactory) error {
	defaults := config.Default()
	cfg := &SetupConfig{
		BaseDir:    baseDir,
		Sentinel:   defaults.Sentinel,
		Security:   defaults.Security,
		LLM:        defaults.LLM,
		RuleEngine: true,
	}

	m := NewConfigureModel(cfg, driverFunc)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

// DefaultBaseDir returns ~/.opendb
func DefaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".opendb")
	}
	return filepath.Join(home, ".opendb")
}
