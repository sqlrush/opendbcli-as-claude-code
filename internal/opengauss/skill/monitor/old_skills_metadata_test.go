/*-------------------------------------------------------------------------
 *
 * old_skills_metadata_test.go
 *	  Test cases for old_skills_metadata.go (monitor package):
 *	  TestOldMonitorSkillsMetadata, TestOldMonitorSkillsSecurityLevel.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/old_skills_metadata_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// This file covers metadata (Name / Description / ToolDef / CLIDef /
// SecurityLevel / Validate) for the pre-existing ("old") OpenGauss monitor
// skills. It complements new_skills_test.go (which covers the skills added
// in the capability-optimization project) and trace_test.go / perfsnap_test.go
// (which have dedicated tests).
//
// These tests catch interface drift at build time — real SQL behaviour is
// validated against a live OpenGauss instance.

type oldSkillMeta struct {
	name  string
	key   string
	skill skill.Skill
}

func oldMonitorSkillCases(drv *mock.Driver) []oldSkillMeta {
	return []oldSkillMeta{
		{name: "sessions", key: "sessions", skill: NewSessionsSkill(drv)},
		{name: "activesessions", key: "activesessions", skill: NewActiveSessionsSkill(drv)},
		{name: "vacuum", key: "vacuum", skill: NewVacuumSkill(drv)},
		{name: "xid", key: "xid", skill: NewXIDSkill(drv)},
		{name: "health", key: "health", skill: NewHealthSkill(drv)},
		{name: "waits", key: "waits", skill: NewWaitsSkill(drv)},
		{name: "blocktree", key: "blocktree", skill: NewBlockTreeSkill(drv)},
		{name: "wal", key: "wal", skill: NewWALSkill(drv)},
		{name: "replication", key: "replication", skill: NewReplicationSkill(drv)},
		{name: "resource", key: "resource", skill: NewResourceSkill(drv)},
		{name: "segments", key: "segments", skill: NewSegmentsSkill(drv)},
		{name: "longtx", key: "longtx", skill: NewLongTxSkill(drv)},
		{name: "bloat", key: "bloat", skill: NewBloatSkill(drv)},
		{name: "os", key: "os", skill: NewOSSkill(drv, "localhost")},
		{name: "users", key: "users", skill: NewUsersSkill(drv)},
		{name: "slots", key: "slots", skill: NewSlotsSkill(drv)},
		// dbtop uses the NewOGDBTopSkill constructor (disambiguates from
		// other db-top skills across DB families).
		{name: "dbtop", key: "dbtop", skill: NewOGDBTopSkill(drv)},
		{name: "sqlcount", key: "sqlcount", skill: NewSQLCountSkill(drv)},
		{name: "respool", key: "respool", skill: NewResPoolSkill(drv)},
		{name: "gsmem", key: "gsmem", skill: NewGSMemSkill(drv)},
		{name: "sessionmem", key: "sessionmem", skill: NewSessionMemSkill(drv)},
		{name: "indexhealth", key: "indexhealth", skill: NewIndexHealthSkill(drv)},
	}
}

func TestOldMonitorSkillsMetadata(t *testing.T) {
	drv := mock.NewMockDriver()
	for _, c := range oldMonitorSkillCases(drv) {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := c.skill.Name(); got != c.key {
				t.Errorf("Name() = %q, want %q", got, c.key)
			}
			if c.skill.Description() == "" {
				t.Errorf("Description() is empty")
			}

			tool := c.skill.ToolDef()
			if tool.Name != c.key {
				t.Errorf("ToolDef().Name = %q, want %q", tool.Name, c.key)
			}
			if tool.Description == "" {
				t.Errorf("ToolDef().Description is empty")
			}

			cli := c.skill.CLIDef()
			if cli.Usage == "" {
				t.Errorf("CLIDef().Usage is empty")
			}

			if err := c.skill.Validate(skill.Params{}); err != nil {
				t.Errorf("Validate({}) unexpected error: %v", err)
			}
		})
	}
}

// All pre-existing monitor skills are read-only — they only SELECT from
// catalogs / system views. If any of these ever needs a higher privilege
// level, this test will flag it so the intent change is explicit.
func TestOldMonitorSkillsSecurityLevel(t *testing.T) {
	drv := mock.NewMockDriver()
	for _, c := range oldMonitorSkillCases(drv) {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := c.skill.SecurityLevel(); got != skill.LevelReadOnly {
				t.Errorf("%s SecurityLevel() = %v, want LevelReadOnly", c.key, got)
			}
		})
	}
}
