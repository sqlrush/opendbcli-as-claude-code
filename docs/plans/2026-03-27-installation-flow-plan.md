# OpenDB Installation Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a one-command installation flow (`curl | bash` → interactive setup wizard) following the OpenClaw pattern, with `opendb --setup` and `opendb configure` commands.

**Architecture:** bash install script downloads binary and triggers `opendb --setup`. The setup wizard is a bubbletea TUI app in `internal/setup/` package, using lipgloss for styling. Each wizard step is a standalone `tea.Model` composed by a central orchestrator. `opendb configure` reuses the same step components in menu-driven mode.

**Tech Stack:** Go, bubbletea, bubbles, lipgloss, bash

**Spec:** `docs/plans/2026-03-27-installation-flow-design.md`

---

## File Structure

```
install/
├── install.sh                    # Bash install script

internal/setup/
├── setup.go                      # Main orchestrator model + entry point
├── styles.go                     # Shared colors, styles, panel rendering
├── components.go                 # Reusable TUI components (select, input, panel)
├── welcome.go                    # Step 1: Welcome + brand
├── mode.go                       # Step 2: QuickStart/Custom
├── dbtype.go                     # Step 3a: Database type selection
├── permission.go                 # Step 3b: Permission guide (per DB type)
├── connform.go                   # Step 3c: Connection form
├── conntest.go                   # Step 3d: Connection + permission test
├── sentinel.go                   # Step 4: Sentinel intro + config
├── llmconfig.go                  # Step 5a: LLM intro + config
├── llmtest.go                    # Step 5b: LLM connectivity test
├── rule.go                       # Step 6: Rule Engine intro + config
├── skills.go                     # Step 7: Skills showcase
├── security.go                   # Step 8: Security config
├── finalize.go                   # Step 9: Config generation + test run
├── configure.go                  # opendb configure menu-driven mode
├── setup_test.go                 # Orchestrator tests
├── components_test.go            # Component tests
├── conntest_test.go              # Connection test logic tests
├── permission_test.go            # Permission data tests
├── finalize_test.go              # Config generation tests

cmd/opendb/main.go                # Modify: add --setup and configure flags
internal/skill/builtin/shared/
└── configure.go                  # New: /configure skill
```

---

### Task 1: Foundation — Package Structure, Types, Styles

**Files:**
- Create: `internal/setup/styles.go`
- Create: `internal/setup/setup.go`

- [ ] **Step 1: Create styles.go with color palette and panel helpers**

```go
// internal/setup/styles.go
package setup

import "github.com/charmbracelet/lipgloss"

// Color palette — reference OpenClaw's multi-color scheme
var (
	ColorPrimary   = lipgloss.Color("#7C3AED") // purple — brand accent
	ColorSecondary = lipgloss.Color("#06B6D4") // cyan — info panels
	ColorSuccess   = lipgloss.Color("#22C55E") // green — checkmarks
	ColorWarning   = lipgloss.Color("#EAB308") // yellow — warnings
	ColorError     = lipgloss.Color("#EF4444") // red — errors
	ColorDim       = lipgloss.Color("#6B7280") // gray — secondary text
	ColorText      = lipgloss.Color("#F9FAFB") // white — primary text
	ColorHighlight = lipgloss.Color("#F59E0B") // amber — highlights
)

// Styles
var (
	StyleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorText)

	StyleSubtitle = lipgloss.NewStyle().
			Foreground(ColorDim)

	StyleSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	StyleWarning = lipgloss.NewStyle().
			Foreground(ColorWarning)

	StyleError = lipgloss.NewStyle().
			Foreground(ColorError)

	StyleDim = lipgloss.NewStyle().
			Foreground(ColorDim)

	StyleHighlight = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorHighlight)

	StyleBrand = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)
)

// InfoPanel renders a bordered panel with title — similar to OpenClaw's ◇ panels
func InfoPanel(title, content string) string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(1, 2).
		Width(60)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorSecondary)

	return titleStyle.Render("◇  "+title+" ") + "\n" + border.Render(content)
}

// ProgressLine renders a step with status icon
func ProgressLine(icon, text string) string {
	return "  " + icon + " " + text
}

// SuccessLine renders ✓ text in green
func SuccessLine(text string) string {
	return ProgressLine(StyleSuccess.Render("✓"), text)
}

// WarningLine renders ⚠ text in yellow
func WarningLine(text string) string {
	return ProgressLine(StyleWarning.Render("⚠"), text)
}

// ErrorLine renders ✗ text in red
func ErrorLine(text string) string {
	return ProgressLine(StyleError.Render("✗"), text)
}

// BulletLine renders · text
func BulletLine(text string) string {
	return ProgressLine(StyleDim.Render("·"), text)
}
```

- [ ] **Step 2: Create setup.go with orchestrator skeleton and entry point**

```go
// internal/setup/setup.go
package setup

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/config"
)

// SetupConfig accumulates user choices across all steps
type SetupConfig struct {
	BaseDir    string // ~/.opendb
	Mode       string // "quickstart" or "custom"
	DBType     string // "oracle", "mysql", "postgres"
	Connection config.Connection
	Password   string // collected during form, used for test
	Sentinel   config.SentinelConfig
	LLM        config.LLMConfig
	RuleEngine bool
	Security   config.SecurityConfig
}

// Step is the interface each wizard step implements
type Step interface {
	tea.Model
	Title() string
	Done() bool
}

// stepDoneMsg signals the orchestrator that current step is complete
type stepDoneMsg struct{}

// Model is the main setup orchestrator
type Model struct {
	steps    []Step
	current  int
	cfg      *SetupConfig
	width    int
	height   int
	quitting bool
	err      error
}

func newModel(baseDir string, mode string) *Model {
	cfg := &SetupConfig{
		BaseDir:  baseDir,
		Sentinel: config.Default().Sentinel,
		Security: config.Default().Security,
		LLM:      config.Default().LLM,
	}
	return &Model{cfg: cfg}
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
	case stepDoneMsg:
		m.current++
		if m.current >= len(m.steps) {
			return m, tea.Quit
		}
		return m, m.steps[m.current].Init()
	}

	if m.current < len(m.steps) {
		updated, cmd := m.steps[m.current].Update(msg)
		m.steps[m.current] = updated.(Step)
		return m, cmd
	}
	return m, nil
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

// RunSetup is the entry point called from main.go
func RunSetup(baseDir string) error {
	m := newModel(baseDir, "")
	m.steps = buildSetupSteps(m.cfg)

	p := tea.NewProgram(m, tea.WithAltScreen())
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
func RunConfigure(baseDir string) error {
	m := newModel(baseDir, "configure")
	m.steps = buildConfigureSteps(m.cfg)

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// buildSetupSteps creates the 9-step setup flow
func buildSetupSteps(cfg *SetupConfig) []Step {
	// Will be populated as steps are implemented
	return []Step{}
}

// buildConfigureSteps creates the configure menu flow
func buildConfigureSteps(cfg *SetupConfig) []Step {
	return []Step{}
}

// defaultBaseDir returns ~/.opendb
func defaultBaseDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".opendb")
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 4: Commit**

```bash
git add internal/setup/styles.go internal/setup/setup.go
git commit -m "feat: setup wizard foundation — orchestrator and styles"
```

---

### Task 2: Reusable TUI Components

**Files:**
- Create: `internal/setup/components.go`
- Create: `internal/setup/components_test.go`

- [ ] **Step 1: Write tests for component logic**

```go
// internal/setup/components_test.go
package setup

import "testing"

func TestSelectComponent_Navigation(t *testing.T) {
	items := []SelectItem{
		{Label: "Oracle", Value: "oracle"},
		{Label: "MySQL", Value: "mysql"},
		{Label: "PostgreSQL", Value: "postgres"},
	}
	s := NewSelectModel("Choose DB", items, "")
	if s.Selected() != "oracle" {
		t.Errorf("expected initial selection 'oracle', got %q", s.Selected())
	}
	s.cursor = 2
	if s.Selected() != "postgres" {
		t.Errorf("expected 'postgres' at cursor 2, got %q", s.Selected())
	}
}

