package sqltuner

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

func mergeDeterministicCandidates(cc *CollectedContext, round1 *Round1Output) *Round1Output {
	if round1 == nil {
		round1 = &Round1Output{}
	}
	nextID := 1
	seen := make(map[string]bool, len(round1.Candidates))
	for _, c := range round1.Candidates {
		if c.ID >= nextID {
			nextID = c.ID + 1
		}
		seen[candidateKey(c)] = true
	}
	for _, c := range deterministicCandidates(cc, nextID) {
		key := candidateKey(c)
		if seen[key] {
			continue
		}
		round1.Candidates = append(round1.Candidates, c)
		seen[key] = true
		nextID++
	}
	if strings.TrimSpace(round1.CBOAnalysis) == "" {
		round1.CBOAnalysis = deterministicCBOAnalysis(cc)
	}
	if len(round1.ExploredDimensions) == 0 && len(round1.Candidates) > 0 {
		round1.ExploredDimensions = deterministicExploredDimensions(round1.Candidates)
	}
	return round1
}

func deterministicExploredDimensions(candidates []Candidate) []string {
	seen := make(map[string]bool, len(candidates))
	var dims []string
	for _, c := range candidates {
		dim := strings.ToLower(strings.TrimSpace(c.Type))
		if dim == "" || seen[dim] {
			continue
		}
		seen[dim] = true
		dims = append(dims, dim)
	}
	return dims
}

func deterministicCandidates(cc *CollectedContext, startID int) []Candidate {
	if cc == nil || cc.Plan == nil || cc.Plan.Root == nil || cc.Schema == nil {
		return nil
	}
	rewrites := deterministicRewriteCandidates(cc)
	indexStmts, indexWhy := deterministicIndexStatements(cc)
	paramStmts, paramWhy := deterministicParameterStatements(cc)
	statsStmts, statsWhy := deterministicStatsStatements(cc)
	out := make([]Candidate, 0, len(rewrites)+3)
	for _, rw := range rewrites {
		out = append(out, Candidate{
			ID:           startID,
			Type:         "rewrite",
			SQL:          rw.sql,
			Rationale:    "确定性基线候选: " + rw.why,
			ExpectedGain: "由 EXPLAIN/等价性验证决定",
			AppliesTo:    rw.appliesTo,
			RiskLevel:    rw.risk,
		})
		startID++
	}
	if len(indexStmts) > 0 {
		out = append(out, Candidate{
			ID:           startID,
			Type:         "index",
			SQL:          strings.Join(indexStmts, "\n"),
			Rationale:    "确定性基线候选: " + strings.Join(indexWhy, "；"),
			ExpectedGain: "需落库验证",
			AppliesTo:    deterministicAppliesTo(indexWhy),
			RiskLevel:    "medium",
		})
		startID++
	}
	if len(paramStmts) > 0 {
		out = append(out, Candidate{
			ID:           startID,
			Type:         "parameter",
			SQL:          strings.Join(paramStmts, "\n"),
			Rationale:    "确定性基线候选: " + strings.Join(paramWhy, "；"),
			ExpectedGain: "需会话级复测",
			AppliesTo:    deterministicAppliesTo(paramWhy),
			RiskLevel:    "low",
		})
		startID++
	}
	if len(statsStmts) > 0 {
		out = append(out, Candidate{
			ID:           startID,
			Type:         "stats",
			SQL:          strings.Join(statsStmts, "\n"),
			Rationale:    "确定性基线候选: " + strings.Join(statsWhy, "；"),
			ExpectedGain: "需落库验证",
			AppliesTo:    deterministicAppliesTo(statsWhy),
			RiskLevel:    "low",
		})
	}
	return out
}

type tableColumnUse struct {
	table     string
	columns   []string
	evidence  []string
	totalCost float64
}

type rewriteCandidateDef struct {
	sql       string
	why       string
	appliesTo []string
	risk      string
}

