/*-------------------------------------------------------------------------
 *
 * repl_input.go
 *	  REPL input line — captures keystrokes, handles cursor movement,
 *	  history (up/down), tab completion for /commands, and Ctrl-C/D
 *	  without leaving the screen in a weird state.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_input.go
 *
 *-------------------------------------------------------------------------
 */
package ui


import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// clearLLMSessions deletes all engine session files for the currently
// active instance under ~/.opendb/sessions/<instance>/. Returns number
// of .jsonl files removed and any error encountered.
//
// Implements /clear semantics matching Claude Code: dropping LLM history
// is now an EXPLICIT user action (replacing the removed Jaccard-based
// topic-drift heuristic that misfired in multiple scenarios — see
// CHANGELOG v1.1.47).
func (r *REPL) clearLLMSessions() (int, error) {
	instance := r.connMgr.CurrentName()
	if instance == "" {
		return 0, nil // not connected — nothing to clear
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("locate home dir: %w", err)
	}
	dir := filepath.Join(home, ".opendb", "sessions", instance)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // no session dir yet
		}
		return 0, fmt.Errorf("read sessions dir: %w", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil {
			return count, fmt.Errorf("remove %s: %w", e.Name(), err)
		}
		count++
	}
	return count, nil
}

// ── Input Handling ────────────────────────────────────────────

