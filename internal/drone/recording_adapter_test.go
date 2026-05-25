/*-------------------------------------------------------------------------
 *
 * recording_adapter_test.go
 *	  Test cases for recording_adapter.go (drone package):
 *	  TestRecordingAdapter_RecordMode, TestRecordingAdapter_ReplayMode,
 *	  TestRecordingAdapter_ReplayExhaustion.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/recording_adapter_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// mockAdapter is a test double for provider.ProviderAdapter.
type mockAdapter struct {
	name       string
	caps       *provider.ProviderCapability
	chatResp   *provider.Response
	chatErr    error
	chatCalled int
	streamResp *provider.Response
}

func newMockAdapter(name string, resp *provider.Response) *mockAdapter {
	return &mockAdapter{
		name:     name,
		caps:     &provider.ProviderCapability{Name: name, MaxContextWindow: 4096},
		chatResp: resp,
	}
}

func (m *mockAdapter) Name() string                          { return m.name }
func (m *mockAdapter) Capability() *provider.ProviderCapability { return m.caps }
func (m *mockAdapter) EnhanceRequest(_ *provider.Request)    {}
func (m *mockAdapter) ParseRateLimitHeaders(_ http.Header) *provider.RateLimitInfo { return nil }

func (m *mockAdapter) Chat(_ context.Context, _ *provider.Request) (*provider.Response, error) {
	m.chatCalled++
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	return m.chatResp, nil
}

func (m *mockAdapter) ChatStream(_ context.Context, _ *provider.Request) (provider.Stream, error) {
	resp := m.streamResp
	if resp == nil {
		resp = m.chatResp
	}
	m.chatCalled++
	return newSingleResponseStream(resp), nil
}

// ── Test helpers ──

func makeRequest(userMsg string) *provider.Request {
	return &provider.Request{
		Messages: []provider.Message{
			{Role: "user", Content: userMsg},
		},
	}
}

func makeResponse(content string) *provider.Response {
	return &provider.Response{
		Content:    content,
		StopReason: "stop",
		Usage:      provider.Usage{InputTokens: 10, OutputTokens: 20},
	}
}

// ── Tests ──

func TestRecordingAdapter_RecordMode(t *testing.T) {
	dir := t.TempDir()

	hooks, err := NewLLMHooks(LLMHookRecord, dir, "")
	if err != nil {
		t.Fatalf("create hooks: %v", err)
	}
	defer hooks.Close()

	inner := newMockAdapter("test-llm", makeResponse("diagnosis: tablespace full"))
	adapter := NewRecordingAdapter(inner, hooks)

	req := makeRequest("analyze temp_usage anomaly")
	resp, err := adapter.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "diagnosis: tablespace full" {
		t.Errorf("content = %q, want %q", resp.Content, "diagnosis: tablespace full")
	}
	if inner.chatCalled != 1 {
		t.Errorf("inner called %d times, want 1", inner.chatCalled)
	}

	// Verify JSONL was written.
	hooks.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "llm_session_*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no JSONL file written")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read JSONL: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("JSONL file is empty")
	}
	content := string(data)
	if !contains(content, "tablespace full") {
		t.Errorf("JSONL does not contain response content:\n%s", content)
	}
	if !contains(content, "engine-v2") {
		t.Errorf("JSONL does not contain model name:\n%s", content)
	}
}

func TestRecordingAdapter_ReplayMode(t *testing.T) {
	// Step 1: Record a session.
	dir := t.TempDir()
	recordHooks, err := NewLLMHooks(LLMHookRecord, dir, "")
	if err != nil {
		t.Fatalf("create record hooks: %v", err)
	}

	inner := newMockAdapter("test-llm", makeResponse("replayed answer"))
	recordAdapter := NewRecordingAdapter(inner, recordHooks)

	_, err = recordAdapter.Chat(context.Background(), makeRequest("question 1"))
	if err != nil {
		t.Fatalf("record Chat: %v", err)
	}
	recordHooks.Close()

	// Step 2: Replay the session without calling inner.
	files, _ := filepath.Glob(filepath.Join(dir, "llm_session_*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no recording file found")
	}

	replayInner := newMockAdapter("test-llm", makeResponse("should not be called"))
	replayHooks, err := NewLLMHooks(LLMHookReplay, "", files[0])
	if err != nil {
		t.Fatalf("create replay hooks: %v", err)
	}

	replayAdapter := NewRecordingAdapter(replayInner, replayHooks)

	resp, err := replayAdapter.Chat(context.Background(), makeRequest("question 1"))
	if err != nil {
		t.Fatalf("replay Chat: %v", err)
	}
	if resp.Content != "replayed answer" {
		t.Errorf("content = %q, want %q", resp.Content, "replayed answer")
	}
	if replayInner.chatCalled != 0 {
		t.Errorf("inner was called %d times during replay, want 0", replayInner.chatCalled)
	}
}

func TestRecordingAdapter_ReplayExhaustion(t *testing.T) {
	// Record one interaction.
	dir := t.TempDir()
	recordHooks, err := NewLLMHooks(LLMHookRecord, dir, "")
	if err != nil {
		t.Fatalf("create record hooks: %v", err)
	}

	inner := newMockAdapter("test-llm", makeResponse("recorded once"))
	recordAdapter := NewRecordingAdapter(inner, recordHooks)

	_, err = recordAdapter.Chat(context.Background(), makeRequest("q1"))
	if err != nil {
		t.Fatalf("record Chat: %v", err)
	}
	recordHooks.Close()

	// Replay: first call uses recording, second falls through.
	files, _ := filepath.Glob(filepath.Join(dir, "llm_session_*.jsonl"))
	replayHooks, err := NewLLMHooks(LLMHookReplay, "", files[0])
	if err != nil {
		t.Fatalf("create replay hooks: %v", err)
	}

	fallbackResp := makeResponse("fallback from inner")
	replayInner := newMockAdapter("test-llm", fallbackResp)
	replayAdapter := NewRecordingAdapter(replayInner, replayHooks)

	// First call: from recording.
	resp1, err := replayAdapter.Chat(context.Background(), makeRequest("q1"))
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	if resp1.Content != "recorded once" {
		t.Errorf("first content = %q, want %q", resp1.Content, "recorded once")
	}

	// Second call: recording exhausted, falls through to inner.
	resp2, err := replayAdapter.Chat(context.Background(), makeRequest("q2"))
	if err != nil {
		t.Fatalf("exhausted replay: %v", err)
	}
	if resp2.Content != "fallback from inner" {
		t.Errorf("second content = %q, want %q", resp2.Content, "fallback from inner")
	}
	if replayInner.chatCalled != 1 {
		t.Errorf("inner called %d times, want 1 (fallback only)", replayInner.chatCalled)
	}
}

func TestRecordingAdapter_Delegation(t *testing.T) {
	hooks, err := NewLLMHooks(LLMHookRecord, t.TempDir(), "")
	if err != nil {
		t.Fatalf("create hooks: %v", err)
	}
	defer hooks.Close()

	inner := newMockAdapter("ollama-test", makeResponse("ok"))
	inner.caps = &provider.ProviderCapability{
		Name:             "ollama-test",
		MaxContextWindow: 32768,
		MaxOutputTokens:  4096,
	}
	adapter := NewRecordingAdapter(inner, hooks)

	// Name delegation.
	if adapter.Name() != "ollama-test" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "ollama-test")
	}

	// Capability delegation.
	caps := adapter.Capability()
	if caps.Name != "ollama-test" {
		t.Errorf("Capability().Name = %q, want %q", caps.Name, "ollama-test")
	}
	if caps.MaxContextWindow != 32768 {
		t.Errorf("MaxContextWindow = %d, want %d", caps.MaxContextWindow, 32768)
	}

	// ParseRateLimitHeaders delegation (returns nil for mock).
	if adapter.ParseRateLimitHeaders(http.Header{}) != nil {
		t.Error("ParseRateLimitHeaders should return nil for mock adapter")
	}
}

func TestRecordingAdapter_ChatStreamReplay(t *testing.T) {
	// Record a session.
	dir := t.TempDir()
	recordHooks, err := NewLLMHooks(LLMHookRecord, dir, "")
	if err != nil {
		t.Fatalf("create hooks: %v", err)
	}

	inner := newMockAdapter("test-llm", makeResponse("streamed content"))
	recordAdapter := NewRecordingAdapter(inner, recordHooks)

	_, err = recordAdapter.Chat(context.Background(), makeRequest("stream test"))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	recordHooks.Close()

	// Replay via ChatStream.
	files, _ := filepath.Glob(filepath.Join(dir, "llm_session_*.jsonl"))
	replayHooks, err := NewLLMHooks(LLMHookReplay, "", files[0])
	if err != nil {
		t.Fatalf("replay hooks: %v", err)
	}

	replayInner := newMockAdapter("test-llm", makeResponse("should not be called"))
	replayAdapter := NewRecordingAdapter(replayInner, replayHooks)

	stream, err := replayAdapter.ChatStream(context.Background(), makeRequest("stream test"))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	evt, err := stream.Next()
	if err != nil {
		t.Fatalf("stream.Next: %v", err)
	}
	if evt.Content != "streamed content" {
		t.Errorf("stream content = %q, want %q", evt.Content, "streamed content")
	}
	if replayInner.chatCalled != 0 {
		t.Errorf("inner called %d times during stream replay, want 0", replayInner.chatCalled)
	}
}

func TestRecordingAdapter_ChatStreamRecord(t *testing.T) {
	dir := t.TempDir()
	hooks, err := NewLLMHooks(LLMHookRecord, dir, "")
	if err != nil {
		t.Fatalf("create hooks: %v", err)
	}
	defer hooks.Close()

	inner := newMockAdapter("test-llm", makeResponse("stream recorded"))
	adapter := NewRecordingAdapter(inner, hooks)

	stream, err := adapter.ChatStream(context.Background(), makeRequest("record stream"))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// Drain the stream.
	var content string
	for {
		evt, err := stream.Next()
		if err != nil || evt.Type == provider.StreamDone {
			break
		}
		content += evt.Content
	}
	stream.Close()

	if content != "stream recorded" {
		t.Errorf("stream content = %q, want %q", content, "stream recorded")
	}

	// Verify recording was written.
	hooks.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "llm_session_*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no JSONL file written for stream recording")
	}
	data, _ := os.ReadFile(files[0])
	if !contains(string(data), "stream recorded") {
		t.Errorf("JSONL missing stream content:\n%s", string(data))
	}
}

func TestParseAgentArgs_LLMFlags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantRecord string
		wantReplay string
	}{
		{
			name:       "record flag with space",
			args:       []string{"start", "--llm-record", "/tmp/recordings"},
			wantRecord: "/tmp/recordings",
		},
		{
			name:       "record flag with equals",
			args:       []string{"start", "--llm-record=/tmp/rec"},
			wantRecord: "/tmp/rec",
		},
		{
			name:       "replay flag with space",
			args:       []string{"start", "--llm-replay", "/tmp/session.jsonl"},
			wantReplay: "/tmp/session.jsonl",
		},
		{
			name:       "replay flag with equals",
			args:       []string{"start", "--llm-replay=/tmp/s.jsonl"},
			wantReplay: "/tmp/s.jsonl",
		},
		{
			name:       "both flags together",
			args:       []string{"start", "--llm-record", "/rec", "--llm-replay", "/play.jsonl"},
			wantRecord: "/rec",
			wantReplay: "/play.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAgentArgs(tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.LLMRecord != tt.wantRecord {
				t.Errorf("LLMRecord = %q, want %q", got.LLMRecord, tt.wantRecord)
			}
			if got.LLMReplay != tt.wantReplay {
				t.Errorf("LLMReplay = %q, want %q", got.LLMReplay, tt.wantReplay)
			}
		})
	}
}

func TestRecordingAdapter_ProviderInterface(t *testing.T) {
	// Verify RecordingAdapter satisfies ProviderAdapter at compile time.
	hooks, err := NewLLMHooks(LLMHookRecord, t.TempDir(), "")
	if err != nil {
		t.Fatalf("create hooks: %v", err)
	}
	defer hooks.Close()

	inner := newMockAdapter("test", makeResponse("ok"))
	var _ provider.ProviderAdapter = NewRecordingAdapter(inner, hooks)
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Ensure mockAdapter satisfies ProviderAdapter.
var _ provider.ProviderAdapter = (*mockAdapter)(nil)

// Suppress unused import warning.
var _ = fmt.Sprintf
