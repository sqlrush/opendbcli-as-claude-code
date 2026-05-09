/*-------------------------------------------------------------------------
 *
 * dbtop.go
 *	  DbtopRefreshConfig carries the driver and settings to the REPL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/dbtop.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/mysql/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/skill"
)

// DbtopRefreshConfig carries the driver and settings to the REPL.
type DbtopRefreshConfig struct {
	Driver      db.Driver
	IntervalSec int
}

// DbtopInterval implements skill.DbtopRefreshSource.
func (c *DbtopRefreshConfig) DbtopInterval() int { return c.IntervalSec }

// NewDbtopLoop implements skill.DbtopRefreshSource.
func (c *DbtopRefreshConfig) NewDbtopLoop() skill.DbtopLoop {
	return &mysqlDbtopLoop{collector: dbtop.NewCollector(c.Driver)}
}

// mysqlDbtopLoop wraps the MySQL collector+renderer into a DbtopLoop.
type mysqlDbtopLoop struct {
	collector *dbtop.Collector
}

func (l *mysqlDbtopLoop) RenderFrame(ctx context.Context, cols, intervalSec int) []string {
	snap := l.collector.Collect(ctx)
	return dbtop.Render(snap, cols, intervalSec)
}

// DBTopSkill provides a real-time MySQL monitoring dashboard.
type DBTopSkill struct {
	driver db.Driver
}

// NewDBTopSkill creates a DBTopSkill backed by the given driver.
func NewDBTopSkill(driver db.Driver) *DBTopSkill {
	return &DBTopSkill{driver: driver}
}

func (s *DBTopSkill) Name() string                      { return "dbtop" }
func (s *DBTopSkill) Description() string               { return "Real-time MySQL performance monitor" }
func (s *DBTopSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *DBTopSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "dbtop",
		Description: "Real-time MySQL performance monitor",
	}
}

func (s *DBTopSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "dbtop",
		Usage:   "/dbtop [interval_seconds]",
	}
}

func (s *DBTopSkill) Validate(_ skill.Params) error { return nil }

func (s *DBTopSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	intervalSec := 1
	if args := params.StringOr("args", ""); args != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(args)); err == nil && n > 0 {
			intervalSec = n
		}
	}

	return &skill.Result{
		Type: skill.ResultRefresh,
		Data: &DbtopRefreshConfig{
			Driver:      s.driver,
			IntervalSec: intervalSec,
		},
	}, nil
}
