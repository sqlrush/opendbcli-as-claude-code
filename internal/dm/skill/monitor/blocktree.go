/*-------------------------------------------------------------------------
 *
 * blocktree.go
 *	  blocktree — BlockTreeSkill plus helpers (NewBlockTreeSkill) used
 *	  by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/blocktree.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// blocktreeSQL: DM 阻塞链 — V$LOCK self-join + V$SESSIONS
//
// CRITICAL（参考 OG blocktree OOM 教训）：
// - bl 是被阻塞侧 (BLOCKED=1)
// - kl 是持有侧 (BLOCKED=0)，必须显式 BLOCKED=0 过滤
// - 否则 N 个 ungranted waiter 互相 join 形成完全图导致 OOM
const blocktreeSQL = `SELECT
    bs.SESS_ID    AS BLOCKED_SESS,
    bs.USER_NAME  AS BLOCKED_USER,
    SUBSTR(bs.SQL_TEXT, 1, 60) AS BLOCKED_SQL,
    ks.SESS_ID    AS BLOCKER_SESS,
    ks.USER_NAME  AS BLOCKER_USER,
    bl.LTYPE,
    bl.TABLE_ID
FROM V$LOCK bl
JOIN V$SESSIONS bs ON bs.TRX_ID = bl.TRX_ID
JOIN V$LOCK kl ON kl.TABLE_ID = bl.TABLE_ID
              AND kl.ROW_IDX  = bl.ROW_IDX
              AND kl.TRX_ID   != bl.TRX_ID
              AND kl.BLOCKED  = 0
JOIN V$SESSIONS ks ON ks.TRX_ID = kl.TRX_ID
WHERE bl.BLOCKED = 1
ORDER BY bs.SESS_ID`

type BlockTreeSkill struct{ driver db.Driver }

func NewBlockTreeSkill(driver db.Driver) *BlockTreeSkill { return &BlockTreeSkill{driver: driver} }

func (s *BlockTreeSkill) Name() string                       { return "blocktree" }
func (s *BlockTreeSkill) Description() string                { return "锁阻塞链 (V$LOCK self-join + V$SESSIONS)" }
func (s *BlockTreeSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *BlockTreeSkill) Validate(_ skill.Params) error      { return nil }
func (s *BlockTreeSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "blocktree", Description: "Show DM block chains (waiter → holder)"}
}
func (s *BlockTreeSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "blocktree", Usage: "/blocktree"}
}

func (s *BlockTreeSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, blocktreeSQL)
	if err != nil {
		return nil, fmt.Errorf("dm blocktree: %w", err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type: skill.ResultText, Rendered: "当前无阻塞链\n[summary]\nblock_chains: 0\n",
			Summary: "no block chains",
		}, nil
	}

	// BLOCKED_SESS(0), BLOCKED_USER(1), BLOCKED_SQL(2), BLOCKER_SESS(3), BLOCKER_USER(4), LTYPE(5), TABLE_ID(6)
	blockers := dmutil.CountByCol(r.Rows, 3)
	entries := []dmutil.SummaryEntry{
		{Key: "block_chains", Val: len(r.Rows)},
		{Key: "unique_blockers", Val: len(blockers)},
	}
	if len(r.Rows) > 0 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "first_blocked_sess", Val: r.Rows[0][0]},
			dmutil.SummaryEntry{Key: "first_blocker_sess", Val: r.Rows[0][3]},
			dmutil.SummaryEntry{Key: "kill_blocker_cmd", Val: fmt.Sprintf("CALL SP_CLOSE_SESSION(%v)", r.Rows[0][3])},
		)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("阻塞链 — %d 条", len(r.Rows)),
	}, nil
}
