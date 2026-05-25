package shared

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/external"
)

func TestSkillsSkillListShowAndReload(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFixture(t, root, "my_check")
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})

	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "reload"}))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !strings.Contains(res.Rendered, "1 个") {
		t.Fatalf("reload output = %q", res.Rendered)
	}

	res, err = s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "list"}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(res.Rendered, "my_check") || !strings.Contains(res.Rendered, "read_only") {
		t.Fatalf("list output = %q", res.Rendered)
	}

	res, err = s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "show my_check"}))
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(res.Rendered, "Path") || !strings.Contains(res.Rendered, "my_check") {
		t.Fatalf("show output = %q", res.Rendered)
	}
}

func TestSkillsSkillInitCreatesTemplate(t *testing.T) {
	root := t.TempDir()
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "init custom_check"}))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(res.Rendered, "custom_check") {
		t.Fatalf("init output = %q", res.Rendered)
	}
	for _, name := range []string{"skill.md", "run.sh"} {
		if _, err := os.Stat(filepath.Join(root, "custom_check", name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
}

func TestSkillsSkillRunReadOnlyExternalSkill(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFixture(t, root, "my_check")
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reg.SetActiveDB("gaussdb")
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": `run my_check {"target":"orders"}`}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Rendered, "orders") {
		t.Fatalf("run output = %q", res.Rendered)
	}
}

func TestSkillsSkillInitGeneratedTemplateCanReloadAndRun(t *testing.T) {
	root := t.TempDir()
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})
	if _, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "init generated_check"})); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "reload"})); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reg.SetActiveDB("gaussdb")
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": `run generated_check {"args":"hello"}`}))
	if err != nil {
		t.Fatalf("run generated template: %v", err)
	}
	if !strings.Contains(res.Rendered, "外部 skill 已执行") || !strings.Contains(res.Rendered, "generated_check") {
		t.Fatalf("generated template output = %q", res.Rendered)
	}
}

func TestSkillsSkillRunDBTypeMismatchGivesClearHint(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFixture(t, root, "gauss_only")
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reg.SetActiveDB("mysql")
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": `run gauss_only {"target":"orders"}`}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Rendered, "当前连接未匹配") || !strings.Contains(res.Rendered, "gaussdb") {
		t.Fatalf("mismatch output = %q", res.Rendered)
	}
}

func TestSkillsSkillOutputDoesNotOverflowTerminalWidth(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFixtureWithLongMetadata(t, root, "long_skill_for_ui")
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})

	for _, args := range []string{"list", "show long_skill_for_ui"} {
		res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": args}))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		assertRenderedWidth(t, args, res.Rendered, 120)
	}
}

func assertRenderedWidth(t *testing.T, name, rendered string, maxW int) {
	t.Helper()
	for i, line := range strings.Split(rendered, "\n") {
		if w := format.DisplayWidth(line); w > maxW {
			t.Fatalf("%s line %d overflows: width=%d max=%d line=%q\n%s", name, i+1, w, maxW, line, rendered)
		}
	}
}

func TestHelpListsLoadedExternalSkill(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFixture(t, root, "my_check")
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	reg.Register(NewHelpSkill(reg))
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reg.SetActiveDB("gaussdb")
	h := NewHelpSkill(reg)
	res, err := h.Execute(context.Background(), skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(res.Rendered, "my_check") || !strings.Contains(res.Rendered, "demo external skill") {
		t.Fatalf("help output missing external skill: %q", res.Rendered)
	}
}

func TestSkillsRunRejectsNonReadOnlyExternalSkill(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFixtureWithSecurity(t, root, "ops_check", "operator")
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{root}, MaxTimeout: time.Second})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reg.SetActiveDB("gaussdb")
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{})
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": `run ops_check {}`}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Rendered, "只允许直接运行 read_only") {
		t.Fatalf("operator run output = %q", res.Rendered)
	}
}

func TestSkillsDoctorShowsMCPAllowlist(t *testing.T) {
	reg := skill.NewRegistry()
	mgr := external.NewManager(external.Options{Enabled: true, Dirs: []string{t.TempDir()}, MaxTimeout: time.Second})
	s := NewSkillsSkill(reg, mgr, config.MCPConfig{Enabled: true, Servers: []config.MCPServerConfig{{
		Name:    "customer-mcp",
		Command: []string{"python3", "server.py"},
		Tools: map[string]config.MCPToolAllowEntry{
			"inspect_backup": {Enabled: true, Name: "mcp_inspect_backup", Description: "inspect backups", Security: "read_only", DBTypes: []string{"gaussdb"}},
		},
	}}})
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "doctor"}))
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(res.Rendered, "customer-mcp") || !strings.Contains(res.Rendered, "mcp_inspect_backup") {
		t.Fatalf("doctor output = %q", res.Rendered)
	}
}

func writeExternalSkillFixture(t *testing.T, root, name string) {
	t.Helper()
	writeExternalSkillFixtureWithSecurity(t, root, name, "read_only")
}

func writeExternalSkillFixtureWithSecurity(t *testing.T, root, name, security string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: ` + name + `
description: demo external skill
kind: script
db_types: [gaussdb]
security: ` + security + `
timeout: 5s
command: ["./run.sh"]
parameters:
  type: object
  properties:
    target:
      type: string
---
Demo skill.
`
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	runner := `#!/bin/sh
set -eu
payload=$(cat)
printf 'payload:%s\n' "$payload"
`
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(runner), 0755); err != nil {
		t.Fatal(err)
	}
}

func writeExternalSkillFixtureWithLongMetadata(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: ` + name + `
title: Very Long External Skill Title For Terminal Rendering Guard
description: Read-only table bloat, stale statistics, sequential scan, unused-index, wait-chain, lock-chain, and plan-hotspot advisor for OpenGauss and GaussDB customer sandboxes
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 45s
command: ["./run.sh"]
triggers:
  - explain plan hotspot and row estimate mismatch in a very long customer sentence
  - 执行计划代价热点、行数估算偏差、顺序扫描热点、Nested Loop 热点、Hash Join 风险
tags: [performance, explain, sqltune, readonly, customer-extension]
parameters:
  type: object
  properties:
    target:
      type: string
---
Demo skill with long metadata.
`
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	runner := `#!/bin/sh
set -eu
payload=$(cat)
printf 'payload:%s\n' "$payload"
`
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(runner), 0755); err != nil {
		t.Fatal(err)
	}
}
