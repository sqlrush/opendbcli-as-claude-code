/*-------------------------------------------------------------------------
 *
 * loader_test.go
 *	  Test cases for loader.go (policy package): TestLoader_EmptyDir,
 *	  TestLoader_OrgOnly, TestLoader_InstanceOnly.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/policy/loader_test.go
 *
 *-------------------------------------------------------------------------
 */
package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoader_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir)

	p := loader.Load("platform rules", "mysql-prod", "")
	if p.Merged != "" {
		t.Error("expected empty merged for no policy files")
	}
	if p.Platform != "platform rules" {
		t.Error("platform not preserved")
	}
}

func TestLoader_OrgOnly(t *testing.T) {
	dir := t.TempDir()
	orgDir := filepath.Join(dir, "org")
	os.MkdirAll(orgDir, 0750)
	os.WriteFile(filepath.Join(orgDir, "safety.md"), []byte("禁止直接执行 DDL"), 0640)

	loader := NewLoader(dir)
	p := loader.Load("", "mysql-prod", "")

	if p.Org == "" {
		t.Fatal("org should not be empty")
	}
	if !strings.Contains(p.Merged, "组织规范") {
		t.Error("merged should contain org section header")
	}
	if !strings.Contains(p.Merged, "禁止直接执行 DDL") {
		t.Error("merged should contain org content")
	}
}

func TestLoader_InstanceOnly(t *testing.T) {
	dir := t.TempDir()
	instDir := filepath.Join(dir, "mysql-prod")
	os.MkdirAll(instDir, 0750)
	os.WriteFile(filepath.Join(instDir, "strict.md"), []byte("禁止 kill session"), 0640)

	loader := NewLoader(dir)
	p := loader.Load("", "mysql-prod", "")

	if p.Instance == "" {
		t.Fatal("instance should not be empty")
	}
	if !strings.Contains(p.Merged, "实例规范") {
		t.Error("merged should contain instance section header")
	}
}

func TestLoader_AllLevels(t *testing.T) {
	dir := t.TempDir()

	// Org
	orgDir := filepath.Join(dir, "org")
	os.MkdirAll(orgDir, 0750)
	os.WriteFile(filepath.Join(orgDir, "safety.md"), []byte("所有诊断必须附风险评估"), 0640)

	// Instance
	instDir := filepath.Join(dir, "mysql-prod")
	os.MkdirAll(instDir, 0750)
	os.WriteFile(filepath.Join(instDir, "strict.md"), []byte("维护窗口: 每周日 02:00-06:00"), 0640)

	loader := NewLoader(dir)
	p := loader.Load("platform", "mysql-prod", "只关注空间问题")

	// All three levels should be present
	if !strings.Contains(p.Merged, "组织规范") {
		t.Error("missing org section")
	}
	if !strings.Contains(p.Merged, "实例规范") {
		t.Error("missing instance section")
	}
	if !strings.Contains(p.Merged, "本次会话规范") {
		t.Error("missing session section")
	}

	// Priority declaration should be present
	if !strings.Contains(p.Merged, "优先级：组织规范 > 实例规范 > 会话规范") {
		t.Error("missing priority declaration")
	}
}

func TestLoader_AlphabeticalOrder(t *testing.T) {
	dir := t.TempDir()
	orgDir := filepath.Join(dir, "org")
	os.MkdirAll(orgDir, 0750)
	os.WriteFile(filepath.Join(orgDir, "b_second.md"), []byte("SECOND"), 0640)
	os.WriteFile(filepath.Join(orgDir, "a_first.md"), []byte("FIRST"), 0640)

	loader := NewLoader(dir)
	p := loader.Load("", "test", "")

	firstIdx := strings.Index(p.Org, "FIRST")
	secondIdx := strings.Index(p.Org, "SECOND")
	if firstIdx < 0 || secondIdx < 0 {
		t.Fatal("both files should be loaded")
	}
	if firstIdx > secondIdx {
		t.Error("files should be loaded in alphabetical order (a_ before b_)")
	}
}

func TestLoader_SessionOnly(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir)

	p := loader.Load("", "mysql-prod", "只看 CPU 问题")
	if !strings.Contains(p.Merged, "本次会话规范") {
		t.Error("session policy should be in merged")
	}
	if !strings.Contains(p.Merged, "只看 CPU 问题") {
		t.Error("session content missing")
	}
}

func TestLoader_NoPoliciesDir(t *testing.T) {
	loader := NewLoader("")
	p := loader.Load("platform", "mysql-prod", "")

	if p.Merged != "" {
		t.Error("should be empty when policiesDir is empty")
	}
}
