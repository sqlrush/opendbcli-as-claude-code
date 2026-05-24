/*-------------------------------------------------------------------------
 *
 * slowsql_test.go
 *	  Tests for /slowsql rendering.
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
)

func TestRenderOGSlowSQLFlattensMultilineSQL(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"unique_sql_id", "query", "calls", "avg_ms", "total_sec", "rows"},
		Rows: [][]any{
			{"423340056", "DO $$\nDECLARE\n  i int;\nBEGIN\n  FOR i IN 1..20 LOOP\n    PERFORM c.region;\n  END LOOP;\nEND$$;", 2, "4186833.55", "8373.67", 0},
			{"581990336", "WITH recent_orders AS (\n  SELECT o.id AS order_id, o.customer_id, o.total_amount, o.status, o.created_at\n  FROM bench_orders o\n) SELECT * FROM recent_orders", 1, "3859.48", "3.86", 0},
		},
	}

	out := renderOGSlowSQL(result, 1000)
	for _, forbidden := range []string{"\n  │ DECLARE", "\n  │   i int", "\n  │ BEGIN", "\n  │   SELECT o.id"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("/slowsql rendered raw multiline SQL fragment %q:\n%s", forbidden, out)
		}
	}
	for _, want := range []string{"SQL 摘要", "DO $$ DECLARE i int; BEGIN", "WITH recent_orders AS", "/sqltune <SQL_ID>", "/sqlfetch <SQL_ID>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/slowsql output missing %q:\n%s", want, out)
		}
	}
}

func TestOGOneLineSQLTruncatesByDisplayWidth(t *testing.T) {
	got := ogOneLineSQL("SELECT\n  *\nFROM very_long_table_name WHERE payload LIKE '%abcdef%'", 32)
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("ogOneLineSQL did not flatten whitespace: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("ogOneLineSQL should mark truncation with ellipsis: %q", got)
	}
	if format.DisplayWidth(got) > 32 {
		t.Fatalf("ogOneLineSQL width=%d, want <=32: %q", format.DisplayWidth(got), got)
	}
}
