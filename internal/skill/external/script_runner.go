/*-------------------------------------------------------------------------
 * script_runner.go
 *    Bounded script execution for external DBAA skills.
 *-------------------------------------------------------------------------
 */
package external

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type RunContext struct {
	DBType       string `json:"db_type,omitempty"`
	Connection   string `json:"connection,omitempty"`
	Database     string `json:"database,omitempty"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	User         string `json:"user,omitempty"`
	UserQuestion string `json:"user_question,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
}

type scriptRequest struct {
	APIVersion string         `json:"api_version"`
	Skill      string         `json:"skill"`
	Params     map[string]any `json:"params"`
	Context    RunContext     `json:"context"`
}

type RunnerConfig struct {
	MaxOutputBytes int64
	MaxStderrBytes int64
	InheritEnv     bool
	EnvAllowlist   []string
}

type runnerResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Elapsed  time.Duration
}

func runScript(ctx context.Context, m *Manifest, params map[string]any, runCtx RunContext, cfg RunnerConfig) (runnerResult, error) {
	cmdPath, args, err := resolveCommand(m.Dir, m.Command)
	if err != nil {
		return runnerResult{}, err
	}
	payload, err := json.Marshal(scriptRequest{APIVersion: APIVersion, Skill: m.Name, Params: params, Context: runCtx})
	if err != nil {
		return runnerResult{}, err
	}
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	cmd.Dir = m.Dir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = buildEnv(cfg)

	stdout := &limitBuffer{limit: positiveOr(cfg.MaxOutputBytes, 256*1024)}
	stderr := &limitBuffer{limit: positiveOr(cfg.MaxStderrBytes, 64*1024)}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err = cmd.Run()
	res := runnerResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode(err), Elapsed: time.Since(start)}
	if stdout.truncated {
		res.Stdout += "\n... (stdout truncated by DBAA external skill output limit)"
	}
	if stderr.truncated {
		res.Stderr += "\n... (stderr truncated by DBAA external skill stderr limit)"
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("external skill %s timed out or was cancelled: %w", m.Name, ctx.Err())
	}
	if err != nil {
		if strings.TrimSpace(res.Stderr) != "" {
			return res, fmt.Errorf("external skill %s failed with exit code %d: %s", m.Name, res.ExitCode, strings.TrimSpace(res.Stderr))
		}
		return res, fmt.Errorf("external skill %s failed with exit code %d", m.Name, res.ExitCode)
	}
	return res, nil
}

func buildEnv(cfg RunnerConfig) []string {
	if cfg.InheritEnv {
		return os.Environ()
	}
	allowed := map[string]bool{"PATH": true, "LANG": true}
	for _, key := range cfg.EnvAllowlist {
		key = strings.TrimSpace(key)
		if key != "" {
			allowed[key] = true
		}
	}
	out := make([]string, 0, len(allowed))
	for key := range allowed {
		if val, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+val)
		}
	}
	return out
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func positiveOr(v, fallback int64) int64 {
	if v <= 0 {
		return fallback
	}
	return v
}

type limitBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitBuffer) String() string { return b.buf.String() }
