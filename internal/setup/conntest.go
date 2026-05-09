/*-------------------------------------------------------------------------
 *
 * conntest.go
 *	  PermCheckQuery represents one permission check
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/conntest.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/brand"
)

// PermCheckQuery represents one permission check
type PermCheckQuery struct {
	Name string
	SQL  string
}

// PermissionCheckQueries returns queries to test minimum required permissions
func PermissionCheckQueries(dbType string) []PermCheckQuery {
	switch dbType {
	case "oracle":
		return []PermCheckQuery{
			{"CREATE SESSION", "SELECT 1 FROM DUAL"},
			{"V$SESSION access", "SELECT COUNT(*) FROM V$SESSION WHERE ROWNUM=1"},
			{"V$SQL access", "SELECT COUNT(*) FROM V$SQL WHERE ROWNUM=1"},
			{"DBA_HIST views", "SELECT COUNT(*) FROM DBA_HIST_SNAPSHOT WHERE ROWNUM=1"},
		}
	case "mysql":
		return []PermCheckQuery{
			{"Connect", "SELECT 1"},
			{"PROCESS", "SELECT COUNT(*) FROM information_schema.PROCESSLIST"},
			{"Performance Schema", "SELECT COUNT(*) FROM performance_schema.threads LIMIT 1"},
		}
	case "postgres":
		return []PermCheckQuery{
			{"Connect", "SELECT 1"},
			{"pg_stat_activity", "SELECT COUNT(*) FROM pg_stat_activity"},
			{"pg_stat_database", "SELECT COUNT(*) FROM pg_stat_database"},
			{"pg_locks", "SELECT COUNT(*) FROM pg_locks"},
		}
	case "opengauss", "gaussdb":
		return []PermCheckQuery{
			{"Connect", "SELECT 1"},
			{"pg_stat_activity", "SELECT COUNT(*) FROM pg_stat_activity"},
			{"pg_stat_database", "SELECT COUNT(*) FROM pg_stat_database"},
			{"pg_locks", "SELECT COUNT(*) FROM pg_locks"},
			{"dbe_perf access", "SELECT COUNT(*) FROM dbe_perf.statement"},
			{"gs_stat_activity", "SELECT COUNT(*) FROM gs_stat_activity"},
		}
	default:
		return nil
	}
}

// OverprivilegeCheckQueries returns queries to detect excessive permissions
func OverprivilegeCheckQueries(dbType string) []PermCheckQuery {
	switch dbType {
	case "oracle":
		return []PermCheckQuery{
			{"DBA role", "SELECT COUNT(*) FROM USER_ROLE_PRIVS WHERE GRANTED_ROLE='DBA'"},
			{"SYSDBA privilege", "SELECT COUNT(*) FROM V$PWFILE_USERS WHERE USERNAME=USER AND SYSDBA='TRUE'"},
		}
	case "mysql":
		return []PermCheckQuery{
			{"SUPER privilege", "SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=CONCAT('''',USER(),'''') AND PRIVILEGE_TYPE='SUPER'"},
		}
	case "postgres":
		return []PermCheckQuery{
			{"SUPERUSER", "SELECT CASE WHEN usesuper THEN 1 ELSE 0 END FROM pg_user WHERE usename=current_user"},
		}
	default:
		return nil
	}
}

// ConnTestResult holds one test result
type ConnTestResult struct {
	Name    string
	OK      bool
	Warning bool
	Detail  string
}

type connTestDoneMsg struct {
	dbVersion string
	results   []ConnTestResult
	err       error
}

// connTestPhase tracks where we are in the test flow
type connTestPhase int

const (
	connTestRunning connTestPhase = iota
	connTestShowResult
	connTestChoosing
)

// ConnTestStep runs connection and permission tests with user choice
type ConnTestStep struct {
	cfg        *SetupConfig
	driverFunc DriverFactory
	results    []ConnTestResult
	dbVersion  string
	phase      connTestPhase
	choice     *SelectModel // user choice after results
	done       bool
	skipped    bool
	err        error
}

func NewConnTestStep(cfg *SetupConfig, driverFunc DriverFactory) *ConnTestStep {
	return &ConnTestStep{
		cfg:        cfg,
		driverFunc: driverFunc,
	}
}

func (s *ConnTestStep) Title() string { return "Connection Test" }
func (s *ConnTestStep) Done() bool    { return s.done }

func (s *ConnTestStep) Summary() string {
	if s.skipped {
		return CompletedLine("Connection test", "Skipped")
	}
	if s.err != nil {
		return CompletedLine("Connection test", "Failed — "+s.err.Error())
	}
	passed := 0
	for _, r := range s.results {
		if r.OK && !r.Warning {
			passed++
		}
	}
	total := len(PermissionCheckQueries(s.cfg.DBType))
	return CompletedLine("Connection test",
		fmt.Sprintf("✓ %s · %d/%d permissions OK", s.dbVersion, passed, total))
}

func (s *ConnTestStep) Init() tea.Cmd {
	if s.driverFunc == nil {
		s.skipped = true
		s.done = true
		return emitDone
	}
	s.phase = connTestRunning
	return s.runTests()
}

func (s *ConnTestStep) runTests() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		drv, err := s.driverFunc(s.cfg)
		if err != nil {
			return connTestDoneMsg{err: fmt.Errorf("连接失败: %w", err)}
		}
		defer drv.Close()

		info := drv.ServerInfo()
		var results []ConnTestResult

		queries := filterPermissionsForVersion(
			PermissionCheckQueries(s.cfg.DBType), s.cfg.DBType, info.Version)
		for _, q := range queries {
			_, qErr := drv.Query(ctx, q.SQL)
			results = append(results, ConnTestResult{
				Name:   q.Name,
				OK:     qErr == nil,
				Detail: errStr(qErr),
			})
		}

		for _, q := range OverprivilegeCheckQueries(s.cfg.DBType) {
			qr, qErr := drv.Query(ctx, q.SQL)
			if qErr == nil && len(qr.Rows) > 0 {
				if val, ok := qr.Rows[0][0].(int64); ok && val > 0 {
					results = append(results, ConnTestResult{
						Name:    q.Name,
						OK:      true,
						Warning: true,
						Detail:  "权限偏大，建议收窄",
					})
				}
			}
		}

		return connTestDoneMsg{dbVersion: info.Version, results: results}
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// filterPermissionsForVersion drops checks that don't apply to the live
// server build. Keeps the static check list simple while preventing false
// "✗ permission missing" results on editions that don't ship that object.
//
// Currently:
//   - openGauss-lite (轻量版 5.x) doesn't have the gs_stat_activity view —
//     that view ships only in the full openGauss / GaussDB enterprise
//     builds. Without this filter, every og-lite install shows a red X.
func filterPermissionsForVersion(queries []PermCheckQuery, dbType, version string) []PermCheckQuery {
	if dbType != "opengauss" && dbType != "gaussdb" {
		return queries
	}
	v := strings.ToLower(version)
	if !strings.Contains(v, "lite") {
		return queries
	}
	out := make([]PermCheckQuery, 0, len(queries))
	for _, q := range queries {
		if q.Name == "gs_stat_activity" {
			continue
		}
		out = append(out, q)
	}
	return out
}

func (s *ConnTestStep) buildChoice() {
	testOK := s.err == nil
	if testOK {
		s.choice = NewSelectModel("", []SelectItem{
			{Label: "下一步", Value: "next", Desc: "继续配置"},
			{Label: "重新配置", Value: "reconfigure", Desc: "修改连接信息"},
		}, "next")
	} else {
		s.choice = NewSelectModel("", []SelectItem{
			{Label: "重新配置", Value: "reconfigure", Desc: "修改连接信息"},
			{Label: "跳过", Value: "skip", Desc: "稍后通过 " + brand.Current().BinaryName + " configure 配置"},
		}, "reconfigure")
	}
}

func (s *ConnTestStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case connTestRunning:
		if m, ok := msg.(connTestDoneMsg); ok {
			s.dbVersion = m.dbVersion
			s.results = m.results
			s.err = m.err
			if s.cfg != nil {
				s.cfg.ConnTestOK = m.err == nil
				s.cfg.ConnVersion = m.dbVersion
			}
			s.buildChoice()
			s.phase = connTestChoosing
			return s, nil
		}

	case connTestChoosing:
		updated, cmd := s.choice.Update(msg)
		s.choice = updated
		if s.choice.Done() {
			selected := s.choice.Selected()
			if selected == "reconfigure" {
				// Go back to connection form (1 step back)
				return s, emitBack(1)
			}
			// "next" or "skip"
			s.done = true
			return s, emitDone
		}
		return s, cmd
	}
	return s, nil
}

func (s *ConnTestStep) View() string {
	if s.skipped {
		return "\n" + BulletLine("Connection test skipped (will test when you first connect)") + "\n"
	}

	if s.phase == connTestRunning {
		return "\n" + BulletLine("Testing connection...") + "\n"
	}

	var b strings.Builder

	if s.err != nil {
		b.WriteString(ErrorLine("连接失败: " + s.err.Error()) + "\n")
		b.WriteString("\n" + StyleDim.Render("  请检查连接信息是否正确，数据库是否已启动。") + "\n")
	} else {
		// Show the literal version banner returned by the database. We no
		// longer brand-rewrite "openGauss" → "GaussDB" — db_type=gaussdb is
		// now a real, separate type with its own driver, so the legacy
		// rewrite would obscure the actual product the user connected to.
		b.WriteString(SuccessLine(fmt.Sprintf("Connection successful (%s)", s.dbVersion)) + "\n\n")

		for _, r := range s.results {
			if r.Warning {
				b.WriteString(WarningLine(r.Name+" — "+r.Detail) + "\n")
			} else if r.OK {
				b.WriteString(SuccessLine(r.Name+" — OK") + "\n")
			} else {
				b.WriteString(ErrorLine(r.Name+" — "+r.Detail) + "\n")
			}
		}

		requiredCount := len(PermissionCheckQueries(s.cfg.DBType))
		passed := 0
		for _, r := range s.results {
			if r.OK && !r.Warning {
				passed++
			}
		}
		b.WriteString(fmt.Sprintf("\n  Result: %d/%d 必要权限就绪", passed, requiredCount))
	}

	result := "\n" + InfoPanel("Connection Test", b.String()) + "\n"

	if s.choice != nil {
		result += s.choice.View()
	}

	return result
}
