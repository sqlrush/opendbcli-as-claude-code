/*-------------------------------------------------------------------------
 *
 * store_test.go
 *	  Test cases for store.go (memory package): TestStore_WriteAndRead,
 *	  TestStore_WriteInvalidType, TestStore_WriteNoInstance.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/store_test.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("mysql-prod")

	filename, err := s.Write(MemIncident, "Buffer Pool冷启动", "重启后IO打满")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if filename == "" {
		t.Fatal("empty filename")
	}

	content, err := s.Read(filename)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content == "" {
		t.Fatal("empty content")
	}
	if !contains(content, "Buffer Pool冷启动") {
		t.Error("content missing title in frontmatter")
	}
	if !contains(content, "重启后IO打满") {
		t.Error("content missing body")
	}
}

func TestStore_WriteInvalidType(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("mysql-prod")

	_, err := s.Write("invalid", "test", "content")
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestStore_WriteNoInstance(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	_, err := s.Write(MemIncident, "test", "content")
	if err == nil {
		t.Error("expected error when no instance set")
	}
}

func TestStore_Update(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("mysql-prod")

	filename, _ := s.Write(MemWorkload, "负载特征", "白天OLTP")

	err := s.Update(filename, "负载特征-更新", "白天OLTP，晚上跑批")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	content, _ := s.Read(filename)
	if !contains(content, "晚上跑批") {
		t.Error("update not applied")
	}
	if !contains(content, "负载特征-更新") {
		t.Error("title not updated")
	}
}

func TestStore_LoadIndex(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("mysql-prod")

	// No memories yet
	idx, err := s.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if idx != "" {
		t.Error("expected empty index for new instance")
	}

	// Write a memory
	s.Write(MemIncident, "OOM事件", "内存溢出")

	idx, err = s.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if idx == "" {
		t.Error("expected non-empty index after write")
	}
}

func TestStore_ProfileWriteAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("mysql-prod")

	// Profile doesn't exist yet
	if s.ProfileExists() {
		t.Error("profile should not exist initially")
	}

	// Write profile
	err := s.WriteProfile(ProfileTemplate("mysql-prod", "mysql"))
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}

	if !s.ProfileExists() {
		t.Error("profile should exist after write")
	}

	content, err := s.LoadProfile()
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if !contains(content, "mysql-prod") {
		t.Error("profile missing instance name")
	}
}

func TestStore_EnforceFileLimit(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("test-inst")

	// Create MaxFiles + 5 files
	for i := 0; i < MaxFiles+5; i++ {
		s.Write(MemIncident, fmt.Sprintf("event_%d", i), "content")
	}

	// Count remaining memory files (excluding MEMORY.md, PROFILE.md)
	files := listMemoryFiles(filepath.Join(dir, "test-inst"))
	if len(files) > MaxFiles {
		t.Errorf("got %d files, want <= %d", len(files), MaxFiles)
	}
}

func TestRebuildIndex(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.SetActiveInstance("mysql-prod")

	s.Write(MemIncident, "OOM事件", "内存溢出")
	s.Write(MemSolution, "降低连接数", "max_connections设为200")
	s.Write(MemWorkload, "白天OLTP", "9-18点高峰")

	instDir := filepath.Join(dir, "mysql-prod")
	err := RebuildIndex(instDir)
	if err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(instDir, IndexFile))
	idx := string(data)

	// Should contain type headers
	if !contains(idx, "过往问题") {
		t.Error("missing incident section")
	}
	if !contains(idx, "解决方案") {
		t.Error("missing solution section")
	}
	if !contains(idx, "负载特征") {
		t.Error("missing workload section")
	}
}

func TestTruncateEntrypoint(t *testing.T) {
	// Within limits
	short := "line1\nline2\nline3"
	if got := TruncateEntrypoint(short, 200, 25000); got != short {
		t.Errorf("should not truncate short content")
	}

	// Exceed line limit
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	long := joinLines(lines)
	result := TruncateEntrypoint(long, 200, 100000)
	resultLines := len(splitLines(result))
	if resultLines > 202 { // 200 + truncation message
		t.Errorf("got %d lines, want <= 202", resultLines)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Buffer Pool 冷启动", "buffer_pool_冷启动"},
		{"OOM / 内存溢出", "oom__内存溢出"},
		{"", "unnamed"},
	}
	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHumanizeAge(t *testing.T) {
	tests := []struct {
		hours int
		want  string
	}{
		{0, "刚刚"},
		{3, "3小时前"},
		{25, "昨天"},
		{72, "3天前"},
		{168, "1周前"},
		{720, "1月前"},
	}
	for _, tt := range tests {
		got := humanizeAge(time.Duration(tt.hours) * time.Hour)
		if got != tt.want {
			t.Errorf("humanizeAge(%dh) = %q, want %q", tt.hours, got, tt.want)
		}
	}
}

// helpers

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func joinLines(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestProfileTemplateGaussDBUsesOpenGaussChecklist(t *testing.T) {
	tpl := ProfileTemplate("gauss-prod", "gaussdb")
	for _, want := range []string{"(GaussDB)", "GaussDB 版本", "WDR 快照", "MOT 内存引擎"} {
		if !strings.Contains(tpl, want) {
			t.Fatalf("GaussDB profile template missing %q:\n%s", want, tpl)
		}
	}
}
