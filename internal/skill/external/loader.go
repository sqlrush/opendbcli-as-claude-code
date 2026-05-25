/*-------------------------------------------------------------------------
 * loader.go
 *    Discovery and registration manager for external skills.
 *-------------------------------------------------------------------------
 */
package external

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/skill"
)

type Options struct {
	Enabled              bool
	Dirs                 []string
	AllowOverrideBuiltin bool
	MaxTimeout           time.Duration
	MaxOutputBytes       int64
	MaxStderrBytes       int64
	InheritEnv           bool
	EnvAllowlist         []string
	ContextFunc          func() RunContext
}

type Manager struct {
	opts     Options
	infos    map[string]Info
	regs     []registration
	lastErrs []error
}

type registration struct {
	Name   string
	DBType string
	Shared bool
}

func OptionsFromConfig(cfg config.ExternalSkillsConfig, contextFunc func() RunContext) Options {
	return Options{
		Enabled:              cfg.Enabled,
		Dirs:                 cfg.Dirs,
		AllowOverrideBuiltin: cfg.AllowOverrideBuiltin,
		MaxTimeout:           cfg.MaxTimeout,
		MaxOutputBytes:       cfg.MaxOutputBytes,
		MaxStderrBytes:       cfg.MaxStderrBytes,
		InheritEnv:           cfg.InheritEnv,
		EnvAllowlist:         cfg.EnvAllowlist,
		ContextFunc:          contextFunc,
	}
}

func NewManager(opts Options) *Manager {
	return &Manager{opts: opts, infos: make(map[string]Info)}
}

func (m *Manager) Enabled() bool { return m != nil && m.opts.Enabled }

func (m *Manager) DefaultDir() string {
	if m == nil || len(m.opts.Dirs) == 0 {
		return filepath.Join(config.DefaultOpenDBDir(), "skills")
	}
	return expandPath(m.opts.Dirs[0])
}

func (m *Manager) List() []Info {
	if m == nil {
		return nil
	}
	out := make([]Info, 0, len(m.infos))
	for _, info := range m.infos {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) Get(name string) (Info, bool) {
	if m == nil {
		return Info{}, false
	}
	info, ok := m.infos[name]
	return info, ok
}

func (m *Manager) LastErrors() []error {
	if m == nil {
		return nil
	}
	return append([]error(nil), m.lastErrs...)
}

func (m *Manager) Reload(reg *skill.Registry) error {
	if m == nil || !m.opts.Enabled {
		return nil
	}
	loaded, err := m.discover(reg)
	if err != nil {
		m.lastErrs = []error{err}
		return err
	}
	for _, r := range m.regs {
		if r.Shared {
			reg.Unregister(r.Name)
		} else {
			reg.UnregisterForDB(r.DBType, r.Name)
		}
	}
	m.regs = nil
	m.infos = make(map[string]Info)
	for _, sk := range loaded {
		info := sk.Info()
		m.infos[sk.Name()] = info
		for _, dbt := range info.DBTypes {
			if dbt == "all" || dbt == "shared" {
				reg.Register(sk)
				m.regs = append(m.regs, registration{Name: sk.Name(), Shared: true})
				continue
			}
			reg.RegisterForDB(dbt, sk)
			m.regs = append(m.regs, registration{Name: sk.Name(), DBType: dbt})
		}
	}
	m.lastErrs = nil
	return nil
}

func (m *Manager) discover(reg *skill.Registry) ([]*ExternalScriptSkill, error) {
	var out []*ExternalScriptSkill
	seen := map[string]string{}
	oldNames := map[string]bool{}
	for name := range m.infos {
		oldNames[name] = true
	}
	for _, rawDir := range m.opts.Dirs {
		dir := expandPath(rawDir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan external skills dir %q: %w", dir, err)
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			skillDir := filepath.Join(dir, ent.Name())
			manifestPath := findManifest(skillDir)
			if manifestPath == "" {
				continue
			}
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", manifestPath, err)
			}
			mf, err := parseManifest(data, manifestPath, skillDir)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", manifestPath, err)
			}
			if mf.Kind != "script" {
				return nil, fmt.Errorf("%s: kind %q is reserved for the MCP adapter and is not loaded from external_skills.dirs", manifestPath, mf.Kind)
			}
			if prev, ok := seen[mf.Name]; ok {
				return nil, fmt.Errorf("duplicate external skill %q in %s and %s", mf.Name, prev, manifestPath)
			}
			seen[mf.Name] = manifestPath
			if !m.opts.AllowOverrideBuiltin && reg != nil && reg.HasAny(mf.Name) && !oldNames[mf.Name] {
				return nil, fmt.Errorf("external skill %q conflicts with existing built-in skill", mf.Name)
			}
			mf.Hash = sha256Hex(data)
			skill, err := NewExternalScriptSkill(mf, m.opts)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", manifestPath, err)
			}
			out = append(out, skill)
		}
	}
	return out, nil
}

func findManifest(dir string) string {
	for _, name := range []string{"skill.md", "SKILL.md"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	if strings.HasPrefix(p, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[len("$HOME/"):])
		}
	}
	return p
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
