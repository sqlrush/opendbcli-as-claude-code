package sqltuner

import (
	"strings"
	"testing"
)

func TestDeterministicCandidatesGenerateRewriteIndexAndStats(t *testing.T) {
	cc := &CollectedContext{
		OrigSQL: "SELECT * FROM bench_orders o JOIN bench_shipments s ON s.order_id=o.id WHERE o.status IN ('test', 'test', 'paid')",
		Plan: &PlanInfo{TotalCost: 1000, Root: &PlanNode{
			Operator:      "Hash Join",
			TotalCost:     1000,
			HashCondition: "(s.order_id = o.id)",
			Children: []*PlanNode{
				{
					Operator:     "Seq Scan",
					RelationName: "bench_orders",
					Alias:        "o",
					TotalCost:    600,
					PlanRows:     1,
					Filter:       "((status = 'test') AND (created_at >= now()))",
				},
				{
					Operator:     "Seq Scan",
					RelationName: "bench_shipments",
					Alias:        "s",
					TotalCost:    400,
					PlanRows:     1800000,
				},
			},
		}},
		Schema: &SchemaInfo{
			Tables: map[string]*TableInfo{
				"bench_orders":    {Name: "bench_orders", Schema: "public"},
				"bench_shipments": {Name: "bench_shipments", Schema: "public"},
			},
			Indexes: map[string][]IndexInfo{
				"bench_orders": {{Name: "bench_orders_pkey", Columns: []string{"id"}, Primary: true}},
			},
			Stats: map[string][]ColStat{
				"bench_orders": {
					{Table: "bench_orders", Column: "id"},
					{Table: "bench_orders", Column: "status"},
					{Table: "bench_orders", Column: "created_at"},
				},
				"bench_shipments": {
					{Table: "bench_shipments", Column: "order_id"},
				},
			},
		},
	}

	out := mergeDeterministicCandidates(cc, &Round1Output{})
	if len(out.Candidates) != 3 {
		t.Fatalf("expected rewrite + index + stats candidates, got %#v", out.Candidates)
	}
	if out.Candidates[0].Type != "rewrite" || !strings.Contains(out.Candidates[0].SQL, "o.status IN ('test', 'paid')") {
		t.Fatalf("expected duplicate-IN rewrite candidate first, got %#v", out.Candidates[0])
	}
	if strings.Join(out.ExploredDimensions, ",") != "rewrite,index,stats" {
		t.Fatalf("unexpected explored dimensions: %#v", out.ExploredDimensions)
	}
	reportSQL := out.Candidates[0].SQL + "\n" + out.Candidates[1].SQL + "\n" + out.Candidates[2].SQL
	for _, want := range []string{
		"CREATE INDEX CONCURRENTLY idx_dbaa_bench_orders_status_created_at",
		"ON public.bench_orders (status, created_at)",
		"CREATE INDEX CONCURRENTLY idx_dbaa_bench_shipments_order_id",
		"ALTER TABLE public.bench_orders\n  ALTER COLUMN status SET STATISTICS 1000",
	} {
		if !strings.Contains(reportSQL, want) {
			t.Fatalf("missing %q in deterministic SQL:\n%s", want, reportSQL)
		}
	}
}

func TestDeterministicCandidatesGenerateParameterForSortSpill(t *testing.T) {
	cc := &CollectedContext{
		OrigSQL: "SELECT * FROM bench_orders ORDER BY created_at",
		Plan: &PlanInfo{TotalCost: 2000, Root: &PlanNode{
			Operator:      "Sort",
			TotalCost:     2000,
			PlanRows:      100000,
			SortKey:       []string{"created_at"},
			SortMethod:    "external merge",
			SortSpaceType: "Disk",
			SortSpaceUsed: 8192,
		}},
		Schema: &SchemaInfo{Tables: map[string]*TableInfo{}},
	}

	out := mergeDeterministicCandidates(cc, &Round1Output{})
	if len(out.Candidates) != 1 {
		t.Fatalf("expected parameter candidate, got %#v", out.Candidates)
	}
	c := out.Candidates[0]
	if c.Type != "parameter" || !strings.Contains(c.SQL, "SET LOCAL work_mem = '128MB'") {
		t.Fatalf("unexpected parameter candidate: %#v", c)
	}
	verifies := []VerifyResult{{CandID: c.ID, Verifiable: false, Note: "参数类方案需会话级 SET LOCAL 后重新 EXPLAIN"}}
	accepted, rejected := classifyReportCandidates(out.Candidates, verifies)
	if len(accepted) != 1 || len(rejected) != 0 {
		t.Fatalf("parameter candidate should be accepted as scoped validation advice: accepted=%#v rejected=%#v", accepted, rejected)
	}
}

