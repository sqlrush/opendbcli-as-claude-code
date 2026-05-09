/*-------------------------------------------------------------------------
 *
 * components.go
 *	  SelectItem represents one option in a selection list
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/components.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// stepDoneMsg signals the orchestrator that current step is complete
type stepDoneMsg struct{}

func emitDone() tea.Msg { return stepDoneMsg{} }

// stepBackMsg signals the orchestrator to go back N steps
type stepBackMsg struct {
	n int // how many steps to go back
}

func emitBack(n int) tea.Cmd {
	return func() tea.Msg { return stepBackMsg{n: n} }
}

// ── SelectModel ─────────────────────────────────────────

// SelectItem represents one option in a selection list
type SelectItem struct {
	Label    string
	Value    string
	Desc     string // optional description shown beside label
	IsHeader bool   // true = section header, not selectable
}

// SelectModel is a single-select menu component
type SelectModel struct {
	title string
	items []SelectItem
	cursor int
	done  bool
}

func NewSelectModel(title string, items []SelectItem, defaultValue string) *SelectModel {
	cursor := 0
	for i, item := range items {
		if item.Value == defaultValue && !item.IsHeader {
			cursor = i
			break
		}
	}
	// Ensure cursor doesn't start on a header
	s := &SelectModel{title: title, items: items, cursor: cursor}
	s.skipHeaders(1)
	return s
}

// skipHeaders moves cursor in direction (+1 or -1) to skip header items
func (s *SelectModel) skipHeaders(dir int) {
	for s.cursor >= 0 && s.cursor < len(s.items) && s.items[s.cursor].IsHeader {
		s.cursor += dir
	}
	if s.cursor < 0 {
		s.cursor = 0
		s.skipHeaders(1)
	}
	if s.cursor >= len(s.items) {
		s.cursor = len(s.items) - 1
		s.skipHeaders(-1)
	}
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
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
				s.skipHeaders(-1)
			}
		case "down", "j":
			if s.cursor < len(s.items)-1 {
				s.cursor++
				s.skipHeaders(1)
			}
		case "enter":
			if !s.items[s.cursor].IsHeader {
				s.done = true
				return s, emitDone
			}
		}
	}
	return s, nil
}

func (s *SelectModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + StyleBrand.Render("◆") + "  " + s.title + "\n")
	for i, item := range s.items {
		if item.IsHeader {
			// Section header — highlighted, not selectable
			b.WriteString("  " + StyleDim.Render("│") + "  " + StyleHighlight.Render(item.Label) + "\n")
			continue
		}
		if i == s.cursor {
			label := StyleBrand.Render("│  ● " + item.Label)
			b.WriteString("  " + label)
		} else {
			label := StyleDim.Render("│  ○ " + item.Label)
			b.WriteString("  " + label)
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
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "enter":
			inp.done = true
			return inp, emitDone
		case "backspace":
			if inp.cursorPos > 0 && len(inp.value) > 0 {
				runes := []rune(inp.value)
				runes = append(runes[:inp.cursorPos-1], runes[inp.cursorPos:]...)
				inp.value = string(runes)
				inp.cursorPos--
			}
		case "left":
			if inp.cursorPos > 0 {
				inp.cursorPos--
			}
		case "right":
			if inp.cursorPos < len([]rune(inp.value)) {
				inp.cursorPos++
			}
		default:
			// Handle both single char typing and multi-char paste
			if len(keyMsg.Runes) > 0 {
				existing := []rune(inp.value)
				pasted := keyMsg.Runes
				result := make([]rune, 0, len(existing)+len(pasted))
				result = append(result, existing[:inp.cursorPos]...)
				result = append(result, pasted...)
				result = append(result, existing[inp.cursorPos:]...)
				inp.value = string(result)
				inp.cursorPos += len(pasted)
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
		displayValue = strings.Repeat("•", len([]rune(displayValue)))
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
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "left", "h":
			c.cursor = 0
		case "right", "l":
			c.cursor = 1
		case "enter":
			c.done = true
			return c, emitDone
		}
	}
	return c, nil
}

func (c *ConfirmModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + StyleBrand.Render("◆") + "  " + c.title + "\n")
	yes := StyleDim.Render("○ Yes")
	no := StyleDim.Render("○ No")
	if c.cursor == 0 {
		yes = StyleBrand.Render("● Yes")
	} else {
		no = StyleBrand.Render("● No")
	}
	b.WriteString("  " + StyleDim.Render("│  ") + yes + "  " + no + "\n")
	b.WriteString("  " + StyleDim.Render("└") + "\n")
	return b.String()
}
