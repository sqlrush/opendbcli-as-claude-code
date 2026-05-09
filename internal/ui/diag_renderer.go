/*-------------------------------------------------------------------------
 *
 * diag_renderer.go
 *	  DiagPhase represents the current phase of a /diag execution.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/diag_renderer.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/ui/termwidth"
)

// DiagPhase represents the current phase of a /diag execution.
type DiagPhase uint8

const (
	DiagPhaseStart      DiagPhase = iota // initial "正在诊断异常 #N..."
	DiagPhaseRule                        // "规则分析: ..."
	DiagPhaseAIStart                     // "AI 深度分析 (mode, 最多N轮)..."
	DiagPhaseAIRound                     // "第N轮: 查询 xxx (Xs)"
	DiagPhaseAIStream                    // streaming text delta from LLM
	DiagPhaseDone                        // completed with result
	DiagPhaseError                       // failed
)

// DiagProgressEvent carries one progress update from DiagnoseSkill to REPL.
type DiagProgressEvent struct {
	Phase   DiagPhase
	Message string        // human-readable status text
	Elapsed time.Duration // elapsed time for this step
	Result  *skill.Result // only set for DiagPhaseDone
	Err     error         // only set for DiagPhaseError
}

// hourglassChars cycle between two hourglass states for the queue animation.
var hourglassChars = []string{"⏳", "⌛"}

// hourglassAnimator manages the hourglass animation on a specific terminal row.
// All methods are called from the REPL main goroutine only (no mutex needed).
type hourglassAnimator struct {
	row     int    // terminal row (adjusted on scroll)
	charIdx int    // current position in hourglassChars cycle
	stopped bool
	writer  func(row int, content string)
}

// newHourglassAnimator creates an hourglass animator on the given row.
func newHourglassAnimator(row int, writer func(row int, content string)) *hourglassAnimator {
	ha := &hourglassAnimator{
		row:    row,
		writer: writer,
	}
	ha.tick()
	return ha
}

// tick cycles the hourglass character and redraws the line.
func (ha *hourglassAnimator) tick() {
	if ha.stopped || ha.row < 1 {
		return
	}
	ch := hourglassChars[ha.charIdx%len(hourglassChars)]
	ha.charIdx++
	line := fmt.Sprintf("  %s 排队中", dimStyle.Render(ch))
	ha.writer(ha.row, line)
}

// stop halts the hourglass animation and clears the line.
func (ha *hourglassAnimator) stop() {
	ha.stopped = true
}

// dotChar is the Claude Code-style blinking dot for SQL execution progress.
const dotChar = "⏺"

// starChars are the Unicode characters cycled for the LLM blinking star indicator.
var starChars = []string{"✶", "✳", "✴", "✵", "⊹", "✦"}

// animStyle distinguishes LLM star animation from SQL dot animation.
type animStyle uint8

const (
	animStar animStyle = iota // LLM: ✶ ✳ ✴ cycling, accent color
	animDot                   // SQL: ⏺ blink, dim style
)

// starAnimator manages a blinking animation on a specific terminal row.
// All methods are called from the REPL main goroutine only (no mutex needed).
// The star row is adjusted by writeOutputLine when scrolling occurs.
type starAnimator struct {
	row     int       // terminal row where the star line lives (adjusted on scroll)
	message string    // text after the indicator
	startAt time.Time // when the animation was created
	charIdx int       // current position in cycle
	style   animStyle // star (LLM) or dot (SQL)
	stopped bool
	writer  func(row int, content string) // callback to write to terminal
}

// newStarAnimator creates a star-style animator (for LLM/diag).
func newStarAnimator(row int, message string, writer func(row int, content string)) *starAnimator {
	sa := &starAnimator{
		row:     row,
		message: message,
		startAt: time.Now(),
		style:   animStar,
		writer:  writer,
	}
	sa.tick()
	return sa
}

// newDotAnimator creates a dot-style animator (for SQL/skill execution).
func newDotAnimator(row int, message string, writer func(row int, content string)) *starAnimator {
	sa := &starAnimator{
		row:     row,
		message: message,
		startAt: time.Now(),
		style:   animDot,
		writer:  writer,
	}
	sa.tick()
	return sa
}

// tick advances the animation and redraws the line.
// Called from the main goroutine's select loop every 150ms.
func (sa *starAnimator) tick() {
	if sa.stopped || sa.row < 1 {
		return
	}
	sa.charIdx++
	elapsed := time.Since(sa.startAt)

	var line string
	switch sa.style {
	case animDot:
		// ⏺ blink: bright/dim alternating, all text in dim
		var dot string
		if sa.charIdx%2 == 0 {
			dot = accentStyle.Render(dotChar)
		} else {
			dot = dimStyle.Render(dotChar)
		}
		line = fmt.Sprintf("  %s %s",
			dot, dimStyle.Render(sa.message+" ("+formatDuration(elapsed)+")"))
	default:
		// Star cycling: ✶ ✳ ✴ ✵ ⊹ ✦ in accent color
		star := starChars[sa.charIdx%len(starChars)]
		line = fmt.Sprintf("  %s %s %s",
			accentStyle.Render(star), sa.message,
			dimStyle.Render("("+formatDuration(elapsed)+")"))
	}
	sa.writer(sa.row, line)
}

// updateMessage changes the displayed message (e.g., "AI 深度分析中...").
func (sa *starAnimator) updateMessage(msg string) {
	sa.message = msg
}

// stop halts the animation and shows the final "Worked for" line.
func (sa *starAnimator) stop() {
	if sa.stopped {
		return
	}
	sa.stopped = true
	if sa.row < 1 {
		return
	}
	elapsed := time.Since(sa.startAt)
	var line string
	switch sa.style {
	case animDot:
		line = fmt.Sprintf("  %s %s",
			dimStyle.Render("⏺"), dimStyle.Render("完成 ("+formatDuration(elapsed)+")"))
	default:
		line = fmt.Sprintf("  %s %s",
			dimStyle.Render("✻"), dimStyle.Render("完成 ("+formatDuration(elapsed)+")"))
	}
	sa.writer(sa.row, line)
}

// stopWithError halts the animation and shows an error line.
func (sa *starAnimator) stopWithError(errMsg string) {
	if sa.stopped {
		return
	}
	sa.stopped = true
	if sa.row < 1 {
		return
	}
	elapsed := time.Since(sa.startAt)
	line := fmt.Sprintf("  %s %s %s",
		errorStyle.Render("✗"), errMsg, dimStyle.Render("("+formatDuration(elapsed)+")"))
	sa.writer(sa.row, line)
}

// formatDuration formats a duration as "Xs" or "Xm Ys".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

// renderDiagProgress handles a progress event from the async diag goroutine.
// All rendering happens on the REPL main goroutine via this function.
func (r *REPL) renderDiagProgress(ev DiagProgressEvent) {
	switch ev.Phase {
	case DiagPhaseStart:
		// Write star line and start animation.
		r.writeOutputLine("") // blank line before diag output
		r.diagStarRow = r.currentStarRow()
		r.writeOutputLine("") // placeholder for star line
		r.diagStar = newStarAnimator(r.diagStarRow, ev.Message, r.overwriteRow)

	case DiagPhaseRule:
		r.writeOutputLine(dimStyle.Render("  ├ " + ev.Message))

	case DiagPhaseAIStart:
		// Update the star message to reflect AI phase.
		if r.diagStar != nil {
			r.diagStar.updateMessage(ev.Message)
		}

	case DiagPhaseAIRound:
		// If streaming is active and partial is displayed, restore committed
		// content before writing round info (prevents partial scrolling up).
		if r.diagStreaming && r.diagPartialShown && r.scrollMode {
			r.restoreLastCommittedLine()
			r.diagPartialShown = false
			r.diagPartialPrev = ""
		}
		elapsed := ""
		if ev.Elapsed > 0 {
			elapsed = fmt.Sprintf(" (%s)", formatDuration(ev.Elapsed))
		}
		r.writeOutputLine(dimStyle.Render(fmt.Sprintf("  ├ %s%s", ev.Message, elapsed)))

	case DiagPhaseAIStream:
		// First streaming delta: stop star animation and start line-buffered output.
		if !r.diagStreaming {
			r.diagStreaming = true
			r.diagStreamBuf = ""
			r.diagFmt = newDiagStreamFormatter(r.cols)
			if r.diagStar != nil {
				r.diagStar.stop()
				r.diagStar = nil
			}
			r.writeOutputLine("")
			r.drawInputArea()
		}
		// Buffer text and flush complete lines via writeOutputLine (scroll-safe).
		r.diagStreamBuf += ev.Message
		flushed := false
		for {
			idx := strings.Index(r.diagStreamBuf, "\n")
			if idx < 0 {
				break
			}
			// In scroll mode, restore the committed content at maxContentRow before
			// flushing. The partial display overwrote it — if we just clear, the
			// blank row gets scrolled up as a permanent blank line (losing content).
			if !flushed && r.diagPartialShown && r.scrollMode {
				r.restoreLastCommittedLine()
				r.diagPartialShown = false
				r.diagPartialPrev = ""
			}
			rawLine := r.diagStreamBuf[:idx]
			r.diagStreamBuf = r.diagStreamBuf[idx+1:]
			// Apply markdown formatting (may return 0 lines for buffered constructs).
			if r.diagFmt != nil {
				formatted := r.diagFmt.formatLine(rawLine)
				for _, fl := range formatted {
					for _, wl := range wrapLineToWidth(fl, r.cols-1) {
						r.writeOutputLine(wl)
						flushed = true
					}
				}
			} else {
				for _, wl := range wrapLineToWidth(rawLine, r.cols-1) {
					r.writeOutputLine(wl)
					flushed = true
				}
			}
		}
		if flushed {
			// Input area position changed after new lines, redraw it.
			r.drawInputArea()
		}
		// Show partial line in-place for real-time character-by-character feel.
		// Append only the new portion to avoid clear+rewrite flicker.
		if r.diagStreamBuf != "" {
			display := r.diagStreamBuf
			maxW := r.cols - 3
			if displayWidth(display) >= r.cols {
				display = truncateToWidth(display, maxW) + ".."
			}
			partialRow := r.contentRow
			if r.scrollMode {
				partialRow = r.maxContentRow()
			}
			if !r.diagPartialShown {
				// First partial: clear row and write from start.
				termWriteAt(r.writer, partialRow, display)
			} else if flushed {
				// Previous partial was committed as a full line; start fresh.
				termWriteAt(r.writer, partialRow, display)
			} else {
				// Append: position cursor after previously written partial content,
				// write only the new delta. This avoids the clear+rewrite flicker.
				prevW := displayWidth(r.diagPartialPrev)
				newContent := ""
				if len(display) > len(r.diagPartialPrev) {
					newContent = display[len(r.diagPartialPrev):]
				} else {
					// Display is shorter than previous partial (new line started);
					// clear and rewrite the whole line.
					termWriteAt(r.writer, partialRow, display)
					r.diagPartialPrev = display
					r.diagPartialShown = true
					r.restoreCursor()
					return
				}
				if len(newContent) > 0 && prevW < maxW {
					termMoveTo(r.writer, partialRow, prevW+1)
					fmt.Fprint(r.writer, newContent)
				}
			}
			r.diagPartialPrev = display
			r.diagPartialShown = true
			r.restoreCursor()
		}
		return // skip unconditional drawInputArea — handled above

	case DiagPhaseDone:
		if r.diagStar != nil {
			r.diagStar.stop()
			r.diagStar = nil
		}
		if r.diagStreaming {
			// Restore committed content before flushing remaining partial.
			if r.diagPartialShown && r.scrollMode {
				r.restoreLastCommittedLine()
			}
			// Flush remaining partial line through the formatter.
			if r.diagStreamBuf != "" {
				if r.diagFmt != nil {
					for _, fl := range r.diagFmt.formatLine(r.diagStreamBuf) {
						for _, wl := range wrapLineToWidth(fl, r.cols-1) {
							r.writeOutputLine(wl)
						}
					}
				} else {
					for _, wl := range wrapLineToWidth(r.diagStreamBuf, r.cols-1) {
						r.writeOutputLine(wl)
					}
				}
				r.diagStreamBuf = ""
			}
			// Flush any buffered multi-line constructs (tables, code blocks).
			if r.diagFmt != nil {
				for _, fl := range r.diagFmt.flush() {
					for _, wl := range wrapLineToWidth(fl, r.cols-1) {
						r.writeOutputLine(wl)
					}
				}
				r.diagFmt = nil
			}
			r.diagStreaming = false
			r.diagPartialShown = false
			r.diagPartialPrev = ""
			r.writeOutputLine("")
		} else {
			r.writeOutputLine("")
			if ev.Result != nil {
				// Apply markdown formatting to non-streaming LLM output
				// (playbook mode returns full text at once, not via streaming deltas).
				if rendered := ev.Result.Rendered; rendered != "" && ev.Result.Type == skill.ResultText {
					fmter := newDiagStreamFormatter(r.cols)
					for _, rawLine := range strings.Split(rendered, "\n") {
						formatted := fmter.formatLine(rawLine)
						for _, fl := range formatted {
							for _, wl := range wrapLineToWidth(fl, r.cols-1) {
								r.writeOutputLine(wl)
							}
						}
					}
					for _, fl := range fmter.flush() {
						for _, wl := range wrapLineToWidth(fl, r.cols-1) {
							r.writeOutputLine(wl)
						}
					}
				} else {
					r.renderResult(ev.Result, "", false)
				}
			}
		}

	case DiagPhaseError:
		r.diagStreaming = false
		r.diagStreamBuf = ""
		r.diagPartialShown = false
		r.diagPartialPrev = ""
		if r.diagStar != nil {
			r.diagStar.stopWithError("诊断失败")
			r.diagStar = nil
		}
		if ev.Err != nil {
			friendly := friendlyLLMError(ev.Err.Error())
			lines := strings.Split(friendly, "\n")
			for i, line := range lines {
				if i == 0 {
					r.writeOutputLine(errorStyle.Render("  ✗ " + line))
				} else {
					r.writeOutputLine(errorStyle.Render("    " + line))
				}
			}
		}
		// If there's a partial result (rule-based), still show it.
		if ev.Result != nil {
			r.writeOutputLine("")
			r.renderResult(ev.Result, "", false)
		}
	}

	r.drawInputArea()
}

// friendlyLLMError translates raw LLM/network errors into user-friendly Chinese messages.
func friendlyLLMError(raw string) string {
	switch {
	case strings.Contains(raw, "connection refused"):
		return "LLM 服务未连接 (connection refused)\n" +
			"    可能原因: Ollama 未启动或 SSH 隧道断开\n" +
			"    替代方案: /rule live — 使用规则引擎诊断 (无需 LLM)"
	case strings.Contains(raw, "timeout") || strings.Contains(raw, "deadline exceeded"):
		return "LLM 请求超时\n" +
			"    可能原因: 模型推理过慢或网络延迟\n" +
			"    替代方案: /rule live — 使用规则引擎诊断"
	case strings.Contains(raw, "EOF") || strings.Contains(raw, "broken pipe"):
		return "LLM 连接中断\n" +
			"    可能原因: 服务端异常断开\n" +
			"    替代方案: /rule live — 使用规则引擎诊断"
	case strings.Contains(raw, "model not found") || strings.Contains(raw, "404"):
		return "LLM 模型未找到\n" +
			"    请检查 config.yaml 中的 llm.model 配置"
	default:
		return raw
	}
}

// currentStarRow returns the row where the next writeOutputLine will go.
// In scroll mode, it's always maxContentRow; otherwise it's contentRow.
func (r *REPL) currentStarRow() int {
	if r.scrollMode {
		return r.maxContentRow()
	}
	return r.contentRow
}

// overwriteRow writes content to a specific terminal row (for star animation).
func (r *REPL) overwriteRow(row int, content string) {
	if row < 1 {
		return
	}
	termWriteAt(r.writer, row, content)
	r.restoreCursor()
}

// restoreLastCommittedLine restores the last committed output line at maxContentRow.
// In scroll mode, partial display overwrites maxContentRow. Before flushing new lines,
// we must restore the committed content so that writeOutputLine's scroll preserves it
// correctly — otherwise the overwritten content is lost (causing missing table rows, etc.).
func (r *REPL) restoreLastCommittedLine() {
	maxRow := r.maxContentRow()
	if len(r.outputBuffer) > 0 {
		termWriteAt(r.writer, maxRow, r.outputBuffer[len(r.outputBuffer)-1])
	} else {
		termClearRow(r.writer, maxRow)
	}
}

// restoreCursor moves the terminal cursor back to the input area prompt position.
func (r *REPL) restoreCursor() {
	_, promptRow, _ := r.inputPosition()
	promptVisW := visibleWidth(r.buildPrompt())
	inputBeforeCursor := string(r.inputBuf[:r.cursorPos])
	cursorCol := promptVisW + displayWidth(inputBeforeCursor) + 1
	termMoveTo(r.writer, promptRow, cursorCol)
	r.writer.Flush()
}

// wrapLineToWidth splits a line into multiple lines, each fitting within maxW
// visible columns. ANSI escape codes are preserved and carried across splits.
// This prevents terminal-level wrapping which breaks the scroll mechanism.
func wrapLineToWidth(line string, maxW int) []string {
	return termwidth.WrapLine(line, maxW)
}
