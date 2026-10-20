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
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/transport"
)

// LocalRemoteRunner implements storage.Manager using the local filesystem
// and local rsync for fast, self-contained integration tests in CI (GitHub Actions).
type LocalRemoteRunner struct {
	BaseDir string
}

// NewLocalRemoteRunner creates a new LocalRemoteRunner rooted in baseDir.
func NewLocalRemoteRunner(baseDir string) *LocalRemoteRunner {
	return &LocalRemoteRunner{BaseDir: baseDir}
}

// HasRevision tests whether a revision directory exists on disk.
func (r *LocalRemoteRunner) HasRevision(ctx context.Context, host, basePath, uuid string) (bool, error) {
	p := filepath.FromSlash(storage.RevisionPath(basePath, uuid))
	fi, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return fi.IsDir(), nil
}

// CloneBaseline copies srcUUID to dstUUID preserving timestamps and permissions via `cp -al --`.
func (r *LocalRemoteRunner) CloneBaseline(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
	srcPath := filepath.FromSlash(storage.RevisionPath(basePath, srcUUID))
	dstPath := filepath.FromSlash(storage.RevisionPath(basePath, dstUUID))
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "cp", "-al", "--", srcPath, dstPath)
	return cmd.Run()
}

// RemoveRevision removes the revision directory and all its contents.
func (r *LocalRemoteRunner) RemoveRevision(ctx context.Context, host, basePath, uuid string) error {
	p := filepath.FromSlash(storage.RevisionPath(basePath, uuid))
	return os.RemoveAll(p)
}

// QueryUsage calculates storage usage of basePath and discovers valid UUID subdirectories.
func (r *LocalRemoteRunner) QueryUsage(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
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

// Download executes a local rsync transfer from remotePath to localPath.
func (r *LocalRemoteRunner) Download(ctx context.Context, req storage.TransferRequest) error {
	remotePath := filepath.FromSlash(storage.RevisionDir(req.BasePath, req.UUID))
	localPath := filepath.FromSlash(storage.NormalizeDir(req.LocalDir))

	args := []string{"-a", "--delete-during"}
	if req.UserOptions != "" {
		opts, err := transport.TokenizeOptions(req.UserOptions)
		if err != nil {
			return err
		}
		args = append(args, opts...)
	}
	args = append(args, remotePath, localPath)

	cmd := exec.CommandContext(ctx, "rsync", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return errcode.NewError(errcode.ErrTransport, "rsync get failed: %s: %v", errBuf.String(), err)
	}
	return nil
}

// Upload executes a local rsync transfer from localPath to remotePath.
func (r *LocalRemoteRunner) Upload(ctx context.Context, req storage.TransferRequest) error {
	remotePath := filepath.FromSlash(storage.RevisionDir(req.BasePath, req.UUID))
	localPath := filepath.FromSlash(storage.NormalizeDir(req.LocalDir))

	if err := os.MkdirAll(filepath.Dir(strings.TrimRight(remotePath, "/\\")), 0o755); err != nil {
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
	args = append(args, localPath, remotePath)

	cmd := exec.CommandContext(ctx, "rsync", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return errcode.NewError(errcode.ErrTransport, "rsync put failed: %s: %v", errBuf.String(), err)
	}
	return nil
}
