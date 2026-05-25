/*-------------------------------------------------------------------------
 *
 * repl.go
 *	  ConnectionInfo holds info about the current database connection
 *	  for display.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"golang.org/x/term"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/dispatch"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/scheduler"
	"github.com/sqlrush/opendb/internal/alert"
	"github.com/sqlrush/opendb/internal/odberr"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui/termwidth"
)

// Colors — matching Claude Code's palette.
var (
	borderColor  = lipgloss.Color("#777777")
	accentColor  = lipgloss.Color("#CC7832")
	titleColor   = lipgloss.Color("#FFFFFF")
	promptColor  = lipgloss.Color("#6C71C4")
	successColor = lipgloss.Color("#859900")
	errorColor   = lipgloss.Color("#DC322F")
	dimColor     = lipgloss.Color("#666666")
	headerColor  = lipgloss.Color("#268BD2")
	textColor    = lipgloss.Color("#93A1A1")
	labelColor   = lipgloss.Color("#888888")
)

// Styles
var (
	borderStyle = lipgloss.NewStyle().Foreground(borderColor)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(titleColor)
	promptStyle = lipgloss.NewStyle().Bold(true).Foreground(promptColor)
	errorStyle  = lipgloss.NewStyle().Foreground(errorColor)
	warnStyle   = lipgloss.NewStyle().Bold(true).Foreground(errorColor) // modification commands
	dimStyle    = lipgloss.NewStyle().Foreground(dimColor)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(headerColor)
	textStyle   = lipgloss.NewStyle().Foreground(textColor)
	labelStyle  = lipgloss.NewStyle().Foreground(labelColor)
)

// ConnectionInfo holds info about the current database connection for display.
type ConnectionInfo struct {
	Name     string
	Host     string
	Version  string
	Instance string
}

// inputHeight is the number of rows reserved for the input area.
// Row layout: top separator + prompt + bottom separator = 3.
const inputHeight = 3

// SentinelAlertSource provides alert channel, auto-start, and stop for sentinel.
// Implemented by each product's SentinelSkill.
type SentinelAlertSource interface {
	AlertCh() <-chan alert.Event
	AutoStart(ctx context.Context) error
	IsRunning() bool
	StopSentinel()
}

// sentinelAlertSource is kept as alias for internal field type.
type sentinelAlertSource = SentinelAlertSource

// cfgProvider provides access to configuration.
type cfgProvider = *config.Config

// modelReloader is the subset of model.Manager the REPL needs for /model
// list / switch / reload / disable. (The in-REPL add wizard used to require
// AddProfile here too — it was removed when adds moved to `<binary>
// configure`.)
type modelReloader interface {
	Reload() (int, error)
	List() []model.ModelProfile
	ActiveName() string
	Switch(name string) (*model.ModelProfile, error)
	Disable()
}

// keyEvent wraps a raw keyboard read result for the async input goroutine.
type keyEvent struct {
	buf []byte
	n   int
	err error
}

// REPL is the main interactive loop (like Claude Code).
type REPL struct {
	dispatcher    *dispatch.Dispatcher
	connMgr       *connection.Manager
	registry      *skill.Registry
	sentinelSkill  sentinelAlertSource  // for alert channel + auto-start
	schedulerSkill schedulerEventSource // for scheduler events + auto-start
	cfg            cfgProvider          // for sentinel/scheduler config
	writer        *bufio.Writer
	connInfo      *ConnectionInfo

	// Terminal dimensions
	rows int
	cols int

	// Content tracking — contentRow is the next row available for content.
	contentRow int
	scrollMode bool

	// Input line editing
	inputBuf  []rune
	cursorPos int

	// Multi-line SQL mode: activated by paste detection when input is classified as SQL.
	sqlMode   bool     // true when buffering multi-line SQL
	sqlBuffer []string // accumulated SQL lines in sqlMode

	// Paste detection: lines buffered during a single read() call with embedded newlines.
	pasteBuffer []string

	// Command history (persistent across sessions)
	history     []string
	historyIdx  int
	historyFile string // path to persistent history file

	// Autocomplete
	completions   []string
	compIdx       int
	compIsArg     bool // true when completions are for arguments (not commands)
	dropdown      *DropdownState // unified dropdown (login/diag/rule), nil when inactive
	dropdownShown    int // number of dropdown rows currently displayed
	drawnTopRow      int // topmost row of last drawn input+dropdown (for cleanup)
	drawnEndRow      int // bottommost row of last drawn input+dropdown
	dropScrollOffset int // how many lines content was scrolled up for dropdown

	// Output buffer for content restoration after dropdown close.
	outputBuffer []string

	// Table browser — last large result available for scrolling.
	pendingTable *format.TableLines

	// Diag async execution
	diagCh          chan DiagProgressEvent // receives progress events from async diag
	diagRunning     bool                   // true while async diag is running
	diagStreaming    bool                   // true while LLM is streaming text
	diagStreamBuf   string                 // line buffer for streaming text
	diagPartialShown bool                  // true when a partial line is displayed at the output row
	diagPartialPrev  string                // previously displayed partial content (for append-mode)
	diagFmt         *diagStreamFormatter   // markdown formatter for streaming lines
	diagSkill       diagAsyncSource        // DiagnoseSkill progress interface
	diagStar        *starAnimator          // blinking star indicator
	diagStarRow     int                    // terminal row of the star line

	// Skill async execution
	skillCh       chan SkillResultEvent   // receives results from async skill goroutine
	skillRunning  bool                   // true while a skill command is running
	skillStar     *starAnimator          // blinking star for skill execution
	skillStarRow  int                    // terminal row of the skill star line

	cmdQueue        []string               // commands queued during async diag/skill

	// Paste preview: when a multi-line paste is detected, the full content is
	// stored here and a compressed preview is shown in inputBuf. On Enter,
	// the full content is expanded in chat and executed.
	pastedContent string

	// Keyboard input channel — shared by REPL main loop and dbtop.
	keyCh chan keyEvent

	// Exit flag
	exitRequested bool
	teardownDone  bool

	// SIGWINCH: terminal resize signal channel.
	sigwinchCh chan os.Signal

	// Cancel function for in-progress async diagnosis.
	diagCancel context.CancelFunc

	// loginPickerBypass / modelPickerBypass: set by picker before calling handleEnter
	// to skip re-entering the picker. Reset after use.
	loginPickerBypass bool
	modelPickerBypass bool
	llmPickerBypass   bool
	rulePickerBypass  bool

	// pickerOriginCmd: when set, the current handleEnter call was triggered by
	// an inline picker. Holds the bare command (e.g., "/model") for display/history.
	// Consumed and cleared on use.
	pickerOriginCmd string

	// Model picker / list support. Picker reads List/ActiveName; /model
	// switch goes through Switch; /model none → Disable; /model reload →
	// Reload. (The in-REPL add wizard was removed in favor of `<binary>
	// configure` — adds and edits live in one place now.)
	modelReloader modelReloader // set via SetModelReloader

	// Per-DB AI skill maps for dynamic resolution on login switch.
	sentinelMap map[string]SentinelAlertSource
	diagMap     map[string]DiagAsyncSource

	// activeInstanceSync is called whenever /login succeeds with the new
	// instance name. Used by main() to keep the shared memory.Store's
	// activeInstance pointing at the connected DB without REPL having to
	// import the memory package.
	activeInstanceSync func(instance string)

	// Alert buffer — ring buffer for /diag and /rule dropdowns.
	alertBuf *alertBuffer

	// Pending events — buffered during async operations or blocking UI
	// to prevent writeOutputLine from corrupting progress animations.
	pendingAlerts []alert.Event
	pendingScheds []scheduler.TaskEvent

	// blockingUI is true while browseTable or a picker is blocking the main loop.
	// Events arriving right after exit are buffered for one select cycle.
	blockingUI bool
}

// DiagAsyncSource is implemented by DiagnoseSkill for progress callbacks.
type DiagAsyncSource interface {
	SetOnProgress(fn func(phase, message string, elapsed time.Duration, result *skill.Result, err error))
	ClearOnProgress()
	HasLLM() bool
}

// diagAsyncSource is kept as alias for internal field type.
type diagAsyncSource = DiagAsyncSource

// NewREPL creates a new REPL with the given dispatcher, connection manager and registry.
func NewREPL(dispatcher *dispatch.Dispatcher, connMgr *connection.Manager, registry *skill.Registry, sentinelSkill sentinelAlertSource, diagSkill diagAsyncSource, schedulerSkill schedulerEventSource, cfg *config.Config) *REPL {
	return &REPL{
		dispatcher:     dispatcher,
		connMgr:        connMgr,
		registry:       registry,
		sentinelSkill:  sentinelSkill,
		diagSkill:      diagSkill,
		schedulerSkill: schedulerSkill,
		cfg:            cfg,
		writer:        bufio.NewWriterSize(os.Stdout, 8192),
		historyIdx:    -1,
		compIdx:       -1,
		alertBuf:      newAlertBuffer(),
	}
}

// Run starts the interactive REPL loop.
func (r *REPL) Run() error {
	// EastAsianWidth=false is set by termwidth.init() — ambiguous-width
	// chars (box-drawing, middle-dot, block elements) are always width 1.

	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}
	r.cols = cols
	r.rows = rows

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("entering raw mode: %w", err)
	}
	defer func() {
		term.Restore(int(os.Stdin.Fd()), oldState)
		if r.teardownDone {
			// teardownScreen already reset scroll region and positioned cursor.
			// Just show cursor — skip scroll region reset which would move cursor to (1,1).
			termShowCursor(os.Stdout)
		} else {
			// Abnormal exit — reset scroll region and reposition cursor to bottom.
			termResetScrollRegion(os.Stdout)
			termMoveToRow(os.Stdout, r.rows)
			termShowCursor(os.Stdout)
		}
	}()

	// Catch SIGTERM/SIGHUP/SIGINT so terminal is restored on pkill/kill.
	// CRITICAL: restore terminal settings FIRST (instant), then cleanup.
	// Cleanup has a 2s timeout to prevent blocking on DB queries (e.g. SIGHUP).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)

	// Listen for SIGWINCH (terminal resize).
	r.sigwinchCh = make(chan os.Signal, 1)
	signal.Notify(r.sigwinchCh, syscall.SIGWINCH)
	defer signal.Stop(r.sigwinchCh)
	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		<-sigCh
		// 1. Restore terminal mode immediately (prevents broken shell).
		term.Restore(int(os.Stdin.Fd()), oldState)
		// 2. Reset scroll region, reposition cursor to bottom, show cursor.
		termResetScrollRegion(os.Stdout)
		termMoveToRow(os.Stdout, r.rows)
		termShowCursor(os.Stdout)
		// 3. Cleanup with timeout — stopSentinel may block on DB query.
		done := make(chan struct{})
		odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
			r.stopScheduler()
			r.stopSentinel()
			close(done)
		})
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		termResetScrollRegion(os.Stdout)
		termWriteAt(os.Stdout, r.rows-2, "  再见!")
		fmt.Fprint(os.Stdout, "\r\n")
		os.Exit(0)
	})
	defer signal.Stop(sigCh)

	// Load persistent command history.
	r.loadHistory()

	// Startup: no screen clear, query cursor, scroll down to make room,
	// then draw welcome with absolute positioning (avoids ANSI+\n blank line bug).
	r.scrollMode = false
	r.printWelcomeInPlace()
	r.drawInputArea()

	// Keyboard reader goroutine — feeds raw bytes into a channel
	// so the main loop can also listen for sentinel alerts via select.
	// Stored as REPL field so dbtop can reuse it (avoid dual stdin readers).
	r.keyCh = make(chan keyEvent, 1)
	keyCh := r.keyCh
	odberr.SafeGo(odberr.ErrPanicGoroutine, func() {
		for {
			buf := make([]byte, 16384) // 16KB — large enough to capture full paste in one read
			n, err := os.Stdin.Read(buf)
			keyCh <- keyEvent{buf: buf[:n], n: n, err: err}
			if err != nil {
				return
			}
		}
	})

	// Star animation ticker — drives the blinking star from the main goroutine
	// to avoid concurrent stdout writes and scroll-position drift.
	starTicker := time.NewTicker(150 * time.Millisecond)
	defer starTicker.Stop()

	// Paste detection timer — when pasteBuffer has accumulated lines,
	// this fires after 150ms of no new input to flush as paste or single enter.
	var pasteTimer <-chan time.Time

	for {
		// Arm paste timer when pasteBuffer has data.
		if len(r.pasteBuffer) > 0 && pasteTimer == nil {
			pasteTimer = time.After(150 * time.Millisecond)
		}
		// Build alert channel — nil if sentinel not running (select ignores nil channels).
		var alertCh <-chan alert.Event
		if r.sentinelSkill != nil {
			alertCh = r.sentinelSkill.AlertCh()
		}

		// Build scheduler event channel — nil if scheduler not running.
		var schedCh <-chan scheduler.TaskEvent
		if r.schedulerSkill != nil {
			schedCh = r.schedulerSkill.EventCh()
		}

		select {
		case ke := <-keyCh:
			if ke.err != nil {
				r.teardownScreen()
				return nil
			}
			// Reset paste timer — more data arriving extends the window.
			if len(r.pasteBuffer) > 0 {
				pasteTimer = nil // will be re-armed at top of next iteration
			}
			r.handleKeyInput(ke.buf, ke.n)
			r.writer.Flush()
			if r.exitRequested {
				r.teardownScreen()
				return nil
			}
			// After blocking UI (browseTable/picker), drain any events that
			// arrived in channels during the block, then render them cleanly.
			if r.blockingUI {
				r.blockingUI = false
				r.flushAfterBlock()
				r.writer.Flush()
			}
			continue

		case a := <-alertCh:
			if e := odberr.Guard(odberr.ErrUISkillRender, func() {
				if r.diagRunning || r.skillRunning || r.blockingUI {
					r.bufferAlert(a)
				} else {
					r.renderAlert(a)
				}
			}); e != nil {
				r.writeOutputLine("  " + e.Display())
			}
			r.writer.Flush()
			continue

		case ev := <-schedCh:
			if e := odberr.Guard(odberr.ErrUISkillRender, func() {
				if r.diagRunning || r.skillRunning || r.blockingUI {
					r.bufferSchedulerEvent(ev)
				} else {
					r.renderSchedulerEvent(ev)
				}
			}); e != nil {
				r.writeOutputLine("  " + e.Display())
			}
			r.writer.Flush()
			continue

		case prog := <-r.diagCh:
			if e := odberr.Guard(odberr.ErrUIDiagRender, func() {
				r.renderDiagProgress(prog)
			}); e != nil {
				r.writeOutputLine("  " + e.Display())
			}
			r.writer.Flush()
			if prog.Phase == DiagPhaseDone || prog.Phase == DiagPhaseError {
				r.diagRunning = false
				r.diagCh = nil
				r.diagCancel = nil
				r.flushPendingEvents()
				r.processCmdQueue()
			}
			continue

		case ev := <-r.skillCh:
			if e := odberr.Guard(odberr.ErrUISkillRender, func() {
				r.renderSkillResult(ev)
			}); e != nil {
				r.writeOutputLine("  " + e.Display())
			}
			r.writer.Flush()
			continue

		case <-starTicker.C:
			if r.diagStar != nil {
				r.diagStar.tick()
			}
			if r.skillStar != nil {
				r.skillStar.tick()
			}
			r.writer.Flush()
			continue

		case <-r.sigwinchCh:
			if e := odberr.Guard(odberr.ErrUIResize, func() {
				r.handleResize()
			}); e != nil {
				r.writeOutputLine("  " + e.Display())
			}
			r.writer.Flush()
			continue

		case <-pasteTimer:
			pasteTimer = nil
			if len(r.pasteBuffer) > 0 {
				// If inputBuf has residual text (incomplete line from paste),
				// append it to the last pasteBuffer entry.
				if len(r.inputBuf) > 0 {
					last := len(r.pasteBuffer) - 1
					r.pasteBuffer[last] = r.pasteBuffer[last] + string(r.inputBuf)
					r.inputBuf = nil
					r.cursorPos = 0
				}
				lines := r.pasteBuffer
				r.pasteBuffer = nil
				if len(lines) == 1 {
					// Single line — set as input, user presses Enter to execute.
					r.inputBuf = []rune(lines[0])
					r.cursorPos = len(r.inputBuf)
					r.drawInputArea()
				} else {
					// Multi-line paste — store full content, show compressed preview.
					// User sees preview in input line, presses Enter to expand + execute.
					joined := strings.Join(lines, "\n")
					r.pastedContent = joined
					firstLine := lines[0]
					if len(firstLine) > 40 {
						firstLine = firstLine[:40] + "..."
					}
					preview := fmt.Sprintf("[Pasted %s + %d lines]", firstLine, len(lines)-1)
					r.inputBuf = []rune(preview)
					r.cursorPos = len(r.inputBuf)
					r.drawInputArea()
				}
				r.writer.Flush()
			}
			continue
		}
	}
}

// handleKeyInput processes raw keyboard bytes from the async reader.
func (r *REPL) handleKeyInput(buf []byte, n int) {
	for i := 0; i < n; {
		b := buf[i]

		switch {
		case b == 3: // Ctrl+C
			// If in SQL multi-line mode, cancel the buffer and return to normal.
			if r.sqlMode {
				r.sqlMode = false
				r.sqlBuffer = nil
				r.inputBuf = nil
				r.cursorPos = 0
				r.writeOutputLine(dimStyle.Render("  SQL 输入已取消"))
				r.drawInputArea()
				i++
				continue
			}
			// If diagnosis is running, cancel it instead of exiting.
			if r.diagRunning && r.diagCancel != nil {
				r.diagCancel()
				r.diagCancel = nil
				return
			}
			r.exitRequested = true
			return

		case b == 4: // Ctrl+D
			if len(r.inputBuf) == 0 {
				r.exitRequested = true
				return
			}
			i++

		case b == 9: // Tab — accept completion
			if len(r.completions) > 0 {
				idx := r.compIdx
				if idx < 0 {
					idx = 0
				}
				comp := r.completions[idx]
				if r.compIsArg {
					// Argument completion: keep command, replace arg.
					// For login dropdown, extract connection name (first word before space).
					connName := comp
					if spIdx := strings.Index(comp, "  ("); spIdx > 0 {
						connName = comp[:spIdx]
					}
					input := string(r.inputBuf)
					spaceIdx := strings.IndexByte(input, ' ')
					if spaceIdx < 0 {
						// No space yet (e.g., "/login" without trailing space).
						r.inputBuf = []rune(input + " " + connName)
					} else {
						r.inputBuf = []rune(input[:spaceIdx+1] + connName)
					}
				} else {
					// Command completion.
					r.inputBuf = []rune("/" + comp + " ")
				}
				r.cursorPos = len(r.inputBuf)
				r.completions = nil
				r.compIdx = -1
				r.compIsArg = false
				r.drawInputArea()
			}
			i++

		case b == 13 || b == 10: // Enter
			// Unified dropdown: if active with a selection, execute selection.
			if r.dropdown != nil && r.dropdown.IsActive() {
				sel := r.dropdown.SelectedItem()
				kind := r.dropdown.Kind
				r.dropdown = nil
				r.completions = nil
				r.compIdx = -1
				if sel != nil {
					r.inputBuf = nil
					r.cursorPos = 0
					r.drawInputArea()
					r.handleDropdownSelect(kind, sel)
				} else {
					// No selection: execute bare command (show full list).
					input := string(r.inputBuf)
					r.inputBuf = nil
					r.cursorPos = 0
					r.drawInputArea()
					r.handleEnter(input)
				}
				if r.exitRequested {
					return
				}
				i++
				continue
			}
			// If pasted content is pending, Enter submits the full paste.
			if r.pastedContent != "" {
				content := r.pastedContent
				r.pastedContent = ""
				r.inputBuf = nil
				r.cursorPos = 0
				r.completions = nil
				r.compIdx = -1
				lines := strings.Split(content, "\n")
				r.drawInputArea()
				r.handlePaste(lines)
				if r.exitRequested {
					return
				}
				i++
				continue
			}

			input := string(r.inputBuf)
			r.inputBuf = nil
			r.cursorPos = 0
			r.completions = nil
			r.compIdx = -1

			// Paste detection:
			// - Same read buffer has more bytes after newline → definitely paste
			// - pasteBuffer already accumulating (cross-read paste) → continue accumulating
			// - Otherwise → normal single-line Enter, execute immediately
			if i+1 < n {
				// More bytes in this read — definitely paste, keep accumulating.
				r.pasteBuffer = append(r.pasteBuffer, input)
				i++
				continue
			}
			if len(r.pasteBuffer) > 0 {
				// Cross-read continuation of an ongoing paste.
				r.pasteBuffer = append(r.pasteBuffer, input)
				// Timer will flush after 150ms of no more data.
				i++
				continue
			}
			// Normal single Enter — execute immediately.
			r.drawInputArea()
			r.handleEnter(input)
			if r.exitRequested {
				return
			}
			i++

		case b == 127 || b == 8: // Backspace
			if r.cursorPos > 0 {
				input := string(r.inputBuf[:r.cursorPos])
				// If cursor is at end of a completed slash command (e.g., "/health " or "/health"),
				// delete the entire command back to "/" in one shot.
				if len(r.inputBuf) == r.cursorPos && len(input) > 1 && input[0] == '/' {
					trimmed := strings.TrimRight(input, " ")
					if !strings.ContainsRune(trimmed[1:], ' ') {
						// Single command word — clear to just "/"
						r.inputBuf = []rune("/")
						r.cursorPos = 1
						r.drawInputArea()
						i++
						continue
					}
				}
				r.inputBuf = append(r.inputBuf[:r.cursorPos-1], r.inputBuf[r.cursorPos:]...)
				r.cursorPos--
				r.drawInputArea()
			}
			i++

		case b == 21: // Ctrl+U
			r.inputBuf = nil
			r.cursorPos = 0
			r.drawInputArea()
			i++

		case b == 23: // Ctrl+W
			if r.cursorPos > 0 {
				end := r.cursorPos
				for r.cursorPos > 0 && r.inputBuf[r.cursorPos-1] == ' ' {
					r.cursorPos--
				}
				for r.cursorPos > 0 && r.inputBuf[r.cursorPos-1] != ' ' {
					r.cursorPos--
				}
				r.inputBuf = append(r.inputBuf[:r.cursorPos], r.inputBuf[end:]...)
				r.drawInputArea()
			}
			i++

		case b == 1: // Ctrl+A
			r.cursorPos = 0
			r.drawInputArea()
			i++

		case b == 5: // Ctrl+E
			r.cursorPos = len(r.inputBuf)
			r.drawInputArea()
			i++

		case b == 27: // ESC
			i++
			if i >= n || buf[i] != '[' {
				if r.dropdown != nil {
					r.dropdown = nil
					r.drawInputArea()
				} else if len(r.completions) > 0 {
					r.completions = nil
					r.compIdx = -1
					r.drawInputArea()
				}
				continue
			}
			if buf[i] == '[' {
				i++
				if i < n {
					switch buf[i] {
					case 'A': // Up
						if r.dropdown != nil && r.dropdown.IsActive() {
							r.dropdown.MoveUp()
							r.drawInputArea()
						} else if len(r.completions) > 1 {
							if r.compIdx <= 0 {
								r.compIdx = len(r.completions) - 1
							} else {
								r.compIdx--
							}
							r.inputBuf = r.buildCompletionInput(r.compIdx)
							r.cursorPos = len(r.inputBuf)
							r.drawInputArea()
						} else if filtered := r.filteredHistory(); len(filtered) > 0 {
							if r.historyIdx < 0 {
								r.historyIdx = len(filtered) - 1
							} else if r.historyIdx > 0 {
								r.historyIdx--
							}
							r.inputBuf = []rune(filtered[r.historyIdx])
							r.cursorPos = len(r.inputBuf)
							r.drawInputArea()
						}
						i++
					case 'B': // Down
						if r.dropdown != nil && r.dropdown.IsActive() {
							r.dropdown.MoveDown()
							r.drawInputArea()
						} else if len(r.completions) > 1 {
							if r.compIdx < 0 || r.compIdx >= len(r.completions)-1 {
								r.compIdx = 0
							} else {
								r.compIdx++
							}
							r.inputBuf = r.buildCompletionInput(r.compIdx)
							r.cursorPos = len(r.inputBuf)
							r.drawInputArea()
						} else if r.historyIdx >= 0 {
							r.historyIdx++
							filtered := r.filteredHistory()
							if r.historyIdx >= len(filtered) {
								r.historyIdx = -1
								r.inputBuf = nil
								r.cursorPos = 0
							} else {
								r.inputBuf = []rune(filtered[r.historyIdx])
								r.cursorPos = len(r.inputBuf)
							}
							r.drawInputArea()
						}
						i++
					case 'C': // Right
						if r.cursorPos < len(r.inputBuf) {
							r.cursorPos++
							r.drawInputArea()
						}
						i++
					case 'D': // Left
						if r.cursorPos > 0 {
							r.cursorPos--
							r.drawInputArea()
						}
						i++
					case 'H': // Home
						r.cursorPos = 0
						r.drawInputArea()
						i++
					case 'F': // End
						r.cursorPos = len(r.inputBuf)
						r.drawInputArea()
						i++
					case '3': // Delete
						if i+1 < n && buf[i+1] == '~' {
							if r.cursorPos < len(r.inputBuf) {
								r.inputBuf = append(r.inputBuf[:r.cursorPos], r.inputBuf[r.cursorPos+1:]...)
								r.drawInputArea()
							}
							i += 2
						} else {
							i++
						}
					default:
						i++
					}
				}
			}

		case b == 12: // Ctrl+L
			r.resetScreen()
			i++

		case b == 'f', b == 'F': // Browse table shortcut
			if len(r.inputBuf) == 0 && r.pendingTable != nil {
				r.blockingUI = true
				r.browseTable(r.pendingTable)
				r.pendingTable = nil
				i++
				continue
			}
			// Fall through to printable handler.
			remaining := buf[i:n]
			ru, size := utf8.DecodeRune(remaining)
			if ru != utf8.RuneError || size > 1 {
				newBuf := make([]rune, len(r.inputBuf)+1)
				copy(newBuf, r.inputBuf[:r.cursorPos])
				newBuf[r.cursorPos] = ru
				copy(newBuf[r.cursorPos+1:], r.inputBuf[r.cursorPos:])
				r.inputBuf = newBuf
				r.cursorPos++
				if len(r.pasteBuffer) == 0 {
					r.drawInputArea()
				}
			}
			i += size

		case b >= 32: // Printable
			remaining := buf[i:n]
			ru, size := utf8.DecodeRune(remaining)
			if ru != utf8.RuneError || size > 1 {
				newBuf := make([]rune, len(r.inputBuf)+1)
				copy(newBuf, r.inputBuf[:r.cursorPos])
				newBuf[r.cursorPos] = ru
				copy(newBuf[r.cursorPos+1:], r.inputBuf[r.cursorPos:])
				r.inputBuf = newBuf
				r.cursorPos++
				if len(r.pasteBuffer) == 0 {
					r.drawInputArea()
				}
			}
			i += size

		default:
			i++
		}
	}
}

// ── Screen Management ─────────────────────────────────────────

// queryCursorRow sends CSI 6n and reads the cursor position response.
// Must be called in raw mode. Returns the current row (1-based).
func (r *REPL) queryCursorRow() int {
	fmt.Fprint(r.writer, "\033[6n")
	r.writer.Flush()
	var resp []byte
	b := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(b)
		if err != nil {
			return 1
		}
		resp = append(resp, b[0])
		if b[0] == 'R' {
			break
		}
		if len(resp) > 32 {
			break
		}
	}
	// Parse \033[row;colR
	s := string(resp)
	start := strings.Index(s, "[")
	semi := strings.Index(s, ";")
	if start >= 0 && semi > start {
		row, err := strconv.Atoi(s[start+1 : semi])
		if err == nil {
			return row
		}
	}
	return 1
}

// ── Persistent History ────────────────────────────────────────

const maxHistoryLines = 500

// loadHistory reads command history from ~/.opendb/history/commands.
func (r *REPL) loadHistory() {
	if r.cfg == nil || r.cfg.Session.HistoryDir == "" {
		return
	}
	r.historyFile = filepath.Join(r.cfg.Session.HistoryDir, "commands")

	f, err := os.Open(r.historyFile)
	if err != nil {
		return // no history file yet
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			r.history = append(r.history, line)
		}
	}
	// Keep only the last maxHistoryLines entries.
	if len(r.history) > maxHistoryLines {
		r.history = r.history[len(r.history)-maxHistoryLines:]
	}
}

// saveHistoryEntry appends a single command to the persistent history file.
func (r *REPL) saveHistoryEntry(cmd string) {
	if r.historyFile == "" || cmd == "" {
		return
	}
	// Ensure directory exists.
	os.MkdirAll(filepath.Dir(r.historyFile), 0755)

	f, err := os.OpenFile(r.historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, cmd)
}

// filteredHistory returns the history slice appropriate for the current state.
// Before login, only pre-login commands (/login, /help, /exit, /clear, /config, /conn) are returned.
// After login, the full history is returned.
func (r *REPL) filteredHistory() []string {
	if r.connInfo != nil {
		return r.history
	}
	// Pre-login: filter to only pre-login commands, deduplicated (most recent wins).
	seen := make(map[string]bool)
	var filtered []string
	// Iterate in reverse to keep most recent occurrence of each command.
	for i := len(r.history) - 1; i >= 0; i-- {
		cmd := r.history[i]
		trimmed := strings.TrimSpace(cmd)
		if !strings.HasPrefix(trimmed, "/") {
			continue
		}
		name := trimmed[1:]
		if spaceIdx := strings.IndexByte(name, ' '); spaceIdx > 0 {
			name = name[:spaceIdx]
		}
		if !preLoginCommands[strings.ToLower(name)] {
			continue
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		filtered = append(filtered, cmd)
	}
	// Reverse to restore chronological order (oldest first, newest last).
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}
	return filtered
}

// teardownScreen resets the terminal to normal state without clearing.
// All chat history stays visible on screen.
func (r *REPL) teardownScreen() {
	// Stop background services before exit to avoid goroutine leak.
	r.stopScheduler()
	r.stopSentinel()
	// Reset scroll region BEFORE clearing — prevents shell from inheriting
	// a restricted scroll region that corrupts all subsequent output.
	// NOTE: termResetScrollRegion moves cursor to (1,1), so we must reposition after.
	termResetScrollRegion(r.writer)
	// Clear the input area rows to remove stale prompt/separator artifacts.
	topSep, _, botSep := r.inputPosition()
	for row := topSep; row <= botSep; row++ {
		termClearRow(r.writer, row)
	}
	// Print goodbye right after the last content line (no extra blank lines).
	goodbyeRow := topSep
	if r.scrollMode {
		goodbyeRow = r.rows - 2
	}
	termWriteAt(r.writer, goodbyeRow, "  再见!")
	fmt.Fprint(r.writer, "\r\n")
	r.writer.Flush()
	// Mark teardown complete so the deferred restore skips redundant \033[r].
	r.teardownDone = true
}

// handleResize is called when SIGWINCH is received (terminal resized).
// It updates rows/cols, resets scroll state, and redraws the screen.
func (r *REPL) handleResize() {
	newCols, newRows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return // can't get size, ignore
	}
	if newCols == r.cols && newRows == r.rows {
		return // no actual change
	}

	r.cols = newCols
	r.rows = newRows

	// Reset scroll region to prevent stale region from previous size.
	termResetScrollRegion(r.writer)

	// Rebuild the screen: clear and redraw from outputBuffer.
	termClearScreen(r.writer)
	r.contentRow = 1
	r.scrollMode = false
	r.dropScrollOffset = 0
	r.drawnTopRow = 0
	r.drawnEndRow = 0
	r.dropdownShown = 0

	// Replay recent output from buffer.
	maxRow := r.rows - inputHeight
	start := 0
	if len(r.outputBuffer) > maxRow {
		start = len(r.outputBuffer) - maxRow
	}
	for _, line := range r.outputBuffer[start:] {
		// Re-wrap lines to new width.
		for _, wl := range wrapLineToWidth(line, r.cols-1) {
			if r.contentRow > maxRow {
				break
			}
			termWriteAt(r.writer, r.contentRow, wl)
			r.contentRow++
		}
	}

	// If content filled the screen, enter scroll mode.
	if r.contentRow > maxRow {
		r.scrollMode = true
		r.contentRow = maxRow
		termSetScrollRegion(r.writer, 1, maxRow)
	}

	r.drawInputArea()
}

// resetScreen clears everything and re-renders the welcome page.
// Used by /clear and Ctrl+L.
func (r *REPL) resetScreen() {
	termResetScrollRegion(r.writer)
	termClearScreen(r.writer)
	r.contentRow = 1
	r.scrollMode = false
	r.dropScrollOffset = 0
	r.printWelcomePositioned()
	r.drawInputArea()
}

// inputPosition returns the three rows of the input area.
func (r *REPL) inputPosition() (topSep, promptRow, botSep int) {
	if r.scrollMode {
		return r.rows - 2, r.rows - 1, r.rows
	}
	return r.contentRow, r.contentRow + 1, r.contentRow + 2
}

// maxContentRow returns the last row that can hold content (before input).
func (r *REPL) maxContentRow() int {
	return r.rows - inputHeight
}

// ── Output Writing ────────────────────────────────────────────

func (r *REPL) writeOutputLine(line string) {
	maxRow := r.maxContentRow()

	// Buffer output for content restoration after dropdown overlay.
	r.outputBuffer = append(r.outputBuffer, line)
	if maxBuf := maxRow * 2; maxBuf > 0 && len(r.outputBuffer) > maxBuf {
		r.outputBuffer = r.outputBuffer[len(r.outputBuffer)-maxRow:]
	}

	// If writing over old input area rows, clear the remaining input rows
	// (prompt + bottom separator) that won't be overwritten by this write,
	// then mark them as already cleared so clearPreviousDrawing won't run.
	if !r.scrollMode && r.drawnTopRow > 0 && r.contentRow >= r.drawnTopRow {
		// Clear rows above contentRow that belong to the old input area
		// (already being overwritten by new content, no action needed)
		// but clear rows BELOW contentRow that still hold old prompt/separator.
		for row := r.contentRow + 1; row <= r.drawnEndRow; row++ {
			if row >= 1 && row <= r.rows {
				termClearRow(r.writer, row)
			}
		}
		r.drawnTopRow = 0
		r.drawnEndRow = 0
		r.dropdownShown = 0
	}

	if !r.scrollMode && r.contentRow > maxRow {
		r.scrollMode = true
		termSetScrollRegion(r.writer, 1, maxRow)
	}

	if r.scrollMode {
		termMoveToRow(r.writer, maxRow)
		fmt.Fprint(r.writer, "\r\n")
		termClearLine(r.writer)
		fmt.Fprint(r.writer, line)
		// Scroll shifts all rows up by 1 — keep star rows in sync.
		if r.diagStarRow > 0 {
			r.diagStarRow--
			if r.diagStar != nil {
				r.diagStar.row = r.diagStarRow
			}
		}
		if r.skillStarRow > 0 {
			r.skillStarRow--
			if r.skillStar != nil {
				r.skillStar.row = r.skillStarRow
			}
		}
	} else {
		termWriteAt(r.writer, r.contentRow, line)
		r.contentRow++
	}
}

// ── Input Area Drawing ────────────────────────────────────────

func (r *REPL) drawInputArea() {
	baseTop, _, baseBotSep := r.inputPosition()
	sepLine := borderStyle.Render(strings.Repeat("─", r.cols))

	// Autocomplete state — contextual dropdown takes priority.
	input := string(r.inputBuf)
	// Try contextual dropdown (login/diag/rule) on every redraw.
	if r.tryShowContextDropdown() {
		// Contextual dropdown active — skip command completions.
	} else {
		// No contextual dropdown — clear if one was showing, update completions.
		if r.dropdown != nil {
			r.dropdown = nil
		}
		if r.compIdx < 0 || !strings.HasPrefix(input, "/") {
			r.updateCompletions()
		}
	}

	// Compute dropdown count — unified dropdown takes priority over completions.
	dropCount := 0
	if r.dropdown != nil && r.dropdown.IsActive() {
		dropCount = r.dropdown.VisibleCount()
	} else if len(r.completions) > 1 {
		dropCount = len(r.completions)
		if dropCount > maxDropdownVisible {
			dropCount = maxDropdownVisible
		}
	}

	// ── Step 1: Clear entire previous drawing (input + dropdown) ──
	r.clearPreviousDrawing()

	// Auto-enter scroll mode when dropdown needs more space than available below input.
	if !r.scrollMode && dropCount > 0 && !r.compIsArg && r.contentRow < r.maxContentRow() {
		avail := r.rows - baseBotSep
		if avail < dropCount {
			r.scrollMode = true
			maxRow := r.maxContentRow()
			termSetScrollRegion(r.writer, 1, maxRow)
			baseTop, _, baseBotSep = r.inputPosition()
		}
	}

	// ── Step 2: Compute actual positions ──
	topSep, promptRow, botSep := baseTop, baseTop+1, baseBotSep

	if r.scrollMode && dropCount > 0 && !r.compIsArg {
		// Use the max of current dropCount and previous scroll offset so
		// the input never moves DOWN when dropdown shrinks.
		effectiveDrop := dropCount
		if r.dropScrollOffset > effectiveDrop {
			effectiveDrop = r.dropScrollOffset
		}
		topSep = baseTop - effectiveDrop
		promptRow = topSep + 1
		botSep = topSep + 2
		// Clamp: if topSep < 1, reduce both counts.
		if topSep < 1 {
			effectiveDrop = effectiveDrop + topSep - 1
			if effectiveDrop < 1 {
				dropCount = 0
				topSep, promptRow, botSep = r.inputPosition()
			} else {
				if dropCount > effectiveDrop {
					dropCount = effectiveDrop
				}
				topSep = 1
				promptRow = 2
				botSep = 3
			}
		}
	} else if dropCount > 0 && (!r.scrollMode || r.compIsArg) {
		// Cap dropdown to available space below.
		avail := r.rows - botSep
		if avail < dropCount {
			dropCount = avail
		}
		if dropCount < 1 {
			dropCount = 0
		}
	}

	// ── Step 2.5: Permanent scroll for dropdown (Claude Code style) ──
	// When dropdown appears or grows, scroll content up permanently using \r\n.
	// When dropdown closes, content stays scrolled — new output will fill the gap.
	if r.scrollMode {
		if dropCount > 0 && !r.compIsArg && dropCount > r.dropScrollOffset {
			scrollNeeded := dropCount - r.dropScrollOffset
			maxRow := r.maxContentRow()
			termSetScrollRegion(r.writer, 1, maxRow)
			for i := 0; i < scrollNeeded; i++ {
				termMoveToRow(r.writer, maxRow)
				fmt.Fprint(r.writer, "\r\n")
				termClearLine(r.writer)
			}
			r.dropScrollOffset = dropCount
			if r.diagStarRow > 0 {
				r.diagStarRow -= scrollNeeded
				if r.diagStar != nil {
					r.diagStar.row = r.diagStarRow
				}
			}
			if r.skillStarRow > 0 {
				r.skillStarRow -= scrollNeeded
				if r.skillStar != nil {
					r.skillStar.row = r.skillStarRow
				}
			}
		} else if dropCount == 0 && r.dropScrollOffset > 0 {
			// Dropdown closed: exit scroll mode so input stays near content.
			// New output will gradually push input back to the bottom.
			maxRow := r.maxContentRow()
			r.contentRow = maxRow - r.dropScrollOffset + 1
			r.scrollMode = false
			termResetScrollRegion(r.writer)
			r.dropScrollOffset = 0
			// Recompute positions since we changed scrollMode.
			topSep, promptRow, botSep = r.inputPosition()
		}
	}

	// ── Step 3: Adjust scroll region when needed ──
	if r.scrollMode {
		newMax := topSep - 1
		if newMax < 1 {
			newMax = 1
		}
		termSetScrollRegion(r.writer, 1, newMax)
	}

	// ── Step 4: Draw input area ──
	termWriteAt(r.writer, topSep, sepLine)
	prompt := r.buildPrompt()
	if r.sqlMode {
		prompt = "  " + dimStyle.Render("...") + " "
	}
	inputStr := string(r.inputBuf)
	termClearRow(r.writer, promptRow)
	fmt.Fprintf(r.writer, "%s%s", prompt, inputStr)
	termWriteAt(r.writer, botSep, sepLine)

	// ── Step 5: Draw dropdown always below botSep ──
	if dropCount > 0 {
		if r.dropdown != nil && r.dropdown.IsActive() {
			// Unified dropdown (login/diag/rule).
			r.renderUnifiedDropdown(botSep+1, dropCount)
		} else {
			// Command/arg completion dropdown.
			r.renderCompletionDropdown(botSep+1, dropCount)
		}
	}

	// Save state for next cleanup.
	r.dropdownShown = dropCount
	r.drawnTopRow = topSep
	endRow := botSep + dropCount
	if endRow > r.rows {
		endRow = r.rows
	}
	r.drawnEndRow = endRow

	// ── Step 6: Ghost text ──
	if len(r.completions) > 0 {
		ghost := r.buildCompletionGhost()
		if ghost != "" {
			termMoveTo(r.writer, promptRow, visibleWidth(prompt)+displayWidth(inputStr)+1)
			fmt.Fprint(r.writer, dimStyle.Render(ghost))
		}
	}

	// ── Step 7: Position cursor ──
	promptVisW := visibleWidth(prompt)
	inputBeforeCursor := string(r.inputBuf[:r.cursorPos])
	cursorCol := promptVisW + displayWidth(inputBeforeCursor) + 1
	termMoveTo(r.writer, promptRow, cursorCol)

	// Flush buffered output in one TCP packet (critical for SSH latency).
	r.writer.Flush()
}

// clearPreviousDrawing erases all rows from the last drawInputArea call.
// Content restoration when dropdown closes is handled by drawInputArea's
// scroll logic (Step 2.5), not here.
func (r *REPL) clearPreviousDrawing() {
	if r.drawnTopRow <= 0 {
		return
	}

	// Clear the entire old drawing area.
	for row := r.drawnTopRow; row <= r.drawnEndRow; row++ {
		if row >= 1 && row <= r.rows {
			termClearRow(r.writer, row)
		}
	}

	r.drawnTopRow = 0
	r.drawnEndRow = 0
	r.dropdownShown = 0
}

// renderCompletionDropdown draws the command/arg completion dropdown.
func (r *REPL) renderCompletionDropdown(startRow, count int) {
	// Standard completion dropdown (also handles login items).
	offset := 0
	if len(r.completions) > maxDropdownVisible && r.compIdx >= 0 {
		if r.compIdx >= offset+maxDropdownVisible {
			offset = r.compIdx - maxDropdownVisible + 1
		}
		if r.compIdx < offset {
			offset = r.compIdx
		}
	}

	for i := 0; i < count; i++ {
		idx := offset + i
		if idx >= len(r.completions) {
			break
		}
		row := startRow + i
		if row > r.rows {
			break
		}
		termClearRow(r.writer, row)
		var label string
		if r.compIsArg {
			label = r.completions[idx]
		} else {
			label = "/" + r.completions[idx]
		}
		if idx == r.compIdx {
			fmt.Fprintf(r.writer, "  %s", accentStyle.Render(label))
		} else {
			fmt.Fprintf(r.writer, "  %s", dimStyle.Render(label))
		}
	}
	// Scroll indicators.
	if len(r.completions) > maxDropdownVisible {
		if offset > 0 {
			termMoveTo(r.writer, startRow, r.cols-1)
			fmt.Fprint(r.writer, dimStyle.Render("▲"))
		}
		if offset+count < len(r.completions) {
			termMoveTo(r.writer, startRow+count-1, r.cols-1)
			fmt.Fprint(r.writer, dimStyle.Render("▼"))
		}
	}
}

// renderLoginDropdown draws the /login connection dropdown with fixed header + scrollable data.
// Layout: row 0 = header (dim), rows 1-5 = data (max 5 visible, scrollable).
func (r *REPL) renderLoginDropdown(startRow, count int) {
	if len(r.completions) < 2 {
		return
	}

	// Row 0: always show header (completions[0]).
	headerRow := startRow
	if headerRow <= r.rows {
		termClearRow(r.writer, headerRow)
		fmt.Fprintf(r.writer, "  %s", dimStyle.Render(r.completions[0]))
		// Separator line.
		if headerRow+1 <= r.rows {
			termClearRow(r.writer, headerRow+1)
			fmt.Fprintf(r.writer, "  %s", dimStyle.Render(strings.Repeat("─", 60)))
		}
	}

	// Data rows: completions[1:], scrollable, max 5-1=4 visible after header+separator.
	dataStart := startRow + 2 // after header + separator
	maxDataVisible := count - 2
	if maxDataVisible < 1 {
		maxDataVisible = 1
	}
	dataItems := r.completions[1:]

	// Compute scroll offset to keep selected item visible.
	dataIdx := r.compIdx - 1 // compIdx is 1-based (0=header), dataIdx is 0-based
	if dataIdx < 0 {
		dataIdx = 0
	}
	offset := 0
	if len(dataItems) > maxDataVisible {
		if dataIdx >= offset+maxDataVisible {
			offset = dataIdx - maxDataVisible + 1
		}
		if dataIdx < offset {
			offset = dataIdx
		}
	}

	for i := 0; i < maxDataVisible; i++ {
		idx := offset + i
		if idx >= len(dataItems) {
			break
		}
		row := dataStart + i
		if row > r.rows {
			break
		}
		termClearRow(r.writer, row)
		if idx == dataIdx {
			fmt.Fprintf(r.writer, "  %s", accentStyle.Render("▸ "+dataItems[idx]))
		} else {
			fmt.Fprintf(r.writer, "    %s", dataItems[idx])
		}
	}

	// Scroll indicators.
	if len(dataItems) > maxDataVisible {
		if offset > 0 {
			termMoveTo(r.writer, dataStart, r.cols-1)
			fmt.Fprint(r.writer, dimStyle.Render("▲"))
		}
		if offset+maxDataVisible < len(dataItems) {
			termMoveTo(r.writer, dataStart+maxDataVisible-1, r.cols-1)
			fmt.Fprint(r.writer, dimStyle.Render("▼"))
		}
	}
}

// renderUnifiedDropdown draws a DropdownState-based dropdown (login/diag/rule).
func (r *REPL) renderUnifiedDropdown(startRow, count int) {
	if r.dropdown == nil {
		return
	}
	items, startIdx := r.dropdown.VisibleSlice()
	showUp, showDown := r.dropdown.ScrollIndicators()

	for i, item := range items {
		if i >= count {
			break
		}
		row := startRow + i
		if row > r.rows {
			break
		}
		termClearRow(r.writer, row)
		if startIdx+i == r.dropdown.SelectedIdx {
			fmt.Fprintf(r.writer, "  %s", accentStyle.Render("▸ "+item.Label))
		} else {
			fmt.Fprintf(r.writer, "    %s", item.Label)
		}
	}
	if showUp {
		termMoveTo(r.writer, startRow, r.cols-1)
		fmt.Fprint(r.writer, dimStyle.Render("▲"))
	}
	if showDown {
		termMoveTo(r.writer, startRow+count-1, r.cols-1)
		fmt.Fprint(r.writer, dimStyle.Render("▼"))
	}
}

// handleDropdownSelect dispatches Enter on a selected dropdown item.
func (r *REPL) handleDropdownSelect(kind DropdownKind, item *DropdownItem) {
	switch kind {
	case KindLogin:
		r.handleEnter("/login " + item.Value)
	case KindDiag:
		r.handleEnter("/llm " + item.Value)
	case KindRule:
		r.handleEnter("/rule " + item.Value)
	}
}

// tryShowContextDropdown checks if the current input should trigger a
// contextual dropdown (login/diag/rule). Returns true if a dropdown was shown.
func (r *REPL) tryShowContextDropdown() bool {
	input := strings.TrimSpace(string(r.inputBuf))

	switch input {
	case "/login":
		return r.showLoginDropdown()
	case "/llm":
		return false // picker runs on Enter via runLLMPicker
	case "/rule":
		return false // picker runs on Enter via runRulePicker
	}
	return false
}

// showLoginDropdown populates the standard completions with aligned connection details.
// If already active, returns true without rebuilding (preserves Up/Down selection).
// First entry is a header row (rendered differently), followed by data rows.
func (r *REPL) showLoginDropdown() bool {
	return false // disabled: picker runs on Enter via runLoginPicker
}

// runLoginPicker shows an inline interactive connection picker below current content.
func (r *REPL) runLoginPicker() {
	conns := r.connMgr.ListConnections()
	if len(conns) == 0 {
		r.writeOutputLine(dimStyle.Render("  暂无已保存的连接"))
		r.drawInputArea()
		return
	}

	// Build aligned labels using display-width-aware padding.
	nameW, typeW, addrW := 4, 4, 4
	for _, c := range conns {
		dbType := c.DBType
		if dbType == "" { dbType = "oracle" }
		addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
		if displayWidth(c.Name) > nameW { nameW = displayWidth(c.Name) }
		if displayWidth(dbType) > typeW { typeW = displayWidth(dbType) }
		if displayWidth(addr) > addrW { addrW = displayWidth(addr) }
	}

	padW := func(s string, w int) string { return padRightDW(s, w) }
	fmtRow := func(a, b, c, d string) string {
		return padW(a, nameW) + "  " + padW(b, typeW) + "  " + padW(c, addrW) + "  " + d
	}

	items := make([]PickerItem, len(conns))
	for i, c := range conns {
		dbType := c.DBType
		if dbType == "" {
			dbType = "oracle"
		}
		// Type column shows the actual db_type — opengauss and gaussdb are
		// distinct since v1.1.23 (different drivers), so no brand-rewrite.
		items[i] = PickerItem{
			Label: fmtRow(c.Name, dbType, fmt.Sprintf("%s:%d", c.Host, c.Port), c.User),
			Value: c.Name,
		}
	}

	r.writeOutputLine("")
	header := fmtRow("名称", "类型", "地址", "用户")
	result := r.RunPicker(items, 0, header, 0)

	if result.Selected != nil {
		r.loginPickerBypass = true
		r.pickerOriginCmd = "/login"
		r.handleEnter("/login " + result.Selected.Value)
	} else {
		r.drawInputArea()
	}
}

// runModelPicker shows an inline interactive model picker (same pattern as login picker).
func (r *REPL) runModelPicker() {
	if r.modelReloader == nil {
		r.writeOutputLine(dimStyle.Render("  模型管理未初始化"))
		r.drawInputArea()
		return
	}

	profiles := r.modelReloader.List()
	if len(profiles) == 0 {
		r.writeOutputLine(dimStyle.Render("  暂无已配置的模型"))
		r.drawInputArea()
		return
	}

	activeName := r.modelReloader.ActiveName()

	// Build aligned labels.
	nameW, vendorW, modelW := 4, 4, 4
	for _, p := range profiles {
		if displayWidth(p.Name) > nameW { nameW = displayWidth(p.Name) }
		if displayWidth(p.DisplayVendor()) > vendorW { vendorW = displayWidth(p.DisplayVendor()) }
		if displayWidth(p.DisplayModel()) > modelW { modelW = displayWidth(p.DisplayModel()) }
	}

	padW := func(s string, w int) string { return padRightDW(s, w) }
	fmtRow := func(name, vendor, mdl, active string) string {
		return padW(name, nameW) + "  " + padW(vendor, vendorW) + "  " + padW(mdl, modelW) + "  " + active
	}

	items := make([]PickerItem, 0, len(profiles)+1)
	initialIdx := 0
	for i, p := range profiles {
		active := ""
		if p.Name == activeName {
			active = "✓"
			initialIdx = i
		}
		items = append(items, PickerItem{
			Label: fmtRow(p.Name, p.DisplayVendor(), p.DisplayModel(), active),
			Value: p.Name,
		})
	}
	// Add "none" option.
	noneActive := ""
	if activeName == "" || activeName == "none" {
		noneActive = "✓"
		initialIdx = len(items)
	}
	items = append(items, PickerItem{Label: fmtRow("none", "-", "禁用LLM", noneActive), Value: "none"})

	r.writeOutputLine("")
	header := fmtRow("名称", "厂商", "模型", "")
	result := r.RunPicker(items, 0, header, initialIdx)

	if result.Selected != nil {
		r.modelPickerBypass = true
		r.pickerOriginCmd = "/model"
		r.handleEnter("/model " + result.Selected.Value)
	} else {
		r.drawInputArea()
	}
}

// runAlertPicker shows an inline picker for /llm or /rule with Sentinel+Scheduler alerts.
// First item is always "current" (health check). Remaining items from alertBuf.
func (r *REPL) runAlertPicker(cmd string) {
	// Build items: first is always "current".
	items := []PickerItem{{Label: "current — 当前数据库健康检查", Value: "current"}}

	// Add alert entries from alertBuf.
	if r.alertBuf != nil {
		sentinelIdx := 0
		for _, e := range r.alertBuf.Entries() {
			ts := e.Timestamp.Format("01-02 15:04")
			label := fmt.Sprintf("[%s] %s  %s", ts, e.Summary, dimStyle.Render(e.Source))
			value := "current"
			if e.Source == "Sentinel" {
				sentinelIdx++
				value = fmt.Sprintf("%d", sentinelIdx)
			}
			items = append(items, PickerItem{Label: label, Value: value})
		}
	}

	r.writeOutputLine("")
	result := r.RunPicker(items, 0, "", 0)

	if result.Selected != nil {
		if cmd == "llm" {
			r.llmPickerBypass = true
			r.pickerOriginCmd = "/llm"
			r.handleEnter("/llm " + result.Selected.Value)
		} else {
			r.rulePickerBypass = true
			r.pickerOriginCmd = "/rule"
			r.handleEnter("/rule " + result.Selected.Value)
		}
	} else {
		r.drawInputArea()
	}
}

// showExceptionDropdown builds and activates the Sentinel+Scheduler exception dropdown.
func (r *REPL) showExceptionDropdown(kind DropdownKind) bool {
	if r.alertBuf == nil {
		return false
	}
	entries := r.alertBuf.Entries()
	if len(entries) == 0 {
		return false
	}

	items := make([]DropdownItem, 0, len(entries))
	for _, e := range entries {
		ts := e.Timestamp.Format("15:04:05")
		label := fmt.Sprintf("[%-9s] %-40s  %s", e.Source, e.Summary, ts)
		// Value is the index as string for /diag N or /rule N dispatch.
		value := ""
		if e.Index >= 0 {
			value = fmt.Sprintf("%d", e.Index)
		}
		items = append(items, DropdownItem{
			Label: label,
			Value: value,
		})
	}

	r.dropdown = NewDropdown(kind, items)
	r.completions = nil
	r.compIdx = -1
	return true
}

// preLoginCommands are the only commands available before connecting.
var preLoginCommands = map[string]bool{
	"login":  true,
	"conn":   true,
	"exit":   true,
	"quit":   true,
	"help":   true,
	"clear":  true,
	"config": true,
}

func (r *REPL) updateCompletions() {
	// clear login dropdown if active
	input := string(r.inputBuf)
	if !strings.HasPrefix(input, "/") {
		r.completions = nil
		r.compIdx = -1
		r.compIsArg = false
		return
	}

	// Argument-level completion: "/cmd " + partial arg.
	if spaceIdx := strings.IndexByte(input, ' '); spaceIdx > 0 {
		cmdName := input[1:spaceIdx]
		argPrefix := strings.ToLower(strings.TrimSpace(input[spaceIdx+1:]))

		s, ok := r.registry.Get(cmdName)
		if !ok {
			r.completions = nil
			r.compIdx = -1
			r.compIsArg = false
			return
		}

		args := s.CLIDef().ArgCompletions
		r.completions = make([]string, 0, len(args))
		for _, arg := range args {
			if strings.HasPrefix(strings.ToLower(arg), argPrefix) {
				r.completions = append(r.completions, arg)
			}
		}
		r.compIsArg = true
		if r.compIdx >= len(r.completions) {
			r.compIdx = -1
		}
		return
	}

	// Command-level completion.
	r.compIsArg = false
	prefix := input[1:]
	connected := r.connInfo != nil

	matches := r.registry.Match(prefix)
	r.completions = make([]string, 0, len(matches))
	for _, s := range matches {
		name := s.Name()
		if !connected && !preLoginCommands[name] {
			continue
		}
		r.completions = append(r.completions, name)
	}

	for cmd := range preLoginCommands {
		if strings.HasPrefix(cmd, strings.ToLower(prefix)) {
			found := false
			for _, c := range r.completions {
				if c == cmd {
					found = true
					break
				}
			}
			if !found {
				r.completions = append(r.completions, cmd)
			}
		}
	}

	sortStrings(r.completions)

	if r.compIdx >= len(r.completions) {
		r.compIdx = -1
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (r *REPL) buildCompletionGhost() string {
	if len(r.completions) == 0 || len(r.inputBuf) == 0 || r.inputBuf[0] != '/' {
		return ""
	}
	idx := r.compIdx
	if idx < 0 {
		idx = 0
	}
	comp := r.completions[idx]
	input := string(r.inputBuf)

	if r.compIsArg {
		// Argument ghost: show rest of the argument after typed portion.
		spaceIdx := strings.IndexByte(input, ' ')
		if spaceIdx < 0 {
			return ""
		}
		argTyped := strings.TrimSpace(input[spaceIdx+1:])
		if len(comp) > len(argTyped) {
			return comp[len(argTyped):]
		}
		return ""
	}

	// Command ghost: show rest of command name.
	typed := input[1:]
	if len(comp) > len(typed) {
		return comp[len(typed):]
	}
	// Exact match on a picker command: show "↵" hint so user knows to press Enter.
	if len(comp) == len(typed) && isPickerCommand(input) {
		return " ↵"
	}
	return ""
}

// isPickerCommand returns true if input matches a command that triggers an
// interactive picker on Enter (login/model/llm/rule).
func isPickerCommand(input string) bool {
	switch strings.TrimSpace(input) {
	case "/login", "/model", "/m", "/llm", "/rule":
		return true
	}
	return false
}

// buildCompletionInput constructs the full input buffer for a completion selection.
func (r *REPL) buildCompletionInput(idx int) []rune {
	comp := r.completions[idx]
	if r.compIsArg {
		input := string(r.inputBuf)
		spaceIdx := strings.IndexByte(input, ' ')
		if spaceIdx >= 0 {
			return []rune(input[:spaceIdx+1] + comp)
		}
	}
	return []rune("/" + comp)
}

func (r *REPL) buildPrompt() string {
	connStatus := ""
	if r.connInfo != nil {
		// Render: " <DBType>·(<connName>)" so users see at a glance whether
		// they are connected to openGauss vs GaussDB (different drivers,
		// different protocol — visible distinction matters).
		dbLabel := ""
		if r.connMgr != nil {
			dbLabel = officialDBTypeName(r.connMgr.CurrentDBType())
		}
		if dbLabel != "" {
			connStatus = " " + dimStyle.Render(fmt.Sprintf("%s·(%s)", dbLabel, r.connInfo.Name))
		} else {
			connStatus = " " + dimStyle.Render(fmt.Sprintf("(%s)", r.connInfo.Name))
		}
	}
	queueHint := ""
	if n := len(r.cmdQueue); n > 0 {
		queueHint = dimStyle.Render(fmt.Sprintf(" [%d 排队]", n))
	}
	promptWhite := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	return fmt.Sprintf("%s%s%s ", promptWhite.Render("❯"), connStatus, queueHint)
}

// handlePaste processes multiple lines that arrived in a single read() call (paste).
// SQL input is joined and executed (or enters sqlMode if no ";").
// Natural language is joined as one message and sent to LLM.
func (r *REPL) handlePaste(lines []string) {
	joined := strings.Join(lines, "\n")
	trimmed := strings.TrimSpace(joined)
	if trimmed == "" {
		r.drawInputArea()
		return
	}

	// Classify based on first non-empty line.
	firstLine := ""
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			firstLine = t
			break
		}
	}

	classified := dispatch.ClassifyInput(firstLine)

	// Build collapsed display: [Pasted text: first line... + N lines]
	prompt := r.buildPrompt()
	pasteDisplay := r.colorizeInput(firstLine)
	if len(lines) > 1 {
		truncated := firstLine
		if len(truncated) > 40 {
			truncated = truncated[:40] + "..."
		}
		pasteDisplay = dimStyle.Render("[Pasted ") + r.colorizeInput(truncated) + dimStyle.Render(fmt.Sprintf(" + %d lines]", len(lines)-1))
	}

	if classified == dispatch.InputSQL {
		// SQL paste: if ends with ";", execute immediately.
		if strings.HasSuffix(strings.TrimSpace(trimmed), ";") {
			r.writeOutputLine(prompt + pasteDisplay)
			r.handleEnter(trimmed)
			return
		}
		// No ";" — enter sqlMode with the pasted lines as initial buffer.
		r.sqlMode = true
		r.sqlBuffer = lines
		r.writeOutputLine(prompt + pasteDisplay)
		r.writeOutputLine(dimStyle.Render("  ... 多行 SQL 模式，输入 ; 结束执行，Ctrl+C 取消"))
		r.drawInputArea()
		return
	}

	// Natural language or slash command: treat the joined text as one input.
	r.writeOutputLine(prompt + pasteDisplay)
	r.handleEnter(joined)
}

// colorizeInput highlights modification commands (kill, alter, resize) in red.
func (r *REPL) colorizeInput(input string) string {
	if !strings.HasPrefix(input, "/") {
		return input
	}
	cmd := strings.Fields(input)[0]
	name := cmd[1:] // strip "/"
	if sk, ok := r.registry.Get(name); ok && sk.SecurityLevel() > skill.LevelReadOnly {
		return warnStyle.Render(input)
	}
	return input
}

// ── Width Helpers ─────────────────────────────────────────────

func displayWidth(s string) int {
	return termwidth.RuneWidth(s)
}

// padRightDW pads s with spaces to reach the given display width.
func padRightDW(s string, width int) string {
	return termwidth.PadRight(s, width)
}

// officialDBTypeName returns the official capitalization for a database type.
// openGauss and GaussDB are separate types since v1.1.23 — each has its own
// driver, so the display name follows the actual product, not brand layer.
func officialDBTypeName(dbType string) string {
	switch strings.ToLower(dbType) {
	case "oracle", "":
		return "Oracle"
	case "mysql":
		return "MySQL"
	case "postgres", "postgresql":
		return "PostgreSQL"
	case "opengauss":
		return "openGauss"
	case "gaussdb":
		return "GaussDB"
	default:
		return dbType
	}
}

func visibleWidth(s string) int {
	return termwidth.StringWidth(s)
}

func stripAnsi(s string) string {
	return termwidth.StripANSI(s)
}

func truncateToWidth(s string, maxW int) string {
	return termwidth.Truncate(s, maxW)
}
