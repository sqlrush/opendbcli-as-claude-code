package golden

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type modelCaseResult struct {
	Model    string
	CaseID   string
	Input    string
	Score    int
	Grade    string
	Status   string
	Elapsed  time.Duration
	Findings []Finding
	Error    string
	Excerpt  string
}

func TestTier2ModelGoldenMatrix(t *testing.T) {
	conn := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_CONN"))
	models := splitCSV(os.Getenv("OPENDB_GOLDEN_MODELS"))
	if conn == "" || len(models) == 0 {
		t.Skip("set OPENDB_GOLDEN_CONN and OPENDB_GOLDEN_MODELS to run model matrix golden cases")
	}
	bin := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_BIN"))
	if bin == "" {
		bin = "dbaa"
	}
	baseConfig, err := goldenConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	selectedCases := splitCSV(os.Getenv("OPENDB_GOLDEN_CASES"))
	caseFilter := makeSet(selectedCases)
	defaultTimeout, timeoutOverridden := goldenTimeout(180 * time.Second)

	corpus, err := LoadFile("testdata/tier2.yaml")
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	cases := filterTierCases(corpus.Cases, 2, caseFilter)
	if len(cases) == 0 {
		t.Fatal("no tier2 cases selected")
	}

	var results []modelCaseResult
	for _, modelName := range models {
		modelName := modelName
		t.Run(modelName, func(t *testing.T) {
			tempConfig, cleanup, err := writeTempConfigWithActiveModel(baseConfig, modelName)
			if err != nil {
				t.Fatalf("prepare temp config for %s: %v", modelName, err)
			}
			defer cleanup()
			for _, tc := range cases {
				tc := tc
				t.Run(tc.ID, func(t *testing.T) {
					res := runModelCase(t, bin, conn, tempConfig, modelName, tc, caseTimeout(tc, defaultTimeout, timeoutOverridden))
					results = append(results, res)
					if res.Score < tc.Quality.MinScore {
						t.Fatalf("score %d below minimum %d: %s\n%s", res.Score, tc.Quality.MinScore, res.Error, findingsText(res.Findings))
					}
				})
			}
		})
	}

	reportPath := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_REPORT"))
	if reportPath == "" {
		reportPath = defaultModelMatrixReportPath(conn)
	}
	if err := writeModelMatrixReport(reportPath, results); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("model matrix report: %s", reportPath)
}

func runModelCase(t *testing.T, bin, conn, configPath, modelName string, tc Case, timeout time.Duration) modelCaseResult {
	t.Helper()
	input := tc.Command
	if input == "" {
		input = tc.Input
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-c", conn, input)
	cmd.Env = append(os.Environ(), "OPENDB_CONFIG="+configPath, "NO_COLOR=1")
	started := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(started)
	output := string(out)
	res := modelCaseResult{Model: modelName, CaseID: tc.ID, Input: input, Elapsed: elapsed, Excerpt: compactExcerpt(output, 900)}
	if ctx.Err() == context.DeadlineExceeded {
		res.Status = "timeout"
		res.Error = fmt.Sprintf("timed out after %s", timeout)
	} else if err != nil {
		res.Status = "error"
		res.Error = err.Error()
	} else {
		res.Status = "ok"
	}
	res.Findings = EvaluateOutputContract(tc, output).Findings
	res.Findings = append(res.Findings, EvaluateRubricContract(tc, output)...)
	res.Score = scoreModelCase(tc, res)
	res.Grade = grade(res.Score)
	return res
}

func scoreModelCase(c Case, r modelCaseResult) int {
	score := 100
	if r.Status == "timeout" {
		score -= 45
	} else if r.Status == "error" {
		score -= 35
	}
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Check, "rubric:") {
			score -= rubricPoints(c, strings.TrimPrefix(f.Check, "rubric:"))
			continue
		}
		switch f.Check {
		case "required_string":
			score -= 12
		case "forbidden_string":
			score -= 25
		default:
			score -= 10
		}
	}
	if c.Quality.MaxLatencyMS > 0 && r.Elapsed > time.Duration(c.Quality.MaxLatencyMS)*time.Millisecond {
		score -= 8
	}
	if score < 0 {
		return 0
	}
	return score
}

func rubricPoints(c Case, name string) int {
	for _, check := range c.Quality.Rubric {
		if check.Name == name {
			if check.Points > 0 {
				return check.Points
			}
			return 10
		}
	}
	return 10
}

func grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 85:
		return "A-"
	case score >= 80:
		return "B+"
	case score >= 75:
		return "B"
	case score >= 70:
		return "B-"
	case score >= 60:
		return "C"
	default:
		return "Fail"
	}
}

