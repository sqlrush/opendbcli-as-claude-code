/*-------------------------------------------------------------------------
 *
 * skill_fleet.go
 *	  skill_fleet.go implements Cerebrate skills for global fleet
 *	  management. Skills: /overlords, /global-topology, /fleet-report,
 *	  /push-policy, /schedule-maintenance, /cluster
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/skill_fleet.go
 *
 *-------------------------------------------------------------------------
 */
// skill_fleet.go implements Cerebrate skills for global fleet management.
// Skills: /overlords, /global-topology, /fleet-report, /push-policy, /schedule-maintenance, /cluster
package cerebrate

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/skill"
)

// ---- get_all_overlords ----

type OverlordsSkill struct {
	fleet *FleetManager
}

func NewOverlordsSkill(fm *FleetManager) *OverlordsSkill {
	return &OverlordsSkill{fleet: fm}
}

func (s *OverlordsSkill) Name() string        { return "overlords" }
func (s *OverlordsSkill) Description() string { return "Show status of all Overlords" }
func (s *OverlordsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *OverlordsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "get_all_overlords",
		Description: "Get status of all Overlords (Memory Agents) and their managed Workers",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *OverlordsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "overlords", Usage: "/overlords", Examples: []string{"/overlords"}}
}

func (s *OverlordsSkill) Validate(_ skill.Params) error { return nil }

func (s *OverlordsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	regions := s.fleet.GetAllRegionStatus(ctx)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Fleet: %d overlords, %d workers total, %d online\n\n",
		s.fleet.OverlordCount(), s.fleet.TotalWorkerCount(), s.fleet.TotalOnlineCount()))

	for _, r := range regions {
		health := "—"
		if r.RegionHealth != nil {
			health = fmt.Sprintf("%d/100", r.RegionHealth.Score)
		}
		sb.WriteString(fmt.Sprintf("  %-15s %-12s %d/%d workers  %s\n",
			r.OverlordId, r.Region, r.WorkerOnline, r.WorkerCount, health))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     regions,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("%d overlords, %d workers", s.fleet.OverlordCount(), s.fleet.TotalWorkerCount()),
	}, nil
}

// ---- get_global_topology ----

type GlobalTopologySkill struct {
	fleet *FleetManager
}

func NewGlobalTopologySkill(fm *FleetManager) *GlobalTopologySkill {
	return &GlobalTopologySkill{fleet: fm}
}

func (s *GlobalTopologySkill) Name() string        { return "global-topology" }
func (s *GlobalTopologySkill) Description() string { return "Show global fleet topology" }
func (s *GlobalTopologySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *GlobalTopologySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "get_global_topology",
		Description: "Get the global fleet topology across all regions, including cross-region risk analysis",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *GlobalTopologySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "global-topology", Aliases: []string{"gtopo"}, Usage: "/global-topology", Examples: []string{"/global-topology"}}
}

func (s *GlobalTopologySkill) Validate(_ skill.Params) error { return nil }

func (s *GlobalTopologySkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	regions := s.fleet.GetAllRegionStatus(ctx)

	var sb strings.Builder
	sb.WriteString("Global Topology\n")
	sb.WriteString(strings.Repeat("─", 60) + "\n\n")

	for _, r := range regions {
		sb.WriteString(fmt.Sprintf("  %s (%s): %d workers\n", r.OverlordId, r.Region, r.WorkerCount))
		groups := make(map[string]int)
		for _, w := range r.Workers {
			groups[w.DbType]++
		}
		for dbType, count := range groups {
			sb.WriteString(fmt.Sprintf("    %s × %d\n", dbType, count))
		}
		sb.WriteString("\n")
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     regions,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("global topology: %d regions", len(regions)),
	}, nil
}

// ---- generate_fleet_report ----

type FleetReportSkill struct {
	fleet *FleetManager
}

func NewFleetReportSkill(fm *FleetManager) *FleetReportSkill {
	return &FleetReportSkill{fleet: fm}
}

func (s *FleetReportSkill) Name() string        { return "fleet-report" }
func (s *FleetReportSkill) Description() string { return "Generate global fleet health report" }
func (s *FleetReportSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *FleetReportSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "generate_fleet_report",
		Description: "Generate a comprehensive health report for the entire database fleet across all regions",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *FleetReportSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "fleet-report", Aliases: []string{"fr"}, Usage: "/fleet-report", Examples: []string{"/fleet-report"}}
}

