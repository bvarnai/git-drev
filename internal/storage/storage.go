// Package storage manages remote storage operations, revision baselines, and synchronization.
package storage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/transport"
)

// AccessResult represents the probed permissions and state of the remote base path.
type AccessResult struct {
	Readable bool
	Writable bool
	Created  bool
}

// Manager defines the interface for remote storage operations.
type Manager interface {
	HasRevision(ctx context.Context, host, basePath, uuid string) (bool, error)
	CloneBaseline(ctx context.Context, host, basePath, srcUUID, dstUUID string) error
	RemoveRevision(ctx context.Context, host, basePath, uuid string) error
	QueryUsage(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (totalSize string, revisions []domain.RemoteRevision, err error)
	Download(ctx context.Context, req TransferRequest) error
	Upload(ctx context.Context, req TransferRequest) error
	CheckAccess(ctx context.Context, host, basePath string) (AccessResult, error)
}

// Client implements Manager using transport.SSHRunner and transport.RsyncRunner.
type Client struct {
	ssh   transport.SSHRunner
	rsync transport.RsyncRunner
}

// NewClient creates a new storage Client.
func NewClient(ssh transport.SSHRunner, rsync transport.RsyncRunner) *Client {
	return &Client{
		ssh:   ssh,
		rsync: rsync,
	}
}

// RevisionPath returns the clean absolute remote path for a revision: <basePath>/<uuid>.
func RevisionPath(basePath, uuid string) string {
	cleanBase := strings.TrimRight(strings.ReplaceAll(basePath, "\\", "/"), "/")
	return cleanBase + "/" + strings.Trim(uuid, "/")
}

// RevisionDir returns the remote path for a revision with a single trailing slash for directory syncing.
func RevisionDir(basePath, uuid string) string {
	return RevisionPath(basePath, uuid) + "/"
}

// NormalizeDir ensures a local directory path uses forward slashes and has a single trailing slash.
func NormalizeDir(dir string) string {
	clean := filepath.ToSlash(filepath.Clean(dir))
	if !strings.HasSuffix(clean, "/") {
		clean += "/"
	}
	return clean
}

// HasRevision tests whether a revision directory exists on host.
func (c *Client) HasRevision(ctx context.Context, host, basePath, uuid string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	remotePath := RevisionPath(basePath, uuid)
	cmdStr := fmt.Sprintf("test -d %s", transport.POSIXEscape(remotePath))
	_, err := c.ssh.Run(ctx, host, cmdStr)
	if err == nil {
		return true, nil
	}

	if ctx.Err() != nil {
		return false, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "Operation interrupted")
	}

	if code, ok := command.ExitCodeOf(err); ok && code == 1 {
		return false, nil
	}

	return false, errcode.NewError(errcode.ErrTransport, "SSH check failed on host %q: %s", host, command.StderrOrError(err))
}

// CloneBaseline creates a remote hardlinked copy of srcUUID at dstUUID using `cp -al --`.
func (c *Client) CloneBaseline(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	srcPath := RevisionPath(basePath, srcUUID)
	dstPath := RevisionPath(basePath, dstUUID)
	cmdStr := fmt.Sprintf("cp -al -- %s %s", transport.POSIXEscape(srcPath), transport.POSIXEscape(dstPath))
	_, err := c.ssh.Run(ctx, host, cmdStr)
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "Operation interrupted")
	}

	return errcode.NewError(errcode.ErrTransport, "SSH clone failed on host %q: %s", host, command.StderrOrError(err))
}

// RemoveRevision removes a remote revision directory on host using `rm -rf -- <remotePath>`.
func (c *Client) RemoveRevision(ctx context.Context, host, basePath, uuid string) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	remotePath := RevisionPath(basePath, uuid)
	cmdStr := fmt.Sprintf("rm -rf -- %s", transport.POSIXEscape(remotePath))
	_, err := c.ssh.Run(ctx, host, cmdStr)
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "Operation interrupted")
	}

	return errcode.NewError(errcode.ErrTransport, "SSH remove failed on host %q: %s", host, command.StderrOrError(err))
}

