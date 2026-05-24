/*-------------------------------------------------------------------------
 *
 * intent.go
 *	  Deterministic natural-language intent routing for OpenGauss/GaussDB.
 *
 *-------------------------------------------------------------------------
 */
package intent

import (
	"regexp"
	"strings"
)

type Mode string

const (
	ModeLLM         Mode = "llm"
	ModeDirectSkill Mode = "direct_skill"
	ModeEvidence    Mode = "evidence_then_llm"
)

const (
	IntentFreeLLM     = "free_llm"
	IntentCurrentDiag = "current_diag"
	IntentSQLTune     = "sql_tune"
	IntentWDRList     = "wdr_list"
	IntentWDRAnalyze  = "wdr_analyze"
	IntentSlowSQL     = "slow_sql"
	IntentBlockers    = "blockers"
	IntentSessions    = "sessions"
	IntentParams      = "params"
	IntentObjectStats = "object_stats"
)

type Decision struct {
	Intent     string
	Mode       Mode
	Skill      string
	Params     map[string]any
	UseLLM     bool
	Confidence float64
	Reason     string
}

var (
	sqlIDRe                     = regexp.MustCompile(`(?i)(?:sql[_ ]?id|sqlid)\s*[:=：]?\s*(\d{6,25})`)
	bareSQLIDTuneRe             = regexp.MustCompile(`(?i)\b(\d{6,25})\b`)
	wdrSnapshotPairWithPrefixRe = regexp.MustCompile(`(?i)(?:快照|snapshot|snap)\s*#?\s*(\d{1,10})\s*(?:和|与|到|至|之间|,|，|~|-|and|to)\s*(?:(?:快照|snapshot|snap)\s*#?\s*)?(\d{1,10})`)
	wdrSnapshotPairPlainRe      = regexp.MustCompile(`(?i)(\d{1,10})\s*(?:和|与|到|至|,|，|~|-|and|to)\s*(\d{1,10}).*(?:wdr|awr|报告|快照)`)
	wdrSnapshotPairLooseRe      = regexp.MustCompile(`(?i)(?:分析|解读|对比|比较|问题|风险|优化|看看|看下).{0,12}?(\d{1,10})\s*(?:和|与|到|至|之间|,|，|~|-|and|to)\s*(\d{1,10}).{0,20}?(?:报告|快照|wdr|awr)`)
)

func Classify(question string) Decision {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return llm(IntentCurrentDiag, "empty input defaults to current diagnosis")
	}

	if d, ok := classifySQLTune(q); ok {
		return d
	}
	if d, ok := classifyWDR(q); ok {
		return d
	}
	if d, ok := classifyOperationalDirect(q); ok {
		return d
	}
	if isCurrentDiag(q) {
		return llm(IntentCurrentDiag, "matched current database diagnosis intent")
	}
	return llm(IntentFreeLLM, "no deterministic database evidence route matched")
}

func classifySQLTune(q string) (Decision, bool) {
	if !containsAny(q, []string{"优化", "调优", "怎么改", "执行计划", "慢", "tune", "explain"}) {
		return Decision{}, false
	}
	if m := sqlIDRe.FindStringSubmatch(q); len(m) == 2 {
		return evidence(IntentSQLTune, "sqltune", map[string]any{"args": m[1], "mode": "quick"}, 0.98, "matched SQL_ID tune intent"), true
	}
	if containsAny(q, []string{"sql", "查询"}) {
		if m := bareSQLIDTuneRe.FindStringSubmatch(q); len(m) == 2 {
			return evidence(IntentSQLTune, "sqltune", map[string]any{"args": m[1], "mode": "quick"}, 0.88, "matched numeric SQL_ID with tune words"), true
		}
	}
	return Decision{}, false
}

