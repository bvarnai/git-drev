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

func TestCreateCommand_UnitTable(t *testing.T) {
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
			name:         "missing branch argument returns usage error",
			args:         []string{"create"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "No branch name specified",
		},
		{
			name:         "unexpected extra argument returns usage error",
			args:         []string{"create", "feat-a", "extra"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Unexpected argument 'extra'",
		},
		{
			name: "invalid branch name starting with dash returns usage error",
			args: []string{"create", "--", "-bad-name"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Invalid branch name '-bad-name'",
		},
		{
			name: "branch exists locally checkouts local branch",
			args: []string{"create", "feature/local"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
				"check-ref-format refs/heads/feature/local":                     {out: ""},
				"config --local --get-all remote.origin.fetch":                  {out: git.WildcardFetchRefspec},
				"show-ref --verify --quiet refs/heads/feature/local":            {out: ""},
				"fetch --all --prune":                                           {out: ""},
				"-c advice.detachedHead=false checkout feature/local":           {out: "Switched to branch 'feature/local'"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "-c advice.detachedHead=false checkout feature/local")
			},
		},
		{
			name: "branch exists remotely switches to branch",
			args: []string{"create", "feature/remote"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix":  {out: "true\n" + tempDir + "\n"},
				"check-ref-format refs/heads/feature/remote":                     {out: ""},
				"config --local --get-all remote.origin.fetch":                   {out: git.WildcardFetchRefspec},
				"show-ref --verify --quiet refs/heads/feature/remote":            {err: &git.CommandError{ExitCode: 1}},
				"ls-remote --exit-code --heads origin refs/heads/feature/remote": {out: "abcdef refs/heads/feature/remote"},
				"fetch --all --prune":   {out: ""},
				"switch feature/remote": {out: "Switched to a new branch 'feature/remote'"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "switch feature/remote")
			},
		},
		{
			name: "branch does not exist creates new branch with marker and pushes",
			args: []string{"create", "feature/brand-new"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix":                           {out: "true\n" + tempDir + "\n"},
				"check-ref-format refs/heads/feature/brand-new":                                           {out: ""},
				"config --local --get-all remote.origin.fetch":                                            {out: git.WildcardFetchRefspec},
				"show-ref --verify --quiet refs/heads/feature/brand-new":                                  {err: &git.CommandError{ExitCode: 1}},
				"ls-remote --exit-code --heads origin refs/heads/feature/brand-new":                       {err: &git.CommandError{ExitCode: 2}},
				"log -n 1 --format=%s HEAD --":                                                            {out: `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"1"}`},
				"symbolic-ref --quiet --short HEAD":                                                       {out: "main"},
				"rev-parse --verify --quiet HEAD^{commit}":                                                {out: "1111111111111111111111111111111111111111"},
				"checkout -b feature/brand-new":                                                           {out: "Switched to a new branch 'feature/brand-new'"},
				`commit --allow-empty -m {"uuid":"550e8400-e29b-41d4-a716-446655440000","marker":"true"}`: {out: "[feature/brand-new 2222222] marker"},
				"for-each-ref --format=%(upstream:short) refs/heads/feature/brand-new":                    {out: ""},
				"push --set-upstream --force-with-lease origin refs/heads/feature/brand-new":              {out: "To origin * [new branch] feature/brand-new -> feature/brand-new"},
			},
			expectedCode: errcode.ExitSuccess,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "checkout -b feature/brand-new")
				assert.Contains(t, executed, "push --set-upstream --force-with-lease origin refs/heads/feature/brand-new")
			},
		},
		{
			name: "push fails triggers rollback and deletes locally created branch",
			args: []string{"create", "feature/fail-push"},
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix":                           {out: "true\n" + tempDir + "\n"},
				"check-ref-format refs/heads/feature/fail-push":                                           {out: ""},
				"config --local --get-all remote.origin.fetch":                                            {out: git.WildcardFetchRefspec},
				"show-ref --verify --quiet refs/heads/feature/fail-push":                                  {err: &git.CommandError{ExitCode: 1}},
				"ls-remote --exit-code --heads origin refs/heads/feature/fail-push":                       {err: &git.CommandError{ExitCode: 2}},
				"log -n 1 --format=%s HEAD --":                                                            {out: `{"uuid":"550e8400-e29b-41d4-a716-446655440000","seq":"1"}`},
				"symbolic-ref --quiet --short HEAD":                                                       {out: "main"},
				"rev-parse --verify --quiet HEAD^{commit}":                                                {out: "1111111111111111111111111111111111111111"},
				"checkout -b feature/fail-push":                                                           {out: "Switched to a new branch 'feature/fail-push'"},
				`commit --allow-empty -m {"uuid":"550e8400-e29b-41d4-a716-446655440000","marker":"true"}`: {out: "[feature/fail-push 2222222] marker"},
				"for-each-ref --format=%(upstream:short) refs/heads/feature/fail-push":                    {out: ""},
				"push --set-upstream --force-with-lease origin refs/heads/feature/fail-push":              {err: errors.New("network error")},
				"-c advice.detachedHead=false checkout main":                                              {out: ""},
				"checkout main":               {out: ""},
				"branch -D feature/fail-push": {out: "Deleted branch feature/fail-push"},
			},
			expectedCode: errcode.ExitGitExec,
			checkExecuted: func(t *testing.T, executed []string) {
				assert.Contains(t, executed, "branch -D feature/fail-push")
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
				if tt.checkExecuted != nil {
					tt.checkExecuted(t, runner.executed)
				}
			}
		})
	}
}
