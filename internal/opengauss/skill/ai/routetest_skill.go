/*-------------------------------------------------------------------------
 *
 * routetest_skill.go
 *	  Dry-run DBAA diagnosis routing without calling LLM or tools.
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/diagtrace"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/opengauss/agent"
	"github.com/sqlrush/opendb/internal/skill"
)

type RouteTestSkill struct {
	modelMgr *model.Manager
}

type routeTestReport struct {
	Command     string              `json:"command"`
	ActiveModel string              `json:"active_model,omitempty"`
	ToolMode    string              `json:"tool_mode,omitempty"`
	Capability  string              `json:"capability,omitempty"`
	Analysis    agent.RouteAnalysis `json:"analysis"`
}

func NewRouteTestSkill(modelMgr *model.Manager) *RouteTestSkill {
	return &RouteTestSkill{modelMgr: modelMgr}
}

func (s *RouteTestSkill) Name() string { return "routetest" }
func (s *RouteTestSkill) Description() string {
	return "Dry-run DBAA route decisions without calling LLM or tools"
}
func (s *RouteTestSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *RouteTestSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "routetest",
		Description: "Show whether a question will enter LLM, direct skill, or forced tool-first flow without executing anything",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Question text, optionally with --json",
				},
			},
		},
	}
}

func (s *RouteTestSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "routetest",
		Usage:       "/routetest <question> [--json]",
		Description: "Dry-run the DBAA router to explain whether a question will call LLM, use direct routing, or force tools before the model.",
		Examples: []string{
			"/routetest 当前数据库有什么问题",
			"/routetest sql id 1775585557 如何优化 --json",
		},
	}
}

func (s *RouteTestSkill) Validate(params skill.Params) error {
	if strings.TrimSpace(stripRouteTestJSONFlag(params.StringOr("args", ""))) == "" {
		return fmt.Errorf("用法: /routetest <question> [--json]")
	}
	return nil
}

func (s *RouteTestSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	asJSON := routeTestJSON(args)
	question := strings.TrimSpace(stripRouteTestJSONFlag(args))
	modelName, toolMode, capability := "", "", ""
	if s.modelMgr != nil {
		modelName = s.modelMgr.ActiveName()
		toolMode = s.modelMgr.ToolMode()
		capability = s.modelMgr.Capability()
	}
	report := routeTestReport{
		Command:     "routetest",
		ActiveModel: modelName,
		ToolMode:    routeDisplayToolMode(toolMode),
		Capability:  capability,
		Analysis:    agent.AnalyzeRoute(question, capability, toolMode, modelName),
	}
	diagtrace.SetLastRouteTest(report)
	if asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return &skill.Result{Type: skill.ResultText, Rendered: fmt.Sprintf(`{"error":%q}`, err.Error()), Summary: "routetest error"}, nil
		}
		return &skill.Result{Type: skill.ResultText, Data: report, Rendered: string(data), Summary: "routetest ok"}, nil
	}
	return &skill.Result{Type: skill.ResultText, Data: report, Rendered: renderRouteTest(report), Summary: "routetest ok"}, nil
}

func renderRouteTest(report routeTestReport) string {
	a := report.Analysis
	var b strings.Builder
	b.WriteString("Route Test\n\n")
	writeRouteTestKV(&b, "input", a.Input)
	writeRouteTestKV(&b, "active_model", routeValueOrDash(report.ActiveModel))
	writeRouteTestKV(&b, "tool_mode", routeValueOrDash(report.ToolMode))
	writeRouteTestKV(&b, "capability", routeValueOrDash(report.Capability))
	b.WriteString("\n")
	writeRouteTestKV(&b, "intent", a.Intent)
	writeRouteTestKV(&b, "mode", a.Mode)
	writeRouteTestKV(&b, "llm_used", fmt.Sprintf("%v", a.UseLLM))
	writeRouteTestKV(&b, "route_kind", a.RouteKind)
	writeRouteTestKV(&b, "force_initial_tools", fmt.Sprintf("%v", a.ForceInitialTools))
	writeRouteTestKV(&b, "require_tool_evidence", fmt.Sprintf("%v", a.RequireToolEvidence))
	writeRouteTestKV(&b, "managed_evidence_llm", fmt.Sprintf("%v", a.ManagedEvidenceLLM))
	if a.Skill != "" {
		writeRouteTestKV(&b, "skill", a.Skill)
	}
	if len(a.Params) > 0 {
		if data, err := json.Marshal(a.Params); err == nil {
			writeRouteTestKV(&b, "params", string(data))
		}
	}
	if len(a.ForcedTools) > 0 {
		b.WriteString("\nforced_tools:\n")
		for _, tc := range a.ForcedTools {
			b.WriteString(fmt.Sprintf("- %s %s\n", tc.Name, tc.Arguments))
		}
	}
	if len(a.ExpectedFlow) > 0 {
		b.WriteString("\nexpected_flow:\n")
		for i, step := range a.ExpectedFlow {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
	}
	writeRouteTestKV(&b, "reason", a.Reason)
	return strings.TrimRight(b.String(), "\n")
}

func writeRouteTestKV(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString(key + ": " + value + "\n")
}

func routeValueOrDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func routeDisplayToolMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "native"
	}
	return mode
}

func routeTestJSON(args string) bool {
	for _, f := range strings.Fields(args) {
		if f == "--json" || strings.EqualFold(f, "json") {
			return true
		}
	}
	return false
}

func stripRouteTestJSONFlag(args string) string {
	var out []string
	for _, f := range strings.Fields(args) {
		if f == "--json" || strings.EqualFold(f, "json") {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}
