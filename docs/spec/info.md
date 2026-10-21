# Subcommand Technical Specification: `git-drev info`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev info [flags]`
  - Direct binary: `git-drev info [flags]`
- **Target Go package:** `internal/cli` (`info.go`) and `internal/hook` (as specified in Section 5 & Section 6 of `docs/spec/global.md`).
- **Summary:** Executes the repository's `.git-drev-info-hook` script (when present) with the current revision's metadata and repository context injected as `GIT_DREV_*` environment variables, streaming project-specific build or revision information directly to `stdout`.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | *(None)* | — | — | — | — | `info` defines no subcommand-specific flags; only global flags from `docs/spec/global.md` Section 1 apply. |

- **Positional arguments:**
  - **Cardinality:** `0` (exact).
  - **Path resolution behavior:** Not applicable (no positional arguments accepted).
  - **Validation rules:** Passing one or more positional arguments is rejected immediately with `errcode.ErrUsage` (exit code `2`).

- **Preconditions & state assertions:**
  1. **Non-bare Git working tree:** Must be invoked inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree == "true"`). Invoking outside a Git repository or inside a bare repository fails with `errcode.ErrNotRepository` (exit code `3`).
  2. **Working directory normalization:** The process resolves `RepoRoot` (`git rev-parse --show-toplevel`) and `Prefix` (`git rev-parse --show-prefix`) and changes the working directory (`os.Chdir`) to `RepoRoot` so `.git-drev.yaml`, `.git-drev-info-hook`, and hook-referenced metadata files (e.g., `env.json`) resolve deterministically relative to `RepoRoot` regardless of `GIT_PREFIX` or the caller's original `PWD`.
  3. **Valid repository configuration:** Resolved configuration across the 3-tier YAML cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `<repo-root>/.git-drev.yaml`) must pass `config.Load(repoRoot)` validation (per Section 5 of `docs/spec/global.md`). Missing or malformed configuration fails with `errcode.ErrConfig` (exit code `3`).
  4. **Detached `HEAD` permitted:** Unlike `put` or `update`, `info` is read-only with respect to Git refs and **permits** detached `HEAD` states (e.g., after `git-drev select <tag>` or `git-drev select <uuid>`). If `HEAD` is detached, `GIT_DREV_BRANCH` is set to `"HEAD"`.
  5. **Hook script presence (soft precondition):** `<repo-root>/.git-drev-info-hook` is inspected via `os.Stat`.
     - If `.git-drev-info-hook` does not exist (`errors.Is(err, fs.ErrNotExist)`), `info` logs `[git-drev] No info hook found` to `stderr` (unless `--quiet` is set) and exits cleanly with exit code `0`.
     - If `.git-drev-info-hook` exists but is a directory, or `os.Stat` fails with a permission/I/O error, `info` fails with `errcode.ErrRuntime` (exit code `1`).

## 3. Subcommand Domain Models & Signatures

```go
package hook

import (
	"context"
	"io"
	"time"
)

// Kind identifies the project hook type.
type Kind string

const (
	// KindPut represents the .git-drev-put-hook script executed before committing a revision.
	KindPut Kind = "put"
	// KindInfo represents the .git-drev-info-hook script executed by the info subcommand.
	KindInfo Kind = "info"
)

// Env holds the GIT_DREV_* environment variables injected into hook subprocesses
// as specified in Section 2 of docs/spec/global.md.
type Env struct {
	Version string // GIT_DREV_VERSION: semantic version of git-drev
	UUID    string // GIT_DREV_UUID: active revision UUID (or empty/zero UUID if unavailable)
	Seq     string // GIT_DREV_SEQ: active revision sequence string (or empty if marker/uncommitted)
	Branch  string // GIT_DREV_BRANCH: current branch name or "HEAD" if detached
	WorkDir string // GIT_DREV_WORK_DIR: absolute path to configured workdir
	Root    string // GIT_DREV_ROOT: absolute path to repository working tree root
}

