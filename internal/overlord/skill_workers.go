/*-------------------------------------------------------------------------
 *
 * skill_workers.go
 *	  skill_workers.go implements Overlord skills for Worker management.
 *	  Skills: /workers (get_worker_status), /worker-memory
 *	  (get_worker_memory)
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/overlord/skill_workers.go
 *
 *-------------------------------------------------------------------------
 */
// skill_workers.go implements Overlord skills for Worker management.
// Skills: /workers (get_worker_status), /worker-memory (get_worker_memory)
package overlord

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/skill"
)

// ---- get_worker_status ----

// WorkersSkill shows the status of all managed Workers.
type WorkersSkill struct {
	workerMgr *WorkerManager
}

func NewWorkersSkill(wm *WorkerManager) *WorkersSkill {
	return &WorkersSkill{workerMgr: wm}
}

func (s *WorkersSkill) Name() string        { return "workers" }
func (s *WorkersSkill) Description() string { return "Show status of all managed Workers" }
func (s *WorkersSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *WorkersSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "get_worker_status",
		Description: "Get aggregated status of all managed Workers in this region",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *WorkersSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "workers",
		Usage:   "/workers",
		Examples: []string{"/workers"},
	}
}

func (s *WorkersSkill) Validate(_ skill.Params) error { return nil }

func (s *WorkersSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	statuses := s.workerMgr.GetAllWorkerStatus(ctx)

	if len(statuses) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "No workers connected.",
			Summary:  "0 workers",
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Workers: %d total, %d online\n\n", s.workerMgr.WorkerCount(), s.workerMgr.OnlineCount()))

	for _, st := range statuses {
		health := "—"
		if st.Health != nil {
			health = fmt.Sprintf("%d/100", st.Health.Score)
		}
		sb.WriteString(fmt.Sprintf("  %-20s  %-8s  %-8s  %s\n",
			st.WorkerId, st.DbType, st.State.String(), health))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     statuses,
		Rendered: sb.String(),
		Summary:  fmt.Sprintf("%d workers, %d online", s.workerMgr.WorkerCount(), s.workerMgr.OnlineCount()),
	}, nil
}

// ---- get_worker_memory ----

// WorkerMemorySkill reads a Worker's memory for cross-node analysis.
type WorkerMemorySkill struct {
	workerMgr *WorkerManager
}

func NewWorkerMemorySkill(wm *WorkerManager) *WorkerMemorySkill {
	return &WorkerMemorySkill{workerMgr: wm}
}

func (s *WorkerMemorySkill) Name() string        { return "worker-memory" }
func (s *WorkerMemorySkill) Description() string { return "Read a Worker's memory for cross-node analysis" }
func (s *WorkerMemorySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *WorkerMemorySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "get_worker_memory",
		Description: "Read memory files of a specific Worker for cross-node joint analysis",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"worker_id": map[string]any{
					"type":        "string",
					"description": "Worker ID to read memory from",
				},
			},
			"required": []string{"worker_id"},
		},
	}
}

func (s *WorkerMemorySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "worker-memory",
		Usage:   "/worker-memory <worker_id>",
		Examples: []string{"/worker-memory Oracle-A-037"},
	}
}

func (s *WorkerMemorySkill) Validate(params skill.Params) error {
	wid := params.StringOr("worker_id", params.StringOr("args", ""))
	if wid == "" {
		return fmt.Errorf("worker_id is required")
	}
	return nil
}

func (s *WorkerMemorySkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	wid := params.StringOr("worker_id", params.StringOr("args", ""))

	// TODO: read from local backup of worker memory files
	// For now, return placeholder indicating the capability.
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("Memory for worker %s: (memory sync not yet implemented)", wid),
		Summary:  fmt.Sprintf("worker %s memory", wid),
	}, nil
}
