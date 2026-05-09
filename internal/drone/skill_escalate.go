/*-------------------------------------------------------------------------
 *
 * skill_escalate.go
 *	  skill_escalate.go implements the escalate_to_overlord skill for
 *	  Worker Agent. When Worker's LLM detects a cross-node issue, it
 *	  calls this skill to hand off to Overlord.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/skill_escalate.go
 *
 *-------------------------------------------------------------------------
 */
// skill_escalate.go implements the escalate_to_overlord skill for Worker Agent.
// When Worker's LLM detects a cross-node issue, it calls this skill to hand off to Overlord.
package drone

import (
	"context"
	"fmt"
	"time"

	pb "github.com/sqlrush/opendb/internal/cluster/proto"
	"github.com/sqlrush/opendb/internal/skill"
)

// EscalateToOverlordSkill reports issues to Overlord that Worker cannot handle alone.
type EscalateToOverlordSkill struct{}

func NewEscalateToOverlordSkill() *EscalateToOverlordSkill {
	return &EscalateToOverlordSkill{}
}

func (s *EscalateToOverlordSkill) Name() string        { return "escalate" }
func (s *EscalateToOverlordSkill) Description() string { return "Escalate cross-node issue to Overlord" }
func (s *EscalateToOverlordSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *EscalateToOverlordSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "escalate_to_overlord",
		Description: "Report a cross-node issue to Overlord (Memory Agent). Use when the problem involves multiple nodes (replication lag, shared storage, cluster-wide events) and cannot be resolved locally.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "Why this issue needs cross-node coordination",
				},
				"suspected_scope": map[string]any{
					"type":        "string",
					"description": "Suspected scope: storage, network, replication, cluster",
				},
			},
			"required": []string{"reason"},
		},
	}
}

func (s *EscalateToOverlordSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "escalate",
		Usage:   "/escalate <reason>",
		Examples: []string{
			`/escalate "I/O latency surge, suspected shared storage issue"`,
		},
	}
}

func (s *EscalateToOverlordSkill) Validate(params skill.Params) error {
	reason := params.StringOr("reason", params.StringOr("args", ""))
	if reason == "" {
		return fmt.Errorf("reason is required")
	}
	return nil
}

func (s *EscalateToOverlordSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	reason := params.StringOr("reason", params.StringOr("args", ""))
	scope := params.StringOr("suspected_scope", "unknown")

	// The actual push happens via gRPC EventStream in AutonomyLoop.
	// This skill returns the escalation info for the AutonomyLoop to pick up.
	_ = &pb.AlertEvent{
		Description:   reason,
		CauseName:     fmt.Sprintf("cross-node: %s", scope),
		Severity:      "warning",
		SelfHealed:    false,
		RepairSummary: "escalated to Overlord for cross-node coordination",
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Escalated to Overlord:\n  Reason: %s\n  Scope: %s\n  Time: %s\n", reason, scope, time.Now().Format("15:04:05")),
		Summary:  fmt.Sprintf("escalated: %s", reason),
		Metadata: map[string]string{
			"escalated":       "true",
			"reason":          reason,
			"suspected_scope": scope,
		},
	}, nil
}
