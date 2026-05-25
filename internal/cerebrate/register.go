/*-------------------------------------------------------------------------
 *
 * register.go
 *	  register.go registers all Cerebrate-specific skills into the skill
 *	  registry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/register.go
 *
 *-------------------------------------------------------------------------
 */
// register.go registers all Cerebrate-specific skills into the skill registry.
package cerebrate

import "github.com/sqlrush/opendb/internal/skill"

// RegisterSkills registers all Cerebrate (Manager Agent) skills.
func RegisterSkills(registry *skill.Registry, fm *FleetManager) {
	registry.Register(NewOverlordsSkill(fm))
	registry.Register(NewGlobalTopologySkill(fm))
	registry.Register(NewFleetReportSkill(fm))
	registry.Register(NewPushPolicySkill(fm))
	registry.Register(NewMaintenanceSkill(fm))
	registry.Register(NewClusterManageSkill(fm))
}

// RegisterExtendedSkills registers skills that require additional dependencies
// beyond just the FleetManager (config audit, LLM queue, etc.).
func RegisterExtendedSkills(registry *skill.Registry, audit *ConfigAuditManager, llmQueue *LLMPriorityQueue) {
	if audit != nil {
		registry.Register(NewConfigAuditSkill(audit))
	}
	if llmQueue != nil {
		registry.Register(NewLLMQueueSkill(llmQueue))
	}
}
