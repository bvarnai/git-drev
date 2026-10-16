# Subcommand Technical Specification: `git-drev put`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev put [flags] [<source_directory>]`
  - Direct binary: `git-drev put [flags] [<source_directory>]`
- **Target Go package:** `internal/cli` (`internal/cli/put.go`)
- **Summary:** Uploads the contents of a local source directory (or the configured `workDir`) to a newly generated UUID directory on the remote storage host—cloning the current `HEAD` revision on the remote server as a copy-on-write/delta baseline when available—executes the optional `.drev-put-hook` script, commits the new revision metadata (`{"uuid":"...","seq":"..."}`) to the active branch, and publishes it to `origin` using an optimistic concurrency control loop with sequence-based conflict resolution.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | `--stats` | — | `bool` | `false` | No | Append `--stats` to the `rsync` invocation to print file-transfer statistics. |
  | `--sequence` | `-s` | `string` (parsed as `uint64`) | `""` (resolved at runtime to Unix epoch seconds `uint64(time.Now().Unix())`) | No | Explicit monotonically increasing sequence number (`uint64` decimal string) used to order concurrent uploads and discard outdated revisions. |
  | `--no-sequence-check` | — | `bool` | `false` | No | Disable sequence number comparison during stale-push resolution, forcing the local revision to be rebased onto `origin/<branch>` regardless of remote sequence age. |

  *(Note: Global flags `--verbose` / `-v`, `--quiet` / `-q`, `--dry-run` / `-n`, `--no-color`, `--version`, and `--help` / `-h` are inherited from `docs/spec/global.md` Section 1.)*

