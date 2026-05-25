/*-------------------------------------------------------------------------
 * output.go
 *    Parse external skill stdout into skill.Result.
 *-------------------------------------------------------------------------
 */
package external

import (
	"encoding/json"
	"strings"

	"github.com/sqlrush/opendb/internal/skill"
)

type scriptOutput struct {
	OK       *bool             `json:"ok,omitempty"`
	Summary  string            `json:"summary,omitempty"`
	Rendered string            `json:"rendered,omitempty"`
	Data     any               `json:"data,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func resultFromStdout(stdout string) *skill.Result {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return &skill.Result{Type: skill.ResultText, Rendered: "", Summary: "empty output"}
	}
	var out scriptOutput
	if err := json.Unmarshal([]byte(trimmed), &out); err == nil && (out.Rendered != "" || out.Summary != "" || out.Data != nil || out.Metadata != nil) {
		rendered := out.Rendered
		if rendered == "" {
			rendered = out.Summary
		}
		return &skill.Result{Type: skill.ResultText, Rendered: rendered, Summary: out.Summary, Data: out.Data, Metadata: out.Metadata}
	}
	return &skill.Result{Type: skill.ResultText, Rendered: stdout, Summary: firstLine(trimmed)}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}
