/*-------------------------------------------------------------------------
 *
 * skill_test.go
 *	  Test cases for skill.go (odberr package):
 *	  TestErrorSkill_ListsRegisteredCodes,
 *	  TestErrorSkill_DetailViaCodeParam,
 *	  TestErrorSkill_DetailViaArgsParam.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestErrorSkill_ListsRegisteredCodes(t *testing.T) {
	t.Parallel()
	s := NewErrorSkill()
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text, _ := res.Data.(string)
	if !strings.Contains(text, "ERR-030001") {
		t.Fatalf("list missing UI code: %s", text)
	}
	if !strings.Contains(text, "ERR-999999") {
		t.Fatalf("list missing unknown fallback: %s", text)
	}
	if !strings.Contains(text, "[UI]") {
		t.Fatalf("list missing module grouping: %s", text)
	}
}

func TestErrorSkill_DetailViaCodeParam(t *testing.T) {
	t.Parallel()
	s := NewErrorSkill()
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{
		"code": ErrUIDiagRender,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := res.Data.(string)
	if !strings.Contains(text, "ERR-030001") {
		t.Fatalf("detail missing code: %s", text)
	}
	if !strings.Contains(text, "渲染异常") {
		t.Fatalf("detail missing title: %s", text)
	}
}

func TestErrorSkill_DetailViaArgsParam(t *testing.T) {
	t.Parallel()
	// CLI dispatcher passes everything as `args`.
	s := NewErrorSkill()
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{
		"args": "err-030001", // lowercase — skill should normalize
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := res.Data.(string)
	if !strings.Contains(text, "ERR-030001") {
		t.Fatalf("detail missing code after lowercase input: %s", text)
	}
}

func TestErrorSkill_UnknownCode(t *testing.T) {
	t.Parallel()
	s := NewErrorSkill()
	res, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{
		"args": "ERR-500500",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := res.Data.(string)
	if !strings.Contains(text, "未知错误码") {
		t.Fatalf("expected unknown-code message: %s", text)
	}
}
