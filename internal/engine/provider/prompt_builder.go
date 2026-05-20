/*-------------------------------------------------------------------------
 *
 * prompt_builder.go
 *	  v1.2.0 PromptToolAdapter framework. PromptBuilder is a per-model
 *	  plugin that controls how tool-calling is delivered to the LLM and
 *	  how the response is parsed back into ToolCalls.
 *
 *	  Two implementations:
 *	    - NativeFCBuilder (default): tools go through the provider's native
 *	      tools API parameter. resp.ToolCalls comes back structured.
 *	      Behavior is byte-identical to pre-v1.2.0.
 *	    - PromptModeBuilder (new): serializes tool descriptions into the
 *	      system prompt, parses JSON tool_calls back from resp.Content.
 *	      For LLMs/deployments that don't expose function calling.
 *
 *	  Selection happens once at provider construction based on
 *	  ModelProfile.ToolMode. The Engine main loop is unaware which mode
 *	  is in use — both produce a Response with ToolCalls populated the
 *	  same way.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/prompt_builder.go
 *
 *-------------------------------------------------------------------------
 */
package provider

// FilterContext is the per-turn input that a tool filter sees. Lives in
// provider (not tool) so PromptBuilder can depend on it without creating
// a circular import between provider and tool packages.
//
// The actual filter implementation (SceneBasedFilter) lives in
// internal/engine/tool/filter.go and consumes/produces ToolSchema from
// this package.
type FilterContext struct {
	UserMessage    string
	PreviousRounds int
	LastToolCalls  []string
	Database       string
}

// ToolFilterFunc is the function signature a PromptBuilder uses to narrow
// the tool list per round. tool.SceneBasedFilter.Filter satisfies this.
// Pass nil to disable filtering (all tools always injected).
type ToolFilterFunc func(allTools []ToolSchema, ctx FilterContext) []ToolSchema

// PromptBuilder is the per-model plugin that adapts tool-calling between
// the Engine's native (FC-style) request/response contract and what the
// underlying LLM actually understands.
//
// Call order during a single Chat() invocation:
//
//	1. BuildSystemPrompt(base, tools)  → final system prompt string
//	2. PrepareRequest(req)             → mutate req (e.g., clear req.Tools
//	                                      for prompt mode)
//	3. Provider sends req to LLM API
//	4. PostProcessResponse(resp)       → mutate resp (e.g., parse JSON
//	                                      tool_calls out of resp.Content)
//
// NativeFCBuilder is a no-op on steps 1, 2, 4 — pre-v1.2.0 behavior preserved.
type PromptBuilder interface {
	// BuildSystemPrompt returns the final system prompt for a turn. The
	// `base` argument is the engine's standard system prompt (role,
	// reasoning principles, output templates). `tools` is the full list of
	// tools the engine wants the LLM to be aware of. Implementations may
	// append tool descriptions, format rules, few-shot examples, etc.
	BuildSystemPrompt(base string, tools []ToolSchema) string

	// PrepareRequest is the last hook before the request is sent to the
	// LLM API. PromptMode uses it to clear req.Tools so vLLM/OpenAI doesn't
	// reject calls (some endpoints 400 when both tools and prompt-embedded
	// tools are present). NativeFCBuilder is a no-op.
	PrepareRequest(req *Request)

	// PostProcessResponse is invoked after the LLM returns. PromptMode
	// parses JSON tool_calls out of resp.Content and populates
	// resp.ToolCalls so the Engine sees the same structure as native FC.
	// NativeFCBuilder is a no-op.
	PostProcessResponse(resp *Response) *Response

	// Mode returns the builder's identifier string for logging
	// ("native" / "prompt" / "auto"). Used in startup banners and trace
	// records so operators can tell which path is active.
	Mode() string
}

// NativeFCBuilder is the default PromptBuilder. Every method is a no-op,
// so this is a zero-impact identity transform — selecting it preserves
// pre-v1.2.0 behavior byte-for-byte.
type NativeFCBuilder struct{}

// BuildSystemPrompt returns the base prompt unchanged. Tools are delivered
// through the provider's native tools parameter, not the system prompt.
func (NativeFCBuilder) BuildSystemPrompt(base string, _ []ToolSchema) string {
	return base
}

// PrepareRequest is a no-op for native FC mode.
func (NativeFCBuilder) PrepareRequest(_ *Request) {}

// PostProcessResponse returns the response unchanged. Native FC providers
// already populate ToolCalls structurally.
func (NativeFCBuilder) PostProcessResponse(resp *Response) *Response { return resp }

// Mode returns "native".
func (NativeFCBuilder) Mode() string { return "native" }

// SelectPromptBuilder picks the appropriate PromptBuilder based on the
// ModelProfile.ToolMode value. Unknown values fall back to NativeFCBuilder
// with a warning logged at provider construction time.
//
// Valid modes:
//   "" / "native" / "auto" → NativeFCBuilder
//   "prompt"               → PromptModeBuilder (created by caller; this
//                            function only returns nil-as-sentinel here
//                            because PromptModeBuilder needs additional
//                            dependencies — caller wires it in)
//
// Returning nil for "prompt" lets the caller (openaicompat constructor)
// build the full PromptModeBuilder with its own ToolFilter and few-shot set.
func SelectPromptBuilder(toolMode string) PromptBuilder {
	switch toolMode {
	case "", "native", "auto":
		return NativeFCBuilder{}
	case "prompt":
		// PromptModeBuilder requires deeper wiring (ToolFilter, few-shot
		// store). The provider constructor is responsible for building it
		// — return nil here so caller knows to instantiate.
		return nil
	default:
		// Unknown tool_mode → defensive fallback to native, caller logs.
		return NativeFCBuilder{}
	}
}
