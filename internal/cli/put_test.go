package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutCommand_UnitTable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ".git-drev.yaml")
	cfgContent := `
workdir: data
remote:
  host: backup.example.com
  path: /storage/myrepo
`
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0o644))

	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "file.txt"), []byte("hello"), 0o644))

	tests := []struct {
		name          string
		args          []string
		setupFS       func(t *testing.T, dir string)
		mockGit       map[string]mockGitResponse
		mockStorage   *storage.MockManager
		mockHooks     *mockHookRunner
		expectedCode  int
		errSubstr     string
		checkExecuted func(t *testing.T, executed []string, storageCalls []string)
		checkStderr   func(t *testing.T, stderr string)
	}{
		{
			name:         "excess arguments returns usage error",
			args:         []string{"put", "dir1", "dir2"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Too many arguments",
		},
		{
			name: "detached HEAD returns ExitGitState/ErrDetachedHead",
			args: []string{"put"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {err: &command.Error{ExitCode: 1, Category: errcode.ErrGitExec}},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "Detached HEAD state detected, aborting",
		},
		{
			name: "locked index returns ErrGitState",
			args: []string{"put"},
			setupFS: func(t *testing.T, dir string) {
				gitDir := filepath.Join(dir, ".git")
				_ = os.MkdirAll(gitDir, 0o755)
				_ = os.WriteFile(filepath.Join(gitDir, "index.lock"), []byte{}, 0o644)
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "Git index is locked",
		},
		{
			name: "missing source directory returns usage error",
			args: []string{"put", "nonexistent-dir"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
			},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "does not exist or is not a directory",
		},
		{
			name: "invalid non-numeric sequence returns usage error",
			args: []string{"put", "-s", "abc"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
			},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Invalid sequence \"abc\"",
		},
		{
			name: "successful put uploads, runs hook, commits and pushes",
			args: []string{"put", "-s", "100"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"50"}`},
				"add --all":                                                     {out: ""},
				"reset -- data":                                                 {out: ""},
				"commit --allow-empty -m *":                                     {out: "[main a1b2c3d] revision"},
				"rev-parse --verify --quiet HEAD^{commit}":                      {out: "84bb88d1234567890abcdef1234567890abcdef1"},
				"push --force-with-lease origin refs/heads/main":                {out: "Everything up-to-date"},
			},
			mockStorage: &storage.MockManager{
				CloneBaselineFn: func(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
					assert.Equal(t, "backup.example.com", host)
					assert.Equal(t, "/storage/myrepo", basePath)
					assert.Equal(t, "11111111-1111-4111-8111-111111111111", srcUUID)
					return nil
				},
				UploadFn: func(ctx context.Context, req storage.TransferRequest) error {
					assert.Equal(t, "backup.example.com", req.Host)
					assert.Equal(t, "/storage/myrepo", req.BasePath)
					return nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, storageCalls []string) {
				assert.Contains(t, executed, "add --all")
				assert.Contains(t, executed, "reset -- data")
				assert.Contains(t, executed, "push --force-with-lease origin refs/heads/main")
				var foundClone, foundUpload bool
				for _, c := range storageCalls {
					if len(c) >= 13 && c[:13] == "CloneBaseline" {
						foundClone = true
					}
					if len(c) >= 6 && c[:6] == "Upload" {
						foundUpload = true
					}
				}
				assert.True(t, foundClone, "expected CloneBaseline call")
				assert.True(t, foundUpload, "expected Upload call")
			},
			checkStderr: func(t *testing.T, stderr string) {
				assert.Regexp(t, regexp.MustCompile(`\[git-drev\] Uploading directory revision '[0-9a-f]{8}\.\.\.' to host 'backup\.example\.com'\.\.\.`), stderr)
				assert.Regexp(t, regexp.MustCompile(`\[git-drev\] Uploaded directory revision '[0-9a-f]{8}\.\.\.' to host 'backup\.example\.com'`), stderr)
				assert.Contains(t, stderr, "[git-drev] Committed revision metadata '84bb88d'")
			},
		},
		{
			name: "put falls back to plain committed log when HEAD hash is unavailable",
			args: []string{"put", "-s", "100", "-v"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`},
				"add --all":                                                     {out: ""},
				"reset -- data":                                                 {out: ""},
				"commit --allow-empty -m *":                                     {out: "[main a1b2c3d] revision"},
				"rev-parse --verify --quiet HEAD^{commit}":                      {err: &command.Error{ExitCode: 1, Category: errcode.ErrGitExec}},
				"push --force-with-lease origin refs/heads/main":                {out: "Everything up-to-date"},
			},
			mockStorage:  &storage.MockManager{},
			expectedCode: errcode.ExitSuccess,
			checkStderr: func(t *testing.T, stderr string) {
				assert.Regexp(t, regexp.MustCompile(`\[git-drev\] Uploading directory revision '[0-9a-f]{8}\.\.\.' to host 'backup\.example\.com'\.\.\.`), stderr)
				assert.Regexp(t, regexp.MustCompile(`\[git-drev\] Uploaded directory revision '[0-9a-f]{8}\.\.\.' to host 'backup\.example\.com'`), stderr)
				assert.Contains(t, stderr, "[git-drev] Committed revision metadata\n")
				assert.NotContains(t, stderr, "[git-drev] Committed revision metadata '")
			},
		},
		{
			name: "successful put on branch with non-zero marker commit clones baseline from parent UUID",
			args: []string{"put", "-s", "100"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "feature"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"11111111-1111-4111-8111-111111111111","marker":"true"}`},
				"add --all":                                                     {out: ""},
				"reset -- data":                                                 {out: ""},
				"commit --allow-empty -m *":                                     {out: "[feature a1b2c3d] revision"},
				"push --force-with-lease origin refs/heads/feature":             {out: "Everything up-to-date"},
			},
			mockStorage: &storage.MockManager{
				CloneBaselineFn: func(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
					assert.Equal(t, "backup.example.com", host)
					assert.Equal(t, "/storage/myrepo", basePath)
					assert.Equal(t, "11111111-1111-4111-8111-111111111111", srcUUID)
					return nil
				},
				UploadFn: func(ctx context.Context, req storage.TransferRequest) error {
					assert.Equal(t, "backup.example.com", req.Host)
					assert.Equal(t, "/storage/myrepo", req.BasePath)
					return nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, storageCalls []string) {
				var foundClone, foundUpload bool
				for _, c := range storageCalls {
					if len(c) >= 13 && c[:13] == "CloneBaseline" {
						foundClone = true
					}
					if len(c) >= 6 && c[:6] == "Upload" {
						foundUpload = true
					}
				}
				assert.True(t, foundClone, "expected CloneBaseline call for non-zero marker commit")
				assert.True(t, foundUpload, "expected Upload call")
			},
		},
		{
			name: "put on branch with ZeroUUID marker commit skips CloneBaseline",
			args: []string{"put", "-s", "100"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`},
				"add --all":                                                     {out: ""},
				"reset -- data":                                                 {out: ""},
				"commit --allow-empty -m *":                                     {out: "[main a1b2c3d] revision"},
				"push --force-with-lease origin refs/heads/main":                {out: "Everything up-to-date"},
			},
			mockStorage: &storage.MockManager{
				CloneBaselineFn: func(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
					t.Fatalf("CloneBaseline must not be called when parent is ZeroUUID")
					return nil
				},
				UploadFn: func(ctx context.Context, req storage.TransferRequest) error {
					assert.Equal(t, "backup.example.com", req.Host)
					assert.Equal(t, "/storage/myrepo", req.BasePath)
					return nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, storageCalls []string) {
				for _, c := range storageCalls {
					if len(c) >= 13 && c[:13] == "CloneBaseline" {
						t.Fatalf("CloneBaseline was called: %s", c)
					}
				}
			},
		},
		{
			name: "stale push resolution drops older revision when local < remote",
			args: []string{"put", "-s", "50"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"40"}`},
				"add --all":                                                     {out: ""},
				"reset -- data":                                                 {out: ""},
				"commit --allow-empty -m *":                                     {out: "[main a1b2c3d] revision"},
				"push --force-with-lease origin refs/heads/main":                {err: &command.Error{Stderr: "stale info", ExitCode: 1, Category: errcode.ErrGitExec}},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"log -n 1 --format=%s refs/remotes/origin/main --":              {out: `{"uuid":"22222222-2222-4222-8222-222222222222","seq":"80"}`},
				"reset --hard refs/remotes/origin/main --":                      {out: "HEAD is now at 2222222"},
			},
			mockStorage:  &storage.MockManager{},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, storageCalls []string) {
				assert.Contains(t, executed, "fetch --all --prune")
				assert.Contains(t, executed, "reset --hard refs/remotes/origin/main --")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFS != nil {
				tt.setupFS(t, tempDir)
			}
			var stdout, stderr bytes.Buffer
			gitRunner := &mockGitRunner{responses: tt.mockGit}
			app := cli.NewApp(&stdout, &stderr, gitRunner)
			app.WorkingDir = tempDir
			if tt.mockStorage != nil {
				app.Storage = tt.mockStorage
			}
			if tt.mockHooks != nil {
				app.Hooks = tt.mockHooks
			}
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tt.checkExecuted != nil {
					var storageCalls []string
					if tt.mockStorage != nil {
						storageCalls = tt.mockStorage.Calls
					}
					tt.checkExecuted(t, gitRunner.executed, storageCalls)
				}
				if tt.checkStderr != nil {
					tt.checkStderr(t, stderr.String())
				}
			} else {
				require.Error(t, err)
				var appErr *errcode.AppError
				require.True(t, errors.As(err, &appErr), "expected *errcode.AppError, got %T: %v", err, err)
				assert.Equal(t, tt.expectedCode, errcode.ExitCode(appErr))
				if tt.errSubstr != "" {
					assert.Contains(t, appErr.Error(), tt.errSubstr)
				}
			}
		})
	}
}

type watchBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	notify chan struct{}
}

func newWatchBuffer() *watchBuffer {
	return &watchBuffer{
		notify: make(chan struct{}, 1),
	}
}

func (w *watchBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return n, err
}

func (w *watchBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *watchBuffer) WaitFor(substr string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if strings.Contains(w.String(), substr) {
			return true
		}
		select {
		case <-w.notify:
		case <-deadline.C:
			return strings.Contains(w.String(), substr)
		}
	}
}

func TestPutCommand_TTYBaselineSpinnerFormatsShortUUIDWithEllipsis(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "file.txt"), []byte("hello"), 0o644))

	cfg := &domain.ProjectConfig{
		WorkDir: "data",
		Remote: domain.RemoteConfig{
			Host: "backup.example.com",
			Path: "/storage/myrepo",
		},
	}

	gitRunner := &mockGitRunner{
		responses: map[string]mockGitResponse{
			"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			"symbolic-ref --quiet --short HEAD":                             {out: "main"},
			"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"1afec2ea-4b75-49ff-bf02-c303eb6c6033","seq":"50"}`},
			"add --all":                                                     {out: ""},
			"reset -- data":                                                 {out: ""},
			"commit --allow-empty -m *":                                     {out: "[main 84bb88d] revision"},
			"rev-parse --verify --quiet HEAD^{commit}":                      {out: "84bb88d1234567890abcdef1234567890abcdef1"},
			"push --force-with-lease origin refs/heads/main":                {out: "Everything up-to-date"},
		},
	}
	insp := git.NewInspector(gitRunner)
	ops := git.NewOperations(gitRunner)

	stderr := newWatchBuffer()
	logger := term.New(term.Options{
		Stderr:      stderr,
		IsTTY:       true,
		NoColorFlag: true,
	})

	stor := &storage.MockManager{
		CloneBaselineFn: func(ctx context.Context, host, basePath, srcUUID, dstUUID string) error {
			assert.True(t, stderr.WaitFor("Cloning baseline revision '1afec2ea...' on host 'backup.example.com'...", 2*time.Second),
				"expected baseline clone spinner with ellipsized UUID, got: %s", stderr.String())
			return nil
		},
	}

	var stdout bytes.Buffer
	_, err := cli.RunPut(context.Background(), insp, ops, stor, nil, cfg, logger, &stdout, stderr, cli.PutOptions{
		Sequence:  "100",
		CallerDir: tempDir,
	})
	require.NoError(t, err)
	assert.Contains(t, stderr.String(), "[git-drev] Committed revision metadata '84bb88d'")
}
