# Spec: git-drev usage

## 1. Command Signature
- **Usage:** `git-drev usage [flags]`
- **Arguments:** None. (Positional arguments are rejected).
- **Flags:**
  - `-a`, `--all`: Display itemized breakdown table of all remote revisions and orphan correlation status.
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0), `ssh`.
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`).
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`) with valid `remote.host` and `remote.path`.
  - Read-only inspection; permitted from attached branch or detached HEAD.

## 3. Core Execution Flow
1. **Context & Argument Verification:**
   - Verify 0 positional arguments are passed.
   - Resolve repository root and load configuration. Validate `remote.host` and `remote.path`.
2. **Fetch Refspec Assertion:**
   - Ensure `remote.origin.fetch` includes `+refs/heads/*:refs/remotes/origin/*`.
3. **Remote SSH Storage Query:**
   - Establish non-interactive SSH connection (`ssh -T <host> /bin/sh -s -- <path> <mode>`).
   - Remote script validates directory existence:
     - If remote path does not exist, exits with code `66` (mapped to `errcode.ErrRemoteNotFound`, Exit Code `6`).
     - Computes total directory usage via `du -sh -- "$1"`.
     - Scans immediate child entries matching the 36-character RFC 4122 UUID regex.
     - In fast mode (`!opts.All`): emits `REV:<uuid>:-:0`.
     - In detailed mode (`opts.All`): emits `COUNT:<total>`, followed by per-directory size `du -sh -- "$d"` and modification time (`stat -c %Y` or `stat -f %m`).
4. **Summary Rendering:**
   - Output storage summary block to `stdout`:
     ```text
     Summary:
       Remote storage:    <host>:<path>
       Total space used:  <total_size>
       Revisions found:   <N>
     ```
   - If revisions count is 0: log `[git-drev] No revision directories found on remote storage.` to `stderr` and exit `0`.
   - If not `--all`: emit hint `[git-drev] Hint: Use -a/--all to view the detailed breakdown of revisions.` and exit `0`.
5. **Git Commit Correlation & Breakdown (`--all`):**
   - Stream local Git commits across all refs (`git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix`).
   - Match discovered remote UUID directories against commit subjects:
     - If matched: display commit short SHA, cleaned branch/tag refs, and commit sequence message.
     - If unmatched: display commit as `[orphaned]`, branch as `-`, message as `-`.
   - Render aligned breakdown table to `stdout`.
6. **Completion:**
   - Exit cleanly with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Usage summary and optional breakdown table printed to `stdout`. | None. |
| `2` | `errcode.ErrUsage` | Positional arguments provided (`git-drev usage extra`). | `[git-drev] Error: usage takes no arguments` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: not a git repository (or any of the parent directories)` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: No configuration file found` / `[git-drev] Error: <details>` |
| `6` | `errcode.ErrRemoteNotFound`| Remote storage base path `<remote.path>` does not exist on `<remote.host>`. | `[git-drev] Error: Remote path '<path>' does not exist.` |
| `7` | `errcode.ErrTransport` | SSH connection failure, authentication rejection, or missing `ssh` binary. | `[git-drev] Error: Failed to connect to remote host '<host>' via SSH...` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Orphan Detection:** Revisions that exist on the remote storage host but have no corresponding commit in local Git history (e.g., deleted branches or test builds) are labeled as `[orphaned]` in the table, enabling automated retention scripts to prune unreferenced storage.
- **Marker Commit Filtering:** Marker commits created by `git drev create` are skipped during correlation so they do not shadow actual payload revision commits.
- **Zero Local Mutation:** `usage` makes zero mutations to local Git refs or remote storage directories.
