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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateCommand_UnitTable(t *testing.T) {
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
		expectedCode  int
		errSubstr     string
		checkExecuted func(t *testing.T, executed []string)
	}{
		{
			name:         "excess arguments returns usage error",
			args:         []string{"update", "extra"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "update takes no positional arguments",
		},
		{
			name: "detached HEAD aborts with ErrDetachedHead",
			args: []string{"update"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {err: &git.CommandError{ExitCode: 1}},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "Detached HEAD state detected",
		},
		{
			name: "remote branch missing aborts with ErrGitState",
			args: []string{"update"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "feature/foo"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"show-ref --verify --quiet refs/remotes/origin/feature/foo":     {err: &git.CommandError{ExitCode: 1}},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "Remote branch 'origin/feature/foo' does not exist",
		},
		{
			name: "successful update fetches and resets hard to upstream",
			args: []string{"update"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "feature/foo"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"show-ref --verify --quiet refs/remotes/origin/feature/foo":     {out: ""},
				"reset --hard refs/remotes/origin/feature/foo --":               {out: "HEAD is now at 1234567"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "fetch --all --prune")
				assert.Contains(t, executed, "reset --hard refs/remotes/origin/feature/foo --")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			runner := &mockGitRunner{responses: tt.mockResponses}
			app := cli.NewApp(&stdout, &stderr, runner)
			app.WorkingDir = tempDir
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tt.checkExecuted != nil {
					tt.checkExecuted(t, runner.executed)
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
