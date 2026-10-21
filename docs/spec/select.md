# Spec: git-drev select

## 1. Command Signature
- **Usage:** `git-drev select [flags] [<branch>|<tag>|<uuid>]`
- **Arguments:**
  - `[<branch>|<tag>|<uuid>]` *(optional)*: Target branch, tag, or 36-character revision UUID to select and checkout. When omitted, selects `branch.default` (`"main"` by default).
- **Flags:**
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`).
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`).
  - Safe to execute from attached branch or detached HEAD.

## 3. Core Execution Flow
1. **Argument Inspection & Context Verification:**
   - Verify argument count is 0 or 1. Reject multiple positional arguments or arguments starting with `-`.
   - Resolve repository root and load configuration.
2. **Fetch Refspec Assertion & Sync:**
   - Ensure `remote.origin.fetch` includes `+refs/heads/*:refs/remotes/origin/*`.
   - Fetch remote updates: `git fetch --all --prune`.
3. **Target Reference Resolution:**
   - **Case A (Omitted Target):** Default to `cfg.Branch.Default` (default: `"main"`). Log `[git-drev] Selecting '<target>' branch by default`.
   - **Case B (UUID Target):**
     - Matches 36-char RFC 4122 UUID pattern (optional braces stripped, case-insensitive).
     - Log `[git-drev] Searching for commit with uuid '<uuid>'`.
     - Stream commits matching UUID via `git log --all -z -F -i --grep=<uuid> --format=%H%x00%s`.
     - Parse JSON commit metadata and select the latest non-marker revision commit (`!meta.IsMarker()`).
     - If no matching revision commit exists, log `[git-drev] Error: No commit was found with uuid '<uuid>'` and fail with Exit Code `4`.
     - Log `[git-drev] Found commit '<hash>'` and set target ref to `<hash>`.
   - **Case C (Branch or Tag Target):** Set target ref to user argument directly.
4. **Checkout Execution:**
   - Execute checkout: `git -c advice.detachedHead=false checkout <resolvedRef>`.
   - If checkout fails (e.g., misspelled branch/tag name), evaluate Levenshtein distance against known branches and tags via `suggest.Closest` and attach suggestion hint if candidate found.
5. **Completion:**
   - Exit cleanly with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Target branch, tag, or UUID commit checked out successfully. | None (operational logs on `stderr`). |
| `2` | `errcode.ErrUsage` | More than 1 positional argument passed (`git-drev select b1 b2`). | `[git-drev] Error: Too many arguments: expected at most 1 target (<branch>\|<tag>\|<uuid>), got <N>` |
| `2` | `errcode.ErrUsage` | Target argument starts with `-` (`git-drev select -- -bad`). | `[git-drev] Error: Invalid target reference '<target>': must not start with '-'` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: Not inside a Git working tree` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: <configuration error>` |
| `4` | `errcode.ErrGitState` | Target UUID not found in any non-marker commit in repository history. | `[git-drev] Error: No commit was found with uuid '<uuid>'` |
| `5` | `errcode.ErrGitExec` | Fetch failure (`git fetch --all --prune`). | `[git-drev] Error: Fetch failed (git fetch --all --prune)` |
| `5` | `errcode.ErrGitExec` | Checkout failure (unknown ref or uncommitted conflicting files). | `[git-drev] Error: Checkout failed (git checkout <ref>)`<br>*(optional)* `[git-drev] Hint: Did you mean '<closest>'?` |
| `7` | `errcode.ErrTransport` | `git` binary missing from host `$PATH`. | `[git-drev] Error: Required binary 'git' not found in $PATH` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Marker Commit Filtering:** Marker commits created by `git drev create` share their parent revision's UUID with `"marker":"true"`. When selecting by UUID, `select` strictly skips marker commits and checks out the actual payload revision commit.
- **Zero UUID Exemption:** Selecting `00000000-0000-0000-0000-000000000000` is rejected with Exit Code `4` because zero UUIDs are reserved for initial marker commits and never represent downloadable snapshots.
- **Detached HEAD Transition:** Checking out a UUID commit or tag intentionally leaves the repository in a detached HEAD state with advice banners suppressed (`-c advice.detachedHead=false`). Selecting a branch name cleanly re-attaches HEAD to `refs/heads/<branch>`.