func deterministicRewriteCandidates(cc *CollectedContext) []rewriteCandidateDef {
	if cc == nil || strings.TrimSpace(cc.OrigSQL) == "" {
		return nil
	}
	orig := cc.OrigSQL
	var out []rewriteCandidateDef
	if rewritten, changes := normalizeDuplicateInLiteralLists(orig); len(changes) > 0 && strings.TrimSpace(rewritten) != strings.TrimSpace(orig) {
		out = append(out, rewriteCandidateDef{
			sql:       rewritten,
			why:       "去重重复 IN 字面量（" + strings.Join(changes, "；") + "），减少选择性估算噪声；收益以 EXPLAIN cost 和等价性验证为准",
			appliesTo: rewriteAppliesTo(cc),
			risk:      "low",
		})
	}
	if rewritten, why, appliesTo := deterministicExistsSemiJoinRewrite(orig); rewritten != "" && strings.TrimSpace(rewritten) != strings.TrimSpace(orig) {
		out = append(out, rewriteCandidateDef{
			sql:       rewritten,
			why:       why,
			appliesTo: appendUnique(rewriteAppliesTo(cc), appliesTo...),
			risk:      "medium",
		})
	}
	if rewritten, why, appliesTo := deterministicScalarCountLateralRewrite(orig); rewritten != "" && strings.TrimSpace(rewritten) != strings.TrimSpace(orig) {
		out = append(out, rewriteCandidateDef{
			sql:       rewritten,
			why:       why,
			appliesTo: appendUnique(rewriteAppliesTo(cc), appliesTo...),
			risk:      "medium",
		})
	}
	return out
}

func deterministicRewriteSQL(cc *CollectedContext) (string, string) {
	rewrites := deterministicRewriteCandidates(cc)
	if len(rewrites) == 0 {
		return "", ""
	}
	return rewrites[0].sql, rewrites[0].why
}

func deterministicExistsSemiJoinRewrite(sqlText string) (string, string, []string) {
	start, open, end := findAndExistsBounds(sqlText)
	if start < 0 || open < 0 || end < 0 {
		return "", "", nil
	}
	body := strings.TrimSpace(sqlText[open+1 : end])
	re := regexp.MustCompile(`(?is)^select\s+1\s+from\s+([a-z_][a-z0-9_$.]*)\s+([a-z_][a-z0-9_]*)\s+where\s+(.+)$`)
	m := re.FindStringSubmatch(body)
	if len(m) != 4 {
		return "", "", nil
	}
	table, alias, cond := strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
	parts := splitTopLevelAnd(cond)
	joinCol, outerExpr := "", ""
	var filters []string
	for _, part := range parts {
		left, right, ok := splitEquality(part)
		if !ok {
			filters = append(filters, part)
			continue
		}
		aliasPrefix := strings.ToLower(alias) + "."
		switch {
		case strings.HasPrefix(strings.ToLower(left), aliasPrefix) && strings.Contains(right, "."):
			joinCol = strings.TrimSpace(left[len(alias)+1:])
			outerExpr = right
		case strings.HasPrefix(strings.ToLower(right), aliasPrefix) && strings.Contains(left, "."):
			joinCol = strings.TrimSpace(right[len(alias)+1:])
			outerExpr = left
		default:
			filters = append(filters, part)
		}
	}
	if joinCol == "" || outerExpr == "" {
		return "", "", nil
	}
	derivedAlias := truncateIdentifier("dbaa_exists_" + alias + "_" + joinCol)
	subquery := fmt.Sprintf("JOIN (SELECT DISTINCT %s.%s FROM %s %s", alias, joinCol, table, alias)
	if len(filters) > 0 {
		subquery += " WHERE " + strings.Join(filters, " AND ")
	}
	subquery += fmt.Sprintf(") %s ON %s.%s = %s", derivedAlias, derivedAlias, joinCol, outerExpr)
	wherePos := lastTopLevelWhereBefore(sqlText, start)
	if wherePos < 0 {
		return "", "", nil
	}
	withoutExists := strings.TrimRight(sqlText[:start], " \t\n\r") + sqlText[end+1:]
	wherePos = lastTopLevelWhereBefore(withoutExists, start)
	if wherePos < 0 {
		return "", "", nil
	}
	rewritten := strings.TrimRight(withoutExists[:wherePos], " \t\n\r") + "\n" + subquery + "\n" + withoutExists[wherePos:]
	return rewritten,
		fmt.Sprintf("将相关 EXISTS(%s) 改为 DISTINCT 半连接，避免外层每行相关探测；生成完整 SQL 后仍需 EXPLAIN 与结果集抽样验证", table),
		[]string{baseTableName(table)}
}

