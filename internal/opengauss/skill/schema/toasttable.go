/*-------------------------------------------------------------------------
 *
 * toasttable.go
 *	  ToastTableSkill shows top tables by TOAST storage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/schema/toasttable.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// toastTableSQL ranks tables by how much of their storage is in the TOAST
// side-table, which is where large text/bytea values get chunked and
// compressed. Tables dominated by TOAST size often benefit from column
// redesign or external storage.
const toastTableSQL = `SELECT
  n.nspname || '.' || c.relname AS table_name,
  pg_size_pretty(pg_relation_size(c.oid))                  AS main_size,
  pg_size_pretty(pg_relation_size(c.reltoastrelid))        AS toast_size,
  pg_size_pretty(pg_total_relation_size(c.oid))            AS total_size,
  CASE WHEN pg_total_relation_size(c.oid) > 0
       THEN ROUND(100.0 * pg_relation_size(c.reltoastrelid)
                  / pg_total_relation_size(c.oid), 1)
       ELSE 0 END                                          AS toast_pct
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
-- Same system-schema set as internal/opengauss/skill/monitor/systemschema.go
-- (kept in sync by hand — schema/ can't import monitor/ without a cycle).
WHERE c.relkind = 'r'
  AND c.reltoastrelid != 0
  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'snapshot',
                        'dbe_perf', 'dbe_pldeveloper', 'dbe_pldebugger',
                        'db4ai', 'gs_logical_cluster', 'sqladvisor')
ORDER BY pg_relation_size(c.reltoastrelid) DESC
LIMIT 20`

// ToastTableSkill shows top tables by TOAST storage.
type ToastTableSkill struct{ driver db.Driver }

// NewToastTableSkill creates a ToastTableSkill.
func NewToastTableSkill(driver db.Driver) *ToastTableSkill {
	return &ToastTableSkill{driver: driver}
}

func (s *ToastTableSkill) Name() string                       { return "toasttable" }
func (s *ToastTableSkill) Description() string                { return "TOAST 大字段存储 Top 20" }
func (s *ToastTableSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ToastTableSkill) Validate(_ skill.Params) error      { return nil }
func (s *ToastTableSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/toasttable"} }

func (s *ToastTableSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "toasttable",
		Description: "Top tables by TOAST (out-of-line large value) storage, with TOAST share of total size",
	}
}

func (s *ToastTableSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, toastTableSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rows := 0
	if result != nil {
		rows = len(result.Rows)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("TOAST 存储 Top %d", rows),
		Summary:  fmt.Sprintf("top %d TOAST tables", rows),
	}, nil
}
