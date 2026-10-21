# Spec: git-drev create

## 1. Command Signature
- **Usage:** `git-drev create [flags] <branch>`
- **Arguments:**
  - `<branch>` *(mandatory)*: Name of the revision metadata branch to create (or switch to if already existing).
- **Flags:**
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`).
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`).
  - Safe to execute from attached branch or detached HEAD. If unborn initial branch, defaults marker UUID to `00000000-0000-0000-0000-000000000000`.

## 3. Core Execution Flow
1. **Argument & Context Verification:**
   - Verify argument `<branch>` is provided and non-empty. Validate branch name format via `git check-ref-format --branch <branch>`.
   - Resolve repository root and load configuration.
   - Start wall-clock timer.
2. **Fetch Refspec Assertion:**
   - Ensure `remote.origin.fetch` includes `+refs/heads/*:refs/remotes/origin/*`, adding it if absent.
3. **Local Branch Check (Idempotent Selection):**
   - Check if `<branch>` exists locally (`git show-ref --verify --quiet refs/heads/<branch>`).
   - If present:
     - Log `[git-drev] Branch '<branch>' found (local)`.
     - Fetch updates (`git fetch --all --prune`) and switch to branch (`git checkout <branch>`).
     - Emit elapsed time and exit cleanly with Exit Code `0`.
4. **Remote Branch Check (Idempotent Tracking):**
   - Check if `<branch>` exists on `origin` (`git ls-remote --exit-code --heads origin refs/heads/<branch>`).
   - If present:
     - Log `[git-drev] Branch '<branch>' found (remote)`.
     - Fetch remotes (`git fetch --all --prune`) and switch (`git switch <branch>`).
     - Emit elapsed time and exit cleanly with Exit Code `0`.
5. **Parent UUID Resolution:**
   - Inspect current `HEAD` commit subject (`git log -1 --format=%s HEAD`).
   - If valid JSON containing a 36-char RFC 4122 UUID, set `parentUUID` to that UUID.
   - Otherwise (unborn `HEAD` or non-JSON commit message), set `parentUUID` to `domain.ZeroUUID` (`00000000-0000-0000-0000-000000000000`).
6. **Branch Creation & Marker Commit:**
   - Create and check out new local branch: `git checkout -b <branch>`.
   - Record previous HEAD ref/hash for rollback safety.
   - Commit empty revision marker:
     ```bash
     git commit --allow-empty -m '{"uuid":"<parentUUID>","marker":"true"}'
     ```
7. **Publish to Remote Origin:**
   - Push branch and configure tracking upstream:
     ```bash
     git push --set-upstream --force-with-lease origin refs/heads/<branch>
     ```
   - If push fails, trigger deferred rollback: checkout original ref and delete unpushed local branch (`git branch -D <branch>`).
8. **Completion:**
   - Log elapsed time summary (`[git-drev] Took X min Y sec`) to `stderr` and exit with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Branch created or switched successfully. | None (operational logs on `stderr`). |
| `2` | `errcode.ErrUsage` | Missing mandatory `<branch>` positional argument. | `[git-drev] Error: No branch name specified` |
| `2` | `errcode.ErrUsage` | Extra positional arguments passed (`git-drev create b1 b2`). | `[git-drev] Error: Unexpected argument '<arg>'` |
| `2` | `errcode.ErrUsage` | Invalid Git branch name format. | `[git-drev] Error: Invalid branch name '<branch>'` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: Not a git repository (or any of the parent directories)` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: No configuration file found` / `[git-drev] Error: <validation>` |
| `5` | `errcode.ErrGitExec` | Remote listing failure (`git ls-remote`). | `[git-drev] Error: Remote listing failed: <git stderr>` |
| `5` | `errcode.ErrGitExec` | Checkout or switch failure (e.g. dirty tracked file conflict). | `[git-drev] Error: Checkout failed: <git stderr>` |
| `5` | `errcode.ErrGitExec` | Commit failure (missing Git identity, index locked). | `[git-drev] Error: Commit failed: <git stderr>` |
| `5` | `errcode.ErrGitExec` | Push failure (rejected, network timeout, hook failure). | `[git-drev] Error: Push failed: <git stderr>` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Transactional Rollback on Push Failure:** If local branch creation or marker commit succeeds but push to `origin` fails (or is interrupted via `SIGINT`), `git-drev` restores the starting `HEAD` state and forcefully deletes the local unpushed branch (`git branch -D <branch>`), ensuring the repository is not left in a half-created local state.
- **Detached HEAD Branching:** Running `create` from a detached HEAD state (such as after `git drev select <uuid>`) branches cleanly from that exact revision commit, capturing the historical UUID in the new branch's marker commit.
- **Idempotent Switching:** If the requested branch already exists locally or on `origin`, `create` acts idempotently by switching to the branch without creating a redundant marker commit.
- **Zero Storage Interaction:** `create` only provisions metadata Git branches; no remote SSH or `rsync` connections are made.
