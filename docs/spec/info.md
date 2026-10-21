# Spec: git-drev info

## 1. Command Signature
- **Usage:** `git-drev info [flags]`
- **Arguments:** None. (Positional arguments are rejected).
- **Flags:**
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `GIT_DREV_HOOK_TIMEOUT` (default `5m`), `NO_COLOR`, `GIT_DREV_COLOR`.
- **Environment variables injected into hook:**
  - `GIT_DREV_VERSION`: Semantic version of `git-drev`.
  - `GIT_DREV_UUID`: Active revision UUID (empty if unavailable).
  - `GIT_DREV_SEQ`: Active revision sequence string (empty if uncommitted/marker).
  - `GIT_DREV_BRANCH`: Current branch name (or `"HEAD"` if detached).
  - `GIT_DREV_WORK_DIR`: Absolute path to configured `workdir`.
  - `GIT_DREV_ROOT`: Absolute path to repository working tree root.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory; operations and hook execute relative to repository root (`git rev-parse --show-toplevel`).
- **Git state assertions:**
  - Must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`).
  - Valid configuration resolved across the 3-tier cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `.git-drev.yaml`).
  - Read-only inspection; permitted from attached branch or detached HEAD.

## 3. Core Execution Flow
1. **Context & Argument Verification:**
   - Verify 0 positional arguments are passed.
   - Resolve repository root and load configuration.
   - Resolve hook timeout from `GIT_DREV_HOOK_TIMEOUT` (defaults to 5 minutes).
2. **Hook Presence Inspection:**
   - Inspect `.git-drev-info-hook` in repository root.
   - If missing: Log `[git-drev] No info hook found` to `stderr` (unless quiet) and exit cleanly with Exit Code `0`.
   - If a directory or dangling symlink: Fail with Exit Code `1`.
3. **Metadata Resolution:**
   - Resolve current branch (or `"HEAD"` if detached).
   - Resolve current revision metadata (`uuid`, `seq`) from `HEAD` commit subject (`git log -1 --format=%s HEAD`).
4. **Hook Execution:**
   - Execute `.git-drev-info-hook` within repository root with timeout context:
     - Direct execution if executable with shebang (`#!`).
     - Sourced execution (`bash -c 'set -e; source "$1"; if declare -F info_hook >/dev/null; then info_hook; fi' -- <path>`) if shell script or non-executable.
   - Injected `GIT_DREV_*` environment variables.
   - Subprocess `stdout` connects directly to `os.Stdout`; subprocess `stderr` connects to `os.Stderr`.
   - Subprocess `stdin` bound to `/dev/null`.
5. **Completion:**
   - If hook exits `0`, return Exit Code `0`.
   - If hook exits non-zero, fail with Exit Code `1`.
   - If hook times out, log timeout error and fail with Exit Code `1`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Hook executed cleanly or hook was absent. | None (hook output on `stdout`). |
| `1` | `errcode.ErrRuntime` | `.git-drev-info-hook` exited with non-zero exit code `N`. | `[git-drev] Error: info hook ".git-drev-info-hook" failed with exit code <N>` |
| `1` | `errcode.ErrRuntime` | `.git-drev-info-hook` exceeded timeout duration. | `[git-drev] Error: info hook ".git-drev-info-hook" timed out after <duration>` |
| `1` | `errcode.ErrRuntime` | `.git-drev-info-hook` is a directory or broken symlink. | `[git-drev] Error: cannot execute info hook ".git-drev-info-hook": ...` |
| `2` | `errcode.ErrUsage` | Positional arguments provided (`git-drev info extra`). | `[git-drev] Error: info takes no arguments (got: <args>)` |
| `2` | `errcode.ErrUsage` | Invalid duration string in `GIT_DREV_HOOK_TIMEOUT`. | `[git-drev] Error: invalid GIT_DREV_HOOK_TIMEOUT "<val>": ...` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--verbose` and `--quiet`). | `[git-drev] Error: --verbose and --quiet cannot be used together` |
| `3` | `errcode.ErrNotRepository` | Outside Git working tree or inside a bare repository. | `[git-drev] Error: not inside a git working tree` |
| `3` | `errcode.ErrConfig` | Missing, unreadable, or invalid `.git-drev.yaml` configuration. | `[git-drev] Error: No configuration file found` |
| `5` | `errcode.ErrGitExec` | Git plumbing failure (`rev-parse` or `symbolic-ref`). | `[git-drev] Error: git command failed: ...` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Missing Hook Graceful Exit:** When `.git-drev-info-hook` is absent, `git-drev info` exits cleanly with code `0` rather than erroring, making it safe to incorporate unconditionally into build and CI scripts.
- **Detached HEAD Information:** When inspected during detached HEAD (e.g. after `git drev select <uuid>`), `GIT_DREV_BRANCH` is injected as `"HEAD"`, allowing the hook to read commit info accurately without failing branch checks.
- **Uncommitted Working Tree:** `info` is strictly read-only; it does not require a clean working tree or commit index.