func TestInputComponent_Defaults(t *testing.T) {
	inp := NewInputModel("Host", "127.0.0.1", false)
	if inp.Value() != "" {
		t.Errorf("expected empty initial value, got %q", inp.Value())
	}
	if inp.ValueOrDefault() != "127.0.0.1" {
		t.Errorf("expected default '127.0.0.1', got %q", inp.ValueOrDefault())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -run TestSelect -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement components**

```go
// internal/setup/components.go
package setup

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ── SelectModel ─────────────────────────────────────────

// SelectItem represents one option in a selection list
type SelectItem struct {
	Label string
	Value string
	Desc  string // optional description shown beside label
}

// SelectModel is a single-select menu component
type SelectModel struct {
	title    string
	items    []SelectItem
	cursor   int
	done     bool
}

func NewSelectModel(title string, items []SelectItem, defaultValue string) *SelectModel {
	cursor := 0
	for i, item := range items {
		if item.Value == defaultValue {
			cursor = i
			break
		}
	}
	return &SelectModel{title: title, items: items, cursor: cursor}
}

func (s *SelectModel) Selected() string {
	if s.cursor >= 0 && s.cursor < len(s.items) {
		return s.items[s.cursor].Value
	}
	return ""
}

func (s *SelectModel) SelectedLabel() string {
	if s.cursor >= 0 && s.cursor < len(s.items) {
		return s.items[s.cursor].Label
	}
	return ""
}

func (s *SelectModel) Done() bool { return s.done }

func (s *SelectModel) Init() tea.Cmd { return nil }

func (s *SelectModel) Update(msg tea.Msg) (*SelectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(s.items)-1 {
				s.cursor++
			}
		case "enter":
			s.done = true
			return s, func() tea.Msg { return stepDoneMsg{} }
		}
	}
	return s, nil
}

func (s *SelectModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + StyleBrand.Render("◆") + "  " + s.title + "\n")
	for i, item := range s.items {
		if i == s.cursor {
			b.WriteString("  " + StyleBrand.Render("│  ● "+item.Label))
		} else {
			b.WriteString("  " + StyleDim.Render("│  ○ "+item.Label))
		}
		if item.Desc != "" {
			b.WriteString(StyleDim.Render(" — " + item.Desc))
		}
		b.WriteString("\n")
	}
	b.WriteString("  " + StyleDim.Render("└") + "\n")
	return b.String()
}

// ── InputModel ──────────────────────────────────────────

// InputModel is a text input component
type InputModel struct {
	label        string
	defaultValue string
	value        string
	cursorPos    int
	hidden       bool // password masking
	done         bool
	hint         string
}

func NewInputModel(label, defaultValue string, hidden bool) *InputModel {
	return &InputModel{
		label:        label,
		defaultValue: defaultValue,
		hidden:       hidden,
	}
}

func NewInputModelWithHint(label, defaultValue, hint string) *InputModel {
	return &InputModel{
		label:        label,
		defaultValue: defaultValue,
		hint:         hint,
	}
}

func (inp *InputModel) Value() string    { return inp.value }
func (inp *InputModel) Done() bool       { return inp.done }

func (inp *InputModel) ValueOrDefault() string {
	if inp.value == "" {
		return inp.defaultValue
	}
	return inp.value
}

func (inp *InputModel) Init() tea.Cmd { return nil }

func (inp *InputModel) Update(msg tea.Msg) (*InputModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			inp.done = true
			return inp, func() tea.Msg { return stepDoneMsg{} }
		case "backspace":
			if inp.cursorPos > 0 && len(inp.value) > 0 {
				inp.value = inp.value[:inp.cursorPos-1] + inp.value[inp.cursorPos:]
				inp.cursorPos--
			}
		case "left":
			if inp.cursorPos > 0 {
				inp.cursorPos--
			}
		case "right":
			if inp.cursorPos < len(inp.value) {
				inp.cursorPos++
			}
		default:
			if len(msg.String()) == 1 {
				inp.value = inp.value[:inp.cursorPos] + msg.String() + inp.value[inp.cursorPos:]
				inp.cursorPos++
			}
		}
	}
	return inp, nil
}

func (inp *InputModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	label := inp.label
	if inp.defaultValue != "" {
		label += StyleDim.Render(fmt.Sprintf(" (%s)", inp.defaultValue))
	}
	b.WriteString("  " + StyleBrand.Render("◆") + "  " + label + "\n")
	displayValue := inp.value
	if inp.hidden && displayValue != "" {
		displayValue = strings.Repeat("•", len(displayValue))
	}
	b.WriteString("  " + StyleDim.Render("│  ") + displayValue + StyleDim.Render("█") + "\n")
	if inp.hint != "" {
		b.WriteString("  " + StyleDim.Render("│  "+inp.hint) + "\n")
	}
	b.WriteString("  " + StyleDim.Render("└") + "\n")
	return b.String()
}

// ── ConfirmModel ────────────────────────────────────────

// ConfirmModel is a yes/no confirmation component
type ConfirmModel struct {
	title  string
	cursor int // 0=yes, 1=no
	done   bool
}

func NewConfirmModel(title string) *ConfirmModel {
	return &ConfirmModel{title: title}
}

func (c *ConfirmModel) Confirmed() bool { return c.cursor == 0 }
func (c *ConfirmModel) Done() bool      { return c.done }
func (c *ConfirmModel) Init() tea.Cmd   { return nil }

func (c *ConfirmModel) Update(msg tea.Msg) (*ConfirmModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "left", "h":
			c.cursor = 0
		case "right", "l":
			c.cursor = 1
		case "enter":
			c.done = true
			return c, func() tea.Msg { return stepDoneMsg{} }
		}
	}
	return c, nil
}

func (c *ConfirmModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + StyleBrand.Render("◆") + "  " + c.title + "\n")
	yes := "Yes"
	no := "No"
	if c.cursor == 0 {
		yes = StyleBrand.Render("● Yes")
		no = StyleDim.Render("○ No")
	} else {
		yes = StyleDim.Render("○ Yes")
		no = StyleBrand.Render("● No")
	}
	b.WriteString("  " + StyleDim.Render("│  ") + yes + "  " + no + "\n")
	b.WriteString("  " + StyleDim.Render("└") + "\n")
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/setup/components.go internal/setup/components_test.go
git commit -m "feat: setup wizard reusable TUI components — select, input, confirm"
```

---

### Task 3: Welcome Page (Step 1)

**Files:**
- Create: `internal/setup/welcome.go`

- [ ] **Step 1: Implement welcome page**

```go
// internal/setup/welcome.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sqlrush/opendb/internal/version"
)

const asciiLogo = `
 ██████╗ ██████╗ ███████╗███╗   ██╗██████╗ ██████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔══██╗██╔══██╗
██║   ██║██████╔╝█████╗  ██╔██╗ ██║██║  ██║██████╔╝
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║██║  ██║██╔══██╗
╚██████╔╝██║     ███████╗██║ ╚████║██████╔╝██████╔╝
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚═════╝ ╚═════╝`

type WelcomeStep struct {
	confirm *ConfirmModel
}

func NewWelcomeStep() *WelcomeStep {
	return &WelcomeStep{
		confirm: NewConfirmModel("Ready to set up OpenDB?"),
	}
}

func (w *WelcomeStep) Title() string { return "Welcome" }
func (w *WelcomeStep) Done() bool    { return w.confirm.Done() }

func (w *WelcomeStep) Init() tea.Cmd { return nil }

func (w *WelcomeStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := w.confirm.Update(msg)
	w.confirm = updated
	if w.confirm.Done() && !w.confirm.Confirmed() {
		// User chose "No" — quit
		return w, tea.Quit
	}
	return w, cmd
}

func (w *WelcomeStep) View() string {
	var b strings.Builder

	// ASCII art logo in brand color
	logoStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	b.WriteString(logoStyle.Render(asciiLogo))
	b.WriteString("\n")

	// Subtitle + slogan
	center := lipgloss.NewStyle().Width(56).Align(lipgloss.Center)
	b.WriteString(center.Render(StyleSubtitle.Render("DB CLI Agent as Claude Code")))
	b.WriteString("\n")
	b.WriteString(center.Render(StyleHighlight.Render("最少交互，最优诊断 | Less input. More insight.")))
	b.WriteString("\n\n")

	// Product info
	info := []string{
		BulletLine("Version:   " + version.Short()),
		BulletLine("Developer: SQLRush"),
		BulletLine("License:   Apache 2.0"),
		BulletLine("Website:   https://opendb.ai"),
		BulletLine("GitHub:    https://github.com/sqlrush/opendb"),
		BulletLine("Contact:   sqlrush@gmail.com"),
	}
	b.WriteString(strings.Join(info, "\n"))
	b.WriteString("\n\n")

	// Core capabilities panel
	capabilities := strings.Join([]string{
		StyleSuccess.Render("·") + " 多数据库支持 — Oracle / MySQL / PostgreSQL",
		StyleSuccess.Render("·") + " LLM 智能诊断 — 自然语言描述问题，LLM 给出方案",
		StyleSuccess.Render("·") + " Sentinel 实时监控 — 自动检测异常，秒级响应",
		StyleSuccess.Render("·") + " Rule Engine — LLM 不可用时的兜底决策引擎",
		StyleSuccess.Render("·") + " 技能系统 — /开头的命令，一键完成复杂操作",
		StyleSuccess.Render("·") + " 单二进制零依赖 — 开箱即用",
	}, "\n")
	b.WriteString(InfoPanel("Welcome", capabilities))
	b.WriteString("\n")

	// Confirmation
	b.WriteString(w.confirm.View())
	return b.String()
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 3: Commit**

```bash
git add internal/setup/welcome.go
git commit -m "feat: setup wizard welcome page with brand and capabilities"
```

---

### Task 4: Mode Selection (Step 2)

**Files:**
- Create: `internal/setup/mode.go`

- [ ] **Step 1: Implement mode selection step**

```go
// internal/setup/mode.go
package setup

import tea "github.com/charmbracelet/bubbletea"

type ModeStep struct {
	cfg    *SetupConfig
	sel    *SelectModel
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
			Desc:  "完整配置（数据库 + Sentinel + LLM + Rule + 安全）",
		},
	}
	return &ModeStep{
		cfg: cfg,
		sel: NewSelectModel("Setup mode", items, "quickstart"),
	}
}

func (s *ModeStep) Title() string { return "Mode" }
func (s *ModeStep) Done() bool    { return s.sel.Done() }

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
	return "\n" + InfoPanel("Setup Mode", "QuickStart 模式只配置数据库连接和 LLM，\n其余使用安全的默认值，快速让你用起来。\n\nCustom 模式可以配置所有选项：\nSentinel 监控、Rule Engine、安全级别等。") + "\n" + s.sel.View()
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 3: Commit**

```bash
git add internal/setup/mode.go
git commit -m "feat: setup wizard mode selection — QuickStart vs Custom"
```

---

### Task 5: Database Type Selection + Permission Guide (Steps 3a-3b)

**Files:**
- Create: `internal/setup/dbtype.go`
- Create: `internal/setup/permission.go`
- Create: `internal/setup/permission_test.go`

- [ ] **Step 1: Write permission data tests**

