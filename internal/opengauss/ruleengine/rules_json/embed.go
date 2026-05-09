/*-------------------------------------------------------------------------
 *
 * embed.go
 *	  Embedded JSON rule data for openGauss — go:embed bundles the
 *	  rule definitions at build time so the binary stays single-file
 *	  and the rules ship versioned with the code.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/rules_json/embed.go
 *
 *-------------------------------------------------------------------------
 */
package rules_json

import "embed"

// RuleFiles embeds all PG/OpenGauss JSON rule files.
// These are loaded at startup by JSONRuleProvider.
//
//go:embed PG*.json
var RuleFiles embed.FS
