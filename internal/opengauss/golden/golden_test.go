package golden

import (
	"strings"
	"testing"
)

func TestTier0IntentGoldenCases(t *testing.T) {
	corpus, err := LoadFile("testdata/tier0.yaml")
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(corpus.Cases) < 8 {
		t.Fatalf("expected a real tier0 corpus, got %d cases", len(corpus.Cases))
	}

	for _, tc := range corpus.Cases {
		tc := tc
		if tc.Tier != 0 || tc.Expected.Intent == "" {
			continue
		}
		if tc.RequiresDB || tc.RequiresLLM {
			t.Fatalf("%s: tier0 intent cases must not require DB or LLM", tc.ID)
		}
		t.Run(tc.ID, func(t *testing.T) {
			result := EvaluateIntentCase(tc)
			for _, finding := range result.Findings {
				t.Error(finding.Error())
			}
		})
	}
}

func TestOutputContractEvaluator(t *testing.T) {
	c := Case{
		ID: "contract",
		Expected: Expected{
			RequiredStrings:  []string{"报告元信息", "WDR 报告分析"},
			ForbiddenStrings: []string{"WDR_REPORT_BEGIN", "Evidence Builder", "v1_"},
		},
	}
	output := "## WDR 报告分析\n\n## 报告元信息\n- 报告格式: wdranalyze-report/v1\n"
	result := EvaluateOutputContract(c, output)
	if !result.Passed() {
		t.Fatalf("valid contract failed: %#v", result.Findings)
	}

	bad := output + "\nEvidence Builder\n"
	result = EvaluateOutputContract(c, bad)
	if result.Passed() {
		t.Fatal("forbidden output should fail")
	}
	if got := result.Findings[0].Got; !strings.Contains(got, "Evidence Builder") {
		t.Fatalf("unexpected finding: %#v", result.Findings)
	}
}

func TestRubricContractEvaluator(t *testing.T) {
	c := Case{
		ID: "rubric",
		Quality: Quality{Rubric: []RubricCheck{
			{Name: "all", RequiredAll: []string{"health", "waits"}},
			{Name: "any", RequiredAny: []string{"建议", "动作"}},
			{Name: "forbidden", ForbiddenAny: []string{"Evidence Builder"}},
		}},
	}
	good := "health ok, waits ok。建议复查。"
	if findings := EvaluateRubricContract(c, good); len(findings) != 0 {
		t.Fatalf("good rubric failed: %#v", findings)
	}
	bad := "health ok。Evidence Builder"
	findings := EvaluateRubricContract(c, bad)
	if len(findings) != 3 {
		t.Fatalf("bad rubric findings=%#v, want 3", findings)
	}
}
