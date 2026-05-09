/*-------------------------------------------------------------------------
 *
 * prompt_dbtype_test.go
 *	  Test cases for prompt_dbtype.go (uitest package):
 *	  TestPicker_ShowsGaussDBAndOpenGaussTypes,
 *	  TestWelcome_DBListIncludesOpenGaussAndGaussDB.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/uitest/prompt_dbtype_test.go
 *
 *-------------------------------------------------------------------------
 */
package uitest

import (
	"strings"
	"testing"
	"time"
)

// TestPicker_ShowsGaussDBAndOpenGaussTypes verifies the connection picker lists
// gaussdb-typed and opengauss-typed connections under their actual db_type
// (no brand-rewrite). v1.1.23 turned openGauss and GaussDB into distinct types;
// the picker must show them clearly so users can pick the right driver path.
func TestPicker_ShowsGaussDBAndOpenGaussTypes(t *testing.T) {
	tt := NewTestTerminal(t, 40, 130)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/login")
	// The picker may take a moment to render after the welcome page.
	time.Sleep(800 * time.Millisecond)
	screen := tt.Screen()

	// Both types should be visible under their canonical names.
	if !strings.Contains(screen, "gaussdb") {
		t.Errorf("picker missing 'gaussdb' type column entry\nscreen:\n%s", screen)
	}
	if !strings.Contains(screen, "opengauss") {
		t.Errorf("picker missing 'opengauss' type column entry\nscreen:\n%s", screen)
	}
}

// TestWelcome_DBListIncludesOpenGaussAndGaussDB verifies the welcome page banner
// lists both database flavors so users at first glance know both are supported.
func TestWelcome_DBListIncludesOpenGaussAndGaussDB(t *testing.T) {
	tt := NewTestTerminal(t, 40, 130)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	screen := tt.Screen()
	if !strings.Contains(screen, "openGauss") {
		t.Errorf("welcome DBList missing 'openGauss'\nscreen:\n%s", screen)
	}
	if !strings.Contains(screen, "GaussDB") {
		t.Errorf("welcome DBList missing 'GaussDB'\nscreen:\n%s", screen)
	}
}
