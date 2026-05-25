/*-------------------------------------------------------------------------
 *
 * fingerprint_test.go
 *	  Unit tests for SQL fingerprinting and similarity scoring.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/fingerprint_test.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import "testing"

func TestComputeFingerprint_BasicTables(t *testing.T) {
	sql := `SELECT * FROM customers c JOIN orders o ON c.id = o.customer_id`
	fp := ComputeFingerprint(sql)
	if fp.Hash == "" {
		t.Fatal("hash should not be empty")
	}
	if !containsTable(fp.Tables, "customers") || !containsTable(fp.Tables, "orders") {
		t.Errorf("expected customers + orders in tables, got %v", fp.Tables)
	}
	if fp.HasCTE {
		t.Errorf("HasCTE should be false")
	}
}

func TestComputeFingerprint_StripsSchema(t *testing.T) {
	sql := `SELECT * FROM sqltune_demo.customers c, public.orders o WHERE c.id = o.customer_id`
	fp := ComputeFingerprint(sql)
	for _, table := range fp.Tables {
		if table == "sqltune_demo.customers" || table == "public.orders" {
			t.Errorf("expected schema-stripped tables, got %v", fp.Tables)
		}
	}
	if !containsTable(fp.Tables, "customers") || !containsTable(fp.Tables, "orders") {
		t.Errorf("expected customers + orders, got %v", fp.Tables)
	}
}

func TestComputeFingerprint_SameSQLDifferentLiterals(t *testing.T) {
	sqlA := `SELECT * FROM customers WHERE email = 'a@gmail.com' AND id > 100`
	sqlB := `SELECT * FROM customers WHERE email = 'b@yahoo.com' AND id > 200`
	fpA := ComputeFingerprint(sqlA)
	fpB := ComputeFingerprint(sqlB)
	if fpA.Hash != fpB.Hash {
		t.Errorf("same SQL with different literals should produce same hash:\n  A: %s\n  B: %s", fpA.Hash, fpB.Hash)
	}
}

func TestComputeFingerprint_DifferentTables(t *testing.T) {
	sqlA := `SELECT * FROM customers WHERE id = 1`
	sqlB := `SELECT * FROM orders WHERE id = 1`
	fpA := ComputeFingerprint(sqlA)
	fpB := ComputeFingerprint(sqlB)
	if fpA.Hash == fpB.Hash {
		t.Errorf("different tables should produce different hash")
	}
}

func TestComputeFingerprint_DetectsCTE(t *testing.T) {
	sql := `WITH foo AS (SELECT 1) SELECT * FROM foo`
	fp := ComputeFingerprint(sql)
	if !fp.HasCTE {
		t.Error("HasCTE should be true for WITH clause")
	}
}

func TestComputeFingerprint_RealWorldComplexSQL(t *testing.T) {
	// The 10-table SQL_ID 2278588878 from the implementation context
	sql := `WITH region_filter AS (SELECT region_id FROM regions WHERE region_id <= 50)
SELECT c.name, p.product_name, su.country
FROM customers c, orders o, order_items oi, products p, regions r,
     suppliers su, categories cat, payments pm
WHERE c.customer_id = o.customer_id
  AND o.order_id = oi.order_id
  AND oi.product_id = p.product_id
  AND c.region_id = r.region_id
  AND p.supplier_id = su.supplier_id
  AND p.category_id = cat.category_id
  AND o.order_id = pm.order_id`
	fp := ComputeFingerprint(sql)
	expected := []string{"customers", "orders", "order_items", "products", "regions", "suppliers", "categories", "payments"}
	for _, e := range expected {
		if !containsTable(fp.Tables, e) {
			t.Errorf("expected %s in tables, got %v", e, fp.Tables)
		}
	}
	if !fp.HasCTE {
		t.Error("HasCTE should be true")
	}
}

func TestSimilarityScore_ExactMatch(t *testing.T) {
	sql := `SELECT * FROM customers c JOIN orders o ON c.id = o.customer_id`
	fpA := ComputeFingerprint(sql)
	fpB := ComputeFingerprint(sql)
	if score := fpA.SimilarityScore(fpB); score != 1.0 {
		t.Errorf("exact match should be 1.0, got %f", score)
	}
}

func TestSimilarityScore_PreventsCrossSQLPollution(t *testing.T) {
	// This is the actual production failure: SQL_ID 33402943 (5 tables) memory
	// was being recalled when querying SQL_ID 2278588878 (10 tables).
	sql5Table := `SELECT c.name FROM customers c, orders o, order_items oi, products p
                  WHERE c.customer_id = o.customer_id
                    AND o.order_id = oi.order_id
                    AND oi.product_id = p.product_id`
	sql10Table := `WITH region_filter AS (SELECT region_id FROM regions)
                   SELECT c.name FROM customers c, orders o, order_items oi, products p,
                                      regions r, suppliers su, categories cat, payments pm
                   WHERE c.customer_id = o.customer_id`
	fp5 := ComputeFingerprint(sql5Table)
	fp10 := ComputeFingerprint(sql10Table)
	score := fp5.SimilarityScore(fp10)
	if score >= SimilarityThreshold {
		t.Errorf("5-table vs 10-table should be below threshold (%f), got %f", SimilarityThreshold, score)
	}
	// But it shouldn't be 0 either — they DO share 4 tables
	if score == 0 {
		t.Error("should still have some Jaccard overlap")
	}
	t.Logf("5-table vs 10-table similarity: %.3f (threshold: %.2f)", score, SimilarityThreshold)
}

func TestSimilarityScore_SameSQLPassesThreshold(t *testing.T) {
	// Same SQL with only literal value differences should easily pass threshold.
	sqlA := `SELECT * FROM customers WHERE email LIKE '%gmail.com' AND age > 18`
	sqlB := `SELECT * FROM customers WHERE email LIKE '%yahoo.com' AND age > 25`
	fpA := ComputeFingerprint(sqlA)
	fpB := ComputeFingerprint(sqlB)
	score := fpA.SimilarityScore(fpB)
	if score < SimilarityThreshold {
		t.Errorf("same SQL different literals should pass threshold (%f), got %f", SimilarityThreshold, score)
	}
}

func TestExtractFingerprintFromFrontmatter(t *testing.T) {
	content := `---
name: test memory
description: a test
type: project
sql_fingerprint: abc123def456
sql_tables: [customers, orders, products]
sql_has_cte: true
sql_depth: 3
---

content body here
`
	fp := extractFingerprintFromFrontmatter(content)
	if fp.Hash != "abc123def456" {
		t.Errorf("hash mismatch: %q", fp.Hash)
	}
	if len(fp.Tables) != 3 || fp.Tables[0] != "customers" {
		t.Errorf("tables mismatch: %v", fp.Tables)
	}
	if !fp.HasCTE {
		t.Error("HasCTE should be true")
	}
	if fp.Depth != 3 {
		t.Errorf("depth mismatch: %d", fp.Depth)
	}
}

func TestExtractFingerprintFromFrontmatter_LegacyEntry(t *testing.T) {
	// Memory entries written before v1.1.30 won't have fingerprint fields.
	content := `---
name: legacy entry
description: a legacy memory
type: project
---

old content
`
	fp := extractFingerprintFromFrontmatter(content)
	if !fp.Empty() {
		t.Errorf("legacy entry should have empty fingerprint, got %+v", fp)
	}
}

func containsTable(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// (helper kept local; existing store_test.go has a separate `contains` for substring tests)
