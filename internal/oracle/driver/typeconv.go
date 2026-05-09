/*-------------------------------------------------------------------------
 *
 * typeconv.go
 *	  Go ↔ Oracle type conversion helpers — turns NUMBER, DATE,
 *	  TIMESTAMP, RAW, CLOB, BFILE values into Go types the rest
 *	  of the codebase already understands.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/driver/typeconv.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	go_ora "github.com/sijms/go-ora/v2"
)

// convertOracleValue converts Oracle-specific types to standard Go types.
// It handles common Oracle driver types that don't map cleanly to Go primitives.
func convertOracleValue(val any) any {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case time.Time:
		return v
	case *time.Time:
		if v == nil {
			return nil
		}
		return *v
	case []byte:
		return v
	case string:
		return v
	case int64:
		return v
	case float64:
		return v
	case bool:
		return v
	case go_ora.Number:
		// go-ora returns Oracle NUMBER as custom Number type.
		// Try float64 first (handles both integers and decimals).
		if f, err := v.Float64(); err == nil {
			return f
		}
		// Float64() fails for some numbers; parse from string representation.
		if s, err := v.String(); err == nil {
			return parseNumericString(s)
		}
		return float64(0)
	case *go_ora.Number:
		if v == nil {
			return nil
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		if s, err := v.String(); err == nil {
			return parseNumericString(s)
		}
		return float64(0)
	default:
		// DEBUG: log unknown types to file for diagnosis
		debugLogType(val)
		return fmt.Sprintf("%v", v)
	}
}

// parseNumericString tries to convert a numeric string to int64 or float64.
// Returns the original string if parsing fails.
func parseNumericString(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Try int64 first (no decimal point).
	if !strings.Contains(s, ".") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	}
	// Try float64.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// debugLogType writes unknown types to /tmp/opendb-types.log for diagnosis.
func debugLogType(val any) {
	f, err := os.OpenFile("/tmp/opendb-types.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "type=%T reflect=%v value=%v\n", val, reflect.TypeOf(val), val)
}

// convertNumberToGo converts an Oracle NUMBER value (represented as a string)
// to the most appropriate Go type based on scale and precision.
//   - scale == 0 and fits in int64 => int64
//   - scale == 0 and too large     => string
//   - scale > 0                    => float64 (or string if precision loss)
func convertNumberToGo(numStr string, precision, scale int) any {
	numStr = strings.TrimSpace(numStr)
	if numStr == "" {
		return nil
	}

	if scale == 0 {
		return convertIntegerNumber(numStr)
	}
	return convertDecimalNumber(numStr, precision)
}

// convertIntegerNumber converts a whole-number Oracle NUMBER to int64 or string.
func convertIntegerNumber(numStr string) any {
	n := new(big.Int)
	if _, ok := n.SetString(numStr, 10); !ok {
		return numStr
	}

	if n.IsInt64() {
		return n.Int64()
	}
	// Number too large for int64; return as string to avoid precision loss
	return numStr
}

// convertDecimalNumber converts a fractional Oracle NUMBER to float64 or string.
func convertDecimalNumber(numStr string, precision int) any {
	f := new(big.Float)
	if _, ok := f.SetString(numStr); !ok {
		return numStr
	}

	float64Val, _ := f.Float64()
	if math.IsInf(float64Val, 0) || (precision > 15) {
		// Precision exceeds float64 capability; return as string
		return numStr
	}
	return float64Val
}

// parseDurationInterval parses an Oracle INTERVAL DAY TO SECOND string
// into a Go time.Duration. The expected format is:
//
//	+DD HH:MI:SS.FFFFFF  or  -DD HH:MI:SS.FFFFFF
func parseDurationInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty interval string")
	}

	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}

	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid interval format: expected 'DD HH:MI:SS', got %q", s)
	}

	var days int
	if _, err := fmt.Sscanf(parts[0], "%d", &days); err != nil {
		return 0, fmt.Errorf("invalid days in interval: %w", err)
	}

	timeParts := strings.SplitN(parts[1], ":", 3)
	if len(timeParts) != 3 {
		return 0, fmt.Errorf("invalid time portion in interval: %q", parts[1])
	}

	var hours, minutes int
	if _, err := fmt.Sscanf(timeParts[0], "%d", &hours); err != nil {
		return 0, fmt.Errorf("invalid hours in interval: %w", err)
	}
	if _, err := fmt.Sscanf(timeParts[1], "%d", &minutes); err != nil {
		return 0, fmt.Errorf("invalid minutes in interval: %w", err)
	}

	var seconds float64
	if _, err := fmt.Sscanf(timeParts[2], "%f", &seconds); err != nil {
		return 0, fmt.Errorf("invalid seconds in interval: %w", err)
	}

	d := time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second))

	if negative {
		d = -d
	}
	return d, nil
}

// truncateCLOB truncates a CLOB/NCLOB string to the specified maximum byte length.
// If the string exceeds maxBytes, it is truncated and "... [truncated]" is appended.
func truncateCLOB(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return s
	}
	if len(s) <= maxBytes {
		return s
	}
	const suffix = "... [truncated]"
	truncLen := maxBytes - len(suffix)
	if truncLen < 0 {
		truncLen = 0
	}
	return s[:truncLen] + suffix
}
