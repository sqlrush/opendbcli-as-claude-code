/*-------------------------------------------------------------------------
 *
 * model.go
 *	  ModelSkill manages LLM model profiles: list, switch, disable,
 *	  reload.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/model.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/brand"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
)

// ModelSkill manages LLM model profiles: list, switch, disable, reload.
type ModelSkill struct {
	manager *model.Manager
}

// NewModelSkill creates a ModelSkill backed by the given ModelManager.
func NewModelSkill(manager *model.Manager) *ModelSkill {
	return &ModelSkill{manager: manager}
}

func (s *ModelSkill) Name() string                       { return "model" }
func (s *ModelSkill) Description() string                { return "List, switch, or add LLM models" }
func (s *ModelSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ModelSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "model",
		Description: "Switch or list LLM models",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Model name to switch to, 'none' to disable, or 'reload' to refresh from disk",
				},
			},
		},
	}
}

// PickerAction is the signal for REPL to open interactive model picker.
const ModelPickerAction = "model_picker"

func (s *ModelSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "model",
		Aliases:  []string{"m"},
		Usage:    "/model",
		Examples: []string{"/model"},
	}
}

func (s *ModelSkill) Validate(_ skill.Params) error { return nil }

func (s *ModelSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args == "" {
		args = strings.TrimSpace(params.StringOr("name", ""))
	}

	switch args {
	case "":
		return s.listModels()
	case "none":
		return s.disableModel()
	case "reload":
		return s.reloadModels()
	default:
		return s.switchModel(args)
	}
}

// listModels shows the current model and all available profiles.
func (s *ModelSkill) listModels() (*skill.Result, error) {
	profiles := s.manager.List()
	activeName := s.manager.ActiveName()

	var b strings.Builder

	if activeName != "" {
		b.WriteString(fmt.Sprintf("  Current model: %s\n\n", activeName))
	} else {
		b.WriteString("  Current model: (none — rule-only mode)\n\n")
	}

	if len(profiles) == 0 {
		b.WriteString("  No models configured.\n")
		b.WriteString(fmt.Sprintf("  Run '%s configure' (in another shell) to add one.\n", brand.Current().BinaryName))
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     b.String(),
			Rendered: b.String(),
			Summary:  "no models",
		}, nil
	}

	// Calculate column widths.
	nameW, vendorW, modeW := 4, 4, len("TOOL_MODE") // minimum header widths
	for _, p := range profiles {
		if len(p.Name) > nameW {
			nameW = len(p.Name)
		}
		v := p.DisplayVendor()
		if len(v) > vendorW {
			vendorW = len(v)
		}
		tm := displayToolMode(p.ToolMode)
		if len(tm) > modeW {
			modeW = len(tm)
		}
	}

	// Header.
	fmtStr := fmt.Sprintf("  %%s %%-%ds  %%-%ds  %%-%ds  %%s\n", nameW, vendorW, modeW)
	b.WriteString(fmt.Sprintf(fmtStr, " ", "NAME", "VENDOR", "TOOL_MODE", "MODEL"))

	// Rows.
	for _, p := range profiles {
		marker := " "
		if p.Name == activeName {
			marker = "▸"
		}
		b.WriteString(fmt.Sprintf(fmtStr, marker, p.Name, p.DisplayVendor(), displayToolMode(p.ToolMode), p.DisplayModel()))
	}

	b.WriteString(fmt.Sprintf("\n  Usage: /model <name> 切换  /model none 禁用\n  添加新模型: %s configure (在另一个 shell 中运行)", brand.Current().BinaryName))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     b.String(),
		Rendered: b.String(),
		Summary:  fmt.Sprintf("%d models, active=%s", len(profiles), activeName),
	}, nil
}

func displayToolMode(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return "native"
	}
	return mode
}

// switchModel activates a named model.
func (s *ModelSkill) switchModel(name string) (*skill.Result, error) {
	prevName := s.manager.ActiveName()

	profile, err := s.manager.Switch(name)
	if err != nil {
		// Show available names in the error.
		profiles := s.manager.List()
		names := make([]string, 0, len(profiles))
		for _, p := range profiles {
			names = append(names, p.Name)
		}
		msg := fmt.Sprintf("Model %q not found.", name)
		if len(names) > 0 {
			msg += fmt.Sprintf(" Available: %s", strings.Join(names, ", "))
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     msg,
			Rendered: "  " + msg,
			Summary:  "not found",
		}, nil
	}

	arrow := "(none)"
	if prevName != "" {
		arrow = prevName
	}

	text := fmt.Sprintf("  Model switched: %s → %s\n  %s | %s | %s",
		arrow, profile.Name, profile.DisplayVendor(), profile.DisplayModel(), profile.Capability)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     text,
		Rendered: text,
		Summary:  fmt.Sprintf("switched to %s", profile.Name),
	}, nil
}

// disableModel deactivates LLM, switching to rule-only mode.
func (s *ModelSkill) disableModel() (*skill.Result, error) {
	s.manager.Disable()

	text := "  LLM disabled. Diagnosis will use rule-based mode only (265 rules).\n  Use /model <name> to re-enable."

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     text,
		Rendered: text,
		Summary:  "disabled",
	}, nil
}

// reloadModels re-reads model profiles from disk.
func (s *ModelSkill) reloadModels() (*skill.Result, error) {
	count, err := s.manager.Reload()
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Data:     err.Error(),
			Rendered: "  Reload failed: " + err.Error(),
			Summary:  "reload failed",
		}, nil
	}

	activeName := s.manager.ActiveName()
	activeMsg := "(none)"
	if activeName != "" {
		activeMsg = activeName
	}

	text := fmt.Sprintf("  Reloaded %d models (config.yaml + models dir)\n  Active model: %s\n  Note: edits to config.yaml `models:` need a restart to take effect.", count, activeMsg)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     text,
		Rendered: text,
		Summary:  fmt.Sprintf("reloaded %d models", count),
	}, nil
}
