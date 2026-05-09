/*-------------------------------------------------------------------------
 *
 * permission.go
 *	  PermissionGuide holds per-DB permission requirements
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/permission.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

// PermissionGuide holds per-DB permission requirements
type PermissionGuide struct {
	DBType         string
	Required       []PermissionItem
	NotRecommended []PermissionItem
	CreateSQL      string
}

// PermissionItem describes one permission entry
type PermissionItem struct {
	Name string
	Desc string
}

// PermissionGuideFor returns the permission guide for the given database type
func PermissionGuideFor(dbType string) PermissionGuide {
	switch dbType {
	case "oracle":
		return PermissionGuide{
			DBType: "oracle",
			Required: []PermissionItem{
				{"CREATE SESSION", "连接数据库"},
				{"SELECT on V$ views", "性能视图查询"},
				{"SELECT on DBA_ views", "数据字典查询"},
				{"SELECT_CATALOG_ROLE", "系统目录读取"},
			},
			NotRecommended: []PermissionItem{
				{"SYSDBA / SYSOPER", "权限过大"},
				{"DBA role", "包含修改权限"},
			},
			CreateSQL: "CREATE USER opendb IDENTIFIED BY <password>;\nGRANT CREATE SESSION TO opendb;\nGRANT SELECT_CATALOG_ROLE TO opendb;",
		}
	case "mysql":
		return PermissionGuide{
			DBType: "mysql",
			Required: []PermissionItem{
				{"SELECT", "查询数据和系统表"},
				{"PROCESS", "查看进程列表和线程状态"},
				{"REPLICATION CLIENT", "查看复制和 Binlog 状态"},
				{"SHOW DATABASES", "列出所有数据库"},
				{"REFERENCES", "查看外键约束"},
			},
			NotRecommended: []PermissionItem{
				{"ALL PRIVILEGES", "权限过大"},
				{"SUPER", "包含管理权限"},
			},
			CreateSQL: "CREATE USER 'opendb'@'%' IDENTIFIED BY '<password>';\n" +
				"GRANT SELECT, PROCESS, REPLICATION CLIENT,\n" +
				"      SHOW DATABASES, REFERENCES\n" +
				"      ON *.* TO 'opendb'@'%';\n" +
				"FLUSH PRIVILEGES;",
		}
	case "postgres":
		return PermissionGuide{
			DBType: "postgres",
			Required: []PermissionItem{
				{"CONNECT", "连接数据库"},
				{"pg_monitor", "性能监控视图（含 pg_stat_activity 等）"},
				{"SELECT on pg_stat*", "统计信息和系统视图查询"},
				{"pg_read_all_settings", "查看所有配置参数"},
			},
			NotRecommended: []PermissionItem{
				{"SUPERUSER", "权限过大"},
				{"pg_write_all_data", "包含写权限"},
			},
			CreateSQL: "CREATE USER opendb WITH PASSWORD '<password>';\n" +
				"GRANT CONNECT ON DATABASE <dbname> TO opendb;\n" +
				"GRANT pg_monitor TO opendb;\n" +
				"GRANT pg_read_all_settings TO opendb;",
		}
	case "opengauss", "gaussdb":
		// openGauss and GaussDB share system views (dbe_perf, gs_*) so the
		// permission requirements are identical. The DBType field is preserved
		// so downstream conntest queries route to the right driver.
		return PermissionGuide{
			DBType: dbType,
			Required: []PermissionItem{
				{"CONNECT", "连接数据库"},
				{"pg_stat_activity SELECT", "会话/等待状态查询"},
				{"dbe_perf SELECT", "扩展统计视图（dbe_perf.statement / wait_events 等）"},
				{"gs_* SELECT", "特有视图（gs_stat_activity / gs_session_stat 等）"},
				{"pg_read_all_settings", "查看所有配置参数"},
			},
			NotRecommended: []PermissionItem{
				{"SUPERUSER / SYSADMIN", "权限过大"},
				{"AUDITADMIN", "包含审计权限，非诊断必需"},
			},
			CreateSQL: "CREATE USER opendb WITH MONITOR PASSWORD '<password>';\n" +
				"GRANT CONNECT ON DATABASE <dbname> TO opendb;\n" +
				"GRANT SELECT ON ALL TABLES IN SCHEMA dbe_perf TO opendb;\n" +
				"GRANT SELECT ON pg_stat_activity, pg_stat_database, pg_locks TO opendb;",
		}
	default:
		return PermissionGuide{DBType: dbType}
	}
}

// PermissionStep shows permission guide for chosen DB type
type PermissionStep struct {
	cfg     *SetupConfig
	guide   PermissionGuide
	confirm *SelectModel
	inited  bool
}

func NewPermissionStep(cfg *SetupConfig) *PermissionStep {
	return &PermissionStep{cfg: cfg}
}

func (s *PermissionStep) initGuide() {
	s.guide = PermissionGuideFor(s.cfg.DBType)
	s.confirm = NewSelectModel("I've prepared the database account. Continue?", []SelectItem{
		{Label: "Yes", Value: "yes", Desc: "已准备好数据库账号"},
		{Label: "I need more time", Value: "exit", Desc: "退出，稍后运行 " + brand.Current().BinaryName + " --setup 继续"},
	}, "yes")
	s.inited = true
}

func (s *PermissionStep) Title() string { return "Permission Guide" }
func (s *PermissionStep) Done() bool    { return s.inited && s.confirm.Done() }

func (s *PermissionStep) Summary() string {
	return CompletedLine("Permission guide", "Reviewed")
}

func (s *PermissionStep) Init() tea.Cmd {
	s.initGuide()
	return nil
}

func (s *PermissionStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !s.inited {
		return s, nil
	}
	updated, cmd := s.confirm.Update(msg)
	s.confirm = updated
	if s.confirm.Done() && s.confirm.Selected() == "exit" {
		return s, tea.Quit
	}
	return s, cmd
}

func (s *PermissionStep) View() string {
	if !s.inited {
		return ""
	}

	var b strings.Builder

	// Build content with all three databases
	var lines []string
	appName := brand.Current().AppName
	lines = append(lines,
		"建议给 "+appName+" 分配专用只读数据库账号。",
		appName+" 遵循最小权限策略，仅需必要查询权限协助您更好地管理数据库。",
		appName+" 在日常协助管理数据库时不会修改任何信息。",
	)

	// Show permission guide for the selected database type only
	guide := s.guide
	dbLabel := DBDisplayName(guide.DBType)
	lines = append(lines, "", StyleHighlight.Render(dbLabel+":"))
	lines = append(lines, StyleTitle.Render("推荐最小权限:"))
	for _, p := range guide.Required {
		lines = append(lines, StyleSuccess.Render("✓ ")+p.Name+StyleDim.Render(" — "+p.Desc))
	}
	lines = append(lines, StyleTitle.Render("建议执行以下 SQL:"))
	lines = append(lines, StyleDim.Render(guide.CreateSQL))

	b.WriteString("\n" + InfoPanel("Permission Guide", strings.Join(lines, "\n")) + "\n")
	b.WriteString(s.confirm.View())
	return b.String()
}
