package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfigCommand_UnitTable(t *testing.T) {
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
		workDir       string
		mockResponses map[string]mockGitResponse
		expectedCode  int
		errSubstr     string
		validateOut   func(t *testing.T, stdout string)
	}{
		{
			name:         "unexpected arguments returns usage error",
			args:         []string{"config", "extra"},
			expectedCode: errcode.ExitUsage,
			errSubstr:    "Unexpected argument: extra",
		},
		{
			name:    "inside repo outputs merged and annotated YAML",
			args:    []string{"config"},
			workDir: tempDir,
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "# Source hierarchy:")
				assert.Contains(t, stdout, "workdir: data")
				var cfg domain.ProjectConfig
				err := yaml.Unmarshal([]byte(stdout), &cfg)
				require.NoError(t, err)
				assert.Equal(t, "data", cfg.WorkDir)
				assert.Equal(t, "example.com", cfg.Remote.Host)
			},
		},
		{
			name:    "sources flag prints only hierarchy comments",
			args:    []string{"config", "--sources"},
			workDir: tempDir,
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "true\n" + tempDir + "\n"},
			},
			expectedCode: errcode.ExitSuccess,
			validateOut: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "# Source hierarchy:")
				assert.Contains(t, stdout, configPath)
				assert.NotContains(t, stdout, "workdir: data")
			},
		},
		{
			name:    "outside repo with no configs returns ExitConfig",
			args:    []string{"config"},
			workDir: t.TempDir(),
			mockResponses: map[string]mockGitResponse{
				"rev-parse --is-inside-work-tree --show-toplevel --show-prefix": {out: "false\n\n"},
			},
			expectedCode: errcode.ExitConfig,
			errSubstr:    "No configuration file found",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runner := &mockGitRunner{responses: tc.mockResponses}
			var stdout, stderr bytes.Buffer
			app := cli.NewApp(&stdout, &stderr, runner)
			if tc.workDir != "" {
				app.WorkingDir = tc.workDir
			}
			app.RootCmd.SetArgs(tc.args)

			err := app.Execute(context.Background())
			if tc.expectedCode == errcode.ExitSuccess {
				require.NoError(t, err)
				if tc.validateOut != nil {
					tc.validateOut(t, stdout.String())
				}
			} else {
				require.Error(t, err)
				var appErr *errcode.AppError
				require.True(t, errors.As(err, &appErr), "expected *errcode.AppError, got %T: %v", err, err)
				assert.Equal(t, tc.expectedCode, errcode.ExitCode(appErr))
				if tc.errSubstr != "" {
					assert.Contains(t, appErr.Error(), tc.errSubstr)
				}
			}
		})
	}
}
