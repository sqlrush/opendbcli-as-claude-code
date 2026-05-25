package external

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCommandAllowsInterpreterScriptInsideSkillDir(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "run.py"), []byte("print('ok')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd, args, err := resolveCommand(dir, CommandSpec{"python3", "scripts/run.py"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cmd != "python3" || len(args) != 1 || args[0] != "scripts/run.py" {
		t.Fatalf("unexpected command: %q %#v", cmd, args)
	}
}

func TestResolveCommandRejectsStaticArgEscapingSkillDir(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveCommand(dir, CommandSpec{"python3", "../run.py"})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escape error, got %v", err)
	}
}

func TestResolveCommandRejectsAbsoluteStaticArg(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveCommand(dir, CommandSpec{"python3", "/tmp/run.py"})
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}
