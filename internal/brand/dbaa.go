//go:build dbaa && !linkdb

/*-------------------------------------------------------------------------
 *
 * dbaa.go
 *	  dbaa brand definition — used by -tags dbaa builds to swap binary
 *	  name, config dir (~/.dbaa instead of ~/.opendb), and display
 *	  strings. Loaded by brand.Init via build-tag dispatch.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/brand/dbaa.go
 *
 *-------------------------------------------------------------------------
 */
package brand

func init() {
	active = &Brand{
		AppName:             "dbaa",
		BinaryName:          "dbaa",
		WelcomeTitle:        "中国农业银行数据库智能体",
		LogoLines: [3]string{
			`█▀▀▄ █▀▀▄ ▄▀▀▄ ▄▀▀▄`,
			`█  █ █▀▀▄ █▀▀█ █▀▀█`,
			`█▄▄▀ █▄▄▀ █  █ █  █`,
		},
		DBList:        "GaussDB · openGauss",
		Tagline:       "数据库CLI Agent / 最少交互,最优诊断",
		ConfigDirName: ".dbaa",
		ConfigEnvVar:  "DBAA_HOME",
		Authorship:    "by 系统六部",
		SetupLogo: `
██████╗ ██████╗  █████╗  █████╗
██╔══██╗██╔══██╗██╔══██╗██╔══██╗
██║  ██║██████╔╝███████║███████║
██║  ██║██╔══██╗██╔══██║██╔══██║
██████╔╝██████╔╝██║  ██║██║  ██║
╚═════╝ ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝`,
		SetupTagline:        "中国农业银行数据库智能体",
		SetupDescription: []string{
			"dbaa是中国农业银行研发的交互式数据库 CLI 智能体",
		},
		NativeCLITools:      "sqlplus/mysql/psql/gsql",
	}
}
