/*-------------------------------------------------------------------------
 *
 * keyring.go
 *	  DeriveKey derives a 32-byte AES key from machine-specific data and
 *	  a per-credential random salt. Each credential gets its own salt
 *	  stored alongside the ciphertext, preventing rainbow-table attacks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/keyring.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	keyLength  = 32     // AES-256
	pbkdf2Iter = 100000 // OWASP recommendation
	SaltSize   = 16     // per-credential random salt
)

// DeriveKey derives a 32-byte AES key from machine-specific data and a
// per-credential random salt. Each credential gets its own salt stored
// alongside the ciphertext, preventing rainbow-table attacks.
func DeriveKey(salt []byte) ([]byte, error) {
	machineID, err := getMachineID()
	if err != nil {
		return nil, fmt.Errorf("getting machine ID: %w", err)
	}

	username, err := getUsername()
	if err != nil {
		return nil, fmt.Errorf("getting username: %w", err)
	}

	// Combine machine ID and username as the password material.
	material := machineID + ":" + username

	key := pbkdf2.Key([]byte(material), salt, pbkdf2Iter, keyLength, sha256.New)
	return key, nil
}

// getMachineID returns a machine-unique identifier.
func getMachineID() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return readLinuxMachineID()
	case "darwin":
		return readDarwinMachineID()
	default:
		return readFallbackMachineID()
	}
}

// readLinuxMachineID reads /etc/machine-id (systemd standard).
func readLinuxMachineID() (string, error) {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		data, err := os.ReadFile(path)
		if err == nil {
			id := strings.TrimSpace(string(data))
			if id != "" {
				return id, nil
			}
		}
	}
	return readFallbackMachineID()
}

// readDarwinMachineID reads the hardware UUID on macOS via ioreg.
func readDarwinMachineID() (string, error) {
	// Try IOPlatformUUID via ioreg output parsed from kern.uuid sysctl file.
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	// Fallback: hash /var/db/SystemKey if available (stable across reboots).
	data, err = os.ReadFile("/var/db/SystemKey")
	if err == nil && len(data) > 0 {
		h := sha256.Sum256(data)
		return fmt.Sprintf("%x", h[:16]), nil
	}

	// Final fallback: hostname-based.
	return readFallbackMachineID()
}

// readFallbackMachineID uses hostname as a last resort.
func readFallbackMachineID() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("cannot determine machine identity: %w", err)
	}
	h := sha256.Sum256([]byte("opendb-fallback:" + hostname))
	return fmt.Sprintf("%x", h[:16]), nil
}

// getUsername returns the current OS username.
func getUsername() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
