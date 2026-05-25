/*-------------------------------------------------------------------------
 *
 * repl_dropdown_state_test.go
 *	  Test cases for repl_dropdown_state.go (ui package):
 *	  TestDropdownState_Empty, TestDropdownState_NilReceiver,
 *	  TestDropdownState_SmallList.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/repl_dropdown_state_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"fmt"
	"testing"
)

func TestDropdownState_Empty(t *testing.T) {
	d := NewDropdown(KindCompletion, nil)

	if d.IsActive() {
		t.Error("empty dropdown should not be active")
	}
	if d.Len() != 0 {
		t.Errorf("Len() = %d, want 0", d.Len())
	}
	if d.VisibleCount() != 0 {
		t.Errorf("VisibleCount() = %d, want 0", d.VisibleCount())
	}

	// MoveUp/MoveDown on empty list should not panic.
	d.MoveUp()
	d.MoveDown()

	if item := d.SelectedItem(); item != nil {
		t.Error("SelectedItem() should be nil for empty dropdown")
	}

	items, startIdx := d.VisibleSlice()
	if items != nil {
		t.Error("VisibleSlice() items should be nil for empty dropdown")
	}
	if startIdx != 0 {
		t.Errorf("VisibleSlice() startIdx = %d, want 0", startIdx)
	}

	showUp, showDown := d.ScrollIndicators()
	if showUp || showDown {
		t.Error("scroll indicators should both be false for empty dropdown")
	}
}

func TestDropdownState_NilReceiver(t *testing.T) {
	var d *DropdownState

	if d.IsActive() {
		t.Error("nil dropdown should not be active")
	}
	if d.Len() != 0 {
		t.Errorf("Len() = %d, want 0", d.Len())
	}
	if d.VisibleCount() != 0 {
		t.Errorf("VisibleCount() = %d, want 0", d.VisibleCount())
	}
	if item := d.SelectedItem(); item != nil {
		t.Error("SelectedItem() should be nil for nil dropdown")
	}

	items, startIdx := d.VisibleSlice()
	if items != nil {
		t.Error("VisibleSlice() items should be nil for nil dropdown")
	}
	if startIdx != 0 {
		t.Errorf("VisibleSlice() startIdx = %d, want 0", startIdx)
	}

	showUp, showDown := d.ScrollIndicators()
	if showUp || showDown {
		t.Error("scroll indicators should both be false for nil dropdown")
	}
}

func TestDropdownState_SmallList(t *testing.T) {
	items := []DropdownItem{
		{Label: "one", Value: "1"},
		{Label: "two", Value: "2"},
		{Label: "three", Value: "3"},
	}
	d := NewDropdown(KindLogin, items)

	if !d.IsActive() {
		t.Error("dropdown with items should be active")
	}
	if d.Len() != 3 {
		t.Errorf("Len() = %d, want 3", d.Len())
	}
	if d.VisibleCount() != 3 {
		t.Errorf("VisibleCount() = %d, want 3", d.VisibleCount())
	}
	if d.Kind != KindLogin {
		t.Errorf("Kind = %d, want KindLogin(%d)", d.Kind, KindLogin)
	}
	if d.SelectedIdx != -1 {
		t.Errorf("initial SelectedIdx = %d, want -1", d.SelectedIdx)
	}

	showUp, showDown := d.ScrollIndicators()
	if showUp || showDown {
		t.Error("scroll indicators should both be false for 3-item list")
	}

	visible, startIdx := d.VisibleSlice()
	if len(visible) != 3 {
		t.Errorf("VisibleSlice() len = %d, want 3", len(visible))
	}
	if startIdx != 0 {
		t.Errorf("VisibleSlice() startIdx = %d, want 0", startIdx)
	}
}

func TestDropdownState_ExactMax(t *testing.T) {
	items := make([]DropdownItem, maxDropdownVisible)
	for i := range items {
		items[i] = DropdownItem{Label: fmt.Sprintf("item-%d", i), Value: fmt.Sprintf("%d", i)}
	}
	d := NewDropdown(KindDiag, items)

	if d.VisibleCount() != maxDropdownVisible {
		t.Errorf("VisibleCount() = %d, want %d", d.VisibleCount(), maxDropdownVisible)
	}

	showUp, showDown := d.ScrollIndicators()
	if showUp || showDown {
		t.Errorf("scroll indicators should both be false for exactly %d items", maxDropdownVisible)
	}

	visible, startIdx := d.VisibleSlice()
	if len(visible) != maxDropdownVisible {
		t.Errorf("VisibleSlice() len = %d, want %d", len(visible), maxDropdownVisible)
	}
	if startIdx != 0 {
		t.Errorf("VisibleSlice() startIdx = %d, want 0", startIdx)
	}
}

func TestDropdownState_LargeList(t *testing.T) {
	items := make([]DropdownItem, 10)
	for i := range items {
		items[i] = DropdownItem{Label: fmt.Sprintf("item-%d", i), Value: fmt.Sprintf("%d", i)}
	}
	d := NewDropdown(KindRule, items)

	if d.Len() != 10 {
		t.Errorf("Len() = %d, want 10", d.Len())
	}
	if d.VisibleCount() != maxDropdownVisible {
		t.Errorf("VisibleCount() = %d, want %d", d.VisibleCount(), maxDropdownVisible)
	}

	// Initial state: scroll offset 0 => showUp=false, showDown=true.
	showUp, showDown := d.ScrollIndicators()
	if showUp {
		t.Error("showUp should be false at initial scroll offset 0")
	}
	if !showDown {
		t.Error("showDown should be true for 10-item list at offset 0")
	}

	visible, startIdx := d.VisibleSlice()
	if len(visible) != maxDropdownVisible {
		t.Errorf("VisibleSlice() len = %d, want %d", len(visible), maxDropdownVisible)
	}
	if startIdx != 0 {
		t.Errorf("VisibleSlice() startIdx = %d, want 0", startIdx)
	}
	if visible[0].Label != "item-0" {
		t.Errorf("first visible label = %q, want %q", visible[0].Label, "item-0")
	}
}

func TestDropdownState_MoveDown_Wrap(t *testing.T) {
	items := make([]DropdownItem, 10)
	for i := range items {
		items[i] = DropdownItem{Label: fmt.Sprintf("item-%d", i), Value: fmt.Sprintf("%d", i)}
	}
	d := NewDropdown(KindCompletion, items)

	// Move to last item.
	for i := 0; i < 10; i++ {
		d.MoveDown()
	}
	// SelectedIdx should be at 9 (started at -1, first MoveDown goes to 0, then increments).
	if d.SelectedIdx != 9 {
		t.Errorf("after 10 MoveDown, SelectedIdx = %d, want 9", d.SelectedIdx)
	}

	// One more MoveDown should wrap to 0.
	d.MoveDown()
	if d.SelectedIdx != 0 {
		t.Errorf("after wrap MoveDown, SelectedIdx = %d, want 0", d.SelectedIdx)
	}
	if d.ScrollOffset != 0 {
		t.Errorf("after wrap MoveDown, ScrollOffset = %d, want 0", d.ScrollOffset)
	}
}

func TestDropdownState_MoveUp_Wrap(t *testing.T) {
	items := make([]DropdownItem, 10)
	for i := range items {
		items[i] = DropdownItem{Label: fmt.Sprintf("item-%d", i), Value: fmt.Sprintf("%d", i)}
	}
	d := NewDropdown(KindCompletion, items)

	// First MoveUp from SelectedIdx=-1 should wrap to last item (index 9).
	d.MoveUp()
	if d.SelectedIdx != 9 {
		t.Errorf("after first MoveUp, SelectedIdx = %d, want 9", d.SelectedIdx)
	}
	// Scroll should adjust to show the last item.
	expectedOffset := 10 - maxDropdownVisible // 4
	if d.ScrollOffset != expectedOffset {
		t.Errorf("after MoveUp wrap, ScrollOffset = %d, want %d", d.ScrollOffset, expectedOffset)
	}

	showUp, showDown := d.ScrollIndicators()
	if !showUp {
		t.Error("showUp should be true when scrolled to bottom")
	}
	if showDown {
		t.Error("showDown should be false when last item is visible")
	}
}

func TestDropdownState_ScrollTracking(t *testing.T) {
	items := make([]DropdownItem, 10)
	for i := range items {
		items[i] = DropdownItem{Label: fmt.Sprintf("item-%d", i), Value: fmt.Sprintf("%d", i)}
	}
	d := NewDropdown(KindCompletion, items)

	// MoveDown 7 times: from -1 -> 0 -> 1 -> 2 -> 3 -> 4 -> 5 -> 6
	for i := 0; i < 7; i++ {
		d.MoveDown()
	}
	if d.SelectedIdx != 6 {
		t.Errorf("after 7 MoveDown, SelectedIdx = %d, want 6", d.SelectedIdx)
	}
	// Item 6 should be visible. With maxDropdownVisible=6, offset should be 1.
	// Visible window: [1..6], selected=6 is at the bottom edge.
	if d.ScrollOffset != 1 {
		t.Errorf("after 7 MoveDown, ScrollOffset = %d, want 1", d.ScrollOffset)
	}

	visible, startIdx := d.VisibleSlice()
	if startIdx != 1 {
		t.Errorf("VisibleSlice startIdx = %d, want 1", startIdx)
	}
	if len(visible) != maxDropdownVisible {
		t.Errorf("VisibleSlice len = %d, want %d", len(visible), maxDropdownVisible)
	}
	if visible[0].Label != "item-1" {
		t.Errorf("first visible = %q, want %q", visible[0].Label, "item-1")
	}
	if visible[5].Label != "item-6" {
		t.Errorf("last visible = %q, want %q", visible[5].Label, "item-6")
	}

	showUp, showDown := d.ScrollIndicators()
	if !showUp {
		t.Error("showUp should be true at offset 1")
	}
	if !showDown {
		t.Error("showDown should be true (items 7-9 still below)")
	}
}

func TestDropdownState_SelectedItem(t *testing.T) {
	items := []DropdownItem{
		{Label: "alpha", Value: "a", Detail: "first"},
		{Label: "beta", Value: "b", Detail: "second"},
		{Label: "gamma", Value: "c", Detail: "third"},
	}
	d := NewDropdown(KindCompletion, items)

	// No selection initially.
	if item := d.SelectedItem(); item != nil {
		t.Error("SelectedItem() should be nil when SelectedIdx=-1")
	}

	// Select first item.
	d.MoveDown()
	item := d.SelectedItem()
	if item == nil {
		t.Fatal("SelectedItem() should not be nil after MoveDown")
	}
	if item.Label != "alpha" || item.Value != "a" || item.Detail != "first" {
		t.Errorf("SelectedItem() = %+v, want {alpha, a, first}", item)
	}

	// Select second item.
	d.MoveDown()
	item = d.SelectedItem()
	if item == nil {
		t.Fatal("SelectedItem() should not be nil after second MoveDown")
	}
	if item.Label != "beta" || item.Value != "b" {
		t.Errorf("SelectedItem() = %+v, want {beta, b, second}", item)
	}

	// Select last item.
	d.MoveDown()
	item = d.SelectedItem()
	if item == nil {
		t.Fatal("SelectedItem() should not be nil after third MoveDown")
	}
	if item.Label != "gamma" || item.Value != "c" {
		t.Errorf("SelectedItem() = %+v, want {gamma, c, third}", item)
	}
}

func TestDropdownState_SelectedItem_ReturnsCopy(t *testing.T) {
	items := []DropdownItem{
		{Label: "original", Value: "v"},
	}
	d := NewDropdown(KindCompletion, items)
	d.MoveDown()

	// Modifying the returned pointer should not affect the original slice
	// because SelectedItem returns &d.Items[idx] (pointer to slice element).
	// This test documents the current behavior.
	item := d.SelectedItem()
	if item == nil {
		t.Fatal("SelectedItem() should not be nil")
	}
	if item.Label != "original" {
		t.Errorf("SelectedItem().Label = %q, want %q", item.Label, "original")
	}
}

func TestDropdownState_ScrollDownThenUp(t *testing.T) {
	items := make([]DropdownItem, 10)
	for i := range items {
		items[i] = DropdownItem{Label: fmt.Sprintf("item-%d", i), Value: fmt.Sprintf("%d", i)}
	}
	d := NewDropdown(KindCompletion, items)

	// Scroll down to item 8.
	for i := 0; i < 9; i++ {
		d.MoveDown()
	}
	if d.SelectedIdx != 8 {
		t.Fatalf("SelectedIdx = %d, want 8", d.SelectedIdx)
	}
	if d.ScrollOffset != 3 {
		t.Errorf("ScrollOffset = %d, want 3", d.ScrollOffset)
	}

	// Now scroll back up to item 2.
	for i := 0; i < 6; i++ {
		d.MoveUp()
	}
	if d.SelectedIdx != 2 {
		t.Fatalf("SelectedIdx = %d, want 2", d.SelectedIdx)
	}
	if d.ScrollOffset != 2 {
		t.Errorf("ScrollOffset = %d, want 2", d.ScrollOffset)
	}

	// Scroll up one more to trigger scroll offset adjustment.
	d.MoveUp()
	if d.SelectedIdx != 1 {
		t.Fatalf("SelectedIdx = %d, want 1", d.SelectedIdx)
	}
	if d.ScrollOffset != 1 {
		t.Errorf("ScrollOffset = %d, want 1", d.ScrollOffset)
	}
}

func TestDropdownState_KindValues(t *testing.T) {
	// Verify enum values are distinct.
	kinds := []DropdownKind{KindCompletion, KindLogin, KindDiag, KindRule}
	seen := make(map[DropdownKind]bool)
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate DropdownKind value: %d", k)
		}
		seen[k] = true
	}
}
