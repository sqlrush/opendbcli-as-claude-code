/*-------------------------------------------------------------------------
 *
 * brand.go
 *	  Package brand centralizes all white-label / OEM-customizable
 *	  strings so the codebase can produce branded builds (OpenDB, dbaa
 *	  for 中国农业银行, future bank/customer variants) from a
 *	  single source tree.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/brand/brand.go
 *
 *-------------------------------------------------------------------------
 */
// Package brand centralizes all white-label / OEM-customizable strings so
// the codebase can produce branded builds (OpenDB, dbaa for 中国农业银行,
// future bank/customer variants) from a single source tree.
//
// Build-tag selects which Brand instance is active:
//   - default (no tag)   → OpenDB brand from default.go
//   - -tags dbaa         → dbaa brand from dbaa.go
//
// All UI/display code MUST read from brand.Current() — never hardcode the
// app name, binary name, logo, etc. See docs/PROJECT-DBAA-BRAND-LAYER.md
// for the full list of branded strings and the rationale.
package brand

// Brand carries every user-visible string that differs between branded
// builds. Adding a new field here is a one-time cost paid by both default.go
// and any custom Brand variant (dbaa.go etc).
type Brand struct {
	// AppName: shown in welcome page header and version banner.
	// e.g. "OpenDB" or "dbaa".
	AppName string

	// BinaryName: name of the compiled binary, used in user-facing
	// command suggestions ("Run `opendb` to start").
	// e.g. "opendb" or "dbaa".
	BinaryName string

	// WelcomeTitle: large greeting on the welcome page.
	// e.g. "欢迎回来!" or "中国农业银行数据库智能体".
	WelcomeTitle string

	// LogoLines: 3-line ASCII art shown center of REPL welcome page (compact).
	// e.g. "OPENDB" half-block art or "DBAA".
	LogoLines [3]string

	// SetupLogo: large ASCII art block shown on the setup wizard intro page
	// (multi-line, can use ╗/║/╝ box-drawing). Set "" to skip rendering.
	SetupLogo string

	// SetupTagline: short line under SetupLogo on setup wizard.
	// e.g. "DB CLI Agent as Claude Code".
	SetupTagline string

	// SetupDescription: 1-2 sentences shown in the Welcome panel of setup
	// wizard, describing what this product is. Each slice element is one
	// rendered line.
	SetupDescription []string

	// NativeCLITools: list of native DB CLI tools the product integrates,
	// used in welcome content "整合 X 功能".
	// e.g. "sqlplus/mysql/psql" (default) or "sqlplus/mysql/psql/gsql" (dbaa).
	NativeCLITools string

	// DBList: tagline under the logo listing supported databases.
	// e.g. "Oracle · MySQL · PostgreSQL" or "GaussDB · Oracle · MySQL · PostgreSQL".
	DBList string

	// Tagline: short description shown above DBList.
	// e.g. "数据库CLI Agent / 最少交互,最优诊断".
	Tagline string

	// ConfigDirName: hidden directory under $HOME for config storage.
	// e.g. ".opendb" or ".dbaa".
	ConfigDirName string

	// ConfigEnvVar: env var that overrides the default config dir.
	// e.g. "OPENDB_HOME" or "DBAA_HOME".
	ConfigEnvVar string

	// Authorship: appears after the "Apache 2.0" license tag on the setup
	// wizard footer. Format: "by <author/team>" optionally followed by
	// " | <contact>". Lets each branded build credit its own maintainer
	// (Sqlrush for opendb, 系统六部 for dbaa, 仁合时创 for linkdb, ...).
	Authorship string
}

// active is the compiled-in Brand. Set by default.go or dbaa.go via build tags.
var active *Brand

// Current returns the active Brand for this build.
func Current() *Brand {
	if active == nil {
		// Defensive fallback (should never happen — build tags guarantee
		// one of default.go / dbaa.go assigns active in init).
		return &Brand{
			AppName: "OpenDB", BinaryName: "opendb",
			WelcomeTitle: "欢迎回来!",
			ConfigDirName: ".opendb", ConfigEnvVar: "OPENDB_HOME",
		}
	}
	return active
}
