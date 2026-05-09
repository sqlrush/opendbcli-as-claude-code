/*-------------------------------------------------------------------------
 *
 * pprof.go
 *	  pprof HTTP server hook — gated by the DBAA_PPROF env var. When
 *	  the address is non-empty, registers net/http/pprof's default
 *	  mux on it so production builds can flip on profiling without
 *	  code changes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opendb/pprof.go
 *
 *-------------------------------------------------------------------------
 */
package main

import (
	"net/http"
	_ "net/http/pprof"
)

func pprofServe(addr string) error {
	return http.ListenAndServe(addr, nil)
}
