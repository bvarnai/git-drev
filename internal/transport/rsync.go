package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// ExecRsyncRunner executes rsync commands using system binaries.
type ExecRsyncRunner struct{}

// NewRsyncRunner creates a new ExecRsyncRunner.
func NewRsyncRunner() *ExecRsyncRunner {
	return &ExecRsyncRunner{}
}

// BuildRsyncArgs constructs sanitized, deterministic rsync command arguments.
func BuildRsyncArgs(opts RsyncOptions, isPut bool) ([]string, error) {
	if opts.Host == "" {
		return nil, errcode.NewError(errcode.ErrConfig, "remote host is not configured")
	}
	if opts.RemotePath == "" {
		return nil, errcode.NewError(errcode.ErrConfig, "remote path is not configured")
	}
	if opts.LocalPath == "" {
		return nil, errcode.NewError(errcode.ErrUsage, "local path is not specified")
	}

	args := []string{
		"-a",
		"--delete-during",
		"-e",
		"ssh -T",
	}

	if opts.Verbose {
		args = append(args, "-v")
	}
	if opts.Quiet {
		args = append(args, "-q")
	}
	if opts.Stats {
		args = append(args, "--stats")
	}

	if strings.TrimSpace(opts.UserOptions) != "" {
		tokens, err := TokenizeOptions(opts.UserOptions)
		if err != nil {
			return nil, err
		}

		if isPut {
			for _, opt := range tokens {
				switch opt {
				case "--inplace", "--append", "--append-verify":
					return nil, errcode.NewError(errcode.ErrConfig, "rsync option %q is not allowed in remote.rsync.put because it mutates hardlinked baseline revisions in-place", opt).
						WithHint("Remove %q from remote.rsync.put so rsync replaces modified files atomically.", opt)
				}
			}
		}

		args = append(args, tokens...)
	}

	remoteSpec := fmt.Sprintf("%s:%s", opts.Host, opts.RemotePath)

	if isPut {
		args = append(args, opts.LocalPath, remoteSpec)
	} else {
		args = append(args, remoteSpec, opts.LocalPath)
	}

	return args, nil
}

// Sync executes rsync according to opts.
func (r *ExecRsyncRunner) Sync(ctx context.Context, opts RsyncOptions) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	args, err := BuildRsyncArgs(opts, opts.IsUpload)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Env = SanitizedSubprocessEnv()
	cmd.WaitDelay = 3 * time.Second

	var errBuf bytes.Buffer
	if opts.Verbose || opts.Stats {
		if opts.Stdout != nil {
			cmd.Stdout = opts.Stdout
		} else {
			cmd.Stdout = os.Stdout
		}
	}

	if opts.Stderr != nil {
		cmd.Stderr = io.MultiWriter(opts.Stderr, &errBuf)
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
	if opts.IsUpload {
		op = "put"
	}
	return errcode.NewError(errcode.ErrTransport, "rsync %s failed: %s", op, errMsg)
}
