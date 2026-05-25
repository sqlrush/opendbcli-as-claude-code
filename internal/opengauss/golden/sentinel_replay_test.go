package golden

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
)

func TestTier0SentinelBurstReplayGoldenCases(t *testing.T) {
	cases := []struct {
		id        string
		file      string
		wantCause sentinel.RootCauseType
		must      []string
	}{
		{
			id:        "OG-GOLDEN-SENTINEL-REPLAY-IO-001",
			file:      "testdata/sentinel/burst_io_temp_spill.json",
			wantCause: sentinel.CauseIOBottleneck,
			must:      []string{"IO瓶颈", "临时空间写入", "IO 等待", "987654321", "pg_stat_database", "work_mem", "回滚方案"},
		},
		{
			id:        "OG-GOLDEN-SENTINEL-REPLAY-LOCK-001",
			file:      "testdata/sentinel/burst_lock_chain.json",
			wantCause: sentinel.CauseLockContention,
			must:      []string{"锁等待阻塞", "PID 3456", "victims=11", "pg_locks", "pg_cancel_backend(3456)", "根因修复"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			data, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read replay: %v", err)
			}
			var report sentinel.BurstReport
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatalf("decode replay: %v", err)
			}
			report.Classification = sentinel.Classify(report)
			if report.Classification.Cause != tc.wantCause {
				t.Fatalf("cause=%s, want %s (%#v)", report.Classification.Cause, tc.wantCause, report.Classification)
			}
			out := sentinel.FormatEvidenceDiagnosis(report)
			for _, want := range tc.must {
				if !strings.Contains(out, want) {
					t.Fatalf("%s missing %q:\n%s", tc.id, want, out)
				}
			}
			for _, forbidden := range []string{"Evidence Builder", "WDR_REPORT_BEGIN", "v1_", "<SQL_ID>", "<pid>"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("%s contains forbidden %q:\n%s", tc.id, forbidden, out)
				}
			}
		})
	}
}
