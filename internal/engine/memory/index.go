/*-------------------------------------------------------------------------
 *
 * index.go
 *	  AppendToIndex appends a single entry line to MEMORY.md. Creates
 *	  the file if it does not exist.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/index.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AppendToIndex appends a single entry line to MEMORY.md.
// Creates the file if it does not exist.
func AppendToIndex(dir, entry string) error {
	path := filepath.Join(dir, IndexFile)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintln(f, entry)
	return err
}

// RebuildIndex scans all memory files in dir and regenerates MEMORY.md.
// Groups entries by type, sorted by modification time (newest first).
func RebuildIndex(dir string) error {
	files := listMemoryFiles(dir)
	if len(files) == 0 {
		// Remove index if no memory files exist
		os.Remove(filepath.Join(dir, IndexFile))
		return nil
	}

	// Read frontmatter from each file
	type indexEntry struct {
		filename string
		name     string
		memType  string
		mtime    time.Time
	}

	var entries []indexEntry
	for _, f := range files {
		data, err := readFirstNLines(f.path, MaxFrontmatterLines)
		if err != nil {
			continue
		}
		name := extractNameFromFrontmatter(data)
		memType := extractTypeFromFrontmatter(data)
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(f.path), ".md")
		}
		entries = append(entries, indexEntry{
			filename: filepath.Base(f.path),
			name:     name,
			memType:  memType,
			mtime:    f.mtime,
		})
	}

	// Sort newest first within each group
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime.After(entries[j].mtime)
	})

	// Build grouped index
	var buf strings.Builder
	buf.WriteString("# 实例记忆索引\n\n")

	typeOrder := []MemoryType{MemIncident, MemSolution, MemPreference, MemWorkload, MemPattern}
	for _, mt := range typeOrder {
		var group []indexEntry
		for _, e := range entries {
			if e.memType == string(mt) {
				group = append(group, e)
			}
		}
		if len(group) == 0 {
			continue
		}

		buf.WriteString(fmt.Sprintf("## %s\n", mt.Label()))
		// Cap each type at MaxIndexEntriesPerType entries to keep MEMORY.md
		// small when injected into system prompt. Older entries still exist
		// on disk and can be read directly via memory_recall.
		shown := group
		truncatedN := 0
		if len(group) > MaxIndexEntriesPerType {
			shown = group[:MaxIndexEntriesPerType]
			truncatedN = len(group) - MaxIndexEntriesPerType
		}
		for _, e := range shown {
			age := humanizeAge(time.Since(e.mtime))
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", e.name, e.filename, age))
		}
		if truncatedN > 0 {
			buf.WriteString(fmt.Sprintf("- _...还有 %d 条更早记录，用 memory_recall 搜索_\n", truncatedN))
		}
		buf.WriteString("\n")
	}

	// Also include entries with unrecognized types
	var unknown []indexEntry
	for _, e := range entries {
		if !MemoryType(e.memType).IsValid() {
			unknown = append(unknown, e)
		}
	}
	if len(unknown) > 0 {
		buf.WriteString("## 其他\n")
		for _, e := range unknown {
			age := humanizeAge(time.Since(e.mtime))
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", e.name, e.filename, age))
		}
		buf.WriteString("\n")
	}

	return atomicWrite(filepath.Join(dir, IndexFile), []byte(buf.String()))
}

// TruncateEntrypoint truncates content to maxLines and maxBytes.
func TruncateEntrypoint(content string, maxLines, maxBytes int) string {
	if len(content) <= maxBytes {
		lines := strings.Split(content, "\n")
		if len(lines) <= maxLines {
			return content
		}
		return strings.Join(lines[:maxLines], "\n") + "\n... (索引已截断)\n"
	}

	// Byte limit hit — truncate by bytes first, then by lines
	content = content[:maxBytes]
	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n") + "\n... (索引已截断)\n"
}

// readFirstNLines reads the first n lines of a file.
func readFirstNLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(lines) < n {
		lines = append(lines, scanner.Text())
	}
	return strings.Join(lines, "\n"), nil
}

// humanizeAge converts a duration to a human-readable Chinese string.
func humanizeAge(d time.Duration) string {
	hours := int(d.Hours())
	if hours < 1 {
		return "刚刚"
	}
	if hours < 24 {
		return fmt.Sprintf("%d小时前", hours)
	}
	days := hours / 24
	if days == 1 {
		return "昨天"
	}
	if days < 7 {
		return fmt.Sprintf("%d天前", days)
	}
	weeks := days / 7
	if weeks < 4 {
		return fmt.Sprintf("%d周前", weeks)
	}
	months := days / 30
	if months < 12 {
		return fmt.Sprintf("%d月前", months)
	}
	return fmt.Sprintf("%d年前", days/365)
}