// CheckAccess probes the remote base path over SSH: verifies connectivity, ensures the
// directory exists (attempting creation if needed), checks read/executable access, and
// detects whether the directory is writable.
func (c *Client) CheckAccess(ctx context.Context, host, basePath string) (AccessResult, error) {
	if err := ctx.Err(); err != nil {
		return AccessResult{}, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	cleanBase := strings.TrimRight(strings.ReplaceAll(basePath, "\\", "/"), "/")
	qBase := transport.POSIXEscape(cleanBase)
	probeScript := fmt.Sprintf(
		"if [ ! -d %s ]; then mkdir -p -- %s 2>/dev/null && echo 'CREATED:1'; fi; "+
			"if [ ! -d %s ]; then echo 'ERROR:NOT_DIR' >&2; exit 42; fi; "+
			"if [ ! -r %s ] || [ ! -x %s ]; then echo 'ERROR:NOT_READABLE' >&2; exit 43; fi; "+
			"if [ -w %s ]; then echo 'ACCESS:rw'; else echo 'ACCESS:ro'; fi",
		qBase, qBase, qBase, qBase, qBase, qBase,
	)

	out, err := c.ssh.Run(ctx, host, probeScript)
	if err != nil {
		if ctx.Err() != nil {
			return AccessResult{}, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "Operation interrupted")
		}
		if code, ok := command.ExitCodeOf(err); ok {
			switch code {
			case 42:
				return AccessResult{}, errcode.NewError(errcode.ErrTransport, "Remote directory %q does not exist and could not be created on host %q", basePath, host).
					WithHint("Ensure the remote path exists and is accessible via SSH.")
			case 43:
				return AccessResult{}, errcode.NewError(errcode.ErrTransport, "Remote directory %q on host %q is not readable or accessible", basePath, host).
					WithHint("Verify directory permissions (read and execute) for the SSH user.")
			}
		}
		return AccessResult{}, errcode.NewError(errcode.ErrTransport, "SSH check failed on host %q: %s", host, command.StderrOrError(err)).
			WithHint("Verify SSH connectivity and authentication for %s.", host)
	}

	res := AccessResult{
		Readable: true,
		Writable: strings.Contains(out, "ACCESS:rw"),
		Created:  strings.Contains(out, "CREATED:1"),
	}
	return res, nil
}

// TransferRequest configures file synchronization between local and remote storage.
type TransferRequest struct {
	Host        string
	BasePath    string
	UUID        string
	LocalDir    string
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
			return errcode.NewError(errcode.ErrConfig, "Rsync option %q is not allowed in remote.rsync.put because it mutates hardlinked baseline revisions in-place", opt).
				WithHint("Remove %q from remote.rsync.put so rsync replaces modified files atomically.", opt)
		}
	}
	return nil
}

// Download synchronizes files from remote storage to local filesystem.
func (c *Client) Download(ctx context.Context, req TransferRequest) error {
	extraArgs, err := transport.TokenizeOptions(req.UserOptions)
	if err != nil {
		return err
	}

	return c.rsync.Sync(ctx, transport.RsyncOptions{
		Host:       req.Host,
		RemotePath: RevisionDir(req.BasePath, req.UUID),
		LocalPath:  NormalizeDir(req.LocalDir),
		IsUpload:   false,
		Verbose:    req.Verbose,
		Quiet:      req.Quiet,
		Stats:      req.Stats,
		ExtraArgs:  extraArgs,
		Stdout:     req.Stdout,
		Stderr:     req.Stderr,
	})
}

// Upload synchronizes files from local filesystem to remote storage.
func (c *Client) Upload(ctx context.Context, req TransferRequest) error {
	extraArgs, err := transport.TokenizeOptions(req.UserOptions)
	if err != nil {
		return err
	}
	if err := ValidateUploadOptions(extraArgs); err != nil {
		return err
	}

	return c.rsync.Sync(ctx, transport.RsyncOptions{
		Host:       req.Host,
		RemotePath: RevisionDir(req.BasePath, req.UUID),
		LocalPath:  NormalizeDir(req.LocalDir),
		IsUpload:   true,
		Verbose:    req.Verbose,
		Quiet:      req.Quiet,
		Stats:      req.Stats,
		ExtraArgs:  extraArgs,
		Stdout:     req.Stdout,
		Stderr:     req.Stderr,
	})
}
