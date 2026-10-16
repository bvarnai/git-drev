// Package config loads, merges, and validates configuration across the 3-tier YAML cascade.
package config

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"git-drev/internal/domain"
	"git-drev/internal/errcode"
	"gopkg.in/yaml.v3"
)

// Default values per docs/spec/global.md Section 5.
const (
	DefaultWorkDir    = "data"
	DefaultBranchName = "main"
)

// FindUserConfigFile searches for user-level global configuration in $HOME.
func FindUserConfigFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}

	candidates := []string{
		filepath.Join(home, ".config", "git-drev", "config.yaml"),
		filepath.Join(home, ".config", "git-drev", "config.yml"),
		filepath.Join(home, ".git-drev.yaml"),
		filepath.Join(home, ".git-drev.yml"),
		filepath.Join(home, ".git-drev", "config.yaml"),
		filepath.Join(home, ".git-drev", "config.yml"),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// FindRepoConfigFile searches for repository-level configuration in workDir.
func FindRepoConfigFile(workDir string) string {
	if workDir == "" {
		return ""
	}
	candidates := []string{
		filepath.Join(workDir, ".git-drev.yaml"),
		filepath.Join(workDir, ".git-drev.yml"),
		filepath.Join(workDir, ".git-drev", "config.yaml"),
		filepath.Join(workDir, ".git-drev", "config.yml"),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadConfigFile(filePath string) (*domain.ProjectConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, errcode.WrapError(errcode.ErrConfig, err, "failed to read configuration file: %s", filePath)
	}

	cfg := &domain.ProjectConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, errcode.WrapError(errcode.ErrConfig, err, "failed to parse configuration file: %s", filePath)
	}
	return cfg, nil
}

// Inspect evaluates and resolves configuration across all 3 tiers, tracking source paths and default provenance.
func Inspect(workDir string) (*ConfigReport, error) {
	report := &ConfigReport{
		InferredFields: make(map[string]string),
	}

	var cfg *domain.ProjectConfig
	foundAnyConfig := false

	// Tier 1: User Global
	if userPath := FindUserConfigFile(); userPath != "" {
		report.UserConfigPath = userPath
		userCfg, err := loadConfigFile(userPath)
		if err != nil {
			return nil, err
		}
		cfg = userCfg
		foundAnyConfig = true
	}

	// Tier 2: Shared Global (GIT_DREV_CONFIG)
	if envPath := os.Getenv("GIT_DREV_CONFIG"); envPath != "" {
		if _, err := os.Stat(envPath); err != nil {
			return nil, errcode.NewError(errcode.ErrConfig, "configuration file specified by GIT_DREV_CONFIG not found: %s", envPath)
		}
		report.SharedConfigPath = envPath
		envCfg, err := loadConfigFile(envPath)
		if err != nil {
			return nil, err
		}
		cfg = MergeConfigs(cfg, envCfg)
		foundAnyConfig = true
	}

	// Tier 3: Repository Local
	repoDefinedRemotePath := false
	if repoPath := FindRepoConfigFile(workDir); repoPath != "" {
		report.RepoConfigPath = repoPath
		repoCfg, err := loadConfigFile(repoPath)
		if err != nil {
			return nil, err
		}
		if repoCfg != nil && strings.TrimSpace(repoCfg.Remote.Path) != "" {
			repoDefinedRemotePath = true
		}
		cfg = MergeConfigs(cfg, repoCfg)
		foundAnyConfig = true
	}

	if !foundAnyConfig {
		return nil, errcode.NewError(errcode.ErrConfig, "No configuration file found").
			WithHint("Create a .git-drev.yaml configuration file in the repository root")
	}

	if cfg == nil {
		cfg = &domain.ProjectConfig{}
	}

	// Detect repository name for template expansion ({repo} / {project}) and isolation.
	repoName := DetectRepoName(workDir)

	// If remote.path was sourced from global configuration (Tier 1 or Tier 2) and NOT
	// explicitly overridden in repository configuration (Tier 3), enforce repository
	// isolation by automatically appending /{repo} if no template placeholder was provided.
	if !repoDefinedRemotePath && strings.TrimSpace(cfg.Remote.Path) != "" {
		hasRepoVar := strings.Contains(cfg.Remote.Path, "{repo}") || strings.Contains(cfg.Remote.Path, "{project}")
		if !hasRepoVar {
			cfg.Remote.Path = strings.TrimRight(cfg.Remote.Path, "/") + "/{repo}"
			report.InferredFields["remote.path"] = "appended /{repo} for repository isolation"
		}
	}

	// Expand {repo} and {project} template placeholders in remote.path.
	if strings.Contains(cfg.Remote.Path, "{repo}") || strings.Contains(cfg.Remote.Path, "{project}") {
		cfg.Remote.Path = strings.ReplaceAll(cfg.Remote.Path, "{repo}", repoName)
		cfg.Remote.Path = strings.ReplaceAll(cfg.Remote.Path, "{project}", repoName)
		if report.InferredFields["remote.path"] == "" {
			report.InferredFields["remote.path"] = "expanded {repo} -> " + repoName
		}
	}

	// Default workdir
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = DefaultWorkDir
		report.InferredFields["workdir"] = "default convention"
	}

	// Default branch.default
	if strings.TrimSpace(cfg.Branch.Default) == "" {
		cfg.Branch.Default = DefaultBranchName
		report.InferredFields["branch.default"] = "default convention"
	}

	if err := NormalizeAndValidate(cfg); err != nil {
		return nil, err
	}

	report.Config = cfg
	return report, nil
}

