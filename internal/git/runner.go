// Package git provides a thin Git subprocess execution boundary (Runner),
// read-only repository plumbing queries (Inspector), and mutating Git
// operations (Operations).
package git

import (
	"context"
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

func (r *ExecRunner) resolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}
	binName := r.BinaryPath
	if binName == "" {
		binName = "git"
	}
	resolvedBin, lookErr := exec.LookPath(binName)
	if lookErr != nil {
		return "", errcode.WrapError(errcode.ErrTransport, lookErr, "Required binary %q not found in $PATH", binName)
	}
	return resolvedBin, nil
}

// RunWithEnv executes a git command with additional environment variables and returns stdout.
func (r *ExecRunner) RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	bin, err := r.resolveBinary(ctx)
	if err != nil {
		return "", err
	}
	out, err := command.Run(ctx, command.Request{
		Name:     bin,
		Args:     args,
		Dir:      dir,
		BaseEnv:  r.BaseEnv,
		ExtraEnv: extraEnv,
		Category: errcode.ErrGitExec,
	})
	return strings.TrimRight(out, "\r\n"), err
}

// RunStream executes a git command streaming stdout and stderr to the provided writers.
func (r *ExecRunner) RunStream(ctx context.Context, dir string, stdout, stderr io.Writer, extraEnv []string, args ...string) error {
	bin, err := r.resolveBinary(ctx)
	if err != nil {
		return err
	}
	_, err = command.Run(ctx, command.Request{
		Name:     bin,
		Args:     args,
		Dir:      dir,
		BaseEnv:  r.BaseEnv,
		ExtraEnv: extraEnv,
		Stdout:   stdout,
		Stderr:   stderr,
		Category: errcode.ErrGitExec,
	})
	return err
}
