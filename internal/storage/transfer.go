package storage

import (
	"context"
	"io"

	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/transport"
)

// TransferRequest configures file synchronization between local and remote storage.
type TransferRequest struct {
	Host        string
	RemotePath  string
	LocalPath   string
	Verbose     bool
	Quiet       bool
	Stats       bool
	UserOptions string
	Stdout      io.Writer
	Stderr      io.Writer
}

// ValidateUploadOptions ensures rsync options do not mutate baseline hardlinks in-place.
func ValidateUploadOptions(tokens []string) error {
	for _, opt := range tokens {
		switch opt {
		case "--inplace", "--append", "--append-verify":
			return errcode.NewError(errcode.ErrConfig, "rsync option %q is not allowed in remote.rsync.put because it mutates hardlinked baseline revisions in-place", opt).
				WithHint("Remove %q from remote.rsync.put so rsync replaces modified files atomically.", opt)
		}
	}
	return nil
}

// Download synchronizes files from remote storage to local filesystem.
func (c *Client) Download(ctx context.Context, req TransferRequest) error {
	stdout := req.Stdout
	if stdout == nil {
		stdout = c.stdout
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = c.stderr
	}

	extraArgs, err := transport.TokenizeOptions(req.UserOptions)
	if err != nil {
		return err
	}

	return c.rsync.Sync(ctx, transport.RsyncOptions{
		Host:       req.Host,
		RemotePath: req.RemotePath,
		LocalPath:  req.LocalPath,
		IsUpload:   false,
		Verbose:    req.Verbose,
		Quiet:      req.Quiet,
		Stats:      req.Stats,
		ExtraArgs:  extraArgs,
		Stdout:     stdout,
		Stderr:     stderr,
	})
}

// Upload synchronizes files from local filesystem to remote storage.
func (c *Client) Upload(ctx context.Context, req TransferRequest) error {
	stdout := req.Stdout
	if stdout == nil {
		stdout = c.stdout
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = c.stderr
	}

	extraArgs, err := transport.TokenizeOptions(req.UserOptions)
	if err != nil {
		return err
	}
	if err := ValidateUploadOptions(extraArgs); err != nil {
		return err
	}

	return c.rsync.Sync(ctx, transport.RsyncOptions{
		Host:       req.Host,
		RemotePath: req.RemotePath,
		LocalPath:  req.LocalPath,
		IsUpload:   true,
		Verbose:    req.Verbose,
		Quiet:      req.Quiet,
		Stats:      req.Stats,
		ExtraArgs:  extraArgs,
		Stdout:     stdout,
		Stderr:     stderr,
	})
}
