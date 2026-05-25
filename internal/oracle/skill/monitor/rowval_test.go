/*-------------------------------------------------------------------------
 *
 * rowval_test.go
 *	  Test cases for rowval.go (monitor package): TestRowStr,
 *	  TestRowFloat, TestRowInt.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/rowval_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"testing"
)

func TestRowStr(t *testing.T) {
	row := []any{"hello", 42, nil, 3.14}
	if rowStr(row, 0) != "hello" {
		t.Errorf("rowStr string = %q, want hello", rowStr(row, 0))
	}
	if rowStr(row, 1) != "42" {
		t.Errorf("rowStr int = %q, want 42", rowStr(row, 1))
	}
	if rowStr(row, 2) != "" {
		t.Errorf("rowStr nil = %q, want empty", rowStr(row, 2))
	}
	if rowStr(row, 99) != "" {
		t.Errorf("rowStr out of bounds = %q, want empty", rowStr(row, 99))
	}
}

func TestRowFloat(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want float64
	}{
		{"float64", float64(3.14), 3.14},
		{"int64", int64(42), 42.0},
		{"int", 100, 100.0},
		{"string_int", "2048", 2048.0},
		{"string_float", "303.8", 303.8},
		{"string_negative", "-1.5", -1.5},
		{"string_spaces", "  512  ", 512.0},
		{"string_invalid", "abc", 0},
		{"nil", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := []any{tc.val}
			got := rowFloat(row, 0)
			if got != tc.want {
				t.Errorf("rowFloat(%v) = %f, want %f", tc.val, got, tc.want)
			}
		})
	}
}

func TestRowInt(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		{"int", 42, 42},
		{"int64", int64(100), 100},
		{"float64", float64(3.7), 3},
		{"string_int", "2048", 2048},
		{"string_float", "3.0", 3},
		{"string_negative", "-5", -5},
		{"string_invalid", "abc", 0},
		{"nil", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := []any{tc.val}
			got := rowInt(row, 0)
			if got != tc.want {
				t.Errorf("rowInt(%v) = %d, want %d", tc.val, got, tc.want)
			}
		})
	}
}

func TestRowFloat_OutOfBounds(t *testing.T) {
	row := []any{"hello"}
	if rowFloat(row, 5) != 0 {
		t.Error("out of bounds should return 0")
	}
	if rowFloat(nil, 0) != 0 {
		t.Error("nil row should return 0")
	}
}