```go
// internal/setup/permission_test.go
package setup

import "testing"

func TestPermissionGuide_Oracle(t *testing.T) {
	guide := PermissionGuideFor("oracle")
	if guide.DBType != "oracle" {
		t.Errorf("expected oracle, got %s", guide.DBType)
	}
	if len(guide.Required) == 0 {
		t.Error("expected non-empty required permissions for oracle")
	}
	if len(guide.NotRecommended) == 0 {
		t.Error("expected non-empty not-recommended permissions for oracle")
	}
	if guide.CreateSQL == "" {
		t.Error("expected non-empty create SQL for oracle")
	}
}

func TestPermissionGuide_MySQL(t *testing.T) {
	guide := PermissionGuideFor("mysql")
	if guide.DBType != "mysql" {
		t.Errorf("expected mysql, got %s", guide.DBType)
	}
	if len(guide.Required) == 0 {
		t.Error("expected non-empty required permissions for mysql")
	}
}

func TestPermissionGuide_Postgres(t *testing.T) {
	guide := PermissionGuideFor("postgres")
	if guide.DBType != "postgres" {
		t.Errorf("expected postgres, got %s", guide.DBType)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -run TestPermission -v`
Expected: FAIL

- [ ] **Step 3: Implement permission.go**

```go
// internal/setup/permission.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PermissionGuide holds per-DB permission requirements
type PermissionGuide struct {
	DBType         string
	Required       []PermissionItem
	NotRecommended []PermissionItem
	CreateSQL      string
}

type PermissionItem struct {
	Name string
	Desc string
}

func PermissionGuideFor(dbType string) PermissionGuide {
	switch dbType {
	case "oracle":
		return PermissionGuide{
			DBType: "oracle",
			Required: []PermissionItem{
				{"CREATE SESSION", "连接数据库"},
				{"SELECT on V$ views", "性能视图查询"},
				{"SELECT on DBA_ views", "数据字典查询"},
				{"SELECT_CATALOG_ROLE", "系统目录读取"},
			},
			NotRecommended: []PermissionItem{
				{"SYSDBA / SYSOPER", "权限过大"},
				{"DBA role", "包含修改权限"},
			},
			CreateSQL: "CREATE USER opendb IDENTIFIED BY <password>;\nGRANT CREATE SESSION TO opendb;\nGRANT SELECT_CATALOG_ROLE TO opendb;",
		}
	case "mysql":
		return PermissionGuide{
			DBType: "mysql",
			Required: []PermissionItem{
				{"SELECT", "查询数据"},
				{"PROCESS", "查看进程列表"},
				{"REPLICATION CLIENT", "查看复制状态"},
				{"SHOW DATABASES", "列出所有数据库"},
			},
			NotRecommended: []PermissionItem{
				{"ALL PRIVILEGES", "权限过大"},
				{"SUPER", "包含管理权限"},
			},
			CreateSQL: "CREATE USER 'opendb'@'%' IDENTIFIED BY '<password>';\nGRANT SELECT, PROCESS, REPLICATION CLIENT, SHOW DATABASES ON *.* TO 'opendb'@'%';",
		}
	case "postgres":
		return PermissionGuide{
			DBType: "postgres",
			Required: []PermissionItem{
				{"CONNECT", "连接数据库"},
				{"pg_monitor role", "性能监控视图"},
				{"SELECT on pg_stat*", "统计信息查询"},
			},
			NotRecommended: []PermissionItem{
				{"SUPERUSER", "权限过大"},
				{"pg_write_all_data", "包含写权限"},
			},
			CreateSQL: "CREATE USER opendb WITH PASSWORD '<password>';\nGRANT CONNECT ON DATABASE mydb TO opendb;\nGRANT pg_monitor TO opendb;",
		}
	default:
		return PermissionGuide{DBType: dbType}
	}
}

// PermissionStep shows permission guide for chosen DB type
type PermissionStep struct {
	cfg     *SetupConfig
	guide   PermissionGuide
	confirm *SelectModel
}

func NewPermissionStep(cfg *SetupConfig) *PermissionStep {
	guide := PermissionGuideFor(cfg.DBType)
	items := []SelectItem{
		{Label: "Yes", Value: "yes", Desc: "已准备好数据库账号"},
		{Label: "I need more time", Value: "exit", Desc: "退出，稍后运行 opendb --setup 继续"},
	}
	return &PermissionStep{
		cfg:     cfg,
		guide:   guide,
		confirm: NewSelectModel("I've prepared the database account. Continue?", items, "yes"),
	}
}

func (s *PermissionStep) Title() string { return "Permission Guide" }
func (s *PermissionStep) Done() bool    { return s.confirm.Done() }

func (s *PermissionStep) Init() tea.Cmd { return nil }

func (s *PermissionStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.confirm.Update(msg)
	s.confirm = updated
	if s.confirm.Done() && s.confirm.Selected() == "exit" {
		return s, tea.Quit
	}
	return s, cmd
}

func (s *PermissionStep) View() string {
	var b strings.Builder

	// Required permissions
	var reqLines []string
	for _, p := range s.guide.Required {
		reqLines = append(reqLines, StyleSuccess.Render("✅ ")+p.Name+StyleDim.Render(" — "+p.Desc))
	}

	// Not recommended
	var notRecLines []string
	for _, p := range s.guide.NotRecommended {
		notRecLines = append(notRecLines, StyleWarning.Render("⚠️  ")+p.Name+StyleDim.Render(" — "+p.Desc))
	}

	content := strings.Join([]string{
		"OpenDB 需要一个专用数据库账号来工作。",
		"权限太少会导致功能受限，权限太多存在安全风险。",
		"",
		StyleTitle.Render("推荐权限（最小必要集）:"),
		strings.Join(reqLines, "\n"),
		"",
		StyleTitle.Render("不建议使用:"),
		strings.Join(notRecLines, "\n"),
		"",
		StyleTitle.Render("建议执行以下 SQL:"),
		StyleDim.Render(s.guide.CreateSQL),
	}, "\n")

	title := "Permission Guide (" + strings.Title(s.guide.DBType) + ")"
	b.WriteString("\n" + InfoPanel(title, content) + "\n")
	b.WriteString(s.confirm.View())
	return b.String()
}
```

- [ ] **Step 4: Implement dbtype.go**

```go
// internal/setup/dbtype.go
package setup

import (
	tea "github.com/charmbracelet/bubbletea"
)

type DBTypeStep struct {
	cfg *SetupConfig
	sel *SelectModel
}

func NewDBTypeStep(cfg *SetupConfig) *DBTypeStep {
	items := []SelectItem{
		{Label: "Oracle", Value: "oracle"},
		{Label: "MySQL", Value: "mysql"},
		{Label: "PostgreSQL", Value: "postgres"},
	}
	return &DBTypeStep{
		cfg: cfg,
		sel: NewSelectModel("Select your database type", items, "oracle"),
	}
}

func (s *DBTypeStep) Title() string { return "Database Type" }
func (s *DBTypeStep) Done() bool    { return s.sel.Done() }

func (s *DBTypeStep) Init() tea.Cmd { return s.sel.Init() }

func (s *DBTypeStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.sel.Update(msg)
	s.sel = updated
	if s.sel.Done() {
		s.cfg.DBType = s.sel.Selected()
	}
	return s, cmd
}

func (s *DBTypeStep) View() string {
	panel := InfoPanel("Multi-Database",
		"OpenDB 用同一套交互体验覆盖主流数据库。\n"+
			"无论你用哪种数据库，诊断、监控、技能命令\n"+
			"都保持一致的操作方式。")
	return "\n" + panel + "\n" + s.sel.View()
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/setup/dbtype.go internal/setup/permission.go internal/setup/permission_test.go
git commit -m "feat: setup wizard database type selection and permission guide"
```

---

### Task 6: Connection Form (Step 3c)

**Files:**
- Create: `internal/setup/connform.go`

- [ ] **Step 1: Implement connection form step**

This is a multi-field form. Each field is filled sequentially (enter advances to next field). The form collects: connection name, host, port, service/database, username, auth method.

