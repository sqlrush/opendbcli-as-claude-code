/*-------------------------------------------------------------------------
 *
 * users.go
 *	  UsersSkill shows database user accounts with password expiry
 *	  status.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/users.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
	"github.com/sqlrush/opendb/internal/skill/builtin/shared"
)

const usersSQL = `SELECT username, account_status, default_tablespace, profile,
       TO_CHAR(created, 'YYYY-MM-DD') AS created_date,
       CASE WHEN expiry_date IS NULL THEN '-'
            ELSE TO_CHAR(expiry_date, 'YYYY-MM-DD') END AS expiry_str,
       CASE WHEN expiry_date IS NULL THEN NULL
            ELSE ROUND(expiry_date - SYSDATE, 0) END AS days_left
FROM dba_users
WHERE username NOT IN ('SYS','SYSTEM','DBSNMP','OUTLN','APPQOSSYS','WMSYS','CTXSYS','XDB','ORDDATA','MDSYS','OLAPSYS','ORDSYS','GSMADMIN_INTERNAL','LBACSYS','DVSYS','AUDSYS','ANONYMOUS','OJVMSYS','SI_INFORMTN_SCHEMA','DIP','REMOTE_SCHEDULER_AGENT','SYSBACKUP','SYSDG','SYSKM','SYSRAC','SYS$UMF')
ORDER BY
  CASE WHEN account_status LIKE '%EXPIRED%' THEN 0
       WHEN expiry_date IS NOT NULL AND expiry_date < SYSDATE + 14 THEN 1
       ELSE 2 END,
  expiry_date NULLS LAST`

// UsersSkill shows database user accounts with password expiry status.
type UsersSkill struct {
	driver db.Driver
}

// NewUsersSkill creates a UsersSkill backed by the given driver.
func NewUsersSkill(driver db.Driver) *UsersSkill {
	return &UsersSkill{driver: driver}
}

func (s *UsersSkill) Name() string                       { return "users" }
func (s *UsersSkill) Description() string                { return "User accounts with password expiry status" }
func (s *UsersSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *UsersSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "users",
		Description: "Show database user accounts with password expiry status",
	}
}

func (s *UsersSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "users",
		Usage:    "/users",
		Examples: []string{"/users"},
	}
}

func (s *UsersSkill) Validate(_ skill.Params) error { return nil }

func (s *UsersSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, usersSQL)
	if err != nil {
		return nil, fmt.Errorf("querying users: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无用户账户",
			Summary:  "no users",
		}, nil
	}

	var rows [][]any
	var alerts []string
	for _, row := range result.Rows {
		if len(row) < 7 {
			continue
		}
		username := shared.AsString(row[0])
		status := shared.AsString(row[1])
		tablespace := shared.AsString(row[2])
		profile := shared.AsString(row[3])
		expiryStr := shared.AsString(row[5])
		daysLeft := shared.ToFloat64(row[6])
		hasDays := row[6] != nil

		// Determine password status.
		pwStatus := "OK"
		if strings.Contains(status, "EXPIRED") {
			pwStatus = "EXPIRED"
			alerts = append(alerts, fmt.Sprintf("CRITICAL: %s 密码已过期", username))
		} else if hasDays {
			days := int(daysLeft)
			if days < 0 {
				pwStatus = "EXPIRED"
				alerts = append(alerts, fmt.Sprintf("CRITICAL: %s 密码已过期", username))
			} else if days <= 7 {
				pwStatus = fmt.Sprintf("%dd", days)
				alerts = append(alerts, fmt.Sprintf("WARN: %s 密码 %d 天后过期", username, days))
			} else if days <= 14 {
				pwStatus = fmt.Sprintf("%dd", days)
				alerts = append(alerts, fmt.Sprintf("INFO: %s 密码 %d 天后过期", username, days))
			}
		}

		statusIcon := ""
		if strings.Contains(status, "LOCKED") {
			statusIcon = "LOCKED"
		} else if strings.Contains(status, "EXPIRED") {
			statusIcon = "EXPIRED"
		} else {
			statusIcon = "OPEN"
		}

		rows = append(rows, []any{
			username,
			statusIcon,
			pwStatus,
			expiryStr,
			profile,
			tablespace,
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"User", "Status", "Password", "Expiry", "Profile", "Tablespace"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120})

	var b strings.Builder
	fmt.Fprintf(&b, "用户账户 (%d 个)\n\n%s", len(rows), table)

	if len(alerts) > 0 {
		b.WriteString("\n\n密码告警:\n")
		for _, a := range alerts {
			b.WriteString("  " + a + "\n")
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("%d 用户, %d 告警", len(rows), len(alerts)),
	}, nil
}

