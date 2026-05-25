/*-------------------------------------------------------------------------
 *
 * connstring.go
 *	  ParsedConnString holds the parsed components of a connection
 *	  string.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/connection/connstring.go
 *
 *-------------------------------------------------------------------------
 */
package connection

import (
	"fmt"
	"strconv"
	"strings"
)

const defaultPort = 1521

// ParsedConnString holds the parsed components of a connection string.
type ParsedConnString struct {
	User        string
	Password    string
	Host        string
	Port        int
	Service     string
	HasPassword bool
	Privilege   string // sysdba|sysoper (empty = normal)
	IsOSAuth    bool   // true if user is "/" (OS authentication)
}

// ParseConnString parses an Oracle-style connection string.
// Supported formats:
//
//	user/pass@host:port/service
//	user/pass@host/service          (default port 1521)
//	user@host:port/service          (will prompt for password)
//	user@host/service               (default port, prompt password)
//	/@host:port/service             (OS authentication)
//	/ as sysdba                     (OS auth, localhost, default port)
//	user/pass@host:port/service as sysdba
func ParseConnString(s string) (ParsedConnString, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ParsedConnString{}, fmt.Errorf("empty connection string")
	}

	result := ParsedConnString{Port: defaultPort}

	// Extract "as sysdba" / "as sysoper" suffix.
	lower := strings.ToLower(s)
	if idx := strings.Index(lower, " as "); idx >= 0 {
		suffix := strings.TrimSpace(s[idx+4:])
		switch strings.ToLower(suffix) {
		case "sysdba":
			result.Privilege = "sysdba"
		case "sysoper":
			result.Privilege = "sysoper"
		default:
			return ParsedConnString{}, fmt.Errorf("unknown privilege %q, use: sysdba, sysoper", suffix)
		}
		s = strings.TrimSpace(s[:idx])
	}

	// Handle bare "/" (OS auth, localhost).
	if s == "/" {
		result.IsOSAuth = true
		result.Host = "127.0.0.1"
		return result, nil
	}

	// Split on "@" to separate credentials from host.
	atIdx := strings.Index(s, "@")
	if atIdx < 0 {
		return ParsedConnString{}, fmt.Errorf("invalid format: expected user@host or user/pass@host")
	}

	credPart := s[:atIdx]
	hostPart := s[atIdx+1:]

	// Parse credentials.
	if credPart == "/" || credPart == "" {
		result.IsOSAuth = true
	} else {
		slashIdx := strings.Index(credPart, "/")
		if slashIdx >= 0 {
			result.User = credPart[:slashIdx]
			result.Password = credPart[slashIdx+1:]
			result.HasPassword = true
		} else {
			result.User = credPart
		}
	}

	// Parse host:port/service.
	if hostPart == "" {
		return ParsedConnString{}, fmt.Errorf("host is required after @")
	}

	serviceIdx := strings.Index(hostPart, "/")
	hostPort := hostPart
	if serviceIdx >= 0 {
		result.Service = hostPart[serviceIdx+1:]
		hostPort = hostPart[:serviceIdx]
	}

	colonIdx := strings.LastIndex(hostPort, ":")
	if colonIdx >= 0 {
		result.Host = hostPort[:colonIdx]
		portStr := hostPort[colonIdx+1:]
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return ParsedConnString{}, fmt.Errorf("invalid port %q: %w", portStr, err)
		}
		result.Port = port
	} else {
		result.Host = hostPort
	}

	if result.Host == "" {
		return ParsedConnString{}, fmt.Errorf("host is required")
	}

	return result, nil
}