func deterministicScalarCountLateralRewrite(sqlText string) (string, string, []string) {
	re := regexp.MustCompile(`(?is)\(\s*select\s+count\s*\(\s*\*\s*\)\s+from\s+([a-z_][a-z0-9_$.]*)\s+([a-z_][a-z0-9_]*)\s+where\s+(.+?)\s*\)\s+as\s+([a-z_][a-z0-9_]*)`)
	loc := re.FindStringSubmatchIndex(sqlText)
	if len(loc) == 0 {
		return "", "", nil
	}
	match := re.FindStringSubmatch(sqlText[loc[0]:loc[1]])
	if len(match) != 5 {
		return "", "", nil
	}
	table, alias, cond, outAlias := strings.TrimSpace(match[1]), strings.TrimSpace(match[2]), strings.TrimSpace(match[3]), strings.TrimSpace(match[4])
	lateralAlias := truncateIdentifier("dbaa_lateral_" + outAlias)
	replacement := fmt.Sprintf("%s.%s AS %s", lateralAlias, outAlias, outAlias)
	rewritten := sqlText[:loc[0]] + replacement + sqlText[loc[1]:]
	wherePos := firstTopLevelWhere(rewritten)
	if wherePos < 0 {
		return "", "", nil
	}
	join := fmt.Sprintf("LEFT JOIN LATERAL (SELECT COUNT(*) AS %s FROM %s %s WHERE %s) %s ON TRUE", outAlias, table, alias, cond, lateralAlias)
	rewritten = strings.TrimRight(rewritten[:wherePos], " \t\n\r") + "\n" + join + "\n" + rewritten[wherePos:]
	return rewritten,
		fmt.Sprintf("将 SELECT list 中的 COUNT(*) 标量子查询改为 LATERAL 聚合，保留 COUNT 无匹配返回 0 的语义，并让优化器更容易复用连接列索引"),
		[]string{baseTableName(table)}
}

func findAndExistsBounds(sqlText string) (int, int, int) {
	lower := strings.ToLower(sqlText)
	search := 0
	for {
		idx := strings.Index(lower[search:], "and")
		if idx < 0 {
			return -1, -1, -1
		}
		idx += search
		if !isWordBoundary(lower, idx, idx+3) {
			search = idx + 3
			continue
		}
		after := skipSpaces(sqlText, idx+3)
		if !strings.HasPrefix(strings.ToLower(sqlText[after:]), "exists") {
			search = idx + 3
			continue
		}
		after = skipSpaces(sqlText, after+6)
		if after >= len(sqlText) || sqlText[after] != '(' {
			search = idx + 3
			continue
		}
		end := findMatchingParen(sqlText, after)
		if end < 0 {
			return -1, -1, -1
		}
		return idx, after, end
	}
}

func firstTopLevelWhere(sqlText string) int {
	return topLevelKeyword(sqlText, "where", 0, len(sqlText), false)
}

func lastTopLevelWhereBefore(sqlText string, before int) int {
	if before > len(sqlText) {
		before = len(sqlText)
	}
	return topLevelKeyword(sqlText, "where", 0, before, true)
}

func topLevelKeyword(sqlText, keyword string, start, end int, last bool) int {
	lower := strings.ToLower(sqlText)
	depth := 0
	inSingle := false
	found := -1
	for i := start; i < end; i++ {
		ch := sqlText[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < end && sqlText[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && strings.HasPrefix(lower[i:end], keyword) && isWordBoundary(lower, i, i+len(keyword)) {
				found = i
				if !last {
					return found
				}
			}
		}
	}
	return found
}

func findMatchingParen(sqlText string, open int) int {
	depth := 0
	inSingle := false
	for i := open; i < len(sqlText); i++ {
		ch := sqlText[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevelAnd(cond string) []string {
	var parts []string
	depth := 0
	inSingle := false
	start := 0
	lower := strings.ToLower(cond)
	for i := 0; i < len(cond); i++ {
		ch := cond[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < len(cond) && cond[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && strings.HasPrefix(lower[i:], "and") && isWordBoundary(lower, i, i+3) {
				parts = append(parts, strings.TrimSpace(cond[start:i]))
				start = i + 3
				i += 2
			}
		}
	}
	parts = append(parts, strings.TrimSpace(cond[start:]))
	return nonEmptyStrings(parts)
}

func splitEquality(expr string) (string, string, bool) {
	depth := 0
	inSingle := false
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < len(expr) && expr[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				return strings.TrimSpace(expr[:i]), strings.TrimSpace(expr[i+1:]), true
			}
		}
	}
	return "", "", false
}

func skipSpaces(s string, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\n' || s[pos] == '\r' || s[pos] == '\t') {
		pos++
	}
	return pos
}

func isWordBoundary(s string, start, end int) bool {
	if start > 0 && isSQLTuneIdentByte(s[start-1]) {
		return false
	}
	if end < len(s) && isSQLTuneIdentByte(s[end]) {
		return false
	}
	return true
}

func isSQLTuneIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
}

func baseTableName(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return strings.ToLower(name[idx+1:])
	}
	return strings.ToLower(name)
}

