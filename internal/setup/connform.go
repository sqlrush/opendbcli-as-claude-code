/*-------------------------------------------------------------------------
 *
 * connform.go
 *	  ConnFormStep collects database connection details
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/connform.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sqlrush/opendb/internal/config"
)

// isPGFamily reports whether dbType uses libpq-style port/database fields:
// postgres, openGauss (open source), gaussdb (Huawei commercial).
func isPGFamily(dbType string) bool {
	return dbType == "postgres" || dbType == "opengauss" || dbType == "gaussdb"
}

type connFormPhase int

const (
	phaseFields connFormPhase = iota
	phaseAuth
	phasePassword
)

// ConnFormStep collects database connection details
type ConnFormStep struct {
	cfg       *SetupConfig
	fields    []*InputModel
	authSel   *SelectModel
	passwdInp *InputModel
	fieldIdx  int
	phase     connFormPhase
	done      bool
}

func NewConnFormStep(cfg *SetupConfig) *ConnFormStep {
	return &ConnFormStep{cfg: cfg}
}

func (s *ConnFormStep) initFields() {
	defaultPort := "1521"
	serviceLabel := "Service name"
	if s.cfg.DBType == "mysql" {
		defaultPort = "3306"
		serviceLabel = "Database name"
	} else if isPGFamily(s.cfg.DBType) {
		defaultPort = "5432"
		serviceLabel = "Database name"
	}

	connHint := "例如: prod-oracle-01"
	serviceHint := ""
	if s.cfg.DBType == "mysql" {
		connHint = "例如: prod-mysql-01"
		serviceHint = "例如: mydb"
	} else if isPGFamily(s.cfg.DBType) {
		connHint = "例如: prod-pg-01"
		serviceHint = "例如: postgres"
	} else {
		serviceHint = "例如: orclpdb1"
	}

	s.fields = []*InputModel{
		NewInputModelWithHint("Connection name", "", connHint),
		NewInputModelWithHint("Host", "127.0.0.1", "数据库主机地址"),
		NewInputModel("Port", defaultPort, false),
		NewInputModelWithHint(serviceLabel, "", serviceHint),
		NewInputModel("Username", "", false),
	}

	authItems := buildAuthItems(s.cfg.DBType)
	s.authSel = NewSelectModel("Authentication method", authItems, "prompt")
}

func buildAuthItems(dbType string) []SelectItem {
	items := []SelectItem{
		{Label: "prompt", Value: "prompt", Desc: "每次连接时输入密码"},
		{Label: "save", Value: "save", Desc: "加密保存到本地 (AES-256-GCM)"},
	}
	if dbType == "oracle" {
		items = append(items,
			SelectItem{Label: "wallet", Value: "wallet", Desc: "Oracle Wallet 自动登录"},
			SelectItem{Label: "os", Value: "os", Desc: "操作系统认证"},
		)
	}
	items = append(items,
		SelectItem{Label: "ldap", Value: "ldap", Desc: "LDAP/AD 认证"},
		SelectItem{Label: "kerberos", Value: "kerberos", Desc: "Kerberos 认证"},
		SelectItem{Label: "token", Value: "token", Desc: "OAuth2 令牌认证"},
	)
	return items
}

func (s *ConnFormStep) Title() string { return "Connection" }
func (s *ConnFormStep) Done() bool    { return s.done }

func (s *ConnFormStep) Summary() string {
	conn := s.cfg.Connection
	addr := conn.Host
	if conn.Port > 0 {
		addr += fmt.Sprintf(":%d", conn.Port)
	}
	svc := conn.Service
	if svc == "" {
		svc = conn.Database
	}
	return CompletedLine("Connection",
		conn.Name+" · "+addr+"/"+svc+" · "+conn.User+" · "+conn.AuthMode)
}

func (s *ConnFormStep) Init() tea.Cmd {
	s.initFields()
	return nil
}

func (s *ConnFormStep) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch s.phase {
	case phaseFields:
		return s.updateFields(msg)
	case phaseAuth:
		return s.updateAuth(msg)
	case phasePassword:
		return s.updatePassword(msg)
	}
	return s, nil
}

func (s *ConnFormStep) updateFields(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.fields[s.fieldIdx].Update(msg)
	s.fields[s.fieldIdx] = updated
	if s.fields[s.fieldIdx].Done() {
		// Reset done state — we handle progression ourselves
		s.fields[s.fieldIdx].done = false
		s.fieldIdx++
		if s.fieldIdx >= len(s.fields) {
			s.phase = phaseAuth
		}
		return s, nil
	}
	return s, cmd
}

func (s *ConnFormStep) updateAuth(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.authSel.Update(msg)
	s.authSel = updated
	if s.authSel.Done() {
		authMode := s.authSel.Selected()
		if authMode == "prompt" || authMode == "save" {
			s.passwdInp = NewInputModel("Password", "", true)
			s.phase = phasePassword
			return s, nil
		}
		s.buildConnection()
		s.done = true
		return s, emitDone
	}
	return s, cmd
}

func (s *ConnFormStep) updatePassword(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := s.passwdInp.Update(msg)
	s.passwdInp = updated
	if s.passwdInp.Done() {
		s.buildConnection()
		s.done = true
		return s, emitDone
	}
	return s, cmd
}

func (s *ConnFormStep) buildConnection() {
	port, _ := strconv.Atoi(s.fields[2].ValueOrDefault())
	authMode := s.authSel.Selected()

	conn := config.Connection{
		Name:     s.fields[0].ValueOrDefault(),
		DBType:   s.cfg.DBType,
		Host:     s.fields[1].ValueOrDefault(),
		Port:     port,
		User:     s.fields[4].ValueOrDefault(),
		AuthMode: authMode,
		Credential: config.Credential{
			Provider: authMode,
		},
	}

	if s.cfg.DBType == "oracle" {
		conn.Service = s.fields[3].ValueOrDefault()
	} else {
		conn.Database = s.fields[3].ValueOrDefault()
	}

	s.cfg.Connection = conn
	if s.passwdInp != nil {
		s.cfg.Password = s.passwdInp.Value()
	}
}

func (s *ConnFormStep) View() string {
	var b strings.Builder
	b.WriteString("\n")

	// Show completed fields
	for i := 0; i < len(s.fields); i++ {
		if i < s.fieldIdx {
			b.WriteString(SuccessLine(s.fields[i].label + ": " + s.fields[i].ValueOrDefault()) + "\n")
		} else if i == s.fieldIdx && s.phase == phaseFields {
			b.WriteString(s.fields[i].View())
		}
	}

	if s.phase == phaseAuth {
		for i := s.fieldIdx; i < len(s.fields); i++ {
			b.WriteString(SuccessLine(s.fields[i].label + ": " + s.fields[i].ValueOrDefault()) + "\n")
		}
		b.WriteString(s.authSel.View())
	}

	if s.phase == phasePassword {
		for _, f := range s.fields {
			b.WriteString(SuccessLine(f.label + ": " + f.ValueOrDefault()) + "\n")
		}
		b.WriteString(SuccessLine("Auth: " + s.authSel.SelectedLabel()) + "\n")
		b.WriteString(s.passwdInp.View())
	}

	return b.String()
}
