package external

import (
	"strings"
	"testing"
)

func TestCommandSpecScalarSplitsWithoutShell(t *testing.T) {
	parts, err := splitCommandLine(`python3 scripts/run.py --name "bench orders" 'literal value'`)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	want := []string{"python3", "scripts/run.py", "--name", "bench orders", "literal value"}
	if len(parts) != len(want) {
		t.Fatalf("len=%d want=%d: %#v", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("parts[%d]=%q want %q (all=%#v)", i, parts[i], want[i], parts)
		}
	}
}

func TestCommandSpecScalarRejectsUnterminatedQuote(t *testing.T) {
	if _, err := splitCommandLine(`python3 "run.py`); err == nil {
		t.Fatal("expected unterminated quote error")
	}
}

func TestManifestNormalizesWildcardDBTypes(t *testing.T) {
	m := &Manifest{Name: "my_check", Description: "desc", Kind: "script", DBTypes: []string{"*", "GaussDB"}, Security: "read_only", Command: CommandSpec{"./run.sh"}}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.DBTypes[0] != "all" || m.DBTypes[1] != "gaussdb" {
		t.Fatalf("db types = %#v", m.DBTypes)
	}
}

func TestExternalSkillToolDescriptionIncludesTriggersAndBody(t *testing.T) {
	m := &Manifest{Name: "my_check", Description: "short desc", Kind: "script", DBTypes: []string{"all"}, Security: "read_only", Command: CommandSpec{"sh"}, Triggers: []string{"customer latency"}, Body: "Longer runbook guidance."}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	sk, err := NewExternalScriptSkill(m, Options{})
	if err != nil {
		t.Fatalf("new skill: %v", err)
	}
	desc := sk.ToolDef().Description
	for _, want := range []string{"short desc", "customer latency", "Longer runbook guidance"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %q", want, desc)
		}
	}
}
