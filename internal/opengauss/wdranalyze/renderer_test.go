package wdranalyze

import (
	"strings"
	"testing"
	"time"
)

func TestRenderFooterMetadataAndUserFacingEvidence(t *testing.T) {
	a := &Analysis{
		Report: &WDRReport{
			Header: ReportHeader{
				InstanceHost:    "og5",
				SnapshotIDStart: 65,
				SnapshotIDEnd:   73,
				WindowStart:     time.Date(2026, 5, 22, 13, 56, 11, 0, time.UTC),
				WindowEnd:       time.Date(2026, 5, 23, 12, 35, 45, 0, time.UTC),
			},
			SectionScores: []SectionScore{{Name: "Instance Efficiency", Level: SectionRisk}},
			RawSections:   map[string]string{SectionInstanceEfficiency: "Buffer Hit %=39.61"},
		},
		GeneratedAt: time.Date(2026, 5, 23, 19, 27, 11, 0, time.UTC),
		Duration:    time.Second,
		ReportPath:  "/tmp/wdr-65-73.md",
	}
	out := Render(a)
	for _, want := range []string{"## 诊断边界", "证据置信度", "## 结构化证据", "## 报告元信息", "生成时间: 2026-05-23 19:27:11", "报告格式: wdranalyze-report/v1", "报告文件: `/tmp/wdr-65-73.md`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"Evidence Builder", "_报告生成于", "v1_"} {
		if strings.Contains(out, bad) {
			t.Fatalf("Render contains internal/ambiguous footer token %q:\n%s", bad, out)
		}
	}
}
