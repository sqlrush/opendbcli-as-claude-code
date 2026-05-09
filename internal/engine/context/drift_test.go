/*-------------------------------------------------------------------------
 *
 * drift_test.go
 *	  Test cases for drift.go (context package): TestDetectDrift,
 *	  TestDropHistoryOnDrift, TestJaccardSimilarity.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/drift_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import "testing"

func TestDetectDrift(t *testing.T) {
	cases := []struct {
		name    string
		history []Message
		next    string
		want    bool
	}{
		{
			name:    "empty history → no drift",
			history: nil,
			next:    "查询变慢了",
			want:    false,
		},
		{
			name: "follow-up on same topic → no drift",
			history: []Message{
				{Role: "user", Content: "数据库查询变慢，看看索引情况"},
				{Role: "assistant", Content: "..."},
			},
			next: "继续看索引的膨胀",
			want: false,
		},
		{
			name: "totally different topic → drift",
			history: []Message{
				{Role: "user", Content: "数据库查询变慢，看看索引情况"},
				{Role: "assistant", Content: "..."},
			},
			next: "换个话题，检查 WAL 归档状态",
			want: true,
		},
		{
			name: "ignore meta messages",
			history: []Message{
				{Role: "user", Content: "<system-reminder>x</system-reminder>", IsMeta: true},
				{Role: "user", Content: "WAL 增长速度快"},
				{Role: "assistant", Content: "..."},
			},
			next: "WAL 归档没启用是不是有风险",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectDrift(tc.history, tc.next)
			if got != tc.want {
				t.Errorf("DetectDrift = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDropHistoryOnDrift(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "索引膨胀"},
		{Role: "assistant", Content: "a"},
	}
	if got := DropHistoryOnDrift(history, "索引还有什么优化"); len(got) != 2 {
		t.Errorf("same topic should preserve history, got %d", len(got))
	}
	if got := DropHistoryOnDrift(history, "换个话题，逻辑复制延迟"); got != nil {
		t.Errorf("drift should drop history, got %d msgs", len(got))
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := tokenize("数据库连接数快满了")
	b := tokenize("数据库查询变慢")
	sim := jaccardSimilarity(a, b)
	if sim <= 0 || sim >= 1 {
		t.Errorf("partial overlap should yield sim in (0,1), got %v", sim)
	}
}