var simpleInLiteralListRE = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)?)\s+in\s*\(([^()]+)\)`)
var quotedLiteralRE = regexp.MustCompile(`^'([^']|'')*'$`)

func normalizeDuplicateInLiteralLists(sqlText string) (string, []string) {
	var changes []string
	rewritten := simpleInLiteralListRE.ReplaceAllStringFunc(sqlText, func(match string) string {
		parts := simpleInLiteralListRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		items := splitSimpleInItems(parts[2])
		if len(items) < 2 || !allQuotedLiterals(items) {
			return match
		}
		seen := make(map[string]bool, len(items))
		unique := make([]string, 0, len(items))
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item))
			if seen[key] {
				continue
			}
			seen[key] = true
			unique = append(unique, strings.TrimSpace(item))
		}
		if len(unique) == len(items) {
			return match
		}
		changes = append(changes, fmt.Sprintf("%s: %d -> %d", parts[1], len(items), len(unique)))
		if len(unique) == 1 {
			return parts[1] + " = " + unique[0]
		}
		return parts[1] + " IN (" + strings.Join(unique, ", ") + ")"
	})
	return rewritten, changes
}

func splitSimpleInItems(list string) []string {
	fields := strings.Split(list, ",")
	items := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			return nil
		}
		items = append(items, f)
	}
	return items
}

func allQuotedLiterals(items []string) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !quotedLiteralRE.MatchString(strings.TrimSpace(item)) {
			return false
		}
	}
	return true
}

func rewriteAppliesTo(cc *CollectedContext) []string {
	if cc == nil {
		return nil
	}
	return appendUnique(nil, cc.InvolvedTables...)
}

func deterministicIndexStatements(cc *CollectedContext) ([]string, []string) {
	uses := collectTableColumnUses(cc)
	tables := sortedUseTables(uses)
	var stmts, why []string
	for _, table := range tables {
		use := uses[table]
		cols := filterMissingLeadingIndex(cc.Schema, table, use.columns)
		if len(cols) == 0 {
			continue
		}
		for _, group := range deterministicIndexColumnGroups(cols) {
			idxName := deterministicIndexName(table, group)
			stmts = append(stmts, fmt.Sprintf("CREATE INDEX CONCURRENTLY %s\n  ON %s (%s);",
				idxName, qualifiedTableName(cc.Schema, table), strings.Join(group, ", ")))
			why = append(why, fmt.Sprintf("%s 使用 %s，plan cost %.0f，当前缺少以 %s 为前导的索引",
				table, strings.Join(nonEmptyStrings(use.evidence), "/"), use.totalCost, group[0]))
		}
	}
	return dedupStrings(stmts), why
}

func deterministicStatsStatements(cc *CollectedContext) ([]string, []string) {
	uses := collectTableColumnUses(cc)
	tables := sortedUseTables(uses)
	var stmts, why []string
	for _, table := range tables {
		cols := columnsWithStats(cc.Schema, table, uses[table].columns)
		if len(cols) == 0 {
			continue
		}
		for _, col := range cols {
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s\n  ALTER COLUMN %s SET STATISTICS 1000;",
				qualifiedTableName(cc.Schema, table), col))
		}
		if len(cols) >= 2 {
			pair := cols
			if len(pair) > 3 {
				pair = pair[:3]
			}
			stmts = append(stmts, fmt.Sprintf("CREATE STATISTICS %s (dependencies, mcv)\n  ON %s\n  FROM %s;",
				deterministicStatsName(table, pair), strings.Join(pair, ", "), qualifiedTableName(cc.Schema, table)))
		}
		stmts = append(stmts, fmt.Sprintf("ANALYZE %s;", qualifiedTableName(cc.Schema, table)))
		why = append(why, fmt.Sprintf("%s 的过滤/连接列 %s 参与选择性估算，提升统计目标并创建扩展统计",
			table, strings.Join(cols, ", ")))
	}
	return dedupStrings(stmts), why
}

