package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
)

// ExecRemoteRunner executes SSH and rsync commands using system binaries.
type ExecRemoteRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

// NewExecRemoteRunner creates a new ExecRemoteRunner with optional stream overrides.
func NewExecRemoteRunner(stdout, stderr io.Writer) *ExecRemoteRunner {
	return &ExecRemoteRunner{
		Stdout: stdout,
		Stderr: stderr,
	}
}

// CheckDirExists tests whether remotePath is an existing directory on host.
func (r *ExecRemoteRunner) CheckDirExists(ctx context.Context, host, remotePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("test -d %s", POSIXEscape(remotePath))
	cmd := exec.CommandContext(ctx, "ssh", "-T", host, cmdStr)
	cmd.Env = sanitizedSubprocessEnv()
	cmd.WaitDelay = 3 * time.Second

	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	if ctx.Err() != nil {
		return false, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Exit code 1 means directory does not exist.
		if exitErr.ExitCode() == 1 {
			return false, nil
		}
	}

	errMsg := strings.TrimSpace(errBuf.String())
	if errMsg == "" {
		errMsg = err.Error()
	}
	return false, errcode.NewError(errcode.ErrTransport, "SSH check failed on host %q: %s", host, errMsg)
}

// CloneBaseline creates a remote hardlinked copy of srcPath at dstPath using `cp -al --`.
func (r *ExecRemoteRunner) CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("cp -al -- %s %s", POSIXEscape(srcPath), POSIXEscape(dstPath))
	cmd := exec.CommandContext(ctx, "ssh", "-T", host, cmdStr)
	cmd.Env = sanitizedSubprocessEnv()
	cmd.WaitDelay = 3 * time.Second

	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	errMsg := strings.TrimSpace(errBuf.String())
	if errMsg == "" {
		errMsg = err.Error()
	}
	return errcode.NewError(errcode.ErrTransport, "SSH clone failed on host %q: %s", host, errMsg)
}

// RemoveDir removes remotePath on host using `rm -rf -- <remotePath>`.
func (r *ExecRemoteRunner) RemoveDir(ctx context.Context, host, remotePath string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("rm -rf -- %s", POSIXEscape(remotePath))
	cmd := exec.CommandContext(ctx, "ssh", "-T", host, cmdStr)
	cmd.Env = sanitizedSubprocessEnv()
	cmd.WaitDelay = 3 * time.Second

	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	errMsg := strings.TrimSpace(errBuf.String())
	if errMsg == "" {
		errMsg = err.Error()
	}
	return errcode.NewError(errcode.ErrTransport, "SSH remove failed on host %q: %s", host, errMsg)
}

const queryUsageScript = `set -e
target="$1"
if [ ! -d "$target" ]; then
    echo "ERROR:Remote path '$target' does not exist." >&2
    exit 66
fi
total_size=$(du -sh -- "$target" 2>/dev/null | cut -f1)
echo "TOTAL_SIZE:${total_size:--}"
for d in "$target"/*; do
    [ -d "$d" ] || continue
    name=$(basename -- "$d")
    case "$name" in
        [0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F])
            size=$(du -sh -- "$d" 2>/dev/null | cut -f1)
            mtime=$(stat -c %Y -- "$d" 2>/dev/null || stat -f %m -- "$d" 2>/dev/null || echo 0)
            echo "REV:${name}:${size:--}:${mtime:-0}"
            ;;
    esac
done
`

// QueryUsage executes remote storage discovery and returns total size and discovered revision directories.
func (r *ExecRemoteRunner) QueryUsage(ctx context.Context, host, basePath string) (string, []domain.RemoteRevision, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("/bin/sh -s -- %s", POSIXEscape(basePath))
	cmd := exec.CommandContext(ctx, "ssh", "-T", host, cmdStr)
	cmd.Env = sanitizedSubprocessEnv()
	cmd.Stdin = strings.NewReader(queryUsageScript)
	cmd.WaitDelay = 3 * time.Second

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	outStr := outBuf.String()
	errStr := errBuf.String()

	if err != nil {
		if ctx.Err() != nil {
			return "", nil, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
		}

		var exitErr *exec.ExitError
		if (errors.As(err, &exitErr) && exitErr.ExitCode() == 66) || strings.Contains(errStr, "ERROR:Remote path") {
			return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path %q does not exist on host %q", basePath, host)
		}

		errMsg := strings.TrimSpace(errStr)
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", nil, errcode.NewError(errcode.ErrTransport, "SSH usage query failed on host %q: %s", host, errMsg)
	}

	totalSize, revisions := ParseQueryUsageOutput(outStr)
	return totalSize, revisions, nil
}

// ParseQueryUsageOutput parses standard stdout emitted by queryUsageScript.
func ParseQueryUsageOutput(output string) (totalSize string, revisions []domain.RemoteRevision) {
	totalSize = "-"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "TOTAL_SIZE:") {
			totalSize = strings.TrimPrefix(line, "TOTAL_SIZE:")
			if totalSize == "" {
				totalSize = "-"
			}
			continue
		}
		if strings.HasPrefix(line, "REV:") {
			parts := strings.SplitN(strings.TrimPrefix(line, "REV:"), ":", 3)
			if len(parts) >= 2 {
				uuid := strings.ToLower(parts[0])
				size := parts[1]
				var mtime time.Time
				if len(parts) == 3 {
					if epoch, err := strconv.ParseInt(parts[2], 10, 64); err == nil && epoch > 0 {
						mtime = time.Unix(epoch, 0)
					}
				}
				revisions = append(revisions, domain.RemoteRevision{
					UUID:      uuid,
					SizeHuman: size,
					ModTime:   mtime,
				})
			}
		}
	}
	return totalSize, revisions
}

// RsyncGet downloads files from remote to local using rsync.
func (r *ExecRemoteRunner) RsyncGet(ctx context.Context, req SyncRequest) error {
	return r.executeRsync(ctx, req, false)
}

// RsyncPut uploads files from local to remote using rsync.
func (r *ExecRemoteRunner) RsyncPut(ctx context.Context, req SyncRequest) error {
	return r.executeRsync(ctx, req, true)
}

func (r *ExecRemoteRunner) executeRsync(ctx context.Context, req SyncRequest, isPut bool) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	args, err := BuildRsyncArgs(req, isPut)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Env = sanitizedSubprocessEnv()
	cmd.WaitDelay = 3 * time.Second

	var errBuf bytes.Buffer
	if req.Verbose || req.Stats {
		if r.Stdout != nil {
			cmd.Stdout = r.Stdout
		} else {
			cmd.Stdout = os.Stdout
		}
	}

	if r.Stderr != nil {
		cmd.Stderr = io.MultiWriter(r.Stderr, &errBuf)
	} else {
		cmd.Stderr = &errBuf
	}

	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	errMsg := strings.TrimSpace(errBuf.String())
	if errMsg == "" {
		errMsg = runErr.Error()
	}

	op := "get"
	if isPut {
		op = "put"
	}
	return errcode.NewError(errcode.ErrTransport, "rsync %s failed: %s", op, errMsg)
}

func sanitizedSubprocessEnv() []string {
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
