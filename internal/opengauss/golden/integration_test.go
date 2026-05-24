package golden

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTier1DBGoldenCases(t *testing.T) {
	conn := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_CONN"))
	if conn == "" {
		t.Skip("set OPENDB_GOLDEN_CONN to run DB-backed golden cases")
	}
	bin := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_BIN"))
	if bin == "" {
		bin = "dbaa"
	}
	enableLLM := os.Getenv("OPENDB_GOLDEN_ENABLE_LLM") == "1"
	defaultTimeout, timeoutOverridden := goldenTimeout(90 * time.Second)
	caseFilter := makeSet(splitCSV(os.Getenv("OPENDB_GOLDEN_CASES")))

	corpus, err := LoadFile("testdata/tier1.yaml")
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("tier1 corpus is empty")
	}

	for _, tc := range corpus.Cases {
		tc := tc
		if tc.Tier != 1 {
			continue
		}
		if len(caseFilter) > 0 && !caseFilter[tc.ID] {
			continue
		}
		if tc.RequiresLLM && !enableLLM {
			t.Run(tc.ID, func(t *testing.T) {
				t.Skip("set OPENDB_GOLDEN_ENABLE_LLM=1 to run LLM-backed DB golden case")
			})
			continue
		}
		t.Run(tc.ID, func(t *testing.T) {
			if tc.Expected.Intent != "" {
				for _, finding := range EvaluateIntentCase(tc).Findings {
					t.Error(finding.Error())
				}
			}
			timeout := caseTimeout(tc, defaultTimeout, timeoutOverridden)
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			input := tc.Command
			if input == "" {
				input = tc.Input
			}
			cmd := exec.CommandContext(ctx, bin, "-c", conn, input)
			out, err := cmd.CombinedOutput()
			output := string(out)
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("command timed out after %s: %s -c %s %q\n%s", timeout, bin, conn, input, output)
			}
			if err != nil {
				t.Fatalf("command failed: %v\n%s", err, output)
			}
			for _, finding := range EvaluateOutputContract(tc, output).Findings {
				t.Error(finding.Error())
			}
			for _, finding := range EvaluateRubricContract(tc, output) {
				t.Error(finding.Error())
			}
		})
	}
}

func goldenTimeout(def time.Duration) (time.Duration, bool) {
	v := strings.TrimSpace(os.Getenv("OPENDB_GOLDEN_TIMEOUT_SEC"))
	if v == "" {
		return def, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def, false
	}
	return time.Duration(n) * time.Second, true
}

func caseTimeout(c Case, def time.Duration, overridden bool) time.Duration {
	if overridden || c.Quality.MaxLatencyMS <= 0 {
		return def
	}
	return time.Duration(c.Quality.MaxLatencyMS) * time.Millisecond
}
