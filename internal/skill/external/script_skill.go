/*-------------------------------------------------------------------------
 * script_skill.go
 *    ExternalScriptSkill implements skill.Skill around a customer script.
 *-------------------------------------------------------------------------
 */
package external

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/skill"
)

type SourceKind string

const (
	SourceScript SourceKind = "script"
	SourceMCP    SourceKind = "mcp"
)

type Info struct {
	Name         string
	Title        string
	Description  string
	Kind         SourceKind
	DBTypes      []string
	Security     string
	Timeout      time.Duration
	Path         string
	Dir          string
	Command      []string
	Triggers     []string
	Tags         []string
	Status       string
	Error        string
	ManifestHash string
}

type ExternalScriptSkill struct {
	manifest *Manifest
	level    skill.SecurityLevel
	timeout  time.Duration
	runner   RunnerConfig
	runCtx   func() RunContext
}

func NewExternalScriptSkill(m *Manifest, opts Options) (*ExternalScriptSkill, error) {
	level, err := parseSecurityLevel(m.Security)
	if err != nil {
		return nil, err
	}
	if _, _, err := resolveCommand(m.Dir, m.Command); err != nil {
		return nil, err
	}
	return &ExternalScriptSkill{
		manifest: m,
		level:    level,
		timeout:  clampTimeout(time.Duration(m.Timeout), opts.MaxTimeout),
		runner: RunnerConfig{
			MaxOutputBytes: opts.MaxOutputBytes,
			MaxStderrBytes: opts.MaxStderrBytes,
			InheritEnv:     opts.InheritEnv,
			EnvAllowlist:   opts.EnvAllowlist,
		},
		runCtx: opts.ContextFunc,
	}, nil
}

func (s *ExternalScriptSkill) Name() string { return s.manifest.Name }

func (s *ExternalScriptSkill) Description() string { return s.manifest.Description }

func (s *ExternalScriptSkill) SecurityLevel() skill.SecurityLevel { return s.level }

func (s *ExternalScriptSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: s.Name(), Description: s.toolDescription(), Parameters: s.manifest.Parameters}
}

func (s *ExternalScriptSkill) toolDescription() string {
	parts := []string{s.Description()}
	if len(s.manifest.Triggers) > 0 {
		parts = append(parts, "Use when the user asks about: "+strings.Join(s.manifest.Triggers, ", "))
	}
	if body := strings.TrimSpace(s.manifest.Body); body != "" {
		parts = append(parts, truncateForToolDescription(body, 800))
	}
	return strings.Join(parts, "\n\n")
}

func truncateForToolDescription(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-3]) + "..."
}

func (s *ExternalScriptSkill) CLIDef() skill.CLIDef {
	usage := "/" + s.Name() + " [json-args]"
	examples := []string{"/" + s.Name()}
	return skill.CLIDef{Command: s.Name(), Usage: usage, Description: s.manifest.Body, Examples: examples}
}

func (s *ExternalScriptSkill) Validate(params skill.Params) error {
	return validateParamsSchema(s.manifest.Parameters, params.Raw())
}

func (s *ExternalScriptSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	runCtx := RunContext{}
	if s.runCtx != nil {
		runCtx = s.runCtx()
	}
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	res, err := runScript(callCtx, s.manifest, params.Raw(), runCtx, s.runner)
	if err != nil {
		return nil, err
	}
	out := resultFromStdout(res.Stdout)
	if out.Metadata == nil {
		out.Metadata = make(map[string]string)
	}
	out.Metadata["source"] = "external_script"
	out.Metadata["skill_path"] = s.manifest.Path
	out.Metadata["elapsed"] = res.Elapsed.String()
	if strings.TrimSpace(res.Stderr) != "" {
		out.Metadata["stderr"] = firstLine(res.Stderr)
	}
	return out, nil
}

func (s *ExternalScriptSkill) Info() Info {
	return Info{Name: s.Name(), Title: s.manifest.Title, Description: s.Description(), Kind: SourceScript, DBTypes: append([]string(nil), s.manifest.DBTypes...), Security: s.manifest.Security, Timeout: s.timeout, Path: s.manifest.Path, Dir: s.manifest.Dir, Command: append([]string(nil), s.manifest.Command...), Triggers: append([]string(nil), s.manifest.Triggers...), Tags: append([]string(nil), s.manifest.Tags...), Status: "ok", ManifestHash: s.manifest.Hash}
}

func validateParamsSchema(schema map[string]any, raw map[string]any) error {
	required := requiredParams(schema)
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			return fmt.Errorf("missing required parameter %q", key)
		}
	}
	props, _ := schema["properties"].(map[string]any)
	for key, val := range raw {
		spec, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := spec["type"].(string)
		if typ == "" {
			continue
		}
		if !matchesJSONType(typ, val) {
			return fmt.Errorf("parameter %q should be %s", key, typ)
		}
	}
	return nil
}

func requiredParams(schema map[string]any) []string {
	var out []string
	switch v := schema["required"].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}

func matchesJSONType(typ string, val any) bool {
	switch strings.ToLower(typ) {
	case "string":
		_, ok := val.(string)
		return ok
	case "integer":
		switch val.(type) {
		case int, int64, int32, float64, float32:
			return true
		}
		return false
	case "number":
		switch val.(type) {
		case int, int64, int32, float64, float32:
			return true
		}
		return false
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "object":
		_, ok := val.(map[string]any)
		return ok
	case "array":
		switch val.(type) {
		case []any, []string:
			return true
		}
		return false
	default:
		return true
	}
}
