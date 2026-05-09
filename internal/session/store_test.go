/*-------------------------------------------------------------------------
 *
 * store_test.go
 *	  Test cases for store.go (session package): TestStore_GetOrCreate,
 *	  TestStore_GetOrCreate_Different, TestStore_List.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/session/store_test.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_GetOrCreate(t *testing.T) {
	store := NewStore(t.TempDir())

	sh1 := store.GetOrCreate("mydb")
	sh2 := store.GetOrCreate("mydb")

	if sh1 != sh2 {
		t.Error("GetOrCreate with same name should return the same instance")
	}
}

func TestStore_GetOrCreate_Different(t *testing.T) {
	store := NewStore(t.TempDir())

	sh1 := store.GetOrCreate("db1")
	sh2 := store.GetOrCreate("db2")

	if sh1 == sh2 {
		t.Error("GetOrCreate with different names should return different instances")
	}
}

func TestStore_List(t *testing.T) {
	store := NewStore(t.TempDir())

	store.GetOrCreate("charlie")
	store.GetOrCreate("alpha")
	store.GetOrCreate("bravo")

	names := store.List()
	if len(names) != 3 {
		t.Fatalf("List() returned %d names, want 3", len(names))
	}

	// Verify sorted order
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("List()[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestStore_SaveAll(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sh1 := store.GetOrCreate("db1")
	sh1.Add("SELECT 1")

	sh2 := store.GetOrCreate("db2")
	sh2.Add("SELECT 2")

	if err := store.SaveAll(); err != nil {
		t.Fatalf("SaveAll() error: %v", err)
	}

	// Verify both files were created
	for _, name := range []string{"db1", "db2"} {
		filePath := filepath.Join(dir, name+".json")
		info, err := os.Stat(filePath)
		if err != nil {
			t.Errorf("file for %q not created: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("file for %q is empty", name)
		}
	}
}
