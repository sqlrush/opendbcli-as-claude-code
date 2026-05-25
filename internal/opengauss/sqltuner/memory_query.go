/*-------------------------------------------------------------------------
 *
 * memory_query.go
 *	  MemoryQuery pulls relevant historical tuning entries for involved
 *	  tables.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/memory_query.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/engine/memory"
)

// MemoryQuery pulls relevant historical tuning entries for involved tables.
//
// memory.Store has no Find(Query) API yet — we use LoadIndex (markdown listing of all
// memory entries) and do client-side filtering by table-name keyword match.
//
// This is the MVP version. P5 阶段计划扩展 memory.Store API for structured tag/time filter.
type MemoryQuery struct {
	store *memory.Store
}

func NewMemoryQuery(store *memory.Store) *MemoryQuery {
	return &MemoryQuery{store: store}
}

// FindRelevant returns up to maxN memory entries relevant to the current SQL.
//
// v1.1.30: when sql is non-empty, switches to fingerprint-based recall —
// only memory entries whose stored fingerprint scores >= memory.SimilarityThreshold
// against the current SQL are returned. This prevents the "5-table SQL diagnosis
// recalled for 10-table SQL" cross-pollution observed in v1.1.29.
//
// Returns empty slice (not nil) if no store / no SQL / no matches above threshold.
func (m *MemoryQuery) FindRelevant(sql string, involvedTables []string, maxN int) []MemoryEntry {
	if m.store == nil {
		return []MemoryEntry{}
	}

	entries := m.store.Find(memory.Query{
		SQL:      sql,
		Tables:   involvedTables,
		Keywords: []string{"sql_tune", "index", "schema_change", "tuning"},
		MaxAge:   90 * 24 * time.Hour,
		Limit:    maxN,
	})

	results := make([]MemoryEntry, 0, len(entries))
	for _, e := range entries {
		results = append(results, MemoryEntry{
			Title:      e.Title,
			Type:       e.Type,
			Content:    summarize(e.Content, 500),
			CreatedAt:  e.CreatedAt,
			Similarity: e.Similarity,
		})
	}
	return results
}

func summarize(content string, maxLen int) string {
	content = strings.TrimSpace(content)
	if len(content) > maxLen {
		content = content[:maxLen] + "..."
	}
	return content
}

// PromptBlock formats memory entries for the LLM user message.
//
// v1.1.30: each entry's similarity score is rendered prominently. The LLM
// must understand recalled memories may be from a related-but-different SQL
// and must not blindly copy their conclusions.
func PromptMemoryBlock(entries []MemoryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("相关历史调优经验（**仅供参考，不是当前 SQL 的真实诊断**）:\n")
	b.WriteString("⚠️ 这些是 fingerprint 相似度过 85% 阈值的历史记录。**当前 SQL 结构可能不同**，\n")
	b.WriteString("   你必须基于当前 EXPLAIN + Schema 出诊断，不能直接复用以下方案。\n")
	for i, e := range entries {
		b.WriteString("\n[历史 #")
		b.WriteString(intToStr(i + 1))
		b.WriteString("]")
		if e.Similarity > 0 {
			b.WriteString(" 相似度 ")
			b.WriteString(formatPct(e.Similarity))
		}
		b.WriteString(" — " + e.Title + "\n")
		b.WriteString(e.Content + "\n")
	}
	return b.String()
}

// formatPct renders 0.85 as "85%".
func formatPct(f float64) string {
	pct := int(f*100 + 0.5)
	return intToStr(pct) + "%"
}
