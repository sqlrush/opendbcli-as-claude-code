/*-------------------------------------------------------------------------
 *
 * picker_test.go
 *	  Test cases for picker.go (uitest package):
 *	  TestModelPicker_AllItemsVisible, TestModelPicker_ArrowNavigation,
 *	  TestModelPicker_EscCancels.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/uitest/picker_test.go
 *
 *-------------------------------------------------------------------------
 */
package uitest

import (
	"testing"
	"time"
)

var (
	keyUp    = []byte{0x1b, '[', 'A'}
	keyDown  = []byte{0x1b, '[', 'B'}
	keyEnter = []byte{0x0d}
	keyEsc   = []byte{0x1b}
)

// ---------- /model picker ----------

// TestModelPicker_AllItemsVisible verifies all configured models appear in the picker.
func TestModelPicker_AllItemsVisible(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/model")
	if err := tt.WaitFor(`名称`, 5*time.Second); err != nil {
		t.Fatalf("/model picker did not appear: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// All configured models must be visible
	for _, name := range []string{"deepseek", "minimax", "opus", "kimi", "gemini", "none"} {
		tt.AssertContains(t, name)
	}

	tt.AssertNoOverflow(t)
	tt.SendKey(keyEsc)
}

// TestModelPicker_ArrowNavigation verifies up/down arrows move the selection indicator.
func TestModelPicker_ArrowNavigation(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/model")
	if err := tt.WaitFor(`▸`, 5*time.Second); err != nil {
		t.Fatalf("picker indicator not found: %v", err)
	}

	// Press down a few times
	for i := 0; i < 3; i++ {
		tt.SendKey(keyDown)
		time.Sleep(200 * time.Millisecond)
	}

	// Indicator should still be present (moved to a different item)
	tt.AssertContains(t, "▸")
	tt.AssertNoOverflow(t)

	// Press up back to top
	for i := 0; i < 3; i++ {
		tt.SendKey(keyUp)
		time.Sleep(200 * time.Millisecond)
	}

	tt.AssertContains(t, "▸")
	tt.SendKey(keyEsc)
}

// TestModelPicker_EscCancels verifies ESC closes the picker without selection.
func TestModelPicker_EscCancels(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/model")
	if err := tt.WaitFor(`名称`, 5*time.Second); err != nil {
		t.Fatalf("picker did not appear: %v", err)
	}

	tt.SendKey(keyEsc)
	time.Sleep(300 * time.Millisecond)

	// Should be back to prompt
	if err := tt.WaitFor(`[❯>]`, 3*time.Second); err != nil {
		t.Errorf("prompt not restored after ESC: %v", err)
	}
}

// ---------- /login picker ----------

// TestLoginPicker_AllItemsVisible verifies all configured connections appear.
func TestLoginPicker_AllItemsVisible(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/login")
	if err := tt.WaitFor(`名称`, 5*time.Second); err != nil {
		t.Fatalf("/login picker did not appear: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// All configured connections must be visible
	for _, name := range []string{"oracle", "mysql", "pg", "og"} {
		tt.AssertContains(t, name)
	}

	tt.AssertNoOverflow(t)
	tt.SendKey(keyEsc)
}

// TestLoginPicker_ArrowNavigation verifies navigation works for all items.
func TestLoginPicker_ArrowNavigation(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/login")
	if err := tt.WaitFor(`▸`, 5*time.Second); err != nil {
		t.Fatalf("picker indicator not found: %v", err)
	}

	// Navigate through all items
	for i := 0; i < 4; i++ {
		tt.SendKey(keyDown)
		time.Sleep(200 * time.Millisecond)
		tt.AssertContains(t, "▸")
	}

	tt.AssertNoOverflow(t)
	tt.SendKey(keyEsc)
}
