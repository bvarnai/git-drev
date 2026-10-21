// Package hook manages project hook detection and execution (.git-drev-put-hook,
// .git-drev-info-hook) with timeout enforcement and GIT_DREV_* environment injection.
package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/errcode"
)

// DefaultTimeout is the standard execution timeout applied to project hooks.
const DefaultTimeout = 5 * time.Minute

// EnvTimeout is the environment variable used to customize the hook execution timeout.
const EnvTimeout = "GIT_DREV_HOOK_TIMEOUT"

// Kind identifies the project hook type.
type Kind string

const (
	// KindPut represents the .git-drev-put-hook script executed before committing a revision.
	KindPut Kind = "put"
	// KindInfo represents the .git-drev-info-hook script executed by the info subcommand.
	KindInfo Kind = "info"
)

// HookFileName returns the canonical filename for the given hook kind.
func HookFileName(kind Kind) string {
	switch kind {
	case KindPut:
		return ".git-drev-put-hook"
	case KindInfo:
		return ".git-drev-info-hook"
	default:
		return ".git-drev-" + string(kind) + "-hook"
	}
}

// HookFunctionName returns the conventional Bash function name for the hook kind.
func HookFunctionName(kind Kind) string {
	switch kind {
	case KindPut:
		return "put_hook"
	case KindInfo:
		return "info_hook"
	default:
		return string(kind) + "_hook"
	}
}

// Env holds the GIT_DREV_* metadata injected into hook subprocess environments.
type Env struct {
	Version string // GIT_DREV_VERSION
	UUID    string // GIT_DREV_UUID
	Seq     string // GIT_DREV_SEQ
	Branch  string // GIT_DREV_BRANCH
	WorkDir string // GIT_DREV_WORK_DIR
	Root    string // GIT_DREV_ROOT
}

// ToEnviron returns a slice of KEY=VALUE strings for the hook environment.
func (e Env) ToEnviron(kind Kind) []string {
	return []string{
		"GIT_DREV_VERSION=" + e.Version,
		"GIT_DREV_UUID=" + e.UUID,
		"GIT_DREV_SEQ=" + e.Seq,
		"GIT_DREV_BRANCH=" + e.Branch,
		"GIT_DREV_WORK_DIR=" + e.WorkDir,
		"GIT_DREV_ROOT=" + e.Root,
		"GIT_DREV_HOOK_TYPE=" + string(kind),
	}
}

// RunRequest configures the execution of a project hook script.
type RunRequest struct {
	Kind    Kind
	Path    string
	WorkDir string
	Args    []string
	Env     Env
	Timeout time.Duration
	Stdout  io.Writer
	Stderr  io.Writer
}

// Runner abstracts project hook detection and execution.
type Runner interface {
	Run(ctx context.Context, req RunRequest) error
}

// ExecRunner implements Runner by launching isolated OS subprocesses.
type ExecRunner struct{}

// NewExecRunner creates a new ExecRunner.
func NewExecRunner() *ExecRunner {
	return &ExecRunner{}
}

// ResolveTimeout parses GIT_DREV_HOOK_TIMEOUT or returns DefaultTimeout.
func ResolveTimeout(envLookup func(string) (string, bool)) (time.Duration, error) {
	if envLookup == nil {
		envLookup = os.LookupEnv
	}
	val, ok := envLookup(EnvTimeout)
	if !ok || strings.TrimSpace(val) == "" {
		return DefaultTimeout, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(val))
	if err != nil || d <= 0 {
		return 0, errcode.NewError(errcode.ErrUsage, "invalid %s %q: must be a positive duration", EnvTimeout, val)
	}
	return d, nil
}

// Run executes the project hook requested in req, enforcing timeouts and environment isolation.
func (r *ExecRunner) Run(ctx context.Context, req RunRequest) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	hookBase := filepath.Base(req.Path)

	fi, err := os.Stat(req.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Check if it's a broken symlink
			if _, lerr := os.Lstat(req.Path); lerr == nil {
				return errcode.NewError(errcode.ErrRuntime, "%s hook %q is a broken symlink", req.Kind, hookBase)
			}
			return fs.ErrNotExist
		}
		return errcode.WrapError(errcode.ErrRuntime, err, "failed to stat %s hook %q", req.Kind, hookBase)
	}

	if fi.IsDir() {
		return errcode.NewError(errcode.ErrRuntime, "%s hook %q is a directory, expected file", req.Kind, hookBase)
	}

	// An empty hook file (0 bytes) is a valid no-op.
	if fi.Size() == 0 {
		return nil
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := buildHookCommand(hookCtx, req, fi)

	// Stream binding
	if req.Stdout != nil {
		cmd.Stdout = req.Stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if req.Stderr != nil {
		cmd.Stderr = req.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	// Working directory
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	} else {
		cmd.Dir = filepath.Dir(req.Path)
	}

	// Environment sanitization
	cmd.Env = buildHookEnv(req.Env, req.Kind)

	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}

	if hookCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return errcode.NewError(errcode.ErrRuntime, "%s hook %q timed out after %v", req.Kind, hookBase, timeout)
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return errcode.NewError(errcode.ErrRuntime, "%s hook %q failed with exit code %d", req.Kind, hookBase, exitErr.ExitCode())
	}

	return errcode.WrapError(errcode.ErrRuntime, runErr, "%s hook %q failed", req.Kind, hookBase)
}

func buildHookCommand(ctx context.Context, req RunRequest, fi os.FileInfo) *exec.Cmd {
	isExec := fi.Mode().Perm()&0o111 != 0
	shebang, isCustomShebang := detectCustomShebang(req.Path)

	if isExec && isCustomShebang && shebang != "" {
		// Standalone executable with non-sh/bash interpreter (e.g. python, perl, binary)
		return command.New(ctx, req.Path, req.Args...)
	}

	// Shell script mode: supports both top-level execution and hook functions
	fnName := HookFunctionName(req.Kind)
	script := fmt.Sprintf(`set -e
hook_file="$1"
shift
source "${hook_file}"
if declare -F %s >/dev/null 2>&1; then
    %s "$@"
fi
`, fnName, fnName)

	args := []string{"-c", script, "--", filepath.ToSlash(req.Path)}
	args = append(args, req.Args...)
	return command.New(ctx, "bash", args...)
}

func detectCustomShebang(path string) (shebang string, custom bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil || n < 2 {
		return "", false
	}

	if !bytes.HasPrefix(buf[:n], []byte("#!")) {
		return "", false
	}

	firstLine := string(bytes.SplitN(buf[:n], []byte("\n"), 2)[0])
	firstLine = strings.TrimSpace(firstLine)

	// If shebang targets sh or bash, we still route through our bash runner
	// so function definitions like info_hook() or put_hook() are invoked.
	lower := strings.ToLower(firstLine)
	if strings.Contains(lower, "bash") || strings.HasSuffix(lower, "/sh") {
		return firstLine, false
	}

	return firstLine, true
}

func buildHookEnv(env Env, kind Kind) []string {
	var base []string
	for _, entry := range os.Environ() {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch k {
		case "GIT_DREV_VERSION", "GIT_DREV_UUID", "GIT_DREV_SEQ", "GIT_DREV_BRANCH",
			"GIT_DREV_WORK_DIR", "GIT_DREV_ROOT", "GIT_DREV_HOOK_TYPE":
			continue
		default:
			base = append(base, entry)
		}
	}

	return command.SanitizedEnv(base, env.ToEnviron(kind))
}
