/*-------------------------------------------------------------------------
 *
 * tooltest.go
 *	  ToolTestSkill runs one read-only skill directly, bypassing the LLM.
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/diagtrace"
	"github.com/sqlrush/opendb/internal/skill"
)

const toolTestOutputLimit = 1600

type ToolTestSkill struct {
	registry *skill.Registry
	executor *skill.Executor
}

type toolTestReport struct {
	Command        string         `json:"command"`
	Tool           string         `json:"tool"`
	Status         string         `json:"status"`
	Error          string         `json:"error,omitempty"`
	ElapsedMS      int64          `json:"elapsed_ms"`
	SecurityLevel  string         `json:"security_level,omitempty"`
	Params         map[string]any `json:"params"`
	OutputBytes    int            `json:"output_bytes,omitempty"`
	OutputSummary  string         `json:"output_summary,omitempty"`
	StartedAt      string         `json:"started_at"`
	EndedAt        string         `json:"ended_at"`
	Recommendation string         `json:"recommendation,omitempty"`
}

func NewToolTestSkill(registry *skill.Registry, executor *skill.Executor) *ToolTestSkill {
	return &ToolTestSkill{registry: registry, executor: executor}
}

func (s *ToolTestSkill) Name() string                       { return "tooltest" }
func (s *ToolTestSkill) Description() string                { return "Run a read-only skill directly for diagnostics" }
func (s *ToolTestSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ToolTestSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "tooltest",
		Description: "Run one read-only DBAA skill directly, bypassing LLM routing and tool-calling",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Tool name plus optional key=value params, for example: health or topsql args=el limit=5",
				},
			},
		},
	}
}

func (s *ToolTestSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "tooltest",
		Usage:       "/tooltest <tool> [key=value ...] [--json]",
		Description: "Execute one read-only tool directly to distinguish tool/DB permission failures from LLM routing or function-calling failures.",
		Examples: []string{
			"/tooltest health",
			"/tooltest waits",
			"/tooltest topsql args=el limit=5",
			"/tooltest sqltune args=1775585557 mode=quick --json",
		},
	}
}

func (s *ToolTestSkill) Validate(params skill.Params) error {
	tool, _, _ := parseToolTestArgs(params.StringOr("args", ""))
	if tool == "" {
		return fmt.Errorf("用法: /tooltest <tool> [key=value ...] [--json]")
	}
	return nil
}

func (s *ToolTestSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	toolName, rawParams, asJSON := parseToolTestArgs(params.StringOr("args", ""))
	start := time.Now()
	report := toolTestReport{
		Command:   "tooltest",
		Tool:      toolName,
		Params:    rawParams,
		StartedAt: start.Format(time.RFC3339),
	}
	defer func() {
		diagtrace.SetLastToolTest(report)
	}()

	if s.registry == nil || s.executor == nil {
		report.Status = "error"
		report.Error = "tooltest is not fully initialized"
		report.Recommendation = "检查 DBAA 初始化流程是否注册了 skill executor。"
		fillToolTestElapsed(&report, start)
		return toolTestResult(report, asJSON), nil
	}
	if toolName == "tooltest" {
		report.Status = "error"
		report.Error = "refuse to execute tooltest recursively"
		fillToolTestElapsed(&report, start)
		return toolTestResult(report, asJSON), nil
	}

	sk, ok := s.registry.Get(toolName)
	if !ok {
		report.Status = "error"
		report.Error = "skill not found"
		report.Recommendation = "先执行 /skills 或 /help 查看当前数据库可用工具。"
		fillToolTestElapsed(&report, start)
		return toolTestResult(report, asJSON), nil
	}
	report.SecurityLevel = sk.SecurityLevel().String()
	if sk.SecurityLevel() > skill.LevelReadOnly {
		report.Status = "error"
		report.Error = fmt.Sprintf("refuse to run non-read-only skill: %s", sk.SecurityLevel().String())
		report.Recommendation = "tooltest 仅用于只读排查；变更类命令请直接按原命令流程执行。"
		fillToolTestElapsed(&report, start)
		return toolTestResult(report, asJSON), nil
	}

	result, err := s.executor.Execute(ctx, toolName, skill.ParamsFromMap(rawParams))
	fillToolTestElapsed(&report, start)
	if err != nil {
		report.Status = "error"
		report.Error = err.Error()
		report.Recommendation = "工具本身执行失败；优先检查数据库连接、权限、SQL 兼容性和工具参数。"
		return toolTestResult(report, asJSON), nil
	}
	report.Status = "ok"
	if result != nil {
		output := strings.TrimSpace(result.Rendered)
		if output == "" {
			output = strings.TrimSpace(result.Summary)
		}
		report.OutputBytes = len([]byte(output))
		report.OutputSummary = diagtrace.SummarizeText(output, toolTestOutputLimit)
	}
	return toolTestResult(report, asJSON), nil
}

func parseToolTestArgs(args string) (string, map[string]any, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	params := make(map[string]any)
	if len(fields) == 0 {
		return "", params, false
	}
	toolName := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	var argParts []string
	asJSON := false
	for _, f := range fields[1:] {
		if f == "--json" || strings.EqualFold(f, "json") {
			asJSON = true
			continue
		}
		if strings.HasPrefix(f, "{") {
			var raw map[string]any
			if err := json.Unmarshal([]byte(strings.Join(fields[1:], " ")), &raw); err == nil {
				return toolName, raw, asJSON
			}
		}
		if k, v, ok := strings.Cut(f, "="); ok {
			params[strings.TrimSpace(k)] = parseToolTestValue(strings.TrimSpace(v))
			continue
		}
		argParts = append(argParts, f)
	}
	if len(argParts) > 0 && params["args"] == nil {
		params["args"] = strings.Join(argParts, " ")
	}
	return toolName, params, asJSON
}

func parseToolTestValue(v string) any {
	v = strings.Trim(v, `"'`)
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	if i, err := strconv.Atoi(v); err == nil {
		return i
	}
	return v
}

func fillToolTestElapsed(report *toolTestReport, start time.Time) {
	report.ElapsedMS = time.Since(start).Milliseconds()
	report.EndedAt = time.Now().Format(time.RFC3339)
}

func toolTestResult(report toolTestReport, asJSON bool) *skill.Result {
	var rendered string
	if asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			rendered = fmt.Sprintf(`{"error":%q}`, err.Error())
		} else {
			rendered = string(data)
		}
	} else {
		rendered = renderToolTest(report)
	}
	return &skill.Result{Type: skill.ResultText, Data: report, Rendered: rendered, Summary: "tooltest " + report.Status}
}

func renderToolTest(report toolTestReport) string {
	var b strings.Builder
	b.WriteString("Tool Test\n\n")
	writeModelTestKV(&b, "tool", report.Tool)
	writeModelTestKV(&b, "status", report.Status)
	writeModelTestKV(&b, "elapsed", time.Duration(report.ElapsedMS*int64(time.Millisecond)).String())
	writeModelTestKV(&b, "security_level", report.SecurityLevel)
	b.WriteString("\nparams:\n")
	if len(report.Params) == 0 {
		b.WriteString("{}\n")
	} else if data, err := json.MarshalIndent(report.Params, "", "  "); err == nil {
		b.WriteString(string(data) + "\n")
	}
	if report.OutputSummary != "" {
		b.WriteString("\noutput:\n")
		b.WriteString(report.OutputSummary + "\n")
	}
	if report.Error != "" {
		writeModelTestKV(&b, "error", report.Error)
	}
	if report.Recommendation != "" {
		writeModelTestKV(&b, "recommendation", report.Recommendation)
	}
	return strings.TrimRight(b.String(), "\n")
}
