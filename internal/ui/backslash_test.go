package ui

import (
	"strings"
	"testing"
)

func TestRewriteBackslashCmdSupportsGaussDB(t *testing.T) {
	got := rewriteBackslashCmd(`\dt`, "gaussdb")
	if got == `\dt` || !strings.Contains(got, "pg_tables") {
		t.Fatalf("expected gaussdb backslash command to rewrite like opengauss, got %q", got)
	}
}
