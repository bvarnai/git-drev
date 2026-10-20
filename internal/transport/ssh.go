package transport

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// ExecSSHRunner executes remote SSH commands using the system OpenSSH binary.
type ExecSSHRunner struct{}

// NewSSHRunner creates a new ExecSSHRunner.
func NewSSHRunner() *ExecSSHRunner {
	return &ExecSSHRunner{}
}

// Run executes a non-interactive command on host via SSH and buffers its stdout and stderr.
func (r *ExecSSHRunner) Run(ctx context.Context, host, cmd string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	execCmd := exec.CommandContext(ctx, "ssh", "-T", host, cmd)
	execCmd.Env = SanitizedSubprocessEnv()
	execCmd.WaitDelay = 3 * time.Second

	var outBuf, errBuf bytes.Buffer
	execCmd.Stdout = &outBuf
	execCmd.Stderr = &errBuf

	err := execCmd.Run()
	outStr := outBuf.String()
	errStr := strings.TrimSpace(errBuf.String())

	if err != nil {
		if ctx.Err() != nil {
			return outStr, errStr, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
		}
		return outStr, errStr, err
	}

	return outStr, errStr, nil
}

// RunStream executes a command on host via SSH with stdin streaming and passes the stdout reader to stdoutPipe callback.
func (r *ExecSSHRunner) RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	execCmd := exec.CommandContext(ctx, "ssh", "-T", host, cmd)
	execCmd.Env = SanitizedSubprocessEnv()
	execCmd.Stdin = stdin
	execCmd.WaitDelay = 3 * time.Second

	var errBuf bytes.Buffer
	execCmd.Stderr = &errBuf

	pipeReader, err := execCmd.StdoutPipe()
	if err != nil {
		return "", errcode.WrapError(errcode.ErrRuntime, err, "failed to create stdout pipe")
	}

	if err := execCmd.Start(); err != nil {
		return "", errcode.NewError(errcode.ErrTransport, "SSH start failed on host %q: %s", host, err.Error())
	}

	var pipeErr error
	if stdoutPipe != nil {
		pipeErr = stdoutPipe(pipeReader)
	}

	waitErr := execCmd.Wait()
	errStr := strings.TrimSpace(errBuf.String())

	if waitErr != nil {
		if ctx.Err() != nil {
			return errStr, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
		}
		return errStr, waitErr
	}

	if pipeErr != nil {
		return errStr, pipeErr
	}

	return errStr, nil
}
