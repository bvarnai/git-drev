package integration_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bvarnai/git-drev/internal/cli"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/test/integration"
)

func TestPureGit_CreateSelectUpdateWorkflow(t *testing.T) {
	f := integration.NewRepoFixture(t)

	// 1. Create branch 'feature/alpha'
	var stdout, stderr bytes.Buffer
	if err := f.RunDrev(&stdout, &stderr, "create", "feature/alpha"); err != nil {
		t.Fatalf("create feature/alpha failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify current branch is feature/alpha
	currBranch := f.MustGit(f.WorkDir, "symbolic-ref", "--short", "HEAD")
	if currBranch != "feature/alpha" {
		t.Fatalf("expected current branch 'feature/alpha', got %q", currBranch)
	}

	// Verify marker commit exists with a valid ZeroUUID
	logSubject := f.MustGit(f.WorkDir, "log", "-1", "--format=%s")
	if !strings.Contains(logSubject, "00000000-0000-0000-0000-000000000000") || !strings.Contains(logSubject, `"marker":"true"`) {
		t.Fatalf("unexpected marker commit subject: %q", logSubject)
	}

	// 2. Query name
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "name"); err != nil {
		t.Fatalf("name command failed: %v\nstderr: %s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "feature/alpha" {
		t.Fatalf("expected 'feature/alpha', got %q", stdout.String())
	}

	// 3. Create second branch 'feature/beta'
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "create", "feature/beta"); err != nil {
		t.Fatalf("create feature/beta failed: %v\nstderr: %s", err, stderr.String())
	}

	// 4. Select back to feature/alpha
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "select", "feature/alpha"); err != nil {
		t.Fatalf("select feature/alpha failed: %v\nstderr: %s", err, stderr.String())
	}

	currBranch = f.MustGit(f.WorkDir, "symbolic-ref", "--short", "HEAD")
	if currBranch != "feature/alpha" {
		t.Fatalf("expected current branch 'feature/alpha' after select, got %q", currBranch)
	}

	// 5. Run update (noop since up to date)
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "update"); err != nil {
		t.Fatalf("update failed: %v\nstderr: %s", err, stderr.String())
	}
}

func TestPureGit_Config(t *testing.T) {
	f := integration.NewRepoFixture(t)

	var stdout, stderr bytes.Buffer

	// Standard config display
	if err := f.RunDrev(&stdout, &stderr, "config"); err != nil {
		t.Fatalf("config command failed: %v\nstderr: %s", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "workdir") || !strings.Contains(out, "data") {
		t.Fatalf("config output missing expected keys: %s", out)
	}

	// Verbose config display with provenance
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "config", "-v"); err != nil {
		t.Fatalf("config -v command failed: %v\nstderr: %s", err, stderr.String())
	}
	outV := stdout.String()
	if !strings.Contains(outV, ".git-drev.yaml") {
		t.Fatalf("config -v output missing provenance: %s", outV)
	}
}

