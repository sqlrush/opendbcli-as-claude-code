/*-------------------------------------------------------------------------
 *
 * typeconv_test.go
 *	  Test cases for typeconv.go (driver package):
 *	  TestConvertOracleValue_Nil, TestConvertOracleValue_String,
 *	  TestConvertOracleValue_Int64.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/driver/typeconv_test.go
 *
 *-------------------------------------------------------------------------
 */
package driver

import (
	"testing"
	"time"
)

func TestConvertOracleValue_Nil(t *testing.T) {
	result := convertOracleValue(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestConvertOracleValue_String(t *testing.T) {
	result := convertOracleValue("hello")
	if result != "hello" {
		t.Errorf("expected %q, got %v", "hello", result)
	}
}

func TestConvertOracleValue_Int64(t *testing.T) {
	result := convertOracleValue(int64(42))
	if result != int64(42) {
		t.Errorf("expected 42, got %v", result)
	}
}

func TestConvertOracleValue_Float64(t *testing.T) {
	result := convertOracleValue(float64(3.14))
	if result != float64(3.14) {
		t.Errorf("expected 3.14, got %v", result)
	}
}

func TestConvertOracleValue_Bool(t *testing.T) {
	result := convertOracleValue(true)
	if result != true {
		t.Errorf("expected true, got %v", result)
	}
}

func TestConvertOracleValue_Time(t *testing.T) {
	now := time.Now()
	result := convertOracleValue(now)
	if result != now {
		t.Errorf("expected %v, got %v", now, result)
	}
}

func TestConvertOracleValue_TimePtr(t *testing.T) {
	now := time.Now()
	result := convertOracleValue(&now)
	if result != now {
		t.Errorf("expected %v, got %v", now, result)
	}
}

func TestConvertOracleValue_NilTimePtr(t *testing.T) {
	var tp *time.Time
	result := convertOracleValue(tp)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestConvertOracleValue_Bytes(t *testing.T) {
	b := []byte{0x01, 0x02, 0x03}
	result := convertOracleValue(b)
	rb, ok := result.([]byte)
	if !ok {
		t.Fatalf("expected []byte, got %T", result)
	}
	if len(rb) != 3 || rb[0] != 0x01 {
		t.Errorf("unexpected byte slice: %v", rb)
	}
}

func TestConvertOracleValue_UnknownType(t *testing.T) {
	type custom struct{ val int }
	result := convertOracleValue(custom{val: 5})
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string fallback, got %T", result)
	}
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}

func TestConvertNumberToGo_IntegerSmall(t *testing.T) {
	result := convertNumberToGo("42", 10, 0)
	v, ok := result.(int64)
	if !ok {
		t.Fatalf("expected int64, got %T", result)
	}
	if v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
}

func TestConvertNumberToGo_IntegerNegative(t *testing.T) {
	result := convertNumberToGo("-100", 10, 0)
	v, ok := result.(int64)
	if !ok {
		t.Fatalf("expected int64, got %T", result)
	}
	if v != -100 {
		t.Errorf("expected -100, got %d", v)
	}
}

func TestConvertNumberToGo_IntegerTooLarge(t *testing.T) {
	huge := "99999999999999999999999999999999999999"
	result := convertNumberToGo(huge, 38, 0)
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string for huge number, got %T", result)
	}
	if s != huge {
		t.Errorf("expected %q, got %q", huge, s)
	}
}

func TestConvertNumberToGo_Decimal(t *testing.T) {
	result := convertNumberToGo("3.14159", 10, 5)
	v, ok := result.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", result)
	}
	if v < 3.14 || v > 3.15 {
		t.Errorf("expected ~3.14159, got %f", v)
	}
}

func TestConvertNumberToGo_HighPrecisionDecimal(t *testing.T) {
	highPrec := "1.23456789012345678901234567890"
	result := convertNumberToGo(highPrec, 30, 29)
	// precision > 15 => should return string
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string for high-precision decimal, got %T", result)
	}
	if s != highPrec {
		t.Errorf("expected %q, got %q", highPrec, s)
	}
}

func TestConvertNumberToGo_Empty(t *testing.T) {
	result := convertNumberToGo("", 10, 0)
	if result != nil {
		t.Errorf("expected nil for empty string, got %v", result)
	}
}

func TestConvertNumberToGo_Whitespace(t *testing.T) {
	result := convertNumberToGo("  ", 10, 0)
	if result != nil {
		t.Errorf("expected nil for whitespace-only string, got %v", result)
	}
}

func TestConvertNumberToGo_InvalidString(t *testing.T) {
	result := convertNumberToGo("not-a-number", 10, 0)
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string for invalid number, got %T", result)
	}
	if s != "not-a-number" {
		t.Errorf("expected %q, got %q", "not-a-number", s)
	}
}

func TestParseDurationInterval_Simple(t *testing.T) {
	d, err := parseDurationInterval("+01 02:03:04.500000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 1*24*time.Hour + 2*time.Hour + 3*time.Minute + 4*time.Second + 500*time.Millisecond
	if d != expected {
		t.Errorf("expected %v, got %v", expected, d)
	}
}

func TestParseDurationInterval_Negative(t *testing.T) {
	d, err := parseDurationInterval("-00 00:05:30.000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := -(5*time.Minute + 30*time.Second)
	if d != expected {
		t.Errorf("expected %v, got %v", expected, d)
	}
}

func TestParseDurationInterval_ZeroDays(t *testing.T) {
	d, err := parseDurationInterval("+00 01:00:00.000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != time.Hour {
		t.Errorf("expected %v, got %v", time.Hour, d)
	}
}

func TestParseDurationInterval_Empty(t *testing.T) {
	_, err := parseDurationInterval("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

func TestParseDurationInterval_Invalid(t *testing.T) {
	_, err := parseDurationInterval("not-an-interval")
	if err == nil {
		t.Error("expected error for invalid interval")
	}
}

func TestParseDurationInterval_NoSign(t *testing.T) {
	d, err := parseDurationInterval("02 12:30:00.000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 2*24*time.Hour + 12*time.Hour + 30*time.Minute
	if d != expected {
		t.Errorf("expected %v, got %v", expected, d)
	}
}

func TestTruncateCLOB_NoTruncation(t *testing.T) {
	s := "short string"
	result := truncateCLOB(s, 100)
	if result != s {
		t.Errorf("expected %q, got %q", s, result)
	}
}

func TestTruncateCLOB_Truncated(t *testing.T) {
	s := "this is a longer string that should be truncated"
	result := truncateCLOB(s, 30)
	if len(result) > 30 {
		t.Errorf("result length %d exceeds max %d", len(result), 30)
	}
	if result[len(result)-1] != ']' {
		t.Errorf("expected truncation suffix, got %q", result)
	}
}

func TestTruncateCLOB_ExactLength(t *testing.T) {
	s := "exactly"
	result := truncateCLOB(s, len(s))
	if result != s {
		t.Errorf("expected %q, got %q", s, result)
	}
}

func TestTruncateCLOB_ZeroMax(t *testing.T) {
	s := "something"
	result := truncateCLOB(s, 0)
	if result != s {
		t.Errorf("expected original string for maxBytes=0, got %q", result)
	}
}
