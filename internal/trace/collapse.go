/*-------------------------------------------------------------------------
 *
 * collapse.go
 *	  CollapseStacks converts raw `perf script` output into folded stack
 *	  format. Each unique stack is aggregated with a count. Output is
 *	  sorted by count descending.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/collapse.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CollapseStacks converts raw `perf script` output into folded stack format.
// Each unique stack is aggregated with a count. Output is sorted by count descending.
func CollapseStacks(raw string) string {
	if raw == "" {
		return ""
	}

	counts := map[string]int{}
	blocks := splitBlocks(raw)

	for _, block := range blocks {
		stack := parseBlock(block)
		if len(stack) == 0 {
			continue
		}
		reversed := reverseStack(stack)
		key := strings.Join(reversed, ";")
		counts[key]++
	}

	if len(counts) == 0 {
		return ""
	}

	type entry struct {
		key   string
		count int
	}

	entries := make([]entry, 0, len(counts))
	for k, c := range counts {
		entries = append(entries, entry{k, c})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].key < entries[j].key
	})

	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s %d\n", e.key, e.count)
	}
	return sb.String()
}

// ExtractTopFuncs extracts the top N hottest leaf functions from collapsed stacks.
// Leaf function is the last element in each semicolon-separated stack line.
func ExtractTopFuncs(collapsed string, topN int) []HotFunc {
	if collapsed == "" {
		return nil
	}

	type leafEntry struct {
		samples int
		stack   string
	}

	byLeaf := map[string]*leafEntry{}
	totalSamples := 0

	for _, line := range strings.Split(strings.TrimSpace(collapsed), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lastSpace := strings.LastIndex(line, " ")
		if lastSpace < 0 {
			continue
		}

		stack := line[:lastSpace]
		countStr := line[lastSpace+1:]

		count, err := strconv.Atoi(countStr)
		if err != nil {
			continue
		}

		parts := strings.Split(stack, ";")
		leaf := parts[len(parts)-1]
		if leaf == "" {
			continue
		}

		totalSamples += count

		if e, ok := byLeaf[leaf]; ok {
			e.samples += count
		} else {
			byLeaf[leaf] = &leafEntry{samples: count, stack: stack}
		}
	}

	if len(byLeaf) == 0 {
		return nil
	}

	type ranked struct {
		name    string
		samples int
		stack   string
	}

	ranked2 := make([]ranked, 0, len(byLeaf))
	for name, e := range byLeaf {
		ranked2 = append(ranked2, ranked{name, e.samples, e.stack})
	}

	sort.Slice(ranked2, func(i, j int) bool {
		if ranked2[i].samples != ranked2[j].samples {
			return ranked2[i].samples > ranked2[j].samples
		}
		return ranked2[i].name < ranked2[j].name
	})

	if topN > len(ranked2) {
		topN = len(ranked2)
	}

	result := make([]HotFunc, topN)
	for i := 0; i < topN; i++ {
		pct := 0.0
		if totalSamples > 0 {
			pct = float64(ranked2[i].samples) / float64(totalSamples) * 100.0
		}
		result[i] = HotFunc{
			Name:       ranked2[i].name,
			Samples:    ranked2[i].samples,
			Percentage: pct,
			Stack:      ranked2[i].stack,
		}
	}
	return result
}

// splitBlocks splits raw perf script output into per-sample blocks separated by blank lines.
func splitBlocks(raw string) []string {
	var blocks []string
	var current []string

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(current) > 0 {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = current[:0]
			}
			continue
		}
		current = append(current, line)
	}

	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

// parseBlock parses one perf sample block and returns stack frames (leaf first).
func parseBlock(block string) []string {
	lines := strings.Split(block, "\n")
	var frames []string

	for _, line := range lines {
		// Header lines are not indented with tab/space in the frame area.
		// Frame lines start with whitespace.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			continue
		}
		name := parseFuncName(line)
		if name == "" {
			continue
		}
		frames = append(frames, name)
	}
	return frames
}

// reverseStack returns a new slice with elements in reverse order.
func reverseStack(stack []string) []string {
	n := len(stack)
	reversed := make([]string, n)
	for i, v := range stack {
		reversed[n-1-i] = v
	}
	return reversed
}

// parseFuncName extracts the function name from a perf stack frame line.
// Format: "    addr funcname+0xoffset (/path/to/binary)" or "    addr funcname (/path)".
// Returns "" if the entry should be skipped ([unknown] or hex-only address).
func parseFuncName(line string) string {
	fields := strings.Fields(line)
	// Minimum: addr funcname
	if len(fields) < 2 {
		return ""
	}

	// fields[0] is the hex address, fields[1] is the function name (with optional +offset).
	raw := fields[1]

	// Strip +0xoffset suffix.
	if plus := strings.Index(raw, "+"); plus >= 0 {
		raw = raw[:plus]
	}

	// Skip [unknown] and hex-only addresses.
	if raw == "[unknown]" || isHexAddr(raw) {
		return ""
	}

	return raw
}

// isHexAddr reports whether s looks like a bare hex address (e.g. "0xdeadbeef" or "deadbeef").
func isHexAddr(s string) bool {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
