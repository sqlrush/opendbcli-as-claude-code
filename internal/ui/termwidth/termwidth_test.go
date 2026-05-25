/*-------------------------------------------------------------------------
 *
 * termwidth_test.go
 *	  Test cases for termwidth.go (termwidth package): TestStringWidth,
 *	  TestTruncate, TestPadRight.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/termwidth/termwidth_test.go
 *
 *-------------------------------------------------------------------------
 */
package termwidth

import "testing"

func TestStringWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"ascii", "hello", 5},
		{"cjk", "你好", 4},
		{"mixed", "hi你好", 6},
		{"ansi_bold", "\033[1mhello\033[0m", 5},
		{"ansi_color", "\033[38;2;255;0;0mred\033[0m", 3},
		{"empty", "", 0},
		{"box_drawing", "┌──┐", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringWidth(tt.input)
			if got != tt.want {
				t.Errorf("StringWidth(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		maxW  int
		want  int // expected display width of result (≤ maxW)
	}{
		{"no_truncate", "hello", 10, 5},
		{"exact", "hello", 5, 5},
		{"truncate", "hello world", 5, 5},
		{"cjk_truncate", "你好世界", 5, 4}, // 你好=4, 世=won't fit at 5
		{"with_ansi", "\033[1mhello world\033[0m", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Truncate(tt.input, tt.maxW)
			got := StringWidth(result)
			if got > tt.maxW {
				t.Errorf("Truncate(%q, %d) width = %d, exceeds max", tt.input, tt.maxW, got)
			}
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) width = %d, want %d", tt.input, tt.maxW, got, tt.want)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		input string
		width int
		want  int
	}{
		{"hi", 10, 10},
		{"你好", 10, 10},
		{"hello world", 5, 11}, // already wider, no padding
	}
	for _, tt := range tests {
		result := PadRight(tt.input, tt.width)
		got := StringWidth(result)
		if got != tt.want {
			t.Errorf("PadRight(%q, %d) width = %d, want %d", tt.input, tt.width, got, tt.want)
		}
	}
}

func TestWrapLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxW     int
		wantLen  int
		wantFits bool // each line ≤ maxW
	}{
		{"short", "hi", 10, 1, true},
		{"exact", "hello", 5, 1, true},
		{"wrap", "hello world!", 5, 3, true},
		{"ansi", "\033[1mhello world\033[0m", 5, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapLine(tt.input, tt.maxW)
			if len(result) != tt.wantLen {
				t.Errorf("WrapLine(%q, %d) = %d lines, want %d", tt.input, tt.maxW, len(result), tt.wantLen)
			}
			if tt.wantFits {
				for i, line := range result {
					w := StringWidth(line)
					if w > tt.maxW {
						t.Errorf("line %d width %d > %d: %q", i, w, tt.maxW, line)
					}
				}
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"\033[1mhello\033[0m", "hello"},
		{"\033[38;2;255;0;0mred\033[0m text", "red text"},
		{"no ansi", "no ansi"},
		{"", ""},
	}
	for _, tt := range tests {
		got := StripANSI(tt.input)
		if got != tt.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
