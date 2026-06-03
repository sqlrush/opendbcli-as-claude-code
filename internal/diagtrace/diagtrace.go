/*-------------------------------------------------------------------------
 *
 * diagtrace.go
 *	  Lightweight last-diagnosis trace for route/tool/LLM debugging.
 *
 *-------------------------------------------------------------------------
 */
package diagtrace

import (
	"context"
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

type StepToken struct {
	InputTokens            int
	OutputTokens           int
	ThinkingTokens         int
	CacheCreationTokens    int
	CacheReadTokens        int
	CacheMissTokens        int
	TotalTokens            int
	EstimatedInputTokens   int
	EstimatedOutputTokens  int
	EstimatedContextTokens int
	EstimatedBeforeTokens  int
	EstimatedAfterTokens   int
	Source                 string
}

type Step struct {
	ID        string
	ParentID  string
	Name      string
	Phase     string
	Status    string
	Doing     string
	Detail    string
	StartedAt time.Time
	EndedAt   time.Time
	Elapsed   time.Duration
	Token     StepToken
	Metadata  map[string]any
}

type Event struct {
	TraceID       string
	Input         string
	Intent        string
	Mode          string
	Strategy      string
	Skill         string
	Params        map[string]any
	Reason        string
	Confidence    float64
	LLMUsed       bool
	Model         string
	ToolMode      string
	PromptSummary string
	PromptBytes   int
	PromptHash    string
	InputTokens   int
	OutputTokens  int
	TimeoutBudget time.Duration
	LastStep      string
	Steps         []Step
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

var probeStore struct {
	sync.Mutex
	modelTest any
	toolTest  any
	routeTest any
}

type stepContextKey struct{}

type StepContext struct {
	Event    *Event
	ParentID string
}

func WithStepContext(ctx context.Context, e *Event, parentID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stepContextKey{}, StepContext{Event: e, ParentID: parentID})
}

func StepContextFrom(ctx context.Context) (StepContext, bool) {
	if ctx == nil {
		return StepContext{}, false
	}
	sc, ok := ctx.Value(stepContextKey{}).(StepContext)
	return sc, ok && sc.Event != nil
}

func SetLast(e Event) {
	copy := setCurrent(e, true)
	appendHistory(copy)
}

func SetCurrent(e Event) {
	setCurrent(e, false)
}

func setCurrent(e Event, persist bool) Event {
	copy := cloneEvent(e)
	store.Lock()
	store.last = &copy
	store.Unlock()
	if persist {
		persistLast(copy)
	}
	return copy
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

func History(limit int) []Event {
	if limit <= 0 {
		limit = 10
	}
	events := loadHistory(limit)
	out := make([]Event, 0, len(events))
	for _, e := range events {
		out = append(out, cloneEvent(e))
	}
	return out
}

func SetLastModelTest(v any) { setProbe("modeltest", v) }
func SetLastToolTest(v any)  { setProbe("tooltest", v) }
func SetLastRouteTest(v any) { setProbe("routetest", v) }

func LastModelTest() any { return getProbe("modeltest") }
func LastToolTest() any  { return getProbe("tooltest") }
func LastRouteTest() any { return getProbe("routetest") }

func setProbe(kind string, v any) {
	copy := cloneProbe(v)
	probeStore.Lock()
	defer probeStore.Unlock()
	switch kind {
	case "modeltest":
		probeStore.modelTest = copy
	case "tooltest":
		probeStore.toolTest = copy
	case "routetest":
		probeStore.routeTest = copy
	}
}

func getProbe(kind string) any {
	probeStore.Lock()
	defer probeStore.Unlock()
	switch kind {
	case "modeltest":
		return cloneProbe(probeStore.modelTest)
	case "tooltest":
		return cloneProbe(probeStore.toolTest)
	case "routetest":
		return cloneProbe(probeStore.routeTest)
	default:
		return nil
	}
}

func cloneProbe(v any) any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

func RenderHistoryDetailJSON(index int) string {
	e, ok := HistoryEvent(index)
	if !ok {
		return fmt.Sprintf(`{"error":"no diagnosis trace at index %d"}`, index)
	}
	data, err := json.MarshalIndent(e, "", "  ")
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
	b.WriteString(fmt.Sprintf("诊断 Trace History / 诊断 Trace List (最近 %d 条)\n\n", len(events)))
	b.WriteString("编号  开始时间             状态     耗时      Token  问题\n")
	b.WriteString("----  -------------------  -------  --------  -----  ----\n")
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
		b.WriteString(fmt.Sprintf("%-4d  %-19s  %-7s  %-8s  %-5d  %s\n", idx, started, valueOr(e.Status, "unknown"), elapsed, eventTotalTokens(e), valueOr(input, "-")))
		if e.Intent != "" {
			b.WriteString("      intent: " + e.Intent + "\n")
		}
		if len(e.ToolCalls) > 0 {
			b.WriteString(fmt.Sprintf("      tools: %d\n", len(e.ToolCalls)))
		}
		if e.Error != "" {
			b.WriteString("      error: " + e.Error + "\n")
		}
	}
	b.WriteString("\n查看详情: /diagtrace <编号>，例如 /diagtrace 1")
	return strings.TrimRight(b.String(), "\n")
}

func RenderHistoryDetail(index int) string {
	e, ok := HistoryEvent(index)
	if !ok {
		return fmt.Sprintf("没有找到编号 %d 的诊断 trace。先用 /diagtrace list 查看可用编号。", index)
	}
	return RenderEvent(fmt.Sprintf("诊断 Trace #%d", index), e)
}

func HistoryEvent(index int) (Event, bool) {
	if index <= 0 {
		return Event{}, false
	}
	events := loadHistory(100)
	if len(events) == 0 || index > len(events) {
		return Event{}, false
	}
	return cloneEvent(events[len(events)-index]), true
}

func RenderLast() string {
	e, ok := Last()
	if !ok {
		return "暂无诊断 trace。"
	}
	return RenderEvent("诊断 Trace Last", e)
}

func RenderEvent(title string, e Event) string {
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	if e.TraceID != "" {
		writeKV(&b, "trace_id", e.TraceID)
	}
	writeKV(&b, "input", e.Input)
	writeKV(&b, "intent", e.Intent)
	writeKV(&b, "mode", e.Mode)
	writeKV(&b, "strategy", e.Strategy)
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
	if e.ToolMode != "" {
		writeKV(&b, "tool_mode", e.ToolMode)
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
		writeKV(&b, "started", formatTime(e.StartedAt))
		writeKV(&b, "ended", formatTime(e.EndedAt))
		writeKV(&b, "elapsed", e.EndedAt.Sub(e.StartedAt).Round(time.Millisecond).String())
	}
	if e.TimeoutBudget > 0 {
		writeKV(&b, "timeout", e.TimeoutBudget.Round(time.Millisecond).String())
	}
	writeKV(&b, "last_step", e.LastStep)
	if e.Status != "" {
		writeKV(&b, "status", e.Status)
	}
	if e.Error != "" {
		writeKV(&b, "error", e.Error)
	}
	if len(e.Steps) > 0 {
		renderTokenSummary(&b, e)
		renderSteps(&b, e.Steps)
		renderSlowSteps(&b, e.Steps)
		renderTokenRanking(&b, e.Steps)
		return strings.TrimRight(b.String(), "\n")
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
	copy.Steps = cloneSteps(e.Steps)
	return copy
}

func persistLast(e Event) {
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
}

func appendHistory(e Event) {
	dir := storeDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
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

func RenderTraceID(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d", t.UnixNano())))
	return fmt.Sprintf("diag-%s-%x", t.Format("20060102-150405"), sum[:2])
}

func StepDone(id, name, phase, status, doing, detail string, start, end time.Time) Step {
	if end.IsZero() {
		end = time.Now()
	}
	return Step{
		ID:        id,
		Name:      name,
		Phase:     phase,
		Status:    status,
		Doing:     doing,
		Detail:    detail,
		StartedAt: start,
		EndedAt:   end,
		Elapsed:   end.Sub(start),
	}
}

func (e *Event) AddStep(step Step) Step {
	step = normalizeStep(step, e.nextStepID(step.ParentID))
	e.Steps = append(e.Steps, step)
	e.LastStep = step.Name
	return step
}

func normalizeStep(step Step, fallbackID string) Step {
	if step.ID == "" {
		step.ID = fallbackID
	}
	if step.Status == "" {
		step.Status = "ok"
	}
	if !step.StartedAt.IsZero() && step.EndedAt.IsZero() {
		step.EndedAt = time.Now()
	}
	if step.Elapsed == 0 && !step.StartedAt.IsZero() && !step.EndedAt.IsZero() {
		step.Elapsed = step.EndedAt.Sub(step.StartedAt)
	}
	return step
}

func (e *Event) AddCompletedStep(id, name, phase, status, doing, detail string, start, end time.Time) Step {
	return e.AddStep(StepDone(id, name, phase, status, doing, detail, start, end))
}

func (e *Event) UpdateStep(step Step) bool {
	if step.ID == "" {
		return false
	}
	step = normalizeStep(step, step.ID)
	for i := range e.Steps {
		if e.Steps[i].ID == step.ID {
			e.Steps[i] = step
			e.LastStep = step.Name
			return true
		}
	}
	return false
}

func (e *Event) nextStepID(parentID string) string {
	if parentID != "" {
		n := 1
		for _, step := range e.Steps {
			if step.ParentID == parentID {
				n++
			}
		}
		return fmt.Sprintf("%s.%d", parentID, n)
	}
	n := 1
	for _, step := range e.Steps {
		if step.ParentID == "" {
			n++
		}
	}
	return fmt.Sprintf("%02d", n)
}

func RecordStep(e *Event, step Step) Step {
	if e == nil {
		return step
	}
	step = e.AddStep(step)
	SetCurrent(*e)
	return step
}

func RecordContextStep(ctx context.Context, step Step) Step {
	sc, ok := StepContextFrom(ctx)
	if !ok {
		return step
	}
	if step.ParentID == "" {
		step.ParentID = sc.ParentID
	}
	return RecordStep(sc.Event, step)
}

func UpdateContextStep(ctx context.Context, step Step) bool {
	sc, ok := StepContextFrom(ctx)
	if !ok {
		return false
	}
	return UpdateStep(sc.Event, step)
}

func UpdateStep(e *Event, step Step) bool {
	if e == nil {
		return false
	}
	if !e.UpdateStep(step) {
		return false
	}
	SetCurrent(*e)
	return true
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05.000 -07:00")
}

func renderTokenSummary(b *strings.Builder, e Event) {
	input, output := e.InputTokens, e.OutputTokens
	stepInput, stepOutput, thinking, cacheCreate, cacheRead, cacheMiss, estimated := 0, 0, 0, 0, 0, 0, 0
	source := "provider_usage"
	parents, actualByID := tokenParentIndex(e.Steps)
	for _, s := range e.Steps {
		if !hasActualTokenAncestor(s, parents, actualByID) {
			stepInput += s.Token.InputTokens
			stepOutput += s.Token.OutputTokens
			thinking += s.Token.ThinkingTokens
			cacheCreate += s.Token.CacheCreationTokens
			cacheRead += s.Token.CacheReadTokens
			cacheMiss += s.Token.CacheMissTokens
		}
		estimated += s.Token.EstimatedContextTokens + s.Token.EstimatedInputTokens + s.Token.EstimatedOutputTokens
	}
	if input == 0 {
		input = stepInput
	}
	if output == 0 {
		output = stepOutput
	}
	if input == 0 && output == 0 {
		source = "local_estimate"
	} else if estimated > 0 {
		source = "provider_usage + local_estimate"
	}
	b.WriteString("\ntoken_summary\n")
	b.WriteString(fmt.Sprintf("  input_tokens     : %d\n", input))
	b.WriteString(fmt.Sprintf("  output_tokens    : %d\n", output))
	if thinking > 0 {
		b.WriteString(fmt.Sprintf("  thinking_tokens  : %d\n", thinking))
	}
	if cacheCreate > 0 || cacheRead > 0 || cacheMiss > 0 {
		b.WriteString(fmt.Sprintf("  cache_tokens     : create=%d read=%d miss=%d\n", cacheCreate, cacheRead, cacheMiss))
	}
	b.WriteString(fmt.Sprintf("  total_tokens     : %d\n", input+output+thinking+cacheCreate+cacheRead))
	if estimated > 0 {
		b.WriteString(fmt.Sprintf("  estimated_tokens : %d\n", estimated))
	}
	b.WriteString(fmt.Sprintf("  token_source     : %s\n", source))
}

func eventTotalTokens(e Event) int {
	actual := e.InputTokens + e.OutputTokens
	stepActual := 0
	stepEstimated := 0
	parents, actualByID := tokenParentIndex(e.Steps)
	for _, s := range e.Steps {
		if !hasActualTokenAncestor(s, parents, actualByID) {
			stepActual += actualTokenTotal(s.Token)
		}
		stepEstimated += estimatedTokenTotal(s.Token)
	}
	if actual > 0 {
		return actual
	}
	if stepActual > 0 {
		return stepActual
	}
	return stepEstimated
}

func renderSteps(b *strings.Builder, steps []Step) {
	b.WriteString("\n步骤明细\n\n")
	for _, step := range steps {
		indent := ""
		if step.ParentID != "" {
			indent = "  "
		}
		b.WriteString(fmt.Sprintf("%s[%s] %s\n", indent, step.ID, step.Name))
		if step.Phase != "" {
			b.WriteString(fmt.Sprintf("%s  phase  : %s\n", indent, step.Phase))
		}
		b.WriteString(fmt.Sprintf("%s  status : %s\n", indent, valueOr(step.Status, "-")))
		b.WriteString(fmt.Sprintf("%s  start  : %s\n", indent, formatTime(step.StartedAt)))
		b.WriteString(fmt.Sprintf("%s  end    : %s\n", indent, formatTime(step.EndedAt)))
		b.WriteString(fmt.Sprintf("%s  cost   : %s\n", indent, formatDuration(step.Elapsed)))
		b.WriteString(fmt.Sprintf("%s  token  : %s\n", indent, formatStepToken(step.Token)))
		b.WriteString(fmt.Sprintf("%s  doing  : %s\n", indent, valueOr(step.Doing, "-")))
		if step.Detail != "" {
			b.WriteString(fmt.Sprintf("%s  detail : %s\n", indent, step.Detail))
		}
		b.WriteString("\n")
	}
}

func renderSlowSteps(b *strings.Builder, steps []Step) {
	ranked := append([]Step(nil), steps...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Elapsed > ranked[j].Elapsed })
	if len(ranked) > 5 {
		ranked = ranked[:5]
	}
	b.WriteString("慢步骤排行\n")
	for i, step := range ranked {
		if step.Elapsed <= 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("%d. [%s] %s  %s  %s\n", i+1, step.ID, step.Name, formatDuration(step.Elapsed), valueOr(step.Status, "-")))
	}
}

func renderTokenRanking(b *strings.Builder, steps []Step) {
	type item struct {
		step  Step
		total int
	}
	var items []item
	parents, actualByID := tokenParentIndex(steps)
	for _, step := range steps {
		total := estimatedTokenTotal(step.Token)
		if !hasActualTokenAncestor(step, parents, actualByID) {
			total += actualTokenTotal(step.Token)
		}
		if total > 0 {
			items = append(items, item{step: step, total: total})
		}
	}
	if len(items) == 0 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].total > items[j].total })
	if len(items) > 5 {
		items = items[:5]
	}
	b.WriteString("\nToken 贡献排行\n")
	for i, item := range items {
		b.WriteString(fmt.Sprintf("%d. [%s] %s  %s\n", i+1, item.step.ID, item.step.Name, formatStepToken(item.step.Token)))
	}
}

