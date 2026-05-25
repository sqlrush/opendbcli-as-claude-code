/*-------------------------------------------------------------------------
 *
 * encrypted.go
 *	  StorableProvider extends Provider with credential storage
 *	  capabilities.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/encrypted.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	nonceSize   = 12              // AES-GCM standard nonce size
	minFileSize = SaltSize + nonceSize + 1 // salt + nonce + at least 1 byte ciphertext
)

// StorableProvider extends Provider with credential storage capabilities.
type StorableProvider interface {
	Provider
	Store(connName string, password string) error
	Delete(connName string) error
}

// EncryptedProvider stores and retrieves credentials encrypted with AES-256-GCM.
// Credentials are stored as individual files in the credentials directory.
// File format: [16-byte salt][12-byte nonce][ciphertext + GCM tag]
type EncryptedProvider struct {
	dir string // directory for encrypted credential files
}

// NewEncryptedProvider creates an EncryptedProvider that stores credentials
// in the given directory. The directory is created on first Store if needed.
func NewEncryptedProvider(dir string) *EncryptedProvider {
	return &EncryptedProvider{dir: dir}
}

// Resolve decrypts and returns the password for the given connection.
func (p *EncryptedProvider) Resolve(connName string) (string, error) {
	path, err := p.filePath(connName)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no saved credential for %q (use /conn to save)", connName)
		}
		return "", fmt.Errorf("reading credential for %q: %w", connName, err)
	}

	if len(data) < minFileSize {
		return "", fmt.Errorf("credential file for %q is corrupted", connName)
	}

	salt := data[:SaltSize]
	nonce := data[SaltSize : SaltSize+nonceSize]
	ciphertext := data[SaltSize+nonceSize:]

	key, err := DeriveKey(salt)
	if err != nil {
		return "", fmt.Errorf("deriving key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting credential for %q: authentication failed (wrong machine or corrupted)", connName)
	}

	return string(plaintext), nil
}

// Store encrypts and saves the password for the given connection.
func (p *EncryptedProvider) Store(connName string, password string) error {
	path, err := p.filePath(connName)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return fmt.Errorf("creating credentials directory: %w", err)
	}

	// Generate per-credential random salt.
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("generating salt: %w", err)
	}

	key, err := DeriveKey(salt)
	if err != nil {
		return fmt.Errorf("deriving key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(password), nil)

	// Write salt + nonce + ciphertext to file.
	data := make([]byte, 0, SaltSize+nonceSize+len(ciphertext))
	data = append(data, salt...)
	data = append(data, nonce...)
	data = append(data, ciphertext...)

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing credential for %q: %w", connName, err)
	}

	return nil
}

// Delete removes the stored credential for the given connection.
func (p *EncryptedProvider) Delete(connName string) error {
	path, err := p.filePath(connName)
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting credential for %q: %w", connName, err)
	}
	return nil
}

// filePath returns the filesystem path for a connection's credential file.
// Validates the connection name to prevent path traversal attacks.
func (p *EncryptedProvider) filePath(connName string) (string, error) {
	if connName == "" || connName == "." || connName == ".." ||
		strings.ContainsAny(connName, "/\\") {
		return "", fmt.Errorf("invalid connection name %q", connName)
	}
	return filepath.Join(p.dir, connName+".enc"), nil
}
