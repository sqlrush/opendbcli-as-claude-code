/*-------------------------------------------------------------------------
 *
 * llmtest.go
 *	  LLMTestStep runs three-step connectivity test with user choice
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/llmtest.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

type llmTestResult struct {
	step    int    // 1=reachable, 2=model, 3=inference
	ok      bool
	detail  string
	latency time.Duration
}

type llmTestDoneMsg struct {
	results []llmTestResult
}

type llmTestPhase int

const (
	llmTestRunning  llmTestPhase = iota
	llmTestChoosing
)

// LLMTestStep runs three-step connectivity test with user choice
type LLMTestStep struct {
	cfg     *SetupConfig
	phase   llmTestPhase
	results []llmTestResult
	choice  *SelectModel
	done    bool
}

func NewLLMTestStep(cfg *SetupConfig) *LLMTestStep {
	return &LLMTestStep{cfg: cfg}
}

func (s *LLMTestStep) Title() string { return "Model Test" }
func (s *LLMTestStep) Done() bool    { return s.done }

func (s *LLMTestStep) Summary() string {
	if s.cfg.LLM.Provider == "none" {
		return CompletedLine("Model Test", "Skipped")
	}
	allOK := true
	for _, r := range s.results {
		if !r.ok {
			allOK = false
			break
		}
	}
	if allOK {
		s.cfg.LLMTestOK = true
		return CompletedLine("Model Test", "All checks passed")
	}
	return CompletedLine("Model Test", "Some checks failed")
}

func (s *LLMTestStep) Init() tea.Cmd {
	if s.cfg.LLM.Provider == "none" {
		s.done = true
		return emitDone
	}
	s.phase = llmTestRunning
	return s.runTests()
}

func (s *LLMTestStep) runTests() tea.Cmd {
	return func() tea.Msg {
		var results []llmTestResult

		// Step 1: API reachable
		reachable := false
		ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel1()

		url := s.cfg.LLM.BaseURL + "/models"
		req, err := http.NewRequestWithContext(ctx1, http.MethodGet, url, nil)
		if err == nil {
			if s.cfg.LLMApiKey != "" {
				req.Header.Set("Authorization", "Bearer "+s.cfg.LLMApiKey)
			}
			resp, httpErr := http.DefaultClient.Do(req)
			if httpErr != nil {
				results = append(results, llmTestResult{step: 1, ok: false, detail: httpErr.Error()})
			} else {
				resp.Body.Close()
				reachable = true
				results = append(results, llmTestResult{step: 1, ok: true, detail: s.cfg.LLM.BaseURL})
			}
		} else {
			results = append(results, llmTestResult{step: 1, ok: false, detail: err.Error()})
		}

		if !reachable {
			return llmTestDoneMsg{results: results}
		}

		// Step 2: Inference (combined model-exists + chat) — single chat
		// request that doubles as "model recognised by API" check and latency
		// measurement. Previously this was split into two steps that each
		// burned a chat request, tripping per-org rate limits (Moonshot 429
		// after picker already probed N models).
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()

		// Build chat URL: append /chat/completions to baseURL.
		// If baseURL already ends with a version path (/v1, /v4, etc.), use directly.
		// If not (e.g. Ollama localhost:11434), add /v1 first.
		trimmed := strings.TrimRight(s.cfg.LLM.BaseURL, "/")
		chatURL := trimmed + "/chat/completions"
		if !hasVersionSuffix(trimmed) {
			chatURL = trimmed + "/v1/chat/completions"
		}

		reqBody, _ := json.Marshal(map[string]any{
			"model":      s.cfg.LLM.Model,
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
			"max_tokens": 1,
		})
		chatReq, _ := http.NewRequestWithContext(ctx2, http.MethodPost, chatURL, bytes.NewReader(reqBody))
		chatReq.Header.Set("Content-Type", "application/json")
		if s.cfg.LLMApiKey != "" {
			chatReq.Header.Set("Authorization", "Bearer "+s.cfg.LLMApiKey)
		}

		start := time.Now()
		chatResp, inferErr := http.DefaultClient.Do(chatReq)
		latency := time.Since(start)

		if inferErr != nil {
			results = append(results, llmTestResult{step: 2, ok: false, detail: inferErr.Error()})
			return llmTestDoneMsg{results: results}
		}
		body, _ := io.ReadAll(chatResp.Body)
		chatResp.Body.Close()

		if chatResp.StatusCode == http.StatusOK {
			results = append(results, llmTestResult{step: 2, ok: true,
				detail: s.cfg.LLM.Model, latency: latency})
		} else if chatResp.StatusCode == http.StatusTooManyRequests {
			// Rate-limited: don't fail the model — the config IS valid, the
			// API is just busy. Picker / step 1 already confirmed reachable.
			// Mark as ok=true with a warning detail so user can save and try
			// again later without fighting the wizard.
			results = append(results, llmTestResult{step: 2, ok: true,
				detail: s.cfg.LLM.Model + " (rate limited, 配置可用, 稍后重试)",
				latency: latency,
			})
		} else if chatResp.StatusCode == http.StatusNotFound || chatResp.StatusCode == http.StatusBadRequest {
			// 404/400 typically means model name wrong — pull /models for hint
			hint := fmt.Sprintf("HTTP %d: %s", chatResp.StatusCode, truncRW(string(body), 80))
			if available, ferr := FetchModelsFromAPI(s.cfg.LLM.BaseURL, s.cfg.LLMApiKey); ferr == nil && len(available) > 0 {
				hint += "\n  Available models: " + strings.Join(available, ", ")
			}
			results = append(results, llmTestResult{step: 2, ok: false, detail: hint})
		} else {
			results = append(results, llmTestResult{step: 2, ok: false,
				detail: fmt.Sprintf("HTTP %d: %s", chatResp.StatusCode, truncRW(string(body), 80))})
		}

		return llmTestDoneMsg{results: results}
	}
}

// hasVersionSuffix checks if a URL path ends with a version segment like /v1, /v4, /v1beta, etc.
func hasVersionSuffix(url string) bool {
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return false
	}
	last := strings.ToLower(parts[len(parts)-1])
	return strings.HasPrefix(last, "v") && len(last) >= 2 && last[1] >= '0' && last[1] <= '9'
}

func (s *LLMTestStep) buildChoice() {
	allOK := true
	for _, r := range s.results {
		if !r.ok {
			allOK = false
			break
		}
	}
	s.cfg.LLMTestOK = allOK

	if allOK {
		s.choice = NewSelectModel("", []SelectItem{
			{Label: "下一步", Value: "next", Desc: "继续配置"},
			{Label: "重新配置", Value: "reconfigure", Desc: "修改 Model 配置"},
		}, "next")
	} else {
		s.choice = NewSelectModel("", []SelectItem{
			{Label: "重新配置", Value: "reconfigure", Desc: "修改 Model 配置"},
			{Label: "跳过 LLM (rule-only)", Value: "skip", Desc: "不保存这次填的模型, 稍后用 " + brand.Current().BinaryName + " configure 添加"},
		}, "reconfigure")
	}
}

func (s *LLMTestStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case llmTestRunning:
		if m, ok := msg.(llmTestDoneMsg); ok {
			s.results = m.results
			s.buildChoice()
			s.phase = llmTestChoosing
			return s, nil
		}
	case llmTestChoosing:
		updated, cmd := s.choice.Update(msg)
		s.choice = updated
		if s.choice.Done() {
			selected := s.choice.Selected()
			if selected == "reconfigure" {
				return s, emitBack(2)
			}
			if selected == "skip" {
				// User chose to skip a failed LLM test — clear the broken
				// model so FinalizeStep doesn't write it into config.yaml.
				// Without this, the wizard saved a known-broken model and
				// told the user "稍后修复" — confusing because /llm would
				// then try to use it.
				s.cfg.LLM.Provider = "none"
				s.cfg.LLM.Model = ""
				s.cfg.LLM.BaseURL = ""
				s.cfg.LLMApiKey = ""
				s.cfg.LLMVendor = ""
			}
			s.done = true
			return s, emitDone
		}
		return s, cmd
	}
	return s, nil
}

func (s *LLMTestStep) View() string {
	var b strings.Builder

	stepNames := []string{
		"API reachable",
		"Inference",
	}

	if s.phase == llmTestRunning {
		for _, name := range stepNames {
			b.WriteString(BulletLine(name+"...") + "\n")
		}
		return "\n" + InfoPanel("Model Connection Test", b.String()) + "\n"
	}

	for _, r := range s.results {
		if r.step < 1 || r.step > len(stepNames) {
			continue
		}
		name := stepNames[r.step-1]
		if r.ok {
			detail := r.detail
			if r.step == 2 && r.latency > 0 {
				if detail == "" {
					detail = fmt.Sprintf("%.1fs", r.latency.Seconds())
				} else {
					detail = fmt.Sprintf("%s · %.1fs", detail, r.latency.Seconds())
				}
			}
			b.WriteString(SuccessLine(name+" — "+detail) + "\n")
		} else {
			b.WriteString(ErrorLine(name+" — "+r.detail) + "\n")
		}
	}

	allOK := true
	for _, r := range s.results {
		if !r.ok {
			allOK = false
			break
		}
	}

	if allOK {
		b.WriteString("\n" + SuccessLine("Model 配置就绪") + "\n")
	} else {
		// Don't claim "已保存" — the wizard hasn't reached FinalizeStep yet,
		// and the user may pick "重新配置" to fix the failure. Showing
		// "saved" up here misleads users into thinking the broken model is
		// already in their config. The choice menu below clarifies.
		b.WriteString("\n" + StyleDim.Render("  请选择: 重新配置修复, 或跳过 LLM 配置 (rule-only 模式).") + "\n")
	}

	result := "\n" + InfoPanel("Model Connection Test", b.String()) + "\n"
	if s.choice != nil {
		result += s.choice.View()
	}
	return result
}