func writeModelMatrixReport(path string, results []modelCaseResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# OpenGauss/GaussDB Model Golden Matrix\n\n")
	b.WriteString("Generated: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")
	b.WriteString("Scoring: command status + timeout + required/forbidden strings + DBA rubric checks. LLMs are evaluated by machine rules; this is not LLM self-judging.\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString("| Model | Cases | Avg Score | Grade | Failed |\n")
	b.WriteString("|---|---:|---:|---|---:|\n")
	for _, s := range summarizeByModel(results) {
		b.WriteString(fmt.Sprintf("| %s | %d | %.1f | %s | %d |\n", s.model, s.count, s.avg, grade(int(s.avg+0.5)), s.failed))
	}
	b.WriteString("\n## Scenario Scorecard\n\n")
	b.WriteString("| Model | 当前诊断 | WDR分析 | SQLTune | 综合 |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range summarizeScenarioScorecard(results) {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", row.model, row.currentDiag, row.wdrAnalyze, row.sqlTune, row.overall))
	}
	b.WriteString("\n## Details\n\n")
	b.WriteString("| Model | Case | Score | Grade | Status | Elapsed | Findings |\n")
	b.WriteString("|---|---|---:|---|---|---:|---|\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %s | %s | %.1fs | %s |\n", r.Model, r.CaseID, r.Score, r.Grade, r.Status, r.Elapsed.Seconds(), markdownEscape(findingsText(r.Findings))))
	}
	b.WriteString("\n## Output Excerpts\n\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("### %s / %s\n\n", r.Model, r.CaseID))
		if r.Error != "" {
			b.WriteString("Error: `" + markdownEscape(r.Error) + "`\n\n")
		}
		b.WriteString("```text\n" + r.Excerpt + "\n```\n\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

type scenarioScorecardRow struct {
	model       string
	currentDiag string
	wdrAnalyze  string
	sqlTune     string
	overall     string
}

type modelSummary struct {
	model  string
	count  int
	avg    float64
	failed int
}

func summarizeScenarioScorecard(results []modelCaseResult) []scenarioScorecardRow {
	byModelScenario := map[string]map[string][]modelCaseResult{}
	for _, r := range results {
		model := r.Model
		scenario := scenarioLabel(r.CaseID)
		if byModelScenario[model] == nil {
			byModelScenario[model] = map[string][]modelCaseResult{}
		}
		byModelScenario[model][scenario] = append(byModelScenario[model][scenario], r)
	}
	models := make([]string, 0, len(byModelScenario))
	for model := range byModelScenario {
		models = append(models, model)
	}
	sort.Strings(models)
	out := make([]scenarioScorecardRow, 0, len(models))
	for _, model := range models {
		scenarios := byModelScenario[model]
		out = append(out, scenarioScorecardRow{
			model:       model,
			currentDiag: gradeForScenario(scenarios["current_diag"]),
			wdrAnalyze:  gradeForScenario(scenarios["wdr_analyze"]),
			sqlTune:     gradeForScenario(scenarios["sqltune"]),
			overall:     gradeForScenario(flattenScenarioResults(scenarios)),
		})
	}
	return out
}

func scenarioLabel(caseID string) string {
	lower := strings.ToLower(caseID)
	switch {
	case strings.Contains(lower, "wdr"):
		return "wdr_analyze"
	case strings.Contains(lower, "sqltune") || strings.Contains(lower, "sql_id") || strings.Contains(lower, "sqlid"):
		return "sqltune"
	default:
		return "current_diag"
	}
}

func gradeForScenario(items []modelCaseResult) string {
	if len(items) == 0 {
		return "-"
	}
	var sum int
	for _, item := range items {
		sum += item.Score
	}
	return grade(int(float64(sum)/float64(len(items)) + 0.5))
}

func flattenScenarioResults(scenarios map[string][]modelCaseResult) []modelCaseResult {
	var out []modelCaseResult
	for _, items := range scenarios {
		out = append(out, items...)
	}
	return out
}

func defaultModelMatrixReportPath(conn string) string {
	name := strings.TrimSpace(conn)
	if name == "" {
		name = "default"
	}
	name = strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(name)
	return filepath.Join(os.TempDir(), "opendb-golden-reports", "model-matrix-"+name+".md")
}

func summarizeByModel(results []modelCaseResult) []modelSummary {
	byModel := map[string][]modelCaseResult{}
	for _, r := range results {
		byModel[r.Model] = append(byModel[r.Model], r)
	}
	models := make([]string, 0, len(byModel))
	for model := range byModel {
		models = append(models, model)
	}
	sort.Strings(models)
	out := make([]modelSummary, 0, len(models))
	for _, model := range models {
		var sum, failed int
		for _, r := range byModel[model] {
			sum += r.Score
			if r.Score < 85 || r.Status != "ok" {
				failed++
			}
		}
		out = append(out, modelSummary{model: model, count: len(byModel[model]), avg: float64(sum) / float64(len(byModel[model])), failed: failed})
	}
	return out
}

func goldenConfigPath() (string, error) {
	for _, path := range []string{
		strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_CONFIG")),
		strings.TrimSpace(os.Getenv("OPENDB_CONFIG")),
	} {
		if path != "" {
			return path, nil
		}
	}
	if home := strings.TrimSpace(os.Getenv("DBAA_HOME")); home != "" {
		return filepath.Join(home, "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dbaa", "config.yaml"), nil
}

func writeTempConfigWithActiveModel(basePath, modelName string) (string, func(), error) {
	data, err := os.ReadFile(basePath)
	if err != nil {
		return "", nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return "", nil, err
	}
	setYAMLScalar(&node, "active_model", modelName)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return "", nil, err
	}
	if err := enc.Close(); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "opendb-golden-model-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { os.RemoveAll(dir) }, nil
}

func setYAMLScalar(node *yaml.Node, key, value string) {
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		setYAMLScalar(node.Content[0], key, value)
		return
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1].Kind = yaml.ScalarNode
			node.Content[i+1].Tag = "!!str"
			node.Content[i+1].Value = value
			return
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func filterTierCases(cases []Case, tier int, include map[string]bool) []Case {
	out := make([]Case, 0, len(cases))
	for _, c := range cases {
		if c.Tier != tier {
			continue
		}
		if len(include) > 0 && !include[c.ID] {
			continue
		}
		out = append(out, c)
	}
	return out
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func makeSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

func findingsText(findings []Finding) string {
	if len(findings) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, f.Check+":"+f.Want)
	}
	return strings.Join(parts, "; ")
}

func compactExcerpt(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "\n... (truncated)"
}

func markdownEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
