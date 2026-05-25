/*-------------------------------------------------------------------------
 *
 * markdown.go
 *	  Markdown renderer for /llm output — converts model markdown
 *	  (headings, fenced code, tables, inline emphasis) to ANSI-styled
 *	  terminal output via chroma for syntax highlighting.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/markdown.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
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
	ansiBoldCyan  = "\033[1;36m"
	ansiBoldRed   = "\033[1;31m"
	ansiBoldGreen = "\033[1;32m"
	ansiOrange    = "\033[38;5;208m" // accent color for section headers
)

// Regex patterns for markdown element detection.
var (
	mdHeaderRe       = regexp.MustCompile(`^(#{1,4})\s+(.+)$`)
	mdCodeStartRe    = regexp.MustCompile("^\\s*```(\\w*)\\s*$")
	mdBulletRe       = regexp.MustCompile(`^(\s*)[-*]\s+(.+)$`)
	mdNumberedRe     = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.+)$`)
	mdBoldRe         = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdMetricRe       = regexp.MustCompile(`(\d+[\d.]*)\s*(?:→|->)\s*(\d+[\d.]*)`)
	mdSQLIDRe        = regexp.MustCompile(`[A-Za-z0-9]{13}`)
	mdInlineCodeRe   = regexp.MustCompile("`([^`]+)`")
	mdSlashCmdLineRe = regexp.MustCompile(`^\s*/[a-zA-Z][\w-]*\s*.*$`)
)

// diagStreamFormatter applies markdown formatting to streaming lines.
// It tracks state across lines for multi-line constructs (code blocks, tables).
type diagStreamFormatter struct {
	inCodeBlock bool
	codeLang    string
	codeLines   []string
	tableLines  []string
	termWidth   int
	inAction    bool // inside ```action block (suppressed)
	lastBlank   bool // track consecutive blank lines
}

// newDiagStreamFormatter creates a formatter for the given terminal width.
func newDiagStreamFormatter(termWidth int) *diagStreamFormatter {
	return &diagStreamFormatter{termWidth: termWidth}
}

// formatLine processes one line and returns formatted output lines.
// May return 0 lines (buffering multi-line construct) or multiple lines.
func (f *diagStreamFormatter) formatLine(line string) []string {
	// Handle code block boundaries.
	if m := mdCodeStartRe.FindStringSubmatch(line); m != nil && !f.inCodeBlock {
		// Flush any pending table before entering code block.
		var result []string
		if len(f.tableLines) > 0 {
			result = f.flushTable()
		}
		f.inCodeBlock = true
		f.codeLang = m[1]
		f.codeLines = nil
		f.inAction = (f.codeLang == "action")
		return result
	}
	// Also catch standalone ``` lines that aren't inside a code block — just skip them.
	if strings.TrimSpace(line) == "```" && !f.inCodeBlock {
		return nil // orphan closing marker, discard
	}
	if strings.TrimSpace(line) == "```" && f.inCodeBlock {
		f.inCodeBlock = false
		if f.inAction {
			f.inAction = false
			f.codeLines = nil
			return nil
		}
		var b strings.Builder
		f.renderCodeBlock(&b)
		f.codeLang = ""
		f.codeLines = nil
		text := strings.TrimRight(b.String(), "\n")
		if text == "" {
			return nil
		}
		return strings.Split(text, "\n")
	}
	if f.inCodeBlock {
		if !f.inAction {
			f.codeLines = append(f.codeLines, line)
		}
		return nil
	}

	// Handle table accumulation.
	if mdIsTableRow(line) {
		f.tableLines = append(f.tableLines, line)
		return nil
	}

	// Non-table line after table lines → flush table first.
	var result []string
	if len(f.tableLines) > 0 {
		result = append(result, f.flushTable()...)
		f.lastBlank = false
	}

	// Suppress consecutive blank lines — allow at most one.
	if strings.TrimSpace(line) == "" {
		if f.lastBlank {
			return result // already have a blank line, skip this one
		}
		f.lastBlank = true
		result = append(result, "")
		return result
	}
	f.lastBlank = false

	// Standalone /command lines → auto-box them.
	trimmed := strings.TrimSpace(line)
	if mdSlashCmdLineRe.MatchString(trimmed) {
		var box strings.Builder
		// Temporarily set codeLines to render the box.
		savedLines := f.codeLines
		savedLang := f.codeLang
		f.codeLines = []string{trimmed}
		f.codeLang = ""
		f.renderCodeBlock(&box)
		f.codeLines = savedLines
		f.codeLang = savedLang
		text := strings.TrimRight(box.String(), "\n")
		if text != "" {
			result = append(result, strings.Split(text, "\n")...)
		}
		return result
	}

	// Format the current line. Headers may include a leading \n for visual
	// spacing — split embedded newlines so each entry maps to one
	// writeOutputLine call (which cannot handle embedded \n in scroll mode).
	formatted := f.formatSingleLine(line)
	result = append(result, strings.Split(formatted, "\n")...)
	return result
}

