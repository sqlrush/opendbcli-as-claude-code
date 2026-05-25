/*-------------------------------------------------------------------------
 *
 * recording_adapter.go
 *	  recording_adapter.go wraps a provider.ProviderAdapter with LLM
 *	  recording/replay. Record mode: calls inner adapter, then records
 *	  request+response to JSONL. Replay mode: returns recorded responses
 *	  without calling inner; falls through on exhaustion.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/recording_adapter.go
 *
 *-------------------------------------------------------------------------
 */
// recording_adapter.go wraps a provider.ProviderAdapter with LLM recording/replay.
// Record mode: calls inner adapter, then records request+response to JSONL.
// Replay mode: returns recorded responses without calling inner; falls through on exhaustion.
package drone

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
	"github.com/sqlrush/opendb/internal/engine/provider"
)

// RecordingAdapter wraps a ProviderAdapter to intercept LLM calls for recording or replay.
type RecordingAdapter struct {
	inner provider.ProviderAdapter
	hooks *LLMHooks
}

// NewRecordingAdapter creates a RecordingAdapter wrapping the given inner adapter.
// hooks must not be nil and must be configured for record or replay mode.
func NewRecordingAdapter(inner provider.ProviderAdapter, hooks *LLMHooks) *RecordingAdapter {
	return &RecordingAdapter{inner: inner, hooks: hooks}
}

// Name delegates to the inner adapter.
func (a *RecordingAdapter) Name() string { return a.inner.Name() }

// Capability delegates to the inner adapter.
func (a *RecordingAdapter) Capability() *provider.ProviderCapability { return a.inner.Capability() }

// EnhanceRequest delegates to the inner adapter.
func (a *RecordingAdapter) EnhanceRequest(req *provider.Request) { a.inner.EnhanceRequest(req) }

// ParseRateLimitHeaders delegates to the inner adapter.
func (a *RecordingAdapter) ParseRateLimitHeaders(h http.Header) *provider.RateLimitInfo {
	return a.inner.ParseRateLimitHeaders(h)
}

// Chat performs a chat call with recording or replay interception.
func (a *RecordingAdapter) Chat(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	metric := metricFromRequest(req)

	// Replay mode: try to return a recorded response.
	if a.hooks.Mode == LLMHookReplay {
		return a.chatReplay(ctx, req, metric)
	}

	// Record mode (or passthrough): call inner, then record.
	return a.chatRecord(ctx, req, metric)
}

// chatReplay returns a recorded response, falling through to inner on exhaustion.
func (a *RecordingAdapter) chatReplay(ctx context.Context, req *provider.Request, metric string) (*provider.Response, error) {
	proceed, replay, err := a.hooks.PreCall(metric)
	if err != nil {
		// Replay exhausted: fall through to inner adapter.
		cluster.DefaultLogger("recording-adapter").Warn("replay exhausted, falling through to inner", slog.String("error", err.Error()))
		return a.inner.Chat(ctx, req)
	}

	if !proceed && replay != nil {
		return replayRecordToResponse(replay), nil
	}

	// proceed=true means no replay available (shouldn't happen in replay mode, but be safe).
	return a.inner.Chat(ctx, req)
}

// chatRecord calls the inner adapter and records the interaction.
func (a *RecordingAdapter) chatRecord(ctx context.Context, req *provider.Request, metric string) (*provider.Response, error) {
	start := time.Now()
	resp, err := a.inner.Chat(ctx, req)
	latency := time.Since(start)

	success := err == nil && resp != nil
	llmReq := requestToLLMReq(req)
	llmResp := responseToLLMResp(resp, err)

	a.hooks.PostCall(metric, llmReq, llmResp, latency, success)

	return resp, err
}

// ChatStream handles streaming with recording/replay.
// In replay mode: delegates to Chat (streaming doesn't make sense for recorded data).
// In record mode: collects stream into full response, records, then returns.
func (a *RecordingAdapter) ChatStream(ctx context.Context, req *provider.Request) (provider.Stream, error) {
	// Replay mode: delegate to Chat and wrap result as a single-event stream.
	if a.hooks.Mode == LLMHookReplay {
		resp, err := a.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		return newSingleResponseStream(resp), nil
	}

	// Record mode: collect the stream, record the full response.
	return a.streamAndRecord(ctx, req)
}

