/*-------------------------------------------------------------------------
 *
 * json.go
 *	  JSON marshalling helpers for tool definitions — converts the
 *	  Go-side tool/parameter structs into the OpenAI function-calling
 *	  schema providers expect.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/json.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import "encoding/json"

// jsonUnmarshal is a package-level alias to avoid scattering encoding/json imports.
var jsonUnmarshal = json.Unmarshal
