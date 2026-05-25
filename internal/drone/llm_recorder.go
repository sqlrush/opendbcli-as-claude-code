/*-------------------------------------------------------------------------
 *
 * llm_recorder.go
 *	  llm_recorder.go implements LLM request/response recording and
 *	  replay. Record mode: capture real LLM interactions to files during
 *	  testing. Replay mode: feed recorded responses in CI/CD (zero LLM
 *	  cost, deterministic).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/llm_recorder.go
 *
 *-------------------------------------------------------------------------
 */
// llm_recorder.go implements LLM request/response recording and replay.
// Record mode: capture real LLM interactions to files during testing.
// Replay mode: feed recorded responses in CI/CD (zero LLM cost, deterministic).
package drone

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/cluster"
)

// LLMRecord holds a single LLM request/response pair.
type LLMRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Request   LLMReq    `json:"request"`
	Response  LLMResp   `json:"response"`
	LatencyMs int64     `json:"latency_ms"`
}

// LLMReq is a simplified LLM request for recording.
type LLMReq struct {
	Model    string `json:"model"`
	Messages []any  `json:"messages"` // raw message objects
}

// LLMResp is a simplified LLM response for recording.
type LLMResp struct {
	Content      string `json:"content"`
	ToolCalls    []any  `json:"tool_calls,omitempty"`
	FinishReason string `json:"finish_reason"`
	TokensUsed   int    `json:"tokens_used"`
}

// LLMRecorder captures LLM interactions to a JSONL file.
type LLMRecorder struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	enabled bool
}

// NewLLMRecorder creates a recorder that writes to the given directory.
func NewLLMRecorder(dir string) (*LLMRecorder, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create recording dir: %w", err)
	}

	fileName := fmt.Sprintf("llm_session_%s.jsonl", time.Now().Format("20060102_150405"))
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open recording file: %w", err)
	}

	cluster.DefaultLogger("llm-recorder").Info("Recording to file", slog.String("path", path))
	return &LLMRecorder{file: f, path: path, enabled: true}, nil
}

// Record writes a single LLM interaction to the recording file.
func (r *LLMRecorder) Record(rec LLMRecord) error {
	if !r.enabled {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	if _, err := r.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	return nil
}

// Close closes the recording file.
func (r *LLMRecorder) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// Path returns the recording file path.
func (r *LLMRecorder) Path() string { return r.path }

// ---- Replay ----

// LLMReplayer reads recorded LLM interactions and feeds them back.
type LLMReplayer struct {
	mu      sync.Mutex
	records []LLMRecord
	cursor  int
	path    string
}

// NewLLMReplayer loads a recorded session file for replay.
func NewLLMReplayer(path string) (*LLMReplayer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recording: %w", err)
	}

	var records []LLMRecord
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec LLMRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // skip malformed lines
		}
		records = append(records, rec)
	}

	cluster.DefaultLogger("llm-replayer").Info("Loaded records", slog.Int("count", len(records)), slog.String("path", path))
	return &LLMReplayer{records: records, path: path}, nil
}

// Next returns the next recorded response, or error if exhausted.
func (r *LLMReplayer) Next() (LLMRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cursor >= len(r.records) {
		return LLMRecord{}, fmt.Errorf("replay exhausted (%d records)", len(r.records))
	}

	rec := r.records[r.cursor]
	r.cursor++
	return rec, nil
}

// Remaining returns the number of remaining records.
func (r *LLMReplayer) Remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records) - r.cursor
}

// Reset rewinds the replayer to the beginning.
func (r *LLMReplayer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cursor = 0
}

// splitLines splits byte data by newlines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
