// Package config loads, merges, and validates repository configuration from
// .drev.json (or $GIT_DREV_CONFIG) and git config drev.* overrides.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"git-drev/internal/errcode"
)

// Default values per docs/spec/global.md Section 5.
const (
	DefaultConfigFile = ".drev.json"
	DefaultWorkDir    = "data"
	DefaultBranchName = "main"
)

// Config holds the resolved configuration from .drev.json and git config drev.*.
type Config struct {
	WorkDir       string       `json:"workDir"`
	DefaultBranch string       `json:"defaultBranch"`
	Remote        RemoteConfig `json:"remote"`
}

// RemoteConfig defines the SSH/rsync storage backend parameters.
type RemoteConfig struct {
	Host         string       `json:"host"`
	Path         string       `json:"path"`
	RsyncOptions RsyncOptions `json:"rsyncOptions"`
}

// RsyncOptions defines per-direction custom rsync arguments.
type RsyncOptions struct {
	Get string `json:"get"`
	Put string `json:"put"`
}

// GitConfigReader abstracts reading git config drev.* overrides so package config
// does not depend on subprocess implementation details.
type GitConfigReader interface {
	ConfigOverrides(ctx context.Context) (map[string]string, error)
}

// LoadOptions customizes how Load resolves the configuration file and environment.
type LoadOptions struct {
	RepoRoot  string
	EnvLookup func(key string) (string, bool)
	GitConfig GitConfigReader
}

// Load reads .drev.json (or the path in GIT_DREV_CONFIG), applies any git config drev.*
// overrides, sets defaults for optional fields, and validates all schema constraints.
func Load(ctx context.Context, opts LoadOptions) (*Config, error) {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return nil, errcode.NewError(errcode.ErrNotRepository, "repository root is empty")
	}

	envLookup := opts.EnvLookup
	if envLookup == nil {
		envLookup = os.LookupEnv
	}

	cfgPath, displayPath, err := ResolveConfigPath(opts.RepoRoot, envLookup)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errcode.NewError(errcode.ErrConfig, "No configuration file '%s' found", displayPath)
		}
		return nil, errcode.WrapError(errcode.ErrConfig, err, "Failed to read configuration file '%s'", displayPath)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, errcode.WrapError(errcode.ErrConfig, err, "Invalid JSON in configuration file '%s'", displayPath)
	}

	if opts.GitConfig != nil {
		overrides, err := opts.GitConfig.ConfigOverrides(ctx)
		if err != nil {
			return nil, err
		}
		ApplyGitOverrides(&cfg, overrides)
	}

	if err := NormalizeAndValidate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ResolveConfigPath resolves the absolute path to .drev.json or the GIT_DREV_CONFIG override.
func ResolveConfigPath(repoRoot string, envLookup func(string) (string, bool)) (absPath string, displayPath string, err error) {
	if envLookup != nil {
		if raw, ok := envLookup("GIT_DREV_CONFIG"); ok {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return "", "", errcode.NewError(errcode.ErrConfig, "GIT_DREV_CONFIG is set but empty")
			}
			if filepath.IsAbs(trimmed) {
				return filepath.Clean(trimmed), trimmed, nil
			}
			return filepath.Join(repoRoot, trimmed), trimmed, nil
		}
	}
	return filepath.Join(repoRoot, DefaultConfigFile), DefaultConfigFile, nil
}

// ApplyGitOverrides applies `git config drev.*` keys over the loaded Config.
// Git config keys are normalized to lowercase by `git config --get-regexp`.
func ApplyGitOverrides(cfg *Config, overrides map[string]string) {
	if cfg == nil || len(overrides) == 0 {
		return
	}
	for k, v := range overrides {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "drev.workdir":
			cfg.WorkDir = v
		case "drev.defaultbranch":
			cfg.DefaultBranch = v
		case "drev.remote.host":
			cfg.Remote.Host = v
		case "drev.remote.path":
			cfg.Remote.Path = v
		case "drev.remote.rsyncgetoptions":
			cfg.Remote.RsyncOptions.Get = v
		case "drev.remote.rsyncputoptions":
			cfg.Remote.RsyncOptions.Put = v
		}
	}
}

// NormalizeAndValidate applies defaults, strips trailing slashes on remote.path,
// and validates all fields per docs/spec/global.md Section 5.
func NormalizeAndValidate(cfg *Config) error {
	if cfg == nil {
		return errcode.NewError(errcode.ErrConfig, "configuration is nil")
	}

	// 1. Default and validate workDir.
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = DefaultWorkDir
	}
	if err := validateWorkDir(cfg.WorkDir); err != nil {
		return err
	}

	// 2. Default and validate defaultBranch.
	if strings.TrimSpace(cfg.DefaultBranch) == "" {
		cfg.DefaultBranch = DefaultBranchName
	}
	if strings.HasPrefix(cfg.DefaultBranch, "-") {
		return errcode.NewError(errcode.ErrConfig, "invalid defaultBranch %q: must not start with '-'", cfg.DefaultBranch)
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

	// 5. Trim rsyncOptions.
	cfg.Remote.RsyncOptions.Get = strings.TrimSpace(cfg.Remote.RsyncOptions.Get)
	cfg.Remote.RsyncOptions.Put = strings.TrimSpace(cfg.Remote.RsyncOptions.Put)

	return nil
}

func validateWorkDir(workDir string) error {
	if strings.ContainsRune(workDir, '\x00') {
		return errcode.NewError(errcode.ErrConfig, "invalid workDir: contains NUL byte")
	}
	if filepath.IsAbs(workDir) || strings.HasPrefix(workDir, "/") || strings.HasPrefix(workDir, `\`) {
		return errcode.NewError(errcode.ErrConfig, "invalid workDir %q: must be a relative path within the repository root", workDir)
	}
	normalized := filepath.Clean(filepath.FromSlash(workDir))
	if normalized == "." || normalized == ".." ||
		strings.HasPrefix(normalized, ".."+string(filepath.Separator)) {
		return errcode.NewError(errcode.ErrConfig, "invalid workDir %q: must not escape repository root or equal '.'", workDir)
	}
	if normalized == ".git" || strings.HasPrefix(normalized, ".git"+string(filepath.Separator)) {
		return errcode.NewError(errcode.ErrConfig, "invalid workDir %q: must not point inside .git directory", workDir)
	}
	return nil
}
