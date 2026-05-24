/*-------------------------------------------------------------------------
 *
 * profile.go
 *	  ModelProfile defines a named LLM endpoint, analogous to a database
 *	  connection.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/model/profile.go
 *
 *-------------------------------------------------------------------------
 */
package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelProfile defines a named LLM endpoint, analogous to a database connection.
type ModelProfile struct {
	Name        string `yaml:"name"`
	Provider    string `yaml:"provider"`         // "ollama" | "openai" — implementation selector
	Vendor      string `yaml:"vendor,omitempty"` // display name: "MiniMax" | "Moonshot" | "Anthropic"
	BaseURL     string `yaml:"base_url"`
	Model       string `yaml:"model"`
	Capability  string `yaml:"capability,omitempty"`  // "small" | "large"
	APIKey      string `yaml:"api_key,omitempty"`     // supports ${ENV_VAR}
	StripThink  bool   `yaml:"strip_think,omitempty"` // strip <think>...</think> from response
	Description string `yaml:"description,omitempty"`
	// ToolMode controls how function calling is delivered to the LLM.
	// Values:
	//   "" / "native" (default): use the provider's native function-calling
	//     API (OpenAI tools parameter, Anthropic tool_use, etc.). Requires
	//     the deployment to support function calling.
	//   "prompt": inject tool descriptions into the system prompt and parse
	//     JSON tool_calls from the response (v1.2.0 PromptToolAdapter). Use
	//     this for LLMs/deployments that don't expose native FC (Qwen3 on
	//     older vLLM, gateways that strip the tools parameter, etc.).
	//   "auto": (v1.2.1+) probe once and cache.
	ToolMode string `yaml:"tool_mode,omitempty"`
	Group    string `yaml:"-"` // set from parent file
}

// DisplayVendor returns Vendor if set, otherwise falls back to Provider.
func (p ModelProfile) DisplayVendor() string {
	if p.Vendor != "" {
		return p.Vendor
	}
	return p.Provider
}

// DisplayModel returns Description if set, otherwise falls back to Model (API ID).
func (p ModelProfile) DisplayModel() string {
	if p.Description != "" {
		return p.Description
	}
	return p.Model
}

// ModelGroup is a collection of profiles loaded from one YAML file.
type ModelGroup struct {
	Group  string         `yaml:"group"`
	Models []ModelProfile `yaml:"models"`
}

// LoadProfiles scans modelsDir for YAML files and returns all named profiles.
// Returns an empty map (not nil) when the directory is empty or missing.
func LoadProfiles(modelsDir string) (map[string]ModelProfile, error) {
	profiles := make(map[string]ModelProfile)

	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return profiles, nil
		}
		return nil, fmt.Errorf("reading models directory %q: %w", modelsDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(modelsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading model file %q: %w", entry.Name(), err)
		}

		var group ModelGroup
		if err := yaml.Unmarshal(data, &group); err != nil {
			return nil, fmt.Errorf("parsing model file %q: %w", entry.Name(), err)
		}

		for _, p := range group.Models {
			if p.Name == "" {
				continue
			}
			if existing, dup := profiles[p.Name]; dup {
				return nil, fmt.Errorf("duplicate model name %q: found in group %q and %q",
					p.Name, existing.Group, group.Group)
			}
			p.Group = group.Group
			profiles[p.Name] = p
		}
	}

	return profiles, nil
}

// ExpandEnvVars replaces ${VAR} references with their environment values.
func ExpandEnvVars(s string) string {
	return os.Expand(s, os.Getenv)
}
