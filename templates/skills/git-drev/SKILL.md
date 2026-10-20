---
name: git-drev
description: >-
  Runbook for using git-drev for directory revision management, creating revision branches,
  publishing build snapshots via hardlink baseline cloning and rsync, switching revisions,
  downloading directory payloads, and inspecting storage usage. Activate when managing
  large directory artifacts, CI build snapshots, or git-drev workflows.
---

# `git-drev` Directory Revision Runbook

This skill guides the AI assistant in using `git-drev` (or `git drev`) as the unified CLI for directory revision lifecycle management.

> [!IMPORTANT]
> **Never manually craft `git-drev` JSON metadata commits** or run raw `rsync` against `<remote.path>/<uuid>` when `git-drev` is available. Always use the corresponding `git drev` subcommands so hardlink baseline cloning (`cp -al --`), hook execution, and `--force-with-lease` concurrency retries are handled safely.

---

## 1. Quick Reference & Command Matrix

| Stage | `git-drev` Command | Purpose & Behavior |
| :--- | :--- | :--- |
| **Branch Initialization** | `git drev create <branch>` | Checks out `<branch>` if it exists locally/remotely, or creates an initial marker commit (`{"uuid":"<parent-uuid>","marker":"true"}`) and pushes to `origin`. |
| **Publish Snapshot (Producer)** | `git drev put [-s <seq>] [--stats] [<src_dir>]` | Clones parent baseline on remote via `cp -al --`, uploads diffs via `rsync`, runs `.git-drev-put-hook`, commits metadata (`{"uuid":"...","seq":"..."}`), and pushes with retry. |
| **Select Revision (Consumer)** | `git drev select [<branch>\|<tag>\|<uuid>]` | Fetches `origin` and switches to a branch, tag, or specific commit matching an RFC 4122 UUID (defaults to `branch.default` if omitted). |
| **Download Payload (Consumer)** | `git drev get [-l\|--latest] [--stats] [<dst_dir>]` | Optionally updates branch (`-l`), then downloads `<remote.path>/<uuid>/` into `workdir` (or `<dst_dir>`) via `rsync`. |
| **Update Metadata Only** | `git drev update` | Fetches `origin` and hard-resets the active local branch to `origin/<branch>` without downloading the directory payload. |
| **Revision Info** | `git drev info` | Injects `GIT_DREV_*` environment variables and runs `<repo-root>/.git-drev-info-hook`. |
| **Active Branch Name** | `git drev name` | Prints the current attached local branch name to `stdout` (or fails with exit code `4` if `HEAD` is detached). |
| **Storage Footprint** | `git drev usage` | Calculates incremental (`Size`) and cumulative (`Cumulative`) disk usage per revision on the remote storage server via `du -sh -P`. |
| **Configuration Audit** | `git drev config [--sources]` | Prints the merged 3-tier YAML configuration (`User Global -> $GIT_DREV_CONFIG -> .git-drev.yaml`), optionally annotated with provenance comments. |

---

## 2. Standard Producer & Consumer Workflows

### Producer Workflow (CI/CD or Release Engineer)

1. **Initialize or switch to a target revision branch:**
   ```bash
   git drev create main
   ```
2. **Populate the working directory (`workdir`):**
   Build or copy the directory artifacts into the configured `workdir` (while keeping `workdir/` listed in `.gitignore`).
3. **Publish a new directory revision:**
   ```bash
   # Uses current Unix epoch seconds as sequence number by default
   git drev put

   # Or specify an explicit monotonic CI build number and print transfer stats
   git drev put -s "${BUILD_NUMBER}" --stats
   ```
   - If a concurrent CI job pushes while `rsync` is running, `put` automatically fetches `origin/<branch>`, compares sequence numbers (`seq`), and either drops the older build (`remoteSeq > localSeq`) or rebases (`--strategy-option=theirs`) up to 5 attempts.

### Consumer Workflow (Developer or Test Environment)

1. **Switch to the desired branch, tag, or historical UUID:**
   ```bash
   # Switch to default branch (e.g., main)
   git drev select

   # Switch to a release branch or tag
   git drev select release/2.0

   # Pin to a specific historical revision UUID (detaches HEAD)
   git drev select 4a123f89-8d7b-4b10-8f6a-49321ef19b12
   ```
2. **Download the directory snapshot:**
   ```bash
   # Download revision corresponding to current HEAD into workdir
   git drev get

   # Or fetch latest remote commits on the active branch and download in one step
   git drev get --latest
   ```

---

## 3. Hook Environment Contract

When `.git-drev-put-hook` or `.git-drev-info-hook` is present at `<repo-root>/`, `git-drev` executes it with a configurable timeout (`GIT_DREV_HOOK_TIMEOUT`, default `5m`) and injects:

- `GIT_DREV_VERSION`: CLI version string
- `GIT_DREV_UUID`: Target revision UUID
- `GIT_DREV_SEQ`: Revision sequence number (`uint64` string)
- `GIT_DREV_BRANCH`: Current branch name (or `HEAD` if detached during `info`)
- `GIT_DREV_WORK_DIR`: Absolute path to the effective payload directory
- `GIT_DREV_ROOT`: Absolute path to the Git repository root

---

## 4. Automation & Diagnostic Exit Codes

Check exit codes to diagnose failures programmatically:
- `0`: Success
- `1` (`ErrRuntime`): General runtime, filesystem, or hook execution failure
- `2` (`ErrUsage`): Invalid CLI arguments, unknown flags, or mutually exclusive `-v` and `-q`
- `3` (`ErrConfig` / `ErrNotRepository`): Outside a Git worktree, invalid `.git-drev.yaml`, or forbidden `rsync` flags (`--inplace`, `--append`, `--append-verify`)
- `4` (`ErrGitState` / `ErrDetachedHead`): Detached `HEAD` when a branch is required, active `.git/index.lock`, or no revision committed yet on branch
- `5` (`ErrGitExec`): Underlying `git` command failure or exhausted concurrent push retries
- `6` (`ErrRemoteNotFound`): Target branch, tag, UUID, or remote `<remote.path>/<uuid>` directory does not exist
- `7` (`ErrTransport`): `ssh` or `rsync` connection/transfer failure
- `130` (`ErrInterrupted`): Process interrupted by `SIGINT` or `SIGTERM` (after transactional cleanup)
