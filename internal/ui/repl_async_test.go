/*-------------------------------------------------------------------------
 *
 * repl_async_test.go
 *	  Regression tests for async diagnosis progress handling.
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"context"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/dispatch"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

type fakeProgressDiagSkill struct {
	onProgress func(phase, message string, elapsed time.Duration, result *skill.Result, err error)
	result     *skill.Result
}

func (s *fakeProgressDiagSkill) Name() string        { return "llm" }
func (s *fakeProgressDiagSkill) Description() string { return "fake diag" }
func (s *fakeProgressDiagSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "llm", Description: "fake diag"}
}
func (s *fakeProgressDiagSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Command: "llm"} }
func (s *fakeProgressDiagSkill) Validate(skill.Params) error        { return nil }
func (s *fakeProgressDiagSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *fakeProgressDiagSkill) HasLLM() bool                       { return true }
func (s *fakeProgressDiagSkill) SetOnProgress(fn func(phase, message string, elapsed time.Duration, result *skill.Result, err error)) {
	s.onProgress = fn
}
func (s *fakeProgressDiagSkill) ClearOnProgress() { s.onProgress = nil }
func (s *fakeProgressDiagSkill) Execute(context.Context, skill.Params) (*skill.Result, error) {
	if s.onProgress != nil {
		s.onProgress("start", "fake start", 0, nil, nil)
		s.onProgress("done", "", 0, s.result, nil)
	}
	return s.result, nil
}

func TestStartDiagAsync_DoesNotEmitDuplicateDoneWhenSkillAlreadyEmittedDone(t *testing.T) {
	result := &skill.Result{Type: skill.ResultText, Rendered: "diagnosis"}
	fake := &fakeProgressDiagSkill{result: result}
	registry := skill.NewRegistry()
	registry.Register(fake)
	executor := skill.NewExecutor(registry, security.NewGuard(security.LevelDangerous, nil))

	r := newTestREPL(30, 100)
	r.registry = registry
	r.dispatcher = dispatch.NewDispatcher(registry, executor)
	r.diagSkill = fake

	r.startDiagAsync("当前数据库存在什么问题")

	var phases []DiagPhase
	for {
		select {
		case ev := <-r.diagCh:
			phases = append(phases, ev.Phase)
			if ev.Phase == DiagPhaseDone || ev.Phase == DiagPhaseError {
				goto gotTerminalEvent
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for terminal diag progress event; phases=%v", phases)
		}
	}

gotTerminalEvent:
	select {
	case ev := <-r.diagCh:
		t.Fatalf("unexpected duplicate diag terminal event after skill emitted done: %v; phases=%v", ev.Phase, phases)
	case <-time.After(50 * time.Millisecond):
	}
}
