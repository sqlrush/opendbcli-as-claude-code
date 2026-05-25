/*-------------------------------------------------------------------------
 *
 * styles.go
 *	  InfoPanel renders a bordered panel with title — clack style
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/styles.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"github.com/charmbracelet/lipgloss"
)

// Color palette — unified orange #CC7832 matching OpenDB REPL welcome page
var (
	ColorPrimary   = lipgloss.Color("#CC7832") // orange — brand accent, borders, titles
	ColorSecondary = lipgloss.Color("#CC7832") // orange — info panels (same as primary)
	ColorSuccess   = lipgloss.Color("#22C55E") // green — checkmarks
	ColorWarning   = lipgloss.Color("#EAB308") // yellow — warnings
	ColorError     = lipgloss.Color("#EF4444") // red — errors
	ColorDim       = lipgloss.Color("#6B7280") // gray — secondary text
	ColorText      = lipgloss.Color("#F9FAFB") // white — primary text
	ColorHighlight = lipgloss.Color("#CC7832") // orange — highlights (same as primary)
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

	// Completed step symbol
	StyleCompleted = lipgloss.NewStyle().
			Foreground(ColorDim)
)

// InfoPanel renders a bordered panel with title — clack style
func InfoPanel(title, content string) string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(90)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary)

	return titleStyle.Render("◇  "+title) + "\n" + border.Render(content)
}

// SuccessLine renders ✓ text in green
func SuccessLine(text string) string {
	return "  " + StyleSuccess.Render("✓") + " " + text
}

// WarningLine renders ⚠ text in yellow
func WarningLine(text string) string {
	return "  " + StyleWarning.Render("⚠") + " " + text
}

// ErrorLine renders ✗ text in red
func ErrorLine(text string) string {
	return "  " + StyleError.Render("✗") + " " + text
}

// BulletLine renders · text
func BulletLine(text string) string {
	return "  " + StyleDim.Render("·") + " " + text
}

// CompletedLine renders a folded completed step (◇ style)
func CompletedLine(title, value string) string {
	return StyleDim.Render("◇  "+title) + "\n" + StyleDim.Render("│  "+value)
}

// DBDisplayName returns the canonical display name for a database type
func DBDisplayName(dbType string) string {
	switch dbType {
	case "oracle":
		return "Oracle"
	case "mysql":
		return "MySQL"
	case "postgres":
		return "PostgreSQL"
	case "opengauss":
		return "openGauss"
	case "gaussdb":
		return "GaussDB"
	default:
		return dbType
	}
}
