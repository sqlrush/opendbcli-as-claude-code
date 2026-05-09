/*-------------------------------------------------------------------------
 *
 * params.go
 *	  params — Params plus helpers (ParamsFromJSON and ParamsFromMap)
 *	  used by the skill package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/params.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import (
	"encoding/json"
	"fmt"
)

type Params struct {
	raw map[string]any
}

func ParamsFromJSON(data []byte) (Params, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Params{}, fmt.Errorf("invalid JSON params: %w", err)
	}
	return Params{raw: raw}, nil
}

func ParamsFromMap(m map[string]any) Params {
	return Params{raw: m}
}

func (p Params) String(key string) (string, error) {
	v, ok := p.raw[key]
	if !ok {
		return "", fmt.Errorf("param %q not found", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("param %q is not a string", key)
	}
	return s, nil
}

func (p Params) StringOr(key, fallback string) string {
	s, err := p.String(key)
	if err != nil {
		return fallback
	}
	return s
}

func (p Params) Int(key string) (int, error) {
	v, ok := p.raw[key]
	if !ok {
		return 0, fmt.Errorf("param %q not found", key)
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, fmt.Errorf("param %q is not a number", key)
	}
}

func (p Params) IntOr(key string, fallback int) int {
	n, err := p.Int(key)
	if err != nil {
		return fallback
	}
	return n
}

func (p Params) Bool(key string) (bool, error) {
	v, ok := p.raw[key]
	if !ok {
		return false, fmt.Errorf("param %q not found", key)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("param %q is not a bool", key)
	}
	return b, nil
}

func (p Params) BoolOr(key string, fallback bool) bool {
	b, err := p.Bool(key)
	if err != nil {
		return fallback
	}
	return b
}

func (p Params) Has(key string) bool {
	_, ok := p.raw[key]
	return ok
}

// With returns a new Params with the given key set to value.
func (p Params) With(key string, value any) Params {
	cp := p.Raw()
	cp[key] = value
	return Params{raw: cp}
}

func (p Params) Raw() map[string]any {
	cp := make(map[string]any, len(p.raw))
	for k, v := range p.raw {
		cp[k] = v
	}
	return cp
}
