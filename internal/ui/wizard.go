/*-------------------------------------------------------------------------
 *
 * wizard.go
 *	  Wizard guides users through first-time setup via stdin/stdout
 *	  prompts.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/wizard.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	defaultPort        = 1521
	defaultCredProvider = "prompt"
	connectionsSubdir  = "connections"
	configFileName     = "config.yaml"
	defaultConnFile    = "default.yaml"
)

// Wizard guides users through first-time setup via stdin/stdout prompts.
type Wizard struct {
	reader  io.Reader
	writer  io.Writer
	baseDir string
}

// NewWizard creates a Wizard that reads from reader, writes to writer, and
// persists configuration under baseDir (e.g., ~/.opendb).
func NewWizard(reader io.Reader, writer io.Writer, baseDir string) *Wizard {
	return &Wizard{
		reader:  reader,
		writer:  writer,
		baseDir: baseDir,
	}
}

// Run executes the interactive wizard flow. It returns the created Config and
// Connection, or an error if the setup fails.
func (w *Wizard) Run() (*config.Config, *config.Connection, error) {
	scanner := bufio.NewScanner(w.reader)

	w.printWelcome()

	// Step 1: Database type (Oracle only for now).
	w.writeLine("")
	w.writeLine("Database type: Oracle (auto-selected)")

	// Step 2: Connection name.
	name, err := w.promptRequired(scanner, "Connection name (e.g., prod-db1)")
	if err != nil {
		return nil, nil, fmt.Errorf("reading connection name: %w", err)
	}

	// Step 3: Host.
	host, err := w.promptRequired(scanner, "Host (e.g., 192.168.1.100)")
	if err != nil {
		return nil, nil, fmt.Errorf("reading host: %w", err)
	}

	// Step 4: Port with default.
	port, err := w.promptPort(scanner)
	if err != nil {
		return nil, nil, fmt.Errorf("reading port: %w", err)
	}

	// Step 5: Service name.
	service, err := w.promptRequired(scanner, "Service name (e.g., ORCL)")
	if err != nil {
		return nil, nil, fmt.Errorf("reading service name: %w", err)
	}

	// Step 6: Username.
	user, err := w.promptRequired(scanner, "Username (e.g., system)")
	if err != nil {
		return nil, nil, fmt.Errorf("reading username: %w", err)
	}

	// Step 7: Password strategy (prompt for MVP).
	w.writeLine("")
	w.writeLine("Password strategy: prompt (password will be asked at each connection)")

	// Build immutable config and connection.
	cfg := w.buildConfig()
	conn := w.buildConnection(name, host, port, service, user)

	// Persist files.
	if err := w.saveConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("saving config: %w", err)
	}
	if err := w.saveConnection(conn); err != nil {
		return nil, nil, fmt.Errorf("saving connection: %w", err)
	}

	w.printSuccess(name)

	return &cfg, &conn, nil
}

// promptRequired displays a prompt and reads a non-empty line from the scanner.
func (w *Wizard) promptRequired(scanner *bufio.Scanner, label string) (string, error) {
	w.writePrompt(label, "")

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected end of input for %q", label)
	}

	value := strings.TrimSpace(scanner.Text())
	if value == "" {
		return "", fmt.Errorf("%s cannot be empty", label)
	}
	return value, nil
}

// promptPort asks for the port number, accepting an empty input as the default.
func (w *Wizard) promptPort(scanner *bufio.Scanner) (int, error) {
	w.writePrompt("Port", strconv.Itoa(defaultPort))

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("unexpected end of input for port")
	}

	text := strings.TrimSpace(scanner.Text())
	if text == "" {
		return defaultPort, nil
	}

	port, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("invalid port number %q: %w", text, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return port, nil
}

// writePrompt prints a labelled prompt. If defaultVal is non-empty, it is
// shown in brackets.
func (w *Wizard) writePrompt(label, defaultVal string) {
	if defaultVal != "" {
		fmt.Fprintf(w.writer, "  %s [%s]: ", label, defaultVal)
	} else {
		fmt.Fprintf(w.writer, "  %s: ", label)
	}
}

// writeLine writes a line of text followed by a newline.
func (w *Wizard) writeLine(s string) {
	fmt.Fprintln(w.writer, s)
}

// printWelcome prints the welcome banner.
func (w *Wizard) printWelcome() {
	w.writeLine("===========================================")
	w.writeLine("  Welcome to OpenDB - First Time Setup")
	w.writeLine("===========================================")
	w.writeLine("")
	w.writeLine("This wizard will help you configure your first database connection.")
}

// printSuccess prints the completion message.
func (w *Wizard) printSuccess(connName string) {
	w.writeLine("")
	w.writeLine("===========================================")
	w.writeLine("  Setup complete!")
	w.writeLine("===========================================")
	w.writeLine("")
	w.writeLine(fmt.Sprintf("  Config saved to:     %s", filepath.Join(w.baseDir, configFileName)))
	w.writeLine(fmt.Sprintf("  Connection saved to: %s", filepath.Join(w.baseDir, connectionsSubdir, defaultConnFile)))
	w.writeLine(fmt.Sprintf("  Connection name:     %s", connName))
	w.writeLine("")
	w.writeLine("You can add more connections by creating YAML files in the connections directory.")
}

// buildConfig creates a Config based on the wizard's base directory.
func (w *Wizard) buildConfig() config.Config {
	return config.Config{
		ConnectionsDir: filepath.Join(w.baseDir, connectionsSubdir),
		Security: config.SecurityConfig{
			DefaultLevel:       0,
			ConfirmOnDangerous: true,
		},
		Output: config.OutputConfig{
			Format:  "terminal",
			MaxRows: 1000,
		},
		LLM: config.LLMConfig{
			Provider: "none",
		},
		Session: config.SessionConfig{
			RestoreOnSwitch: true,
			HistoryDir:      filepath.Join(w.baseDir, "history"),
		},
	}
}

// buildConnection creates a Connection from the collected inputs.
func (w *Wizard) buildConnection(name, host string, port int, service, user string) config.Connection {
	return config.Connection{
		Name:    name,
		Host:    host,
		Port:    port,
		Service: service,
		User:    user,
		Credential: config.Credential{
			Provider: defaultCredProvider,
		},
	}
}

// saveConfig marshals the config to YAML and writes it to baseDir/config.yaml.
func (w *Wizard) saveConfig(cfg config.Config) error {
	if err := os.MkdirAll(w.baseDir, 0o750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	path := filepath.Join(w.baseDir, configFileName)
	return os.WriteFile(path, data, 0o640)
}

// saveConnection marshals the connection as a ConnectionGroup and writes it
// to baseDir/connections/default.yaml.
func (w *Wizard) saveConnection(conn config.Connection) error {
	connDir := filepath.Join(w.baseDir, connectionsSubdir)
	if err := os.MkdirAll(connDir, 0o750); err != nil {
		return fmt.Errorf("creating connections directory: %w", err)
	}

	group := config.ConnectionGroup{
		Group:       "default",
		Tags:        []string{"setup-wizard"},
		Connections: []config.Connection{conn},
	}

	data, err := yaml.Marshal(group)
	if err != nil {
		return fmt.Errorf("marshaling connection: %w", err)
	}

	path := filepath.Join(connDir, defaultConnFile)
	return os.WriteFile(path, data, 0o640)
}
