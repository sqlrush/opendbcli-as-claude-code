/*-------------------------------------------------------------------------
 *
 * register.go
 *	  register.go registers all Overlord-specific skills into the skill
 *	  registry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/register.go
 *
 *-------------------------------------------------------------------------
 */
// register.go registers all Overlord-specific skills into the skill registry.
package overlord

import "github.com/sqlrush/opendb/internal/skill"

// RegisterSkills registers all Overlord (Memory Agent) skills.
// dbAccess may be nil — failover skill falls back to template-based plans.
func RegisterSkills(registry *skill.Registry, wm *WorkerManager, srv *OverlordServer, region string, dbAccess *DBAccessManager) {
	registry.Register(NewWorkersSkill(wm))
	registry.Register(NewWorkerMemorySkill(wm))
	registry.Register(NewTopologySkill(wm, region))
	registry.Register(NewBroadcastSkill(wm))
	registry.Register(NewFailoverSkill(wm, dbAccess))
	registry.Register(NewRegionReportSkill(wm, region))
	registry.Register(NewEscalateSkill(srv))
}