// RunRequest configures the execution of a repository hook script.
type RunRequest struct {
	Kind    Kind
	Path    string        // Absolute path to the hook file (<repo-root>/.git-drev-info-hook)
	WorkDir string        // Process working directory (<repo-root>)
	Env     Env           // Injected GIT_DREV_* environment metadata
	Timeout time.Duration // Execution timeout resolved from GIT_DREV_HOOK_TIMEOUT (default 5m)
	Stdout  io.Writer     // Standard output stream for hook output
	Stderr  io.Writer     // Standard error stream for hook diagnostics
}

// Runner abstracts project hook detection and execution for unit testing.
type Runner interface {
	Run(ctx context.Context, req RunRequest) error
}
```

```go
package cli

import (
	"context"
	"io"
	"time"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/hook"
	"github.com/bvarnai/git-drev/internal/term"
)

// InfoOptions encapsulates all inputs for the `git-drev info` subcommand.
type InfoOptions struct {
	RepoRoot    string
	Version     string
	Quiet       bool
	Verbose     bool
	HookTimeout time.Duration
}

// InfoResult captures the outcome of running `git-drev info`.
type InfoResult struct {
	HookFound    bool
	HookExecuted bool
	HookPath     string
	Env          hook.Env
}

// InfoService coordinates repository inspection and .git-drev-info-hook execution.
type InfoService struct {
	Insp   *git.Inspector
	Hooks  hook.Runner
	Logger *term.Logger
	Stdout io.Writer
	Stderr io.Writer
}

// RunInfo validates preconditions, resolves revision metadata from HEAD, and executes .git-drev-info-hook.
func (s *InfoService) RunInfo(ctx context.Context, cfg domain.ProjectConfig, opts InfoOptions) (InfoResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)

1. **Input normalization & validation:**
   1. Reject any positional arguments passed to `info`. If `len(args) > 0`, return `errcode.NewError(errcode.ErrUsage, "this command takes no arguments (got %d)", len(args))`.
   2. Validate mutual exclusivity of global `--verbose` and `--quiet` flags (`errcode.ErrUsage`).
   3. Parse `GIT_DREV_HOOK_TIMEOUT` from the environment if set (defaulting to `5 * time.Minute` per Section 2 of `docs/spec/global.md`). If invalid or `<= 0`, return `errcode.NewError(errcode.ErrUsage, ...)` (`exit code 2`).
   4. Query `Insp.RepoRoot(ctx, dir)` to verify the process is inside a non-bare Git working tree (`git rev-parse --is-inside-work-tree`, `--show-toplevel`, `--show-prefix`) and ensure the working directory is `RepoRoot`.
   5. Load and validate `*domain.ProjectConfig` via `config.Load(repoRoot)`.

