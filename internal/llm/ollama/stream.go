/*-------------------------------------------------------------------------
 *
 * stream.go
 *	  OllamaStream implements llm.Stream by reading SSE events from an
 *	  HTTP response body.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/ollama/stream.go
 *
 *-------------------------------------------------------------------------
 */
package ollama

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/sqlrush/opendb/internal/llm"
)

// OllamaStream implements llm.Stream by reading SSE events from an HTTP response body.
type OllamaStream struct {
	reader *bufio.Reader
	body   io.ReadCloser
}

func newOllamaStream(body io.ReadCloser) *OllamaStream {
	return &OllamaStream{
		reader: bufio.NewReader(body),
		body:   body,
	}
}

// Next reads the next StreamEvent from the SSE stream.
// Returns io.EOF when the stream is complete.
func (s *OllamaStream) Next() (llm.StreamEvent, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return llm.StreamEvent{Type: llm.StreamDone}, io.EOF
			}
			return llm.StreamEvent{}, err
		}

		line = strings.TrimSpace(line)

		// Skip empty lines and SSE comments.
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Handle the "[DONE]" sentinel.
		if line == "data: [DONE]" {
			return llm.StreamEvent{Type: llm.StreamDone}, io.EOF
		}

		// Parse SSE data lines.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var chunk openAIResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Emit tool call delta events.
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

		// Emit text delta events.
		if delta.Content != "" {
			return llm.StreamEvent{
				Type:    llm.StreamTextDelta,
				Content: delta.Content,
			}, nil
		}

		// Check for finish_reason signaling completion.
		if chunk.Choices[0].FinishReason != nil {
			return llm.StreamEvent{Type: llm.StreamDone}, io.EOF
		}
	}
}

// Close releases the underlying HTTP response body.
func (s *OllamaStream) Close() error {
	return s.body.Close()
}