func (r *REPL) handleEnter(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		r.pickerOriginCmd = "" // defensive clear
		r.drawInputArea()
		return
	}

	// SQL multi-line mode: accumulate lines until ";" is found.
	if r.sqlMode {
		r.sqlBuffer = append(r.sqlBuffer, input)
		// Check if input ends with ";".
		if strings.HasSuffix(input, ";") {
			joined := strings.Join(r.sqlBuffer, "\n")
			r.sqlMode = false
			r.sqlBuffer = nil
			// Echo the continuation line, then dispatch the joined SQL.
			prompt := "  " + dimStyle.Render("...") + " "
			r.writeOutputLine(prompt + input)
			r.handleEnter(joined) // recursive call with full SQL
			return
		}
		// Still accumulating — show continuation prompt.
		prompt := "  " + dimStyle.Render("...") + " "
		r.writeOutputLine(prompt + input)
		r.drawInputArea()
		return
	}

	// Capture and clear pickerOriginCmd early to avoid stale leaks on any return path.
	pickerOrigin := r.pickerOriginCmd
	r.pickerOriginCmd = ""

	// /login (with or without args) → interactive connection picker.
	// Exception: internal calls from runLoginPicker set loginPickerBypass.
	// Skip picker during async — queue bare command instead.
	if strings.HasPrefix(input, "/login") && !r.loginPickerBypass && !r.diagRunning && !r.skillRunning {
		trimmed := strings.TrimSpace(input)
		if trimmed == "/login" || strings.HasPrefix(trimmed, "/login ") {
			prompt := r.buildPrompt()
			r.writeOutputLine(prompt + r.colorizeInput(input))
			r.blockingUI = true
			r.runLoginPicker()
			return
		}
	}
	r.loginPickerBypass = false

	// /model (with or without args) → interactive model picker.
	// Exception: internal calls from runModelPicker set modelPickerBypass.
	// Skip picker during async — queue bare command instead.
	if (strings.HasPrefix(input, "/model") || strings.HasPrefix(input, "/m ") || input == "/m") && !r.modelPickerBypass && !r.diagRunning && !r.skillRunning {
		trimmed := strings.TrimSpace(input)
		cmd := strings.Fields(trimmed)[0]
		if cmd == "/model" || cmd == "/m" {
			prompt := r.buildPrompt()
			r.writeOutputLine(prompt + r.colorizeInput(input))
			r.blockingUI = true
			r.runModelPicker()
			return
		}
	}
	r.modelPickerBypass = false

	// /llm → interactive alert picker.
	// Skip picker during async — queue bare command instead.
	if strings.HasPrefix(input, "/llm") && !r.llmPickerBypass && !r.diagRunning && !r.skillRunning {
		trimmed := strings.TrimSpace(input)
		if trimmed == "/llm" || strings.HasPrefix(trimmed, "/llm ") {
			prompt := r.buildPrompt()
			r.writeOutputLine(prompt + r.colorizeInput(input))
			r.blockingUI = true
			r.runAlertPicker("llm")
			return
		}
	}
	r.llmPickerBypass = false

	// /rule → interactive alert picker.
	// Skip picker during async — queue bare command instead.
	if strings.HasPrefix(input, "/rule") && !r.rulePickerBypass && !r.diagRunning && !r.skillRunning {
		trimmed := strings.TrimSpace(input)
		if trimmed == "/rule" || strings.HasPrefix(trimmed, "/rule ") {
			prompt := r.buildPrompt()
			r.writeOutputLine(prompt + r.colorizeInput(input))
			r.blockingUI = true
			r.runAlertPicker("rule")
			return
		}
	}
	r.rulePickerBypass = false

	// Block bare "/" or unmatched "/" commands.
	if strings.HasPrefix(input, "/") {
		cmd := strings.Fields(input)[0]
		builtin := cmd == "/exit" || cmd == "/quit"
		if !builtin && (cmd == "/" || len(r.registry.Match(cmd[1:])) == 0) {
			r.writeOutputLine("")
			r.writeOutputLine(errorStyle.Render("  ✗ 未知命令: " + cmd))
			r.drawInputArea()
			return
		}
	}

	// Translate native CLI commands to standard SQL.
	dbType := r.connMgr.CurrentDBType()
	// Oracle: SQL*Plus commands (SHOW PARAMETER, DESC, SHOW ERRORS, etc.)
	if rewritten := rewriteSQLPlus(input, dbType); rewritten != input {
		input = rewritten
	}
	// PostgreSQL/OpenGauss: psql backslash commands (\d, \dt, \du, etc.)
	if rewritten := rewriteBackslashCmd(input, dbType); rewritten != input {
		// \hint: prefix = unsupported command with friendly message.
		if strings.HasPrefix(rewritten, `\hint:`) {
			r.writeOutputLine(dimStyle.Render("  " + rewritten[len(`\hint:`):]))
			r.drawInputArea()
			return
		}
		input = rewritten
	}

	// Picker-originated commands: display/history uses the bare command name,
	// and skip second echo (already echoed when picker opened).
	displayCmd := input
	fromPicker := pickerOrigin != ""
	if fromPicker {
		displayCmd = pickerOrigin
	}

	// Avoid consecutive duplicates in history.
	if len(r.history) == 0 || r.history[len(r.history)-1] != displayCmd {
		r.history = append(r.history, displayCmd)
		r.saveHistoryEntry(displayCmd)
	}
	r.historyIdx = -1

	// Echo command to chat area (skip if already echoed by picker).
	if !fromPicker {
		prompt := r.buildPrompt()
		r.writeOutputLine(prompt + r.colorizeInput(input))
	}

	// /exit and /quit always execute immediately, even during async diag.
	if input == "/quit" || input == "/exit" || input == "exit" || input == "quit" {
		r.exitRequested = true
		return
	}

	// If an async operation is running, queue the command for later execution.
	// Display queued count in input area — no hourglass animation to avoid
	// cursor drift conflicts with streaming/star animations.
	if r.diagRunning || r.skillRunning {
		// LLM/diag commands have a queue limit of 3; others allow more.
		if r.isDiagWithLLM(input) && len(r.cmdQueue) >= 3 {
			r.writeOutputLine(dimStyle.Render("  ✗ 排队已满 (最多3个诊断命令), 请稍后再试"))
			r.drawInputArea()
			return
		}
		r.cmdQueue = append(r.cmdQueue, input)
		r.drawInputArea()
		return
	}

	if input == "/clear" {
		// v1.1.47 enhancement: /clear now ALSO drops persisted LLM session
		// (not just screen). Mirrors Claude Code semantics — user explicitly
		// starts fresh context. Replaces removed Jaccard topic-drift heuristic.
		clearedCount, clearErr := r.clearLLMSessions()
		r.resetScreen()
		if clearErr == nil && clearedCount > 0 {
			r.writeOutputLine(dimStyle.Render(fmt.Sprintf("  ✓ 已清空 %d 个 LLM session 文件 (后续问题从空 history 开始)", clearedCount)))
		} else if clearErr != nil {
			r.writeOutputLine(dimStyle.Render("  ⚠️ 清屏成功但 LLM session 文件清理失败: " + clearErr.Error()))
		}
		return
	}

	// Async LLM: if command is /llm with LLM, or natural language input, run asynchronously.
	if r.isDiagWithLLM(input) {
		r.startDiagAsync(input)
		return
	}
	// Natural language → route to /llm async if LLM is configured.
	if r.isNaturalLanguageWithLLM(input) {
		r.startDiagAsync("/llm " + input)
		return
	}

	// Async path: non-sync commands run in background goroutine.
	if !isSyncCommand(input) {
		r.startSkillAsync(input)
		return
	}

	// Sync path: interactive / instant commands.
	vertical := false
	dispatchInput := input
	if strings.HasSuffix(strings.TrimSpace(input), `\G`) {
		vertical = true
		dispatchInput = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(input), `\G`))
	}

	ctx := context.Background()
	result, err := r.dispatcher.Dispatch(ctx, dispatchInput)
	if err != nil {
		r.writeOutputLine("")
		r.writeOutputLine(errorStyle.Render("  ✗ " + err.Error()))
		r.drawInputArea()
		return
	}

	if result != nil {
		// Intercept /login picker signal: open interactive selector.
		if result.Metadata != nil && result.Metadata["action"] == "connection_picker" {
			r.blockingUI = true
			selected := r.pickConnection()
			if selected != "" {
				loginCmd := "/login " + selected
				loginDisplayCmd := "/login"
				r.history = append(r.history, loginDisplayCmd)
				r.saveHistoryEntry(loginDisplayCmd)
				r.writeOutputLine(r.buildPrompt() + r.colorizeInput(loginDisplayCmd))
				loginResult, loginErr := r.dispatcher.Dispatch(ctx, loginCmd)
				if loginErr != nil {
					r.writeOutputLine(errorStyle.Render("  ✗ " + loginErr.Error()))
				} else if loginResult != nil {
					r.renderResult(loginResult, loginCmd, false)
					r.updateConnInfoFromResult(loginResult, loginCmd)
				}
			}
			r.writeOutputLine("")
			r.drawInputArea()
			return
		}

		// Connection / model wizards moved to `<binary> configure`. The
		// in-REPL wizards were removed because two add paths (REPL wizard
		// vs configure) caused confusion about where state lived. /conn
		// no-args and /model with no add subcommand now just print info.

		r.renderResult(result, input, vertical)
		r.updateConnInfoFromResult(result, input)

		if strings.HasPrefix(input, "/logout") {
			r.connInfo = nil
			r.historyIdx = -1 // reset history navigation on logout
			r.registry.SetActiveDB("")
			r.stopSentinel()
			r.stopScheduler()
		}
	}

	r.writeOutputLine("")
	r.drawInputArea()
}

