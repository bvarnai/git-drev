package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git-drev/internal/cli"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"git-drev/internal/transport"
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
		mockRemote    *transport.MockRemoteRunner
		mockHooks     *mockHookRunner
		expectedCode  int
		errSubstr     string
		checkExecuted func(t *testing.T, executed []string, remoteCalls []string)
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
				"symbolic-ref --quiet --short HEAD":                             {err: &git.CommandError{ExitCode: 1}},
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
			errSubstr:    "invalid sequence \"abc\"",
		},
		{
			name: "successful put uploads, runs hook, commits and pushes",
			args: []string{"put", "-s", "100"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix":        {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                                    {out: "main"},
				"log -n 1 --format=%s HEAD --":                                         {out: `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"50"}`},
				"add --all":                                                            {out: ""},
				"commit --allow-empty -m *":                                            {out: "[main a1b2c3d] revision"},
				"push --force-with-lease origin refs/heads/main":                       {out: "Everything up-to-date"},
			},
			mockRemote: &transport.MockRemoteRunner{
				CloneBaselineFn: func(ctx context.Context, host, srcPath, dstPath string) error {
					assert.Equal(t, "backup.example.com", host)
					assert.Contains(t, srcPath, "11111111-1111-4111-8111-111111111111")
					return nil
				},
				RsyncPutFn: func(ctx context.Context, req transport.SyncRequest) error {
					assert.Equal(t, "backup.example.com", req.Host)
					assert.Contains(t, req.RemotePath, "/storage/myrepo/")
					return nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, remoteCalls []string) {
				assert.Contains(t, executed, "add --all")
				assert.Contains(t, executed, "push --force-with-lease origin refs/heads/main")
				var foundClone, foundPut bool
				for _, c := range remoteCalls {
					if len(c) >= 13 && c[:13] == "CloneBaseline" {
						foundClone = true
					}
					if len(c) >= 8 && c[:8] == "RsyncPut" {
						foundPut = true
					}
				}
				assert.True(t, foundClone, "expected CloneBaseline call")
				assert.True(t, foundPut, "expected RsyncPut call")
			},
		},
		{
			name: "stale push resolution drops older revision when local < remote",
			args: []string{"put", "-s", "50"},
			setupFS: func(t *testing.T, dir string) {
				_ = os.Remove(filepath.Join(dir, ".git", "index.lock"))
			},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix":        {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                                    {out: "main"},
				"log -n 1 --format=%s HEAD --":                                         {out: `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"40"}`},
				"add --all":                                                            {out: ""},
				"commit --allow-empty -m *":                                            {out: "[main a1b2c3d] revision"},
				"push --force-with-lease origin refs/heads/main":                       {err: &git.CommandError{Stderr: "stale info", ExitCode: 1}},
				"config --local --get-all remote.origin.fetch":                         {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                                  {out: ""},
				"log -n 1 --format=%s refs/remotes/origin/main --":                     {out: `{"uuid":"22222222-2222-4222-8222-222222222222","seq":"80"}`},
				"reset --hard refs/remotes/origin/main --":                             {out: "HEAD is now at 2222222"},
			},
			mockRemote: &transport.MockRemoteRunner{},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, remoteCalls []string) {
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
			if tt.mockRemote != nil {
				app.Remote = tt.mockRemote
			}
			if tt.mockHooks != nil {
				app.Hooks = tt.mockHooks
			}
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tt.checkExecuted != nil {
					var remoteCalls []string
					if tt.mockRemote != nil {
						remoteCalls = tt.mockRemote.Calls
					}
					tt.checkExecuted(t, gitRunner.executed, remoteCalls)
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
