/*-------------------------------------------------------------------------
 *
 * human.go
 *	  HumanNumber formats a number with K/M/G suffixes for readability.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/human.go
 *
 *-------------------------------------------------------------------------
 */
package format

import "fmt"

// HumanNumber formats a number with K/M/G suffixes for readability.
func HumanNumber(n float64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fG", n/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", n/1e6)
	case n >= 1e4:
		return fmt.Sprintf("%.1fK", n/1e3)
	default:
		if n == float64(int(n)) {
			return fmt.Sprintf("%d", int(n))
		}
		return fmt.Sprintf("%.1f", n)
	}
}

// HumanBytes formats a byte count into human-readable format (B, K, M, G, T).
// Input is raw bytes as float64.
func HumanBytes(b float64) string {
	switch {
	case b >= 1024*1024*1024*1024:
		return fmt.Sprintf("%.1fT", b/(1024*1024*1024*1024))
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1fG", b/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.0fM", b/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.0fK", b/1024)
	default:
		return fmt.Sprintf("%.0fB", b)
	}
}