// updateConnInfoFromResult checks if a login result indicates a successful
// connection and updates connInfo accordingly.
func (r *REPL) updateConnInfoFromResult(result *skill.Result, input string) {
	if !strings.HasPrefix(input, "/login") && !strings.HasPrefix(input, "/conn") {
		return
	}
	if result.Type != skill.ResultText {
		return
	}
	text := result.Rendered
	if text == "" {
		text, _ = result.Data.(string)
	}
	if strings.HasPrefix(text, "Connected to") {
		name := strings.TrimPrefix(text, "Connected to ")
		r.connInfo = &ConnectionInfo{Name: name}
		r.historyIdx = -1 // reset history navigation on login
		// Route skill commands to the connected database type.
		if dbType := r.connMgr.CurrentDBType(); dbType != "" {
			r.registry.SetActiveDB(dbType)
			// Switch sentinel and diag to the correct product.
			r.switchAISkills(dbType)
			// Initialize context stores for session/memory/policy support.
			r.initContextStores(name)
			if r.activeInstanceSync != nil {
				r.activeInstanceSync(name)
			}
		}
		r.autoStartSentinel()
		r.autoStartScheduler()
	}
}

// SetAISkillMaps stores per-DB sentinel and diag skill maps for dynamic resolution.
func (r *REPL) SetAISkillMaps(sentinelMap map[string]SentinelAlertSource, diagMap map[string]DiagAsyncSource) {
	r.sentinelMap = sentinelMap
	r.diagMap = diagMap
}

// switchAISkills switches sentinel and diag to the product matching dbType.
// Stops the old sentinel if running, then sets the new one.
func (r *REPL) switchAISkills(dbType string) {
	// Stop current sentinel if running and switching to a different product.
	if r.sentinelSkill != nil && r.sentinelSkill.IsRunning() {
		r.sentinelSkill.StopSentinel()
	}
	// Resolve sentinel for the new DB type.
	if ss, ok := r.sentinelMap[dbType]; ok {
		r.sentinelSkill = ss
	}
	// Resolve diag for the new DB type.
	if ds, ok := r.diagMap[dbType]; ok {
		r.diagSkill = ds
	}
}

// contextStoreInitializer is optionally implemented by DiagnoseSkill to enable session/memory/policy.
type contextStoreInitializer interface {
	SetContextStores(baseDir, instance string)
}

