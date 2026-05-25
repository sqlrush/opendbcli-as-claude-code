/*-------------------------------------------------------------------------
 *
 * manifest.go
 *    Manifest parser for customer-provided DBAA external skills.
 *
 *-------------------------------------------------------------------------
 */
package external

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const APIVersion = "opendb.skill/v1"

var validSkillName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{1,63}$`)

// Manifest is the YAML frontmatter in skill.md / SKILL.md.
type Manifest struct {
	APIVersion  string         `yaml:"api_version"`
	Name        string         `yaml:"name"`
	Title       string         `yaml:"title"`
	Description string         `yaml:"description"`
	Kind        string         `yaml:"kind"`
	DBTypes     []string       `yaml:"db_types"`
	Security    string         `yaml:"security"`
	Timeout     SkillDuration  `yaml:"timeout"`
	Command     CommandSpec    `yaml:"command"`
	Parameters  map[string]any `yaml:"parameters"`
	Triggers    []string       `yaml:"triggers"`
	Tags        []string       `yaml:"tags"`
	Requires    []string       `yaml:"requires"`
	Body        string         `yaml:"-"`
	Path        string         `yaml:"-"`
	Dir         string         `yaml:"-"`
	Hash        string         `yaml:"-"`
}

// SkillDuration accepts either `30s` or an integer nanosecond value in skill.md.
type SkillDuration time.Duration

func (d *SkillDuration) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || strings.TrimSpace(value.Value) == "" {
		*d = 0
		return nil
	}
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("timeout must be a scalar duration like 30s")
	}
	s := strings.TrimSpace(value.Value)
	var dur time.Duration
	if parsed, err := time.ParseDuration(s); err == nil {
		dur = parsed
	} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		dur = time.Duration(n)
	} else {
		return fmt.Errorf("timeout must be a duration like 30s or integer nanoseconds")
	}
	*d = SkillDuration(dur)
	return nil
}

// CommandSpec accepts either `command: ./run.sh` or a YAML array.
type CommandSpec []string

func (c *CommandSpec) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*c = nil
			return nil
		}
		parts, err := splitCommandLine(s)
		if err != nil {
			return err
		}
		*c = parts
		return nil
	case yaml.SequenceNode:
		var out []string
		if err := value.Decode(&out); err != nil {
			return err
		}
		*c = out
		return nil
	default:
		return fmt.Errorf("command must be a string or string array")
	}
}

func splitCommandLine(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("command has unterminated quote")
	}
	flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("command is empty")
	}
	return out, nil
}

func parseManifest(data []byte, path, dir string) (*Manifest, error) {
	front, body, ok := splitFrontmatter(data)
	if !ok {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}
	var m Manifest
	if err := yaml.Unmarshal(front, &m); err != nil {
		return nil, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	m.Body = strings.TrimSpace(string(body))
	m.Path = path
	m.Dir = dir
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func splitFrontmatter(data []byte) (front, body []byte, ok bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !bytes.HasPrefix(data, []byte("---\n")) && !bytes.HasPrefix(data, []byte("---\r\n")) {
		return nil, nil, false
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) < 3 {
		return nil, nil, false
	}
	var pos int
	pos += len(lines[0])
	for i := 1; i < len(lines); i++ {
		trim := strings.TrimSpace(string(lines[i]))
		if trim == "---" {
			front = data[len(lines[0]):pos]
			body = data[pos+len(lines[i]):]
			return front, body, true
		}
		pos += len(lines[i])
	}
	return nil, nil, false
}

func normalizeDBType(dbt string) string {
	dbt = strings.ToLower(strings.TrimSpace(dbt))
	switch dbt {
	case "*", "any":
		return "all"
	default:
		return dbt
	}
}

func (m *Manifest) Validate() error {
	m.APIVersion = strings.TrimSpace(m.APIVersion)
	if m.APIVersion == "" {
		m.APIVersion = APIVersion
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("unsupported api_version %q", m.APIVersion)
	}
	m.Name = strings.TrimSpace(m.Name)
	if !validSkillName.MatchString(m.Name) {
		return fmt.Errorf("invalid skill name %q: use ^[A-Za-z][A-Za-z0-9_]{1,63}$", m.Name)
	}
	m.Kind = strings.ToLower(strings.TrimSpace(m.Kind))
	if m.Kind == "" {
		m.Kind = "script"
	}
	if m.Kind != "script" && m.Kind != "mcp" {
		return fmt.Errorf("unsupported skill kind %q", m.Kind)
	}
	m.Description = strings.TrimSpace(m.Description)
	if m.Description == "" {
		return fmt.Errorf("description is required")
	}
	m.Security = strings.ToLower(strings.TrimSpace(m.Security))
	if _, err := parseSecurityLevel(m.Security); err != nil {
		return err
	}
	if len(m.DBTypes) == 0 {
		return fmt.Errorf("db_types is required")
	}
	for i, dbt := range m.DBTypes {
		m.DBTypes[i] = normalizeDBType(dbt)
		if m.DBTypes[i] == "" {
			return fmt.Errorf("db_types contains empty value")
		}
	}
	if m.Parameters == nil {
		m.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if m.Kind == "script" && len(m.Command) == 0 {
		return fmt.Errorf("command is required for script skill")
	}
	return nil
}
