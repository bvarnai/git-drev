package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCommand_UnitTable(t *testing.T) {
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

	validUUID := "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name          string
		args          []string
		mockGit       map[string]mockGitResponse
		mockStorage   *storage.MockManager
		expectedCode  int
		errSubstr     string
		checkExecuted func(t *testing.T, executed []string, storageCalls []string)
	}{
		{
			name:         "excess arguments returns usage error",
			args:         []string{"get", "dir1", "dir2"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Too many arguments",
		},
		{
			name: "latest flag with detached HEAD returns ExitGitState/ErrDetachedHead",
			args: []string{"get", "-l"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {err: &git.CommandError{ExitCode: 1}},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "Cannot update detached HEAD to latest",
		},
		{
			name: "marker commit on branch returns ErrGitState",
			args: []string{"get"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "No revision committed yet on this branch",
		},
		{
			name: "remote revision not found returns ExitRemoteNotFound",
			args: []string{"get"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"` + validUUID + `","seq":"10"}`},
			},
			mockStorage: &storage.MockManager{
				CheckDirExistsFn: func(ctx context.Context, host, remotePath string) (bool, error) {
					return false, nil
				},
			},
			expectedCode: errcode.ExitRemoteNotFound,
			errSubstr:    "Unable to find revision '" + validUUID + "' on host 'backup.example.com'",
		},
		{
			name: "successful get synchronizes target directory",
			args: []string{"get"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"` + validUUID + `","seq":"10"}`},
			},
			mockStorage: &storage.MockManager{
				CheckDirExistsFn: func(ctx context.Context, host, remotePath string) (bool, error) {
					return true, nil
				},
				DownloadFn: func(ctx context.Context, req storage.TransferRequest) error {
					assert.Equal(t, "backup.example.com", req.Host)
					assert.Equal(t, "/storage/myrepo/"+validUUID+"/", req.RemotePath)
					assert.True(t, filepath.IsAbs(req.LocalPath))
					return nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string, storageCalls []string) {
				assert.Contains(t, storageCalls, "CheckDirExists backup.example.com /storage/myrepo/"+validUUID)
				assert.Contains(t, storageCalls, "Download backup.example.com /storage/myrepo/"+validUUID+"/")
			},
		},
		{
			name: "successful get with --latest updates branch first",
			args: []string{"get", "--latest"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"show-ref --verify --quiet refs/remotes/origin/main":            {out: ""},
				"reset --hard refs/remotes/origin/main --":                      {out: "HEAD is now at 1234567"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"` + validUUID + `","seq":"10"}`},
			},
			mockStorage: &storage.MockManager{
				CheckDirExistsFn: func(ctx context.Context, host, remotePath string) (bool, error) {
					return true, nil
				},
			},
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
			var stdout, stderr bytes.Buffer
			gitRunner := &mockGitRunner{responses: tt.mockGit}
			app := cli.NewApp(&stdout, &stderr, gitRunner)
			app.WorkingDir = tempDir
			if tt.mockStorage != nil {
				app.Storage = tt.mockStorage
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
