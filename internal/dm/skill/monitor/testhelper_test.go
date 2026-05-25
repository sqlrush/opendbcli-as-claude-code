/*-------------------------------------------------------------------------
 *
 * testhelper_test.go
 *	  Test cases for testhelper.go (monitor package).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/testhelper_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
)

// sqlRouter routes incoming SQL to a list of (matchSubstr, response) tuples.
// 每条 skill 的 Execute 路径多次 Query 时, 给每条 SQL 配相应的返回数据.
type sqlMatcher struct {
	contains string // SQL 必须包含的子串 (大写匹配)
	result   *db.QueryResult
	err      error
}

// makeRoutedDriver 返回一个 mock driver, 按 matcher 顺序匹配 SQL 子串.
// 匹配大小写不敏感. 没匹中 → 返回空 QueryResult (不报错).
func makeRoutedDriver(matchers ...sqlMatcher) *mock.Driver {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		up := strings.ToUpper(sql)
		for _, m := range matchers {
			if strings.Contains(up, strings.ToUpper(m.contains)) {
				return m.result, m.err
			}
		}
		return &db.QueryResult{}, nil
	}
	return drv
}

// assertSummaryContains 断言 Rendered 末尾的 [summary] 段含某 key:value 行.
// 形如 `data_window: real-time snapshot`
func assertSummaryContains(t *testing.T, rendered, want string) {
	t.Helper()
	if !strings.Contains(rendered, "[summary]") {
		t.Fatalf("Rendered missing [summary] block. Got:\n%s", rendered)
	}
	if !strings.Contains(rendered, want) {
		t.Errorf("Rendered missing %q in [summary]. Got:\n%s", want, rendered)
	}
}

// assertNotEmpty 断言 Rendered 非空 + 不是 nil.
func assertNotEmpty(t *testing.T, rendered string) {
	t.Helper()
	if strings.TrimSpace(rendered) == "" {
		t.Errorf("Rendered is empty")
	}
	if strings.Contains(rendered, "<nil>") {
		t.Errorf("Rendered contains <nil>: %s", rendered)
	}
}
