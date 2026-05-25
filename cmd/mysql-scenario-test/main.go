/*-------------------------------------------------------------------------
 *
 * main.go
 *	  mysql-scenario-test: Automated MySQL 100 scenario testing against
 *	  a real MySQL. For each scenario: simulate fault → collect live
 *	  metrics → run rule engine → cleanup.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/mysql-scenario-test/main.go
 *
 *-------------------------------------------------------------------------
 */
// mysql-scenario-test: Automated MySQL 100 scenario testing against a real MySQL.
// For each scenario: simulate fault → collect live metrics → run rule engine → cleanup.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/sqlrush/opendb/internal/db"
	mysqldriver "github.com/sqlrush/opendb/internal/mysql/driver"
	"github.com/sqlrush/opendb/internal/mysql/ruleengine"
	"github.com/sqlrush/opendb/internal/mysql/sentinel"
	mysqlsqladv "github.com/sqlrush/opendb/internal/mysql/sqladvisor"
)

// keep sentinel import used
var _ sentinel.BurstReport

// ── Types ───────────────────────────────────────────────────────────────────

type scenario struct {
	ID       string
	Name     string
	Category string
	Skip     bool
	SkipNote string
	Setup    func(ctx context.Context, raw *sql.DB) (cleanup func(), err error)
}

type result struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Category   string             `json:"category"`
	Skipped    bool               `json:"skipped,omitempty"`
	SkipNote   string             `json:"skip_note,omitempty"`
	RuleFired  string             `json:"rule_fired"`
	Cause      string             `json:"cause"`
	Severity   string             `json:"severity"`
	Confidence int                `json:"confidence_pct"`
	Findings   []string           `json:"findings"`
	Actions    []string           `json:"actions"`
	HasSQL     bool               `json:"has_sql"`
	Secondary  string             `json:"secondary,omitempty"`
	AbsorbedN  int                `json:"absorbed"`
	MetricSnap map[string]float64 `json:"metrics,omitempty"`
}

// ── Globals ─────────────────────────────────────────────────────────────────

var (
	dsn    string
	rawDB  *sql.DB
	driver db.Driver
	engine *ruleengine.Engine
	eCfg   ruleengine.Config
)

func main() {
	host := e("MY_HOST", "127.0.0.1")
	port, _ := strconv.Atoi(e("MY_PORT", "3306"))
	user := e("MY_USER", "root")
	pass := e("MY_PASS", "YourMySQLPass123!")
	dbname := e("MY_DB", "mytest")

	dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&timeout=15s&readTimeout=30s&writeTimeout=30s&multiStatements=true",
		user, pass, host, port, dbname)

	var err error
	rawDB, err = sql.Open("mysql", dsn)
	fatal(err, "open raw db")
	rawDB.SetMaxOpenConns(5)
	defer rawDB.Close()

	cfg := db.ConnectionConfig{DBType: "mysql", Host: host, Port: port, User: user, Password: pass, Database: dbname}
	d, err := mysqldriver.NewDriver(cfg)
	fatal(err, "mysqldriver")
	driver = d
	defer d.Close()
	log("Connected: %s", d.ServerInfo().Version)

	// Rule engine
	community := &ruleengine.CommunityProvider{}
	jsonProv := ruleengine.NewJSONRuleProviderFromEmbedded("1.0.0")
	provider := ruleengine.NewCombinedProvider(community, jsonProv)
	eCfg = ruleengine.Config{OutputMode: ruleengine.OutputSQL, MaxQueryTimeout: 10, MaxTreeDepth: 20}
	executor := ruleengine.NewDynamicQueryExecutor(driver, provider.QueryRegistry(), eCfg.MaxQueryTimeout)
	engine = ruleengine.New(provider, executor, eCfg)

	// Wire SQL Advisor into Rule Engine for EXPLAIN-level enrichment
	advisor := mysqlsqladv.New(driver)
	engine.SetAdvisor(func(digest string) []ruleengine.AdvisorFinding {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		report, err := advisor.Analyze(ctx2, digest)
		if err != nil || len(report.Findings) == 0 {
			return nil
		}
		var out []ruleengine.AdvisorFinding
		for _, f := range report.Findings {
			af := ruleengine.AdvisorFinding{
				Digest:   digest,
				SQLText:  report.SQLText,
				Category: f.Category,
				Severity: f.Severity,
				Summary:  f.Summary,
				Detail:   f.Detail,
			}
			if len(f.Suggestions) > 0 {
				af.SQL = f.Suggestions[0].SQL
			}
			out = append(out, af)
		}
		return out
	})
	log("Rules: %d (with SQL Advisor)\n", engine.RuleCount())

	scenarios := allScenarios()
	log("Running %d scenarios...\n", len(scenarios))

	var results []result
	for i, sc := range scenarios {
		log("[%d/%d] %s %s", i+1, len(scenarios), sc.ID, sc.Name)
		r := runOne(sc)
		results = append(results, r)

		status := "MISS"
		if r.Skipped {
			status = "SKIP"
		} else if r.RuleFired != "" {
			status = "OK  "
		}
		log("  → [%s] rule=%-20s sev=%-4s cause=%s\n", status, r.RuleFired, r.Severity, trunc(r.Cause, 50))
	}

	// Output JSON
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(results)

	// Summary
	total, matched, skipped := len(results), 0, 0
	for _, r := range results {
		if r.Skipped {
			skipped++
		} else if r.RuleFired != "" {
			matched++
		}
	}
	log("\n=== Summary ===")
	log("Total=%d  RuleFired=%d  Skip=%d  NoMatch=%d", total, matched, skipped, total-matched-skipped)
}

