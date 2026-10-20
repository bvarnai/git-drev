package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageCommand_UnitTable(t *testing.T) {
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

	tests := []struct {
		name         string
		args         []string
		mockGit      map[string]mockGitResponse
		mockRemote   *transport.MockRemoteRunner
		expectedCode int
		errSubstr    string
		validateOut  func(t *testing.T, stdout string, stderr string)
	}{
		{
			name:         "excess arguments returns usage error",
			args:         []string{"usage", "extra"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "usage takes no arguments",
		},
		{
			name: "outside worktree returns code 3",
			args: []string{"usage"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "false\n\n"},
			},
			expectedCode: errcode.ExitConfig,
			errSubstr:    "Not inside a Git working tree",
		},
		{
			name: "remote path not found returns ExitRemoteNotFound",
			args: []string{"usage"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
			},
			mockRemote: &transport.MockRemoteRunner{
				QueryUsageFn: func(ctx context.Context, host, basePath string) (string, []domain.RemoteRevision, error) {
					return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path does not exist")
				},
			},
			expectedCode: errcode.ExitRemoteNotFound,
			errSubstr:    "remote path does not exist",
		},
		{
			name: "summary mode outputs total size and hint",
			args: []string{"usage"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
			},
			mockRemote: &transport.MockRemoteRunner{
				QueryUsageFn: func(ctx context.Context, host, basePath string) (string, []domain.RemoteRevision, error) {
					return "1.5G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.5G"},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.Contains(t, stdout, "Total space used:  1.5G")
				assert.Contains(t, stderr, "Found 1 revision directory(ies)")
				assert.Contains(t, stderr, "Hint: Use -v/--verbose")
			},
		},
		{
			name: "verbose mode correlates commits and marks orphaned",
			args: []string{"usage", "-v"},
			mockGit: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix": {
					out: "1111111111111111111111111111111111111111\x001111111\x001700000000\x00HEAD -> main, origin/main\x00" + `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"10"}` + "\x00",
				},
			},
			mockRemote: &transport.MockRemoteRunner{
				QueryUsageFn: func(ctx context.Context, host, basePath string) (string, []domain.RemoteRevision, error) {
					return "2.0G", []domain.RemoteRevision{
						{UUID: "550e8400-e29b-41d4-a716-446655440000", SizeHuman: "1.0G", ModTime: time.Unix(1700000000, 0)},
						{UUID: "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", SizeHuman: "1.0G", ModTime: time.Unix(1700001000, 0)},
					}, nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string, stderr string) {
				assert.Contains(t, stdout, "Total space used:  2.0G")
				assert.Contains(t, stdout, "Revisions breakdown:")
				// Active revision
				assert.Contains(t, stdout, "550e8400-e29b-41d4-a716-446655440000")
				assert.Contains(t, stdout, "1111111")
				assert.Contains(t, stdout, "main, origin/main")
				assert.Contains(t, stdout, "seq: 10")
				// Orphaned revision
				assert.Contains(t, stdout, "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23")
				assert.Contains(t, stdout, "[orphaned]")
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
			if tt.mockRemote != nil {
				app.Remote = tt.mockRemote
			}
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tt.validateOut != nil {
					tt.validateOut(t, stdout.String(), stderr.String())
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