```go
// internal/setup/connform.go
package setup

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/config"
)

type connFormPhase int

const (
	phaseFields connFormPhase = iota
	phaseAuth
	phasePassword
)

// ConnFormStep collects database connection details
type ConnFormStep struct {
	cfg       *SetupConfig
	fields    []*InputModel
	authSel   *SelectModel
	passwdInp *InputModel
	fieldIdx  int
	phase     connFormPhase
	done      bool
}

func NewConnFormStep(cfg *SetupConfig) *ConnFormStep {
	defaultPort := "1521"
	serviceLabel := "Service name"
	if cfg.DBType == "mysql" {
		defaultPort = "3306"
		serviceLabel = "Database name"
	} else if cfg.DBType == "postgres" {
		defaultPort = "5432"
		serviceLabel = "Database name"
	}

	fields := []*InputModel{
		NewInputModelWithHint("Connection name", "", "例如: prod-oracle-01"),
		NewInputModelWithHint("Host", "127.0.0.1", "数据库主机地址"),
		NewInputModel("Port", defaultPort, false),
		NewInputModel(serviceLabel, "", false),
		NewInputModel("Username", "", false),
	}

	authItems := buildAuthItems(cfg.DBType)

	return &ConnFormStep{
		cfg:     cfg,
		fields:  fields,
		authSel: NewSelectModel("Authentication method", authItems, "prompt"),
	}
}

func buildAuthItems(dbType string) []SelectItem {
	items := []SelectItem{
		{Label: "prompt", Value: "prompt", Desc: "每次连接时输入密码"},
		{Label: "save", Value: "save", Desc: "加密保存到本地 (AES-256-GCM)"},
	}
	if dbType == "oracle" {
		items = append(items,
			SelectItem{Label: "wallet", Value: "wallet", Desc: "Oracle Wallet 自动登录"},
			SelectItem{Label: "os", Value: "os", Desc: "操作系统认证"},
		)
	}
	items = append(items,
		SelectItem{Label: "ldap", Value: "ldap", Desc: "LDAP/AD 认证"},
		SelectItem{Label: "kerberos", Value: "kerberos", Desc: "Kerberos 认证"},
		SelectItem{Label: "token", Value: "token", Desc: "OAuth2 令牌认证"},
	)
	return items
}

func (s *ConnFormStep) Title() string { return "Connection" }
func (s *ConnFormStep) Done() bool    { return s.done }

func (s *ConnFormStep) Init() tea.Cmd { return nil }

func (s *ConnFormStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case phaseFields:
		return s.updateFields(msg)
	case phaseAuth:
		return s.updateAuth(msg)
	case phasePassword:
		return s.updatePassword(msg)
	}
	return s, nil
}

func (s *ConnFormStep) updateFields(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.fields[s.fieldIdx].Update(msg)
	s.fields[s.fieldIdx] = updated
	if s.fields[s.fieldIdx].Done() {
		s.fieldIdx++
		if s.fieldIdx >= len(s.fields) {
			s.phase = phaseAuth
		}
		return s, nil
	}
	return s, cmd
}

func (s *ConnFormStep) updateAuth(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.authSel.Update(msg)
	s.authSel = updated
	if s.authSel.Done() {
		authMode := s.authSel.Selected()
		if authMode == "prompt" || authMode == "save" {
			s.passwdInp = NewInputModel("Password", "", true)
			s.phase = phasePassword
			return s, nil
		}
		// No password needed for wallet/os/kerberos, or more fields for ldap/token
		// Simplified: just build connection
		s.buildConnection()
		s.done = true
		return s, func() tea.Msg { return stepDoneMsg{} }
	}
	return s, cmd
}

func (s *ConnFormStep) updatePassword(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.passwdInp.Update(msg)
	s.passwdInp = updated
	if s.passwdInp.Done() {
		s.buildConnection()
		s.done = true
		return s, func() tea.Msg { return stepDoneMsg{} }
	}
	return s, cmd
}

func (s *ConnFormStep) buildConnection() {
	port, _ := strconv.Atoi(s.fields[2].ValueOrDefault())
	authMode := s.authSel.Selected()

	conn := config.Connection{
		Name:     s.fields[0].ValueOrDefault(),
		DBType:   s.cfg.DBType,
		Host:     s.fields[1].ValueOrDefault(),
		Port:     port,
		User:     s.fields[4].ValueOrDefault(),
		AuthMode: authMode,
		Credential: config.Credential{
			Provider: authMode,
		},
	}
	// Service vs Database
	if s.cfg.DBType == "oracle" {
		conn.Service = s.fields[3].ValueOrDefault()
	} else {
		conn.Database = s.fields[3].ValueOrDefault()
	}

	s.cfg.Connection = conn
	if s.passwdInp != nil {
		s.cfg.Password = s.passwdInp.Value()
	}
}

func (s *ConnFormStep) View() string {
	var b strings.Builder
	b.WriteString("\n")

	// Show completed fields
	for i := 0; i < len(s.fields); i++ {
		if i < s.fieldIdx {
			// Completed field
			b.WriteString(SuccessLine(s.fields[i].label + ": " + s.fields[i].ValueOrDefault()) + "\n")
		} else if i == s.fieldIdx && s.phase == phaseFields {
			// Current field
			b.WriteString(s.fields[i].View())
		}
	}

	if s.phase == phaseAuth {
		// Show all fields as completed
		for i := s.fieldIdx; i < len(s.fields); i++ {
			b.WriteString(SuccessLine(s.fields[i].label + ": " + s.fields[i].ValueOrDefault()) + "\n")
		}
		b.WriteString(s.authSel.View())
	}

	if s.phase == phasePassword {
		b.WriteString(SuccessLine("Auth: " + s.authSel.SelectedLabel()) + "\n")
		b.WriteString(s.passwdInp.View())
	}

	return b.String()
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 3: Commit**

```bash
git add internal/setup/connform.go
git commit -m "feat: setup wizard connection form with multi-auth support"
```

---

### Task 7: Connection Testing + Permission Validation (Step 3d)

**Files:**
- Create: `internal/setup/conntest.go`
- Create: `internal/setup/conntest_test.go`

- [ ] **Step 1: Write test for permission check SQL generation**

```go
// internal/setup/conntest_test.go
package setup

import "testing"

func TestPermissionCheckQueries_Oracle(t *testing.T) {
	queries := PermissionCheckQueries("oracle")
	if len(queries) == 0 {
		t.Error("expected non-empty permission check queries for oracle")
	}
	for _, q := range queries {
		if q.SQL == "" {
			t.Error("query SQL must not be empty")
		}
		if q.Name == "" {
			t.Error("query Name must not be empty")
		}
	}
}

func TestPermissionCheckQueries_MySQL(t *testing.T) {
	queries := PermissionCheckQueries("mysql")
	if len(queries) == 0 {
		t.Error("expected non-empty permission check queries for mysql")
	}
}

func TestPermissionCheckQueries_Postgres(t *testing.T) {
	queries := PermissionCheckQueries("postgres")
	if len(queries) == 0 {
		t.Error("expected non-empty permission check queries for postgres")
	}
}

func TestOverprivilegeCheckQueries(t *testing.T) {
	queries := OverprivilegeCheckQueries("oracle")
	if len(queries) == 0 {
		t.Error("expected non-empty overprivilege check queries for oracle")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -run TestPermissionCheck -v`
Expected: FAIL

- [ ] **Step 3: Implement conntest.go**

```go
// internal/setup/conntest.go
package setup

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/db"
)

// PermCheckQuery represents one permission check
type PermCheckQuery struct {
	Name string // display name
	SQL  string // test query
}

// PermissionCheckQueries returns queries to test minimum required permissions
func PermissionCheckQueries(dbType string) []PermCheckQuery {
	switch dbType {
	case "oracle":
		return []PermCheckQuery{
			{"CREATE SESSION", "SELECT 1 FROM DUAL"},
			{"V$SESSION access", "SELECT COUNT(*) FROM V$SESSION WHERE ROWNUM=1"},
			{"V$SQL access", "SELECT COUNT(*) FROM V$SQL WHERE ROWNUM=1"},
			{"DBA_HIST views", "SELECT COUNT(*) FROM DBA_HIST_SNAPSHOT WHERE ROWNUM=1"},
		}
	case "mysql":
		return []PermCheckQuery{
			{"Connect", "SELECT 1"},
			{"PROCESS", "SELECT COUNT(*) FROM information_schema.PROCESSLIST"},
			{"SHOW DATABASES", "SHOW DATABASES"},
			{"Performance Schema", "SELECT COUNT(*) FROM performance_schema.threads WHERE ROWNUM=1"},
		}
	case "postgres":
		return []PermCheckQuery{
			{"Connect", "SELECT 1"},
			{"pg_stat_activity", "SELECT COUNT(*) FROM pg_stat_activity"},
			{"pg_stat_database", "SELECT COUNT(*) FROM pg_stat_database"},
			{"pg_locks", "SELECT COUNT(*) FROM pg_locks"},
		}
	default:
		return nil
	}
}

// OverprivilegeCheckQueries returns queries to detect excessive permissions
func OverprivilegeCheckQueries(dbType string) []PermCheckQuery {
	switch dbType {
	case "oracle":
		return []PermCheckQuery{
			{"DBA role", "SELECT COUNT(*) FROM USER_ROLE_PRIVS WHERE GRANTED_ROLE='DBA'"},
			{"SYSDBA privilege", "SELECT COUNT(*) FROM V$PWFILE_USERS WHERE USERNAME=USER AND SYSDBA='TRUE'"},
		}
	case "mysql":
		return []PermCheckQuery{
			{"SUPER privilege", "SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=CONCAT('''',USER(),'''') AND PRIVILEGE_TYPE='SUPER'"},
		}
	case "postgres":
		return []PermCheckQuery{
			{"SUPERUSER", "SELECT CASE WHEN usesuper THEN 1 ELSE 0 END FROM pg_user WHERE usename=current_user"},
		}
	default:
		return nil
	}
}

// ConnTestResult holds one test result
type ConnTestResult struct {
	Name    string
	OK      bool
	Warning bool // overprivilege detected
	Detail  string
}

// connTestDoneMsg sent when all tests complete
type connTestDoneMsg struct {
	dbVersion string
	results   []ConnTestResult
	err       error
}

// ConnTestStep runs connection and permission tests
type ConnTestStep struct {
	cfg        *SetupConfig
	driver     db.Driver
	driverFunc func(cfg *SetupConfig) (db.Driver, error) // injected for testability
	results    []ConnTestResult
	dbVersion  string
	testing    bool
	done       bool
	err        error
}

func NewConnTestStep(cfg *SetupConfig, driverFunc func(*SetupConfig) (db.Driver, error)) *ConnTestStep {
	return &ConnTestStep{
		cfg:        cfg,
		driverFunc: driverFunc,
	}
}

func (s *ConnTestStep) Title() string { return "Connection Test" }
func (s *ConnTestStep) Done() bool    { return s.done }

func (s *ConnTestStep) Init() tea.Cmd {
	s.testing = true
	return s.runTests()
}

func (s *ConnTestStep) runTests() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Connect
		drv, err := s.driverFunc(s.cfg)
		if err != nil {
			return connTestDoneMsg{err: fmt.Errorf("连接失败: %w", err)}
		}
		defer drv.Close()

		info := drv.ServerInfo()
		dbVersion := info.Version

		var results []ConnTestResult

		// Permission checks
		for _, q := range PermissionCheckQueries(s.cfg.DBType) {
			_, err := drv.Query(ctx, q.SQL)
			results = append(results, ConnTestResult{
				Name:   q.Name,
				OK:     err == nil,
				Detail: errDetail(err),
			})
		}

		// Overprivilege checks
		for _, q := range OverprivilegeCheckQueries(s.cfg.DBType) {
			qr, err := drv.Query(ctx, q.SQL)
			hasPriv := false
			if err == nil && len(qr.Rows) > 0 {
				if val, ok := qr.Rows[0][0].(int64); ok && val > 0 {
					hasPriv = true
				}
			}
			if hasPriv {
				results = append(results, ConnTestResult{
					Name:    q.Name,
					OK:      true,
					Warning: true,
					Detail:  "权限偏大，建议收窄",
				})
			}
		}

		return connTestDoneMsg{dbVersion: dbVersion, results: results}
	}
}

