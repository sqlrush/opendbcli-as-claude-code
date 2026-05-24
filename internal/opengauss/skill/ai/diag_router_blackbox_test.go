package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/dispatch"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

type fakeRouteSkill struct {
	name  string
	calls *[]string
}

func (s fakeRouteSkill) Name() string                       { return s.name }
func (s fakeRouteSkill) Description() string                { return "fake " + s.name }
func (s fakeRouteSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s fakeRouteSkill) Validate(skill.Params) error        { return nil }
func (s fakeRouteSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Command: s.name} }
func (s fakeRouteSkill) ToolDef() skill.ToolDef             { return skill.ToolDef{Name: s.name} }
func (s fakeRouteSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))
	if args != "" {
		*s.calls = append(*s.calls, s.name+":"+args)
	} else {
		*s.calls = append(*s.calls, s.name)
	}
	return &skill.Result{Type: skill.ResultText, Rendered: fmt.Sprintf("%s ok %s", s.name, args), Summary: s.name}, nil
}

func TestDiagnoseNaturalLanguageRouterBlackbox(t *testing.T) {
	cases := []struct {
		input string
		call  string
	}{
		{input: "当前有哪几个wdr报告", call: "wdr"},
		{input: "分析 快照76和77之间生成的wdr报告", call: "wdranalyze:76 77"},
		{input: "我们分析下65到73的报告存在哪些问题", call: "wdranalyze:65 73"},
		{input: "sql id 581990336 如何优化", call: "sqltune:581990336"},
		{input: "当前有没有阻塞", call: "blocktree"},
		{input: "当前有哪些慢SQL", call: "slowsql"},
		{input: "当前有哪些活跃会话", call: "activesessions"},
		{input: "shared_buffers 参数配置如何", call: "params:shared_buffers"},
		{input: "当前有哪些表膨胀", call: "objstats"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			drv := mock.NewMockDriver()
			sentinelSkill := NewSentinelSkill(drv, config.SentinelConfig{})
			ruleSkill := NewRuleSkill(sentinelSkill, drv)
			registry := skill.NewRegistry()
			exec := skill.NewExecutor(registry, security.NewGuard(skill.LevelReadOnly, nil))
			calls := []string{}
			for _, name := range []string{"wdr", "wdranalyze", "sqltune", "blocktree", "slowsql", "activesessions", "params", "objstats"} {
				registry.Register(fakeRouteSkill{name: name, calls: &calls})
			}
			diag := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), exec, registry, sentinelSkill, ruleSkill)
			registry.Register(diag)

			res, err := dispatch.NewDispatcher(registry, exec).Dispatch(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Dispatch error: %v", err)
			}
			if res == nil || !strings.Contains(res.Rendered, strings.Split(tc.call, ":")[0]+" ok") {
				t.Fatalf("unexpected result: %#v", res)
			}
			if len(calls) != 1 || calls[0] != tc.call {
				t.Fatalf("calls=%v, want [%s]", calls, tc.call)
			}
		})
	}
}