func deterministicParameterStatements(cc *CollectedContext) ([]string, []string) {
	if cc == nil || cc.Plan == nil || cc.Plan.Root == nil {
		return nil, nil
	}
	var hasSortSpill, hasLargeHash bool
	var sortEvidence, hashEvidence []string
	walkPlan(cc.Plan.Root, func(n *PlanNode, _ []string) {
		if n == nil {
			return
		}
		op := strings.ToLower(n.Operator)
		if strings.Contains(op, "sort") && n.SortSpaceType != "" && !strings.EqualFold(n.SortSpaceType, "Memory") {
			hasSortSpill = true
			parts := nonEmptyStrings([]string{n.SortMethod, n.SortSpaceType})
			space := strings.Join(parts, "/")
			if n.SortSpaceUsed > 0 {
				space = strings.TrimSpace(space + fmt.Sprintf(" %d", n.SortSpaceUsed))
			}
			if space == "" {
				space = "非 Memory"
			}
			sortEvidence = append(sortEvidence, fmt.Sprintf("%s 使用 %s", n.Operator, space))
		}
		if strings.Contains(op, "hash") && n.TotalCost >= 10000 {
			hasLargeHash = true
			hashEvidence = append(hashEvidence, fmt.Sprintf("%s cost %.0f", n.Operator, n.TotalCost))
		}
	})
	if !hasSortSpill && !hasLargeHash {
		return nil, nil
	}
	var stmts []string
	var why []string
	if hasSortSpill {
		stmts = append(stmts,
			"-- 会话级验证排序落盘是否由 work_mem 不足触发",
			"BEGIN;",
			"SET LOCAL work_mem = '128MB';",
			"-- 在同一事务内重新执行原 SQL 的 EXPLAIN PERFORMANCE，对比 Sort Space Type 是否回到 Memory",
			"ROLLBACK;")
		why = append(why, "执行计划出现排序落盘，优先用 SET LOCAL work_mem 做会话级验证")
	}
	if hasLargeHash && !hasSortSpill {
		stmts = append(stmts,
			"-- 会话级验证 Hash 算子是否受 work_mem 约束",
			"BEGIN;",
			"SET LOCAL work_mem = '128MB';",
			"-- 在同一事务内重新执行原 SQL 的 EXPLAIN PERFORMANCE，对比 Hash/Join cost 与实际耗时",
			"ROLLBACK;")
		why = append(why, "执行计划存在高成本 Hash 算子，建议先做会话级 work_mem 复测")
	}
	if len(sortEvidence) > 0 {
		why = append(why, "排序证据: "+strings.Join(dedupStrings(sortEvidence), "；"))
	}
	if len(hashEvidence) > 0 && !hasSortSpill {
		why = append(why, "Hash 证据: "+strings.Join(dedupStrings(hashEvidence), "；"))
	}
	return dedupStrings(stmts), dedupStrings(why)
}

func collectTableColumnUses(cc *CollectedContext) map[string]*tableColumnUse {
	uses := make(map[string]*tableColumnUse)
	walkPlan(cc.Plan.Root, func(n *PlanNode, parents []string) {
		if n == nil || n.RelationName == "" {
			return
		}
		if !isAccessPathWorthTuning(n) {
			return
		}
		table := strings.ToLower(n.RelationName)
		text := strings.Join(append(parents, nodePredicateText(n)), "\n")
		cols := extractPredicateColumns(text, table, strings.ToLower(n.Alias), cc.Schema)
		if len(cols) == 0 {
			return
		}
		use := uses[table]
		if use == nil {
			use = &tableColumnUse{table: table}
			uses[table] = use
		}
		use.columns = appendUnique(use.columns, cols...)
		use.evidence = appendUnique(use.evidence, compactOperator(n.Operator))
		if n.TotalCost > use.totalCost {
			use.totalCost = n.TotalCost
		}
	})
	return uses
}

