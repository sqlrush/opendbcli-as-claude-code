package sqltuner

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	dbmock "github.com/sqlrush/opendb/internal/db/mock"
)

func TestCollectIndexesUsesOpenGaussCompatibleIndkeyQuery(t *testing.T) {
	var seenSQL string
	driver := dbmock.NewMockDriver()
	driver.QueryFunc = func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error) {
		seenSQL = sql
		return &db.QueryResult{
			Rows: [][]any{{
				"bench_orders",
				"bench_orders_pkey",
				true,
				true,
				"{id}",
				"CREATE UNIQUE INDEX bench_orders_pkey ON bench_orders USING btree (id)",
			}},
		}, nil
	}

	info := &SchemaInfo{Indexes: map[string][]IndexInfo{}}
	if err := NewSchemaCollector(driver).collectIndexes(context.Background(), []string{"bench_orders"}, info); err != nil {
		t.Fatalf("collectIndexes failed: %v", err)
	}

	if strings.Contains(strings.ToLower(seenSQL), "with ordinality") {
		t.Fatalf("openGauss index query must not use WITH ORDINALITY:\n%s", seenSQL)
	}
	if !strings.Contains(strings.ToLower(seenSQL), "any(ix.indkey)") {
		t.Fatalf("openGauss index query should use ANY(ix.indkey):\n%s", seenSQL)
	}
	if got := info.Indexes["bench_orders"]; len(got) != 1 || got[0].Name != "bench_orders_pkey" {
		t.Fatalf("unexpected collected indexes: %#v", got)
	}
}
