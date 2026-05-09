/*-------------------------------------------------------------------------
 *
 * prompt.go
 *	  PromptProvider reads a password interactively from the terminal.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/credential/prompt.go
 *
 *-------------------------------------------------------------------------
 */
package credential

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// PromptProvider reads a password interactively from the terminal.
type PromptProvider struct {
	// reader is the file descriptor to read from. Defaults to os.Stdin.
	reader *os.File
	// writer is where prompts are written. Defaults to os.Stderr.
	writer io.Writer
}

// PromptOption configures a PromptProvider.
type PromptOption func(*PromptProvider)

// WithReader sets the file to read the password from.
func WithReader(r *os.File) PromptOption {
	return func(p *PromptProvider) {
		p.reader = r
	}
}

// WithWriter sets the writer for prompt output.
func WithWriter(w io.Writer) PromptOption {
	return func(p *PromptProvider) {
		p.writer = w
	}
}

// NewPromptProvider creates a PromptProvider with the given options.
// By default it reads from os.Stdin and writes prompts to os.Stderr.
func NewPromptProvider(opts ...PromptOption) *PromptProvider {
	p := &PromptProvider{
		reader: os.Stdin,
		writer: os.Stderr,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Resolve prompts the user for a password on the terminal.
// The password input is masked (not echoed).
func (p *PromptProvider) Resolve(connName string) (string, error) {
	fmt.Fprintf(p.writer, "Password for %s: ", connName)

	password, err := term.ReadPassword(int(p.reader.Fd()))
	if err != nil {
		return "", fmt.Errorf("reading password for %q: %w", connName, err)
	}

	// Print newline after masked input.
	fmt.Fprintln(p.writer)

	return string(password), nil
}
