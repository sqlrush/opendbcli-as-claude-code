// +build ignore

/*-------------------------------------------------------------------------
 *
 * test_bind.go
 *	  Ad-hoc bind-variable test driver (build:ignore) — used during
 *	  Oracle driver development to verify :n parameter binding
 *	  against v$sql_plan. Not part of the shipped binary.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  scripts/test_bind.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"database/sql"
	"fmt"
	_ "github.com/godror/godror"
)

func main() {
	db, err := sql.Open("godror", `user="system" password="OraclePass123" connectString="127.0.0.1:1521/orclpdb1"`)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Test bind variable :1 with v$sql_plan
	sqlID := "920k3fr1gapbg"
	row := db.QueryRow(`SELECT
       MAX(CASE WHEN p.operation='TABLE ACCESS' AND p.options='FULL' THEN 1 ELSE 0 END) has_full_scan,
       MAX(CASE WHEN p.operation IN ('SORT ORDER BY','SORT AGGREGATE','SORT GROUP BY','SORT UNIQUE','SORT JOIN') THEN 1 ELSE 0 END) has_sort,
       MAX(CASE WHEN p.operation='HASH JOIN' THEN 1 ELSE 0 END) has_hash_join,
       MAX(CASE WHEN INSTR(p.operation,'INDEX') > 0 THEN 1 ELSE 0 END) has_index
    FROM v$sql_plan p
    WHERE p.sql_id = :1`, sqlID)

	var fullScan, hasSort, hashJoin, hasIndex sql.NullFloat64
	err = row.Scan(&fullScan, &hasSort, &hashJoin, &hasIndex)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}
	fmt.Printf("full_scan=%v sort=%v hash=%v idx=%v\n",
		fullScan.Float64, hasSort.Float64, hashJoin.Float64, hasIndex.Float64)
}
