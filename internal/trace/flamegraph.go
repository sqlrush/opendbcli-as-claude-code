/*-------------------------------------------------------------------------
 *
 * flamegraph.go
 *	  GenerateSVG parses folded stack data and writes an interactive SVG
 *	  flame graph to outPath. collapsed format: "func1;func2;func3
 *	  count\n..."
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/flamegraph.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	frameHeight = 16
	svgWidth    = 1200
	minWidth    = 0.1
	fontSize    = 11
	fontFamily  = "monospace"
)

// frame represents a node in the call-stack tree.
type frame struct {
	name     string
	samples  int
	children []*frame
}

// GenerateSVG parses folded stack data and writes an interactive SVG flame graph to outPath.
// collapsed format: "func1;func2;func3 count\n..."
func GenerateSVG(collapsed string, outPath string, title string) error {
	collapsed = strings.TrimSpace(collapsed)
	if collapsed == "" {
		return fmt.Errorf("flamegraph: collapsed stack data is empty")
	}

	root := buildFrameTree(collapsed)
	if root.samples == 0 {
		return fmt.Errorf("flamegraph: no samples parsed from collapsed data")
	}

	svg := renderSVG(root, title)

	if err := os.WriteFile(outPath, []byte(svg), 0o644); err != nil {
		return fmt.Errorf("flamegraph: write SVG to %s: %w", outPath, err)
	}
	return nil
}

// buildFrameTree constructs the tree from folded stack format.
func buildFrameTree(collapsed string) *frame {
	root := &frame{name: "root"}

	for _, line := range strings.Split(collapsed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lastSpace := strings.LastIndex(line, " ")
		if lastSpace < 0 {
			continue
		}

		stackPart := line[:lastSpace]
		countPart := line[lastSpace+1:]

		count, err := strconv.Atoi(strings.TrimSpace(countPart))
		if err != nil || count <= 0 {
			continue
		}

		funcs := strings.Split(stackPart, ";")
		current := root
		root.samples += count

		for _, fn := range funcs {
			fn = strings.TrimSpace(fn)
			if fn == "" {
				continue
			}
			child := findChild(current, fn)
			if child == nil {
				child = &frame{name: fn}
				current.children = append(current.children, child)
			}
			child.samples += count
			current = child
		}
	}

	return root
}

// findChild returns the child of parent with the given name, or nil if not found.
func findChild(parent *frame, name string) *frame {
	for _, c := range parent.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

// sortChildren sorts children by name for deterministic output.
func sortChildren(f *frame) {
	sort.Slice(f.children, func(i, j int) bool {
		return f.children[i].name < f.children[j].name
	})
	for _, c := range f.children {
		sortChildren(c)
	}
}

// calcDepth returns the maximum depth of the tree rooted at f.
func calcDepth(f *frame, current int) int {
	if len(f.children) == 0 {
		return current
	}
	max := current
	for _, c := range f.children {
		d := calcDepth(c, current+1)
		if d > max {
			max = d
		}
	}
	return max
}

// flameColor returns a deterministic warm (red-orange-yellow) color for the given name.
func flameColor(name string) (r, g, b int) {
	h := 0
	for _, ch := range name {
		h = h*31 + int(ch)
		if h < 0 {
			h = -h
		}
	}
	// red: 180-255, green: 50-180, blue: 0-50
	r = 180 + (h%76)
	g = 50 + ((h / 76) % 131)
	b = (h / (76 * 131)) % 51
	return r, g, b
}

// intMin returns the smaller of a and b.
func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renderSVG builds the complete SVG document string.
func renderSVG(root *frame, title string) string {
	sortChildren(root)

	depth := calcDepth(root, 0)
	totalDepth := depth + 1 // include root level
	titleBarH := 30
	svgHeight := totalDepth*frameHeight + titleBarH + frameHeight

	var sb strings.Builder

	// SVG header
	sb.WriteString(fmt.Sprintf(`<svg version="1.1" width="%d" height="%d" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`, svgWidth, svgHeight))
	sb.WriteString("\n")

	// Embedded CSS for hover effect
	sb.WriteString(`<style type="text/css">
.frame rect { stroke: #c8c8c8; stroke-width: 0.5; }
.frame:hover rect { stroke: #000; stroke-width: 1; }
.frame text { fill: #000; font-family: monospace; font-size: 11px; pointer-events: none; }
</style>`)
	sb.WriteString("\n")

	// Background
	sb.WriteString(fmt.Sprintf(`<rect width="%d" height="%d" fill="#f8f8f0"/>`, svgWidth, svgHeight))
	sb.WriteString("\n")

	// Title
	sb.WriteString(fmt.Sprintf(`<text x="%d" y="20" text-anchor="middle" font-family="monospace" font-size="14" font-weight="bold">%s</text>`,
		svgWidth/2, escapeXML(title)))
	sb.WriteString("\n")

	// Render frames bottom-up: root at bottom row (row index = totalDepth-1)
	renderFrame(&sb, root, 0, svgWidth, totalDepth, titleBarH)

	sb.WriteString("</svg>\n")
	return sb.String()
}

// renderFrame renders root and all children bottom-up (root at bottom row).
func renderFrame(sb *strings.Builder, f *frame, xOffset int, width int, totalDepth int, titleBarH int) {
	renderFrameAt(sb, f, xOffset, width, 0, totalDepth, titleBarH)
}

// renderFrameAt renders f at the given level and recurses into children.
func renderFrameAt(sb *strings.Builder, f *frame, xOffset int, width int, level int, totalDepth int, titleBarH int) {
	if width == 0 || f.samples == 0 {
		return
	}

	pct := float64(width) / float64(svgWidth) * 100.0
	if pct < minWidth {
		return
	}

	// y: bottom of SVG is level 0 (root). Higher levels stack upward.
	y := titleBarH + (totalDepth-1-level)*frameHeight

	r, g, b := flameColor(f.name)
	fillColor := fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)

	// Truncate label to fit width (approx 7px per char for 11px monospace)
	label := f.name
	maxChars := intMin(len(label), width/7)
	if maxChars < len(label) {
		if maxChars > 2 {
			label = label[:maxChars-2] + ".."
		} else {
			label = ""
		}
	}

	tooltip := fmt.Sprintf("%s (%d samples)", f.name, f.samples)

	sb.WriteString(fmt.Sprintf(`<g class="frame">
  <title>%s</title>
  <rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>
  <text x="%d" y="%d">%s</text>
</g>
`, escapeXML(tooltip), xOffset, y, width, frameHeight, fillColor,
		xOffset+3, y+frameHeight-4, escapeXML(label)))

	// Render children proportionally within this frame's width
	childX := xOffset
	for _, child := range f.children {
		childWidth := 0
		if f.samples > 0 {
			childWidth = width * child.samples / f.samples
		}
		renderFrameAt(sb, child, childX, childWidth, level+1, totalDepth, titleBarH)
		childX += childWidth
	}
}

// escapeXML escapes special XML characters.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
