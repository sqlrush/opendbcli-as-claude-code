/*-------------------------------------------------------------------------
 *
 * skill_llm_queue.go
 *	  skill_llm_queue.go implements the /llm-queue skill for Cerebrate.
 *	  Shows the current state of the LLM call priority queue.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cerebrate/skill_llm_queue.go
 *
 *-------------------------------------------------------------------------
 */
// skill_llm_queue.go implements the /llm-queue skill for Cerebrate.
// Shows the current state of the LLM call priority queue.
package cerebrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/skill"
)

// LLMQueueSkill exposes the LLM priority queue status as a CLI/LLM skill.
type LLMQueueSkill struct {
	queue *LLMPriorityQueue
}

func NewLLMQueueSkill(queue *LLMPriorityQueue) *LLMQueueSkill {
	return &LLMQueueSkill{queue: queue}
}

func (s *LLMQueueSkill) Name() string        { return "llm-queue" }
func (s *LLMQueueSkill) Description() string { return "Show LLM call priority queue status" }
func (s *LLMQueueSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *LLMQueueSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "llm_queue_status",
		Description: "Show the current LLM call priority queue. Displays pending calls ordered by severity (critical first), with counts per severity level.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *LLMQueueSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "llm-queue",
		Aliases:  []string{"lq"},
		Usage:    "/llm-queue",
		Examples: []string{"/llm-queue"},
	}
}

func (s *LLMQueueSkill) Validate(_ skill.Params) error { return nil }

func (s *LLMQueueSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	stats := s.queue.Stats()

	var sb strings.Builder
	sb.WriteString("LLM Call Priority Queue\n")
	sb.WriteString(strings.Repeat("═", 40) + "\n\n")
	sb.WriteString(fmt.Sprintf("  Total pending:  %d\n", stats.Total))
	sb.WriteString(fmt.Sprintf("  Critical:       %d\n", stats.Critical))
	sb.WriteString(fmt.Sprintf("  Warning:        %d\n", stats.Warning))
	sb.WriteString(fmt.Sprintf("  Info:           %d\n", stats.Info))

	if next := s.queue.Peek(); next != nil {
		sb.WriteString(fmt.Sprintf("\n  Next up: [%s] %s (from %s)\n",
			next.Severity, next.Description, next.SourceOverlord))
	} else {
		sb.WriteString("\n  Queue is empty — no pending LLM calls.\n")
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     stats,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("llm queue: %d pending (%d critical, %d warning, %d info)", stats.Total, stats.Critical, stats.Warning, stats.Info),
	}, nil
}