// flush returns any remaining buffered content (call at end of stream).
func (f *diagStreamFormatter) flush() []string {
	var result []string
	if f.inCodeBlock && len(f.codeLines) > 0 {
		// Render unclosed code blocks (even action blocks at stream end —
		// an unclosed action block likely means the closing ``` was lost
		// during streaming, so show the content rather than silently discard).
		if !f.inAction {
			var b strings.Builder
			f.renderCodeBlock(&b)
			text := strings.TrimRight(b.String(), "\n")
			if text != "" {
				result = append(result, strings.Split(text, "\n")...)
			}
		}
	}
	f.inCodeBlock = false
	f.inAction = false
	f.codeLines = nil

	if len(f.tableLines) > 0 {
		result = append(result, f.flushTable()...)
	}
	return result
}

// flushTable renders buffered table lines as a formatted table.
func (f *diagStreamFormatter) flushTable() []string {
	if len(f.tableLines) == 0 {
		return nil
	}
	lines := f.tableLines
	f.tableLines = nil

	// Parse rows (skip separator lines).
	var rows [][]string
	for _, line := range lines {
		if mdIsTableSeparator(line) {
			continue
		}
		rows = append(rows, mdParseTableCells(line))
	}
	if len(rows) < 2 {
		// Not enough for header + data — return raw lines formatted.
		var result []string
		for _, line := range lines {
			result = append(result, "  "+line)
		}
		return result
	}

	// Build QueryResult for format.FormatTable.
	// Strip markdown inline formatting (backticks, bold) from cells so
	// table width calculation uses clean text.
	columns := rows[0]
	dataRows := make([][]any, len(rows)-1)
	for i, row := range rows[1:] {
		anyRow := make([]any, len(row))
		for j, cell := range row {
			// Strip backtick code markers and ** bold markers.
			cell = strings.ReplaceAll(cell, "`", "")
			cell = strings.ReplaceAll(cell, "**", "")
			anyRow[j] = cell
		}
		// Pad short rows.
		for j := len(row); j < len(columns); j++ {
			anyRow = append(anyRow, "")
		}
		dataRows[i] = anyRow
	}
	// Also clean column headers.
	for i, col := range columns {
		columns[i] = strings.ReplaceAll(strings.ReplaceAll(col, "`", ""), "**", "")
	}

	// Use reduced terminal width (account for left indent from visual bars).
	tableTermWidth := f.termWidth - 4
	if tableTermWidth < 40 {
		tableTermWidth = 40
	}

	qr := &db.QueryResult{Columns: columns, Rows: dataRows}
	table := format.FormatTableOpts(qr, format.TableOptions{
		MaxRows:   50,
		TermWidth: tableTermWidth,
	})

	// Fall back to vertical key-value format when:
	// 1. Any rendered line overflows terminal width, OR
	// 2. Cell content is heavily truncated (contains "..." from fitWidths).
	// This ensures wide tables are readable on narrow terminals.
	shouldVertical := false
	for _, line := range strings.Split(table, "\n") {
		if displayWidth(line) > f.termWidth-2 {
			shouldVertical = true
			break
		}
	}
	if !shouldVertical && len(columns) >= 3 {
		// Check if any data cell was truncated by FormatTableOpts.
		// Compare original cell length against rendered column widths.
		for _, row := range dataRows {
			for _, cell := range row {
				s := fmt.Sprintf("%v", cell)
				if len(s) > 40 && displayWidth(s) > tableTermWidth/len(columns) {
					shouldVertical = true
					break
				}
			}
			if shouldVertical {
				break
			}
		}
	}
	if shouldVertical {
		return f.renderTableVertical(columns, dataRows)
	}

	text := strings.TrimRight(table, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// renderTableVertical renders a table in key-value format when horizontal
// rendering overflows the terminal width.
func (f *diagStreamFormatter) renderTableVertical(columns []string, dataRows [][]any) []string {
	var result []string
	for i, row := range dataRows {
		// Section header.
		header := fmt.Sprintf("  %s─── %d/%d", ansiDim, i+1, len(dataRows))
		if len(row) > 0 {
			if first, ok := row[0].(string); ok && first != "" {
				header += " " + first
			}
		}
		header += fmt.Sprintf(" ──%s", ansiReset)
		result = append(result, header)

		// Key-value pairs (skip first column if used in header).
		startCol := 0
		if len(row) > 0 {
			if first, ok := row[0].(string); ok && first != "" {
				startCol = 1
			}
		}
		for j := startCol; j < len(row) && j < len(columns); j++ {
			val := fmt.Sprintf("%v", row[j])
			if val == "" {
				continue
			}
			colName := columns[j]
			result = append(result, fmt.Sprintf("    %s%s:%s %s", ansiBold, colName, ansiReset, val))
		}
		result = append(result, "") // blank line between rows
	}
	return result
}

// renderCodeBlock writes a boxed code block with optional syntax highlighting.
// The box width adapts to the content (not the terminal width).
// Lines wider than the terminal are truncated to prevent border wrapping.
func (f *diagStreamFormatter) renderCodeBlock(b *strings.Builder) {
	// Single-line /command references: render inline (no box).
	if len(f.codeLines) == 1 {
		line := strings.TrimSpace(f.codeLines[0])
		if strings.HasPrefix(line, "/") {
			b.WriteString(fmt.Sprintf("  %s%s%s\n", ansiDim, line, ansiReset))
			return
		}
	}

	const leftPad = "  "
	const borderExtra = 4 // "│ " prefix + " │" suffix

	// Compute widest content line before wrapping.
	maxContent := 0
	for _, l := range f.codeLines {
		w := displayWidth(l)
		if w > maxContent {
			maxContent = w
		}
	}

	innerWidth := maxContent
	if displayWidth(f.codeLang) > innerWidth {
		innerWidth = displayWidth(f.codeLang)
	}
	if innerWidth < 10 {
		innerWidth = 10
	}

	// Cap inner width so the total box fits within the terminal.
	// Also hard-cap at 100 columns to prevent overly wide boxes.
	maxBox := 100
	if f.termWidth > 0 && f.termWidth < maxBox {
		maxBox = f.termWidth
	}
	maxInner := maxBox - displayWidth(leftPad) - borderExtra
	if maxInner < 10 {
		maxInner = 10
	}
	if innerWidth > maxInner {
		innerWidth = maxInner
	}

	// ┌─ lang ─...─┐
	header := "─"
	if f.codeLang != "" {
		header = "─ " + f.codeLang + " "
	}
	headerW := displayWidth(header)
	remaining := innerWidth + 2 - headerW // +2 to match "│ " and " │" in content lines
	if remaining < 0 {
		remaining = 0
	}
	b.WriteString(fmt.Sprintf("%s%s┌%s┐%s\n", leftPad, ansiDim, header+strings.Repeat("─", remaining), ansiReset))

	renderLines := wrapCodeBlockLines(f.codeLines, innerWidth)
	highlighted := highlightCode(f.codeLang, renderLines)

	for i, raw := range renderLines {
		rawW := displayWidth(raw)
		// Use highlighted version if available, otherwise raw.
		display := raw
		if i < len(highlighted) {
			display = highlighted[i]
		}
		pad := innerWidth - rawW
		if pad < 0 {
			pad = 0
		}
		b.WriteString(fmt.Sprintf("%s%s│%s %s%s%s %s│%s\n",
			leftPad, ansiDim, ansiReset,
			display, ansiReset, strings.Repeat(" ", pad),
			ansiDim, ansiReset))
	}

	b.WriteString(fmt.Sprintf("%s%s└%s┘%s\n", leftPad, ansiDim, strings.Repeat("─", innerWidth+2), ansiReset))
}

func wrapCodeBlockLines(lines []string, width int) []string {
	if width <= 0 {
		return append([]string(nil), lines...)
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if displayWidth(line) <= width {
			out = append(out, line)
			continue
		}
		wrapped := wrapLineToWidth(line, width)
		if len(wrapped) == 0 {
			out = append(out, line)
			continue
		}
		out = append(out, wrapped...)
	}
	return out
}

// highlightCode applies chroma syntax highlighting to code lines.
// Returns ANSI-colored lines parallel to input. Falls back to raw lines
// if the language is unknown or highlighting fails.
func highlightCode(lang string, lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	if strings.EqualFold(lang, "plan") {
		return highlightPlanRefs(lines)
	}

	// Find lexer by language name.
	lexer := lexers.Get(lang)
	if lexer == nil {
		// Try common aliases.
		switch strings.ToLower(lang) {
		case "psql", "plsql", "plpgsql":
			lexer = lexers.Get("sql")
		}
	}
	if lexer == nil {
		return lines // no lexer found, return raw
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	code := strings.Join(lines, "\n")
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return lines
	}

	// Render tokens to ANSI escape sequences.
	var buf strings.Builder
	for _, token := range iterator.Tokens() {
		ansiStyle := tokenToANSI(style, token.Type)
		if ansiStyle != "" {
			buf.WriteString(ansiStyle)
			buf.WriteString(token.Value)
			buf.WriteString(ansiReset)
		} else {
			buf.WriteString(token.Value)
		}
	}

	return strings.Split(buf.String(), "\n")
}

func highlightPlanRefs(lines []string) []string {
	out := make([]string, len(lines))
	re := regexp.MustCompile(`\[(P[0-9]+)\]`)
	for i, line := range lines {
		out[i] = re.ReplaceAllString(line, ansiBoldGreen+"[$1]"+ansiReset)
	}
	return out
}

// tokenToANSI converts a chroma token type to an ANSI escape sequence
// using the given style.
func tokenToANSI(s *chroma.Style, tt chroma.TokenType) string {
	entry := s.Get(tt)
	if entry.IsZero() {
		return ""
	}

	var codes []string
	if entry.Bold == chroma.Yes {
		codes = append(codes, "1")
	}
	if entry.Italic == chroma.Yes {
		codes = append(codes, "3")
	}
	if entry.Colour.IsSet() {
		r, g, b := entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue()
		codes = append(codes, fmt.Sprintf("38;2;%d;%d;%d", r, g, b))
	}
	if len(codes) == 0 {
		return ""
	}
	return fmt.Sprintf("\033[%sm", strings.Join(codes, ";"))
}

// formatSingleLine formats a regular (non-code, non-table) line.
func (f *diagStreamFormatter) formatSingleLine(line string) string {
	// Headers.
	if m := mdHeaderRe.FindStringSubmatch(line); m != nil {
		level := len(m[1])
		title := m[2]
		switch level {
		case 1:
			return fmt.Sprintf("\n%s═══ %s ═══%s", ansiBoldCyan, title, ansiReset)
		case 2:
			return fmt.Sprintf("\n  %s┃%s %s%s%s", ansiOrange, ansiReset, ansiBold, title, ansiReset)
		case 3:
			return fmt.Sprintf("  %s│%s %s▸ %s%s", ansiCyan, ansiReset, ansiCyan, title, ansiReset)
		default:
			return fmt.Sprintf("  %s│%s   %s%s%s", ansiDim, ansiReset, ansiCyan, title, ansiReset)
		}
	}

	// Bullet lists.
	if m := mdBulletRe.FindStringSubmatch(line); m != nil {
		indent := m[1]
		text := mdHighlightLine(m[2])
		return fmt.Sprintf("  %s│%s %s  • %s", ansiDim, ansiReset, indent, text)
	}

	// Numbered lists.
	if m := mdNumberedRe.FindStringSubmatch(line); m != nil {
		indent := m[1]
		num := m[2]
		text := mdHighlightLine(m[3])
		return fmt.Sprintf("  %s│%s %s  %s. %s", ansiDim, ansiReset, indent, num, text)
	}

	// Blockquote (> text).
	if strings.HasPrefix(strings.TrimSpace(line), "> ") {
		inner := strings.TrimPrefix(strings.TrimSpace(line), "> ")
		text := mdHighlightLine(inner)
		return fmt.Sprintf("  %s▎%s %s%s%s", ansiDim, ansiReset, ansiDim, text, ansiReset)
	}

	// Horizontal rule (--- or ***).
	trimmed := strings.TrimSpace(line)
	if (strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "***")) &&
		len(strings.ReplaceAll(strings.ReplaceAll(trimmed, "-", ""), "*", "")) == 0 {
		return fmt.Sprintf("  %s%s%s", ansiDim, strings.Repeat("─", 40), ansiReset)
	}

	// Empty lines.
	if trimmed == "" {
		return ""
	}

	// Regular text with subtle left bar for visual continuity.
	rendered := mdHighlightLine(line)
	return fmt.Sprintf("  %s│%s %s", ansiDim, ansiReset, rendered)
}

// mdHighlightLine applies inline markdown and key term highlighting.
// Handles nested formatting: `code` inside **bold** renders both correctly.
func mdHighlightLine(text string) string {
	// Step 1: Extract and protect inline code spans before bold processing.
	// Replace `code` with placeholders, process bold, then restore with styling.
	var codeSpans []string
	protected := mdInlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[1 : len(m)-1] // strip backticks
		idx := len(codeSpans)
		codeSpans = append(codeSpans, inner)
		return fmt.Sprintf("\x00CODE%d\x00", idx)
	})

	// Step 2: **bold** → ANSI bold (code spans are protected as placeholders).
	protected = mdBoldRe.ReplaceAllString(protected, ansiBold+"$1"+ansiReset)

	// Step 3: Restore code spans with styling.
	text = protected
	for i, span := range codeSpans {
		placeholder := fmt.Sprintf("\x00CODE%d\x00", i)
		text = strings.Replace(text, placeholder, ansiDim+span+ansiReset, 1)
	}

	// Metric transitions (X → Y) → yellow
	text = mdMetricRe.ReplaceAllStringFunc(text, func(m string) string {
		return ansiYellow + m + ansiReset
	})

	// Key Chinese terms.
	for _, term := range []string{"根因", "阻塞", "死锁", "冲高", "异常"} {
		if strings.Contains(text, term) {
			text = strings.ReplaceAll(text, term, ansiBoldRed+term+ansiReset)
		}
	}
	for _, term := range []string{"建议", "修复", "优化", "索引", "解决"} {
		if strings.Contains(text, term) {
			text = strings.ReplaceAll(text, term, ansiBoldGreen+term+ansiReset)
		}
	}

	return text
}

// mdIsTableRow returns true if the line looks like a markdown table row.
func mdIsTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

// mdIsTableSeparator returns true if the line is a table separator (|---|---|).
func mdIsTableSeparator(line string) bool {
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

// mdParseTableCells splits a markdown table row into cell values.
func mdParseTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}
