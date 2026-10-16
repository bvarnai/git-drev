package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git-drev/internal/config"
	"git-drev/internal/domain"
	"git-drev/internal/errcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoader_DefaultsAndTrailingSlashNormalization(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	repoDir := t.TempDir()
	content := `
remote:
  host: drev-server
  path: /var/drev/myproject///
`
	err := os.WriteFile(filepath.Join(repoDir, ".git-drev.yaml"), []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := config.Load(repoDir)
	require.NoError(t, err)
	assert.Equal(t, "data", cfg.WorkDir)
	assert.Equal(t, "main", cfg.Branch.Default)
	assert.Equal(t, "drev-server", cfg.Remote.Host)
	assert.Equal(t, "/var/drev/myproject", cfg.Remote.Path)
}

func TestLoader_LayeredCascadeUserCompanyRepo(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Tier 1: User Global (~/.config/git-drev/config.yaml)
	userConfigDir := filepath.Join(fakeHome, ".config", "git-drev")
	require.NoError(t, os.MkdirAll(userConfigDir, 0755))
	userConfigContent := `
workdir: user-data
branch:
  default: main
remote:
  host: user-host
  rsync:
    get: -az --stats
    put: -az --stats
`
	require.NoError(t, os.WriteFile(filepath.Join(userConfigDir, "config.yaml"), []byte(userConfigContent), 0644))

	// Tier 2: Shared Global via GIT_DREV_CONFIG
	companyConfigDir := t.TempDir()
	companyConfigFile := filepath.Join(companyConfigDir, "company-rules.yaml")
	companyConfigContent := `
branch:
  default: develop
remote:
  host: company-drev-server
  rsync:
    put: -az --whole-file --stats
`
	require.NoError(t, os.WriteFile(companyConfigFile, []byte(companyConfigContent), 0644))
	t.Setenv("GIT_DREV_CONFIG", companyConfigFile)

	// Tier 3: Repo Local (.git-drev.yaml)
	repoDir := t.TempDir()
	repoConfigContent := `
remote:
  path: /var/drev/service-x/
`
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".git-drev.yaml"), []byte(repoConfigContent), 0644))

	cfg, err := config.Load(repoDir)
	require.NoError(t, err)

	// Tier 1 Base preserved:
	assert.Equal(t, "user-data", cfg.WorkDir)
	assert.Equal(t, "-az --stats", cfg.Remote.Rsync.Get)

	// Tier 2 Shared Override applied:
	assert.Equal(t, "develop", cfg.Branch.Default)
	assert.Equal(t, "company-drev-server", cfg.Remote.Host)
	assert.Equal(t, "-az --whole-file --stats", cfg.Remote.Rsync.Put)

	// Tier 3 Repo Override applied:
	assert.Equal(t, "/var/drev/service-x", cfg.Remote.Path)
}

func TestLoader_UserGlobalDotfileFallback(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	dotfileContent := `
remote:
  host: dotfile-host
  path: /var/drev/{repo}
`
	require.NoError(t, os.WriteFile(filepath.Join(fakeHome, ".git-drev.yaml"), []byte(dotfileContent), 0644))

	repoDir := t.TempDir()
	cfg, err := config.Load(repoDir)
	require.NoError(t, err)
	assert.Equal(t, "dotfile-host", cfg.Remote.Host)
	assert.Equal(t, "/var/drev/"+filepath.Base(repoDir), cfg.Remote.Path)
}

func TestLoader_RepoIsolation_GlobalPathAutoAppendsRepo(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	globalConfigDir := filepath.Join(fakeHome, ".config", "git-drev")
	require.NoError(t, os.MkdirAll(globalConfigDir, 0755))
	content := `
remote:
  host: global-host
  path: /var/drev
`
	require.NoError(t, os.WriteFile(filepath.Join(globalConfigDir, "config.yaml"), []byte(content), 0644))

	repoDir := t.TempDir()
	report, err := config.Inspect(repoDir)
	require.NoError(t, err)

	expectedPath := "/var/drev/" + filepath.Base(repoDir)
	assert.Equal(t, expectedPath, report.Config.Remote.Path)
	assert.Equal(t, "appended /{repo} for repository isolation", report.InferredFields["remote.path"])
}

