/*-------------------------------------------------------------------------
 *
 * format.go
 *	  RenderLLMContent transforms LLM markdown-like output into
 *	  terminal-formatted text.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/format.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
)

// ANSI escape codes for terminal styling.
const (
	ansiBold      = "\033[1m"
	ansiDim       = "\033[2m"
	ansiReset     = "\033[0m"
	ansiCyan      = "\033[36m"
	ansiYellow    = "\033[33m"
	ansiGreen     = "\033[32m"
	ansiRed       = "\033[31m"
	ansiBoldCyan  = "\033[1;36m"
	ansiBoldRed   = "\033[1;31m"
	ansiBoldGreen = "\033[1;32m"
)

// headerRe matches markdown headers (## or ###).
var headerRe = regexp.MustCompile(`^(#{1,4})\s+(.+)$`)

// codeBlockRe matches opening/closing ```.
var codeBlockStartRe = regexp.MustCompile("^```(\\w*)$")

// bulletRe matches bullet list items (- or *).
var bulletRe = regexp.MustCompile(`^(\s*)[-*]\s+(.+)$`)

// numberedRe matches numbered list items (1. 2. etc).
var numberedRe = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.+)$`)

// metricRe matches inline metric patterns like "从 X → Y" or "X -> Y".
var metricRe = regexp.MustCompile(`(\d+[\d.]*)\s*(?:→|->)\s*(\d+[\d.]*)`)

// sqlIDRe matches 13-char alphanumeric strings (Oracle SQL_ID format).
var sqlIDRe = regexp.MustCompile(`[A-Za-z0-9]{13}`)

// slashCmdLineRe matches a line that is primarily a /command (possibly with args).
// Matches lines like "/resize TEMP autoextend on maxsize 4G" or "/kill 142".
var slashCmdLineRe = regexp.MustCompile(`^\s*/[a-zA-Z][\w-]*\s*.*$`)

// RenderLLMContent transforms LLM markdown-like output into terminal-formatted text.
func RenderLLMContent(content string) string {
	return RenderLLMContentWidth(content, 0)
}

// RenderLLMContentWidth transforms LLM markdown-like output into terminal-formatted
// text with ANSI styling. termWidth controls code block border alignment (0 = no alignment).
func RenderLLMContentWidth(content string, termWidth int) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	inCodeBlock := false
	codeLang := ""
	lastBlank := false // track consecutive blank lines
	// Collect code block lines so we can compute max width for borders.
	var codeLines []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Handle code blocks.
		if m := codeBlockStartRe.FindStringSubmatch(line); m != nil && !inCodeBlock {
			inCodeBlock = true
			codeLang = m[1]
			codeLines = nil
			lastBlank = false
			continue
		}
		if line == "```" && inCodeBlock {
			inCodeBlock = false
			// Render the collected code block with aligned borders.
			renderCodeBlock(&b, codeLang, codeLines, termWidth)
			codeLang = ""
			codeLines = nil
			lastBlank = false
			continue
		}
		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}

		// Markdown tables: collect consecutive | lines and render aligned.
		if isTableRow(line) {
			var tableLines []string
			for i < len(lines) && isTableRow(lines[i]) {
				tableLines = append(tableLines, lines[i])
				i++
			}
			i-- // back up one since the for loop will increment
			renderMarkdownTable(&b, tableLines)
			lastBlank = false
			continue
		}

		// Markdown headers → bold section markers.
		// Headers already include a leading \n, so suppress preceding blank line.
		if m := headerRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			title := m[2]
			switch level {
			case 1:
				b.WriteString(fmt.Sprintf("\n%s═══ %s ═══%s\n", ansiBoldCyan, title, ansiReset))
			case 2:
				b.WriteString(fmt.Sprintf("\n%s■ %s%s\n", ansiBold, title, ansiReset))
			case 3:
				b.WriteString(fmt.Sprintf("%s  ▸ %s%s\n", ansiCyan, title, ansiReset))
			default:
				b.WriteString(fmt.Sprintf("    %s%s%s\n", ansiCyan, title, ansiReset))
			}
			lastBlank = false
			continue
		}

		// Bullet list items.
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			indent := m[1]
			text := highlightKeyTerms(m[2])
			b.WriteString(fmt.Sprintf("%s  • %s\n", indent, text))
			lastBlank = false
			continue
		}

		// Numbered list items.
		if m := numberedRe.FindStringSubmatch(line); m != nil {
			indent := m[1]
			num := m[2]
			text := highlightKeyTerms(m[3])
			b.WriteString(fmt.Sprintf("%s  %s. %s\n", indent, num, text))
			lastBlank = false
			continue
		}

		// Empty lines → allow at most one consecutive blank line.
		if strings.TrimSpace(line) == "" {
			if !lastBlank {
				b.WriteString("\n")
				lastBlank = true
			}
			continue
		}

		// Standalone /command lines outside code blocks → auto-box them.
		trimmed := strings.TrimSpace(line)
		if slashCmdLineRe.MatchString(trimmed) {
			renderCodeBlock(&b, "", []string{trimmed}, termWidth)
			lastBlank = false
			continue
		}

		// Regular text → highlight key terms and metric transitions.
		rendered := highlightKeyTerms(line)
		rendered = highlightMetrics(rendered)
		b.WriteString(fmt.Sprintf("  %s\n", rendered))
		lastBlank = false
	}

	return b.String()
}

