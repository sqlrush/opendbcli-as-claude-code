/*-------------------------------------------------------------------------
 *
 * textflame.go
 *	  FormatTextFlame generates a tree-style ASCII flame graph from
 *	  collapsed stack data. Style: compact tree with percentage bars and
 *	  ★ hotspot markers.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/textflame.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	textFlameBarWidth  = 35
	textFlameMinPct    = 2.0
	textFlameMaxDepth  = 12
	textFlameLineWidth = 80 // total chars before bar: "  " + marker(2) + nameField
)

// FormatTextFlame generates a tree-style ASCII flame graph from collapsed stack data.
// Style: compact tree with percentage bars and ★ hotspot markers.
func FormatTextFlame(collapsed string, width int) string {
	if collapsed == "" {
		return "  (no stack data)"
	}

	root := buildFrameTree(collapsed)
	if root.samples == 0 {
		return "  (no samples)"
	}

	total := root.samples
	hotspots := findHotspots(root, total)

	var sb strings.Builder
	sb.WriteString("  [FLAME_GRAPH_START]\n")
	sb.WriteString(fmt.Sprintf("  Flame Graph  %d samples\n", total))
	sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("─", textFlameLineWidth+textFlameBarWidth+10)))

	// Render root's significant children
	children := significantChildren(root, total)
	for i, child := range children {
		isLast := i == len(children)-1
		prefix := "├─ "
		childPrefix := "│  "
		if isLast {
			prefix = "└─ "
			childPrefix = "   "
		}
		renderNode(&sb, child, prefix, childPrefix, total, hotspots, 0)
	}

	sb.WriteString(fmt.Sprintf("  %s\n", strings.Repeat("─", textFlameLineWidth+textFlameBarWidth+10)))
	if len(hotspots) > 0 {
		sb.WriteString("  * = 阻塞热点\n")
	}
	sb.WriteString("  [FLAME_GRAPH_END]\n")
	return sb.String()
}

// renderNode recursively renders a node and its children.
func renderNode(sb *strings.Builder, f *frame, prefix, childPrefix string, total int, hotspots map[string]bool, depth int) {
	if depth > textFlameMaxDepth || f.samples == 0 {
		return
	}

	pct := float64(f.samples) / float64(total) * 100.0
	if pct < textFlameMinPct {
		return
	}

	// Compress single-child chains: a → b → c when each has same sample count
	displayName := f.name
	current := f
	for {
		kids := significantChildren(current, total)
		if len(kids) != 1 {
			break
		}
		child := kids[0]
		childPct := float64(child.samples) / float64(total) * 100.0
		if pct-childPct > 1.0 {
			break // significant sample drop, don't compress
		}
		displayName += " → " + child.name
		current = child
		pct = childPct
	}

	// Hotspot marker
	isHot := hotspots[f.name] || hotspots[current.name]

	// Render the line with fixed-width alignment
	// Layout: [marker 2][prefix + name ... padded to lineWidth-4][bar][pct]
	bar := renderBar(pct)
	nameField := prefix + displayName

	// Available width for name = lineWidth - 4 (2 indent + 2 marker)
	maxNameWidth := textFlameLineWidth - 4
	nameWidth := utf8.RuneCountInString(nameField)
	if nameWidth > maxNameWidth {
		runes := []rune(nameField)
		nameField = string(runes[:maxNameWidth-2]) + ".."
		nameWidth = maxNameWidth
	}
	pad := maxNameWidth - nameWidth
	if pad < 0 {
		pad = 0
	}

	marker := "  "
	if isHot {
		marker = "* "
	}

	sb.WriteString(fmt.Sprintf("  %s%s%s %s %5.1f%%\n", marker, nameField, strings.Repeat(" ", pad), bar, pct))

	// Render children of the compressed end node
	children := significantChildren(current, total)
	for i, child := range children {
		isLast := i == len(children)-1
		connector := childPrefix + "├─ "
		nextChildPrefix := childPrefix + "│  "
		if isLast {
			connector = childPrefix + "└─ "
			nextChildPrefix = childPrefix + "   "
		}
		renderNode(sb, child, connector, nextChildPrefix, total, hotspots, depth+1)
	}
}

// renderBar returns a bar string proportional to the percentage.
func renderBar(pct float64) string {
	barLen := int(pct / 100.0 * textFlameBarWidth)
	if barLen < 1 {
		barLen = 1
	}
	return strings.Repeat("▓", barLen)
}

// significantChildren returns children with >= minPct, sorted by samples desc.
func significantChildren(f *frame, total int) []*frame {
	var result []*frame
	for _, c := range f.children {
		pct := float64(c.samples) / float64(total) * 100.0
		if pct >= textFlameMinPct {
			result = append(result, c)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].samples > result[j].samples
	})
	return result
}

// findHotspots identifies blocking points: leaf functions or functions with high self-time.
func findHotspots(root *frame, total int) map[string]bool {
	hotspots := make(map[string]bool)
	findHotspotsWalk(root, total, hotspots)
	return hotspots
}

func findHotspotsWalk(f *frame, total int, hotspots map[string]bool) {
	if f.samples == 0 {
		return
	}

	childSamples := 0
	for _, c := range f.children {
		childSamples += c.samples
	}
	selfSamples := f.samples - childSamples
	selfPct := float64(selfSamples) / float64(total) * 100.0

	// High self-time = this function is where time is actually spent (not just passing through)
	if selfPct >= 15.0 && f.name != "root" {
		hotspots[f.name] = true
	}

	// Leaf with significant total (>= 15%)
	if len(f.children) == 0 {
		pct := float64(f.samples) / float64(total) * 100.0
		if pct >= 15.0 {
			hotspots[f.name] = true
		}
	}

	for _, c := range f.children {
		findHotspotsWalk(c, total, hotspots)
	}
}

// FormatCollapsedSummary returns a compact summary of the top N collapsed stacks.
func FormatCollapsedSummary(collapsed string, topN int) string {
	if collapsed == "" {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(collapsed), "\n")
	if topN > len(lines) {
		topN = len(lines)
	}

	var sb strings.Builder
	for i := 0; i < topN; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lastSpace := strings.LastIndex(line, " ")
		if lastSpace < 0 {
			continue
		}
		stack := line[:lastSpace]
		countStr := line[lastSpace+1:]
		count := 0
		fmt.Sscanf(countStr, "%d", &count)

		parts := strings.Split(stack, ";")
		short := shortenStack(parts)
		sb.WriteString(fmt.Sprintf("  %3d  %s\n", count, short))
	}
	return sb.String()
}

// shortenStack shortens a stack trace for display, keeping key frames.
func shortenStack(parts []string) string {
	if len(parts) <= 5 {
		return strings.Join(parts, " → ")
	}
	result := make([]string, 0, 6)
	result = append(result, parts[0], parts[1])
	result = append(result, "...")
	result = append(result, parts[len(parts)-3:]...)
	return strings.Join(result, " → ")
}
