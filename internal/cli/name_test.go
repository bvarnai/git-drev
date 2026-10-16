package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"git-drev/internal/cli"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNameCommand_UnitTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		mockResponses map[string]mockGitResponse
		expectedCode  int
		errSubstr     string
		expectStdout  string
	}{
		{
			name:         "excess arguments returns usage error",
			args:         []string{"name", "unexpected-arg"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "This command takes no arguments",
		},
		{
			name: "outside worktree returns code 3",
			args: []string{"name"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "false\n\n"},
			},
			expectedCode: errcode.ExitConfig,
			errSubstr:    "Not inside a Git working tree",
		},
		{
			name: "attached branch emits branch name to stdout",
			args: []string{"name"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n/repo\n"},
				"symbolic-ref --quiet --short HEAD":                             {out: "feature/alpha"},
			},
			expectedCode: errcode.ExitSuccess,
			expectStdout: "feature/alpha\n",
		},
		{
			name: "detached HEAD emits canonical detached state message",
			args: []string{"name"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n/repo\n"},
				"symbolic-ref --quiet --short HEAD":                             {err: &git.CommandError{ExitCode: 1}},
			},
			expectedCode: errcode.ExitSuccess,
			expectStdout: cli.DetachedHeadDisplay + "\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			runner := &mockGitRunner{responses: tt.mockResponses}
			app := cli.NewApp(&stdout, &stderr, runner)
			app.RootCmd.SetArgs(tt.args)

			err := app.Execute(context.Background())
			if tt.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				assert.Equal(t, tt.expectStdout, stdout.String())
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
