/*-------------------------------------------------------------------------
 *
 * json_parser.go
 *	  v1.2.0 JSON tool_call parser for PromptModeBuilder. LLMs in prompt
 *	  mode are asked to output one of:
 *
 *	    Format A (call tools):
 *	      ```json
 *	      {"tool_calls": [{"name": "X", "args": {...}}]}
 *	      ```
 *	    Format B (final answer):
 *	      <plain markdown>
 *
 *	  Real LLM output is messier than the spec. This parser layers
 *	  tolerances so the engine sees structured ToolCalls anyway:
 *
 *	    1. Extract: code-fence (```json or ```), then raw {...} substring
 *	    2. Fix: single quotes → double, trailing commas, line/block comments
 *	    3. Validate: must have tool_calls array, each with name field
 *	    4. Correct: tool name fuzzy match (Levenshtein ≤ 1) against
 *	       provided tool list (heath → health)
 *	    5. Fallback: parse failure → treat as Format B (return as content)
 *
 *	  All correction steps are best-effort; the parser only reports a hard
 *	  error when the input cannot be reconciled into any tool call AND
 *	  the caller asked strict mode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/json_parser.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// JSONToolCallParser extracts ToolCall entries from a free-text LLM
// response. It is stateless and safe for concurrent use after construction.
type JSONToolCallParser struct {
	// known is the optional allowlist of tool names used for fuzzy
	// correction. Empty = no correction (parser passes the LLM-provided
	// name through verbatim).
	known []string
}

// NewJSONToolCallParser returns a parser configured to fuzzy-correct tool
// names against the provided allowlist. Pass nil/empty to disable correction.
func NewJSONToolCallParser(knownTools []string) *JSONToolCallParser {
	return &JSONToolCallParser{known: knownTools}
}

// ParseResult captures the parse outcome. Exactly one of Calls / FallbackContent
// is meaningful at a time.
type ParseResult struct {
	// Calls is the extracted tool_calls list (empty if Format B).
	Calls []ToolCall
	// FallbackContent is the original LLM content when no valid JSON was
	// found (Format B fallthrough). Empty if Calls is populated.
	FallbackContent string
	// Corrected is the count of fuzzy-name corrections applied (for
	// telemetry / debugging).
	Corrected int
	// ParseError, if non-nil, indicates JSON syntax was present but
	// malformed beyond repair. Caller may use this to trigger retry
	// feedback in the next turn.
	ParseError error
}

// Parse runs the full extraction pipeline on a single LLM response string.
// Always returns a non-nil result; callers should check Calls first, then
// fall back to FallbackContent.
// rawToolCall 同时承载两种 LLM 输出形态（simple + OpenAI native FC nested）。
type rawToolCall struct {
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
	// 某些模型把 arguments 放顶层 alongside name
	Arguments json.RawMessage `json:"arguments"`
}

func (p *JSONToolCallParser) Parse(content string) ParseResult {
	if strings.TrimSpace(content) == "" {
		return ParseResult{FallbackContent: content}
	}

	// Step 1: locate the most-likely JSON payload.
	payload, ok := extractJSONPayload(content)
	if !ok {
		// No JSON detected at all → Format B.
		return ParseResult{FallbackContent: content}
	}

	// Step 2: apply tolerant fixes (quotes, trailing commas, comments).
	fixed := tolerantFix(payload)

	// Step 3: unmarshal.
	// v1.2.6: 同时支持两种 LLM 输出格式
	//   1. simple form (PromptToolAdapter 约定): {"name":"X","args":{...}}
	//   2. OpenAI native FC form: {"function":{"name":"X","arguments":{...}}}
	// Qwen3 / GLM 等模型即使在 prompt mode 也常常输出 OpenAI 嵌套形式，
	// 不识别会让 engine 把整段 JSON 当成 final content 输出给用户。
	var raw struct {
		ToolCalls []rawToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(fixed), &raw); err != nil {
		// Try one more recovery: maybe LLM gave a single tool object
		// without the wrapping {tool_calls: [...]} envelope.
		if single, recovered := tryUnwrapSingleCall(fixed); recovered {
			raw.ToolCalls = []rawToolCall{{Name: single.Name, Args: single.Args}}
		} else {
			// Hard parse failure — return as fallback with error for retry.
			return ParseResult{
				FallbackContent: content,
				ParseError:      fmt.Errorf("json parse: %w; payload: %s", err, truncForError(fixed)),
			}
		}
	}
	// Even if unmarshal succeeded, the envelope might be empty — LLM
	// could have emitted a bare {"name": "...", "args": ...} object.
	// Try the single-call unwrap as a salvage path.
	if len(raw.ToolCalls) == 0 {
		if single, recovered := tryUnwrapSingleCall(fixed); recovered {
			raw.ToolCalls = []rawToolCall{{Name: single.Name, Args: single.Args}}
		} else {
			return ParseResult{FallbackContent: content}
		}
	}

	// Step 4: validate + fuzzy-correct each tool call.
	out := make([]ToolCall, 0, len(raw.ToolCalls))
	corrected := 0
	for i, tc := range raw.ToolCalls {
		// 优先取 simple form 字段，fallback 到 OpenAI 嵌套形式
		name := strings.TrimSpace(tc.Name)
		if name == "" {
			name = strings.TrimSpace(tc.Function.Name)
		}
		if name == "" {
			continue // skip malformed entries
		}
		if p.known != nil {
			if fixed, changed := p.correctName(name); changed {
				name = fixed
				corrected++
			}
		}
		args := string(tc.Args)
		if args == "" || args == "null" {
			args = string(tc.Function.Arguments)
		}
		if args == "" || args == "null" {
			args = string(tc.Arguments)
		}
		if args == "" || args == "null" {
			args = "{}"
		}
		out = append(out, ToolCall{
			ID:        fmt.Sprintf("prompt_%d", i),
			Name:      name,
			Arguments: args,
		})
	}
	if len(out) == 0 {
		return ParseResult{FallbackContent: content}
	}
	return ParseResult{Calls: out, Corrected: corrected}
}

// ───────────────────────────────────────────────────────────────────────
// Extraction
// ───────────────────────────────────────────────────────────────────────

var (
	// jsonFenceRE matches ```json ... ``` or ``` ... ``` blocks.
	// (?s) enables dot-matches-newline so multiline JSON is captured.
	jsonFenceRE = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*\\n?([\\s\\S]*?)```")
)

// extractJSONPayload tries (in order):
//
//	1. ```json ... ``` code fence
//	2. ``` ... ``` plain fence
//	3. Raw {} substring (brace-balanced, ignoring strings)
//
// Returns the candidate JSON payload string and whether anything was found.
func extractJSONPayload(content string) (string, bool) {
	// Code fences first — they're the explicit signal.
	if matches := jsonFenceRE.FindStringSubmatch(content); len(matches) > 1 {
		inner := strings.TrimSpace(matches[1])
		if inner != "" {
			return inner, true
		}
	}
	// Fall back to raw brace scan.
	if s, ok := extractBracedJSON(content); ok {
		return s, true
	}
	return "", false
}

// extractBracedJSON finds the first balanced { ... } substring, respecting
// string literals (so braces inside "..." don't confuse the counter).
// Returns the substring including the outer braces.
func extractBracedJSON(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// ───────────────────────────────────────────────────────────────────────
// Tolerant fixes
// ───────────────────────────────────────────────────────────────────────

var (
	// trailingCommaRE matches a comma immediately before } or ] (with
	// optional whitespace between).
	trailingCommaRE = regexp.MustCompile(`,(\s*[}\]])`)
	// lineCommentRE matches // ... to end-of-line.
	lineCommentRE = regexp.MustCompile(`(?m)//[^\n]*$`)
	// blockCommentRE matches /* ... */ spanning lines.
	blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// tolerantFix applies a sequence of best-effort fixes that handle common