func TestNormalizeDuplicateInLiteralLists(t *testing.T) {
	got, changes := normalizeDuplicateInLiteralLists("SELECT * FROM t WHERE a IN ('x', 'x') AND b IN ('u', 'v', 'u') AND c IN (1, 1)")
	for _, want := range []string{"a = 'x'", "b IN ('u', 'v')", "c IN (1, 1)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewrite missing %q: %s", want, got)
		}
	}
	if len(changes) != 2 {
		t.Fatalf("expected two literal-list changes, got %#v", changes)
	}
}

func TestDeterministicRewriteCandidatesGenerateExistsAndLateral(t *testing.T) {
	sql := `WITH recent_orders AS (
  SELECT o.id AS order_id, o.customer_id, o.total_amount, o.status, o.created_at
  FROM bench_orders o
  WHERE o.created_at >= now() - interval '1 day'
)
SELECT c.id,
  (SELECT COUNT(*) FROM bench_reviews r2 WHERE r2.product_id = tp.product_id AND r2.customer_id = c.id) AS my_reviews_on_product
FROM customer_spend cs
JOIN bench_customers c ON c.id = cs.customer_id
JOIN recent_orders ro ON ro.customer_id = cs.customer_id
JOIN top_products tp ON tp.product_id = 1
WHERE cs.region_rank <= 50
  AND EXISTS (
    SELECT 1 FROM bench_payments p2
    WHERE p2.order_id = ro.order_id
      AND p2.payment_method IN ('test', 'test')
  )
  AND tp.revenue > 50`
	cc := &CollectedContext{
		OrigSQL: sql,
		Plan:    &PlanInfo{Root: &PlanNode{Operator: "Result"}},
		Schema:  &SchemaInfo{},
	}
	rewrites := deterministicRewriteCandidates(cc)
	joined := ""
	for _, rw := range rewrites {
		joined += rw.sql + "\n" + rw.why + "\n"
	}
	for _, want := range []string{
		"JOIN (SELECT DISTINCT p2.order_id FROM bench_payments p2 WHERE p2.payment_method IN ('test', 'test'))",
		"dbaa_exists_p2_order_id.order_id = ro.order_id",
		"LEFT JOIN LATERAL (SELECT COUNT(*) AS my_reviews_on_product FROM bench_reviews r2",
		"dbaa_lateral_my_reviews_on_product.my_reviews_on_product AS my_reviews_on_product",
		"DISTINCT 半连接",
		"LATERAL 聚合",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in deterministic rewrites:\n%s", want, joined)
		}
	}
}

func TestMergeDeterministicCandidatesKeepsLLMOutput(t *testing.T) {
	cc := &CollectedContext{Plan: &PlanInfo{Root: &PlanNode{Operator: "Result"}}, Schema: &SchemaInfo{}}
	round1 := &Round1Output{Candidates: []Candidate{{
		ID:        9,
		Type:      "rewrite",
		SQL:       "SELECT 1",
		Rationale: "llm candidate",
	}}}

	out := mergeDeterministicCandidates(cc, round1)
	if len(out.Candidates) != 1 || out.Candidates[0].ID != 9 {
		t.Fatalf("unexpected candidates: %#v", out.Candidates)
	}
}

