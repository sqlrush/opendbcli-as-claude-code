/*-------------------------------------------------------------------------
 *
 * level.go
 *	  level — Holds the Level used inside security.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/level.go
 *
 *-------------------------------------------------------------------------
 */
package security

type Level uint8

const (
	LevelReadOnly  Level = 0
	LevelOperator  Level = 1
	LevelAdmin     Level = 2
	LevelDangerous Level = 3
)

func (l Level) String() string {
	switch l {
	case LevelReadOnly:
		return "readonly"
	case LevelOperator:
		return "operator"
	case LevelAdmin:
		return "admin"
	case LevelDangerous:
		return "dangerous"
	default:
		return "unknown"
	}
}

func (l Level) RequiresConfirmation() bool {
	return l >= LevelAdmin
}

func (l Level) CanDisableConfirmation() bool {
	return l < LevelDangerous
}
