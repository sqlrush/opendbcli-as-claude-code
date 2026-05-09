/*-------------------------------------------------------------------------
 *
 * ssl.go
 *	  ssl — SSLSkill plus helpers (NewSSLSkill) used by the monitor
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/ssl.go
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
)

const sslStatusSQL = `SELECT name, setting FROM pg_settings WHERE name IN (
  'ssl', 'ssl_cert_file', 'ssl_key_file', 'ssl_ca_file',
  'ssl_crl_file', 'ssl_min_protocol_version', 'ssl_max_protocol_version'
) ORDER BY name`

const sslCertSQL = `SELECT
  ssl, version, cipher, bits, client_dn, client_serial, issuer_dn
FROM pg_stat_ssl
WHERE pid = pg_backend_pid()`

const sslConnCountSQL = `SELECT
  COALESCE(SUM(CASE WHEN s.ssl THEN 1 ELSE 0 END), 0) AS ssl_conns,
  COUNT(*) AS total_conns
FROM pg_stat_activity a
LEFT JOIN pg_stat_ssl s ON a.pid = s.pid
WHERE a.backend_type = 'client backend'`

type SSLSkill struct{ driver db.Driver }

func NewSSLSkill(driver db.Driver) *SSLSkill { return &SSLSkill{driver: driver} }

func (s *SSLSkill) Name() string                      { return "ssl" }
func (s *SSLSkill) Description() string               { return "SSL/TLS configuration and certificate status" }
func (s *SSLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SSLSkill) Validate(_ skill.Params) error     { return nil }
func (s *SSLSkill) CLIDef() skill.CLIDef              { return skill.CLIDef{Usage: "/ssl"} }
func (s *SSLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "ssl",
		Description: "Show SSL/TLS configuration, current connection encryption status, and SSL connection statistics",
	}
}

func (s *SSLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	var sections []format.PanelSection

	// SSL settings
	settingsResult, err := s.driver.Query(ctx, sslStatusSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	settingsLines := formatSSLSettings(settingsResult)
	sections = append(sections, format.PanelSection{Header: "Configuration", Lines: settingsLines})

	// Current connection SSL
	certResult, _ := s.driver.Query(ctx, sslCertSQL)
	certLines := formatSSLCert(certResult)
	sections = append(sections, format.PanelSection{Header: "Current Connection", Lines: certLines})

	// SSL connection stats
	countResult, _ := s.driver.Query(ctx, sslConnCountSQL)
	countLines := formatSSLCounts(countResult)
	sections = append(sections, format.PanelSection{Header: "Connection Statistics", Lines: countLines})

	rendered := format.Panel("SSL/TLS Status", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  summarizeSSL(settingsResult),
	}, nil
}

func formatSSLSettings(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"  No SSL settings found"}
	}
	var lines []string
	for _, row := range qr.Rows {
		if len(row) < 2 {
			continue
		}
		name := fmtVal(row[0])
		val := fmtVal(row[1])
		lines = append(lines, fmt.Sprintf("  %-30s %s", name, val))
	}
	return lines
}

func formatSSLCert(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"  SSL info not available for current connection"}
	}
	row := qr.Rows[0]
	if len(row) < 4 {
		return []string{"  Incomplete SSL data"}
	}
	sslEnabled := fmtVal(row[0])
	version := fmtVal(row[1])
	cipher := fmtVal(row[2])
	bits := fmtVal(row[3])

	var lines []string
	if strings.EqualFold(sslEnabled, "true") || sslEnabled == "t" {
		lines = append(lines, fmt.Sprintf("  Encrypted:  YES (%s, %s, %s bits)", version, cipher, bits))
	} else {
		lines = append(lines, "  Encrypted:  NO (plaintext connection)")
	}
	if len(row) >= 6 {
		clientDN := fmtVal(row[4])
		if clientDN != "-" && clientDN != "" {
			lines = append(lines, fmt.Sprintf("  Client DN:  %s", clientDN))
		}
	}
	if len(row) >= 7 {
		issuer := fmtVal(row[6])
		if issuer != "-" && issuer != "" {
			lines = append(lines, fmt.Sprintf("  Issuer:     %s", issuer))
		}
	}
	return lines
}

func formatSSLCounts(qr *db.QueryResult) []string {
	if qr == nil || len(qr.Rows) == 0 {
		return []string{"  Connection stats not available"}
	}
	row := qr.Rows[0]
	if len(row) < 2 {
		return []string{"  Incomplete data"}
	}
	sslConns := fmtVal(row[0])
	totalConns := fmtVal(row[1])
	return []string{
		fmt.Sprintf("  SSL connections:   %s / %s total", sslConns, totalConns),
	}
}

func summarizeSSL(qr *db.QueryResult) string {
	if qr == nil {
		return "SSL status unknown"
	}
	for _, row := range qr.Rows {
		if len(row) >= 2 && fmtVal(row[0]) == "ssl" {
			if fmtVal(row[1]) == "on" {
				return "SSL enabled"
			}
			return "SSL disabled"
		}
	}
	return "SSL status unknown"
}
