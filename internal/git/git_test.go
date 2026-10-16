package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git-drev/internal/domain"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"git-drev/test/integration"
)

func TestBuildSanitizedEnv(t *testing.T) {
	t.Parallel()

	env := git.BuildSanitizedEnv([]string{
		"PATH=/usr/bin",
		"LC_ALL=hu_HU.UTF-8",
		"LANG=de_DE.UTF-8",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_ADVICE=1",
		"CUSTOM_VAR=hello",
	})

	got := make(map[string]string, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			got[k] = v
		}
	}

	if got["LC_ALL"] != "C" || got["LANG"] != "C" {
		t.Errorf("expected LC_ALL=C and LANG=C, got LC_ALL=%q LANG=%q", got["LC_ALL"], got["LANG"])
	}
	if got["GIT_TERMINAL_PROMPT"] != "0" || got["GIT_ADVICE"] != "0" {
		t.Errorf("expected GIT_TERMINAL_PROMPT=0 and GIT_ADVICE=0, got %v", got)
	}
	if got["CUSTOM_VAR"] != "hello" {
		t.Errorf("expected CUSTOM_VAR=hello to be preserved")
	}
}

func TestParseCommitByUUIDAndScanAllCommits(t *testing.T) {
	t.Parallel()

	u1 := "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23"
	u2 := "550e8400-e29b-41d4-a716-446655440000"

	// Simulated `git log --all -z --format=%H%x00%s` output where a child branch marker
	// commit shares u1 with its parent revision commit.
	rawFind := strings.Join([]string{
		"1111111111111111111111111111111111111111\x00" + `{"uuid":"` + u1 + `","marker":"true"}`,
		"2222222222222222222222222222222222222222\x00" + `{"uuid":"` + u1 + `","seq":"100"}`,
		"3333333333333333333333333333333333333333\x00Add initial drev configuration",
		"",
	}, "\x00")

	gotHash := git.ParseCommitByUUIDOutput(rawFind, u1)
	if gotHash != "2222222222222222222222222222222222222222" {
		t.Errorf("ParseCommitByUUIDOutput() = %q, want non-marker commit hash", gotHash)
	}
	if gotZero := git.ParseCommitByUUIDOutput(rawFind, domain.ZeroUUID); gotZero != "" {
		t.Errorf("expected ZeroUUID to never match, got %q", gotZero)
	}

	// Simulated `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix` output,
	// including a commit subject containing pipe characters `|` (regression test for legacy usage.sh bug).
	rawScan := strings.Join([]string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x00aaaaaaa\x001700000200\x00HEAD -> main, tag: v1.0, origin/main\x00" + `{"uuid":"` + u2 + `","seq":"200"}`,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\x00bbbbbbb\x001700000100\x00\x00Legacy subject | with | pipes | and " + u1,
		"",
	}, "\x00")

	records := git.ParseScanAllCommitsOutput(rawScan)
	if len(records) != 2 {
		t.Fatalf("ParseScanAllCommitsOutput() returned %d records, want 2", len(records))
	}
	if records[0].Meta == nil || records[0].Meta.UUID != u2 || records[0].Meta.Seq != "200" {
		t.Errorf("unexpected first record meta: %+v", records[0].Meta)
	}
	if len(records[0].Refs) != 3 {
		t.Errorf("expected 3 refs on first record, got %v", records[0].Refs)
	}
	if records[1].Subject != "Legacy subject | with | pipes | and "+u1 {
		t.Errorf("pipe characters corrupted subject: got %q", records[1].Subject)
	}
}

