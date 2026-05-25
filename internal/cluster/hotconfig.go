/*-------------------------------------------------------------------------
 *
 * hotconfig.go
 *	  hotconfig.go implements hot configuration reload without service
 *	  restart. Cerebrate pushes config changes via gRPC to Overlords →
 *	  Workers. Each agent applies changes via atomic in-memory swap.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/hotconfig.go
 *
 *-------------------------------------------------------------------------
 */
// hotconfig.go implements hot configuration reload without service restart.
// Cerebrate pushes config changes via gRPC to Overlords → Workers.
// Each agent applies changes via atomic in-memory swap.
package cluster

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// HotConfig holds runtime-modifiable configuration.
// These can be changed without restarting the agent.
type HotConfig struct {
	mu sync.RWMutex

	// Blacklist — dangerous operations to prohibit.
	Blacklist []string `yaml:"blacklist"`

	// LLM settings.
	LLMModel    string `yaml:"llm_model"`
	LLMBaseURL  string `yaml:"llm_base_url"`
	LLMProvider string `yaml:"llm_provider"`

	// Monitoring thresholds.
	SentinelSigma     float64 `yaml:"sentinel_sigma"`
	HeartbeatInterval int     `yaml:"heartbeat_interval_sec"` // seconds
	StatusPollInterval int    `yaml:"status_poll_interval_sec"`

	// Safety controls — tool visibility.
	ToolControls map[string]string `yaml:"tool_controls"` // tool_name → "enabled"|"confirm"|"disabled"|"hidden"
}

// NewHotConfig creates a HotConfig with defaults.
func NewHotConfig() *HotConfig {
	return &HotConfig{
		Blacklist: []string{
			"DROP TABLE", "TRUNCATE", "SHUTDOWN", "DROP DATABASE",
		},
		SentinelSigma:      3.0,
		HeartbeatInterval:  30,
		StatusPollInterval: 60,
		ToolControls:       make(map[string]string),
	}
}

// LoadHotConfig loads hot config from a YAML file.
func LoadHotConfig(path string) (*HotConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewHotConfig(), nil
		}
		return nil, fmt.Errorf("read hot config: %w", err)
	}

	hc := NewHotConfig()
	if err := yaml.Unmarshal(data, hc); err != nil {
		return nil, fmt.Errorf("parse hot config: %w", err)
	}
	return hc, nil
}

// SaveHotConfig writes hot config to a YAML file.
func SaveHotConfig(path string, hc *HotConfig) error {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	data, err := yaml.Marshal(hc)
	if err != nil {
		return fmt.Errorf("marshal hot config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Apply atomically replaces the entire hot config (used for Cerebrate push).
func (hc *HotConfig) Apply(newCfg *HotConfig) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.Blacklist = newCfg.Blacklist
	hc.LLMModel = newCfg.LLMModel
	hc.LLMBaseURL = newCfg.LLMBaseURL
	hc.LLMProvider = newCfg.LLMProvider
	hc.SentinelSigma = newCfg.SentinelSigma
	hc.HeartbeatInterval = newCfg.HeartbeatInterval
	hc.StatusPollInterval = newCfg.StatusPollInterval
	hc.ToolControls = newCfg.ToolControls
}

// AddToBlacklist adds an operation to the blacklist (append-only).
func (hc *HotConfig) AddToBlacklist(operation string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	for _, b := range hc.Blacklist {
		if b == operation {
			return // already exists
		}
	}
	hc.Blacklist = append(hc.Blacklist, operation)
}

// IsBlacklisted checks if an operation is in the blacklist.
func (hc *HotConfig) IsBlacklisted(operation string) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	for _, b := range hc.Blacklist {
		if b == operation {
			return true
		}
	}
	return false
}

// GetToolControl returns the control level for a tool.
func (hc *HotConfig) GetToolControl(toolName string) string {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if level, exists := hc.ToolControls[toolName]; exists {
		return level
	}
	return "enabled" // default
}

// SetToolControl sets the control level for a tool.
func (hc *HotConfig) SetToolControl(toolName, level string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.ToolControls[toolName] = level
}

// GetBlacklist returns a copy of the current blacklist.
func (hc *HotConfig) GetBlacklist() []string {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	result := make([]string, len(hc.Blacklist))
	copy(result, hc.Blacklist)
	return result
}