func errDetail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *ConnTestStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connTestDoneMsg:
		s.testing = false
		s.dbVersion = msg.dbVersion
		s.results = msg.results
		s.err = msg.err
		s.done = true
		return s, func() tea.Msg { return stepDoneMsg{} }
	case tea.KeyMsg:
		if msg.String() == "enter" && s.done {
			return s, func() tea.Msg { return stepDoneMsg{} }
		}
	}
	return s, nil
}

func (s *ConnTestStep) View() string {
	var b strings.Builder
	b.WriteString("\n")

	if s.testing {
		b.WriteString(BulletLine("Testing connection...") + "\n")
		return b.String()
	}

	if s.err != nil {
		b.WriteString(ErrorLine(s.err.Error()) + "\n")
		return b.String()
	}

	// Connection success
	b.WriteString(SuccessLine(fmt.Sprintf("Connection successful (%s)", s.dbVersion)) + "\n\n")

	// Permission results
	passed := 0
	for _, r := range s.results {
		if r.Warning {
			b.WriteString(WarningLine(r.Name + " — " + r.Detail) + "\n")
		} else if r.OK {
			b.WriteString(SuccessLine(r.Name + " — OK") + "\n")
			passed++
		} else {
			b.WriteString(ErrorLine(r.Name + " — " + r.Detail) + "\n")
		}
	}

	requiredCount := len(PermissionCheckQueries(s.cfg.DBType))
	b.WriteString(fmt.Sprintf("\n  Result: %d/%d 必要权限就绪\n", passed, requiredCount))

	content := b.String()
	return InfoPanel("Connection Test", content)
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/setup/conntest.go internal/setup/conntest_test.go
git commit -m "feat: setup wizard connection testing with permission validation"
```

---

### Task 8: Sentinel Configuration (Step 4)

**Files:**
- Create: `internal/setup/sentinel.go`

- [ ] **Step 1: Implement sentinel step**

```go
// internal/setup/sentinel.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type sentinelPhase int

const (
	sentinelAutoStart sentinelPhase = iota
	sentinelInterval
)

type SentinelStep struct {
	cfg       *SetupConfig
	phase     sentinelPhase
	autoStart *SelectModel
	interval  *SelectModel
	done      bool
}

func NewSentinelStep(cfg *SetupConfig) *SentinelStep {
	return &SentinelStep{
		cfg: cfg,
		autoStart: NewSelectModel("Enable Sentinel auto-start?", []SelectItem{
			{Label: "Yes", Value: "yes", Desc: "recommended"},
			{Label: "No", Value: "no", Desc: "手动启动"},
		}, "yes"),
		interval: NewSelectModel("Probe interval", []SelectItem{
			{Label: "1 second", Value: "1s", Desc: "default, recommended"},
			{Label: "3 seconds", Value: "3s"},
			{Label: "5 seconds", Value: "5s"},
			{Label: "10 seconds", Value: "10s"},
		}, "1s"),
	}
}

func (s *SentinelStep) Title() string { return "Sentinel" }
func (s *SentinelStep) Done() bool    { return s.done }

func (s *SentinelStep) Init() tea.Cmd { return nil }

func (s *SentinelStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case sentinelAutoStart:
		updated, cmd := s.autoStart.Update(msg)
		s.autoStart = updated
		if s.autoStart.Done() {
			s.cfg.Sentinel.AutoStart = s.autoStart.Selected() == "yes"
			s.phase = sentinelInterval
			return s, nil
		}
		return s, cmd
	case sentinelInterval:
		updated, cmd := s.interval.Update(msg)
		s.interval = updated
		if s.interval.Done() {
			switch s.interval.Selected() {
			case "3s":
				s.cfg.Sentinel.ProbeInterval = 3_000_000_000
			case "5s":
				s.cfg.Sentinel.ProbeInterval = 5_000_000_000
			case "10s":
				s.cfg.Sentinel.ProbeInterval = 10_000_000_000
			default:
				s.cfg.Sentinel.ProbeInterval = 1_000_000_000
			}
			s.done = true
			return s, func() tea.Msg { return stepDoneMsg{} }
		}
		return s, cmd
	}
	return s, nil
}

func (s *SentinelStep) View() string {
	var b strings.Builder

	intro := strings.Join([]string{
		"Sentinel 是 OpenDB 的实时异常检测引擎。",
		"它在后台以每秒一次的频率轻量采集数据库核心指标:",
		"",
		StyleSuccess.Render("·") + " 活跃会话 / CPU 会话 / IO 会话 / 锁等待",
		StyleSuccess.Render("·") + " 慢 SQL / Redo 速率 / 硬解析率",
		"",
		"当指标异常冲高时，Sentinel 自动进入高频采集模式，",
		"200ms 一次，捕获问题现场，并生成根因分析报告。",
		"",
		"对数据库性能影响 < 0.1%，可安全用于生产环境。",
	}, "\n")

	b.WriteString("\n" + InfoPanel("Sentinel — Real-time Anomaly Detection", intro) + "\n")

	switch s.phase {
	case sentinelAutoStart:
		b.WriteString(s.autoStart.View())
	case sentinelInterval:
		b.WriteString(SuccessLine("Auto-start: " + s.autoStart.SelectedLabel()) + "\n")
		b.WriteString(s.interval.View())
	}

	return b.String()
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 3: Commit**

```bash
git add internal/setup/sentinel.go
git commit -m "feat: setup wizard sentinel configuration with feature introduction"
```

---

### Task 9: LLM Configuration + Testing (Step 5)

**Files:**
- Create: `internal/setup/llmconfig.go`
- Create: `internal/setup/llmtest.go`

- [ ] **Step 1: Implement LLM config step**

```go
// internal/setup/llmconfig.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type llmPhase int

const (
	llmChooseProvider llmPhase = iota
	llmEnterURL
	llmEnterModel
)

type LLMConfigStep struct {
	cfg      *SetupConfig
	phase    llmPhase
	provider *SelectModel
	urlInput *InputModel
	modelInp *InputModel
	done     bool
}

func NewLLMConfigStep(cfg *SetupConfig) *LLMConfigStep {
	return &LLMConfigStep{
		cfg: cfg,
		provider: NewSelectModel("Configure LLM?", []SelectItem{
			{Label: "Ollama", Value: "ollama", Desc: "本地部署，数据不出内网"},
			{Label: "Skip", Value: "none", Desc: "暂不配置"},
		}, "ollama"),
		urlInput: NewInputModel("Ollama API address", "http://localhost:11434", false),
		modelInp: NewInputModel("Model name", "ailinkdb", false),
	}
}

func (s *LLMConfigStep) Title() string { return "LLM" }
func (s *LLMConfigStep) Done() bool    { return s.done }

func (s *LLMConfigStep) Init() tea.Cmd { return nil }

func (s *LLMConfigStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case llmChooseProvider:
		updated, cmd := s.provider.Update(msg)
		s.provider = updated
		if s.provider.Done() {
			if s.provider.Selected() == "none" {
				s.cfg.LLM.Provider = "none"
				s.done = true
				return s, func() tea.Msg { return stepDoneMsg{} }
			}
			s.phase = llmEnterURL
			return s, nil
		}
		return s, cmd
	case llmEnterURL:
		updated, cmd := s.urlInput.Update(msg)
		s.urlInput = updated
		if s.urlInput.Done() {
			s.phase = llmEnterModel
			return s, nil
		}
		return s, cmd
	case llmEnterModel:
		updated, cmd := s.modelInp.Update(msg)
		s.modelInp = updated
		if s.modelInp.Done() {
			s.cfg.LLM.Provider = "ollama"
			s.cfg.LLM.BaseURL = s.urlInput.ValueOrDefault()
			s.cfg.LLM.Model = s.modelInp.ValueOrDefault()
			s.done = true
			return s, func() tea.Msg { return stepDoneMsg{} }
		}
		return s, cmd
	}
	return s, nil
}

func (s *LLMConfigStep) View() string {
	var b strings.Builder

	intro := strings.Join([]string{
		"OpenDB 内置 LLM 诊断引擎，当数据库出现性能问题时，",
		"用自然语言描述现象，LLM 会分析根因并给出",
		"可直接执行的 SQL 修复方案。",
		"",
		"支持 Ollama 本地部署的大模型，数据不出内网。",
	}, "\n")

	b.WriteString("\n" + InfoPanel("LLM Diagnostics", intro) + "\n")

	switch s.phase {
	case llmChooseProvider:
		b.WriteString(s.provider.View())
	case llmEnterURL:
		b.WriteString(SuccessLine("Provider: Ollama") + "\n")
		b.WriteString(s.urlInput.View())
	case llmEnterModel:
		b.WriteString(SuccessLine("Provider: Ollama") + "\n")
		b.WriteString(SuccessLine("URL: " + s.urlInput.ValueOrDefault()) + "\n")
		b.WriteString(s.modelInp.View())
	}

	return b.String()
}
```

- [ ] **Step 2: Implement LLM connectivity test**

```go
// internal/setup/llmtest.go
package setup

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/llm/ollama"
)

type llmTestDoneMsg struct {
	reachable  bool
	modelFound bool
	inference  bool
	latency    time.Duration
	err        error
}

type LLMTestStep struct {
	cfg     *SetupConfig
	testing bool
	result  *llmTestDoneMsg
	done    bool
}

func NewLLMTestStep(cfg *SetupConfig) *LLMTestStep {
	return &LLMTestStep{cfg: cfg}
}

func (s *LLMTestStep) Title() string { return "LLM Test" }
func (s *LLMTestStep) Done() bool    { return s.done }

func (s *LLMTestStep) Init() tea.Cmd {
	if s.cfg.LLM.Provider == "none" {
		s.done = true
		return func() tea.Msg { return stepDoneMsg{} }
	}
	s.testing = true
	return s.runTest()
}

func (s *LLMTestStep) runTest() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		provider := ollama.NewOllamaProvider(s.cfg.LLM.BaseURL, s.cfg.LLM.Model)

		// Test 1: reachable (simple chat)
		start := time.Now()
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
		})
		latency := time.Since(start)

		if err != nil {
			return llmTestDoneMsg{err: err}
		}

		return llmTestDoneMsg{
			reachable:  true,
			modelFound: true,
			inference:  resp != nil && resp.Content != "",
			latency:    latency,
		}
	}
}

func (s *LLMTestStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case llmTestDoneMsg:
		s.testing = false
		s.result = &msg
		s.done = true
		return s, func() tea.Msg { return stepDoneMsg{} }
	}
	return s, nil
}

func (s *LLMTestStep) View() string {
	var b strings.Builder

	if s.testing {
		content := strings.Join([]string{
			BulletLine(fmt.Sprintf("Connecting to %s...", s.cfg.LLM.BaseURL)),
			BulletLine(fmt.Sprintf("Checking model %s...", s.cfg.LLM.Model)),
			BulletLine("Testing inference..."),
		}, "\n")
		return "\n" + InfoPanel("LLM Connection Test", content)
	}

	if s.result == nil {
		return ""
	}

	if s.result.err != nil {
		content := strings.Join([]string{
			ErrorLine(fmt.Sprintf("连接失败: %s", s.result.err)),
			"",
			StyleDim.Render("  LLM 配置已保存，你可以稍后修复后使用。"),
		}, "\n")
		return "\n" + InfoPanel("LLM Connection Test", content)
	}

	content := strings.Join([]string{
		SuccessLine(fmt.Sprintf("Ollama reachable (%s)", s.cfg.LLM.BaseURL)),
		SuccessLine(fmt.Sprintf("Model %s available", s.cfg.LLM.Model)),
		SuccessLine(fmt.Sprintf("Inference OK (%.1fs)", s.result.latency.Seconds())),
		"",
		"Result: LLM 配置就绪",
	}, "\n")
	return "\n" + InfoPanel("LLM Connection Test", content)
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 4: Commit**

```bash
git add internal/setup/llmconfig.go internal/setup/llmtest.go
git commit -m "feat: setup wizard LLM configuration and connectivity testing"
```

---

### Task 10: Rule Engine + Skills Showcase (Steps 6-7)

**Files:**
- Create: `internal/setup/rule.go`
- Create: `internal/setup/skills.go`

- [ ] **Step 1: Implement Rule Engine step**

```go
// internal/setup/rule.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type RuleStep struct {
	cfg  *SetupConfig
	sel  *SelectModel
}

