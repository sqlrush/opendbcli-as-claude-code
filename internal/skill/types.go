/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Shared type definitions for the skill package: SecurityLevel.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/types.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import "github.com/sqlrush/opendb/internal/security"

type SecurityLevel = security.Level

const (
	LevelReadOnly  = security.LevelReadOnly
	LevelOperator  = security.LevelOperator
	LevelAdmin     = security.LevelAdmin
	LevelDangerous = security.LevelDangerous
)