2. **Core algorithm:**
   1. **Locate hook file:** Construct `hookPath := filepath.Join(opts.RepoRoot, ".git-drev-info-hook")` and inspect it via `os.Stat(hookPath)`:
      - If `errors.Is(err, fs.ErrNotExist)`:
        - Log `[git-drev] No info hook found` via `s.Logger.Info("No info hook found")` (which writes to `stderr` and respects `--quiet`).
        - Return `InfoResult{HookFound: false, HookExecuted: false, HookPath: hookPath}, nil`.
      - If `err != nil`:
        - Return `errcode.WrapError(errcode.ErrRuntime, err, "failed to stat info hook %q", hookPath)`.
      - If `fi.IsDir()`:
        - Return `errcode.NewError(errcode.ErrRuntime, "info hook path %q is a directory, expected file", hookPath)`.
   2. **Resolve Git context for hook environment (`hook.Env`):**
      - Query `Insp.CurrentBranch(ctx, opts.RepoRoot)` (`git symbolic-ref --quiet --short HEAD`):
        - If symbolic ref succeeds, set `Branch = branch`.
        - If detached `HEAD`, set `Branch = "HEAD"`.
        - If unexpected Git execution error, return `errcode.ErrGitExec`.
      - Query `Insp.HeadCommitMeta(ctx, opts.RepoRoot)` (`git log -1 --format=%s HEAD --`):
        - If `HEAD` points to a valid `git-drev` commit subject (`{"uuid":"...","seq":"..."}` or `{"uuid":"...","marker":"true"}`), populate `Env.UUID = meta.UUID` and `Env.Seq = meta.Seq`.
        - If `HEAD` is an unborn branch or a non-`drev` commit (e.g., initial repository setup commit prior to `create` or `put`), `HeadCommitMeta` gracefully falls back to `Env.UUID = ""` and `Env.Seq = ""` (logging a verbose diagnostic if `--verbose` is enabled) so `.git-drev-info-hook` scripts that inspect committed files (like `env.json`) can still run.
      - Set `Env.Version = opts.Version`, `Env.Root = opts.RepoRoot`, and `Env.WorkDir = filepath.Join(opts.RepoRoot, cfg.WorkDir)`.
   3. **Execute `.git-drev-info-hook`:**
      - Create a child context with timeout: `hookCtx, cancel := context.WithTimeout(ctx, opts.HookTimeout); defer cancel()`.
      - Invoke `s.Hooks.Run(hookCtx, hook.RunRequest{...})`:
        - **Execution mode detection (per Section 5 of `docs/spec/global.md`):**
          1. Read the first 2 bytes of `.git-drev-info-hook` and inspect file mode bits (`fi.Mode().Perm() & 0111 != 0`).
          2. If the file starts with a shebang (`#!`) and has executable permissions (`0111` on Unix), or if it is a standalone executable script without an `info_hook` shell function, execute it directly or via `bash`. Specifically, to seamlessly support **both** standalone executables (`#!/usr/bin/env bash`, `#!/usr/bin/env python3`, etc.) and legacy-style sourced shell scripts defining `function info_hook()`:
             - If the file is executable (`mode & 0111 != 0`) and begins with `#!` for a non-`sh`/`bash` interpreter, invoke `<hookPath>` directly as the command with `Dir = RepoRoot`.
             - Otherwise (shell script or mode `0644` file), invoke `bash -c 'set -e; source "$1"; if declare -F info_hook >/dev/null; then info_hook; fi' -- <hookPath>` with `Dir = RepoRoot`.
        - **Environment construction:** Inherit `os.Environ()` with global overrides (`LC_ALL=C`, `LANG=C`, `GIT_TERMINAL_PROMPT=0`, `GIT_ADVICE=0`) plus:
          - `GIT_DREV_VERSION=<version>`
          - `GIT_DREV_UUID=<uuid>`
          - `GIT_DREV_SEQ=<seq>`
          - `GIT_DREV_BRANCH=<branch>`
          - `GIT_DREV_WORK_DIR=<abs-work-dir>`
          - `GIT_DREV_ROOT=<abs-repo-root>`
        - **Stream binding:** Connect subprocess `Stdout` directly to `s.Stdout` (`os.Stdout`) and subprocess `Stderr` directly to `s.Stderr` (`os.Stderr`). `Stdin` is bound to `os.DevNull` (`/dev/null`) to guarantee non-interactive, deterministic execution.
      - If the hook exits with code `0`, return `InfoResult{HookFound: true, HookExecuted: true, HookPath: hookPath, Env: env}, nil`.
      - If `hookCtx.Err() == context.DeadlineExceeded` (and parent `ctx.Err() == nil`), return `errcode.NewError(errcode.ErrRuntime, ...)` reporting that `.git-drev-info-hook` timed out after `opts.HookTimeout`.
      - If `ctx.Err() != nil`, return `errcode.ErrInterrupted` (exit code `130`).
      - If the hook exits with a non-zero exit status, return `errcode.NewError(errcode.ErrRuntime, ...)` (exit code `1`).

3. **Interruption, cleanup & rollback:**
   - `info` performs no Git ref mutations, index staging, or remote SSH/rsync operations; therefore, no repository or remote rollback is required.
   - If `ctx.Done()` fires (`SIGINT` / `SIGTERM`) while `.git-drev-info-hook` is running, `exec.CommandContext` sends `SIGTERM` to the hook process group (escalating to `SIGKILL` after a 3-second `WaitDelay`), and `git-drev` exits with code `130` (`errcode.ErrInterrupted`).

