/*-------------------------------------------------------------------------
 *
 * prompt_stream_adapter.go
 *	  v1.2.1 stream adapter for PromptToolAdapter. Wraps a legacy provider
 *	  stream and routes incoming text deltas through StreamingParser:
 *
 *	    - Format A (tool_call JSON): buffer chunks silently until JSON
 *	      complete, then emit a synthetic StreamToolCallDelta event per
 *	      parsed ToolCall, followed by StreamDone.
 *	    - Format B (markdown answer): pass chunks through as
 *	      StreamTextDelta in real time. Caller sees natural streaming.
 *
 *	  Only activated when the active PromptBuilder is PromptModeBuilder
 *	  (legacywrapper.ChatStream checks Mode() == "prompt"). Native FC
 *	  streams skip this adapter entirely — zero latency overhead.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/bridge/prompt_stream_adapter.go
 *
 *-------------------------------------------------------------------------
 */
package bridge

import (
	"github.com/sqlrush/opendb/internal/engine/provider"
)

// promptStreamAdapter wraps a legacyStreamWrapper and routes text deltas
// through StreamingParser. Implements provider.Stream.
type promptStreamAdapter struct {
	inner  *legacyStreamWrapper
	parser *provider.StreamingParser

	// pendingToolCalls is the queue of synthetic StreamToolCallDelta events
	// emitted after Format A parsing succeeds. We deliver one per Next()
	// call so the engine's stream consumer sees them as discrete events
	// (matching native FC behavior).
	pendingToolCalls []provider.ToolCall
	// pendingText is fallback content from Format A parse failure that
	// needs to be delivered before the Done event.
	pendingText string
	// finalized is true after we've emitted everything from Finish().
	finalized bool

	// v1.2.6: Qwen3/DeepSeek thinking 模式输出 `<think>...</think>` 块，
	// 这些 token 流过来时，不能喂给 StreamingParser（否则 64 字节后会被
	// 误判为 FormatB，导致后面真的 JSON tool_call 被当成文字 fallback）。
	// 用一个简单状态机识别 <think>/</think> 标签：think 块**直接 forward**
	// 给 engine（engine 自己会处理放到 thinking buffer），不进入 parser。
	inThink bool
	// pendingThinkOut 在同一 chunk 既含 </think> 又含后续 content 时，
	// content 已 feed 给 parser，think 段需在下一轮 Next() 返回给 engine。
	pendingThinkOut string
}

// newPromptStreamAdapter constructs the adapter with a fresh StreamingParser.
// knownToolNames enables Levenshtein correction during JSON parse.
func newPromptStreamAdapter(inner *legacyStreamWrapper, knownToolNames []string) *promptStreamAdapter {
	return &promptStreamAdapter{
		inner:  inner,
		parser: provider.NewStreamingParser(knownToolNames, 64),
	}
}

