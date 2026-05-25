/*-------------------------------------------------------------------------
 *
 * evaluator.go
 *	  Deterministic evaluators used by OpenGauss/GaussDB golden tests.
 *
 *-------------------------------------------------------------------------
 */
package golden

import (
	"fmt"
	"strings"

	ogintent "github.com/sqlrush/opendb/internal/opengauss/intent"
)

type Finding struct {
	CaseID string
	Check  string
	Want   string
	Got    string
}

func (f Finding) Error() string {
	return fmt.Sprintf("%s %s: got %q, want %q", f.CaseID, f.Check, f.Got, f.Want)
}

type Result struct {
	Case     Case
	Findings []Finding
}

func (r Result) Passed() bool {
	return len(r.Findings) == 0
}

func EvaluateIntentCase(c Case) Result {
	got := ogintent.Classify(c.Input)
	result := Result{Case: c}
	add := func(check, gotValue, wantValue string) {
		if wantValue != "" && gotValue != wantValue {
			result.Findings = append(result.Findings, Finding{CaseID: c.ID, Check: check, Got: gotValue, Want: wantValue})
		}
	}
	add("intent", got.Intent, c.Expected.Intent)
	add("route_mode", string(got.Mode), c.Expected.RouteMode)
	add("skill", got.Skill, c.Expected.Skill)
	if c.Expected.Args != "" {
		arg, _ := got.Params["args"].(string)
		add("args", arg, c.Expected.Args)
	}
	for _, forbidden := range c.Expected.ForbiddenTools {
		if got.Skill == forbidden {
			result.Findings = append(result.Findings, Finding{CaseID: c.ID, Check: "forbidden_tool", Got: got.Skill, Want: "not " + forbidden})
		}
	}
	for _, required := range c.Expected.RequiredTools {
		if got.Skill != required {
			result.Findings = append(result.Findings, Finding{CaseID: c.ID, Check: "required_tool", Got: got.Skill, Want: required})
		}
	}
	return result
}

func EvaluateOutputContract(c Case, output string) Result {
	result := Result{Case: c}
	for _, want := range c.Expected.RequiredStrings {
		if !strings.Contains(output, want) {
			result.Findings = append(result.Findings, Finding{CaseID: c.ID, Check: "required_string", Got: "(missing)", Want: want})
		}
	}
	for _, notWant := range c.Expected.ForbiddenStrings {
		if strings.Contains(output, notWant) {
			result.Findings = append(result.Findings, Finding{CaseID: c.ID, Check: "forbidden_string", Got: notWant, Want: "absent"})
		}
	}
	return result
}

func EvaluateRubricContract(c Case, output string) []Finding {
	if len(c.Quality.Rubric) == 0 {
		return nil
	}
	lowerOutput := strings.ToLower(output)
	findings := make([]Finding, 0)
	for _, check := range c.Quality.Rubric {
		checkName := check.Name
		if checkName == "" {
			checkName = "unnamed"
		}
		for _, want := range check.RequiredAll {
			if !containsCaseFold(lowerOutput, want) {
				findings = append(findings, Finding{CaseID: c.ID, Check: "rubric:" + checkName, Got: "(missing)", Want: want})
			}
		}
		if len(check.RequiredAny) > 0 && !containsAnyCaseFold(lowerOutput, check.RequiredAny) {
			findings = append(findings, Finding{CaseID: c.ID, Check: "rubric:" + checkName, Got: "(missing any)", Want: strings.Join(check.RequiredAny, " | ")})
		}
		for _, notWant := range check.ForbiddenAny {
			if containsCaseFold(lowerOutput, notWant) {
				findings = append(findings, Finding{CaseID: c.ID, Check: "rubric:" + checkName, Got: notWant, Want: "absent"})
			}
		}
	}
	return findings
}

func containsAnyCaseFold(lowerHaystack string, needles []string) bool {
	for _, needle := range needles {
		if containsCaseFold(lowerHaystack, needle) {
			return true
		}
	}
	return false
}

func containsCaseFold(lowerHaystack, needle string) bool {
	return strings.Contains(lowerHaystack, strings.ToLower(needle))
}
