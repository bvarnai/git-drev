// Package storage manages remote storage operations, revision baselines, and synchronization.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/transport"
)

// Manager defines the interface for remote storage operations.
type Manager interface {
	CheckDirExists(ctx context.Context, host, remotePath string) (bool, error)
	CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error
	RemoveDir(ctx context.Context, host, remotePath string) error
	QueryUsage(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (totalSize string, revisions []domain.RemoteRevision, err error)
	Download(ctx context.Context, req TransferRequest) error
	Upload(ctx context.Context, req TransferRequest) error
}

// Client implements Manager using transport.SSHRunner and transport.RsyncRunner.
type Client struct {
	ssh    transport.SSHRunner
	rsync  transport.RsyncRunner
	stdout io.Writer
	stderr io.Writer
}

// NewClient creates a new storage Client.
func NewClient(ssh transport.SSHRunner, rsync transport.RsyncRunner, stdout, stderr io.Writer) *Client {
	return &Client{
		ssh:    ssh,
		rsync:  rsync,
		stdout: stdout,
		stderr: stderr,
	}
}

// CheckDirExists tests whether remotePath is an existing directory on host.
func (c *Client) CheckDirExists(ctx context.Context, host, remotePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("test -d %s", transport.POSIXEscape(remotePath))
	_, err := c.ssh.Run(ctx, host, cmdStr)
	if err == nil {
		return true, nil
	}

	if ctx.Err() != nil {
		return false, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	if code, ok := transport.ExitCodeOf(err); ok && code == 1 {
		return false, nil
	}

	var cmdErr *transport.CommandError
	errMsg := err.Error()
	if errors.As(err, &cmdErr) && strings.TrimSpace(cmdErr.Stderr) != "" {
		errMsg = strings.TrimSpace(cmdErr.Stderr)
	}

	return false, errcode.NewError(errcode.ErrTransport, "SSH check failed on host %q: %s", host, errMsg)
}

// CloneBaseline creates a remote hardlinked copy of srcPath at dstPath using `cp -al --`.
func (c *Client) CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("cp -al -- %s %s", transport.POSIXEscape(srcPath), transport.POSIXEscape(dstPath))
	_, err := c.ssh.Run(ctx, host, cmdStr)
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	var cmdErr *transport.CommandError
	errMsg := err.Error()
	if errors.As(err, &cmdErr) && strings.TrimSpace(cmdErr.Stderr) != "" {
		errMsg = strings.TrimSpace(cmdErr.Stderr)
	}
	return errcode.NewError(errcode.ErrTransport, "SSH clone failed on host %q: %s", host, errMsg)
}

// RemoveDir removes remotePath on host using `rm -rf -- <remotePath>`.
func (c *Client) RemoveDir(ctx context.Context, host, remotePath string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	cmdStr := fmt.Sprintf("rm -rf -- %s", transport.POSIXEscape(remotePath))
	_, err := c.ssh.Run(ctx, host, cmdStr)
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
	}

	var cmdErr *transport.CommandError
	errMsg := err.Error()
	if errors.As(err, &cmdErr) && strings.TrimSpace(cmdErr.Stderr) != "" {
		errMsg = strings.TrimSpace(cmdErr.Stderr)
	}
	return errcode.NewError(errcode.ErrTransport, "SSH remove failed on host %q: %s", host, errMsg)
}