// RenderThinkingChain formats the reasoning rounds as a compact chain.
func RenderThinkingChain(rounds []RoundInfo) string {
	if len(rounds) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s▸ 推理过程 (%d轮)%s\n", ansiDim, len(rounds), ansiReset))

	for _, round := range rounds {
		marker := "○"
		if round.Action != "" {
			marker = "●"
		}
		b.WriteString(fmt.Sprintf("%s  %s 第%d轮: %s%s\n", ansiDim, marker, round.Round, round.Summary, ansiReset))

		if round.Thinking != "" {
			preview := condenseThinking(round.Thinking, 100)
			b.WriteString(fmt.Sprintf("%s    %s%s\n", ansiDim, preview, ansiReset))
		}
	}

	return b.String()
}

// RenderTokenUsage formats token usage as a compact footer.
func RenderTokenUsage(usage llmUsage) string {
	if usage.InputTokens == 0 {
		return ""
	}
	return fmt.Sprintf("%s[tokens: in=%d out=%d]%s\n",
		ansiDim, usage.InputTokens, usage.OutputTokens, ansiReset)
}

// llmUsage is a local type alias to avoid import cycle.
// Callers pass llm.Usage which has the same fields.
type llmUsage = struct {
	InputTokens  int
	OutputTokens int
}

// highlightKeyTerms applies ANSI emphasis to important database/diagnosis terms.
func highlightKeyTerms(text string) string {
	// Highlight SQL_ID patterns (pre-compiled regex).
	text = sqlIDRe.ReplaceAllStringFunc(text, func(match string) string {
		if looksLikeSQLID(match) {
			return ansiBoldCyan + match + ansiReset
		}
		return match
	})

	// Highlight key Chinese terms.
	for _, term := range []string{"根因", "瓶颈", "阻塞", "死锁", "冲高", "异常"} {
		if strings.Contains(text, term) {
			text = strings.ReplaceAll(text, term, ansiBoldRed+term+ansiReset)
		}
	}

	// Highlight remediation terms.
	for _, term := range []string{"建议", "修复", "优化", "索引", "解决"} {
		if strings.Contains(text, term) {
			text = strings.ReplaceAll(text, term, ansiBoldGreen+term+ansiReset)
		}
	}

	return text
}

// highlightMetrics highlights metric transition patterns (X → Y).
func highlightMetrics(text string) string {
	return metricRe.ReplaceAllStringFunc(text, func(match string) string {
		return ansiYellow + match + ansiReset
	})
}

// looksLikeSQLID checks if a 13-char string looks like an Oracle SQL_ID.
func looksLikeSQLID(s string) bool {
	if utf8.RuneCountInString(s) != 13 {
		return false
	}
	hasDigit := false
	hasLetter := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			hasDigit = true
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
		} else {
			return false
		}
	}
	return hasDigit && hasLetter
}

