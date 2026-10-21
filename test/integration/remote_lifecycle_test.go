package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/test/integration"
)

func TestRemoteLifecycle_PutGetUsageWorkflow(t *testing.T) {
	f := integration.NewRepoFixture(t)

	// 1. Create a branch 'feature/dataset'
	var stdout, stderr bytes.Buffer
	if err := f.RunDrev(&stdout, &stderr, "create", "feature/dataset"); err != nil {
		t.Fatalf("create failed: %v\nstderr: %s", err, stderr.String())
	}

	// 2. Populate workdir 'data' with files
	f.WriteFile("data/model.bin", "model-weights-v1-bytes-content-1234567890")
	f.WriteFile("data/nested/params.json", `{"lr": 0.001, "batch_size": 32}`)

	// 3. Put revision seq 100
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "put", "--sequence", "100"); err != nil {
		t.Fatalf("put failed: %v\nstderr: %s", err, stderr.String())
	}

	// Parse the commit metadata on feature/dataset
	logSubject := f.MustGit(f.WorkDir, "log", "-1", "--format=%s")
	meta, err := domain.ParseRevisionMeta(logSubject)
	if err != nil {
		t.Fatalf("failed to parse revision meta from commit: %v (subject: %q)", err, logSubject)
	}
	if meta.UUID == "" || meta.UUID == domain.ZeroUUID {
		t.Fatalf("expected non-zero UUID, got %q", meta.UUID)
	}
	if meta.Seq != "100" {
		t.Fatalf("expected seq 100, got %q", meta.Seq)
	}

	// Verify remote storage received files under f.StorageDir/<uuid>/
	remoteRevDir := filepath.Join(f.StorageDir, meta.UUID)
	data, err := os.ReadFile(filepath.Join(remoteRevDir, "model.bin"))
	if err != nil {
		t.Fatalf("remote file model.bin missing: %v", err)
	}
	if string(data) != "model-weights-v1-bytes-content-1234567890" {
		t.Fatalf("remote file content mismatch: got %q", string(data))
	}

	// 4. Clone a secondary replica
	replica := f.CloneReplica("replica")

	// In replica, select feature/dataset
	stdout.Reset()
	stderr.Reset()
	if err := replica.RunDrev(&stdout, &stderr, "select", "feature/dataset"); err != nil {
		t.Fatalf("replica select failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify data folder is empty in replica before get
	if _, err := os.Stat(filepath.Join(replica.WorkDir, "data", "model.bin")); !os.IsNotExist(err) {
		t.Fatalf("expected data/model.bin to not exist before get")
	}

	// Run get in replica
	stdout.Reset()
	stderr.Reset()
	if err := replica.RunDrev(&stdout, &stderr, "get"); err != nil {
		t.Fatalf("replica get failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify files arrived in replica/data
	gotData, err := os.ReadFile(filepath.Join(replica.WorkDir, "data", "model.bin"))
	if err != nil {
		t.Fatalf("replica model.bin missing after get: %v", err)
	}
	if string(gotData) != "model-weights-v1-bytes-content-1234567890" {
		t.Fatalf("replica content mismatch: got %q", string(gotData))
	}

	// 5. Query usage in replica
	stdout.Reset()
	stderr.Reset()
	if err := replica.RunDrev(&stdout, &stderr, "usage", "-v"); err != nil {
		t.Fatalf("usage -v failed: %v\nstderr: %s", err, stderr.String())
	}
	usageSummaryOut := stdout.String()
	if strings.Contains(usageSummaryOut, "Revisions breakdown:") {
		t.Fatalf("usage -v without -a unexpectedly contained detailed revisions breakdown:\n%s", usageSummaryOut)
	}
	if !strings.Contains(stderr.String(), "Debug:") {
		t.Fatalf("usage -v expected debug logs on stderr:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := replica.RunDrev(&stdout, &stderr, "usage", "-a", "-v"); err != nil {
		t.Fatalf("usage -a -v failed: %v\nstderr: %s", err, stderr.String())
	}
	usageOut := stdout.String()
	if !strings.Contains(usageOut, meta.UUID) {
		t.Fatalf("usage table missing UUID %s:\n%s", meta.UUID, usageOut)
	}
	if !strings.Contains(usageOut, "feature/dataset") {
		t.Fatalf("usage table missing branch feature/dataset:\n%s", usageOut)
	}
	if strings.Contains(usageOut, "[orphaned]") {
		t.Fatalf("usage table unexpectedly marked active UUID as orphaned:\n%s", usageOut)
	}

	// 6. Test orphaned directory detection in usage
	orphanedUUID := "11111111-2222-3333-4444-555555555555"
	orphanedDir := filepath.Join(f.StorageDir, orphanedUUID)
	if err := os.MkdirAll(orphanedDir, 0o755); err != nil {
		t.Fatalf("mkdir orphaned: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphanedDir, "dummy.txt"), []byte("orphan"), 0o644); err != nil {
		t.Fatalf("write orphaned: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := replica.RunDrev(&stdout, &stderr, "usage", "-a"); err != nil {
		t.Fatalf("usage -a with orphan failed: %v\nstderr: %s", err, stderr.String())
	}
	orphanOut := stdout.String()
	if !strings.Contains(orphanOut, orphanedUUID) || !strings.Contains(orphanOut, "[orphaned]") {
		t.Fatalf("expected orphaned row in usage table:\n%s", orphanOut)
	}
}

func TestRemoteLifecycle_PutHookExecution(t *testing.T) {
	f := integration.NewRepoFixture(t)

	// Create branch
	var stdout, stderr bytes.Buffer
	if err := f.RunDrev(&stdout, &stderr, "create", "feature/hooked"); err != nil {
		t.Fatalf("create failed: %v\nstderr: %s", err, stderr.String())
	}

	// Write .git-drev-put-hook that asserts GIT_DREV_SEQ and creates a stamp file
	stampFile := filepath.Join(f.BaseDir, "hook_ran.txt")
	hookContent := `#!/usr/bin/env bash
put_hook() {
    if [ "${GIT_DREV_SEQ}" != "42" ]; then
        echo "Unexpected GIT_DREV_SEQ: ${GIT_DREV_SEQ}" >&2
        return 1
    fi
    echo "Hook executed for UUID ${GIT_DREV_UUID}" > "` + filepath.ToSlash(stampFile) + `"
}
`
	hookPath := f.WriteFile(".git-drev-put-hook", hookContent)
	if err := os.Chmod(hookPath, 0o755); err != nil {
		t.Fatalf("chmod hook: %v", err)
	}

	f.WriteFile("data/sample.txt", "sample payload")

	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "put", "--sequence", "42"); err != nil {
		t.Fatalf("put with hook failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify stamp file exists and contains UUID
	stampData, err := os.ReadFile(stampFile)
	if err != nil {
		t.Fatalf("put hook stamp file was not created: %v", err)
	}
	if !strings.Contains(string(stampData), "Hook executed for UUID") {
		t.Fatalf("unexpected stamp content: %s", string(stampData))
	}
}

func TestRemoteLifecycle_ConcurrentPushOptimisticRetry(t *testing.T) {
	f := integration.NewRepoFixture(t)

	// 1. Create branch
	var stdout, stderr bytes.Buffer
	if err := f.RunDrev(&stdout, &stderr, "create", "feature/concurrent"); err != nil {
		t.Fatalf("create failed: %v\nstderr: %s", err, stderr.String())
	}

	// 2. Clone replica after branch creation
	replica := f.CloneReplica("replica")
	if err := replica.RunDrev(&stdout, &stderr, "select", "feature/concurrent"); err != nil {
		t.Fatalf("replica select failed: %v\nstderr: %s", err, stderr.String())
	}

	// 3. Main repo puts revision 10
	f.WriteFile("data/file.txt", "v1-from-main")
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "put", "--sequence", "10"); err != nil {
		t.Fatalf("main put seq 10 failed: %v\nstderr: %s", err, stderr.String())
	}

	// 4. Replica puts revision 20 concurrently
	// (Its local origin/feature/concurrent is behind main's push)
	replica.WriteFile("data/file.txt", "v2-from-replica")
	stdout.Reset()
	stderr.Reset()
	if err := replica.RunDrev(&stdout, &stderr, "put", "--sequence", "20"); err != nil {
		t.Fatalf("replica put seq 20 failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify replica has both revisions in history
	logSubjects := replica.MustGit(replica.WorkDir, "log", "--format=%s", "-n", "3")
	if !strings.Contains(logSubjects, `"seq":"20"`) || !strings.Contains(logSubjects, `"seq":"10"`) {
		t.Fatalf("expected both revisions 10 and 20 in replica git log, got:\n%s", logSubjects)
	}
}

func TestRemoteLifecycle_MarkerCommitReusesParentBaseline(t *testing.T) {
	f := integration.NewRepoFixture(t)

	// 1. Put initial revision on main
	f.WriteFile("data/large.bin", "heavy-dataset-payload-v1")
	var stdout, stderr bytes.Buffer
	if err := f.RunDrev(&stdout, &stderr, "put", "--sequence", "10"); err != nil {
		t.Fatalf("put on main failed: %v\nstderr: %s", err, stderr.String())
	}

	mainLog := f.MustGit(f.WorkDir, "log", "-1", "--format=%s")
	mainMeta, err := domain.ParseRevisionMeta(mainLog)
	if err != nil {
		t.Fatalf("failed to parse main revision meta: %v", err)
	}
	if mainMeta.UUID == "" || mainMeta.UUID == domain.ZeroUUID {
		t.Fatalf("expected valid UUID on main, got %q", mainMeta.UUID)
	}

	// 2. Create branch feature/from-main branching from main
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "create", "feature/from-main"); err != nil {
		t.Fatalf("create feature/from-main failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify HEAD is a marker commit carrying mainMeta.UUID
	markerLog := f.MustGit(f.WorkDir, "log", "-1", "--format=%s")
	markerMeta, err := domain.ParseRevisionMeta(markerLog)
	if err != nil {
		t.Fatalf("failed to parse marker meta: %v", err)
	}
	if !markerMeta.IsMarker() {
		t.Fatalf("expected marker commit, got %q", markerLog)
	}
	if markerMeta.UUID != mainMeta.UUID {
		t.Fatalf("expected marker to hold parent UUID %q, got %q", mainMeta.UUID, markerMeta.UUID)
	}

	// 3. Verify get on new branch fails because no revision has been published yet
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "get"); err == nil {
		t.Fatalf("expected get on branch with marker commit to fail, but succeeded")
	}

	// 4. Put on feature/from-main with an extra file
	f.WriteFile("data/extra.txt", "small delta")
	stdout.Reset()
	stderr.Reset()
	if err := f.RunDrev(&stdout, &stderr, "put", "--sequence", "11"); err != nil {
		t.Fatalf("put on feature/from-main failed: %v\nstderr: %s", err, stderr.String())
	}

	// Verify baseline clone was NOT skipped
	if strings.Contains(stderr.String(), "Unable to use previous revision") {
		t.Fatalf("expected put to reuse parent revision as baseline, but got fallback:\n%s", stderr.String())
	}

	// Verify new revision commit
	branchLog := f.MustGit(f.WorkDir, "log", "-1", "--format=%s")
	branchMeta, err := domain.ParseRevisionMeta(branchLog)
	if err != nil {
		t.Fatalf("failed to parse branch revision meta: %v", err)
	}
	if branchMeta.UUID == mainMeta.UUID {
		t.Fatalf("expected new UUID, got identical %q", branchMeta.UUID)
	}

	// Verify that data/large.bin on the remote was hardlinked from mainMeta.UUID
	srcFile := filepath.Join(f.StorageDir, mainMeta.UUID, "large.bin")
	dstFile := filepath.Join(f.StorageDir, branchMeta.UUID, "large.bin")
	fiSrc, err := os.Stat(srcFile)
	if err != nil {
		t.Fatalf("stat src file: %v", err)
	}
	fiDst, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("stat dst file: %v", err)
	}
	if !os.SameFile(fiSrc, fiDst) {
		t.Fatalf("expected large.bin to be hardlinked between %s and %s", srcFile, dstFile)
	}
}
