// Package git provides a thin Git subprocess execution boundary (Runner),
// read-only repository plumbing queries (Inspector), and mutating Git
// operations (Operations).
package git

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

// Runner represents the direct Git execution boundary interface.
type Runner interface {
	// Run executes a git command in dir and returns stdout (with trailing newline trimmed), or an error.
	Run(ctx context.Context, dir string, args ...string) (string, error)
	// RunLines executes a git command in dir and returns stdout split into lines.
	RunLines(ctx context.Context, dir string, args ...string) ([]string, error)
	// RunWithEnv executes a git command in dir with additional environment variables.
	RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error)
	// RunStream executes a git command streaming stdout and stderr to the provided writers.
	RunStream(ctx context.Context, dir string, stdout, stderr io.Writer, extraEnv []string, args ...string) error
}

// ExecRunner implements Runner using os/exec with LC_ALL=C and non-interactive safeguards.
type ExecRunner struct {
	BaseEnv    []string
	BinaryPath string
}

// NewExecRunner creates a new ExecRunner.
func NewExecRunner() *ExecRunner {
	return &ExecRunner{
		BinaryPath: "git",
	}
}

// Run executes a git command in dir and returns stdout (with trailing \r\n or \n trimmed).
func (r *ExecRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return r.RunWithEnv(ctx, dir, nil, args...)
}

// RunLines executes a git command in dir and returns stdout split into lines.
func (r *ExecRunner) RunLines(ctx context.Context, dir string, args ...string) ([]string, error) {
	out, err := r.Run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	return strings.Split(normalized, "\n"), nil
}

// RunWithEnv executes a git command with additional environment variables and returns stdout.
func (r *ExecRunner) RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	err := r.RunStream(ctx, dir, &stdoutBuf, &stderrBuf, extraEnv, args...)
	out := strings.TrimRight(stdoutBuf.String(), "\r\n")
	if err != nil {
		var cmdErr *command.Error
		if errors.As(err, &cmdErr) {
			cmdErr.Stdout = stdoutBuf.String()
			cmdErr.Stderr = stderrBuf.String()
		}
		return out, err
	}
	return out, nil
}

// RunStream executes a git command streaming stdout and stderr to the provided writers.
func (r *ExecRunner) RunStream(ctx context.Context, dir string, stdout, stderr io.Writer, extraEnv []string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	binName := r.BinaryPath
	if binName == "" {
		binName = "git"
	}
	resolvedBin, lookErr := exec.LookPath(binName)
	if lookErr != nil {
		return errcode.WrapError(errcode.ErrTransport, lookErr, "Required binary %q not found in $PATH", binName)
	}

	cmd := command.New(ctx, resolvedBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = command.SanitizedEnv(r.BaseEnv, extraEnv)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if runErr != nil {
		if ctx.Err() != nil {
			return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "Operation interrupted")
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return command.NewError(cmd, exitErr, "", "", errcode.ErrGitExec)
		}
		return errcode.WrapError(errcode.ErrTransport, runErr, "failed to execute %s", binName)
	}
	return nil
}
