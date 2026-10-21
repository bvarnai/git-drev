# Spec: git-drev put

## 1. Command Signature
- **Usage:** `git-drev put [flags] [<source_directory>]`
- **Arguments:**
  - `[<source_directory>]` *(optional)*: Local directory to upload. Defaults to repository `workdir` (e.g. `data`).
- **Flags:**
  - `-s`, `--sequence <uint64>`: Explicit sequence number for concurrent ordering. Defaults to Unix timestamp seconds (`time.Now().Unix()`).
  - `--no-sequence-check`: Disable sequence comparison during stale-push resolution, forcing a rebase onto `origin/<branch>`.
  - `--stats`: Instruct `rsync` to print transfer statistics.
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0), `ssh`, `rsync` (>= 3.1.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `GIT_DREV_SSH_COMMAND`, `GIT_DREV_HOOK_TIMEOUT` (default `5m`), `NO_COLOR`, `GIT_DREV_COLOR`.
- **Environment variables injected into hook:** `GIT_DREV_VERSION`, `GIT_DREV_UUID`, `GIT_DREV_SEQ`, `GIT_DREV_BRANCH`, `GIT_DREV_WORK_DIR`, `GIT_DREV_ROOT`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`). Explicit `<source_directory>` is resolved relative to invocation `PWD`.
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`).
  - `HEAD` must be attached to an active branch (`git symbolic-ref --quiet --short HEAD`). Detached HEAD fails immediately with Exit Code `4`.
  - Git index must not be locked (`.git/index.lock` must not exist).

## 3. Core Execution Flow
1. **Context & Argument Verification:**
   - Verify argument count is 0 or 1.
   - Parse `--sequence` if provided as `uint64`. Defaults to current epoch seconds.
   - Resolve source directory and assert it exists on local filesystem (`os.Stat(sourceDir).IsDir()`).
   - Assert `HEAD` is attached to a branch.
2. **UUID Generation:**
   - Generate cryptographically random RFC 4122 v4 UUID (`crypto/rand`).
   - Log `[git-drev] Directory revision uuid is '<uuid>'`.
3. **Remote Baseline Cloning (Copy-on-Write Optimization):**
   - Inspect parent revision UUID on `HEAD`.
   - If a valid non-zero parent UUID exists:
     - Execute remote hardlink clone via SSH: `cp -al -- <remote.path>/<parent_uuid> <remote.path>/<uuid>`.
     - If baseline clone succeeds, log `[git-drev] Baseline revision uuid is '<parent_uuid>'`.
     - If baseline missing or purged: fallback to full copy and log `[git-drev] Unable to use previous revision, doing a full copy`.
   - If unborn branch or marker commit: skip baseline clone and log `[git-drev] Unable to use previous revision, doing a full copy`.
4. **Rsync Delta Upload:**
   - Log `[git-drev] Putting directory revision to remote host (this might take a while)`.
   - Run `rsync -a --delete-during [flags] -e "ssh -T" <source_directory>/ <host>:<remote.path>/<uuid>/`.
   - Only new or modified files are transmitted over the wire.
5. **Hook Execution (`.git-drev-put-hook`):**
   - If `.git-drev-put-hook` exists in repository root, execute with injected `GIT_DREV_*` environment variables and positional args.
   - If hook fails or times out, fail with Exit Code `1`.
6. **Commit Revision Metadata:**
   - Log `[git-drev] Staging revision metadata` and stage changed files: `git add --all`.
   - Log `[git-drev] Committing revision metadata`.
   - Record metadata commit: `git commit --allow-empty -m '{"uuid":"<uuid>","seq":"<sequence>"}'`.
7. **Publish with Optimistic Concurrency Control (OCC):**
   - Push commit to remote: `git push --force-with-lease origin refs/heads/<branch>`.
   - If push succeeds: proceed to completion.
   - If push rejected due to stale reference (concurrent push):
     - Log `[git-drev] Stale info detected, trying to resolve`.
     - Fetch remote updates: `git fetch --all --prune`.
     - **Sequence Check:** If remote commit has strictly higher sequence (`remoteSeq > localSeq`) and `--no-sequence-check` is not set:
       - Log `[git-drev] Newer revision found, dropping older (hard reset)`.
       - Log `[git-drev] Hint: This is usually ok, you want to see the latest revision`.
       - Hard reset branch to upstream (`git reset --hard refs/remotes/origin/<branch> --`) and exit cleanly with Exit Code `0`.
     - **Rebase:** Otherwise, rebase local commit onto upstream: `git rebase --strategy-option=theirs refs/remotes/origin/<branch>`.
     - Retry `PushForceWithLease` (up to 5 attempts).
8. **Completion:**
   - Log elapsed time summary (`[git-drev] Took X min Y sec`) to `stderr` and exit cleanly with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Revision uploaded and published, or older revision dropped gracefully. | None (operational logs on `stderr`). |
| `1` | `errcode.ErrRuntime` | `.git-drev-put-hook` failed or timed out. | `[git-drev] Error: Put hook '.git-drev-put-hook' failed: <details>` |
| `2` | `errcode.ErrUsage` | Invalid flag, extra arguments, non-existent source directory, or invalid `--sequence`. | `[git-drev] Error: <usage error>` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: Not inside a Git working tree` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: Configuration error in '<path>': <detail>` |
| `4` | `errcode.ErrDetachedHead`| Repository is in detached HEAD state. | `[git-drev] Error: Detached HEAD state detected, aborting`<br>`[git-drev] Hint: Use "git-drev select <branch>" to select a branch first.` |
| `4` | `errcode.ErrGitState` | Git index is locked (`.git/index.lock` exists). | `[git-drev] Error: Git index is locked ('.git/index.lock' exists)` |
| `5` | `errcode.ErrGitExec` | Git staging, commit, fetch, reset, or push exceeded max retries. | `[git-drev] Error: <operation> failed: <git stderr>` |
| `7` | `errcode.ErrTransport` | SSH connection failure or `rsync` upload failure. | `[git-drev] Error: Unable to put directory revision: <details>` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Server-Side Baseline Hardlinking:** By cloning the parent UUID directory via `cp -al` on the remote server prior to running `rsync`, unchanged files across builds consume zero network transmission bandwidth and zero additional disk blocks.
- **Atomic Cleanup on Interruption:** If `put` fails or is interrupted during `rsync`, a deferred cleanup handler removes the in-flight remote directory (`<remote.path>/<uuid>`) via SSH to avoid leaving uncommitted orphan snapshots on the server.
- **Stale Push Resolution:** In CI/multi-builder pipelines where two builds complete simultaneously, sequence-based OCC avoids build races: the older build gracefully yields and adopts the newer build.
