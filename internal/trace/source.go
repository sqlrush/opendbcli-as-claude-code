/*-------------------------------------------------------------------------
 *
 * source.go
 *	  SourceLookup searches a database source tree for hot function
 *	  definitions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/source.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SourceLookup searches a database source tree for hot function definitions.
type SourceLookup struct {
	SourceDir string
}

// Grep finds function definitions for the given hot functions in the source tree.
// Returns only functions that were actually found. If SourceDir is empty, returns nil.
func (s *SourceLookup) Grep(funcs []HotFunc) ([]FuncSource, error) {
	if s.SourceDir == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.SourceDir); os.IsNotExist(err) {
		return nil, nil
	}
	var results []FuncSource
	for _, f := range funcs {
		src, found := s.findFunc(f.Name)
		if found {
			results = append(results, src)
		}
	}
	return results, nil
}

// findFunc walks the source tree looking for a function definition.
func (s *SourceLookup) findFunc(name string) (FuncSource, bool) {
	shortName := stripClassPrefix(name)
	var found FuncSource
	var located bool

	err := filepath.Walk(s.SourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			if skipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSupportedExt(filepath.Ext(path)) {
			return nil
		}
		line, snippet := searchInFile(path, shortName, name)
		if line > 0 {
			found = FuncSource{
				FuncName: name,
				FilePath: path,
				Line:     line,
				Snippet:  snippet,
			}
			located = true
			return fmt.Errorf("stop") // sentinel to stop Walk
		}
		return nil
	})
	if err != nil && err.Error() != "stop" {
		// real walk error — ignore, return not found
		return FuncSource{}, false
	}
	return found, located
}

// stripClassPrefix converts "ha_innodb::write_row" → "write_row".
// If there is no "::", the original name is returned.
func stripClassPrefix(name string) string {
	if idx := strings.LastIndex(name, "::"); idx >= 0 {
		return name[idx+2:]
	}
	return name
}

// skipDir returns true for directories that should be skipped during walk.
func skipDir(name string) bool {
	switch name {
	case ".git", "test", "tests", "unittest":
		return true
	}
	return false
}

// isSupportedExt returns true for C/C++/Go source extensions.
func isSupportedExt(ext string) bool {
	switch ext {
	case ".c", ".cc", ".cpp", ".h", ".go":
		return true
	}
	return false
}

// searchInFile scans a file for a function definition matching shortName or fullName.
// Returns the 1-based line number and a code snippet, or (0, "") if not found.
func searchInFile(path, shortName, fullName string) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if isFuncDef(line, shortName) || isFuncDef(line, fullName) {
			snippet := collectSnippet(path, lineNum, 50)
			return lineNum, snippet
		}
	}
	return 0, ""
}

// isFuncDef returns true when line looks like a function definition for funcName.
// Heuristic: funcName + "(" present, and line is not deeply indented (definition vs call).
func isFuncDef(line, funcName string) bool {
	if funcName == "" {
		return false
	}
	if !strings.Contains(line, funcName+"(") {
		return false
	}
	// Count leading spaces/tabs — deeply indented lines are likely call sites.
	indent := 0
	for _, ch := range line {
		if ch == ' ' {
			indent++
		} else if ch == '\t' {
			indent += 4
		} else {
			break
		}
	}
	return indent < 8
}

// collectSnippet reads up to maxLines lines from startLine (1-based) in path.
// It stops early when it encounters a closing "}" at the start of a line.
func collectSnippet(path string, startLine, maxLines int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	lineNum := 0
	collected := 0
	for scanner.Scan() && collected < maxLines {
		lineNum++
		if lineNum < startLine {
			continue
		}
		line := scanner.Text()
		sb.WriteString(line)
		sb.WriteByte('\n')
		collected++
		if strings.HasPrefix(strings.TrimSpace(line), "}") && collected > 1 {
			break
		}
	}
	return sb.String()
}
