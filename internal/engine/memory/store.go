/*-------------------------------------------------------------------------
 *
 * store.go
 *	  Store manages memory files for database instances. Directory
 *	  layout: {baseDir}/{instance}/{type}_{title}.md
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/store.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Store manages memory files for database instances.
// Directory layout: {baseDir}/{instance}/{type}_{title}.md
type Store struct {
	baseDir        string // e.g. ~/.opendb/memory/
	activeInstance string
}

// NewStore creates a memory store.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// SetActiveInstance sets the current database instance name.
func (s *Store) SetActiveInstance(name string) {
	s.activeInstance = name
}

// ActiveInstance returns the current instance name.
func (s *Store) ActiveInstance() string {
	return s.activeInstance
}

// instanceDir returns the memory directory for the active instance.
func (s *Store) instanceDir() string {
	return filepath.Join(s.baseDir, s.activeInstance)
}

// WriteWithSQL is Write plus an SQL fingerprint computed from sourceSQL and
// stored in the frontmatter. Used by /sqltune and other SQL-context features
// so the entry can be recalled selectively (only for queries with similar
// fingerprint), preventing cross-SQL pollution.
//
// If sourceSQL is "", behaves identically to Write.
func (s *Store) WriteWithSQL(memType MemoryType, title, content, sourceSQL string) (string, error) {
	if sourceSQL == "" {
		return s.Write(memType, title, content)
	}
	if s.activeInstance == "" {
		return "", fmt.Errorf("no active instance")
	}
	if !memType.IsValid() {
		return "", fmt.Errorf("invalid memory type: %s", memType)
	}

	dir := s.instanceDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%s_%s.md", memType, sanitizeFilename(title))
	fp := ComputeFingerprint(sourceSQL)

	// Build frontmatter with fingerprint fields. Ensures recall by SimilarityScore.
	body := fmt.Sprintf(
		"---\nname: %s\ndescription: %s\ntype: %s\nsql_fingerprint: %s\nsql_tables: [%s]\nsql_has_cte: %t\nsql_depth: %d\n---\n\n%s\n",
		title,
		truncateStr(title, 100),
		memType,
		fp.Hash,
		strings.Join(fp.Tables, ", "),
		fp.HasCTE,
		fp.Depth,
		content,
	)

	path := filepath.Join(dir, filename)
	if err := atomicWrite(path, []byte(body)); err != nil {
		return "", err
	}

	age := "刚刚"
	entry := fmt.Sprintf("- [%s](%s) — %s (%s)", title, filename, truncateStr(title, 80), age)
	if err := AppendToIndex(dir, entry); err != nil {
		_ = err
	}
	s.enforceFileLimit(dir)
	return filename, nil
}

// Write creates a new memory file and updates the index.
// Returns the generated filename.
func (s *Store) Write(memType MemoryType, title, content string) (string, error) {
	if s.activeInstance == "" {
		return "", fmt.Errorf("no active instance")
	}
	if !memType.IsValid() {
		return "", fmt.Errorf("invalid memory type: %s", memType)
	}

	dir := s.instanceDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%s_%s.md", memType, sanitizeFilename(title))

	// Build frontmatter + content
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n",
		title, truncateStr(title, 100), memType, content)

	path := filepath.Join(dir, filename)
	if err := atomicWrite(path, []byte(body)); err != nil {
		return "", err
	}

	// Update index
	age := "刚刚"
	entry := fmt.Sprintf("- [%s](%s) — %s (%s)", title, filename, truncateStr(title, 80), age)
	if err := AppendToIndex(dir, entry); err != nil {
		// Non-fatal: memory file was saved, just index update failed
		_ = err
	}

	// Enforce file limit
	s.enforceFileLimit(dir)

	return filename, nil
}

// Read returns the full content of a memory file.
func (s *Store) Read(filename string) (string, error) {
	if s.activeInstance == "" {
		return "", fmt.Errorf("no active instance")
	}
	path := filepath.Join(s.instanceDir(), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Update overwrites an existing memory file and rebuilds the index.
func (s *Store) Update(filename, title, content string) error {
	if s.activeInstance == "" {
		return fmt.Errorf("no active instance")
	}

	dir := s.instanceDir()
	path := filepath.Join(dir, filename)

	// Read existing to preserve type from frontmatter
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	memType := extractTypeFromFrontmatter(string(existing))

	// Build updated content
	if title == "" {
		title = extractNameFromFrontmatter(string(existing))
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\n---\n\n%s\n",
		title, truncateStr(title, 100), memType, content)

	if err := atomicWrite(path, []byte(body)); err != nil {
		return err
	}

	// Rebuild index after update
	return RebuildIndex(dir)
}

// LoadIndex returns the MEMORY.md content, truncated to limits.
func (s *Store) LoadIndex() (string, error) {
	if s.activeInstance == "" {
		return "", nil
	}
	path := filepath.Join(s.instanceDir(), IndexFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return TruncateEntrypoint(string(data), MaxIndexLines, MaxIndexBytes), nil
}

// LoadProfile returns the full PROFILE.md content.
func (s *Store) LoadProfile() (string, error) {
	if s.activeInstance == "" {
		return "", nil
	}
	path := filepath.Join(s.instanceDir(), ProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Query filters memory entries by structured criteria. Used by /sqltune and
// other features that need to find historically-relevant memories without
// scanning index text. Empty fields = no filter (matches all).
type Query struct {
	Tables   []string      // any token mention of these table names matches (case-insensitive)
	Tags     []string      // not yet enforced — frontmatter tag system reserved for future
	Keywords []string      // any keyword present in title or content (case-insensitive)
	SQL      string        // (v1.1.30) when set, computes fingerprint and recalls by similarity
	MaxAge   time.Duration // entries older than this are excluded (0 = no limit)
	Limit    int           // cap result count (0 = no limit, default 20)
}

// Entry is a single memory record returned by Find.
type Entry struct {
	Filename    string
	Title       string
	Type        string
	Content     string      // full markdown body (frontmatter stripped)
	CreatedAt   time.Time   // file mtime as proxy
	Fingerprint Fingerprint // (v1.1.30) parsed from frontmatter; Empty() if none
	Similarity  float64     // (v1.1.30) computed at recall time when Query.SQL is set; 0 otherwise
}

// Find returns memory entries matching Query, sorted by CreatedAt desc.
// Reads files directly (does not rely on MEMORY.md index) so newly-written
// entries are immediately findable. Best-effort: errors on individual files
// are skipped silently.
//
// v1.1.30: when Query.SQL is set, switches to fingerprint-based recall:
//
//	(1) compute fingerprint of the query SQL
//	(2) for each candidate memory, parse stored fingerprint from frontmatter
//	    (legacy entries without fingerprint are scored by 0.5 * Jaccard on
//	    inferred tables — they aren't blocked but get lower priority)
//	(3) keep entries with similarity >= SimilarityThreshold (0.85)
//	(4) attach similarity score to Entry.Similarity for the prompt builder
//	    to render "matched at X%" labels.
//
// This is the fix for the "memory cross-SQL pollution" bug observed in
// v1.1.29 where Opus reused a 5-table SQL diagnosis when answering a
// 10-table SQL question.
func (s *Store) Find(q Query) []Entry {
	if s.activeInstance == "" {
		return nil
	}
	limit := q.Limit
	if limit == 0 {
		limit = 20
	}
	dir := s.instanceDir()
	files := listMemoryFiles(dir)

	// Compute query fingerprint once if SQL provided
	var queryFP Fingerprint
	if q.SQL != "" {
		queryFP = ComputeFingerprint(q.SQL)
	}

	now := time.Now()
	var results []Entry
	for _, f := range files {
		if q.MaxAge > 0 && now.Sub(f.mtime) > q.MaxAge {
			continue
		}
		raw, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		content := string(raw)

		// Apply filters (case-insensitive substring match against full text)
		if len(q.Tables) > 0 || len(q.Keywords) > 0 {
			lower := strings.ToLower(content)
			matched := false
			for _, t := range q.Tables {
				if strings.Contains(lower, strings.ToLower(t)) {
					matched = true
					break
				}
			}
			if !matched {
				for _, k := range q.Keywords {
					if strings.Contains(lower, strings.ToLower(k)) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
		}

		title := extractNameFromFrontmatter(content)
		memType := extractTypeFromFrontmatter(content)
		body := stripFrontmatter(content)
		entryFP := extractFingerprintFromFrontmatter(content)

		// Fingerprint similarity gate (v1.1.30)
		similarity := 0.0
		if !queryFP.Empty() {
			if entryFP.Empty() {
				// Legacy memory without fingerprint: keep with low similarity
				// so it can still surface but is deprioritized vs. fingerprinted.
				// We use 0.5 (below threshold) so it gets dropped unless the
				// caller explicitly disables fingerprint filtering.
				similarity = 0.5
			} else {
				similarity = queryFP.SimilarityScore(entryFP)
			}
			if similarity < SimilarityThreshold {
				continue // drop sub-threshold to prevent cross-SQL pollution
			}
		}

		results = append(results, Entry{
			Filename:    filepath.Base(f.path),
			Title:       title,
			Type:        memType,
			Content:     body,
			CreatedAt:   f.mtime,
			Fingerprint: entryFP,
			Similarity:  similarity,
		})
		if len(results) >= limit {
			break
		}
	}

	// Sort newest first
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	return results
}

// stripFrontmatter removes a leading YAML frontmatter block (--- ... ---) from content.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	rest := content[3:]
	end := strings.Index(rest, "---")
	if end < 0 {
		return content
	}
	return strings.TrimLeft(rest[end+3:], "\n")
}

// enforceFileLimit deletes oldest memory files if count exceeds MaxFiles.
func (s *Store) enforceFileLimit(dir string) {
	files := listMemoryFiles(dir)
	if len(files) <= MaxFiles {
		return
	}

	// Sort by mtime ascending (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.Before(files[j].mtime)
	})

	// Delete oldest until within limit
	for len(files) > MaxFiles {
		os.Remove(files[0].path)
		files = files[1:]
	}

	// Rebuild index after cleanup
	_ = RebuildIndex(dir)
}

type memFile struct {
	path  string
	mtime time.Time
}

// listMemoryFiles returns all .md files except MEMORY.md and PROFILE.md.
func listMemoryFiles(dir string) []memFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []memFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		if name == IndexFile || name == ProfileFile {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, memFile{
			path:  filepath.Join(dir, name),
			mtime: info.ModTime(),
		})
	}
	return files
}

// atomicWrite writes data to a file using temp+rename for atomicity.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sanitizeFilename converts a title to a safe filename component.
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9\p{Han}\p{Hiragana}\p{Katakana}_-]`)

func sanitizeFilename(title string) string {
	// Replace spaces and unsafe chars with underscores
	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return '_'
		}
		return r
	}, title)
	s = unsafeChars.ReplaceAllString(s, "")
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		s = "unnamed"
	}
	return strings.ToLower(s)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// extractTypeFromFrontmatter extracts the type field from frontmatter.
func extractTypeFromFrontmatter(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "type: ") {
			return strings.TrimPrefix(line, "type: ")
		}
	}
	return "incident"
}

// extractNameFromFrontmatter extracts the name field from frontmatter.
func extractNameFromFrontmatter(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "name: ") {
			return strings.TrimPrefix(line, "name: ")
		}
	}
	return ""
}

// extractFingerprintFromFrontmatter parses the SQL fingerprint fields written
// by WriteWithSQL. Returns Empty fingerprint if any required field is missing
// (legacy entries written by Write or by external tools).
func extractFingerprintFromFrontmatter(content string) Fingerprint {
	fp := Fingerprint{}
	// Only scan the frontmatter region (between the two "---" lines)
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return fp
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		switch {
		case strings.HasPrefix(line, "sql_fingerprint: "):
			fp.Hash = strings.TrimSpace(strings.TrimPrefix(line, "sql_fingerprint: "))
		case strings.HasPrefix(line, "sql_tables: "):
			val := strings.TrimSpace(strings.TrimPrefix(line, "sql_tables: "))
			val = strings.TrimPrefix(val, "[")
			val = strings.TrimSuffix(val, "]")
			if val != "" {
				parts := strings.Split(val, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						fp.Tables = append(fp.Tables, p)
					}
				}
			}
		case strings.HasPrefix(line, "sql_has_cte: "):
			fp.HasCTE = strings.TrimSpace(strings.TrimPrefix(line, "sql_has_cte: ")) == "true"
		case strings.HasPrefix(line, "sql_depth: "):
			d := strings.TrimSpace(strings.TrimPrefix(line, "sql_depth: "))
			fp.Depth = parseIntOrZero(d)
		}
	}
	return fp
}

// parseIntOrZero is a tiny helper so we don't pull in strconv here just for one call.
func parseIntOrZero(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