## 5. Side Effects, Git Plumbing & I/O Contract
- **Git plumbing invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix` | Caller `PWD` | 3 newline-separated lines (`true`, `<abs-root>`, `<prefix>`) | Non-zero exit or `false` maps to `errcode.ErrNotRepository` (exit `3`). |
  | `git symbolic-ref --quiet --short HEAD` | `RepoRoot` | Single line `<branch-name>\n` | Exit code `1` indicates detached `HEAD` (`Branch = "HEAD"`); other non-zero exit codes map to `errcode.ErrGitExec` (exit `5`). |
  | `git log -1 --format=%s HEAD --` | `RepoRoot` | Single line commit subject (`{"uuid":"...","seq":"..."}`) | If `HEAD` is unborn or subject is not valid `RevisionMeta` JSON, `UUID` and `Seq` default to `""` without failing the command. |

- **Filesystem & metadata mutations:**
  - `git-drev info` is strictly **read-only**; it creates, modifies, or deletes zero files in `<repo-root>`, `.git/`, or remote storage.

- **Standard output (`stdout`):**
  - Strictly reserved for the raw standard output emitted by `.git-drev-info-hook` (plus `--help` or `--version` when requested).
  - If `.git-drev-info-hook` is absent, `stdout` receives **0 bytes** (the `[git-drev] No info hook found` notice is emitted to `stderr` per Section 1 of `docs/spec/global.md`).

- **Standard error (`stderr`):**
  - When `.git-drev-info-hook` is absent and `--quiet` is false:
    `[git-drev] No info hook found\n`
  - When `--verbose` is active:
    `[git-drev] Executing info hook: <repo-root>/.git-drev-info-hook (branch=<branch>, uuid=<uuid>, seq=<seq>)\n`
  - Any `stderr` output written by `.git-drev-info-hook` itself is forwarded directly to `stderr`.
  - On error:
    `[git-drev] Error: <error message>\n`

## 6. Error Matrix & Exit Code Mapping

| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown CLI flag (e.g., `--foo`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: unknown flag: --foo` |
| Unexpected positional argument(s) provided | `errcode.ErrUsage` | `2` | `[git-drev] Error: info takes no arguments (got: <args>)` |
| Mutually exclusive `--verbose` and `--quiet` flags | `errcode.ErrUsage` | `2` | `[git-drev] Error: --verbose and --quiet cannot be used together` |
| Invalid `GIT_DREV_HOOK_TIMEOUT` duration | `errcode.ErrUsage` | `2` | `[git-drev] Error: invalid GIT_DREV_HOOK_TIMEOUT "<val>": ...` |
| Invoked outside a Git worktree or in bare repo | `errcode.ErrNotRepository` | `3` | `[git-drev] Error: not inside a git working tree` |
| Missing or unreadable `.git-drev.yaml` | `errcode.ErrConfig` | `3` | `[git-drev] Error: No configuration file found` |
| Malformed YAML or invalid fields in `.git-drev.yaml` | `errcode.ErrConfig` | `3` | `[git-drev] Error: <validation error>` |
| `.git-drev-info-hook` is a directory or unreadable | `errcode.ErrRuntime` | `1` | `[git-drev] Error: cannot execute info hook ".git-drev-info-hook": ...` |
| `.git-drev-info-hook` exits with non-zero code `N` | `errcode.ErrRuntime` | `1` | `[git-drev] Error: info hook ".git-drev-info-hook" failed with exit code <N>` |
| `.git-drev-info-hook` exceeds `GIT_DREV_HOOK_TIMEOUT` | `errcode.ErrRuntime` | `1` | `[git-drev] Error: info hook ".git-drev-info-hook" timed out after <duration>` |
| Git subprocess (`symbolic-ref`/`rev-parse`) failure | `errcode.ErrGitExec` | `5` | `[git-drev] Error: git command failed: ...` |
| Context canceled via `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[git-drev] Error: operation interrupted` |

