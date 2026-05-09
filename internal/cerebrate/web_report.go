/*-------------------------------------------------------------------------
 *
 * web_report.go
 *	  web_report.go provides report viewing API handlers and
 *	  markdown-to-HTML rendering. Used by the Cerebrate dashboard for
 *	  report list, detail, and HTML view endpoints.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/web_report.go
 *
 *-------------------------------------------------------------------------
 */
// web_report.go provides report viewing API handlers and markdown-to-HTML rendering.
// Used by the Cerebrate dashboard for report list, detail, and HTML view endpoints.
package cerebrate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// handleReportList serves GET /api/reports?limit=20&severity=critical
// Returns a JSON array of report entries from the ReportStore.
func (wd *WebDashboard) handleReportList(w http.ResponseWriter, r *http.Request) {
	if wd.reports == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*ReportEntry{})
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	severity := r.URL.Query().Get("severity")

	entries := wd.reports.List(limit, severity)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// handleReportDetail serves GET /api/reports/{id}
// Returns a single report as JSON (including the markdown field).
func (wd *WebDashboard) handleReportDetail(w http.ResponseWriter, r *http.Request) {
	id := extractPathSuffix(r.URL.Path, "/api/reports/")
	if id == "" || id == "html" {
		http.Error(w, "report id required", http.StatusBadRequest)
		return
	}

	// If path ends with /html, delegate to HTML handler.
	if strings.HasSuffix(id, "/html") {
		id = strings.TrimSuffix(id, "/html")
		wd.serveReportHTML(w, id)
		return
	}

	if wd.reports == nil {
		http.Error(w, "report store not configured", http.StatusInternalServerError)
		return
	}

	entry := wd.reports.Get(id)
	if entry == nil {
		http.Error(w, "report not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

// serveReportHTML renders a report's markdown as a self-contained HTML page.
func (wd *WebDashboard) serveReportHTML(w http.ResponseWriter, id string) {
	if wd.reports == nil {
		http.Error(w, "report store not configured", http.StatusInternalServerError)
		return
	}

	entry := wd.reports.Get(id)
	if entry == nil {
		http.Error(w, "report not found", http.StatusNotFound)
		return
	}

	htmlContent := markdownToHTML(entry.Markdown)
	page := fmt.Sprintf(reportHTMLTemplate, entry.Summary, htmlContent)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page)
}

// extractPathSuffix returns the path portion after the given prefix.
func extractPathSuffix(path, prefix string) string {
	if len(path) <= len(prefix) {
		return ""
	}
	return path[len(prefix):]
}

// markdownToHTML converts simple markdown to HTML.
// Supports headers (## / ###), code blocks (``` with copy button for SQL),
// tables (| ... |), and bullet lists (- / *).
// No external dependencies — simple line-by-line parsing.
func markdownToHTML(md string) string {
	lines := strings.Split(md, "\n")
	var b strings.Builder
	inCodeBlock := false
	codeLang := ""
	inTable := false
	inList := false
	codeBlockID := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Code block toggle.
		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				b.WriteString("</code></pre>\n")
				inCodeBlock = false
				codeLang = ""
				continue
			}
			inCodeBlock = true
			codeLang = strings.TrimPrefix(trimmed, "```")
			codeLang = strings.TrimSpace(codeLang)
			codeBlockID++
			cbID := fmt.Sprintf("code-block-%d", codeBlockID)
			if strings.EqualFold(codeLang, "sql") {
				b.WriteString(fmt.Sprintf(
					`<div style="position:relative"><button onclick="navigator.clipboard.writeText(document.getElementById('%s').textContent)" `+
						`style="position:absolute;right:8px;top:8px;padding:2px 8px;font-size:11px;cursor:pointer;background:#333;color:#ccc;border:1px solid #555;border-radius:4px">Copy</button>`+
						`<pre style="background:#1e1e2e;padding:12px;border-radius:6px;overflow-x:auto"><code id="%s">`,
					cbID, cbID))
			} else {
				b.WriteString(fmt.Sprintf(
					`<pre style="background:#1e1e2e;padding:12px;border-radius:6px;overflow-x:auto"><code id="%s">`, cbID))
			}
			continue
		}

		if inCodeBlock {
			b.WriteString(escapeHTML(line))
			b.WriteString("\n")
			continue
		}

		// Close list if current line is not a list item.
		if inList && !isListItem(trimmed) {
			b.WriteString("</ul>\n")
			inList = false
		}

		// Close table if current line is not a table row.
		if inTable && !strings.HasPrefix(trimmed, "|") {
			b.WriteString("</tbody></table>\n")
			inTable = false
		}

		// Headers.
		if strings.HasPrefix(trimmed, "### ") {
			b.WriteString("<h3>")
			b.WriteString(escapeHTML(strings.TrimPrefix(trimmed, "### ")))
			b.WriteString("</h3>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			b.WriteString("<h2>")
			b.WriteString(escapeHTML(strings.TrimPrefix(trimmed, "## ")))
			b.WriteString("</h2>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			b.WriteString("<h1>")
			b.WriteString(escapeHTML(strings.TrimPrefix(trimmed, "# ")))
			b.WriteString("</h1>\n")
			continue
		}

		// Table rows.
		if strings.HasPrefix(trimmed, "|") {
			if isSeparatorRow(trimmed) {
				continue // Skip separator rows like |---|---|
			}
			if !inTable {
				b.WriteString(`<table style="border-collapse:collapse;width:100%;margin:8px 0"><thead>`)
				b.WriteString(renderTableRow(trimmed, "th"))
				b.WriteString("</thead><tbody>\n")
				inTable = true
			} else {
				b.WriteString(renderTableRow(trimmed, "td"))
			}
			continue
		}

		// Bullet lists.
		if isListItem(trimmed) {
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			text := strings.TrimLeft(trimmed, "-* ")
			b.WriteString("<li>")
			b.WriteString(escapeHTML(text))
			b.WriteString("</li>\n")
			continue
		}

		// Empty line → paragraph break.
		if trimmed == "" {
			b.WriteString("<br>\n")
			continue
		}

		// Regular text.
		b.WriteString("<p>")
		b.WriteString(escapeHTML(trimmed))
		b.WriteString("</p>\n")
	}

	// Close any open blocks.
	if inCodeBlock {
		b.WriteString("</code></pre>\n")
	}
	if inTable {
		b.WriteString("</tbody></table>\n")
	}
	if inList {
		b.WriteString("</ul>\n")
	}

	return b.String()
}

// isListItem checks if a line starts with a markdown list marker.
func isListItem(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
}

// isSeparatorRow checks if a table row is a separator (e.g. |---|---|).
func isSeparatorRow(line string) bool {
	cleaned := strings.ReplaceAll(line, "|", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, ":", "")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned == ""
}

// renderTableRow converts a markdown table row to HTML.
func renderTableRow(line, tag string) string {
	cells := strings.Split(line, "|")
	var b strings.Builder
	b.WriteString("<tr>")
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue // Skip empty cells from leading/trailing pipes.
		}
		b.WriteString(fmt.Sprintf("<%s style=\"border:1px solid #444;padding:6px 10px\">%s</%s>", tag, escapeHTML(trimmed), tag))
	}
	b.WriteString("</tr>\n")
	return b.String()
}