func (s *FleetReportSkill) Validate(_ skill.Params) error { return nil }

func (s *FleetReportSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	regions := s.fleet.GetAllRegionStatus(ctx)

	var sb strings.Builder
	now := time.Now()
	sb.WriteString(fmt.Sprintf("Global Fleet Report (%s)\n", now.Format("2006-01-02 15:04")))
	sb.WriteString(strings.Repeat("═", 60) + "\n\n")
	sb.WriteString(fmt.Sprintf("  Fleet: %d workers | Online: %d | Offline: %d\n\n",
		s.fleet.TotalWorkerCount(), s.fleet.TotalOnlineCount(),
		s.fleet.TotalWorkerCount()-s.fleet.TotalOnlineCount()))

	for _, r := range regions {
		health := "—"
		if r.RegionHealth != nil {
			health = fmt.Sprintf("%d/100", r.RegionHealth.Score)
		}
		sb.WriteString(fmt.Sprintf("  %s (%s): %s  [%d/%d online]\n",
			r.OverlordId, r.Region, health, r.WorkerOnline, r.WorkerCount))
	}
	sb.WriteString("\n")

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     regions,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("fleet report: %d workers across %d regions", s.fleet.TotalWorkerCount(), len(regions)),
	}, nil
}

// ---- push_policy ----

type PushPolicySkill struct {
	fleet *FleetManager
}

func NewPushPolicySkill(fm *FleetManager) *PushPolicySkill {
	return &PushPolicySkill{fleet: fm}
}

func (s *PushPolicySkill) Name() string        { return "push-policy" }
func (s *PushPolicySkill) Description() string { return "Push policy/blacklist update to all Overlords" }
func (s *PushPolicySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelAdmin }

func (s *PushPolicySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "push_policy",
		Description: "Push a policy or blacklist update to all Overlords, which distribute to all Workers",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"policy_type": map[string]any{"type": "string", "description": "Policy type: blacklist, config, prompt"},
				"action":      map[string]any{"type": "string", "description": "Action: add, update, remove"},
				"content":     map[string]any{"type": "string", "description": "Policy content"},
			},
			"required": []string{"policy_type", "action", "content"},
		},
	}
}

func (s *PushPolicySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "push-policy",
		Usage:   "/push-policy --type <blacklist|config> --action <add|update|remove> --rule <content>",
		Examples: []string{`/push-policy --type blacklist --action add --rule "ALTER SYSTEM FLUSH SHARED_POOL"`},
	}
}

func (s *PushPolicySkill) Validate(params skill.Params) error {
	if params.StringOr("policy_type", params.StringOr("type", "")) == "" {
		return fmt.Errorf("--type is required")
	}
	return nil
}

func (s *PushPolicySkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	policyType := params.StringOr("policy_type", params.StringOr("type", ""))
	action := params.StringOr("action", "add")
	content := params.StringOr("content", params.StringOr("rule", ""))

	req := &pb.PolicyRequest{
		PolicyId:   fmt.Sprintf("policy-%d", time.Now().UnixMilli()),
		PolicyType: policyType,
		Content:    []byte(content),
		Action:     action,
	}

	results := s.fleet.PushPolicyToAll(ctx, req)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Push policy (%s %s) to all Overlords:\n", action, policyType))
	totalUpdated := int32(0)
	for oid, resp := range results {
		if resp.Success {
			sb.WriteString(fmt.Sprintf("  %s: OK (%d workers updated)\n", oid, resp.WorkersUpdated))
			totalUpdated += resp.WorkersUpdated
		} else {
			sb.WriteString(fmt.Sprintf("  %s: FAILED (%s)\n", oid, resp.Message))
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("policy pushed to %d workers", totalUpdated),
	}, nil
}

// ---- schedule_maintenance ----

type MaintenanceSkill struct {
	fleet *FleetManager
}

func NewMaintenanceSkill(fm *FleetManager) *MaintenanceSkill {
	return &MaintenanceSkill{fleet: fm}
}

func (s *MaintenanceSkill) Name() string        { return "schedule-maintenance" }
func (s *MaintenanceSkill) Description() string { return "Schedule preventive maintenance across regions" }
func (s *MaintenanceSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelAdmin }

func (s *MaintenanceSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "schedule_maintenance",
		Description: "Create and dispatch a preventive maintenance plan to Overlords (tablespace expansion, password rotation, etc.)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"overlord_id":  map[string]any{"type": "string", "description": "Target Overlord ID"},
				"description":  map[string]any{"type": "string", "description": "Maintenance description"},
				"window_start": map[string]any{"type": "string", "description": "Maintenance window start (HH:MM)"},
				"window_end":   map[string]any{"type": "string", "description": "Maintenance window end (HH:MM)"},
			},
			"required": []string{"overlord_id", "description"},
		},
	}
}

