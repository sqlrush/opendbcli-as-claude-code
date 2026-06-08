package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/sqlrush/opendb/internal/llm"
)

func TestBuildRequestBodyConservativeOmitsFalseStream(t *testing.T) {
	p := NewProvider("http://llm-gateway/v1", "qwen", "dummy", WithCompatMode("conservative"))
	body, err := p.buildRequestBody(llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: "ping"}},
	}, false)
	if err != nil {
		t.Fatalf("buildRequestBody error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if _, ok := got["stream"]; ok {
		t.Fatalf("conservative non-stream request should omit stream=false: %s", string(body))
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("conservative request without tools should omit tools: %s", string(body))
	}
	if got["model"] != "qwen" {
		t.Fatalf("model = %v, want qwen", got["model"])
	}
}

func TestBuildRequestBodyConservativeKeepsStreamTrue(t *testing.T) {
	p := NewProvider("http://llm-gateway/v1", "qwen", "dummy", WithCompatMode("conservative"))
	body, err := p.buildRequestBody(llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Content: "ping"}},
	}, true)
	if err != nil {
		t.Fatalf("buildRequestBody error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true; body=%s", got["stream"], string(body))
	}
}
