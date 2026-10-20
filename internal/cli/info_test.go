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
	"github.com/bvarnai/git-drev/internal/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockHookRunner struct {
	runFn func(ctx context.Context, req hook.RunRequest) error
	calls []hook.RunRequest
}

func (m *mockHookRunner) Run(ctx context.Context, req hook.RunRequest) error {
	m.calls = append(m.calls, req)
	if m.runFn != nil {
		return m.runFn(ctx, req)
	}
	return nil
}

func TestInfoCommand_UnitTable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ".git-drev.yaml")
	cfgContent := `
workdir: data
remote:
  host: example.com
  path: /storage/myrepo
`
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0o644))

	tests := []struct {
		name          string
		args          []string
		mockResponses map[string]mockGitResponse
		setupFS       func(t *testing.T, dir string)
		mockHooks     *mockHookRunner
		expectedCode  int
		errSubstr     string
		checkStdout   string
		checkStderr   string
	}{
		{
			name:         "excess argument returns usage error",
			args:         []string{"info", "extra"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "info takes no arguments",
		},
		{
			name: "outside worktree returns code 3",
			args: []string{"info"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "false\n\n"},
			},
			expectedCode: errcode.ExitConfig,
			errSubstr:    "Not inside a Git working tree",
		},
		{
			name: "no hook file present logs warning and exits 0",
			args: []string{"info"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			},
			expectedCode: errcode.ExitSuccess,
			checkStderr:  "No info hook found",
		},
		{
			name: "broken symlink at hook returns ErrRuntime",
			args: []string{"info"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			},
			setupFS: func(t *testing.T, dir string) {
				symPath := filepath.Join(dir, ".git-drev-info-hook")
				_ = os.Remove(symPath)
				require.NoError(t, os.Symlink(filepath.Join(dir, "nonexistent"), symPath))
			},
			expectedCode: errcode.ExitRuntime,
			errSubstr:    "is a broken symlink",
		},
		{
			name: "hook present executes and injects env metadata",
			args: []string{"info"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"12"}`},
			},
			setupFS: func(t *testing.T, dir string) {
				hookPath := filepath.Join(dir, ".git-drev-info-hook")
				_ = os.Remove(hookPath)
				require.NoError(t, os.WriteFile(hookPath, []byte("#!/bin/sh\n"), 0o755))
			},
			mockHooks: &mockHookRunner{
				runFn: func(ctx context.Context, req hook.RunRequest) error {
					assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", req.Env.UUID)
					assert.Equal(t, "12", req.Env.Seq)
					assert.Equal(t, "main", req.Env.Branch)
					_, _ = req.Stdout.Write([]byte("hook output: ok\n"))
					return nil
				},
			},
			expectedCode: errcode.ExitSuccess,
			checkStdout:  "hook output: ok\n",
		},
		{
			name: "hook failure returns ErrRuntime",
			args: []string{"info"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "main"},
				"log -n 1 --format=%s HEAD --":                                  {out: `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"12"}`},
			},
			setupFS: func(t *testing.T, dir string) {
				hookPath := filepath.Join(dir, ".git-drev-info-hook")
				_ = os.Remove(hookPath)
				require.NoError(t, os.WriteFile(hookPath, []byte("#!/bin/sh\n"), 0o755))
			},
			mockHooks: &mockHookRunner{
				runFn: func(ctx context.Context, req hook.RunRequest) error {
					return errcode.NewError(errcode.ErrRuntime, "info hook failed with exit code 42")
				},
			},
			expectedCode: errcode.ExitRuntime,
			errSubstr:    "info hook failed with exit code 42",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupFS != nil {
				tt.setupFS(t, tempDir)
			}
			var stdout, stderr bytes.Buffer
			gitRunner := &mockGitRunner{responses: tt.mockResponses}
			app := cli.NewApp(&stdout, &stderr, gitRunner)
			app.WorkingDir = tempDir
			if tt.mockHooks != nil {
				app.Hooks = tt.mockHooks
			}
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tt.checkStdout != "" {
					assert.Equal(t, tt.checkStdout, stdout.String())
				}
				if tt.checkStderr != "" {
					assert.Contains(t, stderr.String(), tt.checkStderr)
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
