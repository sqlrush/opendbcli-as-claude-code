/*-------------------------------------------------------------------------
 *
 * types.go
 *	  MemoryType categorizes a memory entry for DBA scenarios.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/types.go
 *
 *-------------------------------------------------------------------------
 */
package memory

// MemoryType categorizes a memory entry for DBA scenarios.
type MemoryType string

const (
	MemIncident   MemoryType = "incident"   // 过往发生的问题
	MemSolution   MemoryType = "solution"   // 问题的解决方案及效果
	MemPreference MemoryType = "preference" // 管理人员的偏好
	MemWorkload   MemoryType = "workload"   // 业务负载特征
	MemPattern    MemoryType = "pattern"    // 反复出现的问题模式
)

// ValidMemoryTypes lists all recognized memory types.
var ValidMemoryTypes = []MemoryType{
	MemIncident, MemSolution, MemPreference, MemWorkload, MemPattern,
}

// IsValid returns true if the type is recognized.
func (t MemoryType) IsValid() bool {
	for _, v := range ValidMemoryTypes {
		if t == v {
			return true
		}
	}
	return false
}

// Label returns a Chinese display label for the type.
func (t MemoryType) Label() string {
	switch t {
	case MemIncident:
		return "过往问题 (incident)"
	case MemSolution:
		return "解决方案 (solution)"
	case MemPreference:
		return "管理偏好 (preference)"
	case MemWorkload:
		return "负载特征 (workload)"
	case MemPattern:
		return "问题模式 (pattern)"
	default:
		return string(t)
	}
}

// Limits aligned with Claude Code.
const (
	MaxIndexLines            = 200   // MEMORY.md max lines
	MaxIndexBytes            = 25000 // MEMORY.md max bytes
	MaxFiles                 = 200   // max memory files per instance (excluding MEMORY.md, PROFILE.md)
	MaxFrontmatterLines      = 30    // lines to read for frontmatter parsing
	MaxIndexEntriesPerType   = 10    // MEMORY.md: per-type cap to keep prompt lean
)

// Well-known file names.
const (
	IndexFile   = "MEMORY.md"
	ProfileFile = "PROFILE.md"
)
