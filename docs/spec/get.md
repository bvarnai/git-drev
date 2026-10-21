# Spec: git-drev get

## 1. Command Signature
- **Usage:** `git-drev get [flags] [<target_directory>]`
- **Arguments:**
  - `[<target_directory>]` *(optional)*: Local destination directory to synchronize. Defaults to repository `workdir` (e.g. `data`).
- **Flags:**
  - `-l`, `--latest`: Fetch all remotes and hard-reset the active branch to `origin/<branch>` before synchronizing the payload.
  - `--stats`: Instruct `rsync` to print transfer statistics upon completion.
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0), `ssh`, `rsync` (>= 3.1.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `GIT_DREV_SSH_COMMAND`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`). Explicit `<target_directory>` is resolved relative to invocation `PWD`.
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`).
  - If `--latest` is set, `HEAD` must be attached to an active branch (detached HEAD aborts immediately with Exit Code `4`).
  - `HEAD` commit must carry a valid non-marker revision metadata UUID (`!meta.IsMarker()`).

## 3. Core Execution Flow
1. **Context & Argument Verification:**
   - Verify argument count is 0 or 1.
   - Resolve repository root and load configuration.
   - Resolve destination directory (`<target_directory>` or `<repo-root>/<workdir>`). Verify target directory does not equal repository root or `.git`.
2. **Fetch Refspec Assertion:**
   - Ensure `remote.origin.fetch` includes `+refs/heads/*:refs/remotes/origin/*`.
3. **Optional Branch Synchronization (`--latest`):**
   - If `--latest` is enabled:
     - Verify `HEAD` is attached (`git symbolic-ref --quiet --short HEAD`). If detached, fail with Exit Code `4`.
     - Fetch all remotes: `git fetch --all --prune`.
     - Hard-reset active branch to upstream: `git reset --hard refs/remotes/origin/<branch> --`.
4. **Metadata Extraction:**
   - Read `HEAD` commit subject (`git log -1 --format=%s HEAD`).
   - If commit is a branch marker (`"marker":"true"` or `domain.ZeroUUID`), fail with Exit Code `4` (`No directory revision available yet on current branch`).
   - Validate 36-char RFC 4122 UUID and normalize to lowercase.
   - Log `[git-drev] Directory revision uuid is '<uuid>'`.
5. **Remote Revision Probe:**
   - Probe remote directory existence via SSH (`ssh -T <host> sh -c 'test -d "$1"' -- <remote.path>/<uuid>`).
   - If missing on remote host, log `[git-drev] Error: Unable to find revision '<uuid>' on remote host` with hint and fail with Exit Code `6`.
6. **Local Target Directory Preparation:**
   - Inspect local destination path. If missing, create directory via `os.MkdirAll(path, 0755)`. (Probe before creation prevents orphaned empty directories).
7. **Rsync Transfer Execution:**
   - Synchronize remote payload to local target directory:
     ```bash
     rsync -a --delete-during [user-flags] -e "ssh -T" <host>:<remote.path>/<uuid>/ <target_directory>/
     ```
   - Extraneous local files in `<target_directory>` not present in the remote snapshot are deleted.
8. **Completion:**
   - Log elapsed time summary (`[git-drev] Took X min Y sec`) to `stderr` and exit cleanly with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Revision downloaded and synchronized successfully. | None (operational logs on `stderr`). |
| `1` | `errcode.ErrRuntime` | Destination path exists and is a file, or `mkdir` fails. | `[git-drev] Error: Target path '<path>' exists and is not a directory` |
| `2` | `errcode.ErrUsage` | Positional arguments > 1 or destination is repository root / `.git`. | `[git-drev] Error: Invalid target directory '<path>'...` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: Not inside a Git working tree` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: Configuration error: <details>` |
| `4` | `errcode.ErrDetachedHead`| `--latest` flag passed while `HEAD` is in detached state. | `[git-drev] Error: Detached HEAD state detected, aborting`<br>`[git-drev] Hint: Use "git-drev select <branch>" to select a branch first.` |
| `4` | `errcode.ErrGitState` | `HEAD` is a branch creation marker commit (no revisions published yet). | `[git-drev] Error: No directory revision available yet on current branch` |
| `4` | `errcode.ErrGitState` | `HEAD` commit lacks a valid RFC 4122 UUID. | `[git-drev] Error: Unable to read directory metadata (missing or invalid uuid)` |
| `5` | `errcode.ErrGitExec` | Git plumbing failure (`fetch` or `reset --hard` during `--latest`). | `[git-drev] Error: Fetch failed: <git stderr>` / `[git-drev] Error: Reset failed: <git stderr>` |
| `6` | `errcode.ErrRemoteNotFound`| Revision directory `<uuid>` not found on remote storage server. | `[git-drev] Error: Unable to find revision '<uuid>' on remote host`<br>`[git-drev] Hint: Revision might be gone already (that's normal depending on your retention policy)` |
| `7` | `errcode.ErrTransport` | SSH connection failure or `rsync` transfer termination. | `[git-drev] Error: Unable to get directory revision: <details>` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Pre-Creation Probe Safety:** Probing remote directory existence over SSH before invoking `MkdirAll` guarantees that expired or purged revisions do not pollute the local filesystem with empty directories.
- **Incremental Resumption:** In-flight `rsync` transfers interrupted by `SIGINT` leave completed files intact in `<target_directory>`. Re-running `git-drev get` uses `rsync` delta-transfer to resume download without re-transmitting unmodified files.
- **Trailing Slash Semantics:** Both remote source and local target paths include trailing slashes (`/`), ensuring directory contents are synchronized rather than nesting the UUID directory inside the target directory.
