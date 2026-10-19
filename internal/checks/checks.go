package checks

import (
	"context"
	"os"
	"path/filepath"

	"git-drev/internal/domain"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
)

// InsideWorkTree ensures the directory is inside a valid Git work tree and returns its root path and relative prefix.
func InsideWorkTree(ctx context.Context, insp *git.Inspector, dir string) (root, prefix string, err error) {
	if insp == nil {
		return "", "", errcode.NewError(errcode.ErrNotRepository, "Not inside a Git working tree")
	}
	root, prefix, err = insp.RepoRoot(ctx, dir)
	if err != nil {
		return "", "", err
	}
	return root, prefix, nil
}

// AttachedBranch ensures the repository is on a named branch and not in a detached HEAD state.
func AttachedBranch(ctx context.Context, insp *git.Inspector, repoRoot string) (string, error) {
	if insp == nil {
		return "", errcode.NewError(errcode.ErrGitState, "git inspector not initialized")
	}
	branch, detached, err := insp.CurrentBranch(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	if detached || branch == "" {
		return "", errcode.NewError(errcode.ErrDetachedHead, "Detached HEAD state detected, aborting").
			WithHint(`Use "git-drev select <branch>" to select a branch first.`)
	}
	return branch, nil
}

// NoIndexLock verifies that no Git index lock (.git/index.lock) is present.
func NoIndexLock(repoRoot string) error {
	lockPath := filepath.Join(repoRoot, ".git", "index.lock")
	if _, err := os.Stat(lockPath); err == nil {
		return errcode.NewError(errcode.ErrGitState, "Git index is locked ('.git/index.lock' exists)")
	}
	return nil
}

// RemoteConfigured verifies that remote host and remote path are set in the configuration.
func RemoteConfigured(cfg *domain.ProjectConfig) error {
	if cfg == nil {
		return errcode.NewError(errcode.ErrConfig, "configuration is nil")
	}
	if cfg.Remote.Host == "" {
		return errcode.NewError(errcode.ErrConfig, "remote host is not configured")
	}
	if cfg.Remote.Path == "" {
		return errcode.NewError(errcode.ErrConfig, "remote path is not configured")
	}
	return nil
}

// ActiveOperation checks if a rebase, merge, cherry-pick, revert, or bisect is in progress.
func ActiveOperation(repoRoot string) (string, bool) {
	gitDir := filepath.Join(repoRoot, ".git")

	checks := []struct {
		path string
		op   string
	}{
		{filepath.Join(gitDir, "rebase-merge"), "rebase"},
		{filepath.Join(gitDir, "rebase-apply"), "rebase"},
		{filepath.Join(gitDir, "MERGE_HEAD"), "merge"},
		{filepath.Join(gitDir, "CHERRY_PICK_HEAD"), "cherry-pick"},
		{filepath.Join(gitDir, "REVERT_HEAD"), "revert"},
		{filepath.Join(gitDir, "BISECT_LOG"), "bisect"},
	}

	for _, c := range checks {
		if _, err := os.Stat(c.path); err == nil {
			return c.op, true
		}
	}
	return "", false
}

// NoActiveOperation asserts that no rebase, merge, cherry-pick, revert, or bisect operation is in flight.
func NoActiveOperation(repoRoot string) error {
	if op, active := ActiveOperation(repoRoot); active {
		return errcode.NewError(errcode.ErrGitState, "Cannot execute during an active %s", op).
			WithHint("Complete or abort the in-progress operation first")
	}
	return nil
}
