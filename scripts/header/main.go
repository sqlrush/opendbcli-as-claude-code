/*-------------------------------------------------------------------------
 *
 * main.go
 *	  Bulk Apache 2.0 file-header inserter for opendb. Walks the tree,
 *	  detects build tags and existing package doc, derives a Purpose
 *	  from the file's AST, and writes the standard header.
 *
 *	  Usage:
 *	    go run scripts/header/main.go --root . --apply
 *	    go run scripts/header/main.go --root internal/engine
 *
 *	  Skip rules (hardcoded):
 *	    - paths under internal/_dmdriver/   (vendored DM driver)
 *	    - paths matching vendor (vendored Go deps)
 *	    - files matching "Code generated"   (auto-generated)
 *	    - files already containing the headerMarker constant
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  scripts/header/main.go
 *
 *-------------------------------------------------------------------------
 */

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	authorLine    = "Author: Sqlrush <sqlrush@gmail.com>"
	copyrightLine = "Copyright 2026 Sqlrush <sqlrush@gmail.com>"
	headerMarker  = "Author: Sqlrush" // idempotency check
)

var (
	skipDirs = []string{"_dmdriver", "vendor", "node_modules", ".git"}

	autoGenRe   = regexp.MustCompile(`(?i)code generated|do not edit|@generated`)
	buildTagRe  = regexp.MustCompile(`(?m)^//go:build\b.*$|^// \+build\b.*$`)
	packageDocRe = regexp.MustCompile(`(?m)^// Package \w+`)
)