func classifyWDR(q string) (Decision, bool) {
	hasWDRWord := containsAny(q, []string{"wdr", "awr"})
	hasSnapshotReport := strings.Contains(q, "快照") && strings.Contains(q, "报告")
	hasReportPair := false
	for _, re := range []*regexp.Regexp{wdrSnapshotPairWithPrefixRe, wdrSnapshotPairPlainRe, wdrSnapshotPairLooseRe} {
		if re.MatchString(q) {
			hasReportPair = true
			break
		}
	}
	if !hasWDRWord && !hasSnapshotReport && !hasReportPair {
		return Decision{}, false
	}
	if hasReportPair || containsAny(q, []string{"分析", "解读", "生成", "对比", "比较", "之间", "问题", "风险", "analyze", "compare", "between"}) {
		for _, re := range []*regexp.Regexp{wdrSnapshotPairWithPrefixRe, wdrSnapshotPairPlainRe, wdrSnapshotPairLooseRe} {
			if m := re.FindStringSubmatch(q); len(m) == 3 {
				return evidence(IntentWDRAnalyze, "wdranalyze", map[string]any{"args": m[1] + " " + m[2]}, 0.98, "matched WDR analyze snapshot pair"), true
			}
		}
		if hasWDRWord && containsAny(q, []string{"最近", "最新", "latest"}) {
			return evidence(IntentWDRAnalyze, "wdranalyze", map[string]any{"args": "latest"}, 0.9, "matched WDR analyze latest"), true
		}
	}
	if containsAny(q, []string{"哪几个", "哪些", "有几个", "几个", "列表", "列出", "当前", "现有", "已有", "快照", "snapshot", "snapshots", "list"}) {
		return direct(IntentWDRList, "wdr", nil, 0.95, "matched WDR list intent"), true
	}
	return Decision{}, false
}

func classifyOperationalDirect(q string) (Decision, bool) {
	if containsAny(q, []string{"阻塞", "锁等待", "block", "blocking", "deadlock", "死锁"}) {
		return direct(IntentBlockers, "blocktree", nil, 0.86, "matched blockers intent"), true
	}
	if containsAny(q, []string{"慢sql", "慢 sql", "慢查询", "slow sql", "slow query"}) && containsAny(q, []string{"哪些", "列表", "当前", "有", "top", "排行"}) {
		return direct(IntentSlowSQL, "slowsql", nil, 0.86, "matched slow SQL list intent"), true
	}
	if containsAny(q, []string{"会话", "session", "连接"}) && containsAny(q, []string{"哪些", "列表", "当前", "活跃", "有"}) {
		return direct(IntentSessions, "activesessions", nil, 0.82, "matched session list intent"), true
	}
	if containsAny(q, []string{"参数", "配置", "guc", "shared_buffers", "work_mem", "max_connections"}) {
		return direct(IntentParams, "params", map[string]any{"args": extractKnownParam(q)}, 0.82, "matched parameter review intent"), true
	}
	if containsAny(q, []string{"膨胀", "dead tuple", "dead tuples", "统计信息", "vacuum", "analyze", "对象", "表", "索引"}) && containsAny(q, []string{"哪些", "列表", "当前", "有", "查看", "检查"}) {
		return direct(IntentObjectStats, "objstats", nil, 0.78, "matched object stats intent"), true
	}
	return Decision{}, false
}

func isCurrentDiag(q string) bool {
	return containsAny(q, []string{"当前数据库", "存在什么问题", "有哪些问题", "有没有问题", "诊断", "排查", "性能问题", "数据库慢", "异常"})
}

func direct(intent, skill string, params map[string]any, confidence float64, reason string) Decision {
	if params == nil {
		params = map[string]any{}
	}
	return Decision{Intent: intent, Mode: ModeDirectSkill, Skill: skill, Params: params, UseLLM: false, Confidence: confidence, Reason: reason}
}

func evidence(intent, skill string, params map[string]any, confidence float64, reason string) Decision {
	if params == nil {
		params = map[string]any{}
	}
	return Decision{Intent: intent, Mode: ModeEvidence, Skill: skill, Params: params, UseLLM: true, Confidence: confidence, Reason: reason}
}

func llm(intent, reason string) Decision {
	return Decision{Intent: intent, Mode: ModeLLM, Params: map[string]any{}, UseLLM: true, Confidence: 0.6, Reason: reason}
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func extractKnownParam(q string) string {
	for _, p := range []string{"shared_buffers", "work_mem", "max_connections", "effective_cache_size", "maintenance_work_mem"} {
		if strings.Contains(q, p) {
			return p
		}
	}
	return ""
}
