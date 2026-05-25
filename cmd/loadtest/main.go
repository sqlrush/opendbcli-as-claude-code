/*-------------------------------------------------------------------------
 *
 * main.go
 *	  Entry point for the main binary.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/loadtest/main.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/sijms/go-ora/v2"
)

// Config
const (
	dsn          = "oracle://opendb_test:OpenDB_Test_2026@127.0.0.1:1521/orcl"
	workers      = 100 // concurrent goroutines
	plsqlBatch   = 50  // rows per PL/SQL bulk call
	maxTableRows = 500000
	reportEvery  = 3 * time.Second
	testDuration = 5 * time.Minute
)

// Counters
var (
	totalQueries atomic.Int64
	totalTxns    atomic.Int64
	totalInserts atomic.Int64
	totalSelects atomic.Int64
	totalUpdates atomic.Int64
	totalDeletes atomic.Int64
	totalErrors  atomic.Int64
	currentRows  atomic.Int64
)

// PL/SQL blocks — each call executes many SQL statements inside Oracle
const plsqlBulkInsert = `BEGIN
  FOR i IN 1..:1 LOOP
    INSERT INTO loadtest (id, batch_id, payload, val1, val2)
    VALUES (loadtest_seq.NEXTVAL, MOD(ABS(DBMS_RANDOM.RANDOM), 10000),
            'load-' || loadtest_seq.CURRVAL || '-' || DBMS_RANDOM.STRING('A', 40),
            MOD(ABS(DBMS_RANDOM.RANDOM), 10000),
            MOD(ABS(DBMS_RANDOM.RANDOM), 10000));
  END LOOP;
  COMMIT;
END;`

const plsqlBulkUpdate = `BEGIN
  FOR i IN 1..:1 LOOP
    UPDATE loadtest SET val1 = MOD(ABS(DBMS_RANDOM.RANDOM), 10000),
           val2 = MOD(ABS(DBMS_RANDOM.RANDOM), 10000),
           payload = 'upd-' || TO_CHAR(SYSTIMESTAMP, 'FF6')
    WHERE batch_id = MOD(ABS(DBMS_RANDOM.RANDOM), 10000) AND ROWNUM <= 3;
  END LOOP;
  COMMIT;
END;`

const plsqlBulkDelete = `BEGIN
  FOR i IN 1..:1 LOOP
    DELETE FROM loadtest WHERE batch_id = MOD(ABS(DBMS_RANDOM.RANDOM), 10000) AND ROWNUM <= 3;
  END LOOP;
  COMMIT;
END;`

const plsqlBulkSelect = `DECLARE
  v_cnt NUMBER;
  v_sum NUMBER;
  v_id  NUMBER;
  v_pay VARCHAR2(200);
BEGIN
  FOR i IN 1..:1 LOOP
    IF MOD(i, 2) = 0 THEN
      SELECT COUNT(*), NVL(SUM(val1), 0) INTO v_cnt, v_sum
      FROM loadtest WHERE batch_id BETWEEN MOD(ABS(DBMS_RANDOM.RANDOM), 5000)
                                       AND MOD(ABS(DBMS_RANDOM.RANDOM), 5000) + 500;
    ELSE
      BEGIN
        SELECT id, payload INTO v_id, v_pay
        FROM loadtest WHERE batch_id = MOD(ABS(DBMS_RANDOM.RANDOM), 10000) AND ROWNUM = 1;
      EXCEPTION WHEN NO_DATA_FOUND THEN NULL;
      END;
    END IF;
  END LOOP;
END;`

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\n--- Stopping (Ctrl+C) ---")
		cancel()
	}()

	db, err := sql.Open("oracle", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(workers + 20)
	db.SetMaxIdleConns(workers + 10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping error: %v\n", err)
		os.Exit(1)
	}

	var cnt int64
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM loadtest").Scan(&cnt)
	currentRows.Store(cnt)

	var maxID int64
	db.QueryRowContext(ctx, "SELECT NVL(MAX(id),0) FROM loadtest").Scan(&maxID)

	fmt.Printf("=== Oracle Load Test (PL/SQL Bulk) ===\n")
	fmt.Printf("Workers: %d | Batch: %d ops/call | Duration: %v\n", workers, plsqlBatch, testDuration)
	fmt.Printf("Current rows: %d | Max ID: %d\n", cnt, maxID)
	fmt.Printf("Target: QPS > 50,000 | TPS > 5,000\n")
	fmt.Printf("========================================\n\n")

	go reporter(ctx)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(ctx, db, id)
		}(i)
	}

	wg.Wait()
	printFinalReport()
}

func worker(ctx context.Context, db *sql.DB, id int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rows := currentRows.Load()
		r := rng.Intn(100)

		var opErr error
		switch {
		// Space management: if table too big, more deletes
		case rows > maxTableRows && r < 40:
			opErr = doBulkDelete(ctx, db, plsqlBatch)
		case rows < 50000 && r < 40:
			opErr = doBulkInsert(ctx, db, plsqlBatch)
		case r < 22:
			opErr = doBulkInsert(ctx, db, plsqlBatch)
		case r < 44:
			opErr = doBulkDelete(ctx, db, plsqlBatch)
		case r < 72:
			opErr = doBulkSelect(ctx, db, plsqlBatch)
		default:
			opErr = doBulkUpdate(ctx, db, plsqlBatch)
		}

		if opErr != nil {
			totalErrors.Add(1)
		}
		totalTxns.Add(1)
	}
}

func doBulkInsert(ctx context.Context, db *sql.DB, batch int) error {
	_, err := db.ExecContext(ctx, plsqlBulkInsert, batch)
	if err == nil {
		n := int64(batch)
		totalInserts.Add(n)
		totalQueries.Add(n)
		currentRows.Add(n)
	}
	return err
}

func doBulkSelect(ctx context.Context, db *sql.DB, batch int) error {
	_, err := db.ExecContext(ctx, plsqlBulkSelect, batch)
	if err == nil {
		totalSelects.Add(int64(batch))
		totalQueries.Add(int64(batch))
	}
	return err
}

func doBulkUpdate(ctx context.Context, db *sql.DB, batch int) error {
	_, err := db.ExecContext(ctx, plsqlBulkUpdate, batch)
	if err == nil {
		// Each iteration updates up to 3 rows, count as batch operations
		n := int64(batch)
		totalUpdates.Add(n)
		totalQueries.Add(n)
	}
	return err
}

func doBulkDelete(ctx context.Context, db *sql.DB, batch int) error {
	_, err := db.ExecContext(ctx, plsqlBulkDelete, batch)
	if err == nil {
		// Each iteration deletes up to 3 rows
		n := int64(batch)
		totalDeletes.Add(n)
		totalQueries.Add(n)
		currentRows.Add(-n * 2) // approximate: up to 3 rows each, ~2 avg
	}
	return err
}

func reporter(ctx context.Context) {
	ticker := time.NewTicker(reportEvery)
	defer ticker.Stop()

	lastQ := int64(0)
	lastT := int64(0)
	lastTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()

			q := totalQueries.Load()
			t := totalTxns.Load()

			qps := float64(q-lastQ) / elapsed
			tps := float64(t-lastT) / elapsed

			fmt.Printf("[%s] QPS: %8.0f | TPS: %6.0f | I/S/U/D: %d/%d/%d/%d | Rows~%d | Err: %d\n",
				now.Format("15:04:05"),
				qps, tps,
				totalInserts.Load(), totalSelects.Load(), totalUpdates.Load(), totalDeletes.Load(),
				currentRows.Load(),
				totalErrors.Load(),
			)

			lastQ = q
			lastT = t
			lastTime = now
		}
	}
}

func printFinalReport() {
	fmt.Printf("\n=== Final Report ===\n")
	fmt.Printf("Total SQL Ops:  %d\n", totalQueries.Load())
	fmt.Printf("Total Txns:     %d\n", totalTxns.Load())
	fmt.Printf("  Inserts:      %d\n", totalInserts.Load())
	fmt.Printf("  Selects:      %d\n", totalSelects.Load())
	fmt.Printf("  Updates:      %d\n", totalUpdates.Load())
	fmt.Printf("  Deletes:      %d\n", totalDeletes.Load())
	fmt.Printf("  Errors:       %d\n", totalErrors.Load())
	fmt.Printf("Approx Rows:    %d\n", currentRows.Load())
	fmt.Printf("====================\n")
}