func TestInspectorAndOperationsIntegration(t *testing.T) {
	t.Parallel()

	fix := integration.NewRepoFixture(t)
	ctx := context.Background()

	subDir := filepath.Join(fix.WorkDir, "nested", "dir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := git.NewExecRunner(nil)
	runner.BaseEnv = fix.Env

	insp := git.NewInspector(runner)
	ops := git.NewOperations(runner)

	// 1. RepoRoot from a nested subdirectory resolves root and prefix.
	root, prefix, err := insp.RepoRoot(ctx, subDir)
	if err != nil {
		t.Fatalf("RepoRoot error: %v", err)
	}
	if root != fix.WorkDir {
		t.Errorf("root = %q, want %q", root, fix.WorkDir)
	}
	wantPrefix := filepath.Join("nested", "dir")
	if strings.TrimSuffix(prefix, string(filepath.Separator)) != wantPrefix {
		t.Errorf("prefix = %q, want %q", prefix, wantPrefix)
	}

	// 2. CurrentBranch on attached branch 'main'.
	branch, detached, err := insp.CurrentBranch(ctx, root)
	if err != nil || detached || branch != "main" {
		t.Fatalf("CurrentBranch() = (%q, %v, %v), want (\"main\", false, nil)", branch, detached, err)
	}

	// 3. ValidateBranchName accepts valid names and rejects invalid ones.
	if err := insp.ValidateBranchName(ctx, root, "feature/login-1"); err != nil {
		t.Errorf("ValidateBranchName valid failed: %v", err)
	}
	for _, bad := range []string{"", "-flag", "bad..name", "space name", "@", "HEAD"} {
		if err := insp.ValidateBranchName(ctx, root, bad); !errors.Is(err, errcode.ErrUsage) {
			t.Errorf("ValidateBranchName(%q) = %v, want ErrUsage", bad, err)
		}
	}

	// 4. EnsureRemoteFetchConfig is idempotent.
	if err := ops.EnsureRemoteFetchConfig(ctx, root); err != nil {
		t.Fatalf("EnsureRemoteFetchConfig error: %v", err)
	}

	// 5. Create a new revision branch, commit a marker, then a revision commit, and push.
	if err := ops.CheckoutNewBranch(ctx, root, "release/1.0"); err != nil {
		t.Fatalf("CheckoutNewBranch error: %v", err)
	}
	markerJSON, _ := (domain.RevisionMeta{UUID: domain.ZeroUUID, Marker: "true"}).MarshalSubject()
	if err := ops.CommitAllowEmpty(ctx, root, markerJSON); err != nil {
		t.Fatalf("CommitAllowEmpty marker error: %v", err)
	}

	revUUID := "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23"
	revJSON, _ := (domain.RevisionMeta{UUID: revUUID, Seq: "100"}).MarshalSubject()
	if err := ops.CommitAllowEmpty(ctx, root, revJSON); err != nil {
		t.Fatalf("CommitAllowEmpty rev error: %v", err)
	}

	stale, _, err := ops.PushForceWithLease(ctx, root, "release/1.0", true)
	if err != nil || stale {
		t.Fatalf("PushForceWithLease error: stale=%v err=%v", stale, err)
	}

	// 6. Verify LocalBranchExists, RemoteBranchExists, HeadCommitMeta, and FindCommitByUUID.
	if exists, err := insp.LocalBranchExists(ctx, root, "release/1.0"); err != nil || !exists {
		t.Errorf("LocalBranchExists(release/1.0) = (%v, %v)", exists, err)
	}
	// Hierarchical prefix check: "release" must NOT falsely match "release/1.0".
	if exists, err := insp.LocalBranchExists(ctx, root, "release"); err != nil || exists {
		t.Errorf("LocalBranchExists(release) = (%v, %v), want (false, nil)", exists, err)
	}
	if exists, err := insp.RemoteBranchExists(ctx, root, "release/1.0"); err != nil || !exists {
		t.Errorf("RemoteBranchExists(release/1.0) = (%v, %v)", exists, err)
	}
	if exists, err := insp.RemoteBranchExists(ctx, root, "nonexistent"); err != nil || exists {
		t.Errorf("RemoteBranchExists(nonexistent) = (%v, %v), want (false, nil)", exists, err)
	}

	meta, err := insp.HeadCommitMeta(ctx, root)
	if err != nil || meta.UUID != revUUID || meta.Seq != "100" {
		t.Errorf("HeadCommitMeta() = (%+v, %v)", meta, err)
	}

	foundHash, err := insp.FindCommitByUUID(ctx, root, revUUID)
	if err != nil || len(foundHash) != 40 {
		t.Fatalf("FindCommitByUUID() = (%q, %v)", foundHash, err)
	}

	// 7. Checkout commit hash -> detached HEAD state.
	if err := ops.Checkout(ctx, root, foundHash, false); err != nil {
		t.Fatalf("Checkout commit hash error: %v", err)
	}
	_, detached, err = insp.CurrentBranch(ctx, root)
	if err != nil || !detached {
		t.Errorf("expected detached HEAD after checking out commit hash, got detached=%v err=%v", detached, err)
	}

	// 8. Non-repository and bare repository checks return ErrNotRepository.
	if _, _, err := insp.RepoRoot(ctx, t.TempDir()); !errors.Is(err, errcode.ErrNotRepository) {
		t.Errorf("expected ErrNotRepository outside git worktree, got %v", err)
	}
	if _, _, err := insp.RepoRoot(ctx, fix.OriginDir); !errors.Is(err, errcode.ErrNotRepository) {
		t.Errorf("expected ErrNotRepository inside bare repo, got %v", err)
	}
}