func TestPureGit_InfoHook(t *testing.T) {
	f := integration.NewRepoFixture(t)

	var stdout, stderr bytes.Buffer

	// Case 1: No hook file present -> exits 0 with informational message on stderr
	if err := f.RunDrev(&stdout, &stderr, "info"); err != nil {
		t.Fatalf("info without hook failed: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "No info hook found") {
		t.Fatalf("expected informational log on stderr, got: %s", stderr.String())
	}

	// Case 2: Bash function hook present -> executes and outputs to stdout
	hookContent := `#!/usr/bin/env bash
info_hook() {
    echo "Hook executed successfully for branch: ${GIT_DREV_BRANCH}"
}
`
	hookPath := f.WriteFile(".git-drev-info-hook", hookContent)
	if err := os.Chmod(hookPath, 0o755); err != nil {
		t.Fatalf("chmod hook: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "info"); err != nil {
		t.Fatalf("info with hook failed: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Hook executed successfully for branch: main") {
		t.Fatalf("expected hook output in stdout, got: %q", stdout.String())
	}
}

func TestPureGit_SubdirectoryExecution(t *testing.T) {
	f := integration.NewRepoFixture(t)

	// Create a nested subdirectory
	subDir := filepath.Join(f.WorkDir, "sub", "deep", "dir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subDir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := f.NewApp(&stdout, &stderr)
	app.WorkingDir = subDir // simulate running from subdirectory
	app.RootCmd.SetArgs([]string{"name"})

	if err := app.Execute(context.Background()); err != nil {
		t.Fatalf("name from subdirectory failed: %v\nstderr: %s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "main" {
		t.Fatalf("expected 'main', got %q", stdout.String())
	}
}

func TestPureGit_InitWorkflow(t *testing.T) {
	baseDir := t.TempDir()
	initDir := filepath.Join(baseDir, "new-repo")
	if err := os.MkdirAll(initDir, 0o755); err != nil {
		t.Fatalf("mkdir initDir: %v", err)
	}

	storageDir := filepath.Join(baseDir, "storage")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatalf("mkdir storageDir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runner := git.NewExecRunner()
	runner.BaseEnv = integration.IsolatedGitEnv(filepath.Join(baseDir, "home"))

	app := cli.NewApp(&stdout, &stderr, runner)
	app.WorkingDir = initDir
	app.Storage = integration.NewLocalRemoteRunner(baseDir, storageDir, "/srv/drev/myrepo")

	// 1. Run git-drev init with -y to bootstrap fresh repo and commit
	app.RootCmd.SetArgs([]string{
		"init",
		"-y",
		"--host", "storage.example.com",
		"--path", "/srv/drev/myrepo",
		"--workdir", "data",
		"--branch", "main",
	})

	if err := app.Execute(context.Background()); err != nil {
		t.Fatalf("git-drev init failed: %v\nstderr: %s", err, stderr.String())
	}

	// 2. Verify git was initialized and commit was made
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = initDir
	cmd.Env = runner.BaseEnv
	logOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log failed: %v, out: %s", err, string(logOut))
	}
	if strings.TrimSpace(string(logOut)) != "chore: initialize git-drev configuration" {
		t.Fatalf("unexpected initial commit subject: %q", string(logOut))
	}

	// 3. Verify .git-drev.yaml was written
	cfgBytes, err := os.ReadFile(filepath.Join(initDir, ".git-drev.yaml"))
	if err != nil {
		t.Fatalf("read .git-drev.yaml: %v", err)
	}
	if !strings.Contains(string(cfgBytes), `host: "storage.example.com"`) || !strings.Contains(string(cfgBytes), `path: "/srv/drev/myrepo"`) {
		t.Fatalf("unexpected .git-drev.yaml content: %s", string(cfgBytes))
	}

	// 4. Verify .gitignore contains /data
	ignoreBytes, err := os.ReadFile(filepath.Join(initDir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(ignoreBytes), "/data\n") {
		t.Fatalf("expected /data in .gitignore, got: %s", string(ignoreBytes))
	}

	// 5. Verify git-drev name works
	stdout.Reset()
	stderr.Reset()
	nameApp := cli.NewApp(&stdout, &stderr, runner)
	nameApp.WorkingDir = initDir
	nameApp.Storage = app.Storage
	nameApp.RootCmd.SetArgs([]string{"name"})
	if err := nameApp.Execute(context.Background()); err != nil {
		t.Fatalf("git-drev name failed: %v\nstderr: %s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "main" {
		t.Fatalf("expected branch 'main', got %q", stdout.String())
	}

	// 6. Running init again without --force fails due to existing non-empty repo / config guard
	stdout.Reset()
	stderr.Reset()
	reInitApp := cli.NewApp(&stdout, &stderr, runner)
	reInitApp.WorkingDir = initDir
	reInitApp.Storage = app.Storage
	reInitApp.RootCmd.SetArgs([]string{"init", "-y", "--host", "storage.example.com", "--path", "/srv/drev/myrepo"})
	if err := reInitApp.Execute(context.Background()); err == nil {
		t.Fatalf("expected re-init without --force to fail on non-empty repo")
	}

	// 7. Running init with --force succeeds
	stdout.Reset()
	stderr.Reset()
	forceInitApp := cli.NewApp(&stdout, &stderr, runner)
	forceInitApp.WorkingDir = initDir
	forceInitApp.Storage = app.Storage
	forceInitApp.RootCmd.SetArgs([]string{"init", "-y", "--force", "--host", "storage.example.com", "--path", "/srv/drev/myrepo"})
	if err := forceInitApp.Execute(context.Background()); err != nil {
		t.Fatalf("expected force re-init to succeed, got: %v\nstderr: %s", err, stderr.String())
	}
}
