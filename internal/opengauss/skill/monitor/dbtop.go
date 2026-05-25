/*-------------------------------------------------------------------------
 *
 * dbtop.go
 *	  OGDbtopRefreshConfig carries the driver and settings to the REPL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/dbtop.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/opengauss/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/skill"
)

// OGDbtopRefreshConfig carries the driver and settings to the REPL.
type OGDbtopRefreshConfig struct {
	Driver      db.Driver
	IntervalSec int
}

// DbtopInterval implements skill.DbtopRefreshSource.
func (c *OGDbtopRefreshConfig) DbtopInterval() int { return c.IntervalSec }

// NewDbtopLoop implements skill.DbtopRefreshSource.
func (c *OGDbtopRefreshConfig) NewDbtopLoop() skill.DbtopLoop {
	return &ogDbtopLoop{collector: dbtop.NewCollector(c.Driver)}
}

// ogDbtopLoop wraps the OpenGauss collector+renderer into a DbtopLoop.
type ogDbtopLoop struct {
	collector *dbtop.Collector
}

func (l *ogDbtopLoop) RenderFrame(ctx context.Context, cols, intervalSec int) []string {
	snap := l.collector.Collect(ctx)
	return dbtop.Render(snap, cols, intervalSec)
}

// OGDBTopSkill provides a real-time OpenGauss monitoring dashboard.
type OGDBTopSkill struct {
	driver db.Driver
}

// NewOGDBTopSkill creates a OGDBTopSkill backed by the given driver.
func NewOGDBTopSkill(driver db.Driver) *OGDBTopSkill {
	return &OGDBTopSkill{driver: driver}
}

func (s *OGDBTopSkill) Name() string                      { return "dbtop" }
func (s *OGDBTopSkill) Description() string               { return "Real-time OpenGauss performance monitor" }
func (s *OGDBTopSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *OGDBTopSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "dbtop",
		Description: "Real-time OpenGauss performance monitor",
	}
}

func (s *OGDBTopSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "dbtop",
		Usage:   "/dbtop [interval_seconds]",
	}
}

func (s *OGDBTopSkill) Validate(_ skill.Params) error { return nil }

func (s *OGDBTopSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	intervalSec := 1
	if args := params.StringOr("args", ""); args != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(args)); err == nil && n > 0 {
			intervalSec = n
		}
	}

	return &skill.Result{
		Type: skill.ResultRefresh,
		Data: &OGDbtopRefreshConfig{
			Driver:      s.driver,
			IntervalSec: intervalSec,
		},
	}, nil
}