// LLM JSON output errors. Each step is independent — failure of one leaves
// the input unchanged for that step.
func tolerantFix(s string) string {
	// 1. Strip comments first (some LLMs emit "// note: ..." inside JSON).
	s = blockCommentRE.ReplaceAllString(s, "")
	s = lineCommentRE.ReplaceAllString(s, "")
	// 2. Remove trailing commas before closing brackets.
	for {
		newS := trailingCommaRE.ReplaceAllString(s, "$1")
		if newS == s {
			break
		}
		s = newS
	}
	// 3. Single-quote replacement is risky (could mangle strings with
	// embedded apostrophes). Only attempt if no double quotes are present
	// at all — that's a strong signal the LLM used single quotes for
	// the whole thing.
	if !strings.Contains(s, "\"") && strings.Contains(s, "'") {
		s = strings.ReplaceAll(s, "'", "\"")
	}
	return strings.TrimSpace(s)
}

// tryUnwrapSingleCall handles the case where the LLM emitted a bare tool
// call instead of the wrapping {tool_calls: [...]} envelope, e.g.:
//
//	{"name": "health", "args": {}}
//
// Returns the call struct + true if recovery succeeded.
func tryUnwrapSingleCall(s string) (struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}, bool) {
	var single struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(s), &single); err != nil {
		return single, false
	}
	if single.Name == "" {
		return single, false
	}
	return single, true
}

// ───────────────────────────────────────────────────────────────────────
// Tool name fuzzy correction
// ───────────────────────────────────────────────────────────────────────

// correctName attempts to map an LLM-provided tool name to one of the
// known tools using:
//
//	1. Exact match (case-sensitive)
//	2. Case-insensitive match
//	3. Levenshtein distance ≤ 1
//
// Returns the corrected name and whether a correction was applied.
func (p *JSONToolCallParser) correctName(provided string) (string, bool) {
	if provided == "" || len(p.known) == 0 {
		return provided, false
	}
	// Exact match
	for _, k := range p.known {
		if k == provided {
			return provided, false
		}
	}
	// Case-insensitive match
	lower := strings.ToLower(provided)
	for _, k := range p.known {
		if strings.ToLower(k) == lower {
			return k, k != provided
		}
	}
	// Levenshtein distance ≤ 1
	for _, k := range p.known {
		if levenshtein(strings.ToLower(k), lower) <= 1 {
			return k, true
		}
	}
	return provided, false // no match — leave untouched, caller may reject
}

// levenshtein returns the edit distance between two strings. Uses the
// classic dynamic programming algorithm; for the tool-name use case the
// strings are < 30 chars so O(n*m) is fine.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// truncForError keeps error messages short — JSON payloads can be 8KB+.
func truncForError(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
