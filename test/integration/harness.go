// Package integration provides isolated Git repository fixtures in t.TempDir()
// for testing git-drev packages and subcommands without touching host Git or SSH state.
package integration

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/git"
	"gopkg.in/yaml.v3"
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
// repository on branch `main` with a valid `.git-drev.yaml` and an initial commit,
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

	// 3. Write default .git-drev.yaml and .gitignore, commit, and push to origin/main.
	f.WriteConfig(domain.ProjectConfig{
		WorkDir: "data",
		Branch: domain.BranchConfig{
			Default: "main",
		},
		Remote: domain.RemoteConfig{
			Host: "localhost",
			Path: f.RemotePath,
		},
	})
	f.WriteFile(".gitignore", "/data/\n")
	f.MustGit(workDir, "add", ".git-drev.yaml", ".gitignore")
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

// WriteConfig serializes cfg to <WorkDir>/.git-drev.yaml.
func (f *RepoFixture) WriteConfig(cfg domain.ProjectConfig) {
	f.T.Helper()
	b, err := yaml.Marshal(cfg)
	if err != nil {
		f.T.Fatalf("marshal .git-drev.yaml: %v", err)
	}
	f.WriteFile(".git-drev.yaml", string(b))
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

// NewApp creates a *cli.App pre-wired with the fixture's working directory,
// isolated git environment, and LocalRemoteRunner.
func (f *RepoFixture) NewApp(stdout, stderr io.Writer) *cli.App {
	runner := git.NewExecRunner()
	runner.BaseEnv = f.Env
	app := cli.NewApp(stdout, stderr, runner)
	app.WorkingDir = f.WorkDir
	app.Storage = NewLocalRemoteRunner(f.BaseDir)
	return app
}

// RunDrev creates a fresh App and executes git-drev with args against f.WorkDir.
func (f *RepoFixture) RunDrev(stdout, stderr io.Writer, args ...string) error {
	app := f.NewApp(stdout, stderr)
	app.RootCmd.SetArgs(args)
	return app.Execute(context.Background())
}

// CloneReplica creates a secondary working repository clone of origin in <BaseDir>/<name>.
func (f *RepoFixture) CloneReplica(name string) *RepoFixture {
	f.T.Helper()
	replicaDir := filepath.Join(f.BaseDir, name)
	f.MustGit(f.BaseDir, "clone", f.OriginDir, replicaDir)
	f.MustGit(replicaDir, "config", "user.name", "Drev Replica User")
	f.MustGit(replicaDir, "config", "user.email", "drev-replica@example.com")
	return &RepoFixture{
		T:          f.T,
		BaseDir:    f.BaseDir,
		OriginDir:  f.OriginDir,
		WorkDir:    replicaDir,
		RemotePath: f.RemotePath,
		Env:        f.Env,
	}
}
