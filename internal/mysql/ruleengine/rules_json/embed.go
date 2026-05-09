/*-------------------------------------------------------------------------
 *
 * embed.go
 *	  Embedded JSON rule data for MySQL — go:embed bundles the
 *	  rule definitions at build time so the binary stays single-file
 *	  and the rules ship versioned with the code.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/rules_json/embed.go
 *
 *-------------------------------------------------------------------------
 */
package rules_json

import "embed"

// RuleFiles embeds all MySQL JSON rule files from ailinkdb.
// These are loaded at startup by JSONRuleProvider.
//
//go:embed MY_*.json
var RuleFiles embed.FS
