/*-------------------------------------------------------------------------
 *
 * main.go
 *	  gaussdb-probe is a standalone connectivity test for
 *	  HuaweiCloudDeveloper/gaussdb-go.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/gaussdb-probe/main.go
 *
 *-------------------------------------------------------------------------
 */
// gaussdb-probe is a standalone connectivity test for HuaweiCloudDeveloper/gaussdb-go.
//
// Independent of opendb's main code. Customers can run this single binary in
// their network to verify the driver works against their GaussDB instance
// before we deploy opendb itself.
//
// Build:  go build -o gaussdb-probe ./cmd/gaussdb-probe/
// Run:    ./gaussdb-probe -host X -port 5432 -user U -password P -database D
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/HuaweiCloudDeveloper/gaussdb-go/stdlib"
)

type probeResult struct {
	Stage    string
	OK       bool
	Detail   string
	Duration time.Duration
}

func main() {
	host := flag.String("host", "", "GaussDB host (required)")
	port := flag.Int("port", 5432, "GaussDB port")
	user := flag.String("user", "", "GaussDB user (required)")
	password := flag.String("password", "", "GaussDB password (required)")
	database := flag.String("database", "postgres", "GaussDB database name")
	sslmode := flag.String("sslmode", "disable", "ssl mode: disable|require|verify-ca|verify-full")
	timeout := flag.Int("timeout", 15, "connect/query timeout seconds")
	flag.Parse()

	if *host == "" || *user == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "missing required flags: -host -user -password")
		flag.Usage()
		os.Exit(2)
	}

	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s database=%s sslmode=%s connect_timeout=%d application_name=gaussdb-probe",
		*host, *port, *user, *password, *database, *sslmode, *timeout,
	)

	results := runProbe(dsn, time.Duration(*timeout)*time.Second)
	printReport(results, *host, *port, *user, *database)

	for _, r := range results {
		if !r.OK {
			os.Exit(1)
		}
	}
}

func runProbe(dsn string, timeout time.Duration) []probeResult {
	var out []probeResult

	t0 := time.Now()
	conn, err := sql.Open("gaussdb", dsn)
	if err != nil {
		out = append(out, probeResult{Stage: "open", OK: false, Detail: err.Error(), Duration: time.Since(t0)})
		return out
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	out = append(out, probeResult{Stage: "open", OK: true, Detail: "driver registered", Duration: time.Since(t0)})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	t1 := time.Now()
	if err := conn.PingContext(ctx); err != nil {
		out = append(out, probeResult{Stage: "ping", OK: false, Detail: err.Error(), Duration: time.Since(t1)})
		return out
	}
	out = append(out, probeResult{Stage: "ping", OK: true, Detail: "auth + handshake OK", Duration: time.Since(t1)})

	out = append(out, probeQuery(ctx, conn, "version", "SELECT version()"))
	out = append(out, probeQuery(ctx, conn, "password_encryption_type", "SHOW password_encryption_type"))
	out = append(out, probeQuery(ctx, conn, "current_user", "SELECT current_user"))
	out = append(out, probeQuery(ctx, conn, "current_database", "SELECT current_database()"))
	out = append(out, probeQuery(ctx, conn, "server_version_num", "SHOW server_version_num"))

	return out
}

func probeQuery(ctx context.Context, conn *sql.DB, stage, sqlStr string) probeResult {
	t := time.Now()
	var v sql.NullString
	err := conn.QueryRowContext(ctx, sqlStr).Scan(&v)
	if err != nil {
		return probeResult{Stage: stage, OK: false, Detail: err.Error(), Duration: time.Since(t)}
	}
	return probeResult{Stage: stage, OK: true, Detail: v.String, Duration: time.Since(t)}
}

func printReport(results []probeResult, host string, port int, user, database string) {
	fmt.Println("==================================================")
	fmt.Println("  GaussDB connectivity probe (gaussdb-go v1.0.0-rc1)")
	fmt.Println("==================================================")
	fmt.Printf("  target  : %s@%s:%d/%s\n", user, host, port, database)
	fmt.Printf("  ts      : %s\n", time.Now().Format(time.RFC3339))
	fmt.Println()

	allOK := true
	for _, r := range results {
		mark := "OK"
		if !r.OK {
			mark = "FAIL"
			allOK = false
		}
		fmt.Printf("  [%-4s] %-25s  %6.0fms  %s\n",
			mark, r.Stage, float64(r.Duration.Milliseconds()), truncate(r.Detail, 400))
	}

	fmt.Println()
	if allOK {
		fmt.Println("  RESULT: all checks passed — driver compatible.")
	} else {
		fmt.Println("  RESULT: at least one check FAILED — see details above.")
	}
	fmt.Println("==================================================")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
