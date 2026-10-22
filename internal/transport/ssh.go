package transport

import (
	"context"
	"io"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/errcode"
)

// ExecSSHRunner executes remote SSH commands using the system OpenSSH binary.
type ExecSSHRunner struct{}

// NewSSHRunner creates a new ExecSSHRunner.
func NewSSHRunner() *ExecSSHRunner {
	return &ExecSSHRunner{}
}

// Run executes a non-interactive command on host via SSH and returns its stdout, or a *command.Error.
func (r *ExecSSHRunner) Run(ctx context.Context, host, cmd string) (string, error) {
	return command.Run(ctx, command.Request{
		Name:     "ssh",
		Args:     []string{"-T", host, cmd},
		Category: errcode.ErrTransport,
	})
}

// RunStream executes a command on host via SSH with optional stdin streaming and passes stdout reader to stdoutPipe callback.
func (r *ExecSSHRunner) RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) error {
	_, err := command.Run(ctx, command.Request{
		Name:       "ssh",
		Args:       []string{"-T", host, cmd},
		Stdin:      stdin,
		StdoutPipe: stdoutPipe,
		Category:   errcode.ErrTransport,
	})
	return err
}
