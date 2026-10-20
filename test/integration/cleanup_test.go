package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runCleanupScript(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	bashBin, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available in PATH")
	}

	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "cleanup.sh"))
	require.NoError(t, err)

	cmdArgs := append([]string{filepath.ToSlash(scriptPath)}, args...)
	cmd := exec.CommandContext(context.Background(), bashBin, cmdArgs...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	return stdout.String(), stderr.String(), runErr
}

func commitWithDate(t *testing.T, f *RepoFixture, msg string, commitTime time.Time) {
	t.Helper()
	dateStr := commitTime.UTC().Format(time.RFC3339)
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", msg)
	cmd.Dir = f.WorkDir
	cmd.Env = append(append([]string{}, f.Env...),
		"GIT_AUTHOR_DATE="+dateStr,
		"GIT_COMMITTER_DATE="+dateStr,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", string(out))
}

func TestCleanupScript(t *testing.T) {
	t.Parallel()

	f := NewRepoFixture(t)
	storageDir := filepath.Join(f.BaseDir, "remote-storage")

	// UUIDs for our test revisions:
	// 1. uuidOldUntagged: 60 days old, ancestor of tag, not in last 2 commits -> SHOULD BE DELETED
	// 2. uuidOldTagged: 45 days old, tagged with annotated tag v1.0.0 -> SHOULD BE KEPT (Rule 3: Tags)
	// 3. uuidOldMarkerParent: 40 days old on main (outside last 2 commits & 30 days),
	//    but referenced by active branch 'feature/marker-only' ("created from <uuid>") -> SHOULD BE KEPT (Rule 4: Marker baseline)
	// 4. uuidRecentByDays: 10 days old on main (outside last 2 commits, inside 30 days) -> SHOULD BE KEPT (Rule 2: Days)
	// 5. uuidRecentCommit1: 2 days old on main (inside last 2 commits) -> SHOULD BE KEPT (Rule 1: Commits)
	// 6. uuidRecentCommit2: 1 day old on main (inside last 2 commits) -> SHOULD BE KEPT (Rule 1: Commits)
	// 7. uuidDeletedBranch: committed on a branch that was pushed then deleted, and orphaned on server -> SHOULD BE DELETED
	uuidOldUntagged := "11111111-1111-4111-8111-111111111111"
	uuidOldTagged := "22222222-2222-4222-8222-222222222222"
	uuidOldMarkerParent := "33333333-3333-4333-8333-333333333333"
	uuidRecentByDays := "44444444-4444-4444-8444-444444444444"
	uuidRecentCommit1 := "55555555-5555-4555-8555-555555555555"
	uuidRecentCommit2 := "66666666-6666-4666-8666-666666666666"
	uuidDeletedBranch := "77777777-7777-4777-8777-777777777777"
	nonUUIDDir := "not-a-uuid-dir"

	allUUIDs := []string{
		uuidOldUntagged,
		uuidOldTagged,
		uuidOldMarkerParent,
		uuidRecentByDays,
		uuidRecentCommit1,
		uuidRecentCommit2,
		uuidDeletedBranch,
	}

	for _, u := range allUUIDs {
		require.NoError(t, os.MkdirAll(filepath.Join(storageDir, u), 0o755))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(storageDir, nonUUIDDir), 0o755))

	now := time.Now()

	// Commit 1 (60 days ago): uuidOldUntagged
	commitWithDate(t, f, `{"uuid":"`+uuidOldUntagged+`","seq":"1"}`, now.AddDate(0, 0, -60))

	// Commit 2 (45 days ago): uuidOldTagged + annotated tag v1.0.0
	commitWithDate(t, f, `{"uuid":"`+uuidOldTagged+`","seq":"2"}`, now.AddDate(0, 0, -45))
	f.MustGit(f.WorkDir, "tag", "-a", "v1.0.0", "-m", "Release 1.0.0")

	// Commit 3 (40 days ago): uuidOldMarkerParent
	commitWithDate(t, f, `{"uuid":"`+uuidOldMarkerParent+`","seq":"3"}`, now.AddDate(0, 0, -40))

	// Create branch 'feature/marker-only' from Commit 3 with a recent marker commit referencing uuidOldMarkerParent
	f.MustGit(f.WorkDir, "checkout", "-b", "feature/marker-only")
	commitWithDate(t, f, `{"uuid":"00000000-0000-0000-0000-000000000000","seq":"0","msg":"created from `+uuidOldMarkerParent+`"}`, now.AddDate(0, 0, -1))
	f.MustGit(f.WorkDir, "push", "-u", "origin", "feature/marker-only")

	// Switch back to main and add remaining commits
	f.MustGit(f.WorkDir, "checkout", "main")

	// Commit 4 (10 days ago): uuidRecentByDays (outside last 2 commits, inside 30 days)
	commitWithDate(t, f, `{"uuid":"`+uuidRecentByDays+`","seq":"4"}`, now.AddDate(0, 0, -10))

	// Commit 5 (2 days ago): uuidRecentCommit1 (inside last 2 commits)
	commitWithDate(t, f, `{"uuid":"`+uuidRecentCommit1+`","seq":"5"}`, now.AddDate(0, 0, -2))

	// Commit 6 (1 day ago): uuidRecentCommit2 (inside last 2 commits)
	commitWithDate(t, f, `{"uuid":"`+uuidRecentCommit2+`","seq":"6"}`, now.AddDate(0, 0, -1))

	f.MustGit(f.WorkDir, "push", "origin", "main", "--tags")

	// Verify dry-run (-n) reports 2 deletions without removing directories
	stdout, stderr, err := runCleanupScript(
		t,
		f.Env,
		"--dry-run",
		"--days", "30",
		"--commits", "2",
		filepath.ToSlash(f.OriginDir),
		filepath.ToSlash(storageDir),
	)
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Dry run complete: 2 revisions would be deleted, 5 kept.")
	assert.DirExists(t, filepath.Join(storageDir, uuidOldUntagged))
	assert.DirExists(t, filepath.Join(storageDir, uuidDeletedBranch))

	// Execute actual cleanup (--days 30 --commits 2)
	stdout, stderr, err = runCleanupScript(
		t,
		f.Env,
		"--days", "30",
		"--commits", "2",
		filepath.ToSlash(f.OriginDir),
		filepath.ToSlash(storageDir),
	)
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Cleanup complete: 2 revisions deleted, 5 kept.")

	// Deleted:
	assert.NoDirExists(t, filepath.Join(storageDir, uuidOldUntagged), "ancient untagged ancestor behind tag should be deleted")
	assert.NoDirExists(t, filepath.Join(storageDir, uuidDeletedBranch), "orphaned revision should be deleted")

	// Retained:
	assert.DirExists(t, filepath.Join(storageDir, uuidOldTagged), "annotated tagged revision must be retained")
	assert.DirExists(t, filepath.Join(storageDir, uuidOldMarkerParent), "marker parent baseline revision must be retained")
	assert.DirExists(t, filepath.Join(storageDir, uuidRecentByDays), "revision within --days window must be retained")
	assert.DirExists(t, filepath.Join(storageDir, uuidRecentCommit1), "revision within --commits window must be retained")
	assert.DirExists(t, filepath.Join(storageDir, uuidRecentCommit2), "revision within --commits window must be retained")
	assert.DirExists(t, filepath.Join(storageDir, nonUUIDDir), "non-UUID directory must be untouched")
}
