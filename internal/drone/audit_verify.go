/*-------------------------------------------------------------------------
 *
 * audit_verify.go
 *	  VerifyResult holds the outcome of an audit log integrity check.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/audit_verify.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// VerifyResult holds the outcome of an audit log integrity check.
type VerifyResult struct {
	TotalEntries  int   // total number of non-empty lines processed
	ValidEntries  int   // entries whose HMAC matched
	FirstTampered int   // 1-based line number of first tampered entry; -1 if all valid
	TamperedLines []int // 1-based line numbers of all tampered entries
	SkippedLines  int   // lines without a hash suffix (legacy/plain text)
}

// IsValid returns true if no tampering was detected.
func (r *VerifyResult) IsValid() bool {
	return r.FirstTampered == -1
}

// VerifyAuditLog checks every entry in the audit log against its HMAC chain hash.
// logPath is the path to the audit.log file. keyPath is the path to the audit.key file.
func VerifyAuditLog(logPath, keyPath string) (*VerifyResult, error) {
	key, err := readKeyFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	lines, err := readLogLines(logPath)
	if err != nil {
		return nil, fmt.Errorf("read log: %w", err)
	}
	return verifyLines(lines, key), nil
}

// readKeyFile reads and decodes the hex-encoded HMAC key.
func readKeyFile(keyPath string) ([]byte, error) {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", keyPath, err)
	}
	return decodeHexKey(strings.TrimSpace(string(data)))
}

// readLogLines reads all non-empty lines from the log file.
// Returns an empty slice if the file does not exist.
func readLogLines(logPath string) ([]string, error) {
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", logPath, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return lines, nil
}

// verifyLines checks each line's HMAC against the chain.
func verifyLines(lines []string, key []byte) *VerifyResult {
	result := &VerifyResult{
		FirstTampered: -1,
	}
	prevHash := ""

	for i, line := range lines {
		lineNum := i + 1
		entryText, recordedHash, hasHash := splitEntryAndHash(line)
		if !hasHash {
			result.SkippedLines++
			// Legacy line without hash: treat as break in chain.
			// Reset prevHash to "" so the next hashed line chains from genesis.
			prevHash = ""
			continue
		}
		result.TotalEntries++
		expected := computeHMAC(key, prevHash, entryText)
		if expected == recordedHash {
			result.ValidEntries++
		} else {
			result.TamperedLines = append(result.TamperedLines, lineNum)
			if result.FirstTampered == -1 {
				result.FirstTampered = lineNum
			}
		}
		prevHash = recordedHash
	}
	return result
}

// splitEntryAndHash splits a log line into entryText and hash.
// Returns (entryText, hash, true) if a hash suffix exists, or (line, "", false) otherwise.
func splitEntryAndHash(line string) (string, string, bool) {
	idx := strings.LastIndex(line, hashPrefix)
	if idx < 0 {
		return line, "", false
	}
	return line[:idx], line[idx+len(hashPrefix):], true
}