func TestLoader_RepoIsolation_RepoConfigOverridesWithoutAppend(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	globalConfigDir := filepath.Join(fakeHome, ".config", "git-drev")
	require.NoError(t, os.MkdirAll(globalConfigDir, 0755))
	globalContent := `
remote:
  host: global-host
  path: /var/drev
`
	require.NoError(t, os.WriteFile(filepath.Join(globalConfigDir, "config.yaml"), []byte(globalContent), 0644))

	repoDir := t.TempDir()
	repoContent := `
remote:
  path: /custom/isolated-path
`
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".git-drev.yaml"), []byte(repoContent), 0644))

	report, err := config.Inspect(repoDir)
	require.NoError(t, err)
	assert.Equal(t, "/custom/isolated-path", report.Config.Remote.Path)
	assert.Empty(t, report.InferredFields["remote.path"])
}

func TestLoader_RepoIsolation_RepoConfigExpandsTemplate(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	repoDir := t.TempDir()
	repoContent := `
remote:
  host: custom-host
  path: /srv/storage/{project}/data
`
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".git-drev.yaml"), []byte(repoContent), 0644))

	report, err := config.Inspect(repoDir)
	require.NoError(t, err)
	expectedPath := "/srv/storage/" + filepath.Base(repoDir) + "/data"
	assert.Equal(t, expectedPath, report.Config.Remote.Path)
	assert.Equal(t, "expanded {repo} -> "+filepath.Base(repoDir), report.InferredFields["remote.path"])
}

func TestLoader_RepoSubdirConfigFileFallback(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	repoDir := t.TempDir()
	subCfgDir := filepath.Join(repoDir, ".git-drev")
	require.NoError(t, os.MkdirAll(subCfgDir, 0755))
	content := `
workdir: artifacts
remote:
  host: drev-server
  path: /var/drev/project
`
	require.NoError(t, os.WriteFile(filepath.Join(subCfgDir, "config.yaml"), []byte(content), 0644))

	cfg, err := config.Load(repoDir)
	require.NoError(t, err)
	assert.Equal(t, "artifacts", cfg.WorkDir)
	assert.Equal(t, "drev-server", cfg.Remote.Host)
	assert.Equal(t, "/var/drev/project", cfg.Remote.Path)
}

func TestLoader_InvalidEnvVarConfigFails(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "/nonexistent/path/to/config.yaml")

	_, err := config.Load(t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, errcode.ErrConfig))
	assert.Contains(t, err.Error(), "configuration file specified by GIT_DREV_CONFIG not found")
}

func TestLoader_MissingConfigFails(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	_, err := config.Load(t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, errcode.ErrConfig))
	assert.Contains(t, err.Error(), "No configuration file found")
	assert.Contains(t, errcode.Hint(err), ".git-drev.yaml")
}

func TestInspect_DefaultCommentsAndRoundTripYAML(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("GIT_DREV_CONFIG", "")

	repoDir := t.TempDir()
	content := `
remote:
  host: drev-server
  path: /var/drev/myproject
  rsync:
    get: -az --delete-during --stats
    put: -az --delete-during --whole-file --stats
`
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".git-drev.yaml"), []byte(content), 0644))

	report, err := config.Inspect(repoDir)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, "default convention", report.InferredFields["workdir"])
	assert.Equal(t, "default convention", report.InferredFields["branch.default"])

	output, err := config.FormatReport(report, false)
	require.NoError(t, err)

	// Check headers
	assert.Contains(t, output, "# Source hierarchy:")
	assert.Contains(t, output, "Tier 1 (User global [~/.config/git-drev/config.yaml, ~/.git-drev.yaml]):")
	assert.Contains(t, output, "Tier 2 (Shared global [$GIT_DREV_CONFIG]):")
	assert.Contains(t, output, "Tier 3 (Repo local [.git-drev.yaml, .git-drev/config.yaml]):")

	// Check inline annotations
	assert.Contains(t, output, "workdir: data # default convention")
	assert.Contains(t, output, "default: main # default convention")

	// Verify valid YAML round-trip
	var parsed domain.ProjectConfig
	err = yaml.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "data", parsed.WorkDir)
	assert.Equal(t, "main", parsed.Branch.Default)
	assert.Equal(t, "drev-server", parsed.Remote.Host)
	assert.Equal(t, "/var/drev/myproject", parsed.Remote.Path)
	assert.Equal(t, "-az --delete-during --stats", parsed.Remote.Rsync.Get)
	assert.Equal(t, "-az --delete-during --whole-file --stats", parsed.Remote.Rsync.Put)
}

