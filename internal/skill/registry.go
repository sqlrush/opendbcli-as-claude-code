/*-------------------------------------------------------------------------
 *
 * registry.go
 *	  Registry holds registered skills and supports lookup and prefix
 *	  matching. Skills can be registered as shared (database-agnostic)
 *	  or per-DBType. When an active DB is set, Get/All/Match return
 *	  active DB skills first, falling back to shared skills.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/registry.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import (
	"sort"
	"strings"
	"sync"
)

// skillGroup holds skills and aliases for a single scope (shared or per-DB).
type skillGroup struct {
	skills  map[string]Skill
	aliases map[string]string // alias -> canonical name
}

func newSkillGroup() *skillGroup {
	return &skillGroup{
		skills:  make(map[string]Skill),
		aliases: make(map[string]string),
	}
}

func (g *skillGroup) add(s Skill) {
	name := s.Name()
	g.skills[name] = s
	for _, alias := range s.CLIDef().Aliases {
		g.aliases[alias] = name
	}
}

func (g *skillGroup) get(name string) (Skill, bool) {
	if s, ok := g.skills[name]; ok {
		return s, true
	}
	if canonical, ok := g.aliases[name]; ok {
		return g.skills[canonical], true
	}
	return nil, false
}

func (g *skillGroup) remove(name string) {
	delete(g.skills, name)
	for alias, canonical := range g.aliases {
		if canonical == name {
			delete(g.aliases, alias)
		}
	}
}

// Registry holds registered skills and supports lookup and prefix matching.
// Skills can be registered as shared (database-agnostic) or per-DBType.
// When an active DB is set, Get/All/Match return active DB skills first,
// falling back to shared skills.
type Registry struct {
	mu       sync.RWMutex
	shared   *skillGroup            // shared skills (help, clear, login, etc.)
	products map[string]*skillGroup // dbType -> skill group
	activeDB string                 // current active database type
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		shared:   newSkillGroup(),
		products: make(map[string]*skillGroup),
	}
}

// Register adds a shared (database-agnostic) skill to the registry.
func (r *Registry) Register(s Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shared.add(s)
}

// RegisterForDB adds a skill scoped to a specific database type.
func (r *Registry) RegisterForDB(dbType string, s Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.products[dbType]
	if !ok {
		g = newSkillGroup()
		r.products[dbType] = g
	}
	g.add(s)
}

// HasAny reports whether a skill name or alias exists in any scope. It is used
// by external skill loaders to prevent customer-provided skills from silently
// overriding built-in skills.
func (r *Registry) HasAny(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.shared.get(name); ok {
		return true
	}
	for _, g := range r.products {
		if _, ok := g.get(name); ok {
			return true
		}
	}
	return false
}

// Unregister removes a shared skill by canonical name. It also removes aliases
// pointing to the same skill.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shared.remove(name)
}

// UnregisterForDB removes a product-scoped skill by canonical name. It also
// removes aliases pointing to the same skill.
func (r *Registry) UnregisterForDB(dbType, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.products[dbType]; ok {
		g.remove(name)
	}
}

// SetActiveDB sets the active database type for skill resolution.
func (r *Registry) SetActiveDB(dbType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeDB = dbType
}

// ActiveDB returns the current active database type.
func (r *Registry) ActiveDB() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.activeDB
}

// Get retrieves a skill by exact name or alias. It checks the active DB's
// skill group first, then falls back to shared skills.
func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.activeDB != "" {
		if g, ok := r.products[r.activeDB]; ok {
			if s, found := g.get(name); found {
				return s, true
			}
		}
	}
	return r.shared.get(name)
}

// Match returns all skills whose name or alias starts with the given prefix
// (case-insensitive). Results are deduplicated and sorted by name.
func (r *Registry) Match(prefix string) []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lowerPrefix := strings.ToLower(prefix)
	seen := make(map[string]bool)
	var matched []Skill

	// Helper to match within a skill group.
	matchGroup := func(g *skillGroup) {
		for name, s := range g.skills {
			if seen[name] {
				continue
			}
			if strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
				seen[name] = true
				matched = append(matched, s)
			}
		}
		for alias, canonical := range g.aliases {
			if seen[canonical] {
				continue
			}
			if strings.HasPrefix(strings.ToLower(alias), lowerPrefix) {
				seen[canonical] = true
				if s, ok := g.skills[canonical]; ok {
					matched = append(matched, s)
				}
			}
		}
	}

	// Active DB skills first.
	if r.activeDB != "" {
		if g, ok := r.products[r.activeDB]; ok {
			matchGroup(g)
		}
	}
	// Then shared skills.
	matchGroup(r.shared)

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Name() < matched[j].Name()
	})
	return matched
}

// All returns every visible skill (active DB + shared) sorted by name.
func (r *Registry) All() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	var result []Skill

	// Active DB skills first.
	if r.activeDB != "" {
		if g, ok := r.products[r.activeDB]; ok {
			for name, s := range g.skills {
				seen[name] = true
				result = append(result, s)
			}
		}
	}
	// Then shared skills (skip if overridden by active DB).
	for name, s := range r.shared.skills {
		if !seen[name] {
			result = append(result, s)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}
