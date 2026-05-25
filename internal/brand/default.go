//go:build !dbaa && !linkdb

/*-------------------------------------------------------------------------
 *
 * default.go
 *	  Default opendb brand — built when no brand tag is set, identifies
 *	  the binary as `opendb` and points the config dir at ~/.opendb.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/brand/default.go
 *
 *-------------------------------------------------------------------------
 */
package brand

func init() {
	active = &Brand{
		AppName:             "OpenDB",
		BinaryName:          "opendb",
		WelcomeTitle:        "欢迎回来!",
		LogoLines: [3]string{
			`▄▀▀▄ █▀▀▄ █▀▀ █▄ █ █▀▄ █▀▄`,
			`█  █ █▀▀  █▀▀ █ ▀█ █ █ █▀▄`,
			` ▀▀  ▀    ▀▀▀ ▀  ▀ ▀▀  ▀▀ `,
		},
		DBList:              "Oracle · MySQL · PostgreSQL · openGauss · GaussDB",
		Tagline:             "数据库CLI Agent / 最少交互,最优诊断",
		SetupLogo: `
 ██████╗ ██████╗ ███████╗███╗   ██╗██████╗ ██████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔══██╗██╔══██╗
██║   ██║██████╔╝█████╗  ██╔██╗ ██║██║  ██║██████╔╝
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║██║  ██║██╔══██╗
╚██████╔╝██║     ███████╗██║ ╚████║██████╔╝██████╔╝
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚═════╝ ╚═════╝`,
		SetupTagline:        "DB CLI Agent as Claude Code",
		SetupDescription: []string{
			"OpenDB 是参考 Claude Code 交互方式实现的数据库 CLI 智能体，",
			"用最简洁的交互，实现最优的管理和诊断。",
		},
		NativeCLITools: "sqlplus/mysql/psql/gsql",
		ConfigDirName:  ".opendb",
		ConfigEnvVar:   "OPENDB_HOME",
		Authorship:     "by SQLRush  ·  sqlrush@gmail.com",
	}
}