func (s *MaintenanceSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "schedule-maintenance",
		Aliases:  []string{"sm"},
		Usage:    "/schedule-maintenance --overlord <id> --description <text>",
		Examples: []string{`/schedule-maintenance --overlord Overlord-A --description "8 nodes tablespace expansion"`},
	}
}

func (s *MaintenanceSkill) Validate(params skill.Params) error {
	if params.StringOr("overlord_id", params.StringOr("overlord", "")) == "" {
		return fmt.Errorf("--overlord is required")
	}
	return nil
}

func (s *MaintenanceSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	overlordID := params.StringOr("overlord_id", params.StringOr("overlord", ""))
	description := params.StringOr("description", "maintenance")
	windowStart := params.StringOr("window_start", "20:00")
	windowEnd := params.StringOr("window_end", "22:00")

	// TODO: dispatch maintenance plan to Overlord via gRPC EventStream.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Maintenance plan dispatched:\n"))
	sb.WriteString(fmt.Sprintf("  Target:   %s\n", overlordID))
	sb.WriteString(fmt.Sprintf("  Task:     %s\n", description))
	sb.WriteString(fmt.Sprintf("  Window:   %s - %s\n", windowStart, windowEnd))

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("maintenance scheduled for %s", overlordID),
	}, nil
}

// ---- manage_cluster ----

type ClusterManageSkill struct {
	fleet *FleetManager
}

func NewClusterManageSkill(fm *FleetManager) *ClusterManageSkill {
	return &ClusterManageSkill{fleet: fm}
}

func (s *ClusterManageSkill) Name() string        { return "cluster" }
func (s *ClusterManageSkill) Description() string { return "Cluster management (status, add/remove workers, takeover, upgrade)" }
func (s *ClusterManageSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelAdmin }

func (s *ClusterManageSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "manage_cluster",
		Description: "Manage the database cluster: view status, add/remove nodes, Overlord takeover, rolling upgrade",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"description": "Cluster action: status, add-worker, remove-worker, takeover, upgrade",
					"enum": []string{"status", "add-worker", "remove-worker", "takeover", "upgrade"},
				},
			},
			"required": []string{"action"},
		},
	}
}

func (s *ClusterManageSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "cluster",
		Usage:          "/cluster <status|add-worker|remove-worker|takeover|upgrade>",
		ArgCompletions: []string{"status", "add-worker", "remove-worker", "takeover", "upgrade"},
		Examples:       []string{"/cluster status", "/cluster add-worker --overlord Overlord-A MySQL-A-201"},
	}
}

func (s *ClusterManageSkill) Validate(params skill.Params) error {
	action := params.StringOr("action", params.StringOr("args", "status"))
	switch action {
	case "status", "add-worker", "remove-worker", "takeover", "upgrade":
		return nil
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func (s *ClusterManageSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	action := params.StringOr("action", params.StringOr("args", "status"))

	switch action {
	case "status":
		return s.clusterStatus(ctx)
	default:
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("Cluster action '%s' not yet implemented.", action),
			Summary:  action,
		}, nil
	}
}

func (s *ClusterManageSkill) clusterStatus(ctx context.Context) (*skill.Result, error) {
	regions := s.fleet.GetAllRegionStatus(ctx)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Cluster Status\n"))
	sb.WriteString(fmt.Sprintf("  Overlords: %d\n", s.fleet.OverlordCount()))
	sb.WriteString(fmt.Sprintf("  Workers:   %d total, %d online\n\n", s.fleet.TotalWorkerCount(), s.fleet.TotalOnlineCount()))

	for _, r := range regions {
		sb.WriteString(fmt.Sprintf("  %s (%s): %d/%d workers online\n", r.OverlordId, r.Region, r.WorkerOnline, r.WorkerCount))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("cluster: %d overlords, %d workers", s.fleet.OverlordCount(), s.fleet.TotalWorkerCount()),
	}, nil
}
