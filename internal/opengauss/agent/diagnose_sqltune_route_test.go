package agent

import "testing"

func TestShouldForceSQLTune(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "labeled sql id tune", in: "sql id 581990336 如何优化", want: "581990336", ok: true},
		{name: "uppercase sql id explain", in: "SQL_ID 4175761868 看执行计划", want: "4175761868", ok: true},
		{name: "bare id with tune intent", in: "581990336 怎么调优", want: "581990336", ok: true},
		{name: "no tune intent", in: "SQL_ID 581990336", ok: false},
		{name: "pasted sql", in: "select * from bench_orders where id = 581990336 如何优化", ok: false},
		{name: "ambiguous multiple numbers", in: "581990336 和 123456789 哪个慢", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := shouldForceSQLTune(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("shouldForceSQLTune(%q) = %q,%v; want %q,%v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSQLTuneToolCallsUseNumericID(t *testing.T) {
	calls := sqlTuneToolCalls("581990336")
	if len(calls) != 1 {
		t.Fatalf("expected one forced call, got %d", len(calls))
	}
	if calls[0].Name != "sqltune" {
		t.Fatalf("expected sqltune, got %q", calls[0].Name)
	}
	if calls[0].Arguments != `{"args":"581990336","mode":"quick"}` {
		t.Fatalf("unexpected arguments: %s", calls[0].Arguments)
	}
}

func TestShouldUseCurrentDBFastSummary(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		toolMode   string
		want       bool
	}{
		{name: "prompt mode large still fast path", capability: "large", toolMode: "prompt", want: true},
		{name: "small native fast path", capability: "small", toolMode: "native", want: true},
		{name: "medium native fast path", capability: "medium", toolMode: "", want: true},
		{name: "large native keeps agent path", capability: "large", toolMode: "native", want: false},
		{name: "large empty tool mode keeps agent path", capability: "large", toolMode: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseCurrentDBFastSummary(tt.capability, tt.toolMode); got != tt.want {
				t.Fatalf("shouldUseCurrentDBFastSummary(%q,%q)=%v want %v", tt.capability, tt.toolMode, got, tt.want)
			}
		})
	}
}

func TestShouldUseCurrentDBManagedEvidenceLLM(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		toolMode   string
		modelName  string
		want       bool
	}{
		{name: "qwen3 prompt uses managed llm", capability: "large", toolMode: "prompt", modelName: "qwen3-32b-prompt", want: true},
		{name: "qwen36 native uses managed llm", capability: "large", toolMode: "native", modelName: "qwen36-35b-a3b", want: true},
		{name: "unknown small native uses managed llm when model missing", capability: "small", toolMode: "native", modelName: "", want: true},
		{name: "glm keeps full agent path", capability: "medium", toolMode: "", modelName: "glm-5.1", want: false},
		{name: "opus keeps full agent path", capability: "large", toolMode: "native", modelName: "opus", want: false},
		{name: "gpt keeps full agent path", capability: "large", toolMode: "native", modelName: "gpt5.5", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseCurrentDBManagedEvidenceLLM(tt.capability, tt.toolMode, tt.modelName); got != tt.want {
				t.Fatalf("shouldUseCurrentDBManagedEvidenceLLM(%q,%q,%q)=%v want %v", tt.capability, tt.toolMode, tt.modelName, got, tt.want)
			}
		})
	}
}
