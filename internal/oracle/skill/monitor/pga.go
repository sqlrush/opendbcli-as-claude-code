/*-------------------------------------------------------------------------
 *
 * pga.go
 *	  PGAOverviewItem represents a single PGA stat row.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/pga.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const pgaOverviewSQL = `SELECT name, ROUND(value/1048576, 1) AS mb
FROM v$pgastat
WHERE name IN (
    'aggregate PGA target parameter',
    'aggregate PGA auto target',
    'total PGA allocated',
    'total PGA used for auto workareas',
    'maximum PGA allocated',
    'over allocation count',
    'total freeable PGA memory'
)
ORDER BY name`

const pgaTopSessionsSQL = `SELECT s.sid, s.username,
       ROUND(p.pga_alloc_mem/1048576, 1) AS pga_mb,
       s.sql_id, s.program
FROM v$process p
JOIN v$session s ON p.addr = s.paddr
WHERE s.username IS NOT NULL
ORDER BY p.pga_alloc_mem DESC
FETCH FIRST 10 ROWS ONLY`

// PGAOverviewItem represents a single PGA stat row.
type PGAOverviewItem struct {
	Name string  `json:"name"`
	MB   float64 `json:"mb"`
}

// PGASessionItem represents a top PGA consumer session.
type PGASessionItem struct {
	SID     string  `json:"sid"`
	User    string  `json:"user"`
	PGAMB   float64 `json:"pga_mb"`
	SQLID   string  `json:"sql_id"`
	Program string  `json:"program"`
}

// PGAReport is the structured output for LLM consumption.
type PGAReport struct {
	Overview []PGAOverviewItem `json:"overview"`
	Sessions []PGASessionItem  `json:"sessions"`
}

// PGASkill shows PGA memory overview and top PGA sessions.
type PGASkill struct {
	driver db.Driver
}

// NewPGASkill creates a PGASkill backed by the given driver.
func NewPGASkill(driver db.Driver) *PGASkill {
	return &PGASkill{driver: driver}
}

func (s *PGASkill) Name() string                          { return "pga" }
func (s *PGASkill) Description() string                   { return "Show PGA memory overview and top sessions" }
func (s *PGASkill) SecurityLevel() skill.SecurityLevel     { return skill.LevelReadOnly }

func (s *PGASkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "pga",
		Description: "Show PGA memory overview and top sessions",
	}
}

func (s *PGASkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "pga",
		Usage:   "/pga",
	}
}

func (s *PGASkill) Validate(_ skill.Params) error { return nil }

func (s *PGASkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	overviewResult, err := s.driver.Query(ctx, pgaOverviewSQL)
	if err != nil {
		return nil, fmt.Errorf("querying PGA overview: %w", err)
	}

	sessionsResult, err := s.driver.Query(ctx, pgaTopSessionsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying PGA top sessions: %w", err)
	}

	overview := make([]PGAOverviewItem, 0, len(overviewResult.Rows))
	for _, row := range overviewResult.Rows {
		overview = append(overview, PGAOverviewItem{
			Name: rowStr(row, 0),
			MB:   rowFloat(row, 1),
		})
	}

	sessions := make([]PGASessionItem, 0, len(sessionsResult.Rows))
	for _, row := range sessionsResult.Rows {
		sessions = append(sessions, PGASessionItem{
			SID:     rowStr(row, 0),
			User:    rowStr(row, 1),
			PGAMB:   rowFloat(row, 2),
			SQLID:   rowStr(row, 3),
			Program: rowStr(row, 4),
		})
	}

	report := PGAReport{Overview: overview, Sessions: sessions}
	rendered := pgaRender(overview, sessions)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     &report,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d PGA stats, top %d sessions", len(overview), len(sessions)),
	}, nil
}

func pgaRender(overview []PGAOverviewItem, sessions []PGASessionItem) string {
	var b strings.Builder

	b.WriteString("PGA 内存概览:\n\n")

	// Find max name width for alignment.
	nameWidth := 10
	for _, item := range overview {
		if len(item.Name) > nameWidth {
			nameWidth = len(item.Name)
		}
	}

	headerFmt := fmt.Sprintf(" %%-%ds  %%s\n", nameWidth)
	rowFmt := fmt.Sprintf(" %%-%ds  %%8.1f MB\n", nameWidth)

	fmt.Fprintf(&b, headerFmt, "指标", "值")
	b.WriteString(" ")
	b.WriteString(strings.Repeat("─", nameWidth+12))
	b.WriteByte('\n')

	for _, item := range overview {
		fmt.Fprintf(&b, rowFmt, item.Name, item.MB)
	}

	b.WriteString("\nTop PGA 会话:\n\n")

	sessQR := &db.QueryResult{
		Columns: []string{"SID", "USER", "PGA_MB", "SQL_ID", "PROGRAM"},
		Rows:    make([][]any, 0, len(sessions)),
	}
	for _, sess := range sessions {
		sessQR.Rows = append(sessQR.Rows, []any{
			sess.SID, sess.User,
			fmt.Sprintf("%.1f", sess.PGAMB),
			sess.SQLID, sess.Program,
		})
	}
	b.WriteString(format.FormatTableOpts(sessQR, format.TableOptions{MaxRows: 10, TermWidth: 120}))

	b.WriteString("\n\n提示: /alter pga_aggregate_target 2G 扩大PGA\n")

	return b.String()
}
