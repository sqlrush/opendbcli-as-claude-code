/*-------------------------------------------------------------------------
 *
 * repl_dropdown.go
 *	  DropdownKind identifies the type of dropdown being shown.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_dropdown.go
 *
 *-------------------------------------------------------------------------
 */
package ui

// DropdownKind identifies the type of dropdown being shown.
type DropdownKind int

const (
	KindCompletion DropdownKind = iota // existing / command completion
	KindLogin                          // /login connection list
	KindDiag                           // /diag exception list
	KindRule                           // /rule exception list
)

const maxDropdownVisible = 6

// DropdownItem represents one row in a dropdown list.
type DropdownItem struct {
	Label  string // display text (may contain ANSI)
	Value  string // underlying value (connection name, alert index, etc.)
	Detail string // optional secondary info
}

// DropdownState manages a scrollable dropdown list.
type DropdownState struct {
	Kind         DropdownKind
	Items        []DropdownItem
	SelectedIdx  int // -1 = no selection
	ScrollOffset int // first visible item index
}

// NewDropdown creates a dropdown with the given items.
func NewDropdown(kind DropdownKind, items []DropdownItem) *DropdownState {
	return &DropdownState{
		Kind:        kind,
		Items:       items,
		SelectedIdx: -1,
	}
}

// IsActive returns true if the dropdown has items to show.
func (d *DropdownState) IsActive() bool {
	return d != nil && len(d.Items) > 0
}

// Len returns the total number of items.
func (d *DropdownState) Len() int {
	if d == nil {
		return 0
	}
	return len(d.Items)
}

// VisibleCount returns how many rows to display (capped at maxDropdownVisible).
func (d *DropdownState) VisibleCount() int {
	if d == nil {
		return 0
	}
	n := len(d.Items)
	if n > maxDropdownVisible {
		return maxDropdownVisible
	}
	return n
}

// MoveUp moves selection up, scrolling if needed. Wraps around.
func (d *DropdownState) MoveUp() {
	if len(d.Items) == 0 {
		return
	}
	if d.SelectedIdx <= 0 {
		d.SelectedIdx = len(d.Items) - 1
	} else {
		d.SelectedIdx--
	}
	d.adjustScroll()
}

// MoveDown moves selection down, scrolling if needed. Wraps around.
func (d *DropdownState) MoveDown() {
	if len(d.Items) == 0 {
		return
	}
	if d.SelectedIdx >= len(d.Items)-1 {
		d.SelectedIdx = 0
	} else {
		d.SelectedIdx++
	}
	d.adjustScroll()
}

// SelectedItem returns the currently selected item, or nil if no selection.
func (d *DropdownState) SelectedItem() *DropdownItem {
	if d == nil || d.SelectedIdx < 0 || d.SelectedIdx >= len(d.Items) {
		return nil
	}
	return &d.Items[d.SelectedIdx]
}

// VisibleSlice returns the slice of items currently visible, and the start index.
func (d *DropdownState) VisibleSlice() (items []DropdownItem, startIdx int) {
	if d == nil || len(d.Items) == 0 {
		return nil, 0
	}
	end := d.ScrollOffset + maxDropdownVisible
	if end > len(d.Items) {
		end = len(d.Items)
	}
	return d.Items[d.ScrollOffset:end], d.ScrollOffset
}

// ScrollIndicators returns whether up/down scroll indicators should be shown.
func (d *DropdownState) ScrollIndicators() (showUp, showDown bool) {
	if d == nil || len(d.Items) <= maxDropdownVisible {
		return false, false
	}
	showUp = d.ScrollOffset > 0
	showDown = d.ScrollOffset+maxDropdownVisible < len(d.Items)
	return
}

// adjustScroll ensures the selected item is visible.
func (d *DropdownState) adjustScroll() {
	if d.SelectedIdx < d.ScrollOffset {
		d.ScrollOffset = d.SelectedIdx
	} else if d.SelectedIdx >= d.ScrollOffset+maxDropdownVisible {
		d.ScrollOffset = d.SelectedIdx - maxDropdownVisible + 1
	}
}