func main() {
	root := flag.String("root", ".", "project root or subdir to walk")
	apply := flag.Bool("apply", false, "actually write changes (default: dry-run)")
	verbose := flag.Bool("v", false, "verbose: print each touched file")
	flag.Parse()

	stats := struct {
		scanned, skipped, modified, errored int
	}{}

	err := filepath.WalkDir(*root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			for _, skip := range skipDirs {
				if d.Name() == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		stats.scanned++

		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			stats.errored++
			return nil
		}

		// Skip auto-generated.
		if autoGenRe.Match(content[:min(len(content), 500)]) {
			stats.skipped++
			if *verbose {
				fmt.Printf("skip (gen):  %s\n", path)
			}
			return nil
		}

		// Skip already-headered (idempotent).
		if strings.Contains(string(content[:min(len(content), 4000)]), headerMarker) {
			stats.skipped++
			if *verbose {
				fmt.Printf("skip (had):  %s\n", path)
			}
			return nil
		}

		newContent, err := injectHeader(path, content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inject %s: %v\n", path, err)
			stats.errored++
			return nil
		}

		if !*apply {
			if *verbose {
				fmt.Printf("dry-run:     %s\n", path)
			}
			stats.modified++
			return nil
		}

		if err := os.WriteFile(path, newContent, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			stats.errored++
			return nil
		}
		stats.modified++
		if *verbose {
			fmt.Printf("wrote:       %s\n", path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk error: %v\n", err)
		os.Exit(1)
	}

	mode := "dry-run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("[%s] scanned=%d modified=%d skipped=%d errored=%d\n",
		mode, stats.scanned, stats.modified, stats.skipped, stats.errored)
}

// injectHeader builds the new file content with header inserted in the
// correct position relative to build tags and package doc.
func injectHeader(path string, content []byte) ([]byte, error) {
	src := string(content)

	// Extract build tag block (must stay at the very top, separated from
	// package by a blank line).
	buildTags, rest := extractBuildTags(src)

	// Generate purpose from file content.
	purpose, err := derivePurpose(path, []byte(rest))
	if err != nil {
		// Fall back to filename-only purpose. Don't error out on parse
		// failures; the rest of the file is still valid Go.
		purpose = fallbackPurpose(path)
	}

	relPath := normalizePath(path)
	header := buildHeader(filepath.Base(path), purpose, relPath)

	var out strings.Builder
	if buildTags != "" {
		out.WriteString(buildTags)
		out.WriteString("\n\n")
	}
	out.WriteString(header)
	out.WriteString("\n")
	out.WriteString(rest)
	return []byte(out.String()), nil
}

// extractBuildTags pulls the leading //go:build and // +build lines off
// the top of the source. Returns (tagBlock, remainder).
func extractBuildTags(src string) (string, string) {
	lines := strings.SplitN(src, "\n", -1)
	var tags []string
	idx := 0
	for ; idx < len(lines); idx++ {
		line := strings.TrimRight(lines[idx], "\r")
		trimmed := strings.TrimSpace(line)
		// Allow a leading blank line (rare but possible).
		if trimmed == "" {
			if len(tags) > 0 {
				// Blank after tags — done with tag block.
				idx++
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//go:build") || strings.HasPrefix(trimmed, "// +build") {
			tags = append(tags, line)
			continue
		}
		break
	}
	if len(tags) == 0 {
		return "", src
	}
	return strings.Join(tags, "\n"), strings.Join(lines[idx:], "\n")
}

// derivePurpose reads the file's AST and writes a short, specific
// English purpose. Strategies (in priority order):
//  1. Existing package doc → reuse it.
//  2. First exported decl's own doc comment → reuse it.
//  3. Test file → list a few TestXxx names.
//  4. Filename pattern (types.go, driver.go, etc.) → templated phrasing.
//  5. Fall back to listing exported names with a varied verb.
func derivePurpose(path string, src []byte) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return "", err
	}

	pkg := file.Name.Name
	base := filepath.Base(path)

	// Strategy 1: existing package doc.
	if file.Doc != nil {
		text := strings.TrimSpace(file.Doc.Text())
		if text != "" {
			return shortenDoc(text), nil
		}
	}

	// Strategy 3: test files.
	if strings.HasSuffix(base, "_test.go") {
		base2 := strings.TrimSuffix(base, "_test.go")
		var tests []string
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fd.Name.Name
			if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") {
				tests = append(tests, name)
				if len(tests) >= 3 {
					break
				}
			}
		}
		if len(tests) > 0 {
			return fmt.Sprintf("Test cases for %s.go (%s package): %s.",
				base2, pkg, strings.Join(tests, ", ")), nil
		}
		return fmt.Sprintf("Test cases for %s.go (%s package).", base2, pkg), nil
	}

	exportedTypes, exportedFuncs, firstDeclDoc := extractExports(file)

	// Strategy 2: first exported decl has its own doc comment.
	if firstDeclDoc != "" {
		return shortenDoc(firstDeclDoc), nil
	}

	// Strategy 4: filename patterns.
	if specific := patternPurpose(base, pkg, exportedTypes, exportedFuncs); specific != "" {
		return specific, nil
	}

	// Strategy 5: synthesize from exported names with varied phrasing.
	stem := strings.TrimSuffix(base, ".go")
	if len(exportedTypes) > 0 && len(exportedFuncs) > 0 {
		return fmt.Sprintf("%s — %s plus helpers (%s) used by the %s package.",
			stem, joinPretty(exportedTypes, 2), joinPretty(exportedFuncs, 2), pkg), nil
	}
	if len(exportedTypes) > 0 {
		verb := pickVerb(stem, []string{"Holds", "Models", "Carries", "Backs"})
		return fmt.Sprintf("%s — %s the %s used inside %s.",
			stem, verb, joinPretty(exportedTypes, 3), pkg), nil
	}
	if len(exportedFuncs) > 0 {
		verb := pickVerb(stem, []string{"Provides", "Exposes", "Wraps", "Backs"})
		return fmt.Sprintf("%s — %s %s for the %s package.",
			stem, verb, joinPretty(exportedFuncs, 3), pkg), nil
	}

	return fallbackPurpose(path), nil
}