func TestInspect_SourcesOnly(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	userConfigDir := filepath.Join(fakeHome, ".config", "git-drev")
	require.NoError(t, os.MkdirAll(userConfigDir, 0755))
	userConfigFile := filepath.Join(userConfigDir, "config.yaml")
	require.NoError(t, os.WriteFile(userConfigFile, []byte("workdir: data\n"), 0644))

	companyConfigDir := t.TempDir()
	companyConfigFile := filepath.Join(companyConfigDir, "company.yaml")
	require.NoError(t, os.WriteFile(companyConfigFile, []byte("remote:\n  host: drev-server\n"), 0644))
	t.Setenv("GIT_DREV_CONFIG", companyConfigFile)

	repoDir := t.TempDir()
	repoConfigFile := filepath.Join(repoDir, ".git-drev.yaml")
	require.NoError(t, os.WriteFile(repoConfigFile, []byte("remote:\n  path: /var/drev/project\n"), 0644))

	report, err := config.Inspect(repoDir)
	require.NoError(t, err)
	assert.Equal(t, userConfigFile, report.UserConfigPath)
	assert.Equal(t, companyConfigFile, report.SharedConfigPath)
	assert.Equal(t, repoConfigFile, report.RepoConfigPath)

	sourcesOutput, err := config.FormatReport(report, true)
	require.NoError(t, err)
	assert.Contains(t, sourcesOutput, "# Source hierarchy:")
	assert.Contains(t, sourcesOutput, userConfigFile)
	assert.Contains(t, sourcesOutput, companyConfigFile)
	assert.Contains(t, sourcesOutput, repoConfigFile)
	assert.NotContains(t, sourcesOutput, "workdir:")
}

func TestNormalizeAndValidate_ValidationRules(t *testing.T) {
	tests := []struct {
		name string
		cfg  domain.ProjectConfig
	}{
		{
			name: "missing remote.host",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Path: "/var/drev/p"}},
		},
		{
			name: "remote.host starts with dash (SSH injection)",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "-oProxyCommand=evil", Path: "/var/drev/p"}},
		},
		{
			name: "remote.host contains whitespace",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "bad host", Path: "/var/drev/p"}},
		},
		{
			name: "missing remote.path",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "drev-server"}},
		},
		{
			name: "relative remote.path",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "drev-server", Path: "var/drev/p"}},
		},
		{
			name: "root remote.path",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "drev-server", Path: "/"}},
		},
		{
			name: "multiple slashes root remote.path",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "drev-server", Path: "///"}},
		},
		{
			name: "remote.path with shell metacharacters",
			cfg:  domain.ProjectConfig{Remote: domain.RemoteConfig{Host: "drev-server", Path: "/var/drev/$(whoami)"}},
		},
		{
			name: "workdir escapes repo root",
			cfg:  domain.ProjectConfig{WorkDir: "../outside", Remote: domain.RemoteConfig{Host: "drev-server", Path: "/var/drev/p"}},
		},
		{
			name: "workdir is dot",
			cfg:  domain.ProjectConfig{WorkDir: ".", Remote: domain.RemoteConfig{Host: "drev-server", Path: "/var/drev/p"}},
		},
		{
			name: "workdir is absolute",
			cfg:  domain.ProjectConfig{WorkDir: "/abs/path", Remote: domain.RemoteConfig{Host: "drev-server", Path: "/var/drev/p"}},
		},
		{
			name: "workdir inside .git",
			cfg:  domain.ProjectConfig{WorkDir: ".git/objects", Remote: domain.RemoteConfig{Host: "drev-server", Path: "/var/drev/p"}},
		},
		{
			name: "branch.default starts with dash",
			cfg:  domain.ProjectConfig{Branch: domain.BranchConfig{Default: "-main"}, Remote: domain.RemoteConfig{Host: "drev-server", Path: "/var/drev/p"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := config.NormalizeAndValidate(&cfg)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errcode.ErrConfig))
		})
	}
}
