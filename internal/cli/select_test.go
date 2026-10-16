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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectCommand_UnitTable(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ".git-drev.yaml")
	cfgContent := `
workdir: data
branch:
  default: trunk
remote:
  host: example.com
  path: /storage/myrepo
`
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0o644))

	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	missingUUID := "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23"

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
			args:         []string{"select", "branch-a", "branch-b"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Too many arguments",
		},
		{
			name:         "argument starting with dash returns usage error",
			args:         []string{"select", "--", "-invalid"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Invalid target reference '-invalid'",
		},
		{
			name: "no arguments checks out configured default branch",
			args: []string{"select"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"-c advice.detachedHead=false checkout trunk":                   {out: "Switched to branch 'trunk'"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "-c advice.detachedHead=false checkout trunk")
			},
		},
		{
			name: "branch target checks out branch ref",
			args: []string{"select", "feature/my-feat"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"-c advice.detachedHead=false checkout feature/my-feat":         {out: "Switched to branch 'feature/my-feat'"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "-c advice.detachedHead=false checkout feature/my-feat")
			},
		},
		{
			name: "uuid target finds matching commit and checks out commit hash",
			args: []string{"select", validUUID},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"log --all -z --format=%H%x00%s": {
					out: "3333333333333333333333333333333333333333\x00" + `{"uuid":"` + validUUID + `","seq":"10"}` + "\x00",
				},
				"-c advice.detachedHead=false checkout 3333333333333333333333333333333333333333": {out: "HEAD is now at 3333333"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "-c advice.detachedHead=false checkout 3333333333333333333333333333333333333333")
			},
		},
		{
			name: "uuid target not found returns ExitGitState",
			args: []string{"select", missingUUID},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"fetch --all --prune":                                           {out: ""},
				"log --all -z --format=%H%x00%s": {
					out: "3333333333333333333333333333333333333333\x00" + `{"uuid":"` + validUUID + `","seq":"10"}` + "\x00",
				},
			},
			expectedCode: errcode.ExitGitState,
			errSubstr:    "No commit was found with uuid '" + missingUUID + "'",
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
