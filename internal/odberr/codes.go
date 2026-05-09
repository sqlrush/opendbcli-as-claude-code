/*-------------------------------------------------------------------------
 *
 * codes.go
 *	  Module returns the two-char module code embedded in a code string.
 *	  Invalid inputs return "99".
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/codes.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

// Error code constants.
//
// Format: ERR-XXYYYY
//   XX   = module (01-99)
//   YYYY = sequence within module
//
// Module 90 is reserved for generic panic recovery.
// Module 99 is reserved for the unknown / unregistered fallback.
//
// When adding a new constant, also register its Entry in registry.go's
// init-time registration so /error <code> can look it up.
const (
	// --- 01 core: startup, config, initialization ---
	ErrCoreMainPanic       = "ERR-010001"
	ErrCoreConfigLoad      = "ERR-010002"
	ErrCoreSetupWizard     = "ERR-010003"

	// --- 02 conn: connection management ---
	ErrConnOpen            = "ERR-020001"
	ErrConnLost            = "ERR-020002"
	ErrConnAuth            = "ERR-020003"

	// --- 03 ui: REPL, rendering, terminal ---
	ErrUIDiagRender        = "ERR-030001"
	ErrUISkillRender       = "ERR-030002"
	ErrUIResize            = "ERR-030003"

	// --- 04 diag: diagnosis engine, LLM interaction ---
	ErrDiagLLMTimeout      = "ERR-040001"
	ErrDiagToolCall        = "ERR-040002"
	ErrDiagStreamTruncated = "ERR-040003"

	// --- 05 sentinel: monitoring loop ---
	ErrSentinelLoop        = "ERR-050001"
	ErrSentinelCollect     = "ERR-050002"

	// --- 06 rule: rule engine ---
	ErrRuleEval            = "ERR-060001"
	ErrRuleLoad            = "ERR-060002"

	// --- 07 skill: skill execution ---
	ErrSkillExec           = "ERR-070001"
	ErrSkillNotFound       = "ERR-070002"
	ErrSkillInvalidParams  = "ERR-070003"

	// --- 08 llm: provider communication ---
	ErrLLMRequest          = "ERR-080001"
	ErrLLMParseResponse    = "ERR-080002"
	ErrLLMModelNotFound    = "ERR-080003"
	ErrLLMNoActiveModel    = "ERR-080004"
	ErrLLMConfigInvalid    = "ERR-080005"

	// --- 09 storage: file I/O, config write ---
	ErrStorageRead         = "ERR-090001"
	ErrStorageWrite        = "ERR-090002"

	// --- 10 scheduler: scheduled tasks ---
	ErrSchedulerRun        = "ERR-100001"

	// --- 11 cluster: drone, overlord, cerebrate ---
	ErrClusterRPC          = "ERR-110001"

	// --- 90 generic panic segment ---
	ErrPanicREPL           = "ERR-900001"
	ErrPanicGoroutine      = "ERR-900002"

	// --- 99 unknown fallback ---
	ErrUnknown             = "ERR-999999"
)

// Module returns the two-char module code embedded in a code string.
// Invalid inputs return "99".
func Module(code string) string {
	if len(code) != 10 || code[:4] != "ERR-" {
		return "99"
	}
	return code[4:6]
}
