/*-------------------------------------------------------------------------
 *
 * deploy_test.go
 *	  Test cases for deploy.go (cluster package):
 *	  TestValidateInventory_Valid, TestValidateInventory_Nil,
 *	  TestValidateInventory_MissingCerebrateHost.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/deploy_test.go
 *
 *-------------------------------------------------------------------------
 */
package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateInventory_Valid(t *testing.T) {
	inv := validInventory()
	errs := ValidateInventory(inv)
	if len(errs) > 0 {
		t.Errorf("expected no errors for valid inventory, got %d: %v", len(errs), errs)
	}
}

func TestValidateInventory_Nil(t *testing.T) {
	errs := ValidateInventory(nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for nil inventory, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "nil") {
		t.Errorf("expected nil error, got %q", errs[0])
	}
}

func TestValidateInventory_MissingCerebrateHost(t *testing.T) {
	inv := validInventory()
	inv.Cerebrate.Host = ""
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "cerebrate: host is required")
}

func TestValidateInventory_MissingOverlordRegion(t *testing.T) {
	inv := validInventory()
	inv.Overlords[0].Region = ""
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "region is required")
}

func TestValidateInventory_MissingOverlordHost(t *testing.T) {
	inv := validInventory()
	inv.Overlords[1].Host = ""
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "host is required")
}

func TestValidateInventory_MissingDroneHost(t *testing.T) {
	inv := validInventory()
	inv.Drones[0].Host = ""
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "host is required")
}

func TestValidateInventory_MissingDroneOverlord(t *testing.T) {
	inv := validInventory()
	inv.Drones[0].Overlord = ""
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "overlord is required")
}

func TestValidateInventory_DuplicateHosts(t *testing.T) {
	inv := validInventory()
	inv.Drones[0].Host = inv.Overlords[0].Host // duplicate
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "duplicate host")
}

func TestValidateInventory_DuplicateCerebrateAndOverlord(t *testing.T) {
	inv := validInventory()
	inv.Overlords[0].Host = inv.Cerebrate.Host
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "duplicate host")
}

func TestValidateInventory_MissingSSHUser(t *testing.T) {
	inv := validInventory()
	inv.SSHUser = ""
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "ssh_user is required")
}

func TestValidateInventory_EmptyInventory(t *testing.T) {
	inv := &Inventory{}
	errs := ValidateInventory(inv)
	if len(errs) == 0 {
		t.Error("expected errors for empty inventory, got none")
	}
	// Should at least have cerebrate host and ssh_user errors.
	assertContainsError(t, errs, "cerebrate: host is required")
	assertContainsError(t, errs, "ssh_user is required")
}

func TestValidateInventory_WhitespaceOnlyHost(t *testing.T) {
	inv := validInventory()
	inv.Cerebrate.Host = "   "
	errs := ValidateInventory(inv)
	assertContainsError(t, errs, "cerebrate: host is required")
}

func TestValidateInventory_MultipleErrors(t *testing.T) {
	inv := &Inventory{
		Cerebrate: InventoryNode{}, // missing host
		Overlords: []InventoryNode{
			{Name: "ol-1"}, // missing host and region
		},
		Drones: []InventoryNode{
			{Name: "d-1"}, // missing host and overlord
		},
		// missing ssh_user
	}
	errs := ValidateInventory(inv)
	// cerebrate host + overlord host + overlord region + drone host + drone overlord + ssh_user = 6
	if len(errs) < 6 {
		t.Errorf("expected at least 6 errors, got %d: %v", len(errs), errs)
	}
}

func TestDryRun_ContainsAllNodes(t *testing.T) {
	inv := validInventory()
	output := DryRun(inv)

	// Check header.
	if !strings.Contains(output, "Deployment Plan (Dry Run)") {
		t.Error("missing dry run header")
	}

	// Check cerebrate.
	if !strings.Contains(output, inv.Cerebrate.Host) {
		t.Errorf("missing cerebrate host %q", inv.Cerebrate.Host)
	}

	// Check all overlords.
	for _, o := range inv.Overlords {
		if !strings.Contains(output, o.Host) {
			t.Errorf("missing overlord host %q", o.Host)
		}
		if !strings.Contains(output, o.Region) {
			t.Errorf("missing overlord region %q", o.Region)
		}
	}

	// Check all drones.
	for _, d := range inv.Drones {
		if !strings.Contains(output, d.Host) {
			t.Errorf("missing drone host %q", d.Host)
		}
	}

	// Check total count.
	expected := "Total: 9 nodes"
	if !strings.Contains(output, expected) {
		t.Errorf("missing total count, expected %q in output:\n%s", expected, output)
	}
}

