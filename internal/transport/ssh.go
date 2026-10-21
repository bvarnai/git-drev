package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/errcode"
)

// ExecSSHRunner executes remote SSH commands using the system OpenSSH binary.
type ExecSSHRunner struct{}

// NewSSHRunner creates a new ExecSSHRunner.
func NewSSHRunner() *ExecSSHRunner {
	return &ExecSSHRunner{}
}

// Run executes a non-interactive command on host via SSH and returns its stdout, or a *CommandError.
func (r *ExecSSHRunner) Run(ctx context.Context, host, cmd string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	execCmd := command.New(ctx, "ssh", "-T", host, cmd)

	var outBuf, errBuf bytes.Buffer
	execCmd.Stdout = &outBuf
	execCmd.Stderr = &errBuf

	err := execCmd.Run()
	outStr := outBuf.String()
	errStr := strings.TrimSpace(errBuf.String())

	if err != nil {
		if ctx.Err() != nil {
			return outStr, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return outStr, command.NewError(execCmd, exitErr, outStr, errStr, errcode.ErrTransport)
		}
		return outStr, errcode.WrapError(errcode.ErrTransport, err, "SSH execution failed on host %q", host)
	}

	return outStr, nil
}

// RunStream executes a command on host via SSH with optional stdin streaming and passes stdout reader to stdoutPipe callback.
func (r *ExecSSHRunner) RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	execCmd := command.New(ctx, "ssh", "-T", host, cmd)
	execCmd.Stdin = stdin

	var errBuf bytes.Buffer
	execCmd.Stderr = &errBuf

	pipeReader, err := execCmd.StdoutPipe()
	if err != nil {
		return errcode.WrapError(errcode.ErrRuntime, err, "failed to create stdout pipe")
	}

	if err := execCmd.Start(); err != nil {
		return errcode.NewError(errcode.ErrTransport, "SSH start failed on host %q: %s", host, err.Error())
	}

	var pipeErr error
	if stdoutPipe != nil {
		pipeErr = stdoutPipe(pipeReader)
	}

	waitErr := execCmd.Wait()
	errStr := strings.TrimSpace(errBuf.String())

	if waitErr != nil {
		if ctx.Err() != nil {
			return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
		}
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return command.NewError(execCmd, exitErr, "", errStr, errcode.ErrTransport)
		}
		return errcode.WrapError(errcode.ErrTransport, waitErr, "SSH execution failed on host %q", host)
	}

	if pipeErr != nil {
		return pipeErr
	}

	return nil
}
