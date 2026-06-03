/*-------------------------------------------------------------------------
 *
 * modeltest.go
 *	  ModelTestSkill verifies the active LLM endpoint with chat, stream,
 *	  native function-calling, and prompt-mode tool-call probes.
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/diagtrace"
	engineprovider "github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
)

const (
	modelTestDefaultPrompt     = "请只回答：pong"
	modelTestDefaultTimeout    = 30 * time.Second
	modelTestMaxTokens         = 20
	modelTestNativeFCMaxTokens = 256
	modelTestLongMaxTokens     = 2048
)

// ModelTestSkill sends controlled probes through the active model.
type ModelTestSkill struct {
	manager *model.Manager
}

type modelTestOptions struct {
	Mode    string
	Timeout time.Duration
	Prompt  string
	JSON    bool
	Force   bool
}

type modelTestReport struct {
	Command        string              `json:"command"`
	Mode           string              `json:"mode"`
	Status         string              `json:"status"`
	Error          string              `json:"error,omitempty"`
	ElapsedMS      int64               `json:"elapsed_ms"`
	TimeoutMS      int64               `json:"timeout_ms"`
	FirstTokenMS   int64               `json:"first_token_ms,omitempty"`
	Chunks         int                 `json:"chunks,omitempty"`
	ActiveModel    string              `json:"active_model,omitempty"`
	Provider       string              `json:"provider,omitempty"`
	Vendor         string              `json:"vendor,omitempty"`
	BaseURL        string              `json:"base_url,omitempty"`
	Model          string              `json:"model,omitempty"`
	ToolMode       string              `json:"tool_mode,omitempty"`
	Capability     string              `json:"capability,omitempty"`
	Request        string              `json:"request"`
	InputTokens    int                 `json:"input_tokens,omitempty"`
	OutputTokens   int                 `json:"output_tokens,omitempty"`
	StopReason     string              `json:"stop_reason,omitempty"`
	Content        string              `json:"content,omitempty"`
	ToolCalls      []modelTestToolCall `json:"tool_calls,omitempty"`
	ParserStatus   string              `json:"parser_status,omitempty"`
	ParserError    string              `json:"parser_error,omitempty"`
	Corrected      int                 `json:"corrected,omitempty"`
	StartedAt      string              `json:"started_at"`
	EndedAt        string              `json:"ended_at"`
	Recommendation string              `json:"recommendation,omitempty"`
}

type modelTestToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// NewModelTestSkill creates a model connectivity test command.
func NewModelTestSkill(manager *model.Manager) *ModelTestSkill {
	return &ModelTestSkill{manager: manager}
}

func (s *ModelTestSkill) Name() string { return "modeltest" }
func (s *ModelTestSkill) Description() string {
	return "Test active LLM endpoint and tool-calling behavior"
}
func (s *ModelTestSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ModelTestSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "modeltest",
		Description: "Test the active LLM endpoint with ping, stream, native FC, prompt FC, or long-output probes",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Mode ping|stream|fc|promptfc|long, optional timeout=<seconds>, force=true, --json and prompt text",
				},
			},
		},
	}
}

func (s *ModelTestSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "modeltest",
		Usage:       "/modeltest [ping|stream|fc|promptfc|long] [timeout=30] [force=true] [--json] [prompt]",
		Description: "Use the active model config to test HTTP reachability, streaming, native function-calling, prompt-mode tool-call parsing, and long-output timeout behavior.",
		Examples: []string{
			"/modeltest",
			"/modeltest stream timeout=120",
			"/modeltest fc --json",
			"/modeltest fc force=true",
			"/modeltest promptfc timeout=120",
			"/modeltest long timeout=180",
		},
	}
}

func (s *ModelTestSkill) Validate(_ skill.Params) error { return nil }

func (s *ModelTestSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	opts := parseModelTestArgs(params.StringOr("args", ""))
	if s.manager == nil || s.manager.Provider() == nil {
		report := modelTestReport{
			Command:        "modeltest",
			Mode:           opts.Mode,
			Status:         "error",
			Error:          "no active model; use /model to select one",
			TimeoutMS:      opts.Timeout.Milliseconds(),
			StartedAt:      time.Now().Format(time.RFC3339),
			EndedAt:        time.Now().Format(time.RFC3339),
			Recommendation: "先执行 /model 查看并切换 active_model。",
		}
		diagtrace.SetLastModelTest(report)
		return modelTestResult(report, opts.JSON), nil
	}

	profile := s.manager.Active()
	provider := s.manager.Provider()
	var report modelTestReport
	switch opts.Mode {
	case "stream":
		report = runModelTestStream(ctx, profile, provider, opts)
	case "fc":
		report = runModelTestNativeFC(ctx, profile, provider, opts)
	case "promptfc":
		report = runModelTestPromptFC(ctx, profile, provider, opts)
	case "long":
		report = runModelTestLong(ctx, profile, provider, opts)
	default:
		report = runModelTestPing(ctx, profile, provider, opts)
	}
	diagtrace.SetLastModelTest(report)
	return modelTestResult(report, opts.JSON), nil
}

func runModelTestPing(ctx context.Context, profile *model.ModelProfile, provider llm.Provider, opts modelTestOptions) modelTestReport {
	req := modelTestRequest(opts.Prompt, modelTestMaxTokens, nil)
	report := newModelTestReport("ping", profile, provider, opts, "chat/completions max_tokens=20")
	start := time.Now()
	resp, err := chatWithTimeout(ctx, provider, req, opts.Timeout)
	fillModelTestElapsed(&report, start)
	if err != nil {
		report.Status = "error"
		report.Error = err.Error()
		report.Recommendation = "检查 base_url、api_key、model 名称、vLLM 网关日志和超时配置。"
		return report
	}
	fillModelTestResponse(&report, resp)
	report.Status = "ok"
	return report
}

func runModelTestLong(ctx context.Context, profile *model.ModelProfile, provider llm.Provider, opts modelTestOptions) modelTestReport {
	prompt := opts.Prompt
	if strings.TrimSpace(prompt) == "" || prompt == modelTestDefaultPrompt {
		prompt = "请输出一份约1200字的中文数据库诊断报告，包含根因分析、证据、风险、建议动作。不要调用工具。"
	}
	req := modelTestRequest(prompt, modelTestLongMaxTokens, nil)
	report := newModelTestReport("long", profile, provider, opts, fmt.Sprintf("chat/completions max_tokens=%d", modelTestLongMaxTokens))
	start := time.Now()
	resp, err := chatWithTimeout(ctx, provider, req, opts.Timeout)
	fillModelTestElapsed(&report, start)
	if err != nil {
		report.Status = "error"
		report.Error = err.Error()
		report.Recommendation = "长输出失败通常指向客户网关/模型超时；提高 vLLM/gateway timeout，或改用流式输出。"
		return report
	}
	fillModelTestResponse(&report, resp)
	report.Status = "ok"
	return report
}

func runModelTestNativeFC(ctx context.Context, profile *model.ModelProfile, provider llm.Provider, opts modelTestOptions) modelTestReport {
	req := modelTestRequest(nativeFCPrompt(opts.Prompt), modelTestNativeFCMaxTokens, []any{modelTestHealthToolSchema()})
	requestDesc := "chat/completions tools=1 max_tokens=256"
	if opts.Force {
		req.ToolChoice = modelTestHealthToolChoice()
		requestDesc = "chat/completions tools=1 tool_choice=health max_tokens=256"
	}
	report := newModelTestReport("fc", profile, provider, opts, requestDesc)
	start := time.Now()
	resp, err := chatWithTimeout(ctx, provider, req, opts.Timeout)
	fillModelTestElapsed(&report, start)
	if err != nil {
		report.Status = "error"
		report.Error = err.Error()
		if opts.Force {
			report.Recommendation = "原生 FC 强制调用请求失败；检查模型/网关是否支持 OpenAI tool_choice。DeepSeek/Qwen thinking 模式可能拒绝 tool_choice，可先用 /modeltest fc 或改用 tool_mode: prompt。"
		} else {
			report.Recommendation = "原生 FC 请求失败；检查 vLLM 是否启用 --enable-auto-tool-choice/--tool-call-parser/chat_template，或改用 tool_mode: prompt。"
		}
		return report
	}
	fillModelTestResponse(&report, resp)
	if len(report.ToolCalls) == 0 {
		report.Status = "error"
		report.Error = "model returned no native tool_calls"
		if opts.Force {
			report.Recommendation = "HTTP 已返回，但即使 tool_choice=health 也没有返回标准 choices[0].message.tool_calls；若真实 /diagtrace 能看到 tool.round_* 执行则业务链路正常，否则检查模型 FC 能力、vLLM --enable-auto-tool-choice/--tool-call-parser/chat_template。"
		} else {
			report.Recommendation = "HTTP 已返回，但模型没有自动返回标准 choices[0].message.tool_calls；检查模型 FC 能力、vLLM --enable-auto-tool-choice/--tool-call-parser/chat_template。若 /modeltest promptfc 成功，可现场改用 tool_mode: prompt。"
		}
		return report
	}
	report.Status = "ok"
	return report
}

func runModelTestPromptFC(ctx context.Context, profile *model.ModelProfile, provider llm.Provider, opts modelTestOptions) modelTestReport {
	req := modelTestRequest(promptFCPrompt(opts.Prompt), 256, nil)
	report := newModelTestReport("promptfc", profile, provider, opts, "chat/completions prompt-tools=1 max_tokens=256")
	start := time.Now()
	resp, err := chatWithTimeout(ctx, provider, req, opts.Timeout)
	fillModelTestElapsed(&report, start)
	if err != nil {
		report.Status = "error"
		report.Error = err.Error()
		report.Recommendation = "prompt FC 请求失败，先用 /modeltest ping 确认基础连通性。"
		return report
	}
	fillModelTestResponse(&report, resp)
	if len(report.ToolCalls) == 0 && resp != nil {
		parser := engineprovider.NewJSONToolCallParser([]string{"health"})
		parsed := parser.Parse(resp.Content)
		report.Corrected = parsed.Corrected
		if parsed.ParseError != nil {
			report.ParserStatus = "error"
			report.ParserError = parsed.ParseError.Error()
		} else if len(parsed.Calls) > 0 {
			report.ParserStatus = "ok"
			report.ToolCalls = engineToolCallsToModelTest(parsed.Calls)
		} else {
			report.ParserStatus = "no_tool_calls"
		}
	}
	if len(report.ToolCalls) == 0 {
		report.Status = "error"
		if report.Error == "" {
			report.Error = "prompt tool output was not parsed into tool_calls"
		}
		report.Recommendation = "模型没有按 PromptToolAdapter JSON 格式输出；检查 tool_mode: prompt、compat_mode、strip_think 和模型输出。"
		return report
	}
	report.Status = "ok"
	if report.ParserStatus == "" {
		report.ParserStatus = "native_tool_calls"
	}
	return report
}

func runModelTestStream(ctx context.Context, profile *model.ModelProfile, provider llm.Provider, opts modelTestOptions) modelTestReport {
	req := modelTestRequest(opts.Prompt, modelTestMaxTokens, nil)
	report := newModelTestReport("stream", profile, provider, opts, "chat/completions stream=true max_tokens=20")
	callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	start := time.Now()
	report.StartedAt = start.Format(time.RFC3339)
	stream, err := provider.ChatStream(callCtx, req)
	if err != nil {
		fillModelTestElapsed(&report, start)
		report.Status = "error"
		report.Error = err.Error()
		report.Recommendation = "流式接口不可用；检查 vLLM/gateway 是否支持 SSE streaming。"
		return report
	}
	defer stream.Close()

	var content strings.Builder
	var firstToken time.Duration
	for {
		ev, nextErr := stream.Next()
		if nextErr != nil {
			if errors.Is(nextErr, io.EOF) {
				break
			}
			fillModelTestElapsed(&report, start)
			report.Status = "error"
			report.Error = nextErr.Error()
			report.Content = strings.TrimSpace(content.String())
			report.Recommendation = "流式中途失败；检查网关 SSE、idle timeout、模型服务日志。"
			return report
		}
		switch ev.Type {
		case llm.StreamTextDelta:
			if ev.Content != "" {
				if firstToken == 0 {
					firstToken = time.Since(start)
				}
				report.Chunks++
				content.WriteString(ev.Content)
			}
		case llm.StreamToolCallDelta:
			if ev.ToolCall != nil {
				if firstToken == 0 {
					firstToken = time.Since(start)
				}
				report.Chunks++
				report.ToolCalls = append(report.ToolCalls, modelTestToolCall{ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments})
			}
		case llm.StreamDone:
			report.StopReason = ev.FinishReason
			fillModelTestElapsed(&report, start)
			report.FirstTokenMS = firstToken.Milliseconds()
			report.Content = strings.TrimSpace(content.String())
			report.Status = "ok"
			return report
		}
	}
	fillModelTestElapsed(&report, start)
	report.FirstTokenMS = firstToken.Milliseconds()
	report.Content = strings.TrimSpace(content.String())
	if report.Content == "" && len(report.ToolCalls) == 0 {
		report.Status = "error"
		report.Error = "stream ended without content or tool_calls"
		report.Recommendation = "流式连接建立但无有效输出；检查模型是否长时间 thinking 或网关是否吞掉 SSE chunk。"
		return report
	}
	report.Status = "ok"
	return report
}

func chatWithTimeout(ctx context.Context, provider llm.Provider, req llm.ChatRequest, timeout time.Duration) (*llm.Response, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return provider.Chat(callCtx, req)
}

func modelTestRequest(prompt string, maxTokens int, tools []any) llm.ChatRequest {
	req := llm.ChatRequest{
		Messages:  []llm.Message{{Role: "user", Content: prompt}},
		Tools:     tools,
		MaxTokens: maxTokens,
	}
	zero := 0.0
	req.Temperature = &zero
	return req
}

func parseModelTestArgs(args string) modelTestOptions {
	opts := modelTestOptions{Mode: "ping", Timeout: modelTestDefaultTimeout}
	var promptParts []string
	for _, part := range strings.Fields(strings.TrimSpace(args)) {
		lower := strings.ToLower(part)
		switch {
		case lower == "--json" || lower == "json":
			opts.JSON = true
		case lower == "force" || lower == "--force" || lower == "force=true":
			opts.Force = true
		case lower == "force=false":
			opts.Force = false
		case lower == "ping" || lower == "stream" || lower == "fc" || lower == "promptfc" || lower == "long":
			opts.Mode = lower
		case strings.HasPrefix(lower, "timeout="):
			if d := parseModelTestTimeout(strings.TrimPrefix(part, "timeout=")); d > 0 {
				opts.Timeout = d
			}
		default:
			promptParts = append(promptParts, part)
		}
	}
	opts.Prompt = strings.TrimSpace(strings.Join(promptParts, " "))
	if opts.Prompt == "" {
		opts.Prompt = modelTestDefaultPrompt
	}
	return opts
}

func parseModelTestTimeout(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func newModelTestReport(mode string, profile *model.ModelProfile, provider llm.Provider, opts modelTestOptions, request string) modelTestReport {
	now := time.Now()
	report := modelTestReport{
		Command:   "modeltest",
		Mode:      mode,
		TimeoutMS: opts.Timeout.Milliseconds(),
		Request:   request,
		StartedAt: now.Format(time.RFC3339),
	}
	if profile != nil {
		report.ActiveModel = profile.Name
		report.Provider = profile.Provider
		report.Vendor = profile.DisplayVendor()
		report.BaseURL = profile.BaseURL
		report.Model = profile.Model
		report.ToolMode = displayToolMode(profile.ToolMode)
		report.Capability = profile.Capability
	} else if provider != nil {
		report.ActiveModel = "(unknown)"
		report.Provider = provider.Name()
	}
	return report
}

func fillModelTestElapsed(report *modelTestReport, start time.Time) {
	elapsed := time.Since(start)
	report.ElapsedMS = elapsed.Milliseconds()
	report.EndedAt = time.Now().Format(time.RFC3339)
}

func fillModelTestResponse(report *modelTestReport, resp *llm.Response) {
	if resp == nil {
		return
	}
	report.InputTokens = resp.Usage.InputTokens
	report.OutputTokens = resp.Usage.OutputTokens
	report.StopReason = strings.TrimSpace(resp.StopReason)
	report.Content = strings.TrimSpace(resp.Content)
	report.ToolCalls = llmToolCallsToModelTest(resp.ToolCalls)
}

func llmToolCallsToModelTest(calls []llm.ToolCall) []modelTestToolCall {
	out := make([]modelTestToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, modelTestToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	return out
}

func engineToolCallsToModelTest(calls []engineprovider.ToolCall) []modelTestToolCall {
	out := make([]modelTestToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, modelTestToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
	}
	return out
}

func modelTestResult(report modelTestReport, asJSON bool) *skill.Result {
	var rendered string
	if asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			rendered = fmt.Sprintf(`{"error":%q}`, err.Error())
		} else {
			rendered = string(data)
		}
	} else {
		rendered = renderModelTestReport(report)
	}
	summary := "modeltest " + report.Status
	return &skill.Result{Type: skill.ResultText, Data: report, Rendered: rendered, Summary: summary}
}

func renderModelTestReport(report modelTestReport) string {
	var b strings.Builder
	b.WriteString("Model Test\n\n")
	writeModelTestKV(&b, "mode", report.Mode)
	writeModelTestKV(&b, "status", report.Status)
	writeModelTestKV(&b, "active_model", valueOrDash(report.ActiveModel))
	writeModelTestKV(&b, "provider", valueOrDash(report.Provider))
	writeModelTestKV(&b, "vendor", valueOrDash(report.Vendor))
	writeModelTestKV(&b, "base_url", valueOrDash(report.BaseURL))
	writeModelTestKV(&b, "model", valueOrDash(report.Model))
	writeModelTestKV(&b, "tool_mode", valueOrDash(report.ToolMode))
	writeModelTestKV(&b, "capability", valueOrDash(report.Capability))
	writeModelTestKV(&b, "request", report.Request)
	writeModelTestKV(&b, "timeout", time.Duration(report.TimeoutMS*int64(time.Millisecond)).String())
	writeModelTestKV(&b, "elapsed", time.Duration(report.ElapsedMS*int64(time.Millisecond)).String())
	if report.FirstTokenMS > 0 {
		writeModelTestKV(&b, "first_token", time.Duration(report.FirstTokenMS*int64(time.Millisecond)).String())
	}
	if report.Chunks > 0 {
		writeModelTestKV(&b, "chunks", fmt.Sprintf("%d", report.Chunks))
	}
	if report.InputTokens > 0 || report.OutputTokens > 0 {
		writeModelTestKV(&b, "tokens", fmt.Sprintf("input=%d output=%d total=%d", report.InputTokens, report.OutputTokens, report.InputTokens+report.OutputTokens))
	}
	if report.StopReason != "" {
		writeModelTestKV(&b, "stop_reason", report.StopReason)
	}
	if report.ParserStatus != "" {
		writeModelTestKV(&b, "parser_status", report.ParserStatus)
	}
	if report.ParserError != "" {
		writeModelTestKV(&b, "parser_error", report.ParserError)
	}
	if len(report.ToolCalls) > 0 {
		b.WriteString("\ntool_calls:\n")
		for _, tc := range report.ToolCalls {
			b.WriteString(fmt.Sprintf("- %s %s\n", valueOrDash(tc.Name), valueOrDash(tc.Arguments)))
		}
	}
	if report.Content != "" {
		writeModelTestKV(&b, "content", report.Content)
	}
	if report.Error != "" {
		writeModelTestKV(&b, "error", report.Error)
	}
	if report.Recommendation != "" {
		writeModelTestKV(&b, "recommendation", report.Recommendation)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeModelTestKV(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString(key + ": " + value + "\n")
}

func modelTestHealthToolSchema() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "health",
			"description": "检查数据库健康状态。",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func modelTestHealthToolChoice() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "health",
		},
	}
}

func nativeFCPrompt(prompt string) string {
	if strings.TrimSpace(prompt) != "" && prompt != modelTestDefaultPrompt {
		return prompt
	}
	return "请调用 health 工具，参数为空。不要直接回答自然语言。"
}

func promptFCPrompt(prompt string) string {
	if strings.TrimSpace(prompt) != "" && prompt != modelTestDefaultPrompt {
		return prompt
	}
	return `你可以使用工具:
- health: 检查数据库健康状态，参数 JSON schema: {}

请只输出 JSON，不要输出解释文字:
{"tool_calls":[{"name":"health","args":{}}]}`
}

func valueOrDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func displayToolMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "auto"
	}
	return mode
}
