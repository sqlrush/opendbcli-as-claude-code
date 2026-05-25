/*-------------------------------------------------------------------------
 *
 * skill_report.go
 *	  skill_report.go implements Overlord skills for reporting and
 *	  escalation. Skills: /region-report (generate_region_report),
 *	  escalate_to_cerebrate
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/skill_report.go
 *
 *-------------------------------------------------------------------------
 */
// skill_report.go implements Overlord skills for reporting and escalation.
// Skills: /region-report (generate_region_report), escalate_to_cerebrate
package overlord

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/skill"
)

// ---- generate_region_report ----

// RegionReportSkill generates an aggregated region health report.
type RegionReportSkill struct {
	workerMgr *WorkerManager
	region    string
}

func NewRegionReportSkill(wm *WorkerManager, region string) *RegionReportSkill {
	return &RegionReportSkill{workerMgr: wm, region: region}
}

func (s *RegionReportSkill) Name() string        { return "region-report" }
func (s *RegionReportSkill) Description() string { return "Generate aggregated region health report" }
func (s *RegionReportSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *RegionReportSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "generate_region_report",
		Description: "Generate an aggregated health report for all Workers in this region, including anomalies, repairs, and recommendations",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *RegionReportSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "region-report",
		Aliases:  []string{"rr"},
		Usage:    "/region-report",
		Examples: []string{"/region-report"},
	}
}

func (s *RegionReportSkill) Validate(_ skill.Params) error { return nil }

func (s *RegionReportSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	statuses := s.workerMgr.GetAllWorkerStatus(ctx)

	var sb strings.Builder
	now := time.Now()
	sb.WriteString(fmt.Sprintf("Region %s Report (%s)\n", s.region, now.Format("2006-01-02 15:04")))
	sb.WriteString(strings.Repeat("─", 60) + "\n\n")

	total := len(statuses)
	online := 0
	var totalHealth int32
	for _, st := range statuses {
		if st.State == pb.AgentState_STATE_RUNNING {
			online++
		}
		if st.Health != nil {
			totalHealth += st.Health.Score
		}
	}

	avgHealth := int32(0)
	if total > 0 {
		avgHealth = totalHealth / int32(total)
	}

	sb.WriteString(fmt.Sprintf("  Workers: %d total, %d online, %d offline\n", total, online, total-online))
	sb.WriteString(fmt.Sprintf("  Health:  %d/100\n\n", avgHealth))

	// List anomalies.
	anomalyCount := 0
	for _, st := range statuses {
		if st.AnomalyCount > 0 {
			anomalyCount++
			sb.WriteString(fmt.Sprintf("  ⚠ %s: %d anomalies, last at %s\n",
				st.WorkerId, st.AnomalyCount,
				time.Unix(st.LastAnomalyTs, 0).Format("15:04")))
		}
	}
	if anomalyCount == 0 {
		sb.WriteString("  No anomalies detected.\n")
	}

	sb.WriteString(fmt.Sprintf("\n  LLM calls today: %d\n", sumLLMCalls(statuses)))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     statuses,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("region %s: %d workers, health %d/100", s.region, total, avgHealth),
	}, nil
}

func sumLLMCalls(statuses []*pb.StatusResponse) int32 {
	var total int32
	for _, st := range statuses {
		total += st.LlmCallsToday
	}
	return total
}

// ---- escalate_to_cerebrate ----

// EscalateSkill reports critical events to Cerebrate.
type EscalateSkill struct {
	overlordServer *OverlordServer
}

func NewEscalateSkill(srv *OverlordServer) *EscalateSkill {
	return &EscalateSkill{overlordServer: srv}
}

func (s *EscalateSkill) Name() string        { return "escalate" }
func (s *EscalateSkill) Description() string { return "Escalate critical event to Cerebrate" }
func (s *EscalateSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *EscalateSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "escalate_to_cerebrate",
		Description: "Report a critical event to the Manager Agent (Cerebrate) for global analysis and action",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"severity": map[string]any{
					"type":        "string",
					"description": "Event severity: warning, critical",
					"enum":        []string{"warning", "critical"},
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief summary of the event",
				},
				"affected_workers": map[string]any{
					"type":        "string",
					"description": "Comma-separated list of affected Worker IDs",
				},
				"actions_taken": map[string]any{
					"type":        "string",
					"description": "Actions already taken at the regional level",
				},
			},
			"required": []string{"severity", "summary"},
		},
	}
}

func (s *EscalateSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "escalate",
		Usage:   "/escalate --severity critical --summary <text>",
		Examples: []string{
			`/escalate --severity critical --summary "12 nodes I/O anomaly, storage subsystem failure"`,
		},
	}
}

func (s *EscalateSkill) Validate(params skill.Params) error {
	if params.StringOr("summary", "") == "" {
		return fmt.Errorf("--summary is required")
	}
	return nil
}

func (s *EscalateSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	severity := params.StringOr("severity", "warning")
	summary := params.StringOr("summary", "")
	affected := params.StringOr("affected_workers", "")
	actions := params.StringOr("actions_taken", "")

	// Push event to Cerebrate via the outbound event channel.
	evt := &pb.OverlordEvent{
		EventId:   fmt.Sprintf("esc-%d", time.Now().UnixMilli()),
		Timestamp: time.Now().Unix(),
		Payload: &pb.OverlordEvent_Escalation{
			Escalation: &pb.AlertEvent{
				Description:   summary,
				Severity:      severity,
				RepairSummary: actions,
			},
		},
	}
	s.overlordServer.PushEvent(evt)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Escalated to Cerebrate:\n"))
	sb.WriteString(fmt.Sprintf("  Severity: %s\n", severity))
	sb.WriteString(fmt.Sprintf("  Summary:  %s\n", summary))
	if affected != "" {
		sb.WriteString(fmt.Sprintf("  Affected: %s\n", affected))
	}
	if actions != "" {
		sb.WriteString(fmt.Sprintf("  Actions:  %s\n", actions))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("escalated: %s", summary),
	}, nil
}
