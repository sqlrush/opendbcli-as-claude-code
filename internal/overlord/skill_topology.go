/*-------------------------------------------------------------------------
 *
 * skill_topology.go
 *	  skill_topology.go implements the region topology skill. Skill:
 *	  /topology (get_region_topology)
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/skill_topology.go
 *
 *-------------------------------------------------------------------------
 */
// skill_topology.go implements the region topology skill.
// Skill: /topology (get_region_topology)
package overlord

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/skill"
)

// TopologySkill shows the regional topology (master-slave, storage groups, network groups).
type TopologySkill struct {
	workerMgr *WorkerManager
	region    string
}

func NewTopologySkill(wm *WorkerManager, region string) *TopologySkill {
	return &TopologySkill{workerMgr: wm, region: region}
}

func (s *TopologySkill) Name() string        { return "topology" }
func (s *TopologySkill) Description() string { return "Show regional database topology" }
func (s *TopologySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TopologySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "get_region_topology",
		Description: "Get regional topology including master-slave relationships, storage groups, and network groups",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *TopologySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "topology",
		Aliases:  []string{"topo"},
		Usage:    "/topology",
		Examples: []string{"/topology"},
	}
}

func (s *TopologySkill) Validate(_ skill.Params) error { return nil }

func (s *TopologySkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	statuses := s.workerMgr.GetAllWorkerStatus(ctx)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Region: %s (%d workers)\n\n", s.region, len(statuses)))

	// Group by DB type.
	groups := make(map[string][]string)
	for _, st := range statuses {
		groups[st.DbType] = append(groups[st.DbType], st.WorkerId)
	}

	for dbType, workers := range groups {
		sb.WriteString(fmt.Sprintf("  %s (%d nodes):\n", dbType, len(workers)))
		for _, w := range workers {
			sb.WriteString(fmt.Sprintf("    - %s\n", w))
		}
		sb.WriteString("\n")
	}

	// TODO: Add storage groups, network groups, master-slave relationships
	// when OS-level discovery is implemented.
	sb.WriteString("  Storage/Network groups: pending OS-level discovery\n")

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     groups,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("region %s: %d workers", s.region, len(statuses)),
	}, nil
}
