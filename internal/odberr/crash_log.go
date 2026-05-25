/*-------------------------------------------------------------------------
 *
 * crash_log.go
 *	  CrashLogPath returns the resolved path where WriteCrash appends
 *	  records. Honors $OPENDB_HOME, falls back to $HOME/.opendb, then
 *	  current dir.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/crash_log.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/brand"
)

// Max crash log size before rotation (1 MiB). When exceeded, the existing
// file is renamed to crash.log.old and a fresh crash.log is started.
const maxCrashLogSize = 1 << 20

var (
	crashLogMu sync.Mutex

	// crashLogPathOverride is used by tests via SetCrashLogPath.
	// Zero value means "resolve from $OPENDB_HOME / $HOME/.opendb".
	crashLogPathOverride string
)

// CrashLogPath returns the resolved path where WriteCrash appends records.
// Honors $OPENDB_HOME, falls back to $HOME/.opendb, then current dir.
func CrashLogPath() string {
	crashLogMu.Lock()
	override := crashLogPathOverride
	crashLogMu.Unlock()
	if override != "" {
		return override
	}
	return filepath.Join(openDBDir(), "crash.log")
}

// SetCrashLogPath overrides the crash log location. Pass "" to clear.
// Intended for tests; production code should rely on the default path.
func SetCrashLogPath(p string) {
	crashLogMu.Lock()
	crashLogPathOverride = p
	crashLogMu.Unlock()
}

// WriteCrash appends a structured record for e to the crash log.
// Failure to write is silent by design — we never want logging to crash
// the recovery path.
func WriteCrash(e *Error) {
	if e == nil {
		return
	}
	crashLogMu.Lock()
	defer crashLogMu.Unlock()

	path := crashLogPathOverride
	if path == "" {
		path = filepath.Join(openDBDir(), "crash.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	rotateIfLarge(path)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := e.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	entry, _ := Lookup(e.Code)

	fmt.Fprintf(f, "[%s] %s %s\n", ts.Format("2006-01-02 15:04:05"), e.Code, e.Severity)
	fmt.Fprintf(f, "Title:   %s\n", entry.Title)
	fmt.Fprintf(f, "Message: %s\n", e.Message)
	if e.Cause != nil {
		fmt.Fprintf(f, "Cause:   %v\n", e.Cause)
	}
	if e.Stack != "" {
		fmt.Fprintf(f, "Stack:\n%s\n", e.Stack)
	}
	fmt.Fprintln(f, "──────────────────────────────────────────────")
}

func rotateIfLarge(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < maxCrashLogSize {
		return
	}
	_ = os.Rename(path, path+".old")
}

// openDBDir resolves the active brand's base directory.
// v1.1.20: brand-aware via internal/brand (was hardcoded ~/.opendb,
// causing dbaa crashes to write to opendb's directory).
// Cannot import internal/config because config can panic and report
// errors via odberr → would create circular dependency.
func openDBDir() string {
	br := brand.Current()
	if dir := os.Getenv(br.ConfigEnvVar); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return br.ConfigDirName
	}
	return filepath.Join(home, br.ConfigDirName)
}