## 7. Edge Cases & Boundary Conditions
- **Boundary conditions:**
  - **Symlinks:** If `<repo-root>/.git-drev-info-hook` is a symlink pointing to a regular file inside the repository, `os.Stat` follows the symlink and executes the target normally. If it is a broken/dangling symlink, `os.Stat` returns `fs.ErrNotExist`; to help diagnose misconfigured repos, `os.Lstat` is checked when `os.Stat` returns `fs.ErrNotExist`—if a dangling symlink exists at `.git-drev-info-hook`, `info` fails with `errcode.ErrRuntime` (exit code `1`, `[git-drev] Error: info hook ".git-drev-info-hook" is a broken symlink`) rather than silently claiming no hook was found.
  - **Empty hook file (`0` bytes):** Treated as a valid no-op hook; executes cleanly, emits nothing to `stdout`, and exits `0`.
  - **Paths with spaces or Unicode characters:** `RepoRoot`, `GIT_DREV_ROOT`, `GIT_DREV_WORK_DIR`, and `hookPath` are passed as discrete elements in `exec.Cmd.Args` and `exec.Cmd.Env` (`"$1"` in the `bash -c` wrapper), preventing word-splitting on spaces or special characters.
  - **Submodule boundaries:** `git rev-parse --show-toplevel` resolves the enclosing working tree root of the active repository/submodule where `.git-drev.yaml` resides.
  - **Detached `HEAD` and marker commits:**
    - When `HEAD` is detached at a revision commit, `GIT_DREV_BRANCH="HEAD"`, `GIT_DREV_UUID="<uuid>"`, and `GIT_DREV_SEQ="<seq>"`.
    - When `HEAD` points to a branch creation marker commit (`{"uuid":"...","marker":"true"}`), `GIT_DREV_UUID` is set to the marker's UUID (or `00000000-0000-0000-0000-000000000000`) and `GIT_DREV_SEQ=""`.

## 8. Black-Box Verification Scenarios

| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid Git repo with `.git-drev.yaml`, on branch `main` at commit `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"1622824489"}`, and `.git-drev-info-hook` defining `info_hook() { echo "uuid=${GIT_DREV_UUID} seq=${GIT_DREV_SEQ} branch=${GIT_DREV_BRANCH}"; }`. | `git-drev info` | Exit code `0`.<br>`stdout`: `uuid=c1ca82b1-7f34-4f4c-9a76-05e3297b2a23 seq=1622824489 branch=main\n`.<br>`stderr`: empty.<br>No repo/FS mutations. |
| `TC-02` | Valid Git repo with `.git-drev.yaml`, executable `.git-drev-info-hook` (`0755`, `#!/bin/sh\necho "build: ok"`), invoked from subdirectory `sub/dir/`. | `git-drev info` | Exit code `0`.<br>`stdout`: `build: ok\n`.<br>`stderr`: empty.<br>Hook executes with `PWD == <repo-root>`. |
| `TC-03` | Valid Git repo with `.git-drev.yaml`, but no `.git-drev-info-hook` file exists. | `git-drev info` | Exit code `0`.<br>`stdout`: empty (`0` bytes).<br>`stderr`: `[git-drev] No info hook found\n`. |
| `TC-04` | Valid Git repo with `.git-drev.yaml` and `.git-drev-info-hook`. | `git-drev info unexpected-arg` | Exit code `2` (`ErrUsage`).<br>`stdout`: empty.<br>`stderr`: `[git-drev] Error: info takes no arguments (got: unexpected-arg)\n`.<br>Hook is NOT executed. |
| `TC-05` | Valid Git repo with `.git-drev.yaml` and `.git-drev-info-hook` containing `info_hook() { echo "err" >&2; exit 42; }`. | `git-drev info` | Exit code `1` (`ErrRuntime`).<br>`stdout`: empty.<br>`stderr`: `err\n[git-drev] Error: info hook ".git-drev-info-hook" failed with exit code 42\n`. |
| `TC-06` | Valid Git repo with `.git-drev.yaml` and `.git-drev-info-hook` containing `sleep 10`, with `GIT_DREV_HOOK_TIMEOUT=100ms`. | `git-drev info` | Exit code `1` (`ErrRuntime`).<br>`stdout`: empty.<br>`stderr`: `[git-drev] Error: info hook ".git-drev-info-hook" timed out after 100ms\n`.<br>Child process is terminated. |
| `TC-07` | Directory outside any Git repository (or missing `.git-drev.yaml`). | `git-drev info` | Exit code `3` (`ErrNotRepository` or `ErrConfig`).<br>`stdout`: empty.<br>`stderr`: `[git-drev] Error: ...` diagnostic message. |
