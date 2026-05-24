/*-------------------------------------------------------------------------
 *
 * diagtrace.go
 *	  Lightweight last-diagnosis trace for route/tool/LLM debugging.
 *
 *-------------------------------------------------------------------------
 */
package diagtrace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/config"
)

type ToolCall struct {
	Name          string
	Params        map[string]any
	Elapsed       time.Duration
	Status        string
	Error         string
	OutputSummary string
	OutputBytes   int
	OutputHash    string
	StartedAt     time.Time
}

type RoundDetail struct {
	Round   int
	Summary string
	Elapsed time.Duration
}

type Event struct {
	Input         string
	Intent        string
	Mode          string
	Skill         string
	Params        map[string]any
	Reason        string
	Confidence    float64
	LLMUsed       bool
	Model         string
	PromptSummary string
	PromptBytes   int
	PromptHash    string
	InputTokens   int
	OutputTokens  int
	Rounds        []string
	RoundDetails  []RoundDetail
	ToolCalls     []ToolCall
	Status        string
	Error         string
	StartedAt     time.Time
	EndedAt       time.Time
}

var store struct {
	sync.Mutex
	last *Event
}

func SetLast(e Event) {
	copy := cloneEvent(e)
	store.Lock()
	store.last = &copy
	store.Unlock()
	persist(copy)
}

func Last() (Event, bool) {
	store.Lock()
	if store.last != nil {
		copy := cloneEvent(*store.last)
		store.Unlock()
		return copy, true
	}
	store.Unlock()

	loaded, ok := loadLast()
	if !ok {
		return Event{}, false
	}
	copy := cloneEvent(loaded)
	store.Lock()
	store.last = &copy
	store.Unlock()
	return cloneEvent(copy), true
}

