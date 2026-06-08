/*-------------------------------------------------------------------------
 *
 * manager.go
 *	  Manager manages model profiles and the active LLM provider.
 *	  Thread-safe for concurrent reads during diagnosis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/model/manager.go
 *
 *-------------------------------------------------------------------------
 */
package model

import (
	"fmt"
	"sort"
	"sync"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/llm/ollama"
	"github.com/sqlrush/opendb/internal/llm/openaicompat"
	"github.com/sqlrush/opendb/internal/odberr"
)

// Manager manages model profiles and the active LLM provider.
// Thread-safe for concurrent reads during diagnosis.
type Manager struct {
	mu         sync.RWMutex
	profiles   map[string]ModelProfile
	active     *ModelProfile // nil = no LLM
	provider   llm.Provider  // nil = rule-only
	modelsDir  string
	inlineSnap []InlineModel // snapshot of config.yaml `models:` for Reload merge
}

// NewManager loads all model profiles and activates activeName.
//
// Sources (merged, inline wins on name conflict):
//  1. modelsDir (~/.opendb/models/*.yaml) — legacy / shared fleet config
//  2. fallback.InlineModels (config.yaml `models:` block) — preferred
//
// Previously this was either/or (inline > dir, dir ignored if any inline
// existed) which surprised users: adding ~/.opendb/models/glm.yaml had no
// effect when config.yaml already had any inline model. Now both sources
// load, inline wins on name conflict — config.yaml is the canonical place,
// dir kept as a backward-compat path for existing installs.
//
// If activeName is empty but fallback has a provider configured, creates an
// implicit "default" profile from the fallback fields (backward compatibility).
func NewManager(modelsDir string, activeName string, fallback FallbackLLM) (*Manager, error) {
	profiles := make(map[string]ModelProfile)

	// 1. Load from models directory (legacy / fleet share path).
	if modelsDir != "" {
		dirProfiles, err := LoadProfiles(modelsDir)
		if err != nil {
			return nil, err
		}
		for name, p := range dirProfiles {
			if p.Capability == "" {
				p.Capability = InferCapability(p.Provider, p.Model)
			}
			profiles[name] = p
		}
	}

	// 2. Overlay inline models from config.yaml — these win on name conflict.
	// Fill in missing capability so historical YAML entries (configure flow
	// before fix) don't degrade to (guided + strict) by default.
	for _, im := range fallback.InlineModels {
		cap := im.Capability
		if cap == "" {
			cap = InferCapability(im.Provider, im.Model)
		}
		profiles[im.Name] = ModelProfile{
			Name:        im.Name,
			Provider:    im.Provider,
			Vendor:      im.Vendor,
			BaseURL:     im.BaseURL,
			Model:       im.Model,
			Capability:  cap,
			APIKey:      im.APIKey,
			StripThink:  im.StripThink,
			CompatMode:  im.CompatMode,
			Description: im.Description,
			ToolMode:    im.ToolMode,
			Group:       "(config)",
		}
	}

	// Backward compatibility: synthesize "default" profile from config.LLM.
	if fallback.Provider != "" && fallback.Provider != "none" {
		if _, exists := profiles["default"]; !exists {
			profiles["default"] = ModelProfile{
				Name:       "default",
				Provider:   fallback.Provider,
				BaseURL:    fallback.BaseURL,
				Model:      fallback.Model,
				Capability: fallback.Capability,
				Group:      "(config)",
			}
		}
	}

	m := &Manager{
		profiles:   profiles,
		modelsDir:  modelsDir,
		inlineSnap: append([]InlineModel(nil), fallback.InlineModels...),
	}

	// Activate the requested model.
	if activeName != "" {
		if _, err := m.switchLocked(activeName); err != nil {
			// Warn but don't fail startup — degrade to rule-only.
			// v1.1.20: use ODB error code for visibility (red banner + advice
			// + lookup via /error). Previously printed via fmt.Printf which
			// users routinely missed and assumed a crash.
			available := m.modelNames()
			advice := "用 /model 查看可用模型；config.yaml::active_model 必须等于 entry name；"
			if len(available) > 0 {
				advice += fmt.Sprintf("当前可选: %v", available)
			} else {
				advice += "当前 0 个可用模型；检查 inline models / models_dir 配置"
			}
			fmt.Printf("[%s] active_model %q 在配置中找不到，降级到规则诊断模式 — %s\n",
				odberr.ErrLLMModelNotFound, activeName, advice)
		}
	} else if fallback.Provider != "" && fallback.Provider != "none" {
		// No active_model set but LLM configured — activate "default".
		m.switchLocked("default") //nolint:errcheck
	}

	return m, nil
}

// FallbackLLM holds the legacy config.LLM fields for backward compatibility.
type FallbackLLM struct {
	Provider     string
	BaseURL      string
	Model        string
	Capability   string
	InlineModels []InlineModel // from config.yaml inline models
}

// InlineModel mirrors config.ModelConfig for passing inline models from config.
type InlineModel struct {
	Name        string
	Provider    string
	Vendor      string
	BaseURL     string
	Model       string
	Capability  string
	APIKey      string
	StripThink  bool
	CompatMode  string
	Description string
	ToolMode    string // "" / "native" / "prompt" (v1.2.0)
}

