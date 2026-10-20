package integration

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/transport"
)

// LocalRemoteRunner implements transport.RemoteRunner using the local filesystem
// and local rsync for fast, self-contained integration tests in CI (GitHub Actions).
type LocalRemoteRunner struct {
	BaseDir string
}

// NewLocalRemoteRunner creates a new LocalRemoteRunner rooted in baseDir.
func NewLocalRemoteRunner(baseDir string) *LocalRemoteRunner {
	return &LocalRemoteRunner{BaseDir: baseDir}
}

// CheckDirExists tests whether remotePath is an existing directory on disk.
func (r *LocalRemoteRunner) CheckDirExists(ctx context.Context, host, remotePath string) (bool, error) {
	fi, err := os.Stat(remotePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return fi.IsDir(), nil
}

// CloneBaseline copies srcPath to dstPath preserving timestamps and permissions via `cp -R -a`.
func (r *LocalRemoteRunner) CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "cp", "-R", "-a", srcPath, dstPath)
	return cmd.Run()
}

// RemoveDir removes remotePath and all its contents.
func (r *LocalRemoteRunner) RemoveDir(ctx context.Context, host, remotePath string) error {
	return os.RemoveAll(remotePath)
}

// QueryUsage calculates storage usage of basePath and discovers valid UUID subdirectories.
func (r *LocalRemoteRunner) QueryUsage(ctx context.Context, host, basePath string, opts ...transport.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
	fi, err := os.Stat(basePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path %q does not exist on host %q", basePath, host)
		}
		return "", nil, errcode.WrapError(errcode.ErrTransport, err, "failed to stat remote path %q", basePath)
	}
	if !fi.IsDir() {
		return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path %q is not a directory", basePath)
	}

	totalSize := "-"
	duCmd := exec.CommandContext(ctx, "du", "-sh", "--", basePath)
	var duOut bytes.Buffer
	duCmd.Stdout = &duOut
	if err := duCmd.Run(); err == nil {
		parts := strings.Fields(duOut.String())
		if len(parts) > 0 {
			totalSize = parts[0]
		}
	}

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return totalSize, nil, nil
	}

	var revs []domain.RemoteRevision
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		normUUID, ok := domain.ParseUUID(name)
		if !ok {
			continue
		}

		revPath := filepath.Join(basePath, name)
		revSize := "-"
		subDu := exec.CommandContext(ctx, "du", "-sh", "--", revPath)
		var subOut bytes.Buffer
		subDu.Stdout = &subOut
		if err := subDu.Run(); err == nil {
			parts := strings.Fields(subOut.String())
			if len(parts) > 0 {
				revSize = parts[0]
			}
		}

		var mtime time.Time
		if info, err := entry.Info(); err == nil {
			mtime = info.ModTime()
		}

		revs = append(revs, domain.RemoteRevision{
			UUID:      normUUID,
			SizeHuman: revSize,
			ModTime:   mtime,
		})
	}

	return totalSize, revs, nil
}

// RsyncGet executes a local rsync transfer from remotePath to localPath.
func (r *LocalRemoteRunner) RsyncGet(ctx context.Context, req transport.SyncRequest) error {
	args := []string{"-a", "--delete-during"}
	if req.UserOptions != "" {
		opts, err := transport.TokenizeOptions(req.UserOptions)
		if err != nil {
			return err
		}
		args = append(args, opts...)
	}
	args = append(args, req.RemotePath, req.LocalPath)

	cmd := exec.CommandContext(ctx, "rsync", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return errcode.NewError(errcode.ErrTransport, "rsync get failed: %s: %v", errBuf.String(), err)
	}
	return nil
}

// RsyncPut executes a local rsync transfer from localPath to remotePath.
func (r *LocalRemoteRunner) RsyncPut(ctx context.Context, req transport.SyncRequest) error {
	if err := os.MkdirAll(filepath.Dir(strings.TrimRight(req.RemotePath, "/")), 0o755); err != nil {
		return errcode.WrapError(errcode.ErrTransport, err, "failed to create destination directory")
	}

	args := []string{"-a", "--delete-during"}
	if req.UserOptions != "" {
		opts, err := transport.TokenizeOptions(req.UserOptions)
		if err != nil {
			return err
		}
		args = append(args, opts...)
	}
	args = append(args, req.LocalPath, req.RemotePath)

	cmd := exec.CommandContext(ctx, "rsync", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return errcode.NewError(errcode.ErrTransport, "rsync put failed: %s: %v", errBuf.String(), err)
	}
	return nil
}