// escapeHTML escapes HTML special characters.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// reportHTMLTemplate is the self-contained HTML wrapper for rendered reports.
const reportHTMLTemplate = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s — OpenDB Report</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
         background: #1a1a2e; color: #e0e0e0; padding: 20px; line-height: 1.6; }
  h1 { color: #e94560; margin: 16px 0 8px; font-size: 22px; }
  h2 { color: #e94560; margin: 16px 0 8px; font-size: 18px; border-bottom: 1px solid #333; padding-bottom: 4px; }
  h3 { color: #c8c8c8; margin: 12px 0 6px; font-size: 15px; }
  p { margin: 4px 0; }
  table { border-collapse: collapse; width: 100%%; margin: 8px 0; }
  th, td { border: 1px solid #444; padding: 6px 10px; text-align: left; }
  th { background: #16213e; }
  pre { background: #1e1e2e; padding: 12px; border-radius: 6px; overflow-x: auto; margin: 8px 0; }
  code { font-family: 'SF Mono', 'Fira Code', monospace; font-size: 13px; color: #a9dc76; }
  ul { margin: 8px 0 8px 24px; }
  li { margin: 2px 0; }
  .back { display: inline-block; margin-bottom: 16px; color: #e94560; text-decoration: none; }
</style>
</head>
<body>
<a class="back" href="/">&larr; Back to Dashboard</a>
%s
</body>
</html>`
