/*-------------------------------------------------------------------------
 *
 * view_expander.go
 *	  ViewExpander recursively replaces view references in SQL with
 *	  their definitions so the LLM sees the real underlying tables, not
 *	  opaque view names.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/view_expander.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
)

// ViewExpander recursively replaces view references in SQL with their definitions
// so the LLM sees the real underlying tables, not opaque view names.
//
// MVP: depth-limited (default 5) recursive substitution. Doesn't do full SQL parse —
// uses regex to find view names then `pg_get_viewdef`.
type ViewExpander struct {
	driver db.Driver
	maxDepth int
}

func NewViewExpander(d db.Driver) *ViewExpander {
	return &ViewExpander{driver: d, maxDepth: 5}
}

// Expand returns (expandedSQL, viewsExpanded, err).
// If no views found in SQL, returns the SQL unchanged with viewsExpanded=nil.
func (v *ViewExpander) Expand(ctx context.Context, sql string) (string, []string, error) {
	expanded := []string{}
	current := sql

	for depth := 0; depth < v.maxDepth; depth++ {
		viewsInSQL := v.findViewReferences(ctx, current)
		if len(viewsInSQL) == 0 {
			break
		}

		newSQL := current
		for _, viewName := range viewsInSQL {
			def, err := v.fetchViewDef(ctx, viewName)
			if err != nil || def == "" {
				continue
			}
			// Replace `viewName` with `(definition) AS viewName`
			// Naive replace — for MVP. Doesn't handle quoted identifiers.
			pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(viewName) + `\b`)
			newSQL = pattern.ReplaceAllString(newSQL, "("+def+") AS "+viewName)
			expanded = append(expanded, viewName)
		}

		if newSQL == current {
			break // no progress, avoid infinite loop
		}
		current = newSQL
	}

	if len(expanded) == 0 {
		return sql, nil, nil
	}
	return current, expanded, nil
}

// findViewReferences extracts table-like names from the SQL and queries pg_views to filter views.
func (v *ViewExpander) findViewReferences(ctx context.Context, sql string) []string {
	candidates := ExtractTableNames(sql)
	if len(candidates) == 0 {
		return nil
	}

	q := fmt.Sprintf(`
SELECT viewname FROM pg_views
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
  AND viewname IN (%s)`, sqlInList(candidates))

	res, err := v.driver.Query(ctx, q)
	if err != nil {
		return nil
	}
	var views []string
	for _, row := range res.Rows {
		if len(row) > 0 {
			views = append(views, asString(row[0]))
		}
	}
	return views
}

// fetchViewDef returns the SQL definition of a view (without the leading SELECT).
func (v *ViewExpander) fetchViewDef(ctx context.Context, viewName string) (string, error) {
	q := fmt.Sprintf(`SELECT pg_get_viewdef('%s'::regclass, true)`, strings.ReplaceAll(viewName, "'", "''"))
	res, err := v.driver.Query(ctx, q)
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Rows) == 0 {
		return "", nil
	}
	def := asString(res.Rows[0][0])
	// Strip trailing semicolon to allow inline embedding
	def = strings.TrimRight(strings.TrimSpace(def), ";")
	return def, nil
}