func TestDeterministicCandidatesSkipCheapIndexScans(t *testing.T) {
	cc := &CollectedContext{
		Plan: &PlanInfo{TotalCost: 10, Root: &PlanNode{
			Operator:     "Index Scan",
			RelationName: "bench_customers",
			Alias:        "c",
			TotalCost:    2,
			Filter:       "(vip_level >= 50)",
		}},
		Schema: &SchemaInfo{
			Tables: map[string]*TableInfo{
				"bench_customers": {Name: "bench_customers", Schema: "public"},
			},
			Stats: map[string][]ColStat{
				"bench_customers": {{Table: "bench_customers", Column: "vip_level"}},
			},
		},
	}

	out := mergeDeterministicCandidates(cc, &Round1Output{})
	if len(out.Candidates) != 0 {
		t.Fatalf("cheap index scan should not produce deterministic candidates: %#v", out.Candidates)
	}
	if analysis := deterministicCBOAnalysis(cc); strings.Contains(analysis, "Index Scan") {
		t.Fatalf("cheap index scan should not appear as high-cost CBO node: %s", analysis)
	}
}

func TestRenderFallbackReportGuardsUnverifiedAndInvalidCandidates(t *testing.T) {
	cc := &CollectedContext{
		OrigSQL: "SELECT * FROM bench_orders WHERE status = 'test'",
		Plan: &PlanInfo{TotalCost: 1000, Root: &PlanNode{
			Operator:     "Seq Scan",
			RelationName: "bench_orders",
			Alias:        "o",
			TotalCost:    1000,
			PlanRows:     100000,
			Filter:       "(status = 'test')",
		}},
	}
	round1 := &Round1Output{
		CBOAnalysis: "Seq Scan on bench_orders cost=1000",
		Candidates: []Candidate{
			{
				ID:           1,
				Type:         "index",
				SQL:          "CREATE INDEX CONCURRENTLY idx_orders_status ON bench_orders(status);",
				Rationale:    "给过滤列补索引",
				ExpectedGain: "2000000×",
				AppliesTo:    []string{"bench_orders"},
				RiskLevel:    "low",
			},
			{
				ID:           2,
				Type:         "rewrite",
				SQL:          "WITH ... 原 SQL 不变 ...",
				Rationale:    "模型截断的改写",
				ExpectedGain: "3×",
				RiskLevel:    "low",
			},
			{
				ID:           3,
				Type:         "hint",
				SQL:          "/*+ set(HashJoin) */ SELECT",
				Rationale:    "模型给出的 hint 草案不完整",
				ExpectedGain: "5×",
				RiskLevel:    "medium",
			},
			{
				ID:           4,
				Type:         "schema",
				SQL:          "CREATE INDEX idx_template ON <表名>(col);",
				Rationale:    "模型给出的模板占位方案",
				ExpectedGain: "40×",
				RiskLevel:    "high",
			},
			{
				ID:           5,
				Type:         "rewrite",
				SQL:          "SELECT * FROM bench_orders WHERE",
				Rationale:    "模型给出的 SQL 片段",
				ExpectedGain: "2×",
				RiskLevel:    "low",
			},
		},
	}
	verifies := []VerifyResult{
		{CandID: 1, Verifiable: false, Note: "DDL/统计类方案未落库执行，需变更后重新 EXPLAIN"},
		{CandID: 2, Verifiable: true, OldCost: 1000, Error: "syntax error at or near \"...\""},
		{CandID: 3, Verifiable: true, OldCost: 1000, Error: "syntax error at end of input"},
		{CandID: 4, Verifiable: false, Note: "DDL/统计类方案未落库执行，需变更后重新 EXPLAIN"},
		{CandID: 5, Verifiable: true, OldCost: 1000, Error: "syntax error at end of input"},
	}

	got := renderFallbackReport(cc, round1, verifies, "test")
	for _, want := range []string{
		"## 2. 执行计划",
		"[P1] Seq Scan on bench_orders o cost=1000 rows=100000",
		"**对应计划节点**: [P1]",
		"**执行前检查**:",
		"**回滚方式**: DROP INDEX CONCURRENTLY IF EXISTS idx_orders_status;",
		"## 6. 待验证策略（从未采纳候选恢复）",
		"这些策略来自 LLM 候选或验证失败候选的可取方向",
		"## 7. 模型候选被拒绝（调试信息）",
		"Candidate 2 (rewrite): 模型截断的改写",
		"拒绝原因: 候选 SQL/DDL 含省略号占位，不能执行",
		"LLM 把改写方向、占位符或片段当成完整 SQL/DDL 输出",
		"Candidate 3 (hint): 模型给出的 hint 草案不完整",
		"拒绝原因: 候选 SQL/DDL 是不完整片段，不能执行",
		"LLM 把改写方向、占位符或片段当成完整 SQL/DDL 输出",
		"Candidate 4 (schema): 模型给出的模板占位方案",
		"拒绝原因: 候选 SQL/DDL 含模板占位符，不能执行",
		"Candidate 5 (rewrite): 模型给出的 SQL 片段",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in report:\n%s", want, got)
		}
	}
	diagIdx := strings.Index(got, "## 7. 模型候选被拒绝（调试信息）")
	for _, rejectedTitle := range []string{"### 方案 2:", "### 方案 3:", "### 方案 4:", "### 方案 5:"} {
		if strings.Contains(got, rejectedTitle) {
			t.Fatalf("rejected candidate should not appear as accepted plan %q:\n%s", rejectedTitle, got)
		}
	}
	pendingIdx := strings.Index(got, "## 6. 待验证策略（从未采纳候选恢复）")
	if cand3Idx := strings.Index(got, "Candidate 3 (hint)"); pendingIdx < 0 || cand3Idx < pendingIdx {
		t.Fatalf("incomplete candidate should appear only in pending/rejected sections:\n%s", got)
	}
	if strings.Contains(got[:diagIdx], "<表名>") {
		t.Fatalf("placeholder candidate should not appear before rejected diagnostics:\n%s", got)
	}
	for _, notWant := range []string{
		"DDL 类未实跑", "待落库验证", "预期收益", "收益判断", "2000000×",
		"已尝试但未采纳的方案",
	} {
		if strings.Contains(got, notWant) {
			t.Fatalf("report should not contain %q:\n%s", notWant, got)
		}
	}
}

