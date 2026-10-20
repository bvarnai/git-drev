package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bvarnai/git-drev/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRepoFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"git@github.com:org/my-project.git", "my-project"},
		{"git@github.com:org/my-project", "my-project"},
		{"https://github.com/org/my-service.git", "my-service"},
		{"https://github.com/org/my-service/", "my-service"},
		{"ssh://git@git.corp.internal:2222/teams/subteam/app.git", "app"},
		{"file:///local/path/to/my-repo.git", "my-repo"},
		{"/var/git/my-bare-repo.git", "my-bare-repo"},
		{"", ""},
		{"   ", ""},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			actual := config.ExtractRepoFromURL(tc.url)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestDetectRepoName(t *testing.T) {
	t.Run("from git config origin url", func(t *testing.T) {
		tempDir := t.TempDir()
		gitDir := filepath.Join(tempDir, ".git")
		require.NoError(t, os.MkdirAll(gitDir, 0755))
		gitConfig := `
[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:company/cool-app.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644))

		repoName := config.DetectRepoName(tempDir)
		assert.Equal(t, "cool-app", repoName)
	})

	t.Run("fallback to directory basename without git config", func(t *testing.T) {
		tempDir := t.TempDir()
		subDir := filepath.Join(tempDir, "service-xyz")
		require.NoError(t, os.MkdirAll(subDir, 0755))

		repoName := config.DetectRepoName(subDir)
		assert.Equal(t, "service-xyz", repoName)
	})
}
