package storage

import (
	"context"
	"io"

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

	return c.rsync.Sync(ctx, transport.RsyncOptions{
		Host:        req.Host,
		RemotePath:  req.RemotePath,
		LocalPath:   req.LocalPath,
		IsUpload:    false,
		Verbose:     req.Verbose,
		Quiet:       req.Quiet,
		Stats:       req.Stats,
		UserOptions: req.UserOptions,
		Stdout:      stdout,
		Stderr:      stderr,
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

	return c.rsync.Sync(ctx, transport.RsyncOptions{
		Host:        req.Host,
		RemotePath:  req.RemotePath,
		LocalPath:   req.LocalPath,
		IsUpload:    true,
		Verbose:     req.Verbose,
		Quiet:       req.Quiet,
		Stats:       req.Stats,
		UserOptions: req.UserOptions,
		Stdout:      stdout,
		Stderr:      stderr,
	})
}
