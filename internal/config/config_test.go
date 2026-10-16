package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git-drev/internal/config"
	"git-drev/internal/errcode"
)

type stubGitConfig struct {
	overrides map[string]string
	err       error
}

func (s stubGitConfig) ConfigOverrides(_ context.Context) (map[string]string, error) {
	return s.overrides, s.err
}

func TestLoadAndOverrides(t *testing.T) {
	t.Parallel()

	t.Run("loads valid .drev.json and applies defaults", func(t *testing.T) {
		t.Parallel()
		repoRoot := t.TempDir()
		content := `{
			"remote": {
				"host": "drev-server",
				"path": "/var/drev/myproject///"
			}
		}`
		if err := os.WriteFile(filepath.Join(repoRoot, ".drev.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := config.Load(context.Background(), config.LoadOptions{
			RepoRoot: repoRoot,
		})
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}
		if cfg.WorkDir != "data" {
			t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, "data")
		}
		if cfg.DefaultBranch != "main" {
			t.Errorf("DefaultBranch = %q, want %q", cfg.DefaultBranch, "main")
		}
		if cfg.Remote.Host != "drev-server" {
			t.Errorf("Remote.Host = %q, want %q", cfg.Remote.Host, "drev-server")
		}
		if cfg.Remote.Path != "/var/drev/myproject" {
			t.Errorf("Remote.Path = %q, want %q", cfg.Remote.Path, "/var/drev/myproject")
		}
	})

	t.Run("applies git config drev.* overrides and GIT_DREV_CONFIG path", func(t *testing.T) {
		t.Parallel()
		repoRoot := t.TempDir()
		customPath := filepath.Join(repoRoot, "configs", "custom.json")
		if err := os.MkdirAll(filepath.Dir(customPath), 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{
			"workDir": "artifacts",
			"defaultBranch": "develop",
			"remote": {
				"host": "json-host",
				"path": "/var/drev/from-json",
				"rsyncOptions": {
					"get": "--stats",
					"put": "--whole-file"
				}
			}
		}`
		if err := os.WriteFile(customPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := config.Load(context.Background(), config.LoadOptions{
			RepoRoot: repoRoot,
			EnvLookup: func(k string) (string, bool) {
				if k == "GIT_DREV_CONFIG" {
					return "configs/custom.json", true
				}
				return "", false
			},
			GitConfig: stubGitConfig{
				overrides: map[string]string{
					"drev.workdir":                "override-dir",
					"drev.defaultbranch":          "trunk",
					"drev.remote.host":            "override-host",
					"drev.remote.path":            "/srv/drev/override/",
					"drev.remote.rsyncgetoptions": "-z --compress",
					"drev.remote.rsyncputoptions": "-z --inplace",
				},
			},
		})
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}
		if cfg.WorkDir != "override-dir" || cfg.DefaultBranch != "trunk" {
			t.Errorf("got workDir=%q defaultBranch=%q", cfg.WorkDir, cfg.DefaultBranch)
		}
		if cfg.Remote.Host != "override-host" || cfg.Remote.Path != "/srv/drev/override" {
			t.Errorf("got host=%q path=%q", cfg.Remote.Host, cfg.Remote.Path)
		}
		if cfg.Remote.RsyncOptions.Get != "-z --compress" || cfg.Remote.RsyncOptions.Put != "-z --inplace" {
			t.Errorf("got rsyncOptions=%+v", cfg.Remote.RsyncOptions)
		}
	})

	t.Run("missing .drev.json returns ErrConfig", func(t *testing.T) {
		t.Parallel()
		_, err := config.Load(context.Background(), config.LoadOptions{
			RepoRoot: t.TempDir(),
		})
		if !errors.Is(err, errcode.ErrConfig) {
			t.Errorf("expected ErrConfig, got %v", err)
		}
	})

	t.Run("malformed JSON returns ErrConfig", func(t *testing.T) {
		t.Parallel()
		repoRoot := t.TempDir()
		if err := os.WriteFile(filepath.Join(repoRoot, ".drev.json"), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := config.Load(context.Background(), config.LoadOptions{
			RepoRoot: repoRoot,
		})
		if !errors.Is(err, errcode.ErrConfig) {
			t.Errorf("expected ErrConfig, got %v", err)
		}
	})
}

func TestNormalizeAndValidateRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config
	}{
		{
			name: "missing remote.host",
			cfg:  config.Config{Remote: config.RemoteConfig{Path: "/var/drev/p"}},
		},
		{
			name: "remote.host starting with dash (SSH option injection)",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "-oProxyCommand=evil", Path: "/var/drev/p"}},
		},
		{
			name: "missing remote.path",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "server"}},
		},
		{
			name: "relative remote.path",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "server", Path: "var/drev/p"}},
		},
		{
			name: "root remote.path '/'",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "server", Path: "/"}},
		},
		{
			name: "multiple slashes remote.path '///'",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "server", Path: "///"}},
		},
		{
			name: "remote.path with shell metacharacters",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "server", Path: "/var/drev/$(whoami)"}},
		},
		{
			name: "remote.path with semicolon",
			cfg:  config.Config{Remote: config.RemoteConfig{Host: "server", Path: "/var/drev/a;rm -rf /"}},
		},
		{
			name: "workDir parent traversal",
			cfg: config.Config{
				WorkDir: "../outside",
				Remote:  config.RemoteConfig{Host: "server", Path: "/var/drev/p"},
			},
		},
		{
			name: "workDir absolute path",
			cfg: config.Config{
				WorkDir: "/tmp/abs",
				Remote:  config.RemoteConfig{Host: "server", Path: "/var/drev/p"},
			},
		},
		{
			name: "workDir dot",
			cfg: config.Config{
				WorkDir: ".",
				Remote:  config.RemoteConfig{Host: "server", Path: "/var/drev/p"},
			},
		},
		{
			name: "workDir inside .git",
			cfg: config.Config{
				WorkDir: ".git/objects",
				Remote:  config.RemoteConfig{Host: "server", Path: "/var/drev/p"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.cfg
			err := config.NormalizeAndValidate(&c)
			if !errors.Is(err, errcode.ErrConfig) {
				t.Errorf("NormalizeAndValidate(%+v) = %v, want ErrConfig", tc.cfg, err)
			}
		})
	}
}
