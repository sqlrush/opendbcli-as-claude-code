/*-------------------------------------------------------------------------
 *
 * repl_welcome.go
 *	  Welcome banner shown when the REPL starts — connection summary,
 *	  active model, hints. Uses cursor-absolute positioning so a tall
 *	  banner doesn't cause spurious scroll-up at launch.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_welcome.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/version"
)

// ── Welcome Page ──────────────────────────────────────────────

// buildWelcomeLines builds the welcome page content lines.
func (r *REPL) buildWelcomeLines() []string {
	w := r.cols
	if w < 60 {
		w = 60
	}

	innerTotal := w - 7 // 7 = 3 borders (│) + 4 spaces
	leftW := innerTotal * 45 / 100
	rightW := innerTotal - leftW

	wBorder := accentStyle

	br := brand.Current()
	// Top-banner: "{AppName} v{X.Y.ZZ}  ·  {Authorship}"
	// Brand layer drives Authorship: opendb → "by SQLRush | sqlrush@gmail.com",
	// dbaa → "by 系统六部", linkdb → "by 仁合时创".
	// Whole top border (incl. label) renders in single accent color so the
	// border characters and the label visually unify. On narrow terminals
	// authorship drops to keep banner clean.
	ver := br.AppName + " " + version.Version
	topLabel := "─── " + ver + " "
	if br.Authorship != "" {
		attribution := " ·  " + br.Authorship + " "
		// Only attach attribution if the resulting line still has ≥ 8 fill chars
		if w-displayWidth(topLabel)-displayWidth(attribution)-2 >= 8 {
			topLabel = topLabel + attribution
		}
	}
	topFill := w - displayWidth(topLabel) - 2
	if topFill < 0 {
		topFill = 0
	}
	topBorder := wBorder.Render("╭" + topLabel + strings.Repeat("─", topFill) + "╮")
	botBorder := wBorder.Render("╰" + strings.Repeat("─", w-2) + "╯")

	// ASCII logo from brand layer (default = OPENDB, dbaa = DBAA art).
	logoLine1 := br.LogoLines[0]
	logoLine2 := br.LogoLines[1]
	logoLine3 := br.LogoLines[2]
	logoVisW1 := displayWidth(logoLine1)
	logoVisW2 := displayWidth(logoLine2)
	logoVisW3 := displayWidth(logoLine3)

	// White style for welcome page text (instead of dim gray).
	wText := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))

	// Current working directory for display.
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}

	type leftEntry struct {
		text   string
		visW   int
		center bool // true = center, false = left-align with group
	}

	// Calculate left-align indent: align logo and welcome title to same left edge.
	welcomeW := visibleWidth(titleStyle.Render(br.WelcomeTitle))
	groupMaxW := welcomeW
	for _, lw := range []int{logoVisW1, logoVisW2, logoVisW3} {
		if lw > groupMaxW {
			groupMaxW = lw
		}
	}
	groupIndent := (leftW - groupMaxW) / 2

	leftTexts := []leftEntry{
		{"", -1, false},
		{titleStyle.Render(br.WelcomeTitle), -1, true},
		{"", -1, false},
		// Logo lines centered (was left-aligned via false). v1.1.20:
		// dbaa welcome title (中国农业银行数据库智能体) is wider than the
		// 4-letter DBAA logo, so left-align made the logo stick to the
		// title's left edge. Center align gives a balanced appearance.
		{accentStyle.Render(logoLine1), logoVisW1, true},
		{accentStyle.Render(logoLine2), logoVisW2, true},
		{accentStyle.Render(logoLine3), logoVisW3, true},
		{"", -1, false},
		{wText.Render(br.Tagline), -1, true},
		{wText.Render(br.DBList), -1, true},
	}

	numRows := 9
	rightSep := wBorder.Render(strings.Repeat("─", rightW))

	// Build top half (quick start) and bottom half (connections) separately,
	// then insert separator at the exact midpoint.
	topHalf := []string{
		accentStyle.Render("快速入门"),
		wText.Render("/login  连接数据库"),
		wText.Render("/llm    基于模型交互"),
		wText.Render("/rule   基于规则交互"),
	}

	var botHalf []string
	// Build connection list ordered by most recently used.
	recentNames := r.connMgr.RecentConnections()
	allConns := r.connMgr.ListConnections()
	// Build ordered list: recent first, then remaining.
	seen := make(map[string]bool)
	var orderedConns []config.Connection
	for _, name := range recentNames {
		for _, c := range allConns {
			if c.Name == name && !seen[name] {
				orderedConns = append(orderedConns, c)
				seen[name] = true
			}
		}
	}
	for _, c := range allConns {
		if !seen[c.Name] {
			orderedConns = append(orderedConns, c)
		}
	}
	if len(orderedConns) > 0 {
		// "已配置连接" — was "最近使用连接" (v1.1.20). Title now matches the
		// actual content (all configured connections, sorted by last use)
		// instead of misleading users to expect only ones they /login'd to.
		botHalf = append(botHalf, accentStyle.Render("已配置连接"))
		const colName, colType, colAddr, colPort = 9, 12, 14, 6
		hdr := padRightDW("名称", colName) + padRightDW("类型", colType) + padRightDW("地址", colAddr) + padRightDW("端口", colPort) + "用户"
		botHalf = append(botHalf, wText.Render(hdr))
		maxConns := numRows - len(topHalf) - 1 - 2 // 1 sep + 2 (title+header)
		if maxConns < 1 {
			maxConns = 1
		}
		for i, c := range orderedConns {
			if i >= maxConns {
				break
			}
			dbType := officialDBTypeName(c.DBType)
			portStr := fmt.Sprintf("%d", c.Port)
			row := padRightDW(c.Name, colName) + padRightDW(dbType, colType) + padRightDW(c.Host, colAddr) + padRightDW(portStr, colPort) + c.User
			botHalf = append(botHalf, textStyle.Render(row))
		}
	} else {
		botHalf = append(botHalf, accentStyle.Render("已配置连接"))
		botHalf = append(botHalf, wText.Render("暂无保存的连接"))
	}

	// Place separator at absolute midpoint: (numRows-1)/2 rows above, rest below.
	sepIndex := numRows / 2 // index 4 for 9 rows → row 5 (1-based)

	rightTexts := make([]string, 0, numRows)
	for i := 0; i < sepIndex; i++ {
		if i < len(topHalf) {
			rightTexts = append(rightTexts, topHalf[i])
		} else {
			rightTexts = append(rightTexts, "")
		}
	}
	rightTexts = append(rightTexts, rightSep)
	for i := 0; i < numRows-sepIndex-1; i++ {
		if i < len(botHalf) {
			rightTexts = append(rightTexts, botHalf[i])
		} else {
			rightTexts = append(rightTexts, "")
		}
	}
	for len(rightTexts) < numRows {
		rightTexts = append(rightTexts, "")
	}

	var lines []string
	lines = append(lines, topBorder)

	for i := 0; i < numRows; i++ {
		leftContent := ""
		lVis := 0
		if i < len(leftTexts) {
			leftContent = leftTexts[i].text
			if leftTexts[i].visW >= 0 {
				lVis = leftTexts[i].visW
			} else {
				lVis = visibleWidth(leftContent)
			}
		}
		// Truncate left content if wider than leftW.
		if lVis > leftW {
			leftContent = truncateToWidth(leftContent, leftW-2) + ".."
			lVis = visibleWidth(leftContent)
		}
		lPadTotal := leftW - lVis
		if lPadTotal < 0 {
			lPadTotal = 0
		}
		var lPadLeft, lPadRight int
		if i < len(leftTexts) && leftTexts[i].center {
			// Center alignment.
			lPadLeft = lPadTotal / 2
			lPadRight = lPadTotal - lPadLeft
		} else {
			// Left-align with group indent (cat logo + "欢迎回来!" aligned).
			lPadLeft = groupIndent
			lPadRight = lPadTotal - lPadLeft
			if lPadRight < 0 {
				lPadRight = 0
			}
		}

		rightContent := ""
		if i < len(rightTexts) {
			rightContent = rightTexts[i]
		}
		rVis := visibleWidth(rightContent)
		if rVis > rightW {
			rightContent = truncateToWidth(rightContent, rightW-2) + ".."
			rVis = visibleWidth(rightContent)
		}
		rPad := rightW - rVis
		if rPad < 0 {
			rPad = 0
		}

		line := fmt.Sprintf("%s %s%s%s %s %s%s %s",
			wBorder.Render("│"),
			strings.Repeat(" ", lPadLeft),
			leftContent,
			strings.Repeat(" ", lPadRight),
			wBorder.Render("│"),
			rightContent,
			strings.Repeat(" ", rPad),
			wBorder.Render("│"),
		)
		lines = append(lines, line)
	}

	lines = append(lines, botBorder)
	return lines
}

// printWelcomeInPlace prints the welcome page without clearing the screen.
// It scrolls the terminal to make room, then draws with absolute positioning
// to avoid the ANSI+newline blank-line bug.
func (r *REPL) printWelcomeInPlace() {
	lines := r.buildWelcomeLines()
	need := len(lines) + inputHeight // welcome lines + input area

	// Query where the cursor is now.
	startRow := r.queryCursorRow()

	// How many rows we need below startRow (inclusive).
	endRow := startRow + need - 1
	if endRow > r.rows {
		// Scroll terminal down to make room by emitting blank \r\n's.
		scroll := endRow - r.rows
		for i := 0; i < scroll; i++ {
			fmt.Fprint(r.writer, "\r\n")
		}
		startRow -= scroll
		if startRow < 1 {
			startRow = 1
		}
	}

	// Draw welcome lines with absolute positioning (no blank-line bug).
	maxRow := r.maxContentRow()
	for i, line := range lines {
		row := startRow + i
		if row > maxRow {
			break
		}
		termWriteAt(r.writer, row, line)
	}

	written := len(lines)
	if startRow+written-1 > maxRow {
		written = maxRow - startRow + 1
	}
	r.contentRow = startRow + written
}

// printWelcomePositioned prints the welcome page using cursor positioning.
// Used by /clear when the screen has been cleared and we start from row 1.
func (r *REPL) printWelcomePositioned() {
	lines := r.buildWelcomeLines()

	maxRow := r.maxContentRow()
	for i, line := range lines {
		row := r.contentRow + i
		if row > maxRow {
			break
		}
		termWriteAt(r.writer, row, line)
	}

	written := len(lines)
	if r.contentRow+written-1 > maxRow {
		written = maxRow - r.contentRow + 1
	}
	r.contentRow += written
}

