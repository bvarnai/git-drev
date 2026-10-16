// Package errcode defines sentinel error categories, structured application
// errors, and deterministic exit code mapping for git-drev CLI commands.
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

	// ErrConfig indicates a missing or malformed .git-drev.yaml configuration (exit code 3).
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

// AppError is a structured application error associated with a sentinel category,
// a clean user-facing message, an optional underlying cause, and an optional hint.
type AppError struct {
	Category error
	Message  string
	Hint     string
	Err      error
}

// Error returns the user-facing message (and underlying cause if present)
// without polluting the output with the internal sentinel category name.
func (e *AppError) Error() string {
	if e.Err != nil {
		if e.Message == "" {
			return e.Err.Error()
		}
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Category != nil {
		return e.Category.Error()
	}
	return ""
}

// Unwrap returns both the sentinel Category and the underlying Err so that
// errors.Is and errors.As traverse both chains.
func (e *AppError) Unwrap() []error {
	var errs []error
	if e.Category != nil {
		errs = append(errs, e.Category)
	}
	if e.Err != nil {
		errs = append(errs, e.Err)
	}
	return errs
}

// WithHint sets an actionable remediation hint on the AppError and returns it for chaining.
func (e *AppError) WithHint(format string, args ...any) *AppError {
	if len(args) == 0 {
		e.Hint = format
	} else {
		e.Hint = fmt.Sprintf(format, args...)
	}
	return e
}

// NewError creates a new AppError with a sentinel category and formatted user message.
func NewError(category error, format string, args ...any) *AppError {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	return &AppError{
		Category: category,
		Message:  msg,
	}
}

// WrapError wraps an existing error with a sentinel category and formatted context message.
func WrapError(category error, err error, format string, args ...any) *AppError {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	return &AppError{
		Category: category,
		Message:  msg,
		Err:      err,
	}
}

// Hint extracts the remediation hint from err if it wraps an *AppError, or returns "".
func Hint(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Hint
	}
	return ""
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