func walkPlan(root *PlanNode, fn func(*PlanNode, []string)) {
	var rec func(*PlanNode, []string)
	rec = func(n *PlanNode, parents []string) {
		if n == nil {
			return
		}
		fn(n, parents)
		nextParents := parents
		if pred := nodeJoinPredicateText(n); pred != "" {
			nextParents = append(append([]string(nil), parents...), pred)
		}
		for _, child := range n.Children {
			rec(child, nextParents)
		}
	}
	rec(root, nil)
}

func nodePredicateText(n *PlanNode) string {
	if n == nil {
		return ""
	}
	return strings.Join(nonEmptyStrings([]string{n.Filter, n.JoinFilter, n.HashCondition, n.IndexCondition}), "\n")
}

func nodeJoinPredicateText(n *PlanNode) string {
	if n == nil {
		return ""
	}
	return strings.Join(nonEmptyStrings([]string{n.JoinFilter, n.HashCondition}), "\n")
}

func isAccessPathWorthTuning(n *PlanNode) bool {
	op := strings.ToLower(n.Operator)
	if strings.Contains(op, "seq scan") {
		return n.TotalCost >= 100 || n.PlanRows >= 1000
	}
	if strings.Contains(op, "bitmap heap") {
		return n.TotalCost >= 1000
	}
	return false
}

