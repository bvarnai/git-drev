package transport

import (
	"context"
	"os/exec"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/errcode"
)

// CommandError aliases command.Error for backwards-compatibility within transport package.
type CommandError = command.Error

// ExitCodeOf extracts the process exit code from err if it wraps a *command.Error.
func ExitCodeOf(err error) (int, bool) {
	return command.ExitCodeOf(err)
}

// NewCommand creates a context-bound *exec.Cmd using internal/command.New.
func NewCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return command.New(ctx, name, args...)
}

// NewCommandError creates a *CommandError using internal/command.NewError.
func NewCommandError(cmd *exec.Cmd, exitErr *exec.ExitError, stdout, stderr string) *CommandError {
	return command.NewError(cmd, exitErr, stdout, stderr, errcode.ErrTransport)
}
