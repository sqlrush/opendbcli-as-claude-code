/*-------------------------------------------------------------------------
 *
 * gaussdb.go
 *	  GaussDBProfile reuses openGauss diagnostic knowledge while preserving
 *	  the GaussDB product identity in prompts and telemetry.
 *
 *-------------------------------------------------------------------------
 */
package profile

import "strings"

// GaussDBProfile is intentionally behavior-compatible with OpenGaussProfile.
// GaussDB and openGauss share the same diagnostic views/SQL dialect here; the
// product identity differs for display and prompt wording.
type GaussDBProfile struct {
	OpenGaussProfile
}

func (p *GaussDBProfile) Product() string { return "gaussdb" }

func (p *GaussDBProfile) SystemPromptRules() string {
	rules := p.OpenGaussProfile.SystemPromptRules()
	rules = strings.Replace(rules, "# OpenGauss 数据库特定知识", "# GaussDB 数据库特定知识", 1)
	rules = strings.ReplaceAll(rules, "OpenGauss", "GaussDB/openGauss")
	rules = strings.ReplaceAll(rules, "openGauss", "GaussDB/openGauss")
	return rules
}
