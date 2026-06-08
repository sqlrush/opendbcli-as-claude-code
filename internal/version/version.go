/*-------------------------------------------------------------------------
 *
 * version.go
 *	  String returns the full version banner: "<binary> <version>
 *	  (commit: ..., built: ...)". Brand-controlled binary name so dbaa
 *	  builds show "dbaa v1.1.19 ..." not "opendb ...".
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/version/version.go
 *
 *-------------------------------------------------------------------------
 */
package version

import (
	"fmt"

	"github.com/sqlrush/opendb/internal/brand"
)

var (
	Version   = "v1.2.39"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// String returns the full version banner: "<binary> <version> (commit: ..., built: ...)".
// Brand-controlled binary name so dbaa builds show "dbaa v1.1.19 ..." not "opendb ...".
func String() string {
	return fmt.Sprintf("%s %s (commit: %s, built: %s)", brand.Current().BinaryName, Version, GitCommit, BuildDate)
}

// Short returns a compact version string like "OpenDB v1.1.19" or "dbaa v1.1.19".
func Short() string {
	return fmt.Sprintf("%s %s", brand.Current().AppName, Version)
}