func NewRuleStep(cfg *SetupConfig) *RuleStep {
	return &RuleStep{
		cfg: cfg,
		sel: NewSelectModel("Enable Rule Engine?", []SelectItem{
			{Label: "Yes", Value: "yes", Desc: "recommended — LLM 不可用时自动兜底"},
			{Label: "No", Value: "no"},
		}, "yes"),
	}
}

func (s *RuleStep) Title() string { return "Rule Engine" }
func (s *RuleStep) Done() bool    { return s.sel.Done() }

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
		"当 LLM 无法工作时（网络不通、模型不可用），",
		"由 Rule Engine 承担诊断决策。",
		"",
		"内置数十条经过验证的数据库诊断规则，",
		"覆盖常见性能问题，无需外部依赖即可工作。",
		"",
		StyleDim.Render("诊断三层架构: 探针 → Rule Engine → LLM 推理"),
	}, "\n")

	return "\n" + InfoPanel("Rule Engine — 兜底决策引擎", intro) + "\n" + s.sel.View()
}
```

- [ ] **Step 2: Implement skills showcase step**

```go
// internal/setup/skills.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SkillCategory groups related skills for display
type SkillCategory struct {
	Name   string
	Skills []SkillInfo
}

type SkillInfo struct {
	Command string
	Desc    string
}

// DefaultSkillCategories returns the built-in skill showcase data
func DefaultSkillCategories() []SkillCategory {
	return []SkillCategory{
		{
			Name: "监控大盘",
			Skills: []SkillInfo{
				{"/dbtop", "实时数据库监控面板"},
				{"/health", "数据库健康检查"},
				{"/sentinel", "Sentinel 异常检测控制"},
			},
		},
		{
			Name: "会话 / 锁",
			Skills: []SkillInfo{
				{"/session", "活跃会话列表"},
				{"/lock", "锁等待分析"},
				{"/kill", "终止会话"},
			},
		},
		{
			Name: "SQL 分析",
			Skills: []SkillInfo{
				{"/sql", "SQL 执行分析"},
				{"/plan", "执行计划查看"},
			},
		},
		{
			Name: "存储 / 管理",
			Skills: []SkillInfo{
				{"/space", "表空间使用"},
				{"/resize", "表空间扩容"},
				{"/backup", "备份状态"},
				{"/params", "参数查询/修改"},
				{"/alert", "告警日志"},
			},
		},
		{
			Name: "LLM 诊断",
			Skills: []SkillInfo{
				{"/diag", "自动诊断"},
				{"/llm", "LLM 交互式诊断"},
				{"/rule", "规则引擎诊断"},
			},
		},
		{
			Name: "系统",
			Skills: []SkillInfo{
				{"/help", "命令列表"},
				{"/login", "连接数据库"},
				{"/logout", "断开连接"},
				{"/conn", "连接信息"},
				{"/config", "配置管理"},
				{"/model", "模型切换"},
				{"/history", "命令历史"},
				{"/scheduler", "定时任务"},
			},
		},
	}
}

type SkillsStep struct {
	categories []SkillCategory
	done       bool
}

func NewSkillsStep() *SkillsStep {
	return &SkillsStep{
		categories: DefaultSkillCategories(),
	}
}

func (s *SkillsStep) Title() string { return "Skills" }
func (s *SkillsStep) Done() bool    { return s.done }

func (s *SkillsStep) Init() tea.Cmd { return nil }

func (s *SkillsStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "enter" {
			s.done = true
			return s, func() tea.Msg { return stepDoneMsg{} }
		}
	}
	return s, nil
}

func (s *SkillsStep) View() string {
	var b strings.Builder

	for _, cat := range s.categories {
		b.WriteString("\n  " + StyleHighlight.Render(cat.Name) + "\n")
		for _, sk := range cat.Skills {
			cmd := StyleBrand.Render(sk.Command)
			b.WriteString("    " + cmd + StyleDim.Render("  "+sk.Desc) + "\n")
		}
	}

	totalSkills := 0
	for _, cat := range s.categories {
		totalSkills += len(cat.Skills)
	}

	content := b.String()
	footer := StyleDim.Render("  Press Enter to continue")

	return "\n" + InfoPanel("Skills — /命令技能系统",
		"OpenDB 提供丰富的 /命令，覆盖监控、诊断、管理等场景。\n"+
			"所有命令在交互式 REPL 中输入即可使用。") +
		"\n" + content + "\n" + footer + "\n"
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./internal/setup/`
Expected: Clean compilation

- [ ] **Step 4: Commit**

```bash
git add internal/setup/rule.go internal/setup/skills.go
git commit -m "feat: setup wizard rule engine config and skills showcase"
```

---

### Task 11: Security Config + Finalize (Steps 8-9)

**Files:**
- Create: `internal/setup/security.go`
- Create: `internal/setup/finalize.go`
- Create: `internal/setup/finalize_test.go`

- [ ] **Step 1: Write config generation test**

```go
// internal/setup/finalize_test.go
package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
)

func TestGenerateConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &SetupConfig{
		BaseDir: tmpDir,
		Mode:    "quickstart",
		DBType:  "oracle",
		Connection: config.Connection{
			Name:    "test-conn",
			DBType:  "oracle",
			Host:    "192.168.1.100",
			Port:    1521,
			Service: "ORCL",
			User:    "opendb",
			AuthMode: "prompt",
			Credential: config.Credential{Provider: "prompt"},
		},
		LLM: config.LLMConfig{
			Provider: "ollama",
			BaseURL:  "http://localhost:11434",
			Model:    "ailinkdb",
		},
		Security: config.SecurityConfig{
			ConfirmOnDangerous: true,
		},
		Sentinel: config.Default().Sentinel,
	}

	err := GenerateConfig(cfg)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	// Check config file exists
	configPath := filepath.Join(tmpDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config.yaml not created")
	}

	// Check connection file exists
	connPath := filepath.Join(tmpDir, "connections", "test-conn.yaml")
	if _, err := os.Stat(connPath); os.IsNotExist(err) {
		t.Error("connection file not created")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -run TestGenerateConfig -v`
Expected: FAIL

- [ ] **Step 3: Implement security.go**

```go
// internal/setup/security.go
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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
		"OpenDB 提供操作安全保护:",
		StyleSuccess.Render("·") + " DROP / DELETE / TRUNCATE 等危险操作",
		"  执行前要求二次确认，防止误操作。",
	}, "\n")

	return "\n" + InfoPanel("Security", intro) + "\n" + s.sel.View()
}
```

- [ ] **Step 4: Implement finalize.go**

```go
// internal/setup/finalize.go
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/sqlrush/opendb/internal/config"
)

