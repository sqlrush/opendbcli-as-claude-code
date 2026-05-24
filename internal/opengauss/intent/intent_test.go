package intent

import "testing"

func TestClassifyGoldenRoutes(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		intent      string
		mode        Mode
		skill       string
		args        string
		forbidSkill string
	}{
		{name: "wdr list", input: "当前有哪几个wdr报告", intent: IntentWDRList, mode: ModeDirectSkill, skill: "wdr", forbidSkill: "wdranalyze"},
		{name: "wdr analyze pair", input: "分析 快照76和77之间生成的wdr报告", intent: IntentWDRAnalyze, mode: ModeEvidence, skill: "wdranalyze", args: "76 77", forbidSkill: "wdr"},
		{name: "wdr analyze loose report pair", input: "我们分析下65到73的报告存在哪些问题", intent: IntentWDRAnalyze, mode: ModeEvidence, skill: "wdranalyze", args: "65 73", forbidSkill: "wdr"},
		{name: "wdr latest", input: "分析最近的 WDR 报告", intent: IntentWDRAnalyze, mode: ModeEvidence, skill: "wdranalyze", args: "latest"},
		{name: "sql id tune", input: "sql id 581990336 如何优化", intent: IntentSQLTune, mode: ModeEvidence, skill: "sqltune", args: "581990336"},
		{name: "current diag", input: "当前数据库存在什么问题", intent: IntentCurrentDiag, mode: ModeLLM},
		{name: "blockers", input: "当前有没有阻塞", intent: IntentBlockers, mode: ModeDirectSkill, skill: "blocktree"},
		{name: "slow sql", input: "当前有哪些慢SQL", intent: IntentSlowSQL, mode: ModeDirectSkill, skill: "slowsql"},
		{name: "sessions", input: "当前有哪些活跃会话", intent: IntentSessions, mode: ModeDirectSkill, skill: "activesessions"},
		{name: "params", input: "shared_buffers 参数配置如何", intent: IntentParams, mode: ModeDirectSkill, skill: "params", args: "shared_buffers"},
		{name: "objects", input: "当前有哪些表膨胀", intent: IntentObjectStats, mode: ModeDirectSkill, skill: "objstats"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.input)
			if got.Intent != tc.intent || got.Mode != tc.mode || got.Skill != tc.skill {
				t.Fatalf("Classify(%q) = intent=%s mode=%s skill=%s reason=%s, want intent=%s mode=%s skill=%s", tc.input, got.Intent, got.Mode, got.Skill, got.Reason, tc.intent, tc.mode, tc.skill)
			}
			if tc.args != "" && got.Params["args"] != tc.args {
				t.Fatalf("Classify(%q) args=%v, want %q", tc.input, got.Params["args"], tc.args)
			}
			if tc.forbidSkill != "" && got.Skill == tc.forbidSkill {
				t.Fatalf("Classify(%q) used forbidden skill %q", tc.input, tc.forbidSkill)
			}
		})
	}
}