func runOne(sc scenario) result {
	r := result{ID: sc.ID, Name: sc.Name, Category: sc.Category}
	if sc.Skip {
		r.Skipped = true
		r.SkipNote = sc.SkipNote
		return r
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Phase 1: take baseline snapshot of cumulative counters BEFORE setup
	baseline := snapshotCumulatives(ctx, driver)

	// Setup
	var cleanup func()
	if sc.Setup != nil {
		var err error
		cleanup, err = sc.Setup(ctx, rawDB)
		if err != nil {
			log("  setup err: %v", err)
		}
	}

	// Wait for background sessions to establish and MySQL stats to update
	time.Sleep(3 * time.Second)

	// Phase 2: collect live metrics with delta rates
	report := collectLive(ctx, driver, baseline)

	// Snapshot metrics for result
	r.MetricSnap = make(map[string]float64)
	for k, v := range report.Metrics {
		if v.Max > 0 {
			r.MetricSnap[k] = v.Avg
		}
	}

	// Run rule engine
	input := &ruleengine.DiagInput{Type: ruleengine.InputBurstReport, Report: report}
	output, _ := engine.DiagnoseDebug(input)

	if output != nil && output.Primary != nil {
		d := output.Primary
		r.RuleFired = d.RuleID
		r.Cause = d.Cause
		r.Severity = d.Severity.String()
		r.Confidence = int(d.Confidence * 100)
		for _, f := range d.Findings {
			r.Findings = append(r.Findings, f.Desc)
		}
		for _, a := range d.Actions {
			txt := fmt.Sprintf("[%s] %s", a.Type, a.Desc)
			if a.RawSQL != "" {
				txt += " | SQL: " + a.RawSQL
				r.HasSQL = true
			}
			r.Actions = append(r.Actions, txt)
		}
		if output.Secondary != nil {
			r.Secondary = output.Secondary.Cause
		}
		r.AbsorbedN = d.AbsorbedCount
	}

	// Cleanup
	if cleanup != nil {
		cleanup()
	}
	// Kill leftover background sessions
	killBgSessions(context.Background())

	return r
}

// killBgSessions kills all non-system, non-self sessions created by test
func killBgSessions(ctx context.Context) {
	rows, err := rawDB.QueryContext(ctx,
		`SELECT ID FROM information_schema.PROCESSLIST
		 WHERE USER = ? AND ID != CONNECTION_ID()
		 AND COMMAND != 'Daemon'
		 AND TIME > 1`, e("MY_USER", "root"))
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		rawDB.ExecContext(ctx, fmt.Sprintf("KILL %d", id))
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func e(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func fatal(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
		os.Exit(1)
	}
}
func log(f string, a ...any)        { fmt.Fprintf(os.Stderr, f+"\n", a...) }
func trunc(s string, n int) string   { if len(s) <= n { return s }; return s[:n] + "..." }

// bgExec opens a new MySQL connection and executes SQL in background, returns cancel func
func bgExec(sqlStr string) func() {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return func() {}
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.Close()
		close(started)
		conn.ExecContext(ctx, sqlStr)
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	return func() { cancel(); wg.Wait() }
}

// bgLoop opens a connection and repeatedly executes SQL until cancelled
func bgLoop(sqlStr string) func() {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return func() {}
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.Close()
		close(started)
		for ctx.Err() == nil {
			conn.ExecContext(ctx, sqlStr)
		}
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	return func() { cancel(); wg.Wait() }
}

// bgN opens N background connections running the same SQL once
func bgN(n int, sqlStr string) func() {
	var cleanups []func()
	for i := 0; i < n; i++ {
		cleanups = append(cleanups, bgExec(sqlStr))
	}
	return func() {
		for _, c := range cleanups {
			c()
		}
	}
}

// bgNLoop opens N background connections repeatedly running the same SQL
func bgNLoop(n int, sqlStr string) func() {
	var cleanups []func()
	for i := 0; i < n; i++ {
		cleanups = append(cleanups, bgLoop(sqlStr))
	}
	return func() {
		for _, c := range cleanups {
			c()
		}
	}
}

// bgSleep opens N connections that hold a transaction open (idle/sleeping)
func bgSleep(n int) func() {
	type pair struct {
		tx   *sql.Tx
		conn *sql.DB
	}
	var pairs []pair
	for i := 0; i < n; i++ {
		c, err := sql.Open("mysql", dsn)
		if err != nil {
			continue
		}
		c.SetMaxOpenConns(1)
		if err := c.Ping(); err != nil {
			c.Close()
			continue
		}
		tx, err := c.Begin()
		if err != nil {
			c.Close()
			continue
		}
		tx.Exec("SELECT 1")
		pairs = append(pairs, pair{tx: tx, conn: c})
	}
	return func() {
		for _, p := range pairs {
			p.tx.Rollback()
			p.conn.Close()
		}
	}
}

func exec(ctx context.Context, sqlStr string) {
	rawDB.ExecContext(ctx, sqlStr)
}

// ── All 100 Scenarios ───────────────────────────────────────────────────────

func allScenarios() []scenario {
	ss := make([]scenario, 0, 103)

	// ═══ Category 1: 锁与阻塞 (T001-T015) ═══

	ss = append(ss, scenario{ID: "T001", Name: "InnoDB行锁级联: blocker空闲", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=value+1 WHERE id=1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=value+1 WHERE id=1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T002", Name: "InnoDB行锁级联: blocker跑慢SQL", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=2; SELECT count(*) FROM big_table a CROSS JOIN big_table b LIMIT 1; SELECT SLEEP(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(15, "UPDATE lock_test SET value=2 WHERE id=2;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T003", Name: "InnoDB行锁级联: blocker等IO", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; SELECT SQL_NO_CACHE count(*) FROM big_table; UPDATE lock_test SET value=1 WHERE id=2; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(8, "UPDATE lock_test SET value=2 WHERE id=2;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T004", Name: "多层阻塞链", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=10; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgExec("BEGIN; UPDATE lock_test SET value=2 WHERE id=10; UPDATE lock_test SET value=2 WHERE id=11; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c3 := bgExec("BEGIN; UPDATE lock_test SET value=3 WHERE id=11; UPDATE lock_test SET value=3 WHERE id=12; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c4 := bgN(15, "UPDATE lock_test SET value=4 WHERE id=12;")
			return func() { c1(); c2(); c3(); c4() }, nil
		}})

	ss = append(ss, scenario{ID: "T005", Name: "死锁检测", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Sustained deadlock generation - keep producing deadlocks in background
			c := bgNLoop(5, "BEGIN; UPDATE lock_test SET value=1 WHERE id=20; DO SLEEP(0.3); UPDATE lock_test SET value=1 WHERE id=21; COMMIT;")
			c2 := bgNLoop(5, "BEGIN; UPDATE lock_test SET value=2 WHERE id=21; DO SLEEP(0.3); UPDATE lock_test SET value=2 WHERE id=20; COMMIT;")
			return func() { c(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T006", Name: "Metadata Lock阻塞DML", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Long tx holds SHARED MDL, then DDL needs EXCLUSIVE, blocks many readers
			c1 := bgExec("BEGIN; SELECT * FROM lock_test LIMIT 1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgExec("ALTER TABLE lock_test ADD COLUMN IF NOT EXISTS tmp_col INT;")
			time.Sleep(500 * time.Millisecond)
			// Many blocked readers create sustained lock_waits
			c3 := bgNLoop(15, "SELECT count(*) FROM lock_test;")
			return func() {
				c1(); c2(); c3()
				rawDB.ExecContext(ctx, "ALTER TABLE lock_test DROP COLUMN IF EXISTS tmp_col")
			}, nil
		}})

	ss = append(ss, scenario{ID: "T007", Name: "Gap Lock阻塞INSERT", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ; BEGIN; SELECT * FROM lock_test WHERE id BETWEEN 10 AND 20 FOR UPDATE; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(5, "INSERT INTO lock_test (id, value, status) VALUES (15, 999, 'gap');")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T008", Name: "Next-Key Lock并发INSERT死锁", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Sustained NK lock deadlocks — keep trying INSERT pairs that conflict
			c1 := bgNLoop(3, "SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ; BEGIN; INSERT IGNORE INTO lock_test (id, value, status) VALUES (FLOOR(RAND()*50)+200, 1, 'nk'); DO SLEEP(0.2); INSERT IGNORE INTO lock_test (id, value, status) VALUES (FLOOR(RAND()*50)+200, 1, 'nk'); COMMIT;")
			c2 := bgNLoop(3, "SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ; BEGIN; INSERT IGNORE INTO lock_test (id, value, status) VALUES (FLOOR(RAND()*50)+200, 2, 'nk'); DO SLEEP(0.2); INSERT IGNORE INTO lock_test (id, value, status) VALUES (FLOOR(RAND()*50)+200, 2, 'nk'); COMMIT;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T009", Name: "FTWRL阻塞全库", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("FLUSH TABLES WITH READ LOCK; SELECT SLEEP(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=value+1 WHERE id=1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T010", Name: "外键缺索引导致锁扩散", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Hold lock on parent while readers pile up
			c1 := bgExec("BEGIN; DELETE FROM parent_table WHERE id=1; SELECT SLEEP(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(10, "SELECT * FROM parent_table WHERE id=1 FOR SHARE;")
			c3 := bgNLoop(5, "INSERT INTO child_table (parent_id, data) VALUES (1, 'test');")
			return func() { c1(); c2(); c3() }, nil
		}})

	ss = append(ss, scenario{ID: "T011", Name: "Online DDL阻塞分析", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; SELECT * FROM lock_test LIMIT 1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgExec("ALTER TABLE lock_test ADD INDEX idx_tmp_status (status), ALGORITHM=INPLACE;")
			time.Sleep(500 * time.Millisecond)
			c3 := bgN(10, "SELECT count(*) FROM lock_test;")
			return func() {
				c1(); c2(); c3()
				rawDB.ExecContext(ctx, "ALTER TABLE lock_test DROP INDEX IF EXISTS idx_tmp_status")
			}, nil
		}})

	ss = append(ss, scenario{ID: "T012", Name: "自增锁争用", Category: "锁",
		Skip: true, SkipNote: "需要 innodb_autoinc_lock_mode=1 配置，无法运行时更改",
	})

	ss = append(ss, scenario{ID: "T013", Name: "LOCK TABLES阻塞并发", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("LOCK TABLES lock_test WRITE; SELECT SLEEP(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(10, "SELECT count(*) FROM lock_test;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T014", Name: "INSERT ON DUPLICATE KEY热点争用", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(20, "INSERT INTO lock_test (id, value, status) VALUES (FLOOR(RAND()*100)+1, FLOOR(RAND()*1000), 'upsert') ON DUPLICATE KEY UPDATE value=VALUES(value);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T015", Name: "锁等待超时重试风暴", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(10, "SET SESSION innodb_lock_wait_timeout=2; UPDATE lock_test SET value=2 WHERE id=1;")
			return func() { c1(); c2() }, nil
		}})

	// ═══ Category 2: SQL性能 (T016-T035) ═══

	ss = append(ss, scenario{ID: "T016", Name: "全表扫描: 缺索引", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE status = 'rare_value';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T017", Name: "全表扫描: 选择度低不该加索引", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE status = 'active';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T018", Name: "执行计划漂移: 统计信息过期", Category: "SQL",
		Skip: true, SkipNote: "需要大量数据变更+ANALYZE TABLE对比，模拟复杂",
	})

	ss = append(ss, scenario{ID: "T019", Name: "隐式类型转换索引失效", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// varchar_col has index but query uses number → implicit conversion → full scan
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE varchar_col = 12345;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T020", Name: "函数导致索引失效", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE DATE(created_at) = '2026-01-01';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T021", Name: "LIKE前模糊全表扫描", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE varchar_col LIKE '%keyword%';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T022", Name: "分区裁剪失效", Category: "SQL",
		Skip: true, SkipNote: "需要分区表 part_table，视测试环境而定",
	})

	ss = append(ss, scenario{ID: "T023", Name: "NOT IN子查询退化", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SELECT SQL_NO_CACHE * FROM big_table WHERE id NOT IN (SELECT id FROM lock_test);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T024", Name: "OFFSET大分页退化", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table ORDER BY id LIMIT 20 OFFSET 500000;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T025", Name: "多列索引顺序不当", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Index is (created_at, status) but query filters on status first
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE status='active' AND created_at > '2026-01-01';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T026", Name: "过多索引写入变慢", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(10, "INSERT INTO idx_test (col1,col2,col3,col4,col5,col6) VALUES (RAND(),RAND(),RAND(),RAND(),RAND(),RAND());")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T027", Name: "相关子查询N+1", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SELECT SQL_NO_CACHE *, (SELECT COUNT(*) FROM lock_test WHERE lock_test.id = big_table.id) FROM big_table LIMIT 10000;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T028", Name: "JOIN缺索引全表扫描", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SELECT SQL_NO_CACHE a.* FROM big_table a JOIN lock_test b ON a.status = b.status;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T029", Name: "大排序溢出到磁盘", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SET SESSION sort_buffer_size=262144; SELECT SQL_NO_CACHE * FROM big_table ORDER BY varchar_col, status LIMIT 50000;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T030", Name: "Hash Join溢出", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SET SESSION join_buffer_size=262144; SELECT SQL_NO_CACHE /*+ NO_INDEX(b) */ a.id FROM big_table a JOIN big_table b ON a.varchar_col = b.varchar_col LIMIT 100;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T031", Name: "SELECT * 回表开销", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table WHERE status = 'rare_value' LIMIT 10000;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T032", Name: "Filesort + Temporary双重开销", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE status, COUNT(*) AS cnt FROM big_table GROUP BY status ORDER BY cnt DESC;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T033", Name: "大表COUNT(*)无WHERE", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE COUNT(*) FROM big_table;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T034", Name: "大IN列表估算偏差", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Build a large IN list
			in := "1"
			for i := 2; i <= 5000; i++ {
				in += fmt.Sprintf(",%d", i)
			}
			c := bgNLoop(5, fmt.Sprintf("SELECT SQL_NO_CACHE * FROM big_table WHERE id IN (%s);", in))
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T035", Name: "窗口函数无索引", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SELECT SQL_NO_CACHE *, ROW_NUMBER() OVER (PARTITION BY status ORDER BY created_at DESC) AS rn FROM big_table LIMIT 50000;")
			return func() { c() }, nil
		}})

	// ═══ Category 3: InnoDB引擎 (T036-T045) ═══

	ss = append(ss, scenario{ID: "T036", Name: "Buffer Pool命中率下降: 大查询冲刷", Category: "InnoDB",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SELECT SQL_NO_CACHE * FROM big_table;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T037", Name: "Buffer Pool命中率持续低", Category: "InnoDB",
		Skip: true, SkipNote: "需要 innodb_buffer_pool_size 很小，无法运行时调小到足够低",
	})

	ss = append(ss, scenario{ID: "T038", Name: "Redo Log太小频繁Checkpoint", Category: "InnoDB",
		Skip: true, SkipNote: "需要 innodb_redo_log_capacity 配置很小，无法运行时更改",
	})

	ss = append(ss, scenario{ID: "T039", Name: "Undo Log膨胀: 长事务阻止purge", Category: "InnoDB",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Long transaction + continuous updates
			c1 := bgExec("BEGIN; SELECT * FROM big_table LIMIT 1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(5, "UPDATE big_table SET value=FLOOR(RAND()*1000) WHERE id=FLOOR(RAND()*10000)+1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T040", Name: "Change Buffer合并拖慢读", Category: "InnoDB",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Random inserts then reads trigger merge
			c1 := bgNLoop(5, "INSERT INTO idx_test (col1,col2,col3,col4,col5,col6) VALUES (RAND(),RAND(),RAND(),RAND(),RAND(),RAND());")
			time.Sleep(2 * time.Second)
			c2 := bgNLoop(3, "SELECT SQL_NO_CACHE * FROM idx_test WHERE col1 > RAND() LIMIT 100;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T041", Name: "Adaptive Hash Index争用", Category: "InnoDB",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(20, "SELECT SQL_NO_CACHE * FROM big_table WHERE id = FLOOR(RAND()*1000000)+1;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T042", Name: "Doublewrite Buffer写入延迟", Category: "InnoDB",
		Skip: true, SkipNote: "需要 HDD 存储模拟慢写入",
	})

	ss = append(ss, scenario{ID: "T043", Name: "临时表空间膨胀", Category: "InnoDB",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT SQL_NO_CACHE DISTINCT varchar_col, status, value FROM big_table ORDER BY value;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T044", Name: "InnoDB IO线程不足", Category: "InnoDB",
		Skip: true, SkipNote: "需要重启更改 innodb_read_io_threads",
	})

	ss = append(ss, scenario{ID: "T045", Name: "Page Cleaner跟不上脏页", Category: "InnoDB",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(20, "UPDATE big_table SET value=FLOOR(RAND()*1000) WHERE id=FLOOR(RAND()*100000)+1;")
			return func() { c() }, nil
		}})

	// ═══ Category 4: 内存与缓存 (T046-T055) ═══

	ss = append(ss, scenario{ID: "T046", Name: "sort_buffer过大OOM风险", Category: "内存",
		Skip: true, SkipNote: "OOM 测试不安全",
	})

	ss = append(ss, scenario{ID: "T047", Name: "join_buffer不足Block Nested Loop", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SET SESSION join_buffer_size=262144; SELECT SQL_NO_CACHE a.id FROM big_table a JOIN lock_test b ON a.status = b.status;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T048", Name: "tmp_table_size不足磁盘临时表", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "SET SESSION tmp_table_size=1048576; SET SESSION max_heap_table_size=1048576; SELECT SQL_NO_CACHE status, GROUP_CONCAT(varchar_col) FROM big_table GROUP BY status;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T049", Name: "Table Open Cache不足", Category: "内存",
		Skip: true, SkipNote: "需要大量表 + 小 table_open_cache",
	})

	ss = append(ss, scenario{ID: "T050", Name: "Thread Cache不足频繁创建线程", Category: "内存",
		Skip: true, SkipNote: "需要 thread_cache_size=0 配置",
	})

	ss = append(ss, scenario{ID: "T051", Name: "Query Cache争用(5.7)", Category: "内存",
		Skip: true, SkipNote: "MySQL 8.0已移除Query Cache",
	})

	ss = append(ss, scenario{ID: "T052", Name: "Prepared Statement泄漏", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Create many prepared statements without deallocating
			var cleanups []func()
			for i := 0; i < 20; i++ {
				c := bgExec(fmt.Sprintf("PREPARE stmt_%d FROM 'SELECT * FROM big_table WHERE id = ?'; SELECT SLEEP(20);", i))
				cleanups = append(cleanups, c)
			}
			return func() {
				for _, c := range cleanups {
					c()
				}
			}, nil
		}})

	ss = append(ss, scenario{ID: "T053", Name: "Buffer Pool Chunk Size内存浪费", Category: "内存",
		Skip: true, SkipNote: "需要特定 chunk_size/instances 配置",
	})

	ss = append(ss, scenario{ID: "T054", Name: "Binlog Cache溢出磁盘", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "BEGIN; UPDATE big_table SET value=FLOOR(RAND()*1000) WHERE id BETWEEN 1 AND 10000; COMMIT;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T055", Name: "内存碎片化RSS增长", Category: "内存",
		Skip: true, SkipNote: "需要长时间运行观察",
	})

	// ═══ Category 5: Binlog与复制 (T056-T065) ═══

	ss = append(ss, scenario{ID: "T056", Name: "Binlog写入速率过高", Category: "复制",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(10, "UPDATE big_table SET value=FLOOR(RAND()*1000) WHERE id=FLOOR(RAND()*100000)+1;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T057", Name: "sync_binlog提交延迟", Category: "复制",
		Skip: true, SkipNote: "需要 sync_binlog=1 + HDD，环境依赖",
	})

	ss = append(ss, scenario{ID: "T058", Name: "主从延迟: 大事务", Category: "复制",
		Skip: true, SkipNote: "需要主从复制环境",
	})

	ss = append(ss, scenario{ID: "T059", Name: "主从延迟: 从库无索引", Category: "复制",
		Skip: true, SkipNote: "需要主从复制环境",
	})

	ss = append(ss, scenario{ID: "T060", Name: "Binlog文件堆积占满磁盘", Category: "复制",
		Skip: true, SkipNote: "磁盘满测试不安全",
	})

	ss = append(ss, scenario{ID: "T061", Name: "GTID errant transaction", Category: "复制",
		Skip: true, SkipNote: "需要主从复制环境",
	})

	ss = append(ss, scenario{ID: "T062", Name: "半同步降级", Category: "复制",
		Skip: true, SkipNote: "需要半同步复制环境",
	})

	ss = append(ss, scenario{ID: "T063", Name: "STATEMENT格式数据不一致", Category: "复制",
		Skip: true, SkipNote: "需要主从复制环境",
	})

	ss = append(ss, scenario{ID: "T064", Name: "relay_log_space_limit阻塞", Category: "复制",
		Skip: true, SkipNote: "需要主从复制环境",
	})

	ss = append(ss, scenario{ID: "T065", Name: "多源复制冲突", Category: "复制",
		Skip: true, SkipNote: "需要多源复制环境",
	})

	// ═══ Category 6: 等待事件与延迟 (T066-T075) ═══

	ss = append(ss, scenario{ID: "T066", Name: "innodb_data_file IO延迟", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(20, "SELECT SQL_NO_CACHE * FROM big_table WHERE id = FLOOR(RAND()*1000000)+1;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T067", Name: "innodb_log_file Redo写入延迟", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(20, "INSERT INTO lock_test (value, status) VALUES (FLOOR(RAND()*1000), 'redo');")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T068", Name: "buf_pool_mutex争用", Category: "等待",
		Skip: true, SkipNote: "需要 innodb_buffer_pool_instances=1 配置",
	})

	ss = append(ss, scenario{ID: "T069", Name: "trx_mutex争用", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(50, "BEGIN; INSERT INTO lock_test (value, status) VALUES (1, 'trx'); COMMIT;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T070", Name: "AHI latch争用", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(30, "SELECT SQL_NO_CACHE * FROM big_table WHERE id = FLOOR(RAND()*100000)+1;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T071", Name: "表IO等待集中热表", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(10, "SELECT SQL_NO_CACHE * FROM big_table WHERE status = 'active' LIMIT 10000;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T072", Name: "LOCK_open表定义缓存争用", Category: "等待",
		Skip: true, SkipNote: "需要大量表 + 小 table_definition_cache",
	})

	ss = append(ss, scenario{ID: "T073", Name: "Binlog Group Commit等待", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(30, "INSERT INTO lock_test (value, status) VALUES (FLOOR(RAND()*1000), 'gc');")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T074", Name: "TPS骤降根因定位", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Lock a critical row → all transactions wait
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(20, "BEGIN; UPDATE lock_test SET value=2 WHERE id=1; COMMIT;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T075", Name: "CPU spin等待过度并发", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(60, "SELECT SQL_NO_CACHE * FROM lock_test WHERE id = FLOOR(RAND()*100)+1;")
			return func() { c() }, nil
		}})

	// ═══ Category 7: 连接与会话 (T076-T085) ═══

	ss = append(ss, scenario{ID: "T076", Name: "连接风暴无连接池", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Rapid connect/disconnect
			var wg sync.WaitGroup
			stop := make(chan struct{})
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							c, err := sql.Open("mysql", dsn)
							if err != nil {
								continue
							}
							c.SetMaxOpenConns(1)
							c.Ping()
							c.Close()
						}
					}
				}()
			}
			return func() { close(stop); wg.Wait() }, nil
		}})

	ss = append(ss, scenario{ID: "T077", Name: "连接接近max_connections", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgSleep(80)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T078", Name: "Sleep会话堆积", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgSleep(50)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T079", Name: "会话泄漏趋势", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgSleep(40)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T080", Name: "Aborted Connects冲高", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Try connections with wrong password
			badDSN := fmt.Sprintf("baduser:badpass@tcp(%s:%s)/%s?timeout=1s",
				e("MY_HOST", "127.0.0.1"), e("MY_PORT", "3306"), e("MY_DB", "mytest"))
			var wg sync.WaitGroup
			stop := make(chan struct{})
			for i := 0; i < 5; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							c, _ := sql.Open("mysql", badDSN)
							if c != nil {
								c.Ping()
								c.Close()
							}
							time.Sleep(100 * time.Millisecond)
						}
					}
				}()
			}
			return func() { close(stop); wg.Wait() }, nil
		}})

	ss = append(ss, scenario{ID: "T081", Name: "Aborted Clients冲高", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Open connections and force close (simulate network drop)
			var wg sync.WaitGroup
			stop := make(chan struct{})
			for i := 0; i < 5; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							c, err := sql.Open("mysql", dsn)
							if err != nil {
								continue
							}
							c.SetMaxOpenConns(1)
							c.Ping()
							c.Exec("SELECT 1")
							c.Close() // abrupt close mid-session
							time.Sleep(50 * time.Millisecond)
						}
					}
				}()
			}
			return func() { close(stop); wg.Wait() }, nil
		}})

	ss = append(ss, scenario{ID: "T082", Name: "open_files_limit不足", Category: "连接",
		Skip: true, SkipNote: "需要小 open_files_limit 配置",
	})

	ss = append(ss, scenario{ID: "T083", Name: "Active Sessions加速度突增", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(50, "SELECT SQL_NO_CACHE BENCHMARK(1000000, MD5('test'));")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T084", Name: "全库Hang分析", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("FLUSH TABLES WITH READ LOCK; SELECT SLEEP(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(20, "INSERT INTO lock_test (value, status) VALUES (1, 'hang');")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T085", Name: "ProxySQL配置不当", Category: "连接",
		Skip: true, SkipNote: "需要 ProxySQL 环境",
	})

	// ═══ Category 8: 配置与参数 (T086-T090) ═══

	ss = append(ss, scenario{ID: "T086", Name: "innodb_flush_log_at_trx_commit设错", Category: "配置",
		Skip: true, SkipNote: "需要特定配置",
	})

	ss = append(ss, scenario{ID: "T087", Name: "innodb_flush_method不当", Category: "配置",
		Skip: true, SkipNote: "需要重启更改",
	})

	ss = append(ss, scenario{ID: "T088", Name: "innodb_file_per_table=OFF", Category: "配置",
		Skip: true, SkipNote: "需要特定配置",
	})

	ss = append(ss, scenario{ID: "T089", Name: "lower_case_table_names问题", Category: "配置",
		Skip: true, SkipNote: "需要初始化时配置",
	})

	ss = append(ss, scenario{ID: "T090", Name: "sql_mode不严格", Category: "配置",
		Skip: true, SkipNote: "需要特定 sql_mode 配置",
	})

	// ═══ Category 9: 系统与运维 (T091-T100) ═══

	ss = append(ss, scenario{ID: "T091", Name: "慢查询日志未启用", Category: "运维",
		Skip: true, SkipNote: "需要 slow_query_log=OFF 配置",
	})

	ss = append(ss, scenario{ID: "T092", Name: "ERROR日志刷屏", Category: "运维",
		Skip: true, SkipNote: "需要触发大量错误日志",
	})

	ss = append(ss, scenario{ID: "T093", Name: "pt-osc导致负载升高", Category: "运维",
		Skip: true, SkipNote: "需要 pt-online-schema-change 工具",
	})

	ss = append(ss, scenario{ID: "T094", Name: "mysqldump长事务阻塞purge", Category: "运维",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Simulate mysqldump with --single-transaction: long consistent snapshot
			c1 := bgExec("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(5, "UPDATE big_table SET value=FLOOR(RAND()*1000) WHERE id=FLOOR(RAND()*10000)+1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T095", Name: "OPTIMIZE TABLE阻塞读写", Category: "运维",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("OPTIMIZE TABLE lock_test;")
			time.Sleep(200 * time.Millisecond)
			c2 := bgN(10, "SELECT * FROM lock_test WHERE id=1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T096", Name: "版本升级SQL兼容性", Category: "运维",
		Skip: true, SkipNote: "需要升级场景",
	})

	ss = append(ss, scenario{ID: "T097", Name: "Event Scheduler任务堆叠", Category: "运维",
		Skip: true, SkipNote: "需要 event_scheduler=ON + 配置 event",
	})

	ss = append(ss, scenario{ID: "T098", Name: "外键级联删除慢操作", Category: "运维",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Hold delete lock on parent + concurrent readers blocked
			c1 := bgExec("BEGIN; DELETE FROM parent_table WHERE id=2; SELECT SLEEP(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(10, "SELECT * FROM parent_table WHERE id=2 FOR SHARE;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T099", Name: "数据字典锁争用(大量表)", Category: "运维",
		Skip: true, SkipNote: "需要 50000+ 表环境",
	})

	ss = append(ss, scenario{ID: "T100", Name: "综合: IO慢+锁等待+Binlog堆积", Category: "运维",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Lock holder + IO pressure + many waiters
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=1; SELECT SLEEP(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgNLoop(15, "UPDATE lock_test SET value=2 WHERE id=1;")
			c3 := bgNLoop(5, "SELECT SQL_NO_CACHE * FROM big_table;")
			return func() { c1(); c2(); c3() }, nil
		}})

	return ss
}

// ── Cumulative counter baseline snapshot ──────────────────────────────────────

type cumulativeSnapshot struct {
	ts                  time.Time
	comCommit           float64
	comRollback         float64
	queries             float64
	innodbLogWritten    float64
	slowQueries         float64
	tableLocksWaited    float64
	selectFullJoin      float64
	innodbRowLockWaits  float64
	innodbDeadlocks     float64
	innodbRowLockTime   float64
	innodbLogWaits      float64
	bpWaitFree          float64
	abortedConnects     float64
	abortedClients      float64
	createdTmpDisk      float64
	createdTmpTables    float64
	sortMergePasses     float64
	handlerReadRnd      float64
	handlerReadRndNext  float64
}

func snapshotCumulatives(ctx context.Context, drv db.Driver) cumulativeSnapshot {
	s := cumulativeSnapshot{ts: time.Now()}
	qr, err := drv.Query(ctx, `SELECT
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Com_commit'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Com_rollback'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Queries'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_os_log_written'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Slow_queries'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Table_locks_waited'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Select_full_join'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_row_lock_waits'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_deadlocks'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_row_lock_time'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_log_waits'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_wait_free'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Aborted_connects'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Created_tmp_disk_tables'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Created_tmp_tables'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Sort_merge_passes'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Handler_read_rnd'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Handler_read_rnd_next'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Aborted_clients')`)
	if err != nil || len(qr.Rows) == 0 {
		return s
	}
	row := qr.Rows[0]
	if len(row) >= 19 {
		s.comCommit = toF(row[0]); s.comRollback = toF(row[1])
		s.queries = toF(row[2]); s.innodbLogWritten = toF(row[3])
		s.slowQueries = toF(row[4]); s.tableLocksWaited = toF(row[5])
		s.selectFullJoin = toF(row[6]); s.innodbRowLockWaits = toF(row[7])
		s.innodbDeadlocks = toF(row[8]); s.innodbRowLockTime = toF(row[9])
		s.innodbLogWaits = toF(row[10]); s.bpWaitFree = toF(row[11])
		s.abortedConnects = toF(row[12]); s.createdTmpDisk = toF(row[13])
		s.createdTmpTables = toF(row[14]); s.sortMergePasses = toF(row[15])
		s.handlerReadRnd = toF(row[16]); s.handlerReadRndNext = toF(row[17])
		s.abortedClients = toF(row[18])
	}
	return s
}

// ── Live Metric Collection (from real MySQL) ─────────────────────────────────

func collectLive(ctx context.Context, drv db.Driver, base cumulativeSnapshot) *sentinel.BurstReport {
	r := &sentinel.BurstReport{
		TriggerEvent:   sentinel.TriggerEvent{Metric: "threads_running", Strategy: sentinel.StrategyT1},
		DurationSec:    30,
		PeakActive:     5,
		BaselineActive: 2,
		StartTime:      time.Now().Add(-30 * time.Second),
		EndTime:        time.Now(),
		Metrics:        make(map[string]sentinel.MetricSummary),
		RawFrameCount:  30,
	}

	// Take "after" snapshot and compute delta rates
	now := snapshotCumulatives(ctx, drv)
	elapsed := now.ts.Sub(base.ts).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}
	rate := func(before, after float64) float64 { return (after - before) / elapsed }

	// Computed rate metrics (matching sentinel MetricName constants)
	sm(r, "tps", rate(base.comCommit+base.comRollback, now.comCommit+now.comRollback))
	sm(r, "qps", rate(base.queries, now.queries))
	sm(r, "redo_rate", rate(base.innodbLogWritten, now.innodbLogWritten)/1024)
	sm(r, "slow_queries_rate", rate(base.slowQueries, now.slowQueries))
	sm(r, "table_locks_waited_rate", rate(base.tableLocksWaited, now.tableLocksWaited))
	sm(r, "select_full_join_rate", rate(base.selectFullJoin, now.selectFullJoin))
	sm(r, "innodb_row_lock_waits", rate(base.innodbRowLockWaits, now.innodbRowLockWaits))
	sm(r, "deadlocks", rate(base.innodbDeadlocks, now.innodbDeadlocks))
	sm(r, "innodb_log_waits_rate", rate(base.innodbLogWaits, now.innodbLogWaits))
	sm(r, "innodb_buffer_pool_wait_free", rate(base.bpWaitFree, now.bpWaitFree))
	sm(r, "aborted_connects_rate", rate(base.abortedConnects, now.abortedConnects))
	sm(r, "aborted_clients_rate", rate(base.abortedClients, now.abortedClients))
	sm(r, "handler_read_rnd_rate", rate(base.handlerReadRnd+base.handlerReadRndNext, now.handlerReadRnd+now.handlerReadRndNext))

	// Avg row lock time (delta_time / delta_waits)
	lockWaitsDelta := now.innodbRowLockWaits - base.innodbRowLockWaits
	lockTimeDelta := now.innodbRowLockTime - base.innodbRowLockTime
	if lockWaitsDelta > 0 {
		sm(r, "avg_row_lock_time_ms", lockTimeDelta/lockWaitsDelta)
	}

	// tmp_disk_tables_pct from delta (correct metric name)
	tmpDiskDelta := now.createdTmpDisk - base.createdTmpDisk
	tmpTotalDelta := now.createdTmpTables - base.createdTmpTables
	if tmpTotalDelta > 0 {
		sm(r, "tmp_disk_tables_pct", tmpDiskDelta/tmpTotalDelta*100)
	}

	// Gauge metrics (point-in-time, not rates) from PROCESSLIST
	q(ctx, drv, r, `SELECT
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND != 'Sleep' AND COMMAND != 'Daemon' AND COMMAND != 'Binlog Dump') AS active,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND = 'Query' AND STATE NOT LIKE '%lock%' AND STATE NOT LIKE '%wait%') AS cpu,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND != 'Sleep' AND (STATE LIKE '%wait%io%' OR STATE LIKE '%read%' OR STATE LIKE '%write%')) AS io_wait,
		(SELECT COUNT(*) FROM performance_schema.data_lock_waits) AS lock_waits,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND != 'Sleep' AND COMMAND != 'Daemon' AND TIME > 2) AS long_q,
		(SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND = 'Sleep') AS sleep_sess,
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_running'),
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected'),
		ROUND((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') /
			GREATEST((SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME='max_connections'), 1) * 100, 1)`,
		"active_sessions", "cpu_sessions", "io_wait_sessions", "lock_waits",
		"long_queries", "sleep_sessions", "threads_running", "threads_connected", "connections_pct")

	// InnoDB gauge metrics
	q(ctx, drv, r, `SELECT
		ROUND((1 - (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_reads') /
			NULLIF((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_read_requests'), 0)) * 100, 2),
		ROUND((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_dirty') /
			NULLIF((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_total'), 0) * 100, 2),
		(SELECT COUNT FROM information_schema.INNODB_METRICS WHERE NAME='trx_rseg_history_len')`,
		"buffer_pool_hit_pct", "buffer_pool_dirty_pct", "history_list_length")

	// Replication
	q(ctx, drv, r, `SELECT IFNULL(
		(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Seconds_Behind_Source'), -1)`,
		"replication_lag")

	// Blocking chains
	qr, err := drv.Query(ctx, `SELECT
		b.BLOCKING_THREAD_ID,
		IFNULL((SELECT PROCESSLIST_USER FROM performance_schema.threads WHERE THREAD_ID = b.BLOCKING_THREAD_ID), ''),
		IFNULL((SELECT LEFT(PROCESSLIST_INFO, 200) FROM performance_schema.threads WHERE THREAD_ID = b.BLOCKING_THREAD_ID), ''),
		'row_lock',
		COUNT(*)
		FROM performance_schema.data_lock_waits b
		GROUP BY b.BLOCKING_THREAD_ID
		ORDER BY COUNT(*) DESC LIMIT 10`)
	if err == nil {
		for _, row := range qr.Rows {
			if len(row) >= 5 {
				r.BlockingChains = append(r.BlockingChains, sentinel.BlockingChain{
					BlockerThreadID: int64(toI(row[0])),
					BlockerUser:     toS(row[1]),
					BlockerQuery:    toS(row[2]),
					WaitType:        toS(row[3]),
					VictimCount:     toI(row[4]),
				})
			}
		}
	}
	sm(r, "blocker_count", float64(len(r.BlockingChains)))

	// Wait profile
	qr, err = drv.Query(ctx, `SELECT
		EVENT_NAME,
		LEFT(EVENT_NAME, LOCATE('/', EVENT_NAME, 6) - 1),
		COUNT_STAR,
		ROUND(SUM_TIMER_WAIT / 1e9, 2),
		ROUND(COUNT_STAR * 100.0 / NULLIF(
			(SELECT SUM(COUNT_STAR) FROM performance_schema.events_waits_summary_global_by_event_name
			 WHERE COUNT_STAR > 0 AND EVENT_NAME NOT LIKE 'idle%'), 0), 1)
		FROM performance_schema.events_waits_summary_global_by_event_name
		WHERE COUNT_STAR > 0 AND EVENT_NAME NOT LIKE 'idle%'
		ORDER BY COUNT_STAR DESC LIMIT 10`)
	if err == nil {
		for _, row := range qr.Rows {
			if len(row) >= 5 {
				r.WaitProfile = append(r.WaitProfile, sentinel.WaitBucket{
					EventName:  toS(row[0]),
					WaitClass:  toS(row[1]),
					Count:      toI(row[2]),
					TotalMs:    toF(row[3]),
					Percentage: toF(row[4]),
				})
			}
		}
	}

	// Top SQL
	qr, _ = drv.Query(ctx, `SELECT
		IFNULL(DIGEST, ''),
		LEFT(IFNULL(DIGEST_TEXT, ''), 300),
		COUNT_STAR,
		ROUND(AVG_TIMER_WAIT/1000000000, 2),
		ROUND(MAX_TIMER_WAIT/1000000000, 2),
		ROUND(SUM_LOCK_TIME/1000000000, 2)
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST IS NOT NULL AND LAST_SEEN > DATE_SUB(NOW(), INTERVAL 1 MINUTE)
		ORDER BY SUM_TIMER_WAIT DESC LIMIT 5`)
	if qr != nil {
		for _, row := range qr.Rows {
			if len(row) >= 6 {
				r.TopSQLs = append(r.TopSQLs, sentinel.SQLProfile{
					Digest:       toS(row[0]),
					SQLText:      toS(row[1]),
					ExecCount:    int64(toI(row[2])),
					AvgLatencyMs: toF(row[3]),
					MaxLatencyMs: toF(row[4]),
					LockTimeMs:   toF(row[5]),
				})
			}
		}
	}

	if v, ok := r.Metrics["threads_running"]; ok {
		r.PeakActive = int(v.Max)
	}

	// Aliases: core rules use short names, sentinel uses *_rate suffix.
	// Write rate values to both names so both core and JSON rules can match.
	aliases := map[string]string{
		// rate metrics → core rule aliases
		"select_full_join_rate":   "select_full_join",
		"table_locks_waited_rate": "table_locks_waited",
		"handler_read_rnd_rate":   "handler_read_rnd_next",
		"aborted_connects_rate":   "aborted_connects",
		"aborted_clients_rate":    "aborted_clients",
		"slow_queries_rate":       "slow_queries",
		"innodb_log_waits_rate":   "innodb_log_waits",
		// gauge metrics → alternative names
		"lock_waits":      "lock_wait_sessions",
		"threads_running": "active_sessions",
	}
	for src, dst := range aliases {
		if v, ok := r.Metrics[src]; ok {
			if _, exists := r.Metrics[dst]; !exists {
				r.Metrics[dst] = v
			}
		}
	}

	// Mark non-zero rate metrics as "spike" so extractSignals picks them up.
	// Without this, all metrics have Trend="steady" and won't generate signals.
	spikeThresholds := map[string]float64{
		"innodb_row_lock_waits": 1, "deadlocks": 0, "lock_waits": 3,
		"aborted_connects": 5, "aborted_connects_rate": 5,
		"aborted_clients": 5, "aborted_clients_rate": 5,
		"select_full_join": 5, "select_full_join_rate": 5,
		"handler_read_rnd_next": 10000, "handler_read_rnd_rate": 10000,
		"table_locks_waited": 1, "table_locks_waited_rate": 1,
		"slow_queries": 1, "slow_queries_rate": 1,
		"innodb_log_waits": 1, "innodb_log_waits_rate": 1,
		"avg_row_lock_time_ms": 10, "tmp_disk_tables_pct": 10,
		"history_list_length": 1000, "buffer_pool_dirty_pct": 10,
		"threads_running": 10, "active_sessions": 10,
		"long_queries": 1, "threads_connected": 20, "sleep_sessions": 20,
		"connections_pct": 50, "tps": 0, "qps": 0, "redo_rate": 100,
	}
	for name, threshold := range spikeThresholds {
		if m, ok := r.Metrics[name]; ok && m.Avg > threshold {
			m.Trend = "spike"
			r.Metrics[name] = m
		}
	}

	return r
}

func q(ctx context.Context, drv db.Driver, r *sentinel.BurstReport, sqlStr string, names ...string) {
	qr, err := drv.Query(ctx, sqlStr)
	if err != nil || len(qr.Rows) == 0 {
		return
	}
	row := qr.Rows[0]
	for i, name := range names {
		if i < len(row) {
			sm(r, name, toF(row[i]))
		}
	}
}

func sm(r *sentinel.BurstReport, name string, val float64) {
	r.Metrics[name] = sentinel.MetricSummary{Avg: val, Max: val, Min: val, Trend: "steady"}
}

func toF(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case int:
		return float64(n)
	case []uint8:
		f, _ := strconv.ParseFloat(string(n), 64)
		return f
	default:
		f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f
	}
}
func toI(v any) int    { return int(toF(v)) }
func toS(v any) string { if v == nil { return "" }; return fmt.Sprint(v) }
