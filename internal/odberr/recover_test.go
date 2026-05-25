/*-------------------------------------------------------------------------
 *
 * recover_test.go
 *	  Test cases for recover.go (odberr package):
 *	  TestGuard_CapturesPanicAsError, TestGuard_NoPanicReturnsNil,
 *	  TestGuard_PanicWithError.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/recover_test.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGuard_CapturesPanicAsError(t *testing.T) {
	// Not parallel: we mutate crash log path.
	tmp := filepath.Join(t.TempDir(), "crash.log")
	SetCrashLogPath(tmp)
	t.Cleanup(func() { SetCrashLogPath("") })

	e := Guard(ErrUIDiagRender, func() {
		panic("boom")
	})

	if e == nil {
		t.Fatal("Guard returned nil on panic")
	}
	if e.Code != ErrUIDiagRender {
		t.Fatalf("code = %q, want %s", e.Code, ErrUIDiagRender)
	}
	if e.Stack == "" {
		t.Fatal("stack not captured")
	}
	if !strings.Contains(e.Error(), "boom") {
		t.Fatalf("panic value lost: %q", e.Error())
	}
}

func TestGuard_NoPanicReturnsNil(t *testing.T) {
	t.Parallel()
	ran := false
	e := Guard(ErrUIDiagRender, func() { ran = true })
	if e != nil {
		t.Fatalf("Guard returned error on clean run: %v", e)
	}
	if !ran {
		t.Fatal("fn was not executed")
	}
}

func TestGuard_PanicWithError(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "crash.log")
	SetCrashLogPath(tmp)
	t.Cleanup(func() { SetCrashLogPath("") })

	cause := errors.New("specific cause")
	e := Guard(ErrSkillExec, func() {
		panic(cause)
	})

	if e == nil {
		t.Fatal("Guard returned nil")
	}
	if !errors.Is(e, cause) {
		t.Fatal("cause not unwrappable via errors.Is")
	}
}

func TestSafeGo_DoesNotEscapePanic(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "crash.log")
	SetCrashLogPath(tmp)
	t.Cleanup(func() { SetCrashLogPath("") })

	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(ErrSentinelLoop, func() {
		defer wg.Done()
		panic("goroutine explode")
	})
	wg.Wait()

	// Recovery (WriteCrash) runs in SafeGo's outer defer, which fires AFTER
	// the user's defer wg.Done(). Poll briefly for the crash log to land.
	deadline := time.Now().Add(2 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		d, err := os.ReadFile(tmp)
		if err == nil && strings.Contains(string(d), ErrSentinelLoop) {
			data = d
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if data == nil {
		t.Fatal("crash log never written within 2s")
	}
	if !strings.Contains(string(data), "goroutine explode") {
		t.Fatalf("crash log missing message: %s", string(data))
	}
}

func TestCrashLog_Rotation(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "crash.log")
	SetCrashLogPath(tmp)
	t.Cleanup(func() { SetCrashLogPath("") })

	// Seed file beyond rotation threshold.
	big := make([]byte, maxCrashLogSize+10)
	if err := os.WriteFile(tmp, big, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	WriteCrash(New(ErrUIDiagRender, "post-rotation"))

	if _, err := os.Stat(tmp + ".old"); err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatalf("new crash.log missing: %v", err)
	}
	if info.Size() >= int64(maxCrashLogSize) {
		t.Fatalf("new log should start small, got %d bytes", info.Size())
	}
}
