/*-------------------------------------------------------------------------
 *
 * llmconfig.go
 *	  LLMProviderStep shows cloud/local provider list for selection
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/llmconfig.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

// ── Step 1: LLM Provider Selection ─────────────────

// LLMProviderStep shows cloud/local provider list for selection
type LLMProviderStep struct {
	cfg  *SetupConfig
	sel  *SelectModel
	done bool
}

func NewLLMProviderStep(cfg *SetupConfig) *LLMProviderStep {
	var items []SelectItem

	// Cloud providers section
	items = append(items, SelectItem{Label: "模型云端部署", IsHeader: true})
	for _, p := range CloudProviders() {
		items = append(items, SelectItem{Label: p.Name, Value: "cloud:" + p.Name, Desc: p.Vendor})
	}

	// Local providers section
	items = append(items, SelectItem{Label: "模型本地部署", IsHeader: true})
	for _, p := range LocalProviders() {
		items = append(items, SelectItem{Label: p.Name, Value: "local:" + p.Name, Desc: p.BaseURL})
	}

	// Skip option
	items = append(items, SelectItem{Label: "Skip", Value: "skip", Desc: "暂不配置 LLM"})

	return &LLMProviderStep{
		cfg: cfg,
		sel: NewSelectModel("Model/auth provider", items, "cloud:Moonshot (Kimi)"),
	}
}

func (s *LLMProviderStep) Title() string { return "LLM Provider" }
func (s *LLMProviderStep) Done() bool    { return s.done }

func (s *LLMProviderStep) Summary() string {
	if s.cfg.LLM.Provider == "none" {
		return CompletedLine("Model Provider", "Skipped")
	}
	return CompletedLine("Model Provider", s.cfg.LLMProvider)
}

func (s *LLMProviderStep) Init() tea.Cmd { return nil }

func (s *LLMProviderStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Skip section headers
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		selected := s.sel.Selected()
		if selected == "_cloud_header" || selected == "_local_header" {
			switch keyMsg.String() {
			case "enter":
				// Don't allow selecting headers
				return s, nil
			}
		}
	}

	updated, cmd := s.sel.Update(msg)
	s.sel = updated
	if s.sel.Done() {
		selected := s.sel.Selected()
		if selected == "skip" {
			s.cfg.LLM.Provider = "none"
			s.cfg.LLMProvider = "Skip"
			s.done = true
			return s, emitDone
		}

		// Parse "cloud:Name" or "local:Name"
		parts := strings.SplitN(selected, ":", 2)
		if len(parts) != 2 {
			return s, nil
		}
		providerName := parts[1]
		s.cfg.LLMProvider = providerName

		// Find provider info and apply defaults
		allProviders := AllProviders()
		for _, p := range allProviders {
			if p.Name == providerName {
				s.cfg.LLM.BaseURL = p.BaseURL
				s.cfg.LLM.Provider = "openai"
				s.cfg.LLMVendor = p.Vendor
				if p.IsLocal {
					s.cfg.LLMApiKey = "" // no key needed
				}
				break
			}
		}
		s.done = true
		return s, emitDone
	}
	return s, cmd
}

func (s *LLMProviderStep) View() string {
	intro := strings.Join([]string{
		brand.Current().AppName + " 内置可和 Model 联动的可控多轮链式推理引擎，",
		"每轮通过 function calling 调用 30+ 个内置",
		"查询 skill 进行链式推理，逐步收敛直至找到根因。",
		"",
		"支持 Opus、Gemini、Kimi、MiniMax 等主流 Model，",
		"并进行了专门的提示词优化。",
		"",
		"同时支持 Ollama 等本地部署大模型，",
		"确保金融等场景下数据不出内网。",
	}, "\n")

	return "\n" + InfoPanel("Model Diagnostics", intro) + "\n" + s.sel.View()
}

// ── Step 2: LLM Configure (base_url, api_key, model) ──

type llmCfgPhase int

const (
	llmCfgURL llmCfgPhase = iota
	llmCfgKey
	llmCfgFetchModels
	llmCfgSelectModel
	llmCfgInputModel
)

type modelsFetchedMsg struct {
	models []string
	err    error
}

// LLMConfigureStep configures base_url, api_key, and model name
type LLMConfigureStep struct {
	cfg       *SetupConfig
	phase     llmCfgPhase
	urlInput  *InputModel
	keyInput  *InputModel
	modelSel  *SelectModel
	modelInp  *InputModel
	needsKey  bool
	presets   []string // preset models for this provider
	fetched   []string // models fetched from API
	done      bool
}

func NewLLMConfigureStep(cfg *SetupConfig) *LLMConfigureStep {
	step := &LLMConfigureStep{
		cfg: cfg,
	}
	return step
}

func (s *LLMConfigureStep) Title() string { return "LLM Configure" }
func (s *LLMConfigureStep) Done() bool    { return s.done }

func (s *LLMConfigureStep) Summary() string {
	if s.cfg.LLM.Provider == "none" {
		return CompletedLine("Model Configure", "Skipped")
	}
	return CompletedLine("Model Configure",
		s.cfg.LLM.Model+" · "+s.cfg.LLM.BaseURL)
}

func (s *LLMConfigureStep) Init() tea.Cmd {
	// Skip if no LLM provider selected
	if s.cfg.LLM.Provider == "none" {
		s.done = true
		return emitDone
	}

	// Find provider info for presets and needsKey
	for _, p := range AllProviders() {
		if p.Name == s.cfg.LLMProvider {
			s.needsKey = p.NeedsKey
			s.presets = p.Models
			break
		}
	}

	s.urlInput = NewInputModel("Base URL", s.cfg.LLM.BaseURL, false)
	s.keyInput = NewInputModel("API Key", "", true)
	s.phase = llmCfgURL
	return nil
}

func (s *LLMConfigureStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case llmCfgURL:
		updated, cmd := s.urlInput.Update(msg)
		s.urlInput = updated
		if s.urlInput.Done() {
			s.urlInput.done = false
			baseURL := s.urlInput.ValueOrDefault()
			// Auto-add https:// if user omitted protocol
			if baseURL != "" && !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
				baseURL = "https://" + baseURL
			}
			s.cfg.LLM.BaseURL = baseURL
			if s.needsKey {
				s.phase = llmCfgKey
			} else {
				s.phase = llmCfgFetchModels
				return s, s.fetchModels()
			}
			return s, nil
		}
		return s, cmd

	case llmCfgKey:
		updated, cmd := s.keyInput.Update(msg)
		s.keyInput = updated
		if s.keyInput.Done() {
			s.keyInput.done = false
			s.cfg.LLMApiKey = s.keyInput.Value()
			s.phase = llmCfgFetchModels
			return s, s.fetchModels()
		}
		return s, cmd

	case llmCfgFetchModels:
		if m, ok := msg.(modelsFetchedMsg); ok {
			if m.err == nil && len(m.models) > 0 {
				s.fetched = m.models
				s.buildModelSelect(m.models)
			} else if m.err == nil && len(m.models) == 0 && len(s.presets) > 0 {
				// API returned models but none passed probe — fall back to presets
				s.buildModelSelect(s.presets)
			} else if len(s.presets) > 0 {
				s.buildModelSelect(s.presets)
			} else {
				// No presets, no API models — manual input
				s.modelInp = NewInputModel("Model name", "", false)
				s.phase = llmCfgInputModel
				return s, nil
			}
			s.phase = llmCfgSelectModel
			return s, nil
		}
		return s, nil

	case llmCfgSelectModel:
		updated, cmd := s.modelSel.Update(msg)
		s.modelSel = updated
		if s.modelSel.Done() {
			selected := s.modelSel.Selected()
			if selected == "_custom" {
				s.modelInp = NewInputModel("Model name", "", false)
				s.phase = llmCfgInputModel
				return s, nil
			}
			s.cfg.LLM.Model = selected
			s.done = true
			return s, emitDone
		}
		return s, cmd

	case llmCfgInputModel:
		updated, cmd := s.modelInp.Update(msg)
		s.modelInp = updated
		if s.modelInp.Done() {
			s.cfg.LLM.Model = s.modelInp.Value()
			s.done = true
			return s, emitDone
		}
		return s, cmd
	}
	return s, nil
}

func (s *LLMConfigureStep) fetchModels() tea.Cmd {
	return func() tea.Msg {
		models, err := FetchModelsFromAPI(s.cfg.LLM.BaseURL, s.cfg.LLMApiKey)
		return modelsFetchedMsg{models: models, err: err}
	}
}

func (s *LLMConfigureStep) buildModelSelect(models []string) {
	var items []SelectItem
	for _, m := range models {
		items = append(items, SelectItem{Label: m, Value: m})
	}
	items = append(items, SelectItem{Label: "Custom (手动输入)", Value: "_custom"})
	s.modelSel = NewSelectModel("Select model", items, "")
}

func (s *LLMConfigureStep) View() string {
	var b strings.Builder

	switch s.phase {
	case llmCfgURL:
		b.WriteString(s.urlInput.View())
	case llmCfgKey:
		b.WriteString(SuccessLine("URL: " + s.cfg.LLM.BaseURL) + "\n")
		b.WriteString(s.keyInput.View())
	case llmCfgFetchModels:
		b.WriteString(SuccessLine("URL: " + s.cfg.LLM.BaseURL) + "\n")
		if s.needsKey {
			b.WriteString(SuccessLine("API Key: ••••••••") + "\n")
		}
		b.WriteString(BulletLine("Fetching available models...") + "\n")
	case llmCfgSelectModel:
		b.WriteString(SuccessLine("URL: " + s.cfg.LLM.BaseURL) + "\n")
		if s.needsKey {
			b.WriteString(SuccessLine("API Key: ••••••••") + "\n")
		}
		if len(s.fetched) > 0 {
			b.WriteString(SuccessLine("Models fetched from API") + "\n")
		} else {
			b.WriteString(BulletLine("Using preset model list") + "\n")
		}
		b.WriteString(s.modelSel.View())
	case llmCfgInputModel:
		b.WriteString(SuccessLine("URL: " + s.cfg.LLM.BaseURL) + "\n")
		if s.needsKey {
			b.WriteString(SuccessLine("API Key: ••••••••") + "\n")
		}
		b.WriteString(s.modelInp.View())
	}

	return "\n" + b.String()
}
