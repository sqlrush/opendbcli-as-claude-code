/*-------------------------------------------------------------------------
 *
 * new_skills_test.go
 *	  Test cases for new_skills.go (monitor package):
 *	  TestNewMonitorSkillsMetadata, TestNewMonitorSkillsSecurityLevel.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/new_skills_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// This file covers metadata (Name/Description/SecurityLevel/CLIDef/ToolDef)
// for the skills added in the OpenGauss capability-optimization project.
// Real SQL behaviour is validated against a live OG instance — these tests
// catch registration and interface drift at build time.

type metaCase struct {
	name     string
	skill    skill.Skill
	skillKey string
}

func TestNewMonitorSkillsMetadata(t *testing.T) {
	drv := mock.NewMockDriver()

	// Note: /trace takes (connHost, traceCfg) not (driver) and has its own
	// dedicated test file (trace_test.go) covering metadata + remote-host
	// refusal. Keep it out of this table-driven loop to avoid dual coverage.
	cases := []metaCase{
		{name: "lwlocks", skill: NewLWLocksSkill(drv), skillKey: "lwlocks"},
		{name: "autovacuum", skill: NewAutoVacuumSkill(drv), skillKey: "autovacuum"},
		{name: "checkpoint", skill: NewCheckpointSkill(drv), skillKey: "checkpoint"},
		{name: "bgworker", skill: NewBgWorkerSkill(drv), skillKey: "bgworker"},
		{name: "hotkey", skill: NewHotKeySkill(drv), skillKey: "hotkey"},
		{name: "logicalslots", skill: NewLogicalSlotsSkill(drv), skillKey: "logicalslots"},
		{name: "tempusage", skill: NewTempUsageSkill(drv), skillKey: "tempusage"},
		{name: "mot", skill: NewMOTSkill(drv), skillKey: "mot"},
		{name: "cmha", skill: NewCMHASkill(drv), skillKey: "cmha"},
		{name: "pubsub", skill: NewPubSubSkill(drv), skillKey: "pubsub"},
		{name: "walsummary", skill: NewWALSummarySkill(drv), skillKey: "walsummary"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skill.Name() != c.skillKey {
				t.Errorf("Name() = %q, want %q", c.skill.Name(), c.skillKey)
			}
			if c.skill.Description() == "" {
				t.Errorf("Description() is empty")
			}
			if c.skill.ToolDef().Name != c.skillKey {
				t.Errorf("ToolDef().Name = %q, want %q", c.skill.ToolDef().Name, c.skillKey)
			}
			if c.skill.ToolDef().Description == "" {
				t.Errorf("ToolDef().Description is empty")
			}
			if c.skill.CLIDef().Usage == "" {
				t.Errorf("CLIDef().Usage is empty")
			}
			if err := c.skill.Validate(skill.Params{}); err != nil {
				t.Errorf("Validate({}) unexpected error: %v", err)
			}
		})
	}
}

// SecurityLevel expectations: all new monitor skills are read-only. /trace
// is also read-only (perf only samples, doesn't modify DB state) and is
// covered in trace_test.go.
func TestNewMonitorSkillsSecurityLevel(t *testing.T) {
	drv := mock.NewMockDriver()
	readOnlySkills := []skill.Skill{
		NewLWLocksSkill(drv),
		NewAutoVacuumSkill(drv),
		NewCheckpointSkill(drv),
		NewBgWorkerSkill(drv),
		NewHotKeySkill(drv),
		NewLogicalSlotsSkill(drv),
		NewTempUsageSkill(drv),
		NewMOTSkill(drv),
		NewCMHASkill(drv),
		NewPubSubSkill(drv),
		NewWALSummarySkill(drv),
	}
	for _, s := range readOnlySkills {
		if s.SecurityLevel() != skill.LevelReadOnly {
			t.Errorf("%s SecurityLevel() = %v, want LevelReadOnly", s.Name(), s.SecurityLevel())
		}
	}
}
