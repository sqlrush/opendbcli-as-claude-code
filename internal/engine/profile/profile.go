/*-------------------------------------------------------------------------
 *
 * profile.go
 *	  PromptProfile provides DB-specific configuration to the Engine.
 *	  Engine doesn't know Oracle from MySQL — it gets everything
 *	  through this interface.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/profile.go
 *
 *-------------------------------------------------------------------------
 */
package profile

// PromptProfile provides DB-specific configuration to the Engine.
// Engine doesn't know Oracle from MySQL — it gets everything through this interface.
type PromptProfile interface {
	// Product returns the database type identifier.
	Product() string // "oracle" / "mysql" / "postgres" / "opengauss"

	// SystemPromptRules returns DB-specific rules for the system prompt.
	SystemPromptRules() string

	// ToolUsageHint returns a usage scenario description for a named tool.
	ToolUsageHint(skillName string) string

	// ToolFilter returns a filter function for the given diagnosis mode.
	// Tools for which the filter returns false are excluded.
	ToolFilter(mode string) func(name string, securityLevel int) bool

	// DefaultMaxTurns returns the default maximum turns for the given mode.
	DefaultMaxTurns(mode string) int
}

// NewProfile creates the appropriate PromptProfile for a database product.
func NewProfile(product string) PromptProfile {
	switch product {
	case "oracle":
		return &OracleProfile{}
	case "mysql":
		return &MySQLProfile{}
	case "postgres":
		return &PostgresProfile{}
	case "opengauss":
		return &OpenGaussProfile{}
	case "gaussdb":
		return &GaussDBProfile{}
	case "dm":
		return &DMProfile{}
	default:
		return &GenericProfile{product: product}
	}
}

// GenericProfile is a fallback for unknown database types.
type GenericProfile struct {
	product string
}

func (p *GenericProfile) Product() string { return p.product }

func (p *GenericProfile) SystemPromptRules() string {
	return "# 通用数据库规则\n- 引用对象名前先查询确认存在\n- 禁止编造数据"
}

func (p *GenericProfile) ToolUsageHint(name string) string { return "" }

func (p *GenericProfile) ToolFilter(mode string) func(string, int) bool {
	return func(name string, level int) bool {
		switch mode {
		case "playbook":
			return false
		case "assist":
			return level <= 0
		default:
			return true
		}
	}
}

func (p *GenericProfile) DefaultMaxTurns(mode string) int {
	switch mode {
	case "playbook":
		return 1
	case "assist":
		return 10
	default:
		return 20
	}
}
