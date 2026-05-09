/*-------------------------------------------------------------------------
 *
 * dbtop.go
 *	  PGDbtopRefreshConfig carries the driver and settings to the REPL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/dbtop.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/postgres/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/skill"
)

// PGDbtopRefreshConfig carries the driver and settings to the REPL.
type PGDbtopRefreshConfig struct {
	Driver      db.Driver
	IntervalSec int
}

// DbtopInterval implements skill.DbtopRefreshSource.
func (c *PGDbtopRefreshConfig) DbtopInterval() int { return c.IntervalSec }

// NewDbtopLoop implements skill.DbtopRefreshSource.
func (c *PGDbtopRefreshConfig) NewDbtopLoop() skill.DbtopLoop {
	return &pgDbtopLoop{collector: dbtop.NewCollector(c.Driver)}
}

// pgDbtopLoop wraps the PostgreSQL collector+renderer into a DbtopLoop.
type pgDbtopLoop struct {
	collector *dbtop.Collector
}

func (l *pgDbtopLoop) RenderFrame(ctx context.Context, cols, intervalSec int) []string {
	snap := l.collector.Collect(ctx)
	return dbtop.Render(snap, cols, intervalSec)
}

// PGDBTopSkill provides a real-time PostgreSQL monitoring dashboard.
type PGDBTopSkill struct {
	driver db.Driver
}

// NewPGDBTopSkill creates a PGDBTopSkill backed by the given driver.
func NewPGDBTopSkill(driver db.Driver) *PGDBTopSkill {
	return &PGDBTopSkill{driver: driver}
}

func (s *PGDBTopSkill) Name() string                      { return "dbtop" }
func (s *PGDBTopSkill) Description() string               { return "Real-time PostgreSQL performance monitor" }
func (s *PGDBTopSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *PGDBTopSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "dbtop",
		Description: "Real-time PostgreSQL performance monitor",
	}
}

func (s *PGDBTopSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "dbtop",
		Usage:   "/dbtop [interval_seconds]",
	}
}

func (s *PGDBTopSkill) Validate(_ skill.Params) error { return nil }

func (s *PGDBTopSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	intervalSec := 1
	if args := params.StringOr("args", ""); args != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(args)); err == nil && n > 0 {
			intervalSec = n
		}
	}

	return &skill.Result{
		Type: skill.ResultRefresh,
		Data: &PGDbtopRefreshConfig{
			Driver:      s.driver,
			IntervalSec: intervalSec,
		},
	}, nil
}