func TestClassifyReportCandidatesDeduplicatesEquivalentIndexPlans(t *testing.T) {
	candidates := []Candidate{
		{ID: 1, Type: "index", SQL: "CREATE INDEX CONCURRENTLY idx_a ON bench_orders(status, created_at);", RiskLevel: "medium"},
		{ID: 2, Type: "index", SQL: "CREATE INDEX CONCURRENTLY idx_b ON bench_orders(status, created_at);", RiskLevel: "medium"},
	}
	verifies := []VerifyResult{
		{CandID: 1, Verifiable: false, Note: "DDL"},
		{CandID: 2, Verifiable: false, Note: "DDL"},
	}
	accepted, rejected := classifyReportCandidates(candidates, verifies)
	if len(accepted) != 1 || accepted[0].c.ID != 1 {
		t.Fatalf("expected only first equivalent index accepted, got accepted=%#v", accepted)
	}
	if len(rejected) != 1 || !strings.Contains(rejected[0].rejectReason, "同类重复") {
		t.Fatalf("expected duplicate index rejection, got rejected=%#v", rejected)
	}
}

func TestInvalidCandidateSQLReasonRejectsPlaceholdersAndFragments(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{name: "template_table", sql: "CREATE INDEX idx ON <表名>(col);", want: "模板占位符"},
		{name: "template_pid", sql: "SELECT pg_terminate_backend(<PID>);", want: "模板占位符"},
		{name: "compact_original_unchanged", sql: "原SQL不变", want: "完整可执行"},
		{name: "trailing_select", sql: "/*+ set(HashJoin) */ SELECT", want: "不完整片段"},
		{name: "trailing_where", sql: "SELECT * FROM bench_orders WHERE", want: "不完整片段"},
		{name: "trailing_comma", sql: "CREATE INDEX idx ON bench_orders(status,", want: "不完整片段"},
		{name: "volatile_partial_index", sql: "CREATE INDEX idx ON bench_orders(created_at) WHERE created_at >= now() - interval '1 day';", want: "非 immutable"},
		{name: "schema_from_cte", sql: "CREATE TABLE bench_top_products AS SELECT * FROM top_products;", want: "CTE/临时结果"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := invalidCandidateSQLReason(Candidate{SQL: tc.sql})
			if !strings.Contains(got, tc.want) {
				t.Fatalf("invalidCandidateSQLReason(%q) = %q, want contains %q", tc.sql, got, tc.want)
			}
		})
	}

	valid := "SELECT * FROM bench_orders WHERE total_amount < 10 AND created_at > now() - interval '1 day'"
	if got := invalidCandidateSQLReason(Candidate{SQL: valid}); got != "" {
		t.Fatalf("comparison operators should not be treated as placeholders: %q", got)
	}

	validWithLimit := "WITH recent_orders AS (SELECT * FROM bench_orders WHERE status = 'test') SELECT * FROM recent_orders ORDER BY created_at DESC LIMIT 100;"
	if got := invalidCandidateSQLReason(Candidate{SQL: validWithLimit}); got != "" {
		t.Fatalf("complete SQL ending with LIMIT value should not be treated as fragment: %q", got)
	}
}

