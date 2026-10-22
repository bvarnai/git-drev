package transport

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/bvarnai/git-drev/internal/command"
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
		return nil, errcode.NewError(errcode.ErrConfig, "Remote host is not configured")
	}
	if opts.RemotePath == "" {
		return nil, errcode.NewError(errcode.ErrConfig, "Remote path is not configured")
	}
	if opts.LocalPath == "" {
		return nil, errcode.NewError(errcode.ErrUsage, "Local path is not specified")
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
	args, err := BuildRsyncArgs(opts)
	if err != nil {
		return err
	}

	var stdout io.Writer
	if opts.Verbose || opts.Stats {
		if opts.Stdout != nil {
			stdout = opts.Stdout
		} else {
			stdout = os.Stdout
		}
	}

	_, err = command.Run(ctx, command.Request{
		Name:     "rsync",
		Args:     args,
		Stdout:   stdout,
		Stderr:   opts.Stderr,
		Category: errcode.ErrTransport,
	})
	return err
}