// modelNames returns sorted model entry names. Lock-free helper used during
// init for the active_model error message — caller must hold m.mu.
func (m *Manager) modelNames() []string {
	names := make([]string, 0, len(m.profiles))
	for n := range m.profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// List returns all available model profiles, sorted by name.
func (m *Manager) List() []ModelProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ModelProfile, 0, len(m.profiles))
	for _, p := range m.profiles {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Active returns the currently active profile, or nil if no LLM.
func (m *Manager) Active() *ModelProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	cp := *m.active
	return &cp
}

// ActiveName returns the name of the active model, or "" if none.
func (m *Manager) ActiveName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return ""
	}
	return m.active.Name
}

// Provider returns the current llm.Provider, or nil if no LLM.
func (m *Manager) Provider() llm.Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider
}

// Capability returns the active model's capability ("small"/"large"), or "".
func (m *Manager) Capability() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return ""
	}
	return m.active.Capability
}

// ToolMode returns the active model's tool_mode field, or "" if no active
// model. v1.2.0: callers use this to select between native FC and the
// PromptToolAdapter at provider construction time.
func (m *Manager) ToolMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return ""
	}
	return m.active.ToolMode
}

// Switch activates a named model profile and rebuilds the provider.
func (m *Manager) Switch(name string) (*ModelProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.switchLocked(name)
}

// switchLocked performs the switch while the caller holds the write lock.
func (m *Manager) switchLocked(name string) (*ModelProfile, error) {
	profile, ok := m.profiles[name]
	if !ok {
		return nil, fmt.Errorf("model %q not found", name)
	}

	provider, err := buildProvider(profile)
	if err != nil {
		return nil, fmt.Errorf("building provider for %q: %w", name, err)
	}

	m.active = &profile
	m.provider = provider
	return &profile, nil
}

// Disable deactivates the current model, switching to rule-only mode.
func (m *Manager) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = nil
	m.provider = nil
}

// Reload re-reads model profiles from modelsDir AND re-applies the inline
// snapshot captured at construction. If the previously active model still
// exists it remains active; otherwise degrades to rule-only.
//
// Inline edits to config.yaml are NOT picked up by Reload — restart required.
// Reload primarily picks up new files dropped into modelsDir.
func (m *Manager) Reload() (int, error) {
	profiles := make(map[string]ModelProfile)
	if m.modelsDir != "" {
		dirProfiles, err := LoadProfiles(m.modelsDir)
		if err != nil {
			return 0, err
		}
		for name, p := range dirProfiles {
			if p.Capability == "" {
				p.Capability = InferCapability(p.Provider, p.Model)
			}
			profiles[name] = p
		}
	}
	for _, im := range m.inlineSnap {
		cap := im.Capability
		if cap == "" {
			cap = InferCapability(im.Provider, im.Model)
		}
		profiles[im.Name] = ModelProfile{
			Name:        im.Name,
			Provider:    im.Provider,
			Vendor:      im.Vendor,
			BaseURL:     im.BaseURL,
			Model:       im.Model,
			Capability:  cap,
			APIKey:      im.APIKey,
			StripThink:  im.StripThink,
			CompatMode:  im.CompatMode,
			Description: im.Description,
			ToolMode:    im.ToolMode,
			Group:       "(config)",
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prevName := ""
	if m.active != nil {
		prevName = m.active.Name
	}

	m.profiles = profiles

	if prevName != "" {
		if _, ok := profiles[prevName]; ok {
			m.switchLocked(prevName) //nolint:errcheck
		} else {
			m.active = nil
			m.provider = nil
		}
	}

	return len(profiles), nil
}

// AddProfile injects a new model profile into the in-memory registry and
// updates the inline snapshot. Used by /model add wizard after persisting
// to config.yaml so the new model is immediately usable without restart.
func (m *Manager) AddProfile(p ModelProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles[p.Name] = p
	m.inlineSnap = append(m.inlineSnap, InlineModel{
		Name:        p.Name,
		Provider:    p.Provider,
		Vendor:      p.Vendor,
		BaseURL:     p.BaseURL,
		Model:       p.Model,
		Capability:  p.Capability,
		APIKey:      p.APIKey,
		StripThink:  p.StripThink,
		CompatMode:  p.CompatMode,
		Description: p.Description,
		ToolMode:    p.ToolMode,
	})
}

// buildProvider creates an llm.Provider from a ModelProfile.
func buildProvider(p ModelProfile) (llm.Provider, error) {
	apiKey := ExpandEnvVars(p.APIKey)

	switch p.Provider {
	case "ollama":
		return ollama.NewOllamaProvider(p.BaseURL, p.Model), nil
	case "openai":
		var opts []openaicompat.ProviderOption
		if p.StripThink {
			opts = append(opts, openaicompat.WithStripThink(true))
		}
		if p.CompatMode != "" {
			opts = append(opts, openaicompat.WithCompatMode(p.CompatMode))
		}
		return openaicompat.NewProvider(p.BaseURL, p.Model, apiKey, opts...), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: ollama, openai)", p.Provider)
	}
}
