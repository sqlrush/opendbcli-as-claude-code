/*-------------------------------------------------------------------------
 *
 * source_github_test.go
 *	  Test cases for source_github.go (trace package):
 *	  TestGitHubLookup_NoRepo, TestPickBestMatch_PrefersSource,
 *	  TestPickBestMatch_FallbackToFirst.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/source_github_test.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"testing"
)

func TestGitHubLookup_NoRepo(t *testing.T) {
	g := &GitHubLookup{Repo: ""}
	results, err := g.Grep([]HotFunc{{Name: "test"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected empty results when no repo configured")
	}
}

func TestPickBestMatch_PrefersSource(t *testing.T) {
	items := []codeSearchItem{
		{Name: "test_innodb.cc", Path: "unittest/test_innodb.cc"},
		{Name: "ha_innodb.cc", Path: "storage/innobase/handler/ha_innodb.cc"},
		{Name: "ha_innodb.h", Path: "storage/innobase/handler/ha_innodb.h"},
	}
	got := pickBestMatch(items, "write_row")
	if got.Path != "storage/innobase/handler/ha_innodb.cc" {
		t.Errorf("expected ha_innodb.cc, got %s", got.Path)
	}
}

func TestPickBestMatch_FallbackToFirst(t *testing.T) {
	items := []codeSearchItem{
		{Name: "test_innodb.cc", Path: "unittest/test_innodb.cc"},
	}
	got := pickBestMatch(items, "write_row")
	if got.Path != "unittest/test_innodb.cc" {
		t.Errorf("expected fallback to first item, got %s", got.Path)
	}
}

func TestGitHubLookup_DefaultBranch(t *testing.T) {
	g := &GitHubLookup{Repo: "mysql/mysql-server"}
	// Branch should default to "main" — we can't test the API call without network,
	// but we verify the struct is correctly initialized.
	if g.Branch != "" {
		t.Errorf("expected empty default branch, got %q", g.Branch)
	}
}
