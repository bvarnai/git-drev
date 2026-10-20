package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// ExecRsyncRunner executes rsync commands using system binaries.
type ExecRsyncRunner struct{}

// NewRsyncRunner creates a new ExecRsyncRunner.
func NewRsyncRunner() *ExecRsyncRunner {
	return &ExecRsyncRunner{}
}

// BuildRsyncArgs constructs sanitized, deterministic rsync command arguments.
func BuildRsyncArgs(opts RsyncOptions) ([]string, error) {
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

	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}

	remoteSpec := fmt.Sprintf("%s:%s", opts.Host, opts.RemotePath)

	if opts.IsUpload {
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

	args, err := BuildRsyncArgs(opts)
	if err != nil {
		return err
	}

	cmd := NewCommand(ctx, "rsync", args...)

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

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return NewCommandError(cmd, exitErr, "", errMsg)
	}

	return errcode.WrapError(errcode.ErrTransport, runErr, "rsync failed: %s", errMsg)
}