// GenerateConfig writes config.yaml and connection file to disk
func GenerateConfig(cfg *SetupConfig) error {
	// Create directories
	connDir := filepath.Join(cfg.BaseDir, "connections")
	if err := os.MkdirAll(connDir, 0o750); err != nil {
		return fmt.Errorf("create connections dir: %w", err)
	}

	histDir := filepath.Join(cfg.BaseDir, "history")
	if err := os.MkdirAll(histDir, 0o750); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}

	// Build config
	appConfig := config.Config{
		ConnectionsDir: connDir,
		Security:       cfg.Security,
		Output: config.OutputConfig{
			Format:  "terminal",
			MaxRows: 1000,
		},
		LLM: cfg.LLM,
		Session: config.SessionConfig{
			RestoreOnSwitch: true,
			HistoryDir:      histDir,
		},
		Sentinel: cfg.Sentinel,
	}

	// Write config.yaml
	configPath := filepath.Join(cfg.BaseDir, "config.yaml")
	configData, err := yaml.Marshal(appConfig)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, configData, 0o640); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Write connection file
	connGroup := config.ConnectionGroup{
		Group:       "default",
		Tags:        []string{"setup-wizard"},
		Connections: []config.Connection{cfg.Connection},
	}
	connData, err := yaml.Marshal(connGroup)
	if err != nil {
		return fmt.Errorf("marshal connection: %w", err)
	}
	connFileName := cfg.Connection.Name + ".yaml"
	if connFileName == ".yaml" {
		connFileName = "default.yaml"
	}
	connPath := filepath.Join(connDir, connFileName)
	if err := os.WriteFile(connPath, connData, 0o640); err != nil {
		return fmt.Errorf("write connection: %w", err)
	}

	return nil
}

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

type generateDoneMsg struct{ err error }

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
			return s, func() tea.Msg { return stepDoneMsg{} }
		}
		// Wait for user to press Enter after seeing results
		return s, nil
	case tea.KeyMsg:
		if msg.String() == "enter" && s.generated {
			s.done = true
			return s, func() tea.Msg { return stepDoneMsg{} }
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

	// Config files
	configPath := filepath.Join(s.cfg.BaseDir, "config.yaml")
	connName := s.cfg.Connection.Name
	if connName == "" {
		connName = "default"
	}
	connPath := filepath.Join(s.cfg.BaseDir, "connections", connName+".yaml")

	fileInfo := strings.Join([]string{
		SuccessLine("Config saved: " + configPath),
		SuccessLine("Connection saved: " + connPath),
	}, "\n")
	b.WriteString("\n" + InfoPanel("Configuration", fileInfo) + "\n")

	// Test run placeholder — actual execution happens after alt screen exits
	testRun := strings.Join([]string{
		"安装完成后将自动执行:",
		"",
		StyleBrand.Render("  $ opendb help"),
		StyleBrand.Render("  $ opendb health"),
		"",
		"让你直观感受 OpenDB 的功能。",
	}, "\n")
	b.WriteString("\n" + InfoPanel("Test Run", testRun) + "\n")

	// Completion message
	b.WriteString("\n  🚀 " + StyleSuccess.Render("OpenDB is ready!") + "\n\n")

	commands := []string{
		BulletLine("opendb" + StyleDim.Render("                  — 启动交互式 REPL")),
		BulletLine("opendb -c " + connName + StyleDim.Render("  — 连接到指定数据库")),
		BulletLine("opendb configure" + StyleDim.Render("      — 添加更多连接或修改配置")),
		BulletLine("opendb --setup" + StyleDim.Render("        — 重新运行完整配置向导")),
		BulletLine("opendb --version" + StyleDim.Render("      — 查看版本")),
	}
	b.WriteString(strings.Join(commands, "\n"))
	b.WriteString("\n\n" + StyleDim.Render("  Press Enter to exit setup.") + "\n")

	return b.String()
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/setup/security.go internal/setup/finalize.go internal/setup/finalize_test.go
git commit -m "feat: setup wizard security config and config file generation"
```

---

### Task 12: Wire Up Orchestrator — buildSetupSteps + --setup Flag

**Files:**
- Modify: `internal/setup/setup.go`
- Modify: `cmd/opendb/main.go`
- Create: `internal/setup/configure.go`

- [ ] **Step 1: Update setup.go — wire all steps into buildSetupSteps**

Update the `buildSetupSteps` and `buildConfigureSteps` functions in `internal/setup/setup.go`:

```go
// Replace the empty buildSetupSteps in setup.go with:
func buildSetupSteps(cfg *SetupConfig, driverFunc func(*SetupConfig) (db.Driver, error)) []Step {
	return []Step{
		NewWelcomeStep(),
		NewModeStep(cfg),
		NewDBTypeStep(cfg),
		NewPermissionStep(cfg),
		NewConnFormStep(cfg),
		NewConnTestStep(cfg, driverFunc),
		NewSentinelStep(cfg),     // skipped in QuickStart handled by orchestrator
		NewLLMConfigStep(cfg),
		NewLLMTestStep(cfg),
		NewRuleStep(cfg),         // skipped in QuickStart handled by orchestrator
		NewSkillsStep(),
		NewSecurityStep(cfg),     // skipped in QuickStart handled by orchestrator
		NewFinalizeStep(cfg),
	}
}
```

Also update the orchestrator's `Update` method to skip steps based on QuickStart mode:

```go
func (m *Model) shouldSkip(step Step) bool {
	if m.cfg.Mode != "quickstart" {
		return false
	}
	title := step.Title()
	// QuickStart skips: Sentinel, Rule Engine, Security
	return title == "Sentinel" || title == "Rule Engine" || title == "Security"
}
```

Update the step transition in `Update`:

```go
case stepDoneMsg:
	m.current++
	// Skip steps based on mode
	for m.current < len(m.steps) && m.shouldSkip(m.steps[m.current]) {
		m.current++
	}
	if m.current >= len(m.steps) {
		return m, tea.Quit
	}
	return m, m.steps[m.current].Init()
```

- [ ] **Step 2: Implement configure.go**

```go
// internal/setup/configure.go
package setup

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/db"
)

type configurePhase int

const (
	configureMenu configurePhase = iota
	configureAction
)

type ConfigureModel struct {
	cfg         *SetupConfig
	menu        *SelectModel
	activeStep  Step
	phase       configurePhase
	driverFunc  func(*SetupConfig) (db.Driver, error)
	quitting    bool
}

func NewConfigureModel(baseDir string, driverFunc func(*SetupConfig) (db.Driver, error)) *ConfigureModel {
	cfg := &SetupConfig{BaseDir: baseDir}

	menu := NewSelectModel("What would you like to configure?", []SelectItem{
		{Label: "Add/Edit database connection", Value: "connection"},
		{Label: "Sentinel settings", Value: "sentinel"},
		{Label: "LLM settings", Value: "llm"},
		{Label: "Rule Engine settings", Value: "rule"},
		{Label: "Security settings", Value: "security"},
		{Label: "Full setup (reconfigure all)", Value: "full"},
	}, "connection")

	return &ConfigureModel{
		cfg:        cfg,
		menu:       menu,
		driverFunc: driverFunc,
	}
}

func (m *ConfigureModel) Init() tea.Cmd { return nil }

func (m *ConfigureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	}

	switch m.phase {
	case configureMenu:
		updated, cmd := m.menu.Update(msg)
		m.menu = updated
		if m.menu.Done() {
			selected := m.menu.Selected()
			if selected == "full" {
				// Run full setup
				return m, tea.Quit // caller will invoke RunSetup instead
			}
			m.activeStep = m.stepFor(selected)
			m.phase = configureAction
			return m, m.activeStep.Init()
		}
		return m, cmd

	case configureAction:
		updated, cmd := m.activeStep.Update(msg)
		m.activeStep = updated.(Step)
		if m.activeStep.Done() {
			return m, tea.Quit
		}
		return m, cmd
	}
	return m, nil
}

func (m *ConfigureModel) stepFor(choice string) Step {
	switch choice {
	case "connection":
		return NewConnFormStep(m.cfg)
	case "sentinel":
		return NewSentinelStep(m.cfg)
	case "llm":
		return NewLLMConfigStep(m.cfg)
	case "rule":
		return NewRuleStep(m.cfg)
	case "security":
		return NewSecurityStep(m.cfg)
	default:
		return NewConnFormStep(m.cfg)
	}
}

func (m *ConfigureModel) View() string {
	if m.quitting {
		return "\n  Configuration cancelled.\n\n"
	}
	switch m.phase {
	case configureMenu:
		return "\n" + m.menu.View()
	case configureAction:
		return m.activeStep.View()
	}
	return ""
}
```

- [ ] **Step 3: Add --setup and configure flags to main.go**

In `cmd/opendb/main.go`, add flag handling at the top of `main()`:

```go
// Add to main() after --version check:
if len(os.Args) >= 2 {
	switch os.Args[1] {
	case "--setup":
		if err := setup.RunSetup(defaultBaseDir()); err != nil {
			fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
			os.Exit(1)
		}
		return
	case "configure":
		if err := setup.RunConfigure(defaultBaseDir()); err != nil {
			fmt.Fprintf(os.Stderr, "Configure error: %v\n", err)
			os.Exit(1)
		}
		return
	}
}
```

Add import: `"github.com/sqlrush/opendb/internal/setup"`

Also update `runSetupWizardIfNeeded()` to use the new setup wizard:

```go
func runSetupWizardIfNeeded() error {
	baseDir := defaultBaseDir()
	configPath := filepath.Join(baseDir, "config.yaml")
	connDir := filepath.Join(baseDir, "connections")

	configExists := fileExists(configPath)
	connExists := dirExists(connDir)

	if configExists || connExists {
		return nil
	}

	return setup.RunSetup(baseDir)
}
```

- [ ] **Step 4: Verify compilation**

Run: `cd /Users/yingjiewang/opendb && go build ./cmd/opendb/`
Expected: Clean compilation

- [ ] **Step 5: Commit**

```bash
git add internal/setup/setup.go internal/setup/configure.go cmd/opendb/main.go
git commit -m "feat: wire setup wizard into main — --setup flag and configure command"
```

---

### Task 13: install.sh Bash Script

**Files:**
- Create: `install/install.sh`

- [ ] **Step 1: Create install.sh**

```bash
#!/bin/bash
# OpenDB Installer
# Usage: curl -fsSL https://opendb.ai/install.sh | bash
set -euo pipefail

