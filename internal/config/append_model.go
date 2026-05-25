/*-------------------------------------------------------------------------
 *
 * append_model.go
 *	  AppendModel appends a single ModelConfig entry to the `models:`
 *	  block of config.yaml at path, preserving comments and the rest of
 *	  the file. Used by /model add wizard so newly added models land in
 *	  the same file users edit for everything else (instead of a
 *	  separate models_dir/<name>.yaml).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/config/append_model.go
 *
 *-------------------------------------------------------------------------
 */
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppendModel appends a single ModelConfig entry to the `models:` block of
// config.yaml at path, preserving comments and the rest of the file. Used by
// /model add wizard so newly added models land in the same file users edit
// for everything else (instead of a separate models_dir/<name>.yaml).
//
// Behavior:
//   - If the file has no `models:` section, one is appended at the end.
//   - Otherwise the new entry is inserted at the end of the existing block
//     (just before the next top-level key, or at EOF).
//   - Duplicate names are NOT detected here — caller should validate first.
func AppendModel(path string, m ModelConfig) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	text := string(raw)

	entryBytes, err := yaml.Marshal([]ModelConfig{m})
	if err != nil {
		return fmt.Errorf("marshal model: %w", err)
	}
	entry := strings.TrimRight(string(entryBytes), "\n")

	updated := insertModelEntry(text, entry)
	if err := os.WriteFile(path, []byte(updated), 0o640); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}

// insertModelEntry returns config text with `entry` inserted into the
// `models:` block (creating the block if missing).
func insertModelEntry(text, entry string) string {
	lines := strings.Split(text, "\n")

	// Locate `models:` line at top level (not indented).
	startIdx := -1
	for i, l := range lines {
		if l == "models:" || strings.HasPrefix(l, "models: ") {
			startIdx = i
			break
		}
	}

	if startIdx < 0 {
		// No models block — append at end.
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + "models:\n" + entry + "\n"
	}

	// Find end of models block: first line after startIdx that isn't blank,
	// indented, or a list-item continuation.
	endIdx := len(lines)
	for i := startIdx + 1; i < len(lines); i++ {
		l := lines[i]
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, "\t") {
			continue
		}
		endIdx = i
		break
	}

	// Trim trailing blank lines inside the models block so the new entry
	// sits flush with the previous one.
	insertAt := endIdx
	for insertAt > startIdx+1 && lines[insertAt-1] == "" {
		insertAt--
	}

	entryLines := strings.Split(entry, "\n")
	newLines := make([]string, 0, len(lines)+len(entryLines))
	newLines = append(newLines, lines[:insertAt]...)
	newLines = append(newLines, entryLines...)
	newLines = append(newLines, lines[insertAt:]...)
	return strings.Join(newLines, "\n")
}
