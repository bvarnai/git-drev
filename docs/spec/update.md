# Spec: git-drev update

## 1. Command Signature
- **Usage:** `git-drev update [flags]`
- **Arguments:** None. (Positional arguments are rejected).
- **Flags:**
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`).
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`).
  - `HEAD` must be attached to an active branch (`git symbolic-ref --quiet --short HEAD`). Detached HEAD fails immediately with Exit Code `4`.
  - Remote tracking branch `origin/<branch>` must exist after fetch.

## 3. Core Execution Flow
1. **Context & Branch Verification:**
   - Verify 0 positional arguments are passed.
   - Resolve repository root and load configuration.
   - Resolve active branch via `git symbolic-ref --quiet --short HEAD`.
   - If `HEAD` is detached, emit `[git-drev] Error: Detached HEAD state detected, aborting` with hint `Use "git-drev select <branch>" to select a branch first.` and fail with Exit Code `4`.
2. **Fetch Refspec Assertion:**
   - Ensure `remote.origin.fetch` contains `+refs/heads/*:refs/remotes/origin/*`, adding it if absent.
3. **Fetch & Prune Remotes:**
   - Log `[git-drev] Updating branch '<branch>' to origin/<branch>` (unless quiet).
   - Execute `git fetch --all --prune`.
4. **Remote Tracking Ref Assertion:**
   - Verify `refs/remotes/origin/<branch>` exists (`git show-ref --verify --quiet refs/remotes/origin/<branch>`).
   - If absent, log `[git-drev] Error: Remote branch 'origin/<branch>' does not exist` and fail with Exit Code `4`.
5. **Hard-Reset to Upstream:**
   - Execute `git reset --hard refs/remotes/origin/<branch> --`.
   - Any local uncommitted changes to tracked metadata files are discarded to match origin.
   - Untracked files and gitignored `workdir` remain untouched.
6. **Completion:**
   - Exit cleanly with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Branch successfully synchronized and reset to `origin/<branch>`. | None (operational logs on `stderr`). |
| `2` | `errcode.ErrUsage` | Positional arguments provided (`git-drev update extra`). | `[git-drev] Error: update takes no positional arguments` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: Not inside a Git working tree` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: <configuration error>` |
| `4` | `errcode.ErrDetachedHead`| Repository is in detached HEAD state. | `[git-drev] Error: Detached HEAD state detected, aborting`<br>`[git-drev] Hint: Use "git-drev select <branch>" to select a branch first.` |
| `4` | `errcode.ErrGitState` | Current branch does not exist on `origin` after fetch. | `[git-drev] Error: Remote branch 'origin/<branch>' does not exist` |
| `5` | `errcode.ErrGitExec` | Fetch failure (`git fetch --all --prune`). | `[git-drev] Error: Fetch failed: <git stderr>` |
| `5` | `errcode.ErrGitExec` | Hard reset failure (`git reset --hard`). | `[git-drev] Error: Reset failed: <git stderr>` |
| `7` | `errcode.ErrTransport` | `git` binary missing from host `$PATH`. | `[git-drev] Error: Required binary 'git' not found in PATH` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Early Detached HEAD Guard:** Detached HEAD is checked before initiating network calls, ensuring no remote fetches or bandwidth are wasted when update cannot proceed.
- **Payload Preservation:** `update` strictly resets Git-tracked metadata files in the repository root. Gitignored directories (such as `workdir` containing the cached snapshot payload) are untouched by `git reset --hard`.
- **Plumbing Safety:** Passing canonical `refs/remotes/origin/<branch>` followed by `--` to `git reset --hard` prevents option injection and ref/path ambiguity.
