package transport

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

// NewCommand creates a context-bound *exec.Cmd pre-configured with sanitized
// locale environment variables, a 3-second wait delay, and graceful SIGTERM cancellation.
func NewCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = SanitizedSubprocessEnv()
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return cmd
}

// NewCommandError extracts the command name, arguments, and exit code directly
// from the executed *exec.Cmd and *exec.ExitError (single source of truth).
func NewCommandError(cmd *exec.Cmd, exitErr *exec.ExitError, stdout, stderr string) *CommandError {
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
	return &CommandError{
		Command:  name,
		Args:     args,
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		Cause:    exitErr,
	}
}

// CommandError captures the exit code, command, and output streams of a failed subprocess execution.
type CommandError struct {
	Command  string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Cause    error
}

// Error formats the failed command invocation and its stderr/stdout output.
func (e *CommandError) Error() string {
	cmdStr := e.Command
	if len(e.Args) > 0 {
		cmdStr += " " + strings.Join(e.Args, " ")
	}
	out := strings.TrimSpace(e.Stderr)
	if out == "" {
		out = strings.TrimSpace(e.Stdout)
	}
	if out != "" {
		return fmt.Sprintf("%s failed (exit %d): %s", cmdStr, e.ExitCode, out)
	}
	return fmt.Sprintf("%s failed (exit %d)", cmdStr, e.ExitCode)
}

// Unwrap allows errors.Is(err, errcode.ErrTransport) to match CommandError.
func (e *CommandError) Unwrap() error {
	return errcode.ErrTransport
}

// ExitCodeOf extracts the process exit code from err if it wraps a *CommandError,
// or returns (-1, false) otherwise.
func ExitCodeOf(err error) (int, bool) {
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.ExitCode, true
	}
	return -1, false
}

// SanitizedSubprocessEnv returns a sanitized environment slice for subprocess execution.
func SanitizedSubprocessEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch k {
		case "LC_ALL", "LANG", "GIT_TERMINAL_PROMPT", "GIT_ADVICE":
			continue
		default:
			env = append(env, entry)
		}
	}
	env = append(env,
		"LC_ALL=C",
		"LANG=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ADVICE=0",
	)
	return env
}
