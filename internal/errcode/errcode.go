// Package errcode defines sentinel error categories and deterministic exit code
// mapping for git-drev CLI commands.
package errcode

import (
	"context"
	"errors"
	"fmt"
)

// Standard exit codes per docs/spec/global.md Section 3.
const (
	ExitSuccess        = 0
	ExitRuntime        = 1
	ExitUsage          = 2
	ExitConfig         = 3
	ExitGitState       = 4
	ExitGitExec        = 5
	ExitRemoteNotFound = 6
	ExitTransport      = 7
	ExitInterrupted    = 130
)

// Sentinel error categories mapped to standard exit codes.
var (
	// ErrRuntime indicates an unhandled I/O error, filesystem permission failure,
	// or non-zero project hook exit (exit code 1).
	ErrRuntime = errors.New("runtime error")

	// ErrUsage indicates a CLI usage or validation error such as unknown flags,
	// missing mandatory arguments, mutually exclusive flags, or invalid formats (exit code 2).
	ErrUsage = errors.New("usage error")

	// ErrConfig indicates a missing or malformed .drev.json or invalid git config drev.* (exit code 3).
	ErrConfig = errors.New("configuration error")

	// ErrNotRepository indicates invocation outside a non-bare Git working tree (exit code 3).
	ErrNotRepository = errors.New("not a git repository")

	// ErrGitState indicates an invalid repository state such as missing revision UUID,
	// empty branch marker on get, or missing target UUID/branch (exit code 4).
	ErrGitState = errors.New("invalid git state")

	// ErrDetachedHead indicates a detached HEAD state during commands that require
	// an attached branch such as put, update, or get --latest (exit code 4).
	ErrDetachedHead = errors.New("detached HEAD state")

	// ErrGitExec indicates a Git subprocess failure (exit code 5).
	ErrGitExec = errors.New("git execution error")

	// ErrRemoteNotFound indicates that the target revision directory or base path
	// does not exist on the remote server (exit code 6).
	ErrRemoteNotFound = errors.New("remote path not found")

	// ErrTransport indicates an SSH or rsync transport error or missing host binary (exit code 7).
	ErrTransport = errors.New("transport error")

	// ErrInterrupted indicates process interruption via SIGINT or SIGTERM (exit code 130).
	ErrInterrupted = errors.New("operation interrupted")
)

// HintError attaches an optional actionable remediation hint to an error.
type HintError struct {
	Err  error
	Hint string
}

// Error returns the underlying error message.
func (e *HintError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error for errors.Is / errors.As matching.
func (e *HintError) Unwrap() error {
	return e.Err
}

// WithHint wraps err with an actionable remediation hint to be emitted on stderr.
func WithHint(err error, hint string) error {
	if err == nil || hint == "" {
		return err
	}
	return &HintError{
		Err:  err,
		Hint: hint,
	}
}

// Hint extracts the remediation hint from err if present, or returns "".
func Hint(err error) string {
	var he *HintError
	if errors.As(err, &he) {
		return he.Hint
	}
	return ""
}

// Wrap wraps sentinel with a descriptive message while preserving errors.Is matching.
func Wrap(sentinel error, msg string) error {
	if sentinel == nil {
		return errors.New(msg)
	}
	return fmt.Errorf("%w: %s", sentinel, msg)
}

// Wrapf wraps sentinel with a formatted message while preserving errors.Is matching.
func Wrapf(sentinel error, format string, args ...any) error {
	return Wrap(sentinel, fmt.Sprintf(format, args...))
}

// Join wraps cause under sentinel so errors.Is matches both sentinel and cause.
func Join(sentinel error, msg string, cause error) error {
	if cause == nil {
		return Wrap(sentinel, msg)
	}
	if msg == "" {
		return fmt.Errorf("%w: %w", sentinel, cause)
	}
	return fmt.Errorf("%w: %s: %w", sentinel, msg, cause)
}

// ExitCode maps any error to its canonical CLI exit code per docs/spec/global.md Section 3.
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	switch {
	case errors.Is(err, ErrInterrupted), errors.Is(err, context.Canceled):
		return ExitInterrupted
	case errors.Is(err, ErrUsage):
		return ExitUsage
	case errors.Is(err, ErrConfig), errors.Is(err, ErrNotRepository):
		return ExitConfig
	case errors.Is(err, ErrGitState), errors.Is(err, ErrDetachedHead):
		return ExitGitState
	case errors.Is(err, ErrGitExec):
		return ExitGitExec
	case errors.Is(err, ErrRemoteNotFound):
		return ExitRemoteNotFound
	case errors.Is(err, ErrTransport):
		return ExitTransport
	case errors.Is(err, ErrRuntime):
		return ExitRuntime
	default:
		return ExitRuntime
	}
}
