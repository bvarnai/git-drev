package checks_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/bvarnai/git-drev/internal/checks"
	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRunner struct {
	runFn func(ctx context.Context, dir string, args ...string) (string, error)
}

func (m *mockRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if m.runFn != nil {
		return m.runFn(ctx, dir, args...)
	}
	return "", nil
}

func (m *mockRunner) RunLines(ctx context.Context, dir string, args ...string) ([]string, error) {
	out, err := m.Run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return []string{out}, nil
}

func (m *mockRunner) RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	return m.Run(ctx, dir, args...)
}

func (m *mockRunner) RunStream(ctx context.Context, dir string, stdout, stderr io.Writer, extraEnv []string, args ...string) error {
	return nil
}

func TestInsideWorkTree(t *testing.T) {
	t.Run("valid worktree", func(t *testing.T) {
		runner := &mockRunner{
			runFn: func(ctx context.Context, dir string, args ...string) (string, error) {
				return "true\n/repo/root\nsubdir/", nil
			},
		}
		insp := git.NewInspector(runner)
		root, prefix, err := checks.InsideWorkTree(context.Background(), insp, ".")
		require.NoError(t, err)
		assert.Equal(t, filepath.FromSlash("/repo/root"), root)
		assert.Equal(t, filepath.FromSlash("subdir/"), prefix)
	})

	t.Run("not a worktree", func(t *testing.T) {
		runner := &mockRunner{
			runFn: func(ctx context.Context, dir string, args ...string) (string, error) {
				return "", errcode.NewError(errcode.ErrNotRepository, "fatal: not a git repo")
			},
		}
		insp := git.NewInspector(runner)
		_, _, err := checks.InsideWorkTree(context.Background(), insp, ".")
		require.Error(t, err)
		assert.ErrorIs(t, err, errcode.ErrNotRepository)
	})
}

func TestAttachedBranch(t *testing.T) {
	t.Run("attached branch", func(t *testing.T) {
		runner := &mockRunner{
			runFn: func(ctx context.Context, dir string, args ...string) (string, error) {
				return "main", nil
			},
		}
		insp := git.NewInspector(runner)
		branch, err := checks.AttachedBranch(context.Background(), insp, "/repo")
		require.NoError(t, err)
		assert.Equal(t, "main", branch)
	})

	t.Run("detached HEAD", func(t *testing.T) {
		runner := &mockRunner{
			runFn: func(ctx context.Context, dir string, args ...string) (string, error) {
				return "", &command.Error{ExitCode: 1, Category: errcode.ErrGitExec}
			},
		}
		insp := git.NewInspector(runner)
		_, err := checks.AttachedBranch(context.Background(), insp, "/repo")
		require.Error(t, err)
		assert.ErrorIs(t, err, errcode.ErrDetachedHead)
		assert.Equal(t, `Use "git-drev select <branch>" to select a branch first.`, errcode.Hint(err))
	})
}

func TestNoIndexLock(t *testing.T) {
	tmp := t.TempDir()
	gitDir := filepath.Join(tmp, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0755))

	// Clean index
	require.NoError(t, checks.NoIndexLock(tmp))

	// Locked index
	lockFile := filepath.Join(gitDir, "index.lock")
	require.NoError(t, os.WriteFile(lockFile, []byte("lock"), 0644))
	err := checks.NoIndexLock(tmp)
	require.Error(t, err)
	assert.ErrorIs(t, err, errcode.ErrGitState)
}

func TestRemoteConfigured(t *testing.T) {
	assert.ErrorIs(t, checks.RemoteConfigured(nil), errcode.ErrConfig)

	cfg := &domain.ProjectConfig{}
	assert.ErrorIs(t, checks.RemoteConfigured(cfg), errcode.ErrConfig)

	cfg.Remote.Host = "my-host"
	assert.ErrorIs(t, checks.RemoteConfigured(cfg), errcode.ErrConfig)

	cfg.Remote.Path = "/storage/path"
	assert.NoError(t, checks.RemoteConfigured(cfg))
}

func TestActiveOperation(t *testing.T) {
	tmp := t.TempDir()
	gitDir := filepath.Join(tmp, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0755))

	// No active operation
	assert.NoError(t, checks.NoActiveOperation(tmp))

	// Merge active
	mergeHead := filepath.Join(gitDir, "MERGE_HEAD")
	require.NoError(t, os.WriteFile(mergeHead, []byte("123"), 0644))
	err := checks.NoActiveOperation(tmp)
	require.Error(t, err)
	assert.ErrorIs(t, err, errcode.ErrGitState)
}
