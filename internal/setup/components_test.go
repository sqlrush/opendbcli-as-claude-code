/*-------------------------------------------------------------------------
 *
 * components_test.go
 *	  Test cases for components.go (setup package):
 *	  TestSelectComponent_Navigation, TestSelectComponent_DefaultValue,
 *	  TestInputComponent_Defaults.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/components_test.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import "testing"

func TestSelectComponent_Navigation(t *testing.T) {
	items := []SelectItem{
		{Label: "Oracle", Value: "oracle"},
		{Label: "MySQL", Value: "mysql"},
		{Label: "PostgreSQL", Value: "postgres"},
	}
	s := NewSelectModel("Choose DB", items, "")
	if s.Selected() != "oracle" {
		t.Errorf("expected initial selection 'oracle', got %q", s.Selected())
	}
	s.cursor = 2
	if s.Selected() != "postgres" {
		t.Errorf("expected 'postgres' at cursor 2, got %q", s.Selected())
	}
}

func TestSelectComponent_DefaultValue(t *testing.T) {
	items := []SelectItem{
		{Label: "A", Value: "a"},
		{Label: "B", Value: "b"},
		{Label: "C", Value: "c"},
	}
	s := NewSelectModel("Pick", items, "b")
	if s.Selected() != "b" {
		t.Errorf("expected default 'b', got %q", s.Selected())
	}
}

func TestInputComponent_Defaults(t *testing.T) {
	inp := NewInputModel("Host", "127.0.0.1", false)
	if inp.Value() != "" {
		t.Errorf("expected empty initial value, got %q", inp.Value())
	}
	if inp.ValueOrDefault() != "127.0.0.1" {
		t.Errorf("expected default '127.0.0.1', got %q", inp.ValueOrDefault())
	}
}

func TestInputComponent_WithValue(t *testing.T) {
	inp := NewInputModel("Host", "127.0.0.1", false)
	inp.value = "10.0.0.1"
	if inp.ValueOrDefault() != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1', got %q", inp.ValueOrDefault())
	}
}

func TestConfirmModel_Default(t *testing.T) {
	c := NewConfirmModel("Continue?")
	if !c.Confirmed() {
		t.Error("expected default confirmed (cursor=0)")
	}
	c.cursor = 1
	if c.Confirmed() {
		t.Error("expected not confirmed at cursor=1")
	}
}
