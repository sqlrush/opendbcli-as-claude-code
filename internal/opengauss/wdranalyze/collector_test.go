package wdranalyze

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
)

func TestWDRReportQueriesPreferPgCatalogSignature(t *testing.T) {
	queries := wdrReportQueries(76, 77, "summary")
	if len(queries) < 3 {
		t.Fatalf("expected multiple WDR signature candidates, got %d", len(queries))
	}
	if !strings.Contains(queries[0], "pg_catalog.generate_wdr_report(76::bigint, 77::bigint, 'summary', 'cluster', '')") {
		t.Fatalf("first candidate should use GaussDB pg_catalog bigint/cstring signature, got: %s", queries[0])
	}
	if !strings.Contains(queries[len(queries)-1], "dbe_perf.generate_wdr_report") {
		t.Fatalf("expected legacy dbe_perf fallback, got: %v", queries)
	}
}

func TestCollectorFetchFromSnapshotsUsesCompatibleSignature(t *testing.T) {
	var calls []string
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
		calls = append(calls, sql)
		if strings.Contains(sql, "pg_catalog.generate_wdr_report") {
			return &db.QueryResult{Rows: [][]any{{"<html>wdr</html>"}}}, nil
		}
		t.Fatalf("unexpected query before pg_catalog signature: %s", sql)
		return nil, nil
	}

	raw, start, end, err := NewCollector(drv).Fetch(context.Background(), Options{
		Mode:      "snapshot",
		SnapshotA: 77,
		SnapshotB: 76,
	})
	if err != nil {
		t.Fatalf("Fetch unexpected error: %v", err)
	}
	if start != 76 || end != 77 {
		t.Fatalf("Fetch normalized snapshots to (%d, %d), want (76, 77)", start, end)
	}
	if !strings.Contains(raw, "<html>wdr</html>") {
		t.Fatalf("Fetch raw output missing WDR body: %q", raw)
	}
	if len(calls) != 1 {
		t.Fatalf("expected one query call, got %d: %v", len(calls), calls)
	}
}