// initContextStores initializes context stores on the active diagSkill if it supports them.
func (r *REPL) initContextStores(instance string) {
	if r.diagSkill == nil {
		return
	}
	if csi, ok := r.diagSkill.(contextStoreInitializer); ok {
		baseDir := config.DefaultOpenDBDir()
		csi.SetContextStores(baseDir, instance)
	}
}

// SetModelReloader sets the model manager for auto-reload after wizard save.
func (r *REPL) SetModelReloader(mr modelReloader) { r.modelReloader = mr }

// SetActiveInstanceSync registers a callback invoked whenever /login
// succeeds with the new instance name. main() wires this to the shared
// memory.Store.SetActiveInstance so memory_write / memory_recall / profile
// always hit the directory matching the current connection.
func (r *REPL) SetActiveInstanceSync(fn func(instance string)) { r.activeInstanceSync = fn }

// hasLargeCell returns true if any cell in the query result exceeds maxLen bytes.
// Used to switch from table to raw text rendering for commands like SHOW ENGINE INNODB STATUS.
func hasLargeCell(qr *db.QueryResult, maxLen int) bool {
	for _, row := range qr.Rows {
		for _, cell := range row {
			if str, ok := cell.(string); ok && len(str) > maxLen {
				return true
			}
		}
	}
	return false
}

func (r *REPL) renderResult(result *skill.Result, input string, vertical bool) {
	switch result.Type {
	case skill.ResultText:
		// Prefer pre-rendered text; fall back to Data string for backward compatibility.
		text := result.Rendered
		if text == "" {
			text, _ = result.Data.(string)
		}
		if text == "__clear__" {
			r.resetScreen()
			return
		}
		for _, line := range strings.Split(text, "\n") {
			r.writeOutputLine(line)
		}

	case skill.ResultTable:
		if qr, ok := result.Data.(*db.QueryResult); ok {
			// Detect large text cells (>4KB) — render as raw text, not table.
			// Covers SHOW ENGINE INNODB STATUS, SHOW SLAVE STATUS with long fields, etc.
			if hasLargeCell(qr, 4096) {
				r.writeOutputLine("")
				for ri, row := range qr.Rows {
					if ri > 0 {
						r.writeOutputLine("")
					}
					for ci, cell := range row {
						colName := ""
						if ci < len(qr.Columns) {
							colName = qr.Columns[ci]
						}
						str := fmt.Sprintf("%v", cell)
						if len(str) <= 200 {
							r.writeOutputLine(fmt.Sprintf("  %s: %s", accentStyle.Render(colName), str))
						} else {
							r.writeOutputLine(accentStyle.Render(fmt.Sprintf("  ─── %s ───", colName)))
							for _, ln := range strings.Split(str, "\n") {
								r.writeOutputLine(ln)
							}
						}
					}
				}
				if result.Summary != "" {
					r.writeOutputLine("")
					r.writeOutputLine(dimStyle.Render(result.Summary))
				}
			} else {
				// Normal table rendering.
				table := format.FormatTableOpts(qr, format.TableOptions{
					MaxRows:   50,
					TermWidth: r.cols,
					Vertical:  vertical,
				})
				r.writeOutputLine("")
				if result.Rendered != "" {
					r.writeOutputLine(dimStyle.Render("  " + result.Rendered))
				}
				for _, line := range strings.Split(table, "\n") {
					r.writeOutputLine(line)
				}
				if result.Summary != "" {
					r.writeOutputLine(dimStyle.Render(result.Summary))
				}
				// If more rows exist, prepare table browser and show hint.
				r.pendingTable = nil
				if len(qr.Rows) > 50 {
					tl := format.FormatTableLines(qr, r.cols, 500)
					if tl != nil {
						r.pendingTable = tl
						r.writeOutputLine(dimStyle.Render("  按 F 进入滚动浏览 (共 " +
							fmt.Sprintf("%d", tl.TotalRows) + " 行, 最多显示 " +
							fmt.Sprintf("%d", len(tl.Rows)) + " 行)"))
					}
				}
			}
		} else {
			r.writeOutputLine(fmt.Sprintf("%v", result.Data))
		}

	case skill.ResultRefresh:
		if src, ok := result.Data.(skill.DbtopRefreshSource); ok {
			r.runDbtop(src)
		} else {
			text, _ := result.Data.(string)
			r.writeOutputLine("")
			for _, line := range strings.Split(text, "\n") {
				r.writeOutputLine(line)
			}
		}

	case skill.ResultError:
		text, _ := result.Data.(string)
		if text == "" {
			text = result.Summary
		}
		r.writeOutputLine("")
		r.writeOutputLine(errorStyle.Render("  ✗ " + text))
	}
}