func TestFormatSQLForReportStripsANSIArtifacts(t *testing.T) {
	got := formatSQLForReport("CREATE INDEX idx ON bench_orders(customer_\x1b[0mid);\nCREATE INDEX idx2 ON bench_reviews(product_[0mid);")
	for _, notWant := range []string{"\x1b", "[0m"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("formatSQLForReport leaked ANSI artifact %q:\n%s", notWant, got)
		}
	}
	for _, want := range []string{"customer_id", "product_id"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatSQLForReport corrupted identifier %q:\n%s", want, got)
		}
	}
}

func TestRenderAnnotatedPlanAvoidsSingleCharacterWrapOnDeepTrees(t *testing.T) {
	leaf := &PlanNode{
		Operator:     "Seq Scan",
		RelationName: "bench_reviews",
		Alias:        "r",
		TotalCost:    77711,
		PlanRows:     1,
		Filter:       "((r.product_id = oi.product_id) AND (r.customer_id = c.id) AND (r.created_at >= (now() - '1 day'::interval)))",
	}
	root := leaf
	for i := 0; i < 12; i++ {
		root = &PlanNode{
			Operator:  "Nested Loop",
			TotalCost: float64(80000 + i),
			PlanRows:  1,
			Children:  []*PlanNode{root},
		}
	}
	got := renderAnnotatedPlan(&PlanInfo{TotalCost: 90000, Root: root}, map[*PlanNode]planMarker{
		leaf: {Ref: "[P1]", Relation: "bench_reviews", Operator: "Seq Scan", CandidateIDs: []int{1}, Reason: "test"},
	})

	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimSpace(line)
		if len([]rune(trimmed)) == 1 && strings.ContainsAny(trimmed, "Filter:customer_idcreated_at()=<>") {
			t.Fatalf("plan detail wrapped to single-character line %q:\n%s", line, got)
		}
		if strings.HasPrefix(trimmed, "Filter:") && len([]rune(line)) > 110 {
			t.Fatalf("plan filter line too wide for terminal rendering (%d): %q\n%s", len([]rune(line)), line, got)
		}
	}
	if !strings.Contains(got, "[P1] Seq Scan on bench_reviews r") {
		t.Fatalf("marked leaf should remain visible:\n%s", got)
	}
	if strings.Contains(got, "omitted") {
		t.Fatalf("plan should not omit nodes:\n%s", got)
	}
}