func tokenParentIndex(steps []Step) (map[string]string, map[string]bool) {
	parents := make(map[string]string, len(steps))
	actualByID := make(map[string]bool, len(steps))
	for _, step := range steps {
		if step.ID == "" {
			continue
		}
		parents[step.ID] = step.ParentID
		actualByID[step.ID] = actualTokenTotal(step.Token) > 0
	}
	return parents, actualByID
}

func hasActualTokenAncestor(step Step, parents map[string]string, actualByID map[string]bool) bool {
	for parentID := step.ParentID; parentID != ""; parentID = parents[parentID] {
		if actualByID[parentID] {
			return true
		}
		if _, ok := parents[parentID]; !ok {
			return false
		}
	}
	return false
}

func actualTokenTotal(t StepToken) int {
	if t.TotalTokens > 0 {
		return t.TotalTokens
	}
	return t.InputTokens + t.OutputTokens + t.ThinkingTokens + t.CacheCreationTokens + t.CacheReadTokens
}

func estimatedTokenTotal(t StepToken) int {
	return t.EstimatedContextTokens + t.EstimatedInputTokens + t.EstimatedOutputTokens + t.EstimatedAfterTokens
}

func formatStepToken(t StepToken) string {
	var parts []string
	if t.InputTokens > 0 || t.OutputTokens > 0 || t.ThinkingTokens > 0 || t.CacheCreationTokens > 0 || t.CacheReadTokens > 0 || t.TotalTokens > 0 {
		total := t.TotalTokens
		if total == 0 {
			total = t.InputTokens + t.OutputTokens + t.ThinkingTokens + t.CacheCreationTokens + t.CacheReadTokens
		}
		parts = append(parts, fmt.Sprintf("input=%d", t.InputTokens))
		parts = append(parts, fmt.Sprintf("output=%d", t.OutputTokens))
		if t.ThinkingTokens > 0 {
			parts = append(parts, fmt.Sprintf("thinking=%d", t.ThinkingTokens))
		}
		if t.CacheCreationTokens > 0 {
			parts = append(parts, fmt.Sprintf("cache_create=%d", t.CacheCreationTokens))
		}
		if t.CacheReadTokens > 0 {
			parts = append(parts, fmt.Sprintf("cache_read=%d", t.CacheReadTokens))
		}
		if t.CacheMissTokens > 0 {
			parts = append(parts, fmt.Sprintf("cache_miss=%d", t.CacheMissTokens))
		}
		parts = append(parts, fmt.Sprintf("total=%d", total))
	}
	if t.EstimatedInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("estimated_input=%d", t.EstimatedInputTokens))
	}
	if t.EstimatedOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("estimated_output=%d", t.EstimatedOutputTokens))
	}
	if t.EstimatedContextTokens > 0 {
		parts = append(parts, fmt.Sprintf("estimated_context=%d", t.EstimatedContextTokens))
	}
	if t.EstimatedBeforeTokens > 0 || t.EstimatedAfterTokens > 0 {
		parts = append(parts, fmt.Sprintf("estimated_before=%d", t.EstimatedBeforeTokens))
		parts = append(parts, fmt.Sprintf("estimated_after=%d", t.EstimatedAfterTokens))
	}
	if t.Source != "" {
		parts = append(parts, "source="+t.Source)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	return d.Round(time.Millisecond).String()
}

func cloneSteps(steps []Step) []Step {
	if len(steps) == 0 {
		return nil
	}
	out := make([]Step, len(steps))
	for i, step := range steps {
		out[i] = step
		out[i].Metadata = cloneMap(step.Metadata)
	}
	return out
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
