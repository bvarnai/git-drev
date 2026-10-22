package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/transport"
)

// LocalRemoteRunner implements storage.Manager using pure Go local filesystem
// operations (including os.Link hardlinking) so client-side integration tests
// run self-contained across Linux, macOS, and Windows in CI.
type LocalRemoteRunner struct {
	BaseDir    string
	StorageDir string
	RemotePath string
}

// NewLocalRemoteRunner creates a new LocalRemoteRunner rooted in baseDir.
func NewLocalRemoteRunner(baseDir string, storageAndRemote ...string) *LocalRemoteRunner {
	r := &LocalRemoteRunner{BaseDir: baseDir}
	if len(storageAndRemote) >= 1 {
		r.StorageDir = storageAndRemote[0]
	}
	if len(storageAndRemote) >= 2 {
		r.RemotePath = storageAndRemote[1]
	}
	return r
}

func (r *LocalRemoteRunner) resolveBasePath(basePath string) string {
	trimmed := strings.TrimRight(basePath, "/")
	if r.RemotePath != "" && trimmed == strings.TrimRight(r.RemotePath, "/") && r.StorageDir != "" {
		return r.StorageDir
	}
	return filepath.FromSlash(trimmed)
}

func (r *LocalRemoteRunner) resolveRevisionPath(basePath, uuid string) string {
	return filepath.Join(r.resolveBasePath(basePath), strings.Trim(uuid, "/\\"))
}

// HasRevision tests whether a revision directory exists on disk.
func (r *LocalRemoteRunner) HasRevision(ctx context.Context, host, basePath, uuid string) (bool, error) {
	p := r.resolveRevisionPath(basePath, uuid)
	fi, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return fi.IsDir(), nil
}

// CloneBaseline copies srcUUID to dstUUID by hardlinking all regular files via os.Link.
func (r *LocalRemoteRunner) CloneBaseline(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
	srcPath := r.resolveRevisionPath(basePath, srcUUID)
	dstPath := r.resolveRevisionPath(basePath, dstUUID)

	return filepath.WalkDir(srcPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcPath, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstPath, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.Link(path, target)
	})
}

// RemoveRevision removes the revision directory and all its contents.
func (r *LocalRemoteRunner) RemoveRevision(ctx context.Context, host, basePath, uuid string) error {
	p := r.resolveRevisionPath(basePath, uuid)
	return os.RemoveAll(p)
}

// QueryUsage calculates storage usage of basePath and discovers valid UUID subdirectories.
func (r *LocalRemoteRunner) QueryUsage(ctx context.Context, host, basePath string, opts ...storage.QueryUsageOptions) (string, []domain.RemoteRevision, error) {
	resolvedBase := r.resolveBasePath(basePath)
	fi, err := os.Stat(resolvedBase)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path %q does not exist on host %q", basePath, host)
		}
		return "", nil, errcode.WrapError(errcode.ErrTransport, err, "failed to stat remote path %q", basePath)
	}
	if !fi.IsDir() {
		return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path %q is not a directory", basePath)
	}

	totalSize := formatDirSize(resolvedBase)

	entries, err := os.ReadDir(resolvedBase)
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

		revPath := filepath.Join(resolvedBase, name)
		revSize := formatDirSize(revPath)

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

// Download executes a local directory sync from remotePath to localPath.
func (r *LocalRemoteRunner) Download(ctx context.Context, req storage.TransferRequest) error {
	remotePath := r.resolveRevisionPath(req.BasePath, req.UUID)
	localPath := filepath.FromSlash(strings.TrimRight(storage.NormalizeDir(req.LocalDir), "/\\"))

	if req.UserOptions != "" {
		if _, err := transport.TokenizeOptions(req.UserOptions); err != nil {
			return err
		}
	}

	if err := syncDirectoryTree(remotePath, localPath); err != nil {
		return errcode.NewError(errcode.ErrTransport, "rsync get failed: %v", err)
	}
	return nil
}

// Upload executes a local directory sync from localPath to remotePath.
func (r *LocalRemoteRunner) Upload(ctx context.Context, req storage.TransferRequest) error {
	remotePath := r.resolveRevisionPath(req.BasePath, req.UUID)
	localPath := filepath.FromSlash(strings.TrimRight(storage.NormalizeDir(req.LocalDir), "/\\"))

	if req.UserOptions != "" {
		opts, err := transport.TokenizeOptions(req.UserOptions)
		if err != nil {
			return err
		}
		if err := storage.ValidateUploadOptions(opts); err != nil {
			return err
		}
	}

	if err := syncDirectoryTree(localPath, remotePath); err != nil {
		return errcode.NewError(errcode.ErrTransport, "rsync put failed: %v", err)
	}
	return nil
}

func formatDirSize(dir string) string {
	var total int64
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, infoErr := d.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return fmt.Sprintf("%dB", total)
}

func syncDirectoryTree(srcDir, dstDir string) error {
	fi, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("source %q is not a directory", srcDir)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}

	// Remove extraneous entries in dstDir not present in srcDir (mirrors --delete-during).
	if err := pruneExtraneous(srcDir, dstDir); err != nil {
		return err
	}

	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		srcData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if dstData, err := os.ReadFile(dstPath); err == nil && bytes.Equal(srcData, dstData) {
			// Leave unchanged files untouched so baseline hardlinks remain intact.
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		// Remove existing destination first so hardlinks to parent snapshots are broken, not mutated in-place.
		_ = os.Remove(dstPath)

		perm := fs.FileMode(0o644)
		if info, err := d.Info(); err == nil {
			perm = info.Mode().Perm()
		}
		return os.WriteFile(dstPath, srcData, perm)
	})
}

func pruneExtraneous(srcDir, dstDir string) error {
	entries, err := os.ReadDir(dstDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		srcEntry := filepath.Join(srcDir, entry.Name())
		dstEntry := filepath.Join(dstDir, entry.Name())
		srcInfo, err := os.Stat(srcEntry)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if rmErr := os.RemoveAll(dstEntry); rmErr != nil {
					return rmErr
				}
				continue
			}
			return err
		}
		if entry.IsDir() && srcInfo.IsDir() {
			if err := pruneExtraneous(srcEntry, dstEntry); err != nil {
				return err
			}
		} else if entry.IsDir() != srcInfo.IsDir() {
			if rmErr := os.RemoveAll(dstEntry); rmErr != nil {
				return rmErr
			}
		}
	}
	return nil
}

// CheckAccess probes the local filesystem directory corresponding to basePath.
func (r *LocalRemoteRunner) CheckAccess(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
	if err := ctx.Err(); err != nil {
		return storage.AccessResult{}, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	p := r.resolveBasePath(basePath)
	created := false
	fi, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if mkErr := os.MkdirAll(p, 0o755); mkErr == nil {
				created = true
				fi, err = os.Stat(p)
			}
		}
	}
	if err != nil || !fi.IsDir() {
		return storage.AccessResult{}, errcode.NewError(errcode.ErrTransport, "Remote directory %q does not exist and could not be created", basePath)
	}

	// Test write access by writing a temporary probe file
	probeFile := filepath.Join(p, ".probe-access")
	writable := true
	if writeErr := os.WriteFile(probeFile, []byte("ok"), 0o644); writeErr != nil {
		writable = false
	} else {
		_ = os.Remove(probeFile)
	}

	return storage.AccessResult{
		Readable: true,
		Writable: writable,
		Created:  created,
	}, nil
}
