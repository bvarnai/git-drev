// Package command provides low-level subprocess execution primitives,
// environment sanitization, and structured command exit error representation.
package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// DefaultWaitDelay is the wait duration allowed for child process I/O completion after SIGTERM.
const DefaultWaitDelay = 3 * time.Second

// New creates a context-bound *exec.Cmd pre-configured with sanitized
// subprocess environment variables, a 3-second wait delay, and graceful SIGTERM cancellation.
func New(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = SanitizedEnv(nil)
	cmd.WaitDelay = DefaultWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}

// SanitizedEnv constructs a child process environment from baseEnv (or os.Environ()
// if baseEnv is nil), applies extraEnv (with last-wins deduplication over baseEnv),
// and enforces mandatory overrides: LC_ALL=C, LANG=C, GIT_TERMINAL_PROMPT=0, and GIT_ADVICE=0.
func SanitizedEnv(baseEnv []string, extraEnv ...[]string) []string {
	if baseEnv == nil {
		baseEnv = os.Environ()
	}

	overrides := make(map[string]struct{}, 8)
	for _, extra := range extraEnv {
		for _, entry := range extra {
			if k, _, ok := strings.Cut(entry, "="); ok {
				overrides[k] = struct{}{}
			}
		}
	}

	filtered := make([]string, 0, len(baseEnv)+len(overrides)+4)
	for _, entry := range baseEnv {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, overridden := overrides[k]; overridden {
			continue
		}
		if isMandatoryOverride(k) {
			continue
		}
		filtered = append(filtered, entry)
	}

	for _, extra := range extraEnv {
		for _, entry := range extra {
			k, _, ok := strings.Cut(entry, "=")
			if !ok || isMandatoryOverride(k) {
				continue
			}
			filtered = append(filtered, entry)
		}
	}

	return append(filtered,
		"LC_ALL=C",
		"LANG=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ADVICE=0",
	)
}

func isMandatoryOverride(key string) bool {
	switch key {
	case "LC_ALL", "LANG", "GIT_TERMINAL_PROMPT", "GIT_ADVICE":
		return true
	default:
		return false
	}
}

// Error captures the command, arguments, exit code, and output streams of a failed subprocess.
type Error struct {
	Command  string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Category error
	Cause    error
}

// NewError creates an *Error extracting the command binary, args, and exit code
// directly from the executed *exec.Cmd and *exec.ExitError. If category is nil,
// it defaults to errcode.ErrTransport.
func NewError(cmd *exec.Cmd, exitErr *exec.ExitError, stdout, stderr string, category error) *Error {
	var name string
	var args []string
	if cmd != nil && len(cmd.Args) > 0 {
		name = cmd.Args[0]
		args = cmd.Args[1:]
	}
	exitCode := -1
	if exitErr != nil {
		exitCode = exitErr.ExitCode()
	}
	if category == nil {
		category = errcode.ErrTransport
	}
	return &Error{
		Command:  name,
		Args:     args,
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		Category: category,
		Cause:    exitErr,
	}
}

// Error formats the failed command invocation and its output streams.
func (e *Error) Error() string {
	cmdStr := e.Command
	if len(e.Args) > 0 {
		cmdStr += " " + strings.Join(e.Args, " ")
	}
	out := e.CombinedOutput()
	if out != "" {
		return fmt.Sprintf("%s failed (exit %d): %s", cmdStr, e.ExitCode, out)
	}
	return fmt.Sprintf("%s failed (exit %d)", cmdStr, e.ExitCode)
}

// CombinedOutput returns formatted combined output (prioritizing stderr, then stdout, or both).
func (e *Error) CombinedOutput() string {
	s := strings.TrimSpace(e.Stderr)
	o := strings.TrimSpace(e.Stdout)
	if s != "" && o != "" && s != o {
		return o + "\n" + s
	}
	if s != "" {
		return s
	}
	return o
}

// Unwrap allows errors.Is(err, category) and errors.Is(err, cause) to match Error.
// If Category is unset, it defaults to errcode.ErrTransport.
func (e *Error) Unwrap() error {
	if e.Category != nil {
		return e.Category
	}
	return errcode.ErrTransport
}

// ExitCodeOf extracts the process exit code from err if it wraps an *Error,
// or returns (-1, false) otherwise.
func ExitCodeOf(err error) (int, bool) {
	var cmdErr *Error
	if errors.As(err, &cmdErr) {
		return cmdErr.ExitCode, true
	}
	return -1, false
}

// StderrOrError returns the trimmed Stderr of err if it wraps an *Error with non-empty stderr,
// or err.Error() otherwise.
func StderrOrError(err error) string {
	if err == nil {
		return ""
	}
	var cmdErr *Error
	if errors.As(err, &cmdErr) {
		if s := strings.TrimSpace(cmdErr.Stderr); s != "" {
			return s
		}
	}
	return err.Error()
}
