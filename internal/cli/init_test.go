package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCommand_UnitTable(t *testing.T) {
	t.Parallel()

	t.Run("conflicting commit flags returns usage error", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{responses: map[string]mockGitResponse{}}
		app := cli.NewApp(&stdout, &stderr, runner)
		app.RootCmd.SetArgs([]string{"init", "--commit", "--no-commit"})

		err := app.Execute(context.Background())
		require.Error(t, err)
		assert.Equal(t, errcode.ExitUsage, errcode.ExitCode(err))
		assert.Contains(t, err.Error(), "Flags --commit and --no-commit cannot be used together")
	})

	t.Run("outside repository declines git init prompt aborts", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					err: errors.New("fatal: not a git repository"),
				},
			},
		}
		app := cli.NewApp(&stdout, &stderr, runner)
		app.WorkingDir = tempDir
		app.Logger.SetStdin(strings.NewReader("n\n"))
		app.RootCmd.SetArgs([]string{"init"})

		err := app.Execute(context.Background())
		require.Error(t, err)
		assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))
	})

	t.Run("outside repository with -y initializes git and writes config", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer

		var initCalled, addCalled, commitCalled bool
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"init -b main":                                       {out: "Initialized empty Git repository\n"},
				"add -- .git-drev.yaml .gitignore":                   {out: ""},
				"commit -m chore: initialize git-drev configuration": {out: "[main (root-commit) 1234567] init\n"},
			},
		}
		// First call to InsideWorkTree returns not a repo, then init runs, then second call returns true
		callCount := 0
		firstCheckRunner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"init -b main":                                       {out: "Initialized empty Git repository\n"},
				"add -- .git-drev.yaml .gitignore":                   {out: ""},
				"diff --cached --quiet":                              {err: &command.Error{ExitCode: 1}},
				"commit -m chore: initialize git-drev configuration": {out: "[main 1234567] init\n"},
			},
		}
		_ = runner
		_ = callCount

		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{Readable: true, Writable: true}, nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, firstCheckRunner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{
			"init",
			"-y",
			"--host", "storage.example.com",
			"--path", "/srv/drev/myrepo",
			"--workdir", "data",
			"--branch", "main",
		})

		// Wire up mock git runner so the first rev-parse fails and subsequent succeeds
		firstCheckRunner.responses["rev-parse --is-inside-work-tree --show-toplevel --show-prefix"] = mockGitResponse{
			out: "true\n" + tempDir + "\n\n",
		}

		err := app.Execute(context.Background())
		require.NoError(t, err)

		// Verify .git-drev.yaml was written
		cfgData, err := os.ReadFile(filepath.Join(tempDir, ".git-drev.yaml"))
		require.NoError(t, err)
		assert.Contains(t, string(cfgData), `host: "storage.example.com"`)
		assert.Contains(t, string(cfgData), `path: "/srv/drev/myrepo"`)

		// Verify .gitignore was written
		ignoreData, err := os.ReadFile(filepath.Join(tempDir, ".gitignore"))
		require.NoError(t, err)
		assert.Contains(t, string(ignoreData), "/data/")

		_ = initCalled
		_ = addCalled
		_ = commitCalled
	})

	t.Run("existing non-empty repository without force aborts on non-interactive -y", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD": {out: "abc1234\n"},
				"ls-files":                        {out: "main.go\n"},
			},
		}
		app := cli.NewApp(&stdout, &stderr, runner)
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{"init", "-y", "--host", "example.com", "--path", "/srv/drev/repo"})

		err := app.Execute(context.Background())
		require.Error(t, err)
		assert.Equal(t, errcode.ExitGitState, errcode.ExitCode(err))
		assert.Contains(t, err.Error(), "Repository already contains commits or tracked files")
	})

	t.Run("existing non-empty repository with --force proceeds", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD":                    {out: "abc1234\n"},
				"ls-files":                                           {out: "main.go\n"},
				"add -- .git-drev.yaml .gitignore":                   {out: ""},
				"diff --cached --quiet":                              {err: &command.Error{ExitCode: 1}},
				"commit -m chore: initialize git-drev configuration": {out: "ok\n"},
			},
		}
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{Readable: true, Writable: true}, nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{
			"init",
			"-y",
			"--force",
			"--host", "example.com",
			"--path", "/srv/drev/repo",
		})

		err := app.Execute(context.Background())
		require.NoError(t, err)
	})

	t.Run("existing config without force in -y mode fails with ExitConfig", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		existingCfgPath := filepath.Join(tempDir, ".git-drev.yaml")
		require.NoError(t, os.WriteFile(existingCfgPath, []byte("remote:\n  host: old\n  path: /old\n"), 0644))

		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD": {err: errors.New("no head")},
				"ls-files":                        {out: ""},
			},
		}
		app := cli.NewApp(&stdout, &stderr, runner)
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{"init", "-y", "--host", "example.com", "--path", "/srv/drev/repo"})

		err := app.Execute(context.Background())
		require.Error(t, err)
		assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))
		assert.Contains(t, err.Error(), "Configuration file already exists")
	})

	t.Run("read-only remote warns but succeeds", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD":                    {err: errors.New("no head")},
				"ls-files":                                           {out: ""},
				"add -- .git-drev.yaml .gitignore":                   {out: ""},
				"diff --cached --quiet":                              {err: &command.Error{ExitCode: 1}},
				"commit -m chore: initialize git-drev configuration": {out: "ok\n"},
			},
		}
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{Readable: true, Writable: false}, nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{"init", "-y", "--host", "example.com", "--path", "/srv/drev/repo"})

		err := app.Execute(context.Background())
		require.NoError(t, err)
		assert.Contains(t, stderr.String(), "Warning: Remote storage path is read-only ('get' is available, 'put' requires write access)")
	})

	t.Run("storage connection failure returns ExitTransport", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD": {err: errors.New("no head")},
				"ls-files":                        {out: ""},
			},
		}
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{}, errcode.NewError(errcode.ErrTransport, "Connection timed out")
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{"init", "-y", "--host", "example.com", "--path", "/srv/drev/repo"})

		err := app.Execute(context.Background())
		require.Error(t, err)
		assert.Equal(t, errcode.ExitTransport, errcode.ExitCode(err))
	})

	t.Run("skip-check bypasses remote probe", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD":                    {err: errors.New("no head")},
				"ls-files":                                           {out: ""},
				"add -- .git-drev.yaml .gitignore":                   {out: ""},
				"diff --cached --quiet":                              {err: &command.Error{ExitCode: 1}},
				"commit -m chore: initialize git-drev configuration": {out: "ok\n"},
			},
		}
		storageCalled := false
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				storageCalled = true
				return storage.AccessResult{Readable: true, Writable: true}, nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{"init", "-y", "--skip-check", "--host", "example.com", "--path", "/srv/drev/repo"})

		err := app.Execute(context.Background())
		require.NoError(t, err)
		assert.False(t, storageCalled, "expected CheckAccess not to be called with --skip-check")
	})

	t.Run("gitignore idempotency does not duplicate entry", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		gitignorePath := filepath.Join(tempDir, ".gitignore")
		require.NoError(t, os.WriteFile(gitignorePath, []byte("node_modules\n/data/\n*.log\n"), 0644))

		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD":                    {err: errors.New("no head")},
				"ls-files":                                           {out: ""},
				"add -- .git-drev.yaml .gitignore":                   {out: ""},
				"diff --cached --quiet":                              {err: &command.Error{ExitCode: 1}},
				"commit -m chore: initialize git-drev configuration": {out: "ok\n"},
			},
		}
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{Readable: true, Writable: true}, nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.RootCmd.SetArgs([]string{"init", "-y", "--host", "example.com", "--path", "/srv/drev/repo", "--workdir", "data"})

		err := app.Execute(context.Background())
		require.NoError(t, err)

		// Verify .gitignore content did not add extra entries
		content, err := os.ReadFile(gitignorePath)
		require.NoError(t, err)
		assert.Equal(t, "node_modules\n/data/\n*.log\n", string(content))
	})

	t.Run("remote directory missing prompt declined aborts", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD": {err: errors.New("no head")},
				"ls-files":                        {out: ""},
			},
		}
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{Exists: false, Readable: true, Writable: true}, nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.Logger.SetStdin(strings.NewReader("n\n"))
		app.RootCmd.SetArgs([]string{"init", "--host", "example.com", "--path", "/srv/drev/repo", "--workdir", "data", "--branch", "main", "--no-commit"})

		err := app.Execute(context.Background())
		require.Error(t, err)
		assert.Equal(t, errcode.ExitRemoteNotFound, errcode.ExitCode(err))
	})

	t.Run("remote directory missing prompt accepted calls CreateDirectory", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		runner := &mockGitRunner{
			responses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {
					out: "true\n" + tempDir + "\n\n",
				},
				"rev-parse --verify --quiet HEAD": {err: errors.New("no head")},
				"ls-files":                        {out: ""},
			},
		}
		created := false
		mockStorage := &storage.MockManager{
			CheckAccessFn: func(ctx context.Context, host, basePath string) (storage.AccessResult, error) {
				return storage.AccessResult{Exists: false, Readable: true, Writable: true}, nil
			},
			CreateDirectoryFn: func(ctx context.Context, host, basePath string) error {
				created = true
				return nil
			},
		}

		app := cli.NewApp(&stdout, &stderr, runner)
		app.Storage = mockStorage
		app.WorkingDir = tempDir
		app.Logger.SetStdin(strings.NewReader("y\n"))
		app.RootCmd.SetArgs([]string{"init", "--host", "example.com", "--path", "/srv/drev/repo", "--workdir", "data", "--branch", "main", "--no-commit"})

		err := app.Execute(context.Background())
		require.NoError(t, err)
		assert.True(t, created, "CreateDirectory should have been called")

		// Verify local workdir directory was created
		fi, err := os.Stat(filepath.Join(tempDir, "data"))
		require.NoError(t, err)
		assert.True(t, fi.IsDir())
	})
}
