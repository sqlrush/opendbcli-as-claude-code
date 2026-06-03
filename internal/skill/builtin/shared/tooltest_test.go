package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

type toolTestMockSkill struct {
	name  string
	level skill.SecurityLevel
}

func (s toolTestMockSkill) Name() string        { return s.name }
func (s toolTestMockSkill) Description() string { return "mock" }
func (s toolTestMockSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: s.name, Description: "mock", Parameters: map[string]any{"type": "object"}}
}
func (s toolTestMockSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Command: s.name} }
func (s toolTestMockSkill) Validate(skill.Params) error        { return nil }
func (s toolTestMockSkill) SecurityLevel() skill.SecurityLevel { return s.level }
func (s toolTestMockSkill) Execute(context.Context, skill.Params) (*skill.Result, error) {
	return &skill.Result{Type: skill.ResultText, Rendered: "Overall: OK", Summary: "ok"}, nil
}

func TestToolTestSkillRunsReadOnlyTool(t *testing.T) {
	registry := skill.NewRegistry()
	registry.Register(toolTestMockSkill{name: "health", level: skill.LevelReadOnly})
	executor := skill.NewExecutor(registry, security.NewGuard(security.LevelDangerous, nil))

	result, err := NewToolTestSkill(registry, executor).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "health"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Tool Test", "tool: health", "status: ok", "Overall: OK"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("tooltest output missing %q:\n%s", want, result.Rendered)
		}
	}
}

func TestToolTestSkillRejectsNonReadOnlyTool(t *testing.T) {
	registry := skill.NewRegistry()
	registry.Register(toolTestMockSkill{name: "kill", level: skill.LevelAdmin})
	executor := skill.NewExecutor(registry, security.NewGuard(security.LevelDangerous, nil))

	result, err := NewToolTestSkill(registry, executor).Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "kill"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"status: error", "refuse to run non-read-only skill"} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("tooltest output missing %q:\n%s", want, result.Rendered)
		}
	}
}
