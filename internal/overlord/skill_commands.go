/*-------------------------------------------------------------------------
 *
 * skill_commands.go
 *	  skill_commands.go implements Overlord skills for command dispatch
 *	  and failover. Skills: /broadcast (broadcast_command), /failover
 *	  (coordinate_failover)
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/skill_commands.go
 *
 *-------------------------------------------------------------------------
 */
// skill_commands.go implements Overlord skills for command dispatch and failover.
// Skills: /broadcast (broadcast_command), /failover (coordinate_failover)
package overlord

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/skill"
)

// ---- broadcast_command ----

// BroadcastSkill sends a command to multiple Workers.
type BroadcastSkill struct {
	workerMgr *WorkerManager
}

func NewBroadcastSkill(wm *WorkerManager) *BroadcastSkill {
	return &BroadcastSkill{workerMgr: wm}
}

func (s *BroadcastSkill) Name() string        { return "broadcast" }
func (s *BroadcastSkill) Description() string { return "Broadcast a command to multiple Workers" }
func (s *BroadcastSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelOperator }

func (s *BroadcastSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "broadcast_command",
		Description: "Send a command to multiple Workers simultaneously (e.g., parameter adjustment, health check)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"description": "Target selector: worker IDs (comma-separated), storage group name, or 'all'",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "SQL or skill command to execute on each Worker",
				},
			},
			"required": []string{"target", "command"},
		},
	}
}

func (s *BroadcastSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "broadcast",
		Usage:   "/broadcast --target <workers|all> --command <sql>",
		Examples: []string{
			`/broadcast --target all --command "ALTER SYSTEM SET db_file_multiblock_read_count=8"`,
			`/broadcast --target "Oracle-A-037,Oracle-A-038" --command "/health"`,
		},
	}
}

func (s *BroadcastSkill) Validate(params skill.Params) error {
	if params.StringOr("target", "") == "" {
		return fmt.Errorf("--target is required")
	}
	if params.StringOr("command", "") == "" {
		return fmt.Errorf("--command is required")
	}
	return nil
}

func (s *BroadcastSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	target := params.StringOr("target", "")
	command := params.StringOr("command", "")

	// Resolve target to worker IDs.
	var workerIDs []string
	if target == "all" {
		workerIDs = s.workerMgr.WorkerIDs()
	} else {
		workerIDs = strings.Split(target, ",")
		for i := range workerIDs {
			workerIDs[i] = strings.TrimSpace(workerIDs[i])
		}
	}

	if len(workerIDs) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No workers matched target.",
			Summary:  "0 workers",
		}, nil
	}

	cmd := &pb.CommandRequest{
		CommandId:   fmt.Sprintf("broadcast-%d", time.Now().UnixMilli()),
		CommandType: "execute_sql",
		Payload:     command,
	}

	results := s.workerMgr.BroadcastCommand(ctx, workerIDs, cmd)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Broadcasting to %d workers...\n", len(workerIDs)))
	successCount := 0
	for wid, resp := range results {
		if resp.Success {
			sb.WriteString(fmt.Sprintf("  %s: OK\n", wid))
			successCount++
		} else {
			sb.WriteString(fmt.Sprintf("  %s: FAILED (%s)\n", wid, resp.ErrorMessage))
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     results,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("%d/%d workers succeeded", successCount, len(workerIDs)),
	}, nil
}

// ---- coordinate_failover ----

// FailoverSkill generates a detailed failover report with step-by-step instructions.
// OpenDB never executes database changes — the report is the product.
type FailoverSkill struct {
	workerMgr *WorkerManager
	dbAccess  *DBAccessManager // may be nil; analyzer falls back to templates
}

// NewFailoverSkill creates a FailoverSkill. dbAccess may be nil.
func NewFailoverSkill(wm *WorkerManager, dbAccess *DBAccessManager) *FailoverSkill {
	return &FailoverSkill{workerMgr: wm, dbAccess: dbAccess}
}

func (s *FailoverSkill) Name() string        { return "failover" }
func (s *FailoverSkill) Description() string { return "Generate failover report with step-by-step instructions" }
func (s *FailoverSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelAdmin }

func (s *FailoverSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "coordinate_failover",
		Description: "Generate a detailed failover report with step-by-step SQL instructions for primary-standby switchover. OpenDB collects diagnostic data via read-only queries and produces an actionable report — it never executes database changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cluster_name": map[string]any{
					"type":        "string",
					"description": "Name of the replication cluster to failover",
				},
				"primary_worker": map[string]any{
					"type":        "string",
					"description": "Current primary Worker ID",
				},
				"standby_worker": map[string]any{
					"type":        "string",
					"description": "Standby Worker ID to promote",
				},
			},
			"required": []string{"primary_worker", "standby_worker"},
		},
	}
}

func (s *FailoverSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "failover",
		Usage:   "/failover <cluster_name>",
		Examples: []string{"/failover ora-dg-05"},
	}
}

func (s *FailoverSkill) Validate(params skill.Params) error {
	if params.StringOr("primary_worker", "") == "" {
		return fmt.Errorf("primary_worker is required")
	}
	if params.StringOr("standby_worker", "") == "" {
		return fmt.Errorf("standby_worker is required")
	}
	return nil
}

func (s *FailoverSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	primary := params.StringOr("primary_worker", "")
	standby := params.StringOr("standby_worker", "")
	clusterName := params.StringOr("cluster_name", fmt.Sprintf("%s->%s", primary, standby))

	// Determine database type from worker status.
	dbType := s.resolveDBType(ctx, primary, standby)

	// Generate the failover analysis report.
	analyzer := NewFailoverAnalyzer(s.dbAccess)
	report, err := analyzer.Analyze(ctx, primary, standby, dbType)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultError,
			Rendered: fmt.Sprintf("Failover analysis failed for %s: %v", clusterName, err),
			Summary:  "failover analysis failed",
		}, nil
	}

	rendered := RenderFailoverMarkdown(report)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     report,
		Rendered: rendered,
		Summary:  fmt.Sprintf("failover report for %s generated (risk: %s)", clusterName, report.RiskLevel),
	}, nil
}

// resolveDBType tries to determine the database type from worker status.
// Falls back to "oracle" if status is unavailable.
func (s *FailoverSkill) resolveDBType(ctx context.Context, primary, standby string) string {
	resp, err := s.workerMgr.GetWorkerStatus(ctx, primary)
	if err == nil && resp.DbType != "" {
		return resp.DbType
	}

	resp, err = s.workerMgr.GetWorkerStatus(ctx, standby)
	if err == nil && resp.DbType != "" {
		return resp.DbType
	}

	return "oracle"
}
