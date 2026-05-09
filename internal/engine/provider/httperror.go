/*-------------------------------------------------------------------------
 *
 * httperror.go
 *	  HTTPError carries HTTP status, headers, and body for retry
 *	  classification.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/httperror.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"fmt"
	"net/http"
)

// HTTPError carries HTTP status, headers, and body for retry classification.
type HTTPError struct {
	StatusCode int
	Headers    http.Header
	Body       string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}
