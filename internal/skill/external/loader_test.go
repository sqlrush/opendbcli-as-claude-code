package external

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

const scriptTestTimeout = 5 * time.Second

func TestManagerReloadRegistersAndExecutesScriptSkill(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "my_check")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: my_check
description: demo external skill
kind: script
db_types: [gaussdb]
security: read_only
timeout: 5s
command: ["./run.sh"]
parameters:
  type: object
  required: [target]
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
printf '{"summary":"ok","rendered":"ran:%s"}\n' "$payload"
`
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(runner), 0755); err != nil {
		t.Fatal(err)
	}

	reg := skill.NewRegistry()
	mgr := NewManager(Options{
		Enabled:        true,
		Dirs:           []string{root},
		MaxTimeout:     scriptTestTimeout,
		MaxOutputBytes: 4096,
		ContextFunc: func() RunContext {
			return RunContext{DBType: "gaussdb", Connection: "gauss_local"}
		},
	})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reg.SetActiveDB("gaussdb")
	sk, ok := reg.Get("my_check")
	if !ok {
		t.Fatalf("external skill not registered")
	}
	if sk.SecurityLevel() != skill.LevelReadOnly {
		t.Fatalf("security = %v", sk.SecurityLevel())
	}
	exec := skill.NewExecutor(reg, security.NewGuard(security.LevelDangerous, nil))
	res, err := exec.Execute(context.Background(), "my_check", skill.ParamsFromMap(map[string]any{"target": "orders"}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Rendered, "orders") || !strings.Contains(res.Rendered, "gauss_local") {
		t.Fatalf("rendered output missing payload/context: %q", res.Rendered)
	}
}

func TestManagerRejectsBuiltinConflict(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "health")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: health
description: conflict
kind: script
db_types: [all]
security: read_only
command: ["./run.sh"]
---
Conflict.
`
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	reg.Register(testSkill{name: "health"})
	mgr := NewManager(Options{Enabled: true, Dirs: []string{root}})
	if err := mgr.Reload(reg); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestExternalScriptFailureIncludesStderr(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad_check")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: bad_check
description: bad external skill
kind: script
db_types: [all]
security: read_only
command: ["./run.sh"]
---
Bad skill.
`
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho customer failure >&2\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	mgr := NewManager(Options{Enabled: true, Dirs: []string{root}, MaxTimeout: scriptTestTimeout})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	exec := skill.NewExecutor(reg, security.NewGuard(security.LevelDangerous, nil))
	_, err := exec.Execute(context.Background(), "bad_check", skill.ParamsFromMap(map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "customer failure") || !strings.Contains(err.Error(), "exit code 7") {
		t.Fatalf("expected stderr/exit code in error, got %v", err)
	}
}

func TestExternalScriptTimeout(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "slow_check")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: slow_check
description: slow external skill
kind: script
db_types: [all]
security: read_only
command: ["./run.sh"]
---
Slow skill.
`
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\nsleep 2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	mgr := NewManager(Options{Enabled: true, Dirs: []string{root}, MaxTimeout: 50 * time.Millisecond})
	if err := mgr.Reload(reg); err != nil {
		t.Fatalf("reload: %v", err)
	}
	exec := skill.NewExecutor(reg, security.NewGuard(security.LevelDangerous, nil))
	_, err := exec.Execute(context.Background(), "slow_check", skill.ParamsFromMap(map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestManagerRejectsNonExecutableScript(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "not_exec")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
api_version: opendb.skill/v1
name: not_exec
description: non executable script
kind: script
db_types: [all]
security: read_only
command: ["./run.sh"]
---
Non executable.
`
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	reg := skill.NewRegistry()
	mgr := NewManager(Options{Enabled: true, Dirs: []string{root}, MaxTimeout: scriptTestTimeout})
	if err := mgr.Reload(reg); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("expected executable error, got %v", err)
	}
}

type testSkill struct{ name string }

func (s testSkill) Name() string                       { return s.name }
func (s testSkill) Description() string                { return "test" }
func (s testSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s testSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: s.name, Parameters: map[string]any{"type": "object"}}
}
func (s testSkill) CLIDef() skill.CLIDef        { return skill.CLIDef{Command: s.name} }
func (s testSkill) Validate(skill.Params) error { return nil }
func (s testSkill) Execute(context.Context, skill.Params) (*skill.Result, error) {
	return &skill.Result{Type: skill.ResultText, Rendered: "ok"}, nil
}
