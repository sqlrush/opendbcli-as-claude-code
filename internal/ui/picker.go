/*-------------------------------------------------------------------------
 *
 * picker.go
 *	  PickerItem represents one item in an inline picker.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/picker.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"strings"
)

// PickerItem represents one item in an inline picker.
type PickerItem struct {
	Label string // display text
	Value string // underlying value returned on selection
}

// PickerResult is the outcome of RunPicker.
type PickerResult struct {
	Selected *PickerItem // nil if cancelled (ESC)
	Index    int         // index of selected item (-1 if cancelled)
}

// RunPicker shows an inline interactive picker at the current output position.
// It takes over keyboard input until Enter (select) or ESC (cancel).
//
// Parameters:
//   - items: picker items to display
//   - maxVisible: max items visible at once (rest is scrollable)
//   - header: optional header line above items (empty = no header)
//   - initialIdx: initial selection index
func (r *REPL) RunPicker(items []PickerItem, maxVisible int, header string, initialIdx int) PickerResult {
	if len(items) == 0 {
		return PickerResult{Index: -1}
	}
	if maxVisible <= 0 || maxVisible > 15 {
		maxVisible = 15
	}
	if maxVisible > len(items) {
		maxVisible = len(items)
	}

	// Write header if provided.
	// 4-char indent aligns with item content ("  ▸ " selected / "    " unselected).
	if header != "" {
		r.writeOutputLine(dimStyle.Render("    " + header))
		r.writeOutputLine(dimStyle.Render("  " + strings.Repeat("─", 60)))
	}

	// Write item placeholders.
	for i := 0; i < maxVisible; i++ {
		r.writeOutputLine("") // placeholder
	}
	// Calculate actual row positions AFTER all placeholders are written.
	// This avoids row tracking errors when scroll mode activates mid-write.
	itemRows := make([]int, maxVisible)
	var baseRow int
	if r.scrollMode {
		baseRow = r.maxContentRow() - maxVisible + 1
	} else {
		baseRow = r.contentRow - maxVisible
	}
	for i := 0; i < maxVisible; i++ {
		itemRows[i] = baseRow + i
	}
	r.drawInputArea()

	sel := initialIdx
	if sel < 0 || sel >= len(items) {
		sel = 0
	}
	scrollOff := 0

	// Ensure selected item is visible.
	if sel >= scrollOff+maxVisible {
		scrollOff = sel - maxVisible + 1
	}

	termHideCursor(r.writer)

	redraw := func() {
		for vi, row := range itemRows {
			if row < 1 {
				continue
			}
			idx := scrollOff + vi
			if idx >= len(items) {
				termClearRow(r.writer, row)
				continue
			}
			it := items[idx]
			termClearRow(r.writer, row)
			if idx == sel {
				fmt.Fprintf(r.writer, "  %s", accentStyle.Render("▸ "+it.Label))
			} else {
				fmt.Fprintf(r.writer, "    %s", it.Label)
			}
		}
		// Scroll indicators.
		if len(items) > maxVisible {
			if scrollOff > 0 {
				termMoveTo(r.writer, itemRows[0], r.cols-1)
				fmt.Fprint(r.writer, dimStyle.Render("▲"))
			}
			if scrollOff+maxVisible < len(items) {
				termMoveTo(r.writer, itemRows[maxVisible-1], r.cols-1)
				fmt.Fprint(r.writer, dimStyle.Render("▼"))
			}
		}
		r.restoreCursor()
	}
	redraw()

	// Event loop.
	for {
		ke := <-r.keyCh
		if ke.err != nil {
			break
		}
		buf := ke.buf
		n := ke.n
		switch {
		case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A': // Up
			if sel > 0 {
				sel--
			} else {
				sel = len(items) - 1
			}
			adjustScroll(&scrollOff, sel, maxVisible, len(items))
			redraw()
		case n >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B': // Down
			if sel < len(items)-1 {
				sel++
			} else {
				sel = 0
			}
			adjustScroll(&scrollOff, sel, maxVisible, len(items))
			redraw()
		case n >= 1 && (buf[0] == 13 || buf[0] == 10): // Enter
			termShowCursor(r.writer)
			return PickerResult{Selected: &items[sel], Index: sel}
		case n == 1 && buf[0] == 27: // ESC
			termShowCursor(r.writer)
			return PickerResult{Index: -1}
		}
	}
	termShowCursor(r.writer)
	return PickerResult{Index: -1}
}

// currentOutputRow returns the row where the next writeOutputLine will go.
func (r *REPL) currentOutputRow() int {
	if r.scrollMode {
		return r.maxContentRow()
	}
	return r.contentRow
}

// adjustScroll keeps the selected item within the visible window.
func adjustScroll(scrollOff *int, sel, maxVisible, total int) {
	if sel < *scrollOff {
		*scrollOff = sel
	}
	if sel >= *scrollOff+maxVisible {
		*scrollOff = sel - maxVisible + 1
	}
	// Wrap-around: if sel is at the end but scrollOff is at start.
	if *scrollOff < 0 {
		*scrollOff = 0
	}
	max := total - maxVisible
	if max < 0 {
		max = 0
	}
	if *scrollOff > max {
		*scrollOff = max
	}
}

// pickerFromStrings is a convenience to build []PickerItem from label=value pairs.
func pickerFromStrings(labels []string) []PickerItem {
	items := make([]PickerItem, len(labels))
	for i, l := range labels {
		items[i] = PickerItem{Label: l, Value: l}
	}
	return items
}

// pickerFromLabelValue builds []PickerItem from separate label/value slices.
func pickerFromLabelValue(labels, values []string) []PickerItem {
	n := len(labels)
	if len(values) < n {
		n = len(values)
	}
	items := make([]PickerItem, n)
	for i := 0; i < n; i++ {
		items[i] = PickerItem{Label: labels[i], Value: values[i]}
	}
	return items
}

