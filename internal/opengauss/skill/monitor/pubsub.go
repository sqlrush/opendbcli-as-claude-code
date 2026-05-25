/*-------------------------------------------------------------------------
 *
 * pubsub.go
 *	  PubSubSkill lists logical replication publications and
 *	  subscriptions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/pubsub.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// pubSQL enumerates publications.
const pubSQL = `SELECT pubname, puballtables, pubinsert, pubupdate, pubdelete
FROM pg_publication
ORDER BY pubname`

// subSQL enumerates subscriptions + simple runtime stats.
const subSQL = `SELECT
  subname,
  subenabled,
  subconninfo,
  array_length(subpublications, 1) AS pub_count
FROM pg_subscription
ORDER BY subname`

// PubSubSkill lists logical replication publications and subscriptions.
type PubSubSkill struct{ driver db.Driver }

// NewPubSubSkill creates a PubSubSkill.
func NewPubSubSkill(driver db.Driver) *PubSubSkill { return &PubSubSkill{driver: driver} }

func (s *PubSubSkill) Name() string                       { return "pubsub" }
func (s *PubSubSkill) Description() string                { return "发布/订阅 (逻辑复制)" }
func (s *PubSubSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *PubSubSkill) Validate(_ skill.Params) error      { return nil }
func (s *PubSubSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/pubsub"} }

func (s *PubSubSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "pubsub",
		Description: "List logical replication publications and subscriptions",
	}
}

func (s *PubSubSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	pubs, _ := s.driver.Query(ctx, pubSQL)
	subs, _ := s.driver.Query(ctx, subSQL)

	pubRows := 0
	if pubs != nil {
		pubRows = len(pubs.Rows)
	}
	subRows := 0
	if subs != nil {
		subRows = len(subs.Rows)
	}

	if pubRows+subRows == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无发布/订阅 (未启用逻辑复制)",
			Summary:  "no pub/sub",
		}, nil
	}

	// Prefer showing subscriptions since downstream issues are more common.
	if subRows > 0 {
		return &skill.Result{
			Type:     skill.ResultTable,
			Data:     subs,
			Rendered: fmt.Sprintf("订阅 %d 个 / 发布 %d 个", subRows, pubRows),
			Summary:  fmt.Sprintf("%d subs, %d pubs", subRows, pubRows),
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     pubs,
		Rendered: fmt.Sprintf("发布 %d 个 / 订阅 0", pubRows),
		Summary:  fmt.Sprintf("%d pubs", pubRows),
	}, nil
}
