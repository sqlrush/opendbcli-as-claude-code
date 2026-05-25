/*-------------------------------------------------------------------------
 *
 * token.go
 *	  Package cluster implements cluster management for OpenDB
 *	  Autopilot. Handles cluster init/join/deploy/upgrade, agent
 *	  registration, and topology discovery.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/token.go
 *
 *-------------------------------------------------------------------------
 */
// Package cluster implements cluster management for OpenDB Autopilot.
// Handles cluster init/join/deploy/upgrade, agent registration, and topology discovery.
package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Token represents a join token with expiry.
type Token struct {
	Value     string    `yaml:"value"`
	CreatedAt time.Time `yaml:"created_at"`
	ExpiresAt time.Time `yaml:"expires_at"`
}

// DefaultTokenExpiry is the default token validity period.
const DefaultTokenExpiry = 24 * time.Hour

// GenerateToken creates a new random join token.
func GenerateToken() (Token, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Token{}, fmt.Errorf("generate random bytes: %w", err)
	}

	now := time.Now()
	return Token{
		Value:     hex.EncodeToString(b),
		CreatedAt: now,
		ExpiresAt: now.Add(DefaultTokenExpiry),
	}, nil
}

// IsValid checks if the token has not expired.
func (t Token) IsValid() bool {
	return time.Now().Before(t.ExpiresAt)
}

// String returns the token value.
func (t Token) String() string {
	return t.Value
}

// SaveToken writes the token to the cluster config directory.
func SaveToken(baseDir string, token Token) error {
	dir := filepath.Join(baseDir, "cluster")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cluster dir: %w", err)
	}

	content := fmt.Sprintf("%s|%s|%s",
		token.Value,
		token.CreatedAt.Format(time.RFC3339),
		token.ExpiresAt.Format(time.RFC3339),
	)
	return os.WriteFile(filepath.Join(dir, "join-token"), []byte(content), 0600)
}

// LoadToken reads the token from the cluster config directory.
func LoadToken(baseDir string) (Token, error) {
	data, err := os.ReadFile(filepath.Join(baseDir, "cluster", "join-token"))
	if err != nil {
		return Token{}, fmt.Errorf("read token: %w", err)
	}

	parts := strings.SplitN(string(data), "|", 3)
	if len(parts) != 3 {
		return Token{}, fmt.Errorf("invalid token format")
	}

	createdAt, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return Token{}, fmt.Errorf("parse created_at: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, parts[2])
	if err != nil {
		return Token{}, fmt.Errorf("parse expires_at: %w", err)
	}

	return Token{
		Value:     parts[0],
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}