// Load loads and resolves configuration across a 3-tier cascade:
// Tier 1: User / Machine Global (~/.config/git-drev/config.yaml, ~/.git-drev.yaml)
// Tier 2: Shared Global (GIT_DREV_CONFIG)
// Tier 3: Repository Local (.git-drev.yaml, .git-drev/config.yaml in workDir)
func Load(workDir string) (*domain.ProjectConfig, error) {
	report, err := Inspect(workDir)
	if err != nil {
		return nil, err
	}
	return report.Config, nil
}

// NormalizeAndValidate applies defaults, strips trailing slashes on remote.path,
// and validates all fields per docs/spec/global.md Section 5.
func NormalizeAndValidate(cfg *domain.ProjectConfig) error {
	if cfg == nil {
		return errcode.NewError(errcode.ErrConfig, "configuration is nil")
	}

	// 1. Default and validate workdir.
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = DefaultWorkDir
	}
	if err := validateWorkDir(cfg.WorkDir); err != nil {
		return err
	}

	// 2. Default and validate branch.default.
	if strings.TrimSpace(cfg.Branch.Default) == "" {
		cfg.Branch.Default = DefaultBranchName
	}
	if strings.HasPrefix(cfg.Branch.Default, "-") {
		return errcode.NewError(errcode.ErrConfig, "invalid branch.default %q: must not start with '-'", cfg.Branch.Default)
	}

	// 3. Validate remote.host.
	host := strings.TrimSpace(cfg.Remote.Host)
	if host == "" {
		return errcode.NewError(errcode.ErrConfig, "missing required configuration key 'remote.host'")
	}
	if strings.HasPrefix(host, "-") {
		return errcode.NewError(errcode.ErrConfig, "invalid remote.host %q: must not start with '-'", host)
	}
	if strings.ContainsAny(host, " \t\r\n\x00") {
		return errcode.NewError(errcode.ErrConfig, "invalid remote.host %q: must not contain whitespace or control characters", host)
	}
	cfg.Remote.Host = host

	// 4. Normalize and validate remote.path.
	rawPath := strings.TrimSpace(cfg.Remote.Path)
	if rawPath == "" {
		return errcode.NewError(errcode.ErrConfig, "missing required configuration key 'remote.path'")
	}

	// Expand {repo} and {project} template placeholders if still unexpanded.
	if strings.Contains(rawPath, "{repo}") || strings.Contains(rawPath, "{project}") {
		repoName := DetectRepoName("")
		rawPath = strings.ReplaceAll(rawPath, "{repo}", repoName)
		rawPath = strings.ReplaceAll(rawPath, "{project}", repoName)
	}

	if !strings.HasPrefix(rawPath, "/") {
		return errcode.NewError(errcode.ErrConfig, "invalid remote.path %q: must be an absolute POSIX path starting with '/'", rawPath)
	}
	if strings.ContainsAny(rawPath, "`$;&|\n\r\x00") {
		return errcode.NewError(errcode.ErrConfig, "invalid remote.path %q: contains forbidden shell metacharacters", rawPath)
	}

	// Strip trailing slashes and clean POSIX path.
	cleanedPath := strings.TrimRight(rawPath, "/")
	if cleanedPath == "" {
		return errcode.NewError(errcode.ErrConfig, "invalid remote.path \"/\": root directory is not allowed")
	}
	cleanedPath = path.Clean(cleanedPath)
	if cleanedPath == "/" || cleanedPath == "." {
		return errcode.NewError(errcode.ErrConfig, "invalid remote.path %q: root directory is not allowed", rawPath)
	}
	cfg.Remote.Path = cleanedPath

	// 5. Trim remote.rsync options.
	cfg.Remote.Rsync.Get = strings.TrimSpace(cfg.Remote.Rsync.Get)
	cfg.Remote.Rsync.Put = strings.TrimSpace(cfg.Remote.Rsync.Put)

	return nil
}

func validateWorkDir(workDir string) error {
	if strings.ContainsRune(workDir, '\x00') {
		return errcode.NewError(errcode.ErrConfig, "invalid workdir: contains NUL byte")
	}
	if filepath.IsAbs(workDir) || strings.HasPrefix(workDir, "/") || strings.HasPrefix(workDir, `\`) {
		return errcode.NewError(errcode.ErrConfig, "invalid workdir %q: must be a relative path within the repository root", workDir)
	}
	normalized := filepath.Clean(filepath.FromSlash(workDir))
	if normalized == "." || normalized == ".." ||
		strings.HasPrefix(normalized, ".."+string(filepath.Separator)) {
		return errcode.NewError(errcode.ErrConfig, "invalid workdir %q: must not escape repository root or equal '.'", workDir)
	}
	if normalized == ".git" || strings.HasPrefix(normalized, ".git"+string(filepath.Separator)) {
		return errcode.NewError(errcode.ErrConfig, "invalid workdir %q: must not point inside .git directory", workDir)
	}
	return nil
}