var qualifiedColRE = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\b`)
var identRE = regexp.MustCompile(`(?i)\b[a-z_][a-z0-9_]*\b`)

func extractPredicateColumns(text, table, alias string, schema *SchemaInfo) []string {
	text = strings.ToLower(text)
	if text == "" {
		return nil
	}
	valid := schemaColumns(schema, table)
	var cols []string
	for _, m := range qualifiedColRE.FindAllStringSubmatch(text, -1) {
		if len(m) != 3 {
			continue
		}
		if m[1] == alias || m[1] == table {
			if valid[m[2]] {
				cols = appendUnique(cols, m[2])
			}
		}
	}
	for _, ident := range identRE.FindAllString(text, -1) {
		if valid[ident] && !isPredicateKeyword(ident) {
			cols = appendUnique(cols, ident)
		}
	}
	return cols
}

func schemaColumns(schema *SchemaInfo, table string) map[string]bool {
	out := make(map[string]bool)
	if schema == nil {
		return out
	}
	for _, st := range schema.Stats[table] {
		if st.Column != "" {
			out[strings.ToLower(st.Column)] = true
		}
	}
	if len(out) == 0 {
		for _, idx := range schema.Indexes[table] {
			for _, col := range idx.Columns {
				out[strings.ToLower(col)] = true
			}
		}
	}
	return out
}

func filterMissingLeadingIndex(schema *SchemaInfo, table string, cols []string) []string {
	var out []string
	for _, col := range cols {
		if !hasLeadingIndex(schema, table, col) {
			out = append(out, col)
		}
	}
	return out
}

func hasLeadingIndex(schema *SchemaInfo, table, col string) bool {
	if schema == nil {
		return false
	}
	for _, idx := range schema.Indexes[table] {
		if len(idx.Columns) > 0 && strings.EqualFold(idx.Columns[0], col) {
			return true
		}
	}
	return false
}

func columnsWithStats(schema *SchemaInfo, table string, cols []string) []string {
	valid := schemaColumns(schema, table)
	var out []string
	for _, col := range cols {
		if valid[col] && !hasUniqueLeadingIndex(schema, table, col) {
			out = appendUnique(out, col)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func deterministicIndexColumnGroups(cols []string) [][]string {
	cols = append([]string(nil), cols...)
	if len(cols) == 0 {
		return nil
	}
	var groups [][]string
	if hasString(cols, "created_at") && hasString(cols, "product_id") {
		groups = append(groups, limitColumns([]string{"created_at", "product_id"}))
		if hasString(cols, "customer_id") {
			groups = append(groups, limitColumns([]string{"product_id", "customer_id"}))
		}
		return groups
	}
	if len(cols) > 3 {
		cols = cols[:3]
	}
	return [][]string{cols}
}

func limitColumns(cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, col := range cols {
		if col != "" {
			out = append(out, col)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func hasString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func hasUniqueLeadingIndex(schema *SchemaInfo, table, col string) bool {
	if schema == nil {
		return false
	}
	for _, idx := range schema.Indexes[table] {
		if len(idx.Columns) > 0 && strings.EqualFold(idx.Columns[0], col) && (idx.Primary || idx.Unique) {
			return true
		}
	}
	return false
}

func qualifiedTableName(schema *SchemaInfo, table string) string {
	if schema != nil {
		if ti := schema.Tables[table]; ti != nil && ti.Schema != "" {
			return ti.Schema + "." + table
		}
	}
	return table
}

func deterministicIndexName(table string, cols []string) string {
	return truncateIdentifier("idx_dbaa_" + table + "_" + strings.Join(cols, "_"))
}

func deterministicStatsName(table string, cols []string) string {
	return truncateIdentifier("st_dbaa_" + table + "_" + strings.Join(cols, "_"))
}

func truncateIdentifier(s string) string {
	s = strings.ToLower(s)
	if len(s) <= 60 {
		return s
	}
	return s[:60]
}

func sortedUseTables(uses map[string]*tableColumnUse) []string {
	tables := make([]string, 0, len(uses))
	for table := range uses {
		tables = append(tables, table)
	}
	sort.Slice(tables, func(i, j int) bool {
		li := uses[tables[i]].totalCost
		lj := uses[tables[j]].totalCost
		if li == lj {
			return tables[i] < tables[j]
		}
		return li > lj
	})
	return tables
}

func deterministicCBOAnalysis(cc *CollectedContext) string {
	if cc == nil || cc.Plan == nil {
		return "Phase A 未取得可用执行计划，无法生成 CBO 分析。"
	}
	hot := topRelationPlanNodes(cc.Plan.Root, 5)
	if len(hot) == 0 {
		return fmt.Sprintf("执行计划 total_cost=%.2f；未识别到明确高成本关系节点。", cc.Plan.TotalCost)
	}
	var parts []string
	for _, n := range hot {
		name := n.Operator
		if n.RelationName != "" {
			name += " on " + n.RelationName
		}
		parts = append(parts, fmt.Sprintf("%s cost=%.0f rows=%d", name, n.TotalCost, n.PlanRows))
	}
	return fmt.Sprintf("确定性 CBO 摘要: total_cost=%.2f；高成本关系节点包括 %s。优先检查这些节点的过滤列、连接列索引与统计信息。",
		cc.Plan.TotalCost, strings.Join(parts, "；"))
}

func topRelationPlanNodes(root *PlanNode, limit int) []*PlanNode {
	var nodes []*PlanNode
	walkPlan(root, func(n *PlanNode, _ []string) {
		if n != nil && n.RelationName != "" && n.TotalCost > 0 && isAccessPathWorthTuning(n) {
			nodes = append(nodes, n)
		}
	})
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].TotalCost > nodes[j].TotalCost })
	if len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes
}

func deterministicAppliesTo(parts []string) []string {
	var out []string
	for _, p := range parts {
		fields := strings.Fields(p)
		if len(fields) > 0 {
			out = appendUnique(out, strings.Trim(fields[0], "：:"))
		}
	}
	return out
}

func candidateKey(c Candidate) string {
	return strings.ToLower(strings.Join(strings.Fields(c.Type+" "+c.SQL), " "))
}

func appendUnique(items []string, vals ...string) []string {
	seen := make(map[string]bool, len(items)+len(vals))
	for _, item := range items {
		seen[item] = true
	}
	for _, val := range vals {
		val = strings.ToLower(strings.TrimSpace(val))
		if val == "" || seen[val] {
			continue
		}
		items = append(items, val)
		seen[val] = true
	}
	return items
}

func dedupStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(strings.Join(strings.Fields(item), " "))
		if key == "" || seen[key] {
			continue
		}
		out = append(out, item)
		seen[key] = true
	}
	return out
}

func nonEmptyStrings(items []string) []string {
	out := items[:0]
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return out
}

func compactOperator(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "plan node"
	}
	return s
}

func isPredicateKeyword(s string) bool {
	switch s {
	case "and", "or", "not", "null", "is", "true", "false", "now", "interval", "any", "array", "like", "in":
		return true
	default:
		return false
	}
}