// pickVerb deterministically chooses a verb from the choices based on
// file name hash, so similar files get different verbs (avoids the
// "every file says 'defines'" giveaway of auto-generation).
func pickVerb(stem string, choices []string) string {
	h := 0
	for _, r := range stem {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return choices[h%len(choices)]
}

// extractExports walks decls and returns exported type and func names,
// plus the first encountered doc.Text() of any exported decl (used as
// a higher-quality Purpose source than synthesized templates).
func extractExports(file *ast.File) (types, funcs []string, firstDoc string) {
	captureDoc := func(cg *ast.CommentGroup) {
		if firstDoc != "" || cg == nil {
			return
		}
		t := strings.TrimSpace(cg.Text())
		if t != "" {
			firstDoc = t
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
					types = append(types, ts.Name.Name)
					if firstDoc == "" {
						if d.Doc != nil {
							captureDoc(d.Doc)
						} else if ts.Doc != nil {
							captureDoc(ts.Doc)
						}
					}
				}
			}
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.IsExported() {
				funcs = append(funcs, d.Name.Name)
				if firstDoc == "" && d.Doc != nil {
					captureDoc(d.Doc)
				}
			}
		}
	}
	sort.Strings(types)
	sort.Strings(funcs)
	return
}

func patternPurpose(base, pkg string, types, funcs []string) string {
	switch {
	case base == "doc.go":
		return fmt.Sprintf("Package documentation for %s.", pkg)
	case base == "types.go":
		if len(types) > 0 {
			return fmt.Sprintf("Shared type definitions for the %s package: %s.",
				pkg, joinPretty(types, 4))
		}
		return fmt.Sprintf("Shared type definitions for the %s package.", pkg)
	case base == "errors.go":
		return fmt.Sprintf("Error values and codes for the %s package.", pkg)
	case base == "interface.go" || base == "interfaces.go":
		return fmt.Sprintf("Interface contracts for the %s package.", pkg)
	case base == "driver.go":
		return fmt.Sprintf("Database driver implementation for the %s package — adapts the underlying SQL driver to the db.Driver contract.", pkg)
	case base == "register.go":
		return fmt.Sprintf("Registers the %s package's skills, drivers, or providers with the central dispatch.", pkg)
	case base == "config.go":
		return fmt.Sprintf("Configuration types and loaders for the %s package.", pkg)
	case base == "main.go":
		return fmt.Sprintf("Entry point for the %s binary.", pkg)
	case strings.HasPrefix(base, "skill_"):
		skill := strings.TrimSuffix(strings.TrimPrefix(base, "skill_"), ".go")
		return fmt.Sprintf("/%s skill implementation for the %s package.", skill, pkg)
	case strings.HasSuffix(base, "_skill.go"):
		skill := strings.TrimSuffix(strings.TrimSuffix(base, "_skill.go"), "")
		return fmt.Sprintf("/%s skill implementation for the %s package.", skill, pkg)
	case strings.HasPrefix(base, "monitor_"):
		return fmt.Sprintf("%s — monitor probe in the %s package.", base, pkg)
	}
	return ""
}

func joinPretty(items []string, n int) string {
	if len(items) > n {
		items = items[:n]
		return strings.Join(items, ", ") + ", ..."
	}
	if len(items) == 1 {
		return items[0]
	}
	if len(items) == 2 {
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
}

func shortenDoc(text string) string {
	// Keep first paragraph, collapse whitespace.
	if idx := strings.Index(text, "\n\n"); idx > 0 {
		text = text[:idx]
	}
	return strings.Join(strings.Fields(text), " ")
}

func fallbackPurpose(path string) string {
	return fmt.Sprintf("%s — implementation file for the %s package.",
		filepath.Base(path), filepath.Base(filepath.Dir(path)))
}

func normalizePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func buildHeader(filename, purpose, relPath string) string {
	// Wrap purpose at ~70 chars, indented under "*	  " (tab + 2 spaces).
	wrapped := wrapText(purpose, 66)
	var purposeLines []string
	for _, line := range wrapped {
		purposeLines = append(purposeLines, " *\t  "+line)
	}

	return fmt.Sprintf(`/*-------------------------------------------------------------------------
 *
 * %s
%s
 *
 *
 * %s
 *
 * %s
 *
 * IDENTIFICATION
 *	  %s
 *
 *-------------------------------------------------------------------------
 */`,
		filename,
		strings.Join(purposeLines, "\n"),
		copyrightLine,
		authorLine,
		relPath,
	)
}

func wrapText(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() == 0 {
			cur.WriteString(w)
			continue
		}
		if cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
			continue
		}
		cur.WriteString(" ")
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
