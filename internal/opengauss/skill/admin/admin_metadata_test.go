/*-------------------------------------------------------------------------
 *
 * admin_metadata_test.go
 *	  Test cases for admin_metadata.go (admin package):
 *	  TestAdminSkillsMetadata, TestAdminSkillsSecurityLevel,
 *	  TestAdminSkillsValidateEmpty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/admin/admin_metadata_test.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"testing"

	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// This file covers metadata (Name/Description/SecurityLevel/CLIDef/ToolDef)
// for all OpenGauss admin skills. Real SQL behaviour is validated against a
// live OG instance — these tests catch registration and interface drift at
// build time.

type adminMetaCase struct {
	name          string
	skill         skill.Skill
	skillKey      string
	securityLevel skill.SecurityLevel
	// validateEmptyWantErr indicates whether Validate(skill.Params{}) should
	// return a non-nil error. /alter requires args, others accept empty.
	validateEmptyWantErr bool
}

func adminMetaCases() []adminMetaCase {
	drv := mock.NewMockDriver()
	return []adminMetaCase{
		{
			name:          "alert",
			skill:         NewAlertSkill(drv),
			skillKey:      "alert",
			securityLevel: skill.LevelReadOnly,
		},
		{
			name:                 "alter",
			skill:                NewAlterSkill(drv),
			skillKey:             "alter",
			securityLevel:        skill.LevelOperator,
			validateEmptyWantErr: true,
		},
		{
			name:          "backup",
			skill:         NewBackupSkill(drv),
			skillKey:      "backup",
			securityLevel: skill.LevelReadOnly,
		},
		{
			name:          "gather",
			skill:         NewGatherSkill(drv),
			skillKey:      "gather",
			securityLevel: skill.LevelOperator,
		},
		{
			name:          "jobs",
			skill:         NewJobsSkill(drv),
			skillKey:      "jobs",
			securityLevel: skill.LevelReadOnly,
		},
		{
			name:          "kill",
			skill:         NewKillSkill(drv),
			skillKey:      "kill",
			securityLevel: skill.LevelOperator,
		},
		{
			name:          "params",
			skill:         NewParamsSkill(drv),
			skillKey:      "params",
			securityLevel: skill.LevelReadOnly,
		},
		{
			name:          "space",
			skill:         NewSpaceSkill(drv),
			skillKey:      "space",
			securityLevel: skill.LevelReadOnly,
		},
	}
}

func TestAdminSkillsMetadata(t *testing.T) {
	for _, c := range adminMetaCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := c.skill.Name(); got != c.skillKey {
				t.Errorf("Name() = %q, want %q", got, c.skillKey)
			}
			if c.skill.Description() == "" {
				t.Errorf("Description() is empty")
			}
			if got := c.skill.ToolDef().Name; got != c.skillKey {
				t.Errorf("ToolDef().Name = %q, want %q", got, c.skillKey)
			}
			if c.skill.ToolDef().Description == "" {
				t.Errorf("ToolDef().Description is empty")
			}
			if c.skill.CLIDef().Usage == "" {
				t.Errorf("CLIDef().Usage is empty")
			}
		})
	}
}

func TestAdminSkillsSecurityLevel(t *testing.T) {
	for _, c := range adminMetaCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := c.skill.SecurityLevel(); got != c.securityLevel {
				t.Errorf("%s SecurityLevel() = %v, want %v",
					c.skillKey, got, c.securityLevel)
			}
		})
	}
}

func TestAdminSkillsValidateEmpty(t *testing.T) {
	for _, c := range adminMetaCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := c.skill.Validate(skill.Params{})
			if c.validateEmptyWantErr && err == nil {
				t.Errorf("%s Validate({}) = nil, want error", c.skillKey)
			}
			if !c.validateEmptyWantErr && err != nil {
				t.Errorf("%s Validate({}) unexpected error: %v",
					c.skillKey, err)
			}
		})
	}
}
