/*-------------------------------------------------------------------------
 *
 * placeholder_substituter_test.go
 *	  Unit tests for placeholder substitution rules.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/placeholder_substituter_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"
	"testing"
)

// TestSubstitute_IntervalKeyword 真机 E2E 测试发现的 bug:
// `interval ?` 默认填 `1` 触发 og EXPLAIN syntax error (interval 1 不是有效语法).
// 修复后必须填字符串字面量 '1 day'.
func TestSubstitute_IntervalKeyword(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM orders WHERE created_at >= now() - interval ?`
	out, _, err := s.Substitute(context.Background(), sql, "")
	if err != nil {
		t.Fatalf("Substitute err: %v", err)
	}
	if !strings.Contains(out, "interval '1 day'") {
		t.Errorf("interval ? should substitute to interval '1 day', got: %s", out)
	}
	if strings.Contains(out, "interval 1") && !strings.Contains(out, "interval '1") {
		t.Errorf("interval should NOT be filled with bare integer (og syntax error); got: %s", out)
	}
}

func TestSubstitute_LikeOperator(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM customers WHERE email LIKE ?`
	out, subs, err := s.Substitute(context.Background(), sql, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 sub, got %d", len(subs))
	}
	if !strings.Contains(out, "LIKE '%test%'") {
		t.Errorf("expected LIKE '%%test%%', got: %s", out)
	}
}

func TestSubstitute_RangeOnIDColumn(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM regions WHERE region_id <= ?`
	out, _, _ := s.Substitute(context.Background(), sql, "")
	if !strings.Contains(out, "region_id <= 50") {
		t.Errorf("expected region_id <= 50, got: %s", out)
	}
}

func TestSubstitute_EqualsOnIntColumn(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM orders WHERE customer_id = ?`
	out, _, _ := s.Substitute(context.Background(), sql, "")
	if !strings.Contains(out, "customer_id = 1") {
		t.Errorf("expected customer_id = 1, got: %s", out)
	}
}

func TestSubstitute_TOCharFormat(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT TO_CHAR(o.order_date, ?) FROM orders o`
	out, _, _ := s.Substitute(context.Background(), sql, "")
	if !strings.Contains(out, `TO_CHAR(o.order_date, 'YYYY-MM-DD')`) {
		t.Errorf("expected TO_CHAR with format string, got: %s", out)
	}
}

func TestSubstitute_LimitClause(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM customers LIMIT ?`
	out, _, _ := s.Substitute(context.Background(), sql, "")
	if !strings.Contains(out, "LIMIT 100") {
		t.Errorf("expected LIMIT 100, got: %s", out)
	}
}

func TestSubstitute_NoPlaceholders(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM customers WHERE id = 1`
	out, subs, _ := s.Substitute(context.Background(), sql, "")
	if len(subs) != 0 {
		t.Errorf("expected 0 subs, got %d", len(subs))
	}
	if out != sql {
		t.Errorf("expected SQL unchanged")
	}
}

func TestSubstitute_PlaceholdersInString_NotSubstituted(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM customers WHERE name = '?' AND id = ?`
	out, subs, _ := s.Substitute(context.Background(), sql, "")
	if len(subs) != 1 {
		t.Errorf("only the unquoted ? should be substituted, got %d", len(subs))
	}
	if !strings.Contains(out, `'?'`) {
		t.Errorf("the literal '?' inside quotes should be preserved: %s", out)
	}
}

func TestSubstitute_Real10TableSQL(t *testing.T) {
	// SQL_ID 2278588878 normalized form (matches what dbe_perf.statement_history returned)
	sql := `WITH region_filter AS (
    SELECT region_id FROM regions WHERE region_id <= ?
)
SELECT c.name FROM customers c, orders o
WHERE c.customer_id = o.customer_id
  AND UPPER(c.email) LIKE ?
  AND TO_CHAR(o.order_date, ?) = ?
  AND c.region_id IN (SELECT region_id FROM region_filter)`
	s := NewPlaceholderSubstituter(nil)
	out, subs, _ := s.Substitute(context.Background(), sql, "sqltune_demo")
	if len(subs) != 4 {
		t.Errorf("expected 4 subs (one for each ?), got %d", len(subs))
	}
	if strings.Contains(out, "?") {
		// Could be inside string literal which is OK; check the contexts of remaining ?
		for i := 0; i < len(out); i++ {
			if out[i] == '?' && (i == 0 || out[i-1] != '\'') {
				t.Errorf("unsubstituted bare ? at position %d in: %s", i, out)
			}
		}
	}
}

func TestSubstitute_INClause(t *testing.T) {
	s := NewPlaceholderSubstituter(nil)
	sql := `SELECT * FROM orders WHERE status IN (?, ?, ?)`
	out, subs, _ := s.Substitute(context.Background(), sql, "")
	if len(subs) != 3 {
		t.Errorf("expected 3 subs in IN, got %d", len(subs))
	}
	// All three ? should become 'test' (varchar default)
	if strings.Count(out, "'test'") < 3 {
		t.Errorf("expected 3 'test' values in IN clause, got: %s", out)
	}
}
