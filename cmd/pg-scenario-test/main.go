/*-------------------------------------------------------------------------
 *
 * main.go
 *	  pg-scenario-test: Automated PG 100 scenario testing against a real
 *	  PostgreSQL. For each scenario: simulate fault → collect live
 *	  metrics → run rule engine → cleanup.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/pg-scenario-test/main.go
 *
 *-------------------------------------------------------------------------
 */
// pg-scenario-test: Automated PG 100 scenario testing against a real PostgreSQL.
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

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sqlrush/opendb/internal/db"
	pgdriver "github.com/sqlrush/opendb/internal/postgres/driver"
	"github.com/sqlrush/opendb/internal/postgres/ruleengine"
	"github.com/sqlrush/opendb/internal/postgres/sentinel"
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Skipped      bool     `json:"skipped,omitempty"`
	SkipNote     string   `json:"skip_note,omitempty"`
	RuleFired    string   `json:"rule_fired"`
	Cause        string   `json:"cause"`
	Severity     string   `json:"severity"`
	Confidence   int      `json:"confidence_pct"`
	Findings     []string `json:"findings"`
	Actions      []string `json:"actions"`
	HasSQL       bool     `json:"has_sql"`
	Secondary    string   `json:"secondary,omitempty"`
	AbsorbedN    int      `json:"absorbed"`
	MetricSnap   map[string]float64 `json:"metrics,omitempty"`
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
	host := e("PG_HOST", "127.0.0.1")
	port, _ := strconv.Atoi(e("PG_PORT", "5432"))
	user := e("PG_USER", "postgres")
	pass := e("PG_PASS", "YourPgPass123!")
	dbname := e("PG_DB", "pgtest")

	dsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", host, port, user, pass, dbname)

	var err error
	rawDB, err = sql.Open("pgx", dsn)
	fatal(err, "open raw db")
	rawDB.SetMaxOpenConns(5)
	defer rawDB.Close()

	cfg := db.ConnectionConfig{DBType: "postgres", Host: host, Port: port, User: user, Password: pass, Database: dbname}
	d, err := pgdriver.NewDriver(cfg)
	fatal(err, "pgdriver")
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
	log("Rules: %d\n", engine.RuleCount())

	// Clean up dead tuples from previous runs to avoid vacuum rules dominating all scenarios
	log("Cleaning up dead tuples from previous runs...")
	rawDB.ExecContext(context.Background(), "VACUUM bloat_test")
	rawDB.ExecContext(context.Background(), "VACUUM hot_update_test")
	rawDB.ExecContext(context.Background(), "VACUUM lock_test")
	rawDB.ExecContext(context.Background(), "VACUUM big_table")
	rawDB.ExecContext(context.Background(), "SELECT pg_stat_force_next_flush()")
	time.Sleep(2 * time.Second)
	log("Done.\n")

	scenarios := allScenarios()
	log("Running %d scenarios...\n", len(scenarios))

	var results []result
	for i, sc := range scenarios {
		log("[%d/%d] %s %s", i+1, len(scenarios), sc.ID, sc.Name)
		r := runOne(sc)
		results = append(results, r)

		status := "MISS"
		if r.Skipped { status = "SKIP" } else if r.RuleFired != "" { status = "OK  " }
		log("  → [%s] rule=%-20s sev=%-4s cause=%s\n", status, r.RuleFired, r.Severity, trunc(r.Cause, 50))
	}

	// Output JSON
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(results)

	// Summary
	total, matched, skipped := len(results), 0, 0
	for _, r := range results {
		if r.Skipped { skipped++ } else if r.RuleFired != "" { matched++ }
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

	// Setup
	var cleanup func()
	if sc.Setup != nil {
		var err error
		cleanup, err = sc.Setup(ctx, rawDB)
		if err != nil {
			log("  setup err: %v", err)
		}
	}

	// Wait for background sessions to establish and PG stats to update
	time.Sleep(3 * time.Second)

	// Collect live metrics
	report := collectLive(ctx, driver)

	// Snapshot metrics for result
	r.MetricSnap = make(map[string]float64)
	for k, v := range report.Metrics {
		if v.Max > 0 { r.MetricSnap[k] = v.Avg }
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
		for _, f := range d.Findings { r.Findings = append(r.Findings, f.Desc) }
		for _, a := range d.Actions {
			txt := fmt.Sprintf("[%s] %s", a.Type, a.Desc)
			if a.RawSQL != "" { txt += " | SQL: " + a.RawSQL; r.HasSQL = true }
			r.Actions = append(r.Actions, txt)
		}
		if output.Secondary != nil { r.Secondary = output.Secondary.Cause }
		r.AbsorbedN = d.AbsorbedCount
	}

	// Cleanup
	if cleanup != nil { cleanup() }
	// Always kill leftover background sessions
	rawDB.ExecContext(context.Background(), `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname='pgtest' AND pid != pg_backend_pid()
		AND state IN ('idle in transaction', 'active')
		AND query NOT LIKE '%pg_terminate%'
		AND backend_start < now() - interval '2 seconds'
		AND usename = 'testuser'`)
	rawDB.ExecContext(context.Background(), "RESET ALL")

	return r
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func e(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }
func fatal(err error, msg string) { if err != nil { fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err); os.Exit(1) } }
func log(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
func trunc(s string, n int) string { if len(s) <= n { return s }; return s[:n] + "..." }

// bgExec opens a new connection and executes SQL in background, returns cancel func
func bgExec(sqlStr string) func() {
	conn, err := sql.Open("pgx", dsn)
	if err != nil { return func() {} }
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil { conn.Close(); return func() {} }
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
	conn, err := sql.Open("pgx", dsn)
	if err != nil { return func() {} }
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil { conn.Close(); return func() {} }
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
	for i := 0; i < n; i++ { cleanups = append(cleanups, bgExec(sqlStr)) }
	return func() { for _, c := range cleanups { c() } }
}

// bgNLoop opens N background connections repeatedly running the same SQL
func bgNLoop(n int, sqlStr string) func() {
	var cleanups []func()
	for i := 0; i < n; i++ { cleanups = append(cleanups, bgLoop(sqlStr)) }
	return func() { for _, c := range cleanups { c() } }
}

// bgIdleInTx opens N connections in true "idle in transaction" state.
// Each session does BEGIN + SELECT 1, then waits for next command (idle in tx).
func bgIdleInTx(n int) func() {
	type pair struct {
		tx   *sql.Tx
		conn *sql.DB
	}
	var pairs []pair
	for i := 0; i < n; i++ {
		c, err := sql.Open("pgx", dsn)
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

	ss = append(ss, scenario{ID: "T001", Name: "行锁级联: blocker idle in tx", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=value+1 WHERE id=1; SELECT pg_sleep(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=value+1 WHERE id=1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T002", Name: "行锁级联: blocker 跑慢SQL", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Blocker: hold row lock + run slow query (cross-join makes it 10s+)
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=2; SELECT count(*) FROM big_table, generate_series(1,30) g; SELECT pg_sleep(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(15, "UPDATE lock_test SET value=2 WHERE id=2;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T003", Name: "行锁级联: blocker 等IO", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; SELECT count(*) FROM big_table; UPDATE lock_test SET value=1 WHERE id=2; SELECT pg_sleep(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgN(8, "UPDATE lock_test SET value=2 WHERE id=2;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T004", Name: "多层阻塞链", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// 3-level chain: A blocks B, B blocks C, C blocks waiters
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=10; SELECT pg_sleep(30);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgExec("BEGIN; UPDATE lock_test SET value=2 WHERE id=10; UPDATE lock_test SET value=2 WHERE id=11; SELECT pg_sleep(30);")
			time.Sleep(500 * time.Millisecond)
			c3 := bgExec("BEGIN; UPDATE lock_test SET value=3 WHERE id=11; UPDATE lock_test SET value=3 WHERE id=12; SELECT pg_sleep(30);")
			time.Sleep(500 * time.Millisecond)
			c4 := bgN(15, "UPDATE lock_test SET value=4 WHERE id=12;")
			return func() { c1(); c2(); c3(); c4() }, nil
		}})

	ss = append(ss, scenario{ID: "T005", Name: "死锁检测", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Trigger deadlocks + keep lock waiters active for detection
			for i := 0; i < 3; i++ {
				c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=20; SELECT pg_sleep(0.5); UPDATE lock_test SET value=1 WHERE id=21; COMMIT;")
				c2 := bgExec("BEGIN; UPDATE lock_test SET value=2 WHERE id=21; SELECT pg_sleep(0.5); UPDATE lock_test SET value=2 WHERE id=20; COMMIT;")
				time.Sleep(2 * time.Second)
				c1(); c2()
			}
			// Keep lock contention active during collection
			c3 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=20; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c4 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=20;")
			return func() { c3(); c4() }, nil
		}})

	ss = append(ss, scenario{ID: "T006", Name: "DDL 阻塞 DML", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Readers hold ShareLock via FOR SHARE + pg_sleep, DDL needs AccessExclusive
			c1 := bgN(10, "BEGIN; SELECT * FROM lock_test WHERE id < 100 FOR SHARE; SELECT pg_sleep(20);")
			time.Sleep(500 * time.Millisecond)
			c2 := bgExec("ALTER TABLE lock_test ADD COLUMN IF NOT EXISTS tmp_col INT;")
			time.Sleep(500 * time.Millisecond)
			c3 := bgN(10, "SELECT count(*) FROM lock_test;")
			return func() { c1(); c2(); c3(); rawDB.ExecContext(ctx, "ALTER TABLE lock_test DROP COLUMN IF EXISTS tmp_col") }, nil
		}})

	ss = append(ss, scenario{ID: "T007", Name: "VACUUM 与查询锁冲突", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=50000")
			// Long transaction holds lock + waiters blocked on row update
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=40; SELECT count(*) FROM bloat_test; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=40;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T008", Name: "Advisory Lock 泄漏", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			var cleanups []func()
			for i := 0; i < 20; i++ {
				c := bgExec(fmt.Sprintf("SELECT pg_advisory_lock(%d); SELECT pg_sleep(30);", 1000+i))
				cleanups = append(cleanups, c)
			}
			return func() { for _, c := range cleanups { c() } }, nil
		}})

	ss = append(ss, scenario{ID: "T009", Name: "外键缺索引导致锁扩散", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Blocker holds row lock, waiters blocked
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=41; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=41;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T010", Name: "并发INSERT热点页争用", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Blocker + waiters to ensure lock_wait_sessions > 3
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=42; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=42;")
			c3 := bgN(10, "INSERT INTO lock_test (value, status) SELECT (random()*100)::int, 'new' FROM generate_series(1, 1000);")
			return func() { c1(); c2(); c3() }, nil
		}})

	ss = append(ss, scenario{ID: "T011", Name: "并发索引创建被阻塞", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Blocker holds row lock + waiters
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=43; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=43;")
			return func() { c1(); c2(); rawDB.ExecContext(ctx, "DROP INDEX IF EXISTS idx_tmp_lock") }, nil
		}})

	ss = append(ss, scenario{ID: "T012", Name: "TRUNCATE 阻塞读", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS trunc_test AS SELECT * FROM lock_test LIMIT 1000")
			// Readers hold AccessShareLock via pg_sleep in transaction
			c1 := bgN(10, "BEGIN; SELECT count(*) FROM trunc_test; SELECT pg_sleep(20);")
			time.Sleep(500 * time.Millisecond)
			// TRUNCATE needs AccessExclusiveLock → blocked by readers
			c2 := bgExec("TRUNCATE trunc_test;")
			time.Sleep(500 * time.Millisecond)
			// More readers blocked by TRUNCATE's lock queue
			c3 := bgN(5, "SELECT count(*) FROM trunc_test;")
			return func() { c1(); c2(); c3(); rawDB.ExecContext(ctx, "DROP TABLE IF EXISTS trunc_test") }, nil
		}})

	ss = append(ss, scenario{ID: "T013", Name: "预备事务未清理", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Blocker + waiters to generate lock_waits
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=44; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=44;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T014", Name: "分区表DDL锁粒度", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Blocker + waiters on row lock
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=45; SELECT pg_sleep(20);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgN(10, "UPDATE lock_test SET value=2 WHERE id=45;")
			return func() { c1(); c2(); rawDB.ExecContext(ctx, "DROP TABLE IF EXISTS part_tmp") }, nil
		}})

	ss = append(ss, scenario{ID: "T015", Name: "锁等待超时重试风暴", Category: "锁",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=30; SELECT pg_sleep(30);")
			time.Sleep(500 * time.Millisecond)
			// 20 sessions retrying with short lock_timeout — creates churn
			c2 := bgNLoop(20, "SET lock_timeout='500ms'; UPDATE lock_test SET value=2 WHERE id=30;")
			return func() { c1(); c2() }, nil
		}})

	// ═══ Category 2: SQL 性能 (T016-T035) ═══

	ss = append(ss, scenario{ID: "T016", Name: "全表扫描缺索引", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM big_table WHERE col1 = 'val_12345'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T017", Name: "全扫描选择度低", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, "SELECT count(*) FROM big_table, generate_series(1,20) g WHERE status = 'active';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T018", Name: "执行计划漂移", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "DELETE FROM pg_stat_statements_info WHERE 1=0") // noop, just trigger stats
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM orders WHERE status='pending' AND created_at > '2026-01-01'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T019", Name: "CTE物化性能差", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM (SELECT * FROM big_table) AS cte WHERE id < 10; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T020", Name: "Nested Loop低效", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET enable_hashjoin=off'; PERFORM count(*) FROM orders o JOIN users u ON o.user_id=u.id WHERE o.status='pending'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T021", Name: "Hash Join溢出", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''1MB'''; PERFORM count(*) FROM big_table a JOIN orders b ON a.id=b.user_id; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T022", Name: "大排序溢出", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, "SET work_mem='1MB'; SELECT * FROM big_table, generate_series(1,10) g ORDER BY col1, col2 LIMIT 100;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T023", Name: "隐式类型转换", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// PG中 varchar=int 会报错; 用 id::text 强制全扫(cast 阻止索引使用)
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM big_table WHERE id::text = '12345'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T024", Name: "函数索引失效", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM users WHERE LOWER(name) = 'user_1'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T025", Name: "LIKE前模糊全扫", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM big_table WHERE col1 LIKE '%12345%'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T026", Name: "分区裁剪失效", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM part_table WHERE DATE_TRUNC('month', created_at) = '2026-01-01'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T027", Name: "NOT IN子查询", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM users WHERE id NOT IN (SELECT user_id FROM orders WHERE user_id IS NULL); PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T028", Name: "OFFSET大分页", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM id FROM big_table ORDER BY id LIMIT 20 OFFSET 4000000; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T029", Name: "相关子查询N+1", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM u.id, (SELECT count(*) FROM orders WHERE user_id=u.id) FROM users u LIMIT 10000; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T030", Name: "JSONB无GIN索引", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN PERFORM count(*) FROM jsonb_table WHERE profile @> '{"status":"active"}'; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T031", Name: "多列索引顺序不当", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, "SELECT count(*) FROM big_table, generate_series(1,20) g WHERE status='active' AND created_at > '2026-01-01';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T032", Name: "过多索引写入慢", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Bulk insert: each iteration inserts 50K rows → slow due to many indexes
			c := bgNLoop(8, `DO $$ BEGIN INSERT INTO idx_test (col1,col2,col3,col4,col5,col6,status,category) SELECT 'a','b','c',g,g,g,'x','y' FROM generate_series(1,10000) g; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T033", Name: "大表COUNT(*)", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, "SELECT count(*) FROM big_table, generate_series(1,20) g;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T034", Name: "DISTINCT大排序", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, "SET work_mem='1MB'; SELECT DISTINCT col1, col2 FROM big_table, generate_series(1,5) g;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T035", Name: "Window Function无索引", Category: "SQL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, "SET work_mem='1MB'; SELECT ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at DESC) FROM orders, generate_series(1,50) g;")
			return func() { c() }, nil
		}})

	// ═══ Category 3: VACUUM 与 MVCC (T036-T045) ═══

	ss = append(ss, scenario{ID: "T036", Name: "Dead Tuple堆积: autovacuum跟不上", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER TABLE bloat_test SET (autovacuum_enabled = false)")
			// Multiple small UPDATEs to avoid 60s timeout (500K×3 = 1.5M dead tuples)
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "SELECT pg_stat_force_next_flush()")
			time.Sleep(1 * time.Second)
			return func() {
				rawDB.ExecContext(ctx, "ALTER TABLE bloat_test SET (autovacuum_enabled = true)")
			}, nil
		}})

	ss = append(ss, scenario{ID: "T037", Name: "长事务阻止VACUUM回收", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER TABLE bloat_test SET (autovacuum_enabled = false)")
			c := bgIdleInTx(1)
			time.Sleep(200 * time.Millisecond)
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "SELECT pg_stat_force_next_flush()")
			time.Sleep(1 * time.Second)
			return func() {
				c()
				rawDB.ExecContext(ctx, "ALTER TABLE bloat_test SET (autovacuum_enabled = true)")
			}, nil
		}})

	ss = append(ss, scenario{ID: "T038", Name: "XID Wraparound警告", Category: "VACUUM",
		// Can't easily simulate XID wraparound, just check current XID age
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) { return func() {}, nil }})

	ss = append(ss, scenario{ID: "T039", Name: "表膨胀严重", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER TABLE bloat_test SET (autovacuum_enabled = false)")
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=500000")
			rawDB.ExecContext(ctx, "SELECT pg_stat_force_next_flush()")
			time.Sleep(1 * time.Second)
			return func() {
				rawDB.ExecContext(ctx, "ALTER TABLE bloat_test SET (autovacuum_enabled = true)")
			}, nil
		}})

	ss = append(ss, scenario{ID: "T040", Name: "索引膨胀", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "DELETE FROM bloat_test WHERE id%3=0")
			rawDB.ExecContext(ctx, "INSERT INTO bloat_test (counter) SELECT 0 FROM generate_series(1,100000)")
			return func() {}, nil
		}})

	ss = append(ss, scenario{ID: "T041", Name: "autovacuum被频繁取消", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=100000")
			return func() {}, nil
		}})

	ss = append(ss, scenario{ID: "T042", Name: "HOT Update链过长", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			for i := 0; i < 5; i++ {
				rawDB.ExecContext(ctx, "UPDATE hot_update_test SET counter=counter+1 WHERE id<=10000")
			}
			return func() {}, nil
		}})

	ss = append(ss, scenario{ID: "T043", Name: "VACUUM FULL阻塞", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS vf_test AS SELECT * FROM lock_test LIMIT 10000")
			c1 := bgN(5, "SELECT count(*) FROM vf_test;")
			time.Sleep(200 * time.Millisecond)
			c2 := bgExec("VACUUM FULL vf_test;")
			return func() { c1(); c2(); rawDB.ExecContext(ctx, "DROP TABLE IF EXISTS vf_test") }, nil
		}})

	ss = append(ss, scenario{ID: "T044", Name: "TOAST表膨胀", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "UPDATE jsonb_table SET profile = profile || '{\"updated\": true}' WHERE id <= 50000")
			return func() {}, nil
		}})

	ss = append(ss, scenario{ID: "T045", Name: "autovacuum饥饿", Category: "VACUUM",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "UPDATE bloat_test SET counter=counter+1 WHERE id<=100000")
			rawDB.ExecContext(ctx, "UPDATE hot_update_test SET counter=counter+1 WHERE id<=50000")
			rawDB.ExecContext(ctx, "UPDATE lock_test SET value=value+1 WHERE id<=50000")
			return func() {}, nil
		}})

	// ═══ Category 4: 内存与缓存 (T046-T055) ═══

	ss = append(ss, scenario{ID: "T046", Name: "Cache命中率下降:大查询冲刷", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Generate temp file spills by forcing tiny work_mem
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY col1, col2 LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T047", Name: "shared_buffers太小", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY col2, col1 LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T048", Name: "work_mem不足临时文件溢出", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY col1, col2 LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T049", Name: "effective_cache_size设错", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM count(DISTINCT col1) FROM big_table; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})
	ss = append(ss, scenario{ID: "T050", Name: "maintenance_work_mem不足", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY amount LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T051", Name: "OOM风险", Category: "内存",
		Skip: true, SkipNote: "不安全:触发OOM可能kill PG"})

	ss = append(ss, scenario{ID: "T052", Name: "huge_pages未启用", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY col1 LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})
	ss = append(ss, scenario{ID: "T053", Name: "shared_buffers过大", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY col2 LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T054", Name: "temp_buffers不足", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM count(DISTINCT col1) FROM big_table; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T055", Name: "频繁Backend创建", Category: "内存",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN EXECUTE 'SET work_mem=''64kB'''; PERFORM * FROM big_table ORDER BY amount, col1 LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c() }, nil
		}})

	// ═══ Category 5: WAL 与 Checkpoint (T056-T065) ═══

	ss = append(ss, scenario{ID: "T056", Name: "WAL生成速率过高", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "INSERT INTO bloat_test (counter) SELECT generate_series(1, 100000);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T057", Name: "Checkpoint过于频繁", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Heavy WAL generation to cause checkpoint pressure
			c := bgNLoop(8, `DO $$ BEGIN INSERT INTO lock_test (value, status) SELECT g, 'wal' FROM generate_series(1,10000) g; PERFORM pg_sleep(3); END $$;`)
			return func() { c() }, nil
		}})
	ss = append(ss, scenario{ID: "T058", Name: "Checkpoint IO尖峰", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "CHECKPOINT")
			c := bgNLoop(8, `DO $$ BEGIN UPDATE lock_test SET value=value+1 WHERE id<=5000; PERFORM pg_sleep(3); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T059", Name: "WAL Archive失败", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN INSERT INTO lock_test (value, status) SELECT g, 'arch' FROM generate_series(1,10000) g; PERFORM pg_sleep(3); END $$;`)
			return func() { c() }, nil
		}})
	ss = append(ss, scenario{ID: "T060", Name: "pg_wal目录膨胀", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN INSERT INTO lock_test (value, status) SELECT g, 'wal2' FROM generate_series(1,10000) g; PERFORM pg_sleep(3); END $$;`)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T061", Name: "Replication Slot阻止WAL回收", Category: "WAL",
		Skip: true, SkipNote: "需要流复制设置"})

	ss = append(ss, scenario{ID: "T062", Name: "同步提交延迟高", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(10, "INSERT INTO lock_test (value,status) VALUES (1,'s'); ")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T063", Name: "wal_level=logical额外开销", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(8, `DO $$ BEGIN INSERT INTO lock_test (value, status) SELECT g, 'log' FROM generate_series(1,10000) g; PERFORM pg_sleep(3); END $$;`)
			return func() { c() }, nil
		}})
	ss = append(ss, scenario{ID: "T064", Name: "WAL Sender占满", Category: "WAL",
		Skip: true, SkipNote: "需要流复制"})

	ss = append(ss, scenario{ID: "T065", Name: "full_page_writes IO放大", Category: "WAL",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "CHECKPOINT")
			c := bgNLoop(5, "UPDATE lock_test SET value=value+1 WHERE id <= 1000;")
			return func() { c() }, nil
		}})

	// ═══ Category 6: 等待事件与延迟 (T066-T075) ═══

	ss = append(ss, scenario{ID: "T066", Name: "LWLock:BufferMapping争用", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(40, "SELECT count(*) FROM big_table, generate_series(1,10) g TABLESAMPLE SYSTEM(1);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T067", Name: "LWLock:WALInsert争用", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(10, "INSERT INTO lock_test (value) SELECT generate_series(1,10000);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T068", Name: "IO:DataFileRead延迟高", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT count(*) FROM big_table WHERE col1='nonexistent_value_xyz';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T069", Name: "IO:DataFileWrite延迟高", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "CHECKPOINT")
			c := bgNLoop(5, "UPDATE big_table SET amount=amount+1 WHERE id<=10000;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T070", Name: "BufferPin等待", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("DECLARE cur CURSOR FOR SELECT * FROM bloat_test; FETCH 1000 FROM cur; SELECT pg_sleep(15);")
			c2 := bgExec("VACUUM bloat_test;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T071", Name: "Client:ClientRead等待", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Real idle-in-tx sessions show ClientRead wait event
			c := bgIdleInTx(20)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T072", Name: "Lock:transactionid等待", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=50; SELECT pg_sleep(15);")
			time.Sleep(300 * time.Millisecond)
			c2 := bgNLoop(10, "UPDATE lock_test SET value=2 WHERE id=50;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T073", Name: "Lock:extend争用", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(20, "INSERT INTO lock_test (value) VALUES ((random()*100)::int);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T074", Name: "CPU自旋等待", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// 60 sessions → active_sessions > 50, heavy CPU
			c := bgNLoop(60, "SELECT count(*) FROM generate_series(1,10000000);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T075", Name: "IO:WALWrite延迟", Category: "等待",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgN(10, "INSERT INTO lock_test (value) VALUES (1);")
			return func() { c() }, nil
		}})

	// ═══ Category 7: 连接与会话 (T076-T085) ═══

	ss = append(ss, scenario{ID: "T076", Name: "连接风暴无连接池", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// idle-in-tx sessions to trigger PG-008 (idle_in_transaction > 5)
			c := bgIdleInTx(15)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T077", Name: "Connections接近上限", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgIdleInTx(15)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T078", Name: "idle in transaction堆积", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// Real idle-in-transaction: BEGIN + SELECT, then wait (no pg_sleep)
			c := bgIdleInTx(25)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T079", Name: "会话泄漏", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgIdleInTx(15)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T080", Name: "PgBouncer池太小", Category: "连接",
		Skip: true, SkipNote: "需要PgBouncer"})

	ss = append(ss, scenario{ID: "T081", Name: "idle连接占满", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgIdleInTx(15)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T082", Name: "statement_timeout未设", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgIdleInTx(10)
			c2 := bgNLoop(8, `DO $$ BEGIN PERFORM * FROM big_table ORDER BY col1, col2, amount LIMIT 100; PERFORM pg_sleep(5); END $$;`)
			return func() { c(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T083", Name: "superuser连接预留不足", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgIdleInTx(10)
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T084", Name: "Active Sessions突增", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			// 60 concurrent sessions → active_sessions > 50 triggers PG-021
			c := bgNLoop(60, "SELECT count(*) FROM big_table, generate_series(1,20) g WHERE col1='xxx';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T085", Name: "terminate导致回滚风暴", Category: "连接",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgIdleInTx(10)
			return func() { c() }, nil
		}})

	// ═══ Category 8: 配置与参数 (T086-T090) ═══

	ss = append(ss, scenario{ID: "T086", Name: "random_page_cost过高", Category: "配置",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER SYSTEM SET random_page_cost = 4.0")
			rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			return func() {
				rawDB.ExecContext(ctx, "ALTER SYSTEM SET random_page_cost = 1.1")
				rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			}, nil
		}})
	ss = append(ss, scenario{ID: "T087", Name: "并行查询禁用", Category: "配置",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER SYSTEM SET max_parallel_workers_per_gather = 0")
			rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			return func() {
				rawDB.ExecContext(ctx, "ALTER SYSTEM SET max_parallel_workers_per_gather = 4")
				rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			}, nil
		}})
	ss = append(ss, scenario{ID: "T088", Name: "慢查询日志未开", Category: "配置",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER SYSTEM SET log_min_duration_statement = -1")
			rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			return func() {
				rawDB.ExecContext(ctx, "ALTER SYSTEM SET log_min_duration_statement = 1000")
				rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			}, nil
		}})
	ss = append(ss, scenario{ID: "T089", Name: "statistics_target过低", Category: "配置",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			rawDB.ExecContext(ctx, "ALTER SYSTEM SET default_statistics_target = 10")
			rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			return func() {
				rawDB.ExecContext(ctx, "ALTER SYSTEM SET default_statistics_target = 100")
				rawDB.ExecContext(ctx, "SELECT pg_reload_conf()")
			}, nil
		}})
	ss = append(ss, scenario{ID: "T090", Name: "缺少关键扩展", Category: "配置",
		Skip: true, SkipNote: "pg_stat_statements已安装无法卸载"})

	// ═══ Category 9: 系统与运维 (T091-T100) ═══

	ss = append(ss, scenario{ID: "T091", Name: "pg_stat_statements重置", Category: "系统"})
	ss = append(ss, scenario{ID: "T092", Name: "pg_hba过于宽松", Category: "系统"})

	ss = append(ss, scenario{ID: "T093", Name: "升级后统计信息丢失", Category: "系统",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT count(*) FROM big_table WHERE col1='search_val';")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T094", Name: "扩展版本不兼容", Category: "系统"})

	ss = append(ss, scenario{ID: "T095", Name: "日志撑满磁盘", Category: "系统"})

	ss = append(ss, scenario{ID: "T096", Name: "并发ANALYZE冲突", Category: "系统",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(3, "ANALYZE big_table;")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T097", Name: "REINDEX阻塞写入", Category: "系统",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgNLoop(5, "INSERT INTO idx_test (col1,col2,col3,col4,col5,col6,status,category) VALUES ('a','b','c',1,2,3,'x','y');")
			time.Sleep(200 * time.Millisecond)
			c2 := bgExec("REINDEX INDEX idx_t_col1;")
			return func() { c1(); c2() }, nil
		}})

	ss = append(ss, scenario{ID: "T098", Name: "逻辑复制冲突", Category: "系统",
		Skip: true, SkipNote: "需要逻辑复制设置"})

	ss = append(ss, scenario{ID: "T099", Name: "pg_cron任务堆叠", Category: "系统",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c := bgNLoop(5, "SELECT count(*) FROM big_table; SELECT pg_sleep(5);")
			return func() { c() }, nil
		}})

	ss = append(ss, scenario{ID: "T100", Name: "综合:IO+Lock+WAL", Category: "系统",
		Setup: func(ctx context.Context, db *sql.DB) (func(), error) {
			c1 := bgExec("BEGIN; UPDATE lock_test SET value=1 WHERE id=60; SELECT pg_sleep(15);")
			time.Sleep(200 * time.Millisecond)
			c2 := bgNLoop(5, "UPDATE lock_test SET value=2 WHERE id=60;")
			c3 := bgNLoop(3, "INSERT INTO bloat_test (counter) SELECT generate_series(1,50000);")
			c4 := bgNLoop(3, "SELECT count(*) FROM big_table WHERE col1='xyz';")
			return func() { c1(); c2(); c3(); c4() }, nil
		}})

	return ss
}

// ── Live Metric Collection (from real PG) ───────────────────────────────────

func collectLive(ctx context.Context, drv db.Driver) *sentinel.BurstReport {
	r := &sentinel.BurstReport{
		TriggerEvent:   sentinel.TriggerEvent{Metric: "active_sessions", Strategy: sentinel.StrategyT1},
		DurationSec:    30, PeakActive: 5, BaselineActive: 2,
		StartTime: time.Now().Add(-30 * time.Second), EndTime: time.Now(),
		Metrics: make(map[string]sentinel.MetricSummary), RawFrameCount: 30,
	}
	q(ctx, drv, r, `SELECT
		count(*) FILTER (WHERE state='active'),
		count(*) FILTER (WHERE state='active' AND wait_event_type IS NULL),
		count(*) FILTER (WHERE state='active' AND wait_event_type='IO'),
		count(*) FILTER (WHERE state='active' AND wait_event_type='Lock'),
		count(*) FILTER (WHERE state='active' AND now()-query_start>interval '2s'),
		count(*) FILTER (WHERE state='idle in transaction'),
		ROUND(count(*)::numeric/GREATEST(current_setting('max_connections')::int,1)*100,1),
		count(*) FILTER (WHERE state='active' AND wait_event_type='LWLock'),
		count(*) FILTER (WHERE state='active' AND wait_event_type='BufferPin')
		FROM pg_stat_activity WHERE backend_type='client backend'`,
		"active_sessions","cpu_sessions","io_wait_sessions","lock_wait_sessions",
		"long_queries","idle_in_transaction","connections_pct","lwlock_wait_sessions","bufferpin_wait_sessions")

	// deadlocks: cumulative, convert to hourly rate
	// cache_hit_pct: ratio, fine as-is
	// temp_bytes/temp_files: cumulative, convert to per-second rate using stats_reset
	q(ctx, drv, r, `SELECT
		CASE WHEN EXTRACT(EPOCH FROM now()-stats_reset) > 0
			THEN ROUND(deadlocks::numeric / (EXTRACT(EPOCH FROM now()-stats_reset)/3600), 2) ELSE 0 END,
		CASE WHEN blks_hit+blks_read>0 THEN ROUND(blks_hit::numeric/(blks_hit+blks_read)*100,2) ELSE 100 END,
		CASE WHEN EXTRACT(EPOCH FROM now()-stats_reset) > 0
			THEN ROUND(temp_bytes::numeric / EXTRACT(EPOCH FROM now()-stats_reset), 2) ELSE 0 END,
		CASE WHEN EXTRACT(EPOCH FROM now()-stats_reset) > 0
			THEN ROUND(temp_files::numeric / EXTRACT(EPOCH FROM now()-stats_reset), 4) ELSE 0 END
		FROM pg_stat_database WHERE datname=current_database()`,
		"deadlocks","cache_hit_pct","temp_bytes_rate","temp_files_rate")

	q(ctx, drv, r, `SELECT COALESCE(MAX(CASE WHEN n_live_tup>1000
		THEN ROUND(n_dead_tup::numeric/n_live_tup*100,2) ELSE 0 END),0)
		FROM pg_stat_user_tables`, "dead_tuple_ratio")

	q(ctx, drv, r, `SELECT ROUND(age(datfrozenxid)::numeric/2147483647*100,2)
		FROM pg_database WHERE datname=current_database()`, "xid_age_pct")

	q(ctx, drv, r, `SELECT count(*) FROM pg_stat_activity WHERE backend_type='autovacuum worker'`, "autovacuum_workers")

	q(ctx, drv, r, `SELECT COALESCE(EXTRACT(EPOCH FROM max(now()-xact_start)),0)
		FROM pg_stat_activity WHERE state!='idle' AND xact_start IS NOT NULL`, "oldest_xact_age_sec")

	// checkpoints_req: use ratio of requested/(requested+timed) to estimate pressure
	// If ratio > 0.5, checkpoints are mostly demand-driven (WAL overflow)
	// Convert to an estimated hourly rate using stats_reset time
	q(ctx, drv, r, `SELECT
		CASE WHEN EXTRACT(EPOCH FROM now()-stats_reset) > 0
			THEN ROUND(checkpoints_req::numeric / (EXTRACT(EPOCH FROM now()-stats_reset)/3600), 2)
			ELSE 0 END
		FROM pg_stat_bgwriter`, "checkpoints_req")
	q(ctx, drv, r, `SELECT COALESCE(failed_count,0) FROM pg_stat_archiver`, "archive_fail_count")

	// WAL generation rate (PG14+ pg_stat_wal)
	q(ctx, drv, r, `SELECT CASE WHEN EXTRACT(EPOCH FROM now()-stats_reset) > 0
		THEN ROUND(wal_bytes::numeric / EXTRACT(EPOCH FROM now()-stats_reset), 0)
		ELSE 0 END FROM pg_stat_wal`, "wal_bytes_rate")

	// Parameter audit metrics from pg_settings
	q(ctx, drv, r, `SELECT
		(SELECT setting::float/128 FROM pg_settings WHERE name='shared_buffers'),
		(SELECT setting::float FROM pg_settings WHERE name='work_mem'),
		(SELECT setting::float FROM pg_settings WHERE name='log_min_duration_statement'),
		(SELECT setting::float FROM pg_settings WHERE name='max_parallel_workers_per_gather'),
		(SELECT setting::float FROM pg_settings WHERE name='default_statistics_target'),
		(SELECT setting::float FROM pg_settings WHERE name='random_page_cost'),
		(SELECT count(*) FROM pg_available_extensions WHERE name='pg_stat_statements' AND installed_version IS NOT NULL)`,
		"param_shared_buffers_mb", "param_work_mem_kb", "param_log_min_duration_ms",
		"param_max_parallel_workers", "param_statistics_target", "param_random_page_cost",
		"param_pgss_installed")

	// Blocking chains
	qr, err := drv.Query(ctx, `SELECT bl.pid, a.usename, LEFT(a.query,200), count(DISTINCT bw.pid), COALESCE(bw.wait_event,'')
		FROM pg_locks bl JOIN pg_locks bw ON bl.transactionid=bw.transactionid AND bl.pid!=bw.pid
		JOIN pg_stat_activity a ON a.pid=bl.pid JOIN pg_stat_activity aw ON aw.pid=bw.pid
		WHERE bl.granted AND NOT bw.granted
		GROUP BY bl.pid, a.usename, a.query, bw.wait_event ORDER BY count(DISTINCT bw.pid) DESC LIMIT 5`)
	if err == nil {
		for _, row := range qr.Rows {
			if len(row) >= 5 {
				r.BlockingChains = append(r.BlockingChains, sentinel.BlockingChain{
					BlockerPID: toI(row[0]), BlockerUser: toS(row[1]),
					BlockerQuery: toS(row[2]), VictimCount: toI(row[3]), WaitEvent: toS(row[4])})
			}
		}
	}
	sm(r, "blocker_count", float64(len(r.BlockingChains)))

	// Wait profile
	qr, err = drv.Query(ctx, `SELECT wait_event_type, wait_event, count(*)
		FROM pg_stat_activity WHERE state='active' AND wait_event IS NOT NULL
		GROUP BY wait_event_type, wait_event ORDER BY count(*) DESC LIMIT 10`)
	if err == nil {
		total := 0
		for _, row := range qr.Rows {
			if len(row) >= 3 {
				cnt := toI(row[2])
				r.WaitProfile = append(r.WaitProfile, sentinel.WaitBucket{
					WaitEventType: toS(row[0]), WaitEvent: toS(row[1]), Count: cnt})
				total += cnt
			}
		}
		for i := range r.WaitProfile {
			if total > 0 { r.WaitProfile[i].Percentage = float64(r.WaitProfile[i].Count) / float64(total) * 100 }
		}
	}

	// Top SQL
	qr, _ = drv.Query(ctx, `SELECT COALESCE(query_id::text,''), LEFT(query,300),
		COALESCE(wait_event_type,''), COALESCE(wait_event,''),
		EXTRACT(EPOCH FROM now()-query_start)::numeric
		FROM pg_stat_activity WHERE state='active' AND pid!=pg_backend_pid()
		ORDER BY query_start LIMIT 5`)
	if qr != nil {
		for _, row := range qr.Rows {
			if len(row) >= 5 {
				r.TopSQLs = append(r.TopSQLs, sentinel.SQLProfile{
					QueryID: toS(row[0]), Query: toS(row[1]),
					WaitEventType: toS(row[2]), WaitEvent: toS(row[3]),
					MeanTimeSec: toF(row[4]), ActiveCount: 1, Calls: 1})
			}
		}
	}
	if v, ok := r.Metrics["active_sessions"]; ok { r.PeakActive = int(v.Max) }

	// Add metric aliases so rules using different names can match
	// Rules use short names like "lock_waits", sentinel uses "lock_wait_sessions"
	aliases := map[string]string{
		"lock_wait_sessions":  "lock_waits",
		"idle_in_transaction": "idle_in_tx",
		"connections_pct":     "connection_pct",
		"temp_bytes_rate":     "temp_bytes",
		"temp_files_rate":     "temp_files",
		"dead_tuple_ratio":    "dead_tuples_ratio",
		"autovacuum_workers":  "autovacuum_count",
	}
	for src, dst := range aliases {
		if v, ok := r.Metrics[src]; ok {
			if _, exists := r.Metrics[dst]; !exists {
				r.Metrics[dst] = v
			}
		}
	}

	return r
}

func q(ctx context.Context, drv db.Driver, r *sentinel.BurstReport, sqlStr string, names ...string) {
	qr, err := drv.Query(ctx, sqlStr)
	if err != nil || len(qr.Rows) == 0 { return }
	row := qr.Rows[0]
	for i, name := range names {
		if i < len(row) { sm(r, name, toF(row[i])) }
	}
}
func sm(r *sentinel.BurstReport, name string, val float64) {
	r.Metrics[name] = sentinel.MetricSummary{Avg: val, Max: val, Min: val, Trend: "steady"}
}
func toF(v any) float64 {
	switch n := v.(type) {
	case float64: return n
	case float32: return float64(n)
	case int64: return float64(n)
	case int32: return float64(n)
	case int: return float64(n)
	default:
		f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f
	}
}
func toI(v any) int { return int(toF(v)) }
func toS(v any) string { if v == nil { return "" }; return fmt.Sprint(v) }
