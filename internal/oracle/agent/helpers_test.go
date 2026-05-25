/*-------------------------------------------------------------------------
 *
 * helpers_test.go
 *	  Test cases for helpers.go (agent package).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/helpers_test.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"
	"errors"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

// ---------------------------------------------------------------------------
// Mock LLM Provider
// ---------------------------------------------------------------------------

type mockProvider struct {
	responses []*llm.Response
	callIdx   int
	received  []llm.ChatRequest // captured requests for assertions
}

func (m *mockProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.Response, error) {
	m.received = append(m.received, req)
	if m.callIdx >= len(m.responses) {
		return nil, errors.New("mock provider: no more responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (llm.Stream, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProvider) Name() string { return "mock" }

// errProvider always returns an error from Chat.
type errProvider struct{ err error }

func (e *errProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.Response, error) {
	return nil, e.err
}
func (e *errProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (llm.Stream, error) {
	return nil, e.err
}
func (e *errProvider) Name() string { return "err-mock" }

// ---------------------------------------------------------------------------
// Mock Skill
// ---------------------------------------------------------------------------

type mockSkill struct {
	name          string
	desc          string
	toolDef       skill.ToolDef
	secLevel      security.Level
	validateErr   error
	executeResult *skill.Result
	executeErr    error
}

func (m *mockSkill) Name() string               { return m.name }
func (m *mockSkill) Description() string         { return m.desc }
func (m *mockSkill) ToolDef() skill.ToolDef      { return m.toolDef }
func (m *mockSkill) CLIDef() skill.CLIDef        { return skill.CLIDef{Command: m.name} }
func (m *mockSkill) Validate(_ skill.Params) error { return m.validateErr }
func (m *mockSkill) SecurityLevel() security.Level { return m.secLevel }

func (m *mockSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	return m.executeResult, m.executeErr
}

// ---------------------------------------------------------------------------
// Mock Guard
// ---------------------------------------------------------------------------

type mockGuard struct{}

func (g *mockGuard) Authorize(_ context.Context, _ security.Level, _ string) error { return nil }
func (g *mockGuard) RequestConfirmation(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (g *mockGuard) MaxLevel() security.Level { return security.LevelDangerous }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestRegistry(skills ...*mockSkill) *skill.Registry {
	reg := skill.NewRegistry()
	for _, s := range skills {
		reg.Register(s)
	}
	return reg
}

func newTestExecutor(reg *skill.Registry) *skill.Executor {
	return skill.NewExecutor(reg, &mockGuard{})
}

// textResponse builds a Response with plain text content and no tool calls.
func textResponse(content string) *llm.Response {
	return &llm.Response{Content: content}
}

// toolCallResponse builds a Response that requests one or more tool calls.
func toolCallResponse(content string, calls ...llm.ToolCall) *llm.Response {
	return &llm.Response{
		Content:   content,
		ToolCalls: calls,
	}
}

func makeToolCall(id, name, argsJSON string) llm.ToolCall {
	return llm.ToolCall{
		ID:        id,
		Name:      name,
		Arguments: argsJSON,
	}
}

// emptyParams returns an empty Params for testing.
func emptyParams() skill.Params {
	return skill.ParamsFromMap(map[string]any{})
}

// p builds Params from key-value pairs for testing.
func p(kvs ...string) skill.Params {
	m := map[string]any{}
	for i := 0; i+1 < len(kvs); i += 2 {
		m[kvs[i]] = kvs[i+1]
	}
	return skill.ParamsFromMap(m)
}