# ── Brand ────────────────────────────────────────────

PURPLE='\033[0;35m'
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
DIM='\033[2m'
BOLD='\033[1m'
NC='\033[0m'

print_banner() {
    echo ""
    echo -e "${PURPLE}${BOLD}"
    echo " ██████╗ ██████╗ ███████╗███╗   ██╗██████╗ ██████╗ "
    echo "██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔══██╗██╔══██╗"
    echo "██║   ██║██████╔╝█████╗  ██╔██╗ ██║██║  ██║██████╔╝"
    echo "██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║██║  ██║██╔══██╗"
    echo "╚██████╔╝██║     ███████╗██║ ╚████║██████╔╝██████╔╝"
    echo " ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚═════╝ ╚═════╝"
    echo -e "${NC}"
    echo -e "  ${DIM}DB CLI Agent as Claude Code${NC}"
    echo -e "  ${YELLOW}最少交互，最优诊断 | Less input. More insight.${NC}"
    echo ""
}

# ── Detect Platform ──────────────────────────────────

detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux)  os="linux" ;;
        Darwin) os="darwin" ;;
        *)
            echo -e "${RED}✗ Unsupported OS: $(uname -s)${NC}"
            echo "  OpenDB supports Linux and macOS."
            exit 1
            ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)  arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)
            echo -e "${RED}✗ Unsupported architecture: $(uname -m)${NC}"
            echo "  OpenDB supports amd64 and arm64."
            exit 1
            ;;
    esac

    echo "${os}/${arch}"
}

# ── Select Mirror ────────────────────────────────────

select_mirror() {
    # TODO: Replace with actual mirror URLs when domains are ready
    local CN_MIRROR="https://dl-cn.opendb.ai"
    local INTL_MIRROR="https://dl.opendb.ai"

    # Simple geo-detection: try China mirror first, fall back to international
    if curl -s --connect-timeout 2 "${CN_MIRROR}/ping" >/dev/null 2>&1; then
        echo "${CN_MIRROR}"
        return
    fi
    echo "${INTL_MIRROR}"
}

# ── Get Latest Version ───────────────────────────────

get_latest_version() {
    local mirror="$1"
    local version

    version=$(curl -fsSL --connect-timeout 5 "${mirror}/latest-version" 2>/dev/null || echo "")
    if [ -z "${version}" ]; then
        echo -e "${RED}✗ Failed to fetch latest version${NC}" >&2
        exit 1
    fi
    echo "${version}"
}

# ── Download & Install ───────────────────────────────

download_and_install() {
    local mirror="$1"
    local version="$2"
    local platform="$3"
    local os="${platform%%/*}"
    local arch="${platform##*/}"

    local binary_name="opendb-${os}-${arch}"
    local url="${mirror}/releases/${version}/${binary_name}"
    local checksum_url="${url}.sha256"
    local install_dir="/usr/local/bin"
    local tmp_dir

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "${tmp_dir}"' EXIT

    # Download binary
    echo -e "  ${DIM}·${NC} Downloading opendb ${version}..."
    if ! curl -fSL --progress-bar -o "${tmp_dir}/opendb" "${url}"; then
        echo -e "  ${RED}✗ Download failed${NC}"
        echo "  URL: ${url}"
        exit 1
    fi
    echo -e "  ${GREEN}✓${NC} Download complete"

    # Verify checksum
    echo -e "  ${DIM}·${NC} Verifying checksum..."
    if curl -fsSL -o "${tmp_dir}/opendb.sha256" "${checksum_url}" 2>/dev/null; then
        cd "${tmp_dir}"
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum -c opendb.sha256 --quiet 2>/dev/null && \
                echo -e "  ${GREEN}✓${NC} Checksum verified" || \
                echo -e "  ${YELLOW}⚠${NC} Checksum mismatch (continuing)"
        elif command -v shasum >/dev/null 2>&1; then
            shasum -a 256 -c opendb.sha256 --quiet 2>/dev/null && \
                echo -e "  ${GREEN}✓${NC} Checksum verified" || \
                echo -e "  ${YELLOW}⚠${NC} Checksum mismatch (continuing)"
        fi
        cd - >/dev/null
    else
        echo -e "  ${YELLOW}⚠${NC} Checksum not available (skipping)"
    fi

    # Install
    chmod +x "${tmp_dir}/opendb"
    if [ -w "${install_dir}" ]; then
        mv "${tmp_dir}/opendb" "${install_dir}/opendb"
    else
        echo -e "  ${DIM}·${NC} Installing to ${install_dir} (requires sudo)..."
        sudo mv "${tmp_dir}/opendb" "${install_dir}/opendb"
    fi
    echo -e "  ${GREEN}✓${NC} OpenDB installed to ${install_dir}/opendb"
}

# ── Verify Installation ─────────────────────────────

verify_install() {
    if ! command -v opendb >/dev/null 2>&1; then
        echo -e "  ${RED}✗ opendb not found in PATH${NC}"
        echo "  Try: export PATH=/usr/local/bin:\$PATH"
        exit 1
    fi
    local ver
    ver=$(opendb --version 2>&1 | head -1)
    echo -e "  ${GREEN}✓${NC} ${ver}"
}

# ── Main ─────────────────────────────────────────────

main() {
    print_banner

    # Step 1: Detect platform
    local platform
    platform=$(detect_platform)
    local os="${platform%%/*}"
    echo -e "${GREEN}✓${NC} Detected: ${os}/${platform##*/}"
    echo ""

    # Select mirror
    local mirror
    echo -e "  ${DIM}·${NC} Selecting mirror..."
    mirror=$(select_mirror)
    local mirror_label="International"
    if [[ "${mirror}" == *"-cn"* ]]; then
        mirror_label="China"
    fi
    echo -e "  ${GREEN}✓${NC} Using ${mirror_label} mirror"

    # Get latest version
    local version
    version=$(get_latest_version "${mirror}")

    # Show install plan
    echo ""
    echo -e "${BOLD}Install plan${NC}"
    echo -e "  OS:         ${os}"
    echo -e "  Arch:       ${platform##*/}"
    echo -e "  Version:    ${version}"
    echo -e "  Install to: /usr/local/bin/opendb"
    echo ""

    # Step 2: Download and install
    echo -e "${BOLD}[1/3] Downloading${NC}"
    download_and_install "${mirror}" "${version}" "${platform}"
    echo ""

    # Step 3: Install
    echo -e "${BOLD}[2/3] Installing${NC}"
    echo -e "  ${GREEN}✓${NC} Binary installed"
    echo ""

    # Step 4: Verify
    echo -e "${BOLD}[3/3] Verifying${NC}"
    verify_install
    echo ""

    echo -e "🚀 ${GREEN}${BOLD}Starting setup wizard...${NC}"
    echo -e "  ${DIM}Run 'opendb --setup' anytime to reconfigure.${NC}"
    echo ""

    # Launch setup wizard
    opendb --setup
}

main "$@"
```

- [ ] **Step 2: Make script executable**

Run: `chmod +x /Users/yingjiewang/opendb/install/install.sh`

- [ ] **Step 3: Verify script syntax**

Run: `bash -n /Users/yingjiewang/opendb/install/install.sh`
Expected: No syntax errors

- [ ] **Step 4: Commit**

```bash
git add install/install.sh
git commit -m "feat: install.sh — one-command installer with platform detection and mirror selection"
```

---

### Task 14: Integration — Full Build and Smoke Test

**Files:**
- Modify: `internal/setup/setup.go` (final wiring)

- [ ] **Step 1: Update RunSetup with driver factory injection**

The `RunSetup` function needs a way to create database drivers for connection testing. Update `setup.go` to accept a driver factory or default to nil (skip connection test in that case):

```go
// Update RunSetup signature:
func RunSetup(baseDir string) error {
	cfg := &SetupConfig{
		BaseDir:  baseDir,
		Sentinel: config.Default().Sentinel,
		Security: config.Default().Security,
		LLM:      config.Default().LLM,
	}

	m := &Model{cfg: cfg}
	m.steps = buildSetupSteps(cfg, nil) // nil driverFunc = skip conn test for now

	p := tea.NewProgram(m, tea.WithAltScreen())
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
```

- [ ] **Step 2: Run full build**

Run: `cd /Users/yingjiewang/opendb && go build ./cmd/opendb/`
Expected: Clean compilation

- [ ] **Step 3: Run all tests**

Run: `cd /Users/yingjiewang/opendb && go test ./internal/setup/ -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: complete setup wizard integration — ready for testing"
```

---

## Task Dependency Graph

```
Task 1 (Foundation)
  └─► Task 2 (Components)
       └─► Task 3 (Welcome)
       └─► Task 4 (Mode)
       └─► Task 5 (DB Type + Permission)
       └─► Task 6 (Conn Form)
       └─► Task 7 (Conn Test)
       └─► Task 8 (Sentinel)
       └─► Task 9 (LLM)
       └─► Task 10 (Rule + Skills)
       └─► Task 11 (Security + Finalize)
            └─► Task 12 (Wire Up)
                 └─► Task 13 (install.sh)
                      └─► Task 14 (Integration)
```

## Notes

- **Existing wizard.go is NOT modified** — the new setup wizard is a complete replacement in `internal/setup/`, leaving the old wizard intact for backward compatibility until we're confident the new one works
- **Connection testing requires driver factories** — in Task 12, the main.go wiring passes driver factories from the product registration. For initial development, connection testing can be skipped (nil driverFunc)
- **Text wording is provisional** — the design spec notes that exact wording for each step will be refined in a separate pass
- **Color scheme is provisional** — will be refined to match OpenClaw's exact style during visual polish
- **Skills list is hardcoded** — could be made dynamic by reading from skill.Registry in a future iteration, but static list is simpler and sufficient for the setup wizard
