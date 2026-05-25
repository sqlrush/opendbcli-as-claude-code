/*-------------------------------------------------------------------------
 * security.go
 *    Validation helpers for external skills.
 *-------------------------------------------------------------------------
 */
package external

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/skill"
)

func parseSecurityLevel(s string) (skill.SecurityLevel, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	switch s {
	case "read_only", "readonly", "read":
		return skill.LevelReadOnly, nil
	case "operator", "operate":
		return skill.LevelOperator, nil
	case "admin":
		return skill.LevelAdmin, nil
	case "dangerous":
		return skill.LevelDangerous, nil
	default:
		return skill.LevelReadOnly, fmt.Errorf("invalid security %q; use read_only/operator/admin", s)
	}
}

func clampTimeout(v, max time.Duration) time.Duration {
	if v <= 0 {
		v = 30 * time.Second
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

func resolveCommand(dir string, spec CommandSpec) (string, []string, error) {
	if len(spec) == 0 {
		return "", nil, fmt.Errorf("command is required")
	}
	cmd := strings.TrimSpace(spec[0])
	if cmd == "" {
		return "", nil, fmt.Errorf("command is empty")
	}
	args := make([]string, 0, len(spec)-1)
	for _, arg := range spec[1:] {
		args = append(args, arg)
	}
	if strings.ContainsRune(cmd, '/') || strings.HasPrefix(cmd, ".") {
		abs, err := safePathInDir(dir, cmd)
		if err != nil {
			return "", nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return "", nil, fmt.Errorf("command %q not accessible: %w", cmd, err)
		}
		if st.IsDir() {
			return "", nil, fmt.Errorf("command %q is a directory", cmd)
		}
		if st.Mode()&0111 == 0 {
			return "", nil, fmt.Errorf("command %q is not executable", cmd)
		}
		cmd = abs
	}
	for _, arg := range args {
		if err := validateStaticCommandArg(dir, arg); err != nil {
			return "", nil, err
		}
	}
	return cmd, args, nil
}

func validateStaticCommandArg(dir, arg string) error {
	if strings.Contains(arg, "\x00") {
		return fmt.Errorf("command argument contains NUL byte")
	}
	pathPart := arg
	if eq := strings.IndexByte(arg, '='); eq >= 0 {
		pathPart = arg[eq+1:]
	}
	if hasParentPathSegment(pathPart) {
		return fmt.Errorf("command argument %q escapes skill directory", arg)
	}
	if isPathish(pathPart) {
		if _, err := safePathInDir(dir, pathPart); err != nil {
			return fmt.Errorf("command argument %q invalid: %w", arg, err)
		}
	}
	return nil
}

func isPathish(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.Contains(s, "/") || strings.Contains(s, "\\") || filepath.IsAbs(s)
}

func hasParentPathSegment(s string) bool {
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func safePathInDir(dir, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q is not allowed", rel)
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(base, rel))
	if err != nil {
		return "", err
	}
	baseEval, _ := filepath.EvalSymlinks(base)
	candEval, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		candEval = candidate
	}
	if baseEval == "" {
		baseEval = base
	}
	baseWithSep := strings.TrimRight(baseEval, string(filepath.Separator)) + string(filepath.Separator)
	if candEval != baseEval && !strings.HasPrefix(candEval, baseWithSep) {
		return "", fmt.Errorf("path %q escapes skill directory", rel)
	}
	return candEval, nil
}