func TestDryRun_Nil(t *testing.T) {
	output := DryRun(nil)
	if !strings.Contains(output, "nil") {
		t.Errorf("expected nil error message, got %q", output)
	}
}

func TestDryRun_SSHKeyShown(t *testing.T) {
	inv := validInventory()
	inv.SSHKey = "/home/deploy/.ssh/id_rsa"
	output := DryRun(inv)
	if !strings.Contains(output, inv.SSHKey) {
		t.Error("expected SSH key path in dry run output")
	}
}

func TestDryRun_Phases(t *testing.T) {
	inv := validInventory()
	output := DryRun(inv)

	phases := []string{"Phase 1: Cerebrate", "Phase 2: Overlords", "Phase 3: Drones"}
	for _, p := range phases {
		if !strings.Contains(output, p) {
			t.Errorf("missing phase %q in output", p)
		}
	}
}

func TestDryRun_RolesShown(t *testing.T) {
	inv := validInventory()
	output := DryRun(inv)

	roles := []string{"role=manager", "role=memory", "role=worker"}
	for _, r := range roles {
		if !strings.Contains(output, r) {
			t.Errorf("missing role %q in output", r)
		}
	}
}

func TestLoadInventory_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.yaml")

	yaml := `cerebrate:
  host: 10.0.0.1
  name: mgr-1
overlords:
  - host: 10.0.1.1
    name: ol-east
    region: china-east
drones:
  - host: 10.0.2.1
    name: d-1
    overlord: 10.0.1.1
    db_type: oracle
ssh_user: deploy
ssh_key: /home/deploy/.ssh/id_rsa
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	inv, err := loadInventory(path)
	if err != nil {
		t.Fatalf("loadInventory: %v", err)
	}
	if inv.Cerebrate.Host != "10.0.0.1" {
		t.Errorf("cerebrate host = %q, want 10.0.0.1", inv.Cerebrate.Host)
	}
	if len(inv.Overlords) != 1 {
		t.Errorf("overlords count = %d, want 1", len(inv.Overlords))
	}
	if len(inv.Drones) != 1 {
		t.Errorf("drones count = %d, want 1", len(inv.Drones))
	}
	if inv.SSHUser != "deploy" {
		t.Errorf("ssh_user = %q, want deploy", inv.SSHUser)
	}

	// Validate the loaded inventory should pass.
	errs := ValidateInventory(inv)
	if len(errs) > 0 {
		t.Errorf("valid inventory had errors: %v", errs)
	}
}

func TestLoadInventory_InvalidPath(t *testing.T) {
	_, err := loadInventory("/nonexistent/path/inventory.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadInventory_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadInventory(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// --- helpers ---

func validInventory() *Inventory {
	return &Inventory{
		Cerebrate: InventoryNode{
			Host:   "10.0.0.1",
			Name:   "mgr-1",
			Listen: "0.0.0.0:9100",
			Web:    "0.0.0.0:8080",
		},
		Overlords: []InventoryNode{
			{Host: "10.0.1.1", Name: "ol-east", Region: "china-east"},
			{Host: "10.0.1.2", Name: "ol-north", Region: "china-north"},
		},
		Drones: []InventoryNode{
			{Host: "10.0.2.1", Name: "d-e1", Overlord: "10.0.1.1", DBType: "oracle"},
			{Host: "10.0.2.2", Name: "d-e2", Overlord: "10.0.1.1", DBType: "oracle"},
			{Host: "10.0.2.3", Name: "d-e3", Overlord: "10.0.1.1", DBType: "mysql"},
			{Host: "10.0.3.1", Name: "d-n1", Overlord: "10.0.1.2", DBType: "oracle"},
			{Host: "10.0.3.2", Name: "d-n2", Overlord: "10.0.1.2", DBType: "mysql"},
			{Host: "10.0.3.3", Name: "d-n3", Overlord: "10.0.1.2", DBType: "oracle"},
		},
		SSHUser: "deploy",
		SSHKey:  "/home/deploy/.ssh/id_rsa",
	}
}

func assertContainsError(t *testing.T, errs []error, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return
		}
	}
	t.Errorf("expected error containing %q, got %v", substr, errs)
}
