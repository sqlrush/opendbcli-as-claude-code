/*-------------------------------------------------------------------------
 *
 * configure.go
 *	  ConfigureModel provides a menu-driven configuration interface
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/configure.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/credential"
	"github.com/sqlrush/opendb/internal/model"
)

type configurePhase int

const (
	configureMenu configurePhase = iota
	configureAction
)

// ConfigureModel provides a menu-driven configuration interface
type ConfigureModel struct {
	cfg        *SetupConfig
	driverFunc DriverFactory
	menu       *SelectModel
	steps      []Step // multi-step flow for chosen action
	stepIdx    int
	phase      configurePhase
	quitting   bool
}

func NewConfigureModel(cfg *SetupConfig, driverFunc DriverFactory) *ConfigureModel {
	menu := NewSelectModel("What would you like to configure?", []SelectItem{
		{Label: "Add/Edit database connection", Value: "connection"},
		{Label: "Sentinel settings", Value: "sentinel"},
		{Label: "Model settings", Value: "llm"},
		{Label: "Rule Engine settings", Value: "rule"},
		{Label: "Full setup (reconfigure all)", Value: "full"},
	}, "connection")

	return &ConfigureModel{
		cfg:        cfg,
		driverFunc: driverFunc,
		menu:       menu,
	}
}

func (m *ConfigureModel) Init() tea.Cmd { return nil }

func (m *ConfigureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	}

	switch m.phase {
	case configureMenu:
		updated, cmd := m.menu.Update(msg)
		m.menu = updated
		if m.menu.Done() {
			selected := m.menu.Selected()
			if selected == "full" {
				m.quitting = true
				return m, tea.Quit
			}
			m.steps = m.stepsFor(selected)
			m.stepIdx = 0
			m.phase = configureAction
			return m, m.steps[0].Init()
		}
		return m, cmd

	case configureAction:
		switch msg := msg.(type) {
		case stepBackMsg:
			target := m.stepIdx - msg.n
			if target < 0 {
				target = 0
			}
			m.stepIdx = target
			return m, m.steps[m.stepIdx].Init()
		case stepDoneMsg:
			// Print completed step into scrollback
			fullView := m.steps[m.stepIdx].View()
			summary := m.steps[m.stepIdx].Summary()
			m.stepIdx++
			if m.stepIdx >= len(m.steps) {
				// Save connection/config if applicable
				m.saveIfNeeded()
				return m, tea.Sequence(
					tea.Println(fullView),
					tea.Println(summary),
					tea.Quit,
				)
			}
			return m, tea.Sequence(
				tea.Println(fullView),
				tea.Println(summary),
				m.steps[m.stepIdx].Init(),
			)
		default:
			if m.stepIdx >= len(m.steps) {
				return m, tea.Quit
			}
			updated, cmd := m.steps[m.stepIdx].Update(msg)
			m.steps[m.stepIdx] = updated.(Step)
			return m, cmd
		}
	}
	return m, nil
}

func (m *ConfigureModel) stepsFor(choice string) []Step {
	switch choice {
	case "connection":
		return []Step{
			NewDBTypeStep(m.cfg),
			NewConnFormStep(m.cfg),
			NewConnTestStep(m.cfg, m.driverFunc),
		}
	case "llm":
		return []Step{
			NewLLMProviderStep(m.cfg),
			NewLLMConfigureStep(m.cfg),
			NewLLMTestStep(m.cfg),
		}
	case "sentinel":
		return []Step{NewSentinelStep(m.cfg)}
	case "rule":
		return []Step{NewRuleStep(m.cfg)}
	default:
		return []Step{NewConnFormStep(m.cfg)}
	}
}

// saveIfNeeded reads config.yaml, merges new connection/model, writes back.
// If config.yaml does not exist (setup was never run), it bootstraps it first.
func (m *ConfigureModel) saveIfNeeded() {
	configPath := filepath.Join(m.cfg.BaseDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return
		}
		// config.yaml missing — bootstrap with defaults
		if bootstrapErr := GenerateConfig(m.cfg); bootstrapErr != nil {
			fmt.Printf("\n  ✗ 无法创建 config.yaml: %v\n", bootstrapErr)
			fmt.Printf("    请先运行 %s setup\n", brand.Current().BinaryName)
			return
		}
		fmt.Printf("\n  ✓ config.yaml 已自动创建\n")
		// Re-read the newly created file
		data, err = os.ReadFile(configPath)
		if err != nil {
			return
		}
	}
	var cfg config.Config
	if yaml.Unmarshal(data, &cfg) != nil {
		return
	}

	changed := false

	// Add/update connection
	conn := m.cfg.Connection
	if conn.Name != "" && conn.Host != "" {
		found := false
		for i, c := range cfg.Connections {
			if c.Name == conn.Name {
				cfg.Connections[i] = conn
				found = true
				break
			}
		}
		if !found {
			cfg.Connections = append(cfg.Connections, conn)
		}
		changed = true
		fmt.Printf("\n  ✓ Connection saved: %s\n", conn.Name)

		// Save encrypted password if auth_mode is "save"
		if conn.AuthMode == "save" && m.cfg.Password != "" {
			credDir := filepath.Join(m.cfg.BaseDir, "credentials")
			provider := credential.NewEncryptedProvider(credDir)
			if storeErr := provider.Store(conn.Name, m.cfg.Password); storeErr == nil {
				fmt.Printf("  ✓ Credential saved (encrypted)\n")
			}
		}
	}

	// Add/update model — append to models[] only.
	//
	// Previously this also did `cfg.LLM = m.cfg.LLM`, which overwrote the
	// top-level legacy `llm:` block with whatever model was just added.
	// Result: configure adding glm-5.1 silently changed `llm.model` from
	// deepseek-v4-pro to glm-5.1, while `active_model` still pointed at
	// deepseek — config looked self-contradictory. The legacy `llm:` block
	// is only used as a fallback when no `default` entry exists in models[],
	// so configure should leave it alone and just grow the models[] array.
	if m.cfg.LLM.Provider != "" && m.cfg.LLM.Provider != "none" && m.cfg.LLM.Model != "" {
		provider := "openai"
		if m.cfg.LLM.Provider == "ollama" {
			provider = "ollama"
		}
		newModel := config.ModelConfig{
			Name:     m.cfg.LLM.Model,
			Provider: provider,
			Vendor:   m.cfg.LLMVendor,
			BaseURL:  m.cfg.LLM.BaseURL,
			Model:    m.cfg.LLM.Model,
			// Capability previously omitted here — models added via configure
			// landed in YAML with empty capability, then runtime saw "" and
			// fell to (guided strategy + strict prompt), the worst-of-both
			// combo. Infer it the same way setup wizard does.
			Capability: model.InferCapability(provider, m.cfg.LLM.Model),
			APIKey:     m.cfg.LLMApiKey,
		}
		found := false
		for i, mod := range cfg.Models {
			if mod.Name == newModel.Name {
				newModel.ToolMode = mod.ToolMode
				cfg.Models[i] = newModel
				found = true
				break
			}
		}
		if !found {
			cfg.Models = append(cfg.Models, newModel)
		}
		changed = true
		fmt.Printf("  ✓ Model saved: %s (capability: %s)\n", newModel.Name, newModel.Capability)
	}

	// Write back
	if changed {
		if newData, marshalErr := yaml.Marshal(cfg); marshalErr == nil {
			os.WriteFile(configPath, newData, 0o640)
		}
	}
}

func (m *ConfigureModel) View() string {
	if m.quitting {
		return "\n  Configuration cancelled.\n\n"
	}
	switch m.phase {
	case configureMenu:
		return "\n" + m.menu.View()
	case configureAction:
		if m.stepIdx < len(m.steps) {
			return m.steps[m.stepIdx].View()
		}
	}
	return ""
}
