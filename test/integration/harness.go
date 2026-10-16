// Package integration provides isolated Git repository fixtures in t.TempDir()
// for testing git-drev packages and subcommands without touching host Git or SSH state.
package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git-drev/internal/config"
)

// RepoFixture holds paths and isolated environment variables for a disposable
// working repository and its attached bare `origin` remote in t.TempDir().
type RepoFixture struct {
	T          testing.TB
	BaseDir    string
	OriginDir  string
	WorkDir    string
	RemotePath string
	Env        []string
}

// NewRepoFixture creates an isolated bare `origin` repository and a cloned working
// repository on branch `main` with a valid `.drev.json` and an initial commit,
// completely isolated from the host machine's ~/.gitconfig and ~/.ssh/config.
func NewRepoFixture(tb testing.TB) *RepoFixture {
	tb.Helper()

	baseDir := tb.TempDir()
	originDir := filepath.Join(baseDir, "origin.git")
	workDir := filepath.Join(baseDir, "work")
	remoteStorageDir := filepath.Join(baseDir, "remote-storage")
	homeDir := filepath.Join(baseDir, "home")

	for _, d := range []string{originDir, workDir, remoteStorageDir, homeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			tb.Fatalf("mkdir %s: %v", d, err)
		}
	}

	env := IsolatedGitEnv(homeDir)

	f := &RepoFixture{
		T:          tb,
		BaseDir:    baseDir,
		OriginDir:  originDir,
		WorkDir:    workDir,
		RemotePath: filepath.ToSlash(remoteStorageDir),
		Env:        env,
	}

	// 1. Initialize bare origin repository with initial branch 'main'.
	f.MustGit(originDir, "init", "--bare", "--initial-branch=main")

	// 2. Initialize working repository on branch 'main' and attach origin.
	f.MustGit(workDir, "init", "--initial-branch=main")
	f.MustGit(workDir, "config", "user.name", "Drev Test User")
	f.MustGit(workDir, "config", "user.email", "drev-test@example.com")
	f.MustGit(workDir, "remote", "add", "origin", originDir)

	// 3. Write default .drev.json and .gitignore, commit, and push to origin/main.
	f.WriteConfig(config.Config{
		WorkDir:       "data",
		DefaultBranch: "main",
		Remote: config.RemoteConfig{
			Host: "localhost",
			Path: f.RemotePath,
		},
	})
	f.WriteFile(".gitignore", "/data/\n")
	f.MustGit(workDir, "add", ".drev.json", ".gitignore")
	f.MustGit(workDir, "commit", "-m", "Add initial drev configuration")
	f.MustGit(workDir, "push", "-u", "origin", "main")

	return f
}

// IsolatedGitEnv constructs an environment slice that disables system/global
// gitconfig files and sets deterministic author/committer identities.
func IsolatedGitEnv(homeDir string) []string {
	var env []string
	for _, entry := range os.Environ() {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX",
			"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
			"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
			"HOME", "XDG_CONFIG_HOME":
			continue
		default:
			env = append(env, entry)
		}
	}
	return append(env,
		"HOME="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=Drev Test User",
		"GIT_AUTHOR_EMAIL=drev-test@example.com",
		"GIT_COMMITTER_NAME=Drev Test User",
		"GIT_COMMITTER_EMAIL=drev-test@example.com",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"LANG=C",
	)
}

// WriteConfig serializes cfg to <WorkDir>/.drev.json.
func (f *RepoFixture) WriteConfig(cfg config.Config) {
	f.T.Helper()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		f.T.Fatalf("marshal .drev.json: %v", err)
	}
	f.WriteFile(".drev.json", string(b)+"\n")
}

// WriteFile writes content to a relative path inside f.WorkDir, creating parent directories.
func (f *RepoFixture) WriteFile(relPath string, content string) string {
	f.T.Helper()
	abs := filepath.Join(f.WorkDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.T.Fatalf("mkdir parent for %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		f.T.Fatalf("write file %s: %v", relPath, err)
	}
	return abs
}

// MustGit executes a git command in dir using the isolated test environment and returns trimmed stdout.
func (f *RepoFixture) MustGit(dir string, args ...string) string {
	f.T.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = f.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		f.T.Fatalf("git %v in %s failed: %v\nstdout: %s\nstderr: %s", args, dir, err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}