// renderCodeBlock writes a boxed code block with optional language label.
// The box width adapts to the content width (not the terminal width).
// Lines wider than the terminal are truncated to prevent border wrapping.
func renderCodeBlock(b *strings.Builder, lang string, lines []string, termWidth int) {
	// leftPad is the visual indent before the box.
	const leftPad = "  "
	// borderExtra accounts for "│ " prefix and " │" suffix (4 chars).
	const borderExtra = 4

	// Compute widest content line (visible display width, CJK-aware).
	maxContent := 0
	for _, l := range lines {
		w := displayWidth(l)
		if w > maxContent {
			maxContent = w
		}
	}

	// Box inner width = max(content width, lang label width).
	innerWidth := maxContent
	if displayWidth(lang) > innerWidth {
		innerWidth = displayWidth(lang)
	}

	// Minimum inner width.
	if innerWidth < 10 {
		innerWidth = 10
	}

	// Cap inner width so the total box fits within the terminal.
	if termWidth > 0 {
		maxInner := termWidth - displayWidth(leftPad) - borderExtra
		if maxInner < 10 {
			maxInner = 10
		}
		if innerWidth > maxInner {
			innerWidth = maxInner
		}
	}

	// ┌─ lang ─...─┐
	header := "─"
	if lang != "" {
		header = "─ " + lang + " "
	}
	headerRunes := displayWidth(header)
	remaining := innerWidth + 2 - headerRunes // +2 to match "│ " and " │" in content lines
	if remaining < 0 {
		remaining = 0
	}
	b.WriteString(fmt.Sprintf("%s%s┌%s%s┐\n", leftPad, ansiDim, header+strings.Repeat("─", remaining), ansiReset))

	// │ content ... │
	for _, l := range lines {
		lw := displayWidth(l)
		// Truncate lines wider than innerWidth.
		if lw > innerWidth {
			l = truncateToWidth(l, innerWidth-2) + ".."
			lw = displayWidth(l)
		}
		pad := innerWidth - lw
		if pad < 0 {
			pad = 0
		}
		b.WriteString(fmt.Sprintf("%s%s│%s %s%s %s│%s\n",
			leftPad, ansiDim, ansiReset,
			l, strings.Repeat(" ", pad),
			ansiDim, ansiReset))
	}

	// └─...─┘
	b.WriteString(fmt.Sprintf("%s%s└%s┘%s\n", leftPad, ansiDim, strings.Repeat("─", innerWidth+2), ansiReset))
}

// isTableRow returns true if the line looks like a markdown table row (starts and ends with |).
func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

// isTableSeparator returns true if the line is a table separator (|---|---|).
func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	for _, r := range trimmed {
		if r != '|' && r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return true
}

// parseTableCells splits a markdown table row into cell values.
func parseTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	// Remove leading and trailing |.
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// renderMarkdownTable renders a markdown table using the same box-drawing
// format as the SQL result table (format.FormatTable).
func renderMarkdownTable(b *strings.Builder, lines []string) {
	if len(lines) == 0 {
		return
	}

	// Parse all rows (skip separator lines).
	var rows [][]string
	for _, line := range lines {
		if isTableSeparator(line) {
			continue
		}
		rows = append(rows, parseTableCells(line))
	}
	if len(rows) < 2 {
		// Need at least header + 1 data row.
		return
	}

	// First row is the header (column names), rest are data.
	columns := rows[0]
	dataRows := make([][]any, len(rows)-1)
	for i, row := range rows[1:] {
		anyRow := make([]any, len(row))
		for j, cell := range row {
			anyRow[j] = cell
		}
		dataRows[i] = anyRow
	}

	qr := &db.QueryResult{
		Columns: columns,
		Rows:    dataRows,
	}
	b.WriteString(format.FormatTable(qr))
}

// displayWidth returns the visual display width of a string,
// accounting for wide CJK characters (2 columns each).
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
			(r >= 0x2e80 && r <= 0x303e) || (r >= 0x3040 && r <= 0x33bf) ||
			(r >= 0x3400 && r <= 0x4dbf) || (r >= 0x4e00 && r <= 0xa4cf) ||
			(r >= 0xa960 && r <= 0xa97c) || (r >= 0xac00 && r <= 0xd7a3) ||
			(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe10 && r <= 0xfe6f) ||
			(r >= 0xff01 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6) ||
			(r >= 0x1f300 && r <= 0x1f9ff) ||
			(r >= 0x20000 && r <= 0x2fffd) || (r >= 0x30000 && r <= 0x3fffd)) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// truncateToWidth truncates a string to fit within maxW display columns.
func truncateToWidth(s string, maxW int) string {
	w := 0
	for i, r := range s {
		rw := 1
		if r >= 0x1100 {
			rw = 2
		}
		if w+rw > maxW {
			return s[:i]
		}
		w += rw
	}
	return s
}

// ExtractSections parses LLM output into named sections based on ## headers.
// Returns a map of section name → content for programmatic access.
func ExtractSections(content string) map[string]string {
	sections := make(map[string]string)
	lines := strings.Split(content, "\n")

	currentSection := ""
	var sectionContent strings.Builder

	for _, line := range lines {
		if m := headerRe.FindStringSubmatch(line); m != nil {
			// Save previous section.
			if currentSection != "" {
				sections[currentSection] = strings.TrimSpace(sectionContent.String())
			}
			currentSection = m[2]
			sectionContent.Reset()
			continue
		}
		if currentSection != "" {
			sectionContent.WriteString(line + "\n")
		}
	}

	// Save last section.
	if currentSection != "" {
		sections[currentSection] = strings.TrimSpace(sectionContent.String())
	}

	return sections
}