// Next reads the next event from the underlying stream, routes it through
// the parser, and returns the appropriate event for the engine.
//
// Flow:
//
//	1. If pendingToolCalls is non-empty, dequeue one and emit it.
//	2. Else if finalized and no pending text, emit StreamDone.
//	3. Else read from inner stream.
//	4. Text delta → parser.Feed(); emit user-facing text or buffer silently.
//	5. Inner stream returns StreamDone → call parser.Finish() and emit
//	   any synthetic tool_call events (queued) then StreamDone.
func (a *promptStreamAdapter) Next() (provider.StreamEvent, error) {
	// Drain queued synthetic events first.
	if len(a.pendingToolCalls) > 0 {
		tc := a.pendingToolCalls[0]
		a.pendingToolCalls = a.pendingToolCalls[1:]
		return provider.StreamEvent{
			Type:     provider.StreamToolCallDelta,
			ToolCall: &tc,
		}, nil
	}
	if a.pendingText != "" {
		text := a.pendingText
		a.pendingText = ""
		return provider.StreamEvent{Type: provider.StreamTextDelta, Content: text}, nil
	}
	if a.pendingThinkOut != "" {
		text := a.pendingThinkOut
		a.pendingThinkOut = ""
		return provider.StreamEvent{Type: provider.StreamTextDelta, Content: text}, nil
	}
	if a.finalized {
		return provider.StreamEvent{Type: provider.StreamDone, FinishReason: "stop"}, nil
	}

	for {
		ev, err := a.inner.Next()
		// v1.2.6: legacy stream (openaicompat) 在 finish_reason chunk 同时返回
		// StreamDone event + io.EOF error。如果直接 return，parser.Finish 永
		// 远不被调用，buffered JSON tool_calls 整段丢失。把 err 时仍把
		// StreamDone 走进入 case 流程，让 Finish 把 calls 取出来。
		if err != nil && ev.Type != provider.StreamDone {
			return ev, err
		}
		switch ev.Type {
		case provider.StreamTextDelta:
			thinkOut, parseIn := a.splitThinkAndContent(ev.Content)
			if parseIn != "" {
				textForUser, _ := a.parser.Feed(parseIn)
				if textForUser != "" {
					if thinkOut != "" {
						a.pendingThinkOut = thinkOut
					}
					return provider.StreamEvent{Type: provider.StreamTextDelta, Content: textForUser}, nil
				}
			}
			if thinkOut != "" {
				return provider.StreamEvent{Type: provider.StreamTextDelta, Content: thinkOut}, nil
			}
			continue

		case provider.StreamThinkingDelta:
			return ev, nil

		case provider.StreamToolCallDelta:
			return ev, nil

		case provider.StreamDone:
			calls, text, _, _ := a.parser.Finish()
			a.finalized = true
			if len(calls) > 0 {
				a.pendingToolCalls = calls
				// Emit the first immediately.
				tc := a.pendingToolCalls[0]
				a.pendingToolCalls = a.pendingToolCalls[1:]
				return provider.StreamEvent{
					Type:     provider.StreamToolCallDelta,
					ToolCall: &tc,
				}, nil
			}
			if text != "" {
				return provider.StreamEvent{Type: provider.StreamTextDelta, Content: text}, nil
			}
			return ev, nil // pass through the Done event with its finish reason

		default:
			return ev, nil
		}
	}
}

// Close releases the underlying stream.
func (a *promptStreamAdapter) Close() error {
	return a.inner.Close()
}

// splitThinkAndContent 把一个 chunk 拆成两部分：
//
//	thinkOut: <think>...</think> 内（含标签自身）的文字，forward 给 engine
//	parseIn:  think 外的真实 content，喂给 parser
//
// 单 chunk 内可能跨标签（"foo</think>\n{\"tool_calls\":...", 含闭合标签+JSON 开始），
// 用状态机识别。a.inThink / a.tagBuf 维持跨 chunk 状态。
func (a *promptStreamAdapter) splitThinkAndContent(delta string) (thinkOut, parseIn string) {
	// 简化策略：用 strings.Index 扫描 <think> 和 </think> 标签
	// （Qwen3/DeepSeek 都用这俩闭合标签，标签本身不会跨 byte 分割）
	s := delta
	for s != "" {
		if a.inThink {
			idx := indexOfClose(s)
			if idx < 0 {
				// 整段都在 think 内 → 全部 forward 为 think 文字
				thinkOut += s
				return
			}
			// 找到 </think>，结束 think 段
			thinkOut += s[:idx+len("</think>")]
			a.inThink = false
			s = s[idx+len("</think>"):]
			continue
		}
		idx := indexOfOpen(s)
		if idx < 0 {
			// 没有 <think>，全段进 parser
			parseIn += s
			return
		}
		// 找到 <think>，进入 think 状态
		parseIn += s[:idx]
		thinkOut += s[idx : idx+len("<think>")]
		a.inThink = true
		s = s[idx+len("<think>"):]
	}
	return
}

func indexOfOpen(s string) int {
	// 不区分大小写不必要：Qwen3 输出固定小写 <think>
	for i := 0; i+7 <= len(s); i++ {
		if s[i] == '<' && s[i:i+7] == "<think>" {
			return i
		}
	}
	return -1
}

func indexOfClose(s string) int {
	for i := 0; i+8 <= len(s); i++ {
		if s[i] == '<' && s[i:i+8] == "</think>" {
			return i
		}
	}
	return -1
}