- **Positional arguments:**
  - `[<source_directory>]` (**Optional**, cardinality `0..1`):
    - Rejects invocations with more than 1 positional argument with `errcode.ErrUsage` (exit code `2`).
    - **When provided:** Resolved relative to `CallerDir` (`filepath.Join(RepoRoot, Prefix)`—the caller's working directory at invocation time, honoring `GIT_PREFIX`) before changing the process working directory to `RepoRoot`.
    - **When omitted:** Defaults to `filepath.Join(RepoRoot, cfg.WorkDir)` (where `cfg.WorkDir` defaults to `"data"` if unset in `.drev.json` and `git config drev.workDir`).
    - **Validation rules:**
      1. The resolved path MUST exist on the local filesystem (`os.Stat`); if missing, fail immediately with `errcode.ErrUsage` (exit code `2`).
      2. The resolved path MUST be a directory (`info.IsDir() == true`, following symlinks at the top-level path via `os.Stat`); if it is a regular file, fail with `errcode.ErrUsage` (exit code `2`).
      3. Must not point to `.git` or a path inside `.git/`.

- **Preconditions & state assertions:**
  1. **Non-bare Git working tree:** Must be invoked inside a valid Git working tree (`git rev-parse --is-inside-work-tree == true`). Bare repositories or invocations inside `.git/` fail with `errcode.ErrNotRepository` (exit code `3`).
  2. **Configuration validity:** `<repo-root>/.drev.json` (or `$GIT_DREV_CONFIG`) must exist, parse as valid UTF-8 JSON, and yield validated `remote.host`, `remote.path`, and `workDir` values after applying `git config drev.*` overrides (`errcode.ErrConfig`, exit code `3`).
  3. **Attached `HEAD` on a named branch:** `HEAD` MUST be a symbolic reference (`git symbolic-ref --quiet --short HEAD`). If `HEAD` is detached, abort before any remote or local mutation with `errcode.ErrDetachedHead` (exit code `4`) and emit `[drev] Hint: Use "git drev select <branch>" to select a branch first.`
  4. **Git index unlocked:** Verify `<git-dir>/index.lock` does not exist before starting the upload or staging changes; if present, fail with `errcode.ErrGitState` (exit code `4`).
  5. **Remote fetch refspec configuration:** Ensure `remote.origin.fetch` includes `+refs/heads/*:refs/remotes/origin/*` via `GitRunner.EnsureRemoteFetchConfig` (non-destructively adding the refspec if missing).

## 3. Subcommand Domain Models & Signatures
```go
package cli

import (
	"context"
	"time"

	"git-drev/internal/config"
	"git-drev/internal/domain"
	"git-drev/internal/git"
	"git-drev/internal/hook"
	"git-drev/internal/logui"
	"git-drev/internal/transport"
)

// MaxPushRetries bounds the optimistic concurrency retry loop when concurrent
// producers push to the same branch.
const MaxPushRetries = 5

// PutOptions encapsulates validated CLI flags and resolved paths for `git-drev put`.
type PutOptions struct {
	// SourceDir is the canonical absolute path to the local directory to upload.
	SourceDir string
	// RawArgs holds the positional arguments passed after flags (forwarded to .drev-put-hook).
	RawArgs []string
	// Sequence is the validated uint64 sequence number (explicit or epoch default).
	Sequence uint64
	// SequenceCheck controls whether older sequence revisions are dropped on stale push.
	SequenceCheck bool
	// Stats enables rsync --stats output.
	Stats bool
	// Verbose enables verbose logging and rsync -v --itemize-changes.
	Verbose bool
	// Quiet suppresses informational progress logs and sets rsync --quiet.
	Quiet bool
	// DryRun simulates remote and Git mutations without persisting changes.
	DryRun bool
}

// PutResult captures the outcome of a `put` execution for testing and telemetry.
type PutResult struct {
	// UUID is the newly generated RFC 4122 v4 UUID for this revision.
	UUID string
	// Sequence is the decimal string representation of the revision sequence.
	Sequence string
	// Branch is the active local branch name to which the revision was published.
	Branch string
	// BaselineUUID is the previous revision UUID cloned on the remote host, or "" if a full copy occurred.
	BaselineUUID string
	// DroppedOlder reports true if a concurrent remote revision had a strictly greater sequence
	// and the local commit was discarded via hard reset to origin/<branch>.
	DroppedOlder bool
	// PushAttempts records the number of `git push --force-with-lease` invocations performed.
	PushAttempts int
	// Elapsed is the total wall-clock execution duration.
	Elapsed time.Duration
}

// PutEnv groups external dependencies injected into RunPut for pure unit testing.
type PutEnv struct {
	Config  config.Config
	Git     git.Runner
	Remote  transport.RemoteRunner
	Hook    hook.Runner
	Logger  *logui.Logger
	UUIDGen func() (string, error)
	Now     func() time.Time
}

// RunPut executes the domain logic of `git-drev put`.
func RunPut(ctx context.Context, env PutEnv, opts PutOptions) (*PutResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)

### 1. Input Normalization & Validation
1. **Flag validation:**
   - Reject mutually exclusive global flags `--verbose` and `--quiet` with `errcode.ErrUsage` (exit code `2`).
   - If `--sequence` (`-s`) is provided, parse it via `strconv.ParseUint(rawSeq, 10, 64)`. If empty or non-numeric (e.g., negative, floating-point, overflow, or non-ASCII digits), fail immediately with `errcode.ErrUsage` (exit code `2`).
   - If `--sequence` is omitted, set `opts.Sequence = uint64(env.Now().Unix())`.
   - Set `opts.SequenceCheck = !noSequenceCheckFlag`.
2. **Repository & path canonicalization:**
   - Query `RepoRoot` and `Prefix` via `env.Git.RepoRoot(ctx)`.
   - Compute `CallerDir = filepath.Join(RepoRoot, Prefix)`.
   - Load and validate `cfg` from `<RepoRoot>/.drev.json` (or `$GIT_DREV_CONFIG`) merged with `git config drev.*` overrides.
   - Resolve `opts.SourceDir`:
     - If positional argument `<source_directory>` is present: if `filepath.IsAbs(arg)`, use `filepath.Clean(arg)`; otherwise `filepath.Join(CallerDir, arg)`.
     - If omitted: `filepath.Join(RepoRoot, cfg.WorkDir)`.
   - Verify `opts.SourceDir` exists and is a directory via `os.Stat(opts.SourceDir)`.
   - Change the process working directory to `RepoRoot` (`os.Chdir(RepoRoot)`) so all subsequent Git operations and hooks execute deterministically from the worktree root.
3. **Precondition assertions:**
   - Call `env.Git.CurrentBranch(ctx)` (`git symbolic-ref --quiet --short HEAD`). If `detached == true`, return `errcode.ErrDetachedHead` (exit code `4`).
   - Call `env.Git.EnsureRemoteFetchConfig(ctx)` to ensure `+refs/heads/*:refs/remotes/origin/*` is configured.

### 2. Core Algorithm
1. **Generate revision identity:**
   - Generate a lowercase RFC 4122 UUID v4 via `env.UUIDGen()` (backed by `crypto/rand.Read`).
   - Log `[drev] Directory revision uuid is '<uuid>'` to `stderr`.
   - Format commit metadata payload using `encoding/json`: `{"uuid":"<uuid>","seq":"<sequence>"}` (`domain.RevisionMeta{UUID: uuid, Seq: strconv.FormatUint(opts.Sequence, 10)}`).
2. **Remote baseline cloning (server-side delta optimization):**
   - Log `[drev] Trying to use previous revision as baseline (this might take a while)` to `stderr`.
   - Query the current `HEAD` commit subject via `env.Git.HeadCommitMeta(ctx)` (`git log -n 1 --format=%s HEAD`).
   - If `HEAD` exists, its subject parses as valid `domain.RevisionMeta`, `meta.UUID` matches RFC 4122 hex format, and `meta.UUID != domain.ZeroUUID`:
     - Register a deferred rollback flag `remoteStagingCreated = true` so that if `put` fails or is interrupted before the commit is pushed, a best-effort remote cleanup can remove `<remote.path>/<uuid>` if needed (or leave cleanup on interruption).
     - Invoke `env.Remote.CloneBaseline(ctx, cfg.Remote.Host, srcPath, dstPath)` where `srcPath = path.Join(cfg.Remote.Path, meta.UUID)` and `dstPath = path.Join(cfg.Remote.Path, uuid)`.
     - If `CloneBaseline` succeeds, set `result.BaselineUUID = meta.UUID` and log `[drev] Baseline revision uuid is '<last_uuid>'`.
     - If `CloneBaseline` returns an error (e.g., previous revision was purged on the server or `HEAD` was an initial commit without a remote directory), treat it as a non-fatal fallback and log `[drev] Unable to use previous revision, doing a full copy`.
   - Otherwise (unparseable `HEAD`, empty repository, or `meta.UUID == domain.ZeroUUID`), skip SSH `cp` and log `[drev] Unable to use previous revision, doing a full copy`.
3. **Upload directory payload via `rsync`:**
   - Log `[drev] Putting directory revision to remote host (this might take a while)` to `stderr`.
   - Construct `transport.SyncRequest`:
     - `Host`: `cfg.Remote.Host`
     - `RemotePath`: `path.Join(cfg.Remote.Path, uuid) + "/"`
     - `LocalPath`: ` strings.TrimRight(opts.SourceDir, string(filepath.Separator)) + "/"` (trailing slash mandatory so `rsync` transfers the *contents* of `SourceDir` into `<remote.path>/<uuid>/`)
     - `Verbose`: `opts.Verbose`
     - `Quiet`: `opts.Quiet`
     - `Stats`: `opts.Stats`
     - `DryRun`: `opts.DryRun`
     - `UserOptions`: `cfg.Remote.RsyncOptions.Put`
   - Invoke `env.Remote.RsyncPut(ctx, syncReq)`. If `RsyncPut` fails, return wrapped `errcode.ErrTransport` (exit code `7`).
4. **Execute `.drev-put-hook` (if present):**
   - Check if `<RepoRoot>/.drev-put-hook` exists and is a regular file (`os.Stat`).
   - If present, execute via `env.Hook.RunPutHook(ctx, hookReq)` with `GIT_DREV_UUID=<uuid>`, `GIT_DREV_SEQ=<seq>`, `GIT_DREV_BRANCH=<branch>`, `GIT_DREV_WORK_DIR=<abs-workDir>`, `GIT_DREV_ROOT=<RepoRoot>`, and positional arguments `opts.RawArgs`, enforcing `GIT_DREV_HOOK_TIMEOUT` (default `5m`).
   - If the hook exits non-zero or times out, return wrapped `errcode.ErrRuntime` (exit code `1`).
5. **Stage and commit revision metadata:**
   - Log `[drev] Staging revision metadata` to `stderr`.
   - Invoke `env.Git.StageAll(ctx)` (`git add --all`). On failure, return `errcode.ErrGitExec` (exit code `5`).
   - Log `[drev] Committing revision metadata` to `stderr`.
   - Invoke `env.Git.CommitAllowEmpty(ctx, commitJSON)` (`git commit --allow-empty -m <json>`). On failure, return `errcode.ErrGitExec` (exit code `5`).
6. **Publish metadata commit with optimistic concurrency control:**
   - Log `[drev] Pushing revision metadata` to `stderr`.
   - Execute a bounded retry loop (`attempt = 1; attempt <= MaxPushRetries; attempt++`):
     1. Invoke `stale, pushOutput, err := env.Git.PushForceWithLease(ctx, branch, false)` (`git push --force-with-lease origin refs/heads/<branch>`).
     2. **If `err == nil`:**
        - Mark upload as committed/published (disarming any interrupted staging rollback).
        - Log `[drev] Took <M> min <S> sec` to `stderr` and return `PutResult`.
     3. **If `err != nil` and `!stale`:**
        - Return wrapped `errcode.ErrGitExec` (exit code `5`).
     4. **If `stale == true`:**
        - If `attempt == MaxPushRetries`, fail with `errcode.ErrGitExec` (exit code `5`): `exceeded maximum push retries (5) due to concurrent updates on origin/<branch>`.
        - Log `[drev] Stale info detected, trying to resolve` to `stderr`.
        - Invoke `env.Git.FetchAll(ctx)` (`git fetch --all --prune`). On failure, return `errcode.ErrGitExec` (exit code `5`).
        - **Sequence comparison (when `opts.SequenceCheck == true`):**
          - Read remote tip metadata via `remoteMeta, metaErr := env.Git.BranchCommitMeta(ctx, "refs/remotes/origin/"+branch)`.
          - If `metaErr == nil` and `remoteMeta.Seq != ""`:
            - Parse `remoteSeq, parseErr := strconv.ParseUint(remoteMeta.Seq, 10, 64)`.
            - If `parseErr == nil` and `opts.Sequence < remoteSeq`:
              - Log `[drev] Newer revision found, dropping older (hard reset)` to `stderr`.
              - Log `[drev] Hint: This is usually ok, you want to see the latest revision` to `stderr`.
              - Invoke `env.Git.ResetHard(ctx, "refs/remotes/origin/"+branch)` (`git reset --hard refs/remotes/origin/<branch>`). On failure, return `errcode.ErrGitExec` (exit code `5`).
              - Set `result.DroppedOlder = true`, log `[drev] Took <M> min <S> sec` to `stderr`, and return `nil` (exit code `0`).
        - **Rebase onto updated upstream:**
          - Invoke `env.Git.RebaseTheirs(ctx, "refs/remotes/origin/"+branch)` (`git rebase --strategy-option=theirs refs/remotes/origin/<branch>`).
          - If `RebaseTheirs` fails, execute `git rebase --abort` to restore worktree consistency and return `errcode.ErrGitExec` (exit code `5`).
        - Apply a brief backoff (`50ms * 2^(attempt-1)` with jitter) and continue to the next loop iteration to retry `PushForceWithLease`.

### 3. Dry-Run Behavior (`--dry-run` / `-n`)
When `opts.DryRun == true`:
1. All read-only validations (repository check, `.drev.json` parsing, `HEAD` symbolic-ref check, `<source_directory>` existence check, UUID generation, and reading `HEAD` commit metadata for baseline discovery) execute normally.
2. **Baseline clone:** Do NOT execute `ssh ... cp -R -a`. Instead, if `HEAD` has a valid non-zero UUID, log `[drev] [DRY RUN] Would clone baseline revision <last_uuid> to <uuid>` to `stderr`.
3. **Rsync upload:** Because the remote `<remote.path>/<uuid>` directory does not exist during a dry-run (as baseline clone was skipped and `rsync --dry-run` fails if the remote destination parent/target cannot be created over SSH in certain configurations), log `[drev] [DRY RUN] Would execute: rsync <flags> -e '<ssh>' <source_dir>/ <host>:<path>/<uuid>/` to `stderr` without mutating the remote host.
4. **Hook execution:** If `.drev-put-hook` exists, skip execution and log `[drev] [DRY RUN] Would execute hook: .drev-put-hook` to `stderr`.
5. **Git staging, commit, and push:** Skip `git add --all`, `git commit`, and `git push`, logging:
   - `[drev] [DRY RUN] Would execute: git add --all`
   - `[drev] [DRY RUN] Would execute: git commit --allow-empty -m {"uuid":"<uuid>","seq":"<seq>"}`
   - `[drev] [DRY RUN] Would execute: git push --force-with-lease origin refs/heads/<branch>`
6. Exit with code `0`.

### 4. Interruption, Cleanup & Rollback
- **Signal handling (`SIGINT` / `SIGTERM`) or mid-upload failure:**
  - When `ctx.Done()` fires during step 2 (`CloneBaseline`) or step 3 (`RsyncPut`), the active `ssh` or `rsync` subprocess is terminated (`SIGTERM` followed by `SIGKILL` after 3s).
  - A deferred cleanup handler runs with a detached `context.WithTimeout(context.Background(), 10*time.Second)` to execute `env.Remote.RemoveDir(cleanupCtx, cfg.Remote.Host, path.Join(cfg.Remote.Path, uuid))` (`ssh -T <host> rm -rf -- '<path>/<uuid>'`) if any remote files were staged before the commit was created, preventing orphaned half-uploaded directories on the server.
- **No premature index pollution:**
  - Unlike legacy `put.sh` (which ran `git add --all` before `rsync`), `git-drev put` does not touch the Git index until after `rsync` and `.drev-put-hook` succeed. If `rsync` or the hook fails, the local Git index and commit history remain untouched.
- **Rebase abort guarantee:**
  - If `git rebase --strategy-option=theirs` fails or is interrupted during stale-push recovery, `git rebase --abort` is invoked via a cleanup `defer` before returning the error so the repository is never left mid-rebase.

## 5. Side Effects, Git Plumbing & I/O Contract

### Git Plumbing Invocations
| Command & Flags | Working Dir | Expected Output Format | Failure Handling |
| :--- | :--- | :--- | :--- |
| `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix --git-dir` | Caller `PWD` | Newline-delimited boolean and paths | Exit code `3` (`ErrNotRepository`) |
| `git symbolic-ref --quiet --short HEAD` | `RepoRoot` | `<branch>\n` | Exit code `1` from `git` maps to `ErrDetachedHead` (exit code `4`) |
| `git config --local --get-all remote.origin.fetch` | `RepoRoot` | Newline-delimited refspecs | If `+refs/heads/*:refs/remotes/origin/*` missing, run `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` |
| `git log -n 1 --format=%s HEAD --` | `RepoRoot` | Single-line JSON string `{"uuid":"..."}` | Non-zero exit (unborn branch) or non-JSON subject treated as "no baseline available" |
| `git add --all` | `RepoRoot` | Empty | Exit code `5` (`ErrGitExec`) |
| `git commit --allow-empty -m <json>` | `RepoRoot` | Standard commit summary (captured) | Exit code `5` (`ErrGitExec`) |
| `git push --force-with-lease origin refs/heads/<branch>` | `RepoRoot` | Standard push progress (`LC_ALL=C`) | If output contains `[rejected]` and `(stale info)`, trigger stale-push recovery; otherwise exit code `5` (`ErrGitExec`) |
| `git fetch --all --prune` | `RepoRoot` | Standard fetch output | Exit code `5` (`ErrGitExec`) |
| `git log -n 1 --format=%s refs/remotes/origin/<branch> --` | `RepoRoot` | Single-line JSON string `{"uuid":"...","seq":"..."}` | Non-JSON or missing `seq` skips sequence drop and proceeds to rebase |
| `git reset --hard refs/remotes/origin/<branch> --` | `RepoRoot` | `HEAD is now at ...` | Exit code `5` (`ErrGitExec`) |
| `git rebase --strategy-option=theirs refs/remotes/origin/<branch>` | `RepoRoot` | Standard rebase output | Run `git rebase --abort` and return exit code `5` (`ErrGitExec`) |

### Filesystem & Metadata Mutations
- **Remote host (`<remote.host>`):**
  - Creates directory `<remote.path>/<uuid>/` (mode `0755`, or cloned from `<remote.path>/<last_uuid>/` via `cp -R -a --`).
  - Synchronizes `<source_directory>/` contents into `<remote.path>/<uuid>/` via `rsync -a --delete-during`.
- **Local repository (`<RepoRoot>`):**
  - `.drev-put-hook` MAY create or modify tracked/untracked metadata files in `<RepoRoot>` (e.g., `env.json`, build manifests).
  - Stages all modified/untracked non-ignored files in `<RepoRoot>` via `git add --all` and records a new commit on `refs/heads/<branch>`.
  - If a concurrent push with a strictly newer sequence (`remoteSeq > localSeq`) is detected, `git reset --hard refs/remotes/origin/<branch>` resets the working tree and index to match `origin/<branch>`.

### Standard Output (`stdout`)
- By default, `git-drev put` emits **nothing** to `stdout` (all operational progress, `rsync` progress, and `git push` summaries are routed to `stderr` per `docs/spec/global.md` Section 1).

### Standard Error (`stderr`)
- Operational progress messages (suppressed when `--quiet` is set):
  ```text
  [drev] Directory revision uuid is '<uuid>'
  [drev] Trying to use previous revision as baseline (this might take a while)
  [drev] Baseline revision uuid is '<last_uuid>'
  [drev] Putting directory revision to remote host (this might take a while)
  [drev] Staging revision metadata
  [drev] Committing revision metadata
  [drev] Pushing revision metadata
  [drev] Took <M> min <S> sec
  ```
- When baseline cloning is unavailable or fails:
  ```text
  [drev] Unable to use previous revision, doing a full copy
  ```
- When stale-push recovery drops an older revision:
  ```text
  [drev] Stale info detected, trying to resolve
  [drev] Newer revision found, dropping older (hard reset)
  [drev] Hint: This is usually ok, you want to see the latest revision
  [drev] Took <M> min <S> sec
  ```

## 6. Error Matrix & Exit Code Mapping
| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown flag, extra positional args (`> 1`), or `--verbose` + `--quiet` | `errcode.ErrUsage` | `2` | `[drev] ! Unsupported or invalid option: <detail>` |
| Invalid `--sequence` value (non-integer, negative, or `uint64` overflow) | `errcode.ErrUsage` | `2` | `[drev] ! Invalid sequence number '<val>': must be an unsigned 64-bit integer` |
| Specified or default `<source_directory>` does not exist or is not a directory | `errcode.ErrUsage` | `2` | `[drev] ! Specified source directory '<path>' does not exist` |
| Invoked outside a Git worktree or inside a bare repository | `errcode.ErrNotRepository` | `3` | `[drev] ! Not inside a Git working tree` |
| Missing or malformed `.drev.json`, or missing `remote.host` / `remote.path` | `errcode.ErrConfig` | `3` | `[drev] ! Configuration error in '<path>': <detail>` |
| Repository is in detached `HEAD` state | `errcode.ErrDetachedHead` | `4` | `[drev] ! Detached HEAD state detected, aborting\n[drev] Hint: Use "git drev select <branch>" to select a branch first.` |
| `.git/index.lock` exists | `errcode.ErrGitState` | `4` | `[drev] ! Git index is locked ('.git/index.lock' exists)` |
| `.drev-put-hook` exits with non-zero code or exceeds `GIT_DREV_HOOK_TIMEOUT` | `errcode.ErrRuntime` | `1` | `[drev] ! Put hook '.drev-put-hook' failed: <detail>` |
| Failed to generate cryptographic UUID | `errcode.ErrRuntime` | `1` | `[drev] ! Failed to generate UUID: <detail>` |
| `rsync` upload fails (SSH connection failure, disk full, permission denied) | `errcode.ErrTransport` | `7` | `[drev] ! Unable to put directory revision: <detail>` |
| `git add --all`, `git commit`, `git fetch`, `git reset`, or `git rebase` fails | `errcode.ErrGitExec` | `5` | `[drev] ! <Operation> failed: <git stderr>` |
| `git push --force-with-lease` fails with non-stale error or exceeds `MaxPushRetries` | `errcode.ErrGitExec` | `5` | `[drev] ! Push failed: <detail>` |
| Process receives `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[drev] ! Operation interrupted` |

## 7. Legacy Modernization & Edge Cases

### Fixed Legacy Quirks
1. **Broken `--no-sequence-check` Double `shift` (`legacy/put.sh:56-60`):**
   - *Legacy bug:* The `--no-sequence-check` case executed `shift` before AND after setting `sequence_check="false"`, silently swallowing the subsequent CLI argument (such as `-s <seq>` or `<source_directory>`).
   - *Modernized resolution:* Parsed via a typed boolean flag; consumes zero positional arguments.
2. **Broken Single-Iteration Stale Push Loop (`legacy/put.sh:207-252`):**
   - *Legacy bug:* At the end of `while [[ -n "${stale}" ]]`, line 251 unconditionally executed `stale=`, overwriting `stale="true"` set on line 243 when the second `git push` also hit a stale-info rejection. Thus, a second concurrent push collision silently exited `0` without pushing the local commit!
   - *Modernized resolution:* Deterministic `for attempt := 1; attempt <= MaxPushRetries; attempt++` loop that only exits `0` when `PushForceWithLease` succeeds or when `DroppedOlder` resets to a newer remote sequence.
3. **Premature `git add --all` Index Pollution (`legacy/put.sh:105` & `180`):**
   - *Legacy bug:* `put.sh` ran `git add --all` before `rsync` and again after `put_hook`. If `rsync` failed, unstaged files in the working tree were left staged in the Git index.
   - *Modernized resolution:* `git add --all` runs strictly once, after `rsync` and `.drev-put-hook` have both succeeded.
4. **Baseline Clone Attempt on `ZeroUUID` Marker Commits (`legacy/put.sh:126-128`):**
   - *Legacy bug:* After `git drev create`, the initial marker commit has `"uuid":"00000000-0000-0000-0000-000000000000"`, which matches `$DRS_UUID_REGEXP`. Legacy `put.sh` always wasted an SSH round-trip trying to `cp -R -a /path/00000000-...` on the remote server.
   - *Modernized resolution:* Explicitly check `meta.UUID != domain.ZeroUUID` before attempting `CloneBaseline`.
5. **Unvalidated `--sequence` & Bash Arithmetic Injection (`legacy/put.sh:220`):**
   - *Legacy bug:* `--sequence` was not validated until `(( sequence < last_sequence ))` inside the stale-push handler, where non-numeric input in `sequence` caused Bash arithmetic syntax errors or variable evaluation.
   - *Modernized resolution:* Validate `--sequence` upfront during flag parsing using `strconv.ParseUint(s, 10, 64)`.
6. **Typo in Error & Log Messages (`legacy/put.sh:96, 186`):**
   - *Legacy quirk:* Fixed `"does not exists"` -> `"does not exist"` and `"Commiting"` -> `"Committing"`.

### Boundary Conditions
- **Symlinks:**
  - If `<source_directory>` itself is a symlink to a directory, `os.Stat` follows it and appending `/` (`<source_directory>/`) causes `rsync -a` to traverse into the target directory and transfer its contents.
  - Symlinks *inside* `<source_directory>` are preserved as symlinks on the remote server (`-l` implied by `rsync -a`) unless overridden by user `remote.rsyncOptions.put` (e.g., `--copy-links`).
- **Empty `<source_directory>`:**
  - Valid and permitted; creates an empty `<remote.path>/<uuid>/` directory on the remote server (and deletes baseline files via `--delete-during` if cloned from a non-empty baseline).
- **Paths with spaces, quotes, or newlines:**
  - Passed as discrete elements in `[]string` to `rsync` and POSIX-quoted in SSH remote commands; never subjected to `eval set --`.
- **Subdirectory invocation (`GIT_PREFIX`):**
  - Running `git drev put ./build-output` from `<repo-root>/sub/dir` resolves `<source_directory>` to `<repo-root>/sub/dir/build-output`, while `.drev.json` and `.drev-put-hook` are resolved at `<repo-root>`.
- **Detached `HEAD` state:**
  - Rejected immediately at precondition check before touching the network or filesystem (exit code `4`).

## 8. Black-Box Verification Scenarios
| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid repo on branch `main`; `HEAD` commit is `{"uuid":"11111111-1111-4111-8111-111111111111","seq":"100"}`; remote has `/var/drev/proj/11111111-1111-4111-8111-111111111111`; local `data/app.bin` modified; `.drev-put-hook` writes `build.txt`. | `git drev put -s 200` | Exit `0`; `stdout` empty; `stderr` logs baseline `11111111-...` cloned to new `<uuid>`, `rsync` transfer, staging, commit, push, and elapsed time; remote `/var/drev/proj/<uuid>/app.bin` exists; `HEAD` and `origin/main` point to commit with `{"uuid":"<uuid>","seq":"200"}` containing `build.txt`. |
| `TC-02` | Newly created branch `feature` whose `HEAD` is marker commit `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`; caller is in subdirectory `sub/` with `./out/artifact.tar`. | `git drev put ./out` | Exit `0`; `CloneBaseline` SSH call is skipped (`Unable to use previous revision, doing a full copy` logged to `stderr`); `sub/out/artifact.tar` is uploaded to `<remote.path>/<uuid>/artifact.tar`; new commit pushed to `origin/feature`. |
| `TC-03` | Valid repo on branch `main`; `data/` exists with files. | `git drev put --dry-run -s 300` | Exit `0`; `stderr` contains `[drev] [DRY RUN] Would...` lines for baseline clone, `rsync`, `git add`, `git commit`, and `git push`; no remote directory created; local `HEAD` and `git status` unchanged. |
| `TC-04` | Local branch `main` at `seq: 100`; concurrent producer already pushed `seq: 500` to `origin/main`. | `git drev put --sequence 250` | Exit `0`; first `git push --force-with-lease` rejected as stale; `git fetch --all` runs; `250 < 500` detected; `stderr` logs `Newer revision found, dropping older (hard reset)`; local `main` hard-reset to `origin/main` (`seq: 500`). |
| `TC-05` | Local branch `main` at `seq: 100`; concurrent producer pushed `seq: 150` to `origin/main`. | `git drev put --no-sequence-check --sequence 120 data` | Exit `0`; `--no-sequence-check` does not swallow `--sequence 120` or `data`; after stale push rejection, sequence check is bypassed, local commit (`seq: 120`) is rebased with `--strategy-option=theirs` onto `origin/main` and pushed on retry. |
| `TC-06` | Repository is in detached `HEAD` state (e.g., after `git drev select <uuid>`); `data/` exists. | `git drev put` | Exit `4` (`ErrDetachedHead`); `stderr` outputs `[drev] ! Detached HEAD state detected, aborting` and `[drev] Hint: Use "git drev select <branch>" to select a branch first.`; no SSH, `rsync`, or Git mutations occur. |
| `TC-07` | Valid repo on branch `main`; `rsync` fails mid-transfer (or receives `SIGINT`). | `git drev put` | Exit `7` (`ErrTransport`) on `rsync` error or `130` (`ErrInterrupted`) on `SIGINT`; deferred remote cleanup removes incomplete `<remote.path>/<uuid>`; local Git index and `HEAD` remain completely unmodified. |
