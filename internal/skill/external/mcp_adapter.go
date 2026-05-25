/*-------------------------------------------------------------------------
 * mcp_adapter.go
 *    P1 configuration surface for adapting allowlisted MCP tools as DBAA
 *    skills. Runtime MCP invocation is intentionally left behind this small
 *    interface so it stays opt-in and deny-by-default.
 *-------------------------------------------------------------------------
 */
package external

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
)

type MCPAdapter struct {
	cfg config.MCPConfig
}

type MCPToolInfo struct {
	Server      string
	ToolKey     string
	Name        string
	Description string
	Security    string
	DBTypes     []string
	Triggers    []string
	Status      string
}

func NewMCPAdapter(cfg config.MCPConfig) *MCPAdapter {
	return &MCPAdapter{cfg: cfg}
}

func (a *MCPAdapter) Enabled() bool {
	return a != nil && a.cfg.Enabled
}

func (a *MCPAdapter) Servers() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.cfg.Servers))
	for _, srv := range a.cfg.Servers {
		name := strings.TrimSpace(srv.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (a *MCPAdapter) ToolInfos() []MCPToolInfo {
	if a == nil {
		return nil
	}
	var out []MCPToolInfo
	for _, srv := range a.cfg.Servers {
		for key, entry := range srv.Tools {
			if !entry.Enabled {
				continue
			}
			name := strings.TrimSpace(entry.Name)
			if name == "" {
				name = fmt.Sprintf("mcp_%s_%s", sanitizeName(srv.Name), sanitizeName(key))
			}
			out = append(out, MCPToolInfo{
				Server:      srv.Name,
				ToolKey:     key,
				Name:        name,
				Description: entry.Description,
				Security:    entry.Security,
				DBTypes:     append([]string(nil), entry.DBTypes...),
				Triggers:    append([]string(nil), entry.Triggers...),
				Status:      "configured",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server == out[j].Server {
			return out[i].Name < out[j].Name
		}
		return out[i].Server < out[j].Server
	})
	return out
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}
