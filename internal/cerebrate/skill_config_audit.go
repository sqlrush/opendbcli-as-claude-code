/*-------------------------------------------------------------------------
 *
 * skill_config_audit.go
 *	  skill_config_audit.go implements the /config-audit skill for
 *	  Cerebrate. Collects PG parameters from all regions, detects drift,
 *	  and generates a report.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/skill_config_audit.go
 *
 *-------------------------------------------------------------------------
 */
// skill_config_audit.go implements the /config-audit skill for Cerebrate.
// Collects PG parameters from all regions, detects drift, and generates a report.
package cerebrate

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/skill"
)

// ConfigAuditSkill exposes config drift detection as a CLI/LLM skill.
type ConfigAuditSkill struct {
	audit *ConfigAuditManager
}

func NewConfigAuditSkill(audit *ConfigAuditManager) *ConfigAuditSkill {
	return &ConfigAuditSkill{audit: audit}
}

func (s *ConfigAuditSkill) Name() string        { return "config-audit" }
func (s *ConfigAuditSkill) Description() string { return "Detect PG configuration drift across regions" }
func (s *ConfigAuditSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ConfigAuditSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "config_audit",
		Description: "Collect PostgreSQL parameters from all regions and detect configuration drift. Returns a report of parameters that differ across regions, with critical parameters highlighted.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *ConfigAuditSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "config-audit",
		Aliases:  []string{"ca", "drift"},
		Usage:    "/config-audit",
		Examples: []string{"/config-audit"},
	}
}

func (s *ConfigAuditSkill) Validate(_ skill.Params) error { return nil }

func (s *ConfigAuditSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	// Collect fresh snapshots
	snapshots, err := s.audit.CollectFromAllRegions(ctx)
	if err != nil {
		return nil, fmt.Errorf("config collection failed: %w", err)
	}

	// Detect drift
	report := s.audit.DetectDrift()

	// Generate readable report
	markdown := s.audit.GenerateDriftReport(report)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     report,
		Rendered: markdown,
		Summary: fmt.Sprintf("config audit: %d regions, %d drifts found (%d critical)",
			len(snapshots), len(report.Drifts), countCritical(report.Drifts)),
	}, nil
}