func RenderLastJSON() string {
	e, ok := Last()
	if !ok {
		return `{"error":"no diagnosis trace"}`
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(data)
}

func RenderHistoryJSON(limit int) string {
	if limit <= 0 {
		limit = 10
	}
	events := loadHistory(limit)
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(data)
}

func RenderHistory(limit int) string {
	if limit <= 0 {
		limit = 10
	}
	events := loadHistory(limit)
	if len(events) == 0 {
		return "暂无持久化诊断 trace。"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("诊断 Trace History (最近 %d 条)\n\n", len(events)))
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		idx := len(events) - i
		started := "-"
		if !e.StartedAt.IsZero() {
			started = e.StartedAt.Format("2006-01-02 15:04:05")
		}
		elapsed := "-"
		if !e.StartedAt.IsZero() && !e.EndedAt.IsZero() {
			elapsed = e.EndedAt.Sub(e.StartedAt).Round(time.Millisecond).String()
		}
		input := e.Input
		if len([]rune(input)) > 60 {
			r := []rune(input)
			input = string(r[:60]) + "..."
		}
		b.WriteString(fmt.Sprintf("%d. %s · %s · %s · %s · %s\n", idx, started, valueOr(e.Intent, "unknown"), valueOr(e.Status, "unknown"), valueOr(e.Model, "-"), elapsed))
		if input != "" {
			b.WriteString("   input: " + input + "\n")
		}
		if len(e.Rounds) > 0 {
			b.WriteString(fmt.Sprintf("   rounds: %d\n", len(e.Rounds)))
		}
		if len(e.ToolCalls) > 0 {
			b.WriteString(fmt.Sprintf("   tools: %d\n", len(e.ToolCalls)))
		}
		if e.Error != "" {
			b.WriteString("   error: " + e.Error + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func RenderLast() string {
	e, ok := Last()
	if !ok {
		return "暂无诊断 trace。"
	}
	var b strings.Builder
	b.WriteString("诊断 Trace Last\n\n")
	writeKV(&b, "input", e.Input)
	writeKV(&b, "intent", e.Intent)
	writeKV(&b, "mode", e.Mode)
	writeKV(&b, "route_kind", routeKind(e))
	if e.Skill != "" {
		writeKV(&b, "skill", e.Skill)
	}
	if len(e.Params) > 0 {
		writeKV(&b, "params", formatMap(e.Params))
	}
	writeKV(&b, "reason", e.Reason)
	if e.Confidence > 0 {
		writeKV(&b, "confidence", fmt.Sprintf("%.2f", e.Confidence))
	}
	writeKV(&b, "llm", fmt.Sprintf("%v", e.LLMUsed))
	if e.Model != "" {
		writeKV(&b, "model", e.Model)
	}
	if e.PromptSummary != "" {
		writeKV(&b, "prompt", e.PromptSummary)
	}
	if e.PromptBytes > 0 {
		writeKV(&b, "prompt_bytes", fmt.Sprintf("%d", e.PromptBytes))
	}
	if e.PromptHash != "" {
		writeKV(&b, "prompt_hash", e.PromptHash)
	}
	if e.InputTokens > 0 || e.OutputTokens > 0 {
		writeKV(&b, "tokens", fmt.Sprintf("input=%d output=%d total=%d", e.InputTokens, e.OutputTokens, e.InputTokens+e.OutputTokens))
	}
	if !e.StartedAt.IsZero() && !e.EndedAt.IsZero() {
		writeKV(&b, "elapsed", e.EndedAt.Sub(e.StartedAt).Round(time.Millisecond).String())
	}
	if e.Status != "" {
		writeKV(&b, "status", e.Status)
	}
	if e.Error != "" {
		writeKV(&b, "error", e.Error)
	}
	if len(e.ToolCalls) > 0 {
		writeKV(&b, "tool_call_count", fmt.Sprintf("%d", len(e.ToolCalls)))
		b.WriteString("\ntool_calls:\n")
		for _, tc := range e.ToolCalls {
			line := fmt.Sprintf("  - %s", tc.Name)
			if len(tc.Params) > 0 {
				line += " " + formatMap(tc.Params)
			}
			if tc.Elapsed > 0 {
				line += " · " + tc.Elapsed.Round(time.Millisecond).String()
			}
			if tc.Status != "" {
				line += " · " + tc.Status
			}
			if tc.OutputBytes > 0 {
				line += fmt.Sprintf(" · output=%dB", tc.OutputBytes)
			}
			if tc.OutputHash != "" {
				line += " · sha256=" + tc.OutputHash
			}
			if tc.Error != "" {
				line += " · " + tc.Error
			}
			b.WriteString(line + "\n")
			if tc.OutputSummary != "" {
				b.WriteString("    output_summary: " + tc.OutputSummary + "\n")
			}
		}
	}
	if len(e.Rounds) > 0 {
		writeKV(&b, "round_count", fmt.Sprintf("%d", len(e.Rounds)))
		b.WriteString("\nrounds:\n")
		if len(e.RoundDetails) == len(e.Rounds) {
			for i, r := range e.Rounds {
				detail := e.RoundDetails[i]
				line := "  - " + r
				if detail.Elapsed > 0 {
					line += " · " + detail.Elapsed.Round(time.Millisecond).String()
				}
				b.WriteString(line + "\n")
			}
		} else {
			for _, r := range e.Rounds {
				b.WriteString("  - " + r + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func SummarizeText(s string, limit int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if limit <= 0 {
		limit = 160
	}
	if len([]rune(s)) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + "..."
}

func HashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}

func cloneEvent(e Event) Event {
	copy := e
	copy.Params = cloneMap(e.Params)
	copy.ToolCalls = append([]ToolCall(nil), e.ToolCalls...)
	copy.Rounds = append([]string(nil), e.Rounds...)
	copy.RoundDetails = append([]RoundDetail(nil), e.RoundDetails...)
	return copy
}

func persist(e Event) {
	dir := storeDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "last.json"), append(data, '\n'), 0o600)
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "history.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func loadHistory(limit int) []Event {
	data, err := os.ReadFile(filepath.Join(storeDir(), "history.jsonl"))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return nil
	}
	start := 0
	if limit > 0 && len(lines) > limit {
		start = len(lines) - limit
	}
	out := make([]Event, 0, len(lines)-start)
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			out = append(out, e)
		}
	}
	return out
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func loadLast() (Event, bool) {
	data, err := os.ReadFile(filepath.Join(storeDir(), "last.json"))
	if err != nil {
		return Event{}, false
	}
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return Event{}, false
	}
	return e, true
}

func storeDir() string {
	if dir := strings.TrimSpace(os.Getenv("OPENDB_DIAGTRACE_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(config.DefaultOpenDBDir(), "diagtrace")
}

func routeKind(e Event) string {
	switch e.Mode {
	case "direct_skill":
		if e.Skill != "" {
			return "direct skill (no free-form LLM)"
		}
		return "direct skill"
	case "evidence_then_llm":
		if e.Skill != "" {
			return "evidence skill + managed synthesis"
		}
		return "evidence + managed synthesis"
	case "llm":
		return "LLM planning / tool-use"
	default:
		if e.Skill != "" {
			return "skill"
		}
		if e.LLMUsed {
			return "LLM"
		}
		return "unknown"
	}
}

func writeKV(b *strings.Builder, k, v string) {
	if strings.TrimSpace(v) == "" {
		return
	}
	b.WriteString(k)
	b.WriteString(": ")
	b.WriteString(v)
	b.WriteByte('\n')
}

func formatMap(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
