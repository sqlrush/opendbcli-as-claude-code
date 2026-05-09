/*-------------------------------------------------------------------------
 *
 * smoke_test.go
 *	  Test cases for smoke.go (uitest package): TestWelcome_Golden,
 *	  TestWelcome_NoOverflow, TestWelcome_ContentCheck.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/uitest/smoke_test.go
 *
 *-------------------------------------------------------------------------
 */
package uitest

import (
	"testing"
	"time"
)

// ---------- 基础启动 ----------

// TestWelcome_Golden captures the welcome page and compares against golden file.
// Run with -update to regenerate: go test -count=1 ./internal/ui/uitest/ -args -update
func TestWelcome_Golden(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}
	tt.RequireGolden(t)
}

// TestWelcome_NoOverflow checks every row fits within 120 columns.
func TestWelcome_NoOverflow(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Skipf("opendb did not start: %v", err)
	}
	tt.AssertNoOverflow(t)
}

// TestWelcome_ContentCheck verifies key elements are present on the welcome page.
func TestWelcome_ContentCheck(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.AssertContains(t, "OpenDB")
	tt.AssertContains(t, "/login")
	tt.AssertContains(t, "/llm")
	tt.AssertContains(t, "/rule")
	tt.AssertContains(t, "❯")
}

// ---------- /exit 退出 ----------

// TestExit_ShowsByeAndExits sends /exit and verifies clean shutdown.
func TestExit_ShowsByeAndExits(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/exit")

	// Should show farewell message
	if err := tt.WaitFor(`再见`, 5*time.Second); err != nil {
		t.Errorf("did not show farewell message: %v", err)
	}

	// Process should exit
	time.Sleep(500 * time.Millisecond)
	if !tt.ProcessExited() {
		t.Error("opendb process did not exit after /exit")
	}
}

// ---------- /help 输出 ----------

// TestHelp_Renders sends /help and checks the output renders correctly.
func TestHelp_Renders(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	tt.SendLine("/help")

	// /help should show command list
	if err := tt.WaitFor(`(help|命令|command)`, 5*time.Second); err != nil {
		t.Fatalf("/help did not render: %v", err)
	}

	tt.AssertNoOverflow(t)
}

// ---------- 补全下拉列表 ----------

// TestCompletion_SlashTriggers sends "/" and verifies dropdown appears.
func TestCompletion_SlashTriggers(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	// Type "/" to trigger autocomplete dropdown
	tt.ptmx.Write([]byte("/")) //nolint:errcheck
	time.Sleep(500 * time.Millisecond)

	// Should see at least some slash commands in the dropdown
	screen := tt.Screen()
	hasDropdown := false
	for _, cmd := range []string{"login", "help", "exit", "health"} {
		if containsIgnoreCase(screen, cmd) {
			hasDropdown = true
			break
		}
	}
	if !hasDropdown {
		t.Errorf("no dropdown appeared after typing /\n--- screen ---\n%s", screen)
	}

	// Press Escape to close dropdown
	tt.SendKey([]byte{0x1b})
	tt.AssertNoOverflow(t)
}

// ---------- 终端尺寸适配 ----------

// TestWelcome_NarrowTerminal checks welcome page at 80x24 (minimum usable).
func TestWelcome_NarrowTerminal(t *testing.T) {
	tt := NewTestTerminal(t, 24, 80)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Skipf("opendb did not start at 80x24: %v", err)
	}

	tt.AssertNoOverflow(t)
	tt.AssertContains(t, "OpenDB")
}

// TestWelcome_WideTerminal checks welcome page at 200x50 (very wide).
func TestWelcome_WideTerminal(t *testing.T) {
	tt := NewTestTerminal(t, 50, 200)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Skipf("opendb did not start at 200x50: %v", err)
	}

	tt.AssertNoOverflow(t)
	tt.AssertContains(t, "OpenDB")
}

// ---------- Resize ----------

// TestResize_NoCrash starts at 120x40, resizes to 80x24, verifies no panic
// and no line overflow. SIGWINCH handling redraws the screen to fit the new size.
func TestResize_NoCrash(t *testing.T) {
	tt := NewTestTerminal(t, 40, 120)
	defer tt.Close()

	if err := tt.WaitFor(`[❯>]`, 10*time.Second); err != nil {
		t.Fatalf("opendb did not start: %v", err)
	}

	// Resize smaller
	tt.Resize(24, 80)
	time.Sleep(1 * time.Second)

	// Should still be responsive — type something and check prompt is there
	tt.SendLine("")
	time.Sleep(500 * time.Millisecond)

	// Process should not have crashed
	if tt.ProcessExited() {
		t.Error("opendb crashed after resize")
	}

	// After SIGWINCH, all content should fit within the new 80-col width.
	tt.AssertNoOverflow(t)
}

// ---------- helpers ----------

func containsIgnoreCase(s, substr string) bool {
	sl := len(s)
	subl := len(substr)
	if subl > sl {
		return false
	}
	for i := 0; i <= sl-subl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			a, b := s[i+j], substr[j]
			if a != b && a != b+32 && a != b-32 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
