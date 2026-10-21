# Spec: git-drev name

## 1. Command Signature
- **Usage:** `git-drev name [flags]`
- **Arguments:** None. (Positional arguments are rejected).
- **Flags:**
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations are executed relative to repository root (`git rev-parse --show-toplevel`).
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Read-only inspection; safe to execute in any repository state (clean, dirty, rebase, merge, detached HEAD, unborn initial branch).
  - Configuration file (`.git-drev.yaml`) is not required.

## 3. Core Execution Flow
1. **Repository Context Verification:**
   - Call `insp.RepoRoot(ctx, dir)` (verifying `git rev-parse --is-inside-work-tree == "true"`).
   - If outside a repository or in a bare repository, return `ErrNotRepository` (Exit Code `3`).
2. **Symbolic Ref Resolution:**
   - Execute `git symbolic-ref --quiet --short HEAD`.
   - **Case A (Exit Code 0 — Attached Branch):** Read stdout, trim trailing newline, and return `Branch: <name>`, `Detached: false`.
   - **Case B (Exit Code 1 — Detached HEAD):** Verify HEAD resolves to a valid commit via `git rev-parse --verify --quiet HEAD^{commit}`. Return `Branch: ""`, `Detached: true`.
   - **Case C (Exit Code > 1 / Failure):** Wrap and return `ErrGitExec` (Exit Code `5`).
3. **Standard Output Emission:**
   - On attached branch: Write `<branch-name>\n` to `stdout`.
   - On detached HEAD: Write `No branch (detached HEAD state)\n` to `stdout`.
   - Exit with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Branch name or detached HEAD status emitted to `stdout`. | None. |
| `2` | `errcode.ErrUsage` | Positional argument(s) provided (`git-drev name foo`). | `[git-drev] Error: This command takes no arguments (got: "foo")` |
| `2` | `errcode.ErrUsage` | Unknown CLI flag provided (`git-drev name --flag`). | `[git-drev] Error: unknown flag: --flag` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree, inside `.git/`, or in bare repo. | `[git-drev] Error: Not inside a Git working tree` |
| `5` | `errcode.ErrGitExec` | Corrupt `.git/HEAD` or Git plumbing failure. | `[git-drev] Error: Failed to resolve HEAD reference: <git stderr>` |
| `7` | `errcode.ErrTransport` | `git` binary missing from host `$PATH`. | `[git-drev] Error: Required binary "git" not found in $PATH` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Unborn Branch Compatibility:** On a fresh repository with zero commits (`git init`), `git symbolic-ref --quiet --short HEAD` reads `.git/HEAD` and returns the initial configured branch (e.g., `main`) cleanly.
- **Detached HEAD Stability:** After selecting a historical revision via `git drev select <uuid>`, `name` outputs `No branch (detached HEAD state)` to standard output.
- **Subdirectory Invocation:** Invoking `name` from any nested directory within the worktree evaluates the enclosing worktree's branch and outputs the identical branch name.
- **Machine-Readable Porcelain:** Standard output contains strictly the branch string followed by `\n` without log prefixes, making it safe for command substitution (`BRANCH=$(git-drev name)`).
