/*-------------------------------------------------------------------------
 *
 * stream.go
 *	  Streaming SSE parser for OpenAI-compatible providers — turns the
 *	  `data: {...}` event stream into typed chunks (content delta,
 *	  tool_calls delta, finish_reason). Used by the Engine's streaming
 *	  path to yield partial output as it arrives.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/openaicompat/stream.go
 *
 *-------------------------------------------------------------------------
 */
package openaicompat

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/sqlrush/opendb/internal/llm"
)

// stream implements llm.Stream by reading SSE events from an HTTP response body.
type stream struct {
	reader *bufio.Reader
	body   io.ReadCloser
}

func newStream(body io.ReadCloser) *stream {
	return &stream{
		reader: bufio.NewReader(body),
		body:   body,
	}
}

// Next reads the next StreamEvent from the SSE stream.
func (s *stream) Next() (llm.StreamEvent, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return llm.StreamEvent{Type: llm.StreamDone}, io.EOF
			}
			return llm.StreamEvent{}, err
		}

		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		if line == "data: [DONE]" {
			return llm.StreamEvent{Type: llm.StreamDone}, io.EOF
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var chunk oaiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Kimi/MiMo/DeepSeek: reasoning_content carries thinking tokens.
		if delta.ReasoningContent != "" {
			return llm.StreamEvent{
				Type:             llm.StreamTextDelta,
				ReasoningContent: delta.ReasoningContent,
			}, nil
		}

		if len(delta.ToolCalls) > 0 {
			tc := delta.ToolCalls[0]
			return llm.StreamEvent{
				Type: llm.StreamToolCallDelta,
				ToolCall: &llm.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}, nil
		}

		if delta.Content != "" {
			return llm.StreamEvent{
				Type:    llm.StreamTextDelta,
				Content: delta.Content,
			}, nil
		}

		if chunk.Choices[0].FinishReason != nil {
			return llm.StreamEvent{
				Type:         llm.StreamDone,
				FinishReason: *chunk.Choices[0].FinishReason,
			}, io.EOF
		}
	}
}

// Close releases the underlying HTTP response body.
func (s *stream) Close() error {
	return s.body.Close()
}
