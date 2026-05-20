/*-------------------------------------------------------------------------
 *
 * llm_adapter.go
 *	  Adapts internal/llm.Provider → sqltune.LLMCaller. Lives in each
 *	  dialect's sqltuner package (not the neutral sqltune package) so
 *	  sqltune doesn't need to reverse-import internal/llm.
 *
 *	  Identical 30-line file in mysql / postgres / oracle / gaussdb
 *	  sqltuner packages — DRY violation accepted because cross-package
 *	  utility for this would be the worse cost.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/llm_adapter.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// llmAdapter wraps an llm.Provider into a sqltune.LLMCaller.
type llmAdapter struct {
	provider llm.Provider
}

// newLLMAdapter returns nil if provider is nil — callers (GenericTuner)
// handle nil LLMCaller gracefully (skip Round 1, render raw Phase A).
func newLLMAdapter(p llm.Provider) sqltune.LLMCaller {
	if p == nil {
		return nil
	}
	return &llmAdapter{provider: p}
}

func (a *llmAdapter) Chat(ctx context.Context, msgs []sqltune.ChatMessage) (string, error) {
	if a.provider == nil {
		return "", fmt.Errorf("llm provider not configured")
	}
	llmMsgs := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		llmMsgs[i] = llm.Message{Role: m.Role, Content: m.Content}
	}
	resp, err := a.provider.Chat(ctx, llm.ChatRequest{Messages: llmMsgs})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("nil response from llm provider")
	}
	return resp.Content, nil
}
