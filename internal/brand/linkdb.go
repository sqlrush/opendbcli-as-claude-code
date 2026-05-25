//go:build linkdb

/*-------------------------------------------------------------------------
 *
 * linkdb.go
 *	  linkdb brand definition — used by -tags linkdb builds for the
 *	  仁合时创 OEM variant. Same engine and skill set as opendb; only
 *	  binary name, welcome page, setup wizard text and config dir
 *	  differ. Loaded by brand.Init via build-tag dispatch.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/brand/linkdb.go
 *
 *-------------------------------------------------------------------------
 */
package brand

func init() {
	active = &Brand{
		AppName:      "linkdb",
		BinaryName:   "linkdb",
		WelcomeTitle: "仁合时创数据库智能体",
		LogoLines: [3]string{
			`█     █  █▄ █ █ ▄▀ █▀▀▄ █▀▀▄`,
			`█     █  █ ▀█ █▀▄  █  █ █▀▀▄`,
			`▀▀▀   ▀  ▀  ▀ ▀  ▀ ▀▀▀  ▀▀▀ `,
		},
		DBList:  "GaussDB · openGauss · Oracle · MySQL · PostgreSQL · 达梦",
		Tagline: "数据库CLI Agent / 最少交互,最优诊断",
		SetupLogo: `
██╗     ██╗███╗   ██╗██╗  ██╗██████╗ ██████╗
██║     ██║████╗  ██║██║ ██╔╝██╔══██╗██╔══██╗
██║     ██║██╔██╗ ██║█████╔╝ ██║  ██║██████╔╝
██║     ██║██║╚██╗██║██╔═██╗ ██║  ██║██╔══██╗
███████╗██║██║ ╚████║██║  ██╗██████╔╝██████╔╝
╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝╚═════╝ ╚═════╝`,
		SetupTagline: "仁合时创数据库智能体",
		SetupDescription: []string{
			"linkdb是仁合时创研发的交互式数据库 CLI 智能体",
		},
		NativeCLITools: "sqlplus/mysql/psql/gsql/disql",
		ConfigDirName:  ".linkdb",
		ConfigEnvVar:   "LINKDB_HOME",
		Authorship:     "by 仁合时创",
	}
}
