// Package git provides a thin Git subprocess execution boundary (Runner),
// read-only repository plumbing queries (Inspector), and mutating Git
// operations (Operations).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"git-drev/internal/errcode"
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

// CommandError captures the exit code and output streams of a failed Git subprocess.
type CommandError struct {
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Cause    error
}

// Error formats the failed git invocation and its combined output.
func (e *CommandError) Error() string {
	cmd := "git " + strings.Join(e.Args, " ")
	out := e.CombinedOutput()
	if out != "" {
		return fmt.Sprintf("%s failed (exit %d): %s", cmd, e.ExitCode, out)
	}
	return fmt.Sprintf("%s failed (exit %d)", cmd, e.ExitCode)
}

// Unwrap allows errors.Is(err, errcode.ErrGitExec) to match CommandError by default.
func (e *CommandError) Unwrap() error {
	return errcode.ErrGitExec
}

// CombinedOutput returns trimmed stderr if present, or trimmed stdout, or both.
func (e *CommandError) CombinedOutput() string {
	s := strings.TrimSpace(e.Stderr)
	o := strings.TrimSpace(e.Stdout)
	if s != "" && o != "" {
		return o + "\n" + s
	}
	if s != "" {
		return s
	}
	return o
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

// ExecRunner implements Runner using os/exec with LC_ALL=C and non-interactive safeguards.
type ExecRunner struct {
	DebugFn    func(cmd string)
	BaseEnv    []string
	BinaryPath string
}

// NewExecRunner creates a new ExecRunner.
func NewExecRunner(debugFn func(cmd string)) *ExecRunner {
	return &ExecRunner{
		DebugFn:    debugFn,
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
		var cmdErr *CommandError
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

	if r.DebugFn != nil {
		r.DebugFn("git " + strings.Join(args, " "))
	}

	cmd := exec.CommandContext(ctx, resolvedBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = BuildSanitizedEnv(r.BaseEnv, extraEnv)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}

	runErr := cmd.Run()
	if runErr != nil {
		if ctx.Err() != nil {
			return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "Operation interrupted")
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return &CommandError{
				Args:     append([]string(nil), args...),
				ExitCode: exitErr.ExitCode(),
				Cause:    runErr,
			}
		}
		return errcode.WrapError(errcode.ErrTransport, runErr, "failed to execute %s", binName)
	}
	return nil
}

// BuildSanitizedEnv constructs a child process environment from baseEnv (or os.Environ()
// if baseEnv is nil), appends extraEnv, and enforces mandatory overrides per
// docs/spec/global.md Section 2: LC_ALL=C, LANG=C, GIT_TERMINAL_PROMPT=0, and GIT_ADVICE=0.
func BuildSanitizedEnv(baseEnv []string, extraEnv ...[]string) []string {
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	combined := make([]string, 0, len(baseEnv)+8)
	combined = append(combined, baseEnv...)
	for _, extra := range extraEnv {
		combined = append(combined, extra...)
	}

	filtered := make([]string, 0, len(combined)+4)
	for _, entry := range combined {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch k {
		case "LC_ALL", "LANG", "GIT_TERMINAL_PROMPT", "GIT_ADVICE":
			continue
		default:
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