// streamAndRecord opens a stream, collects all events, records the result, and
// returns a pre-filled stream that replays the collected events.
func (a *RecordingAdapter) streamAndRecord(ctx context.Context, req *provider.Request) (provider.Stream, error) {
	start := time.Now()
	stream, err := a.inner.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}

	events, resp := collectStream(stream)
	latency := time.Since(start)

	metric := metricFromRequest(req)
	llmReq := requestToLLMReq(req)
	llmResp := responseToLLMResp(resp, nil)

	a.hooks.PostCall(metric, llmReq, llmResp, latency, true)

	return newBufferedStream(events), nil
}

// ── Conversion helpers ──

// metricFromRequest extracts a metric name from the request for budget tracking.
func metricFromRequest(req *provider.Request) string {
	if req == nil || len(req.Messages) == 0 {
		return "unknown"
	}
	// Use the last user message content prefix as metric identifier.
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			content := req.Messages[i].Content
			if len(content) > 64 {
				content = content[:64]
			}
			return content
		}
	}
	return "chat"
}

// requestToLLMReq converts a provider.Request to an LLMReq for recording.
func requestToLLMReq(req *provider.Request) LLMReq {
	if req == nil {
		return LLMReq{}
	}
	msgs := make([]any, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = m
	}
	return LLMReq{
		Model:    "engine-v2",
		Messages: msgs,
	}
}

// responseToLLMResp converts a provider.Response to an LLMResp for recording.
func responseToLLMResp(resp *provider.Response, err error) LLMResp {
	if resp == nil {
		reason := "error"
		if err != nil {
			reason = err.Error()
		}
		return LLMResp{FinishReason: reason}
	}

	var toolCalls []any
	for _, tc := range resp.ToolCalls {
		toolCalls = append(toolCalls, tc)
	}

	return LLMResp{
		Content:      resp.Content,
		ToolCalls:    toolCalls,
		FinishReason: resp.StopReason,
		TokensUsed:   resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
}

// replayRecordToResponse converts a replayed LLMRecord back to a provider.Response.
func replayRecordToResponse(rec *LLMRecord) *provider.Response {
	return &provider.Response{
		Content:    rec.Response.Content,
		StopReason: rec.Response.FinishReason,
		Usage: provider.Usage{
			OutputTokens: rec.Response.TokensUsed,
		},
	}
}

// ── Single-response stream (for replay mode) ──

type singleResponseStream struct {
	resp *provider.Response
	done bool
}

func newSingleResponseStream(resp *provider.Response) *singleResponseStream {
	return &singleResponseStream{resp: resp}
}

func (s *singleResponseStream) Next() (provider.StreamEvent, error) {
	if s.done {
		return provider.StreamEvent{Type: provider.StreamDone, FinishReason: "stop"}, fmt.Errorf("stream done")
	}
	s.done = true
	return provider.StreamEvent{
		Type:         provider.StreamTextDelta,
		Content:      s.resp.Content,
		FinishReason: s.resp.StopReason,
	}, nil
}

func (s *singleResponseStream) Close() error { return nil }

// ── Buffered stream (for record mode streaming) ──

type bufferedStream struct {
	events []provider.StreamEvent
	cursor int
}

func newBufferedStream(events []provider.StreamEvent) *bufferedStream {
	return &bufferedStream{events: events}
}

func (s *bufferedStream) Next() (provider.StreamEvent, error) {
	if s.cursor >= len(s.events) {
		return provider.StreamEvent{Type: provider.StreamDone, FinishReason: "stop"}, fmt.Errorf("stream done")
	}
	evt := s.events[s.cursor]
	s.cursor++
	return evt, nil
}

func (s *bufferedStream) Close() error { return nil }

// collectStream drains a stream, collecting all events and building a Response.
func collectStream(stream provider.Stream) ([]provider.StreamEvent, *provider.Response) {
	var events []provider.StreamEvent
	var content string

	for {
		evt, err := stream.Next()
		events = append(events, evt)
		if err != nil || evt.Type == provider.StreamDone {
			break
		}
		if evt.Type == provider.StreamTextDelta {
			content += evt.Content
		}
	}
	stream.Close()

	resp := &provider.Response{
		Content:    content,
		StopReason: "stop",
	}
	return events, resp
}
