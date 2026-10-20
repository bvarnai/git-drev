# Subcommand Technical Specification: `git-drev name`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev name [flags]`
  - Direct binary: `git-drev name [flags]`
- **Target Go package:** `internal/cli` (`name.go`)
- **Summary:** Queries the local Git repository state and prints the currently checked-out branch name (or a deterministic detached `HEAD` status indicator) to standard output (`stdout`).

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | *(None)* | — | — | — | — | `name` accepts only the global flags defined in `docs/spec/global.md` (`--verbose`, `--quiet`, `--no-color`, `--help`). |

- **Positional arguments:**
  - **Cardinality:** Exact `0` positional arguments.
  - **Path resolution behavior:** Not applicable (`name` takes no path arguments). The command may be invoked from the worktree root or any subdirectory within the working tree (`GIT_PREFIX` is normalized per Section 2 of `docs/spec/global.md`).
  - **Validation rules:**
    - Passing any positional argument (except when handled by the root `help` command dispatcher) is rejected as a CLI usage violation (`ErrUsage`, exit code `2`), replacing the legacy behavior where `drs::common::no_args` printed an informational notice to `stdout` and exited `0`.
    - Passing any unknown flag or mutually exclusive global flags (`--verbose` and `--quiet`) fails immediately with `ErrUsage` (exit code `2`).

- **Preconditions & state assertions:**
  1. **Non-bare Git working tree:** Must be executed inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `"true"`). Executing outside a Git repository, inside a bare repository, or directly inside the `.git/` directory fails with `ErrNotRepository` (exit code `3`).
  2. **Configuration file independence:** Unlike commands that interact with remote storage (`get`, `put`, `usage`, `select`), `name` is a purely local Git inspection command and **does not** require `.git-drev.yaml` to exist or be valid.
  3. **Unborn branch compatibility:** Must succeed on newly initialized repositories with an unborn `HEAD` (where `HEAD` is a symbolic ref pointing to `refs/heads/<branch>` before the initial commit exists), whereas legacy `git rev-parse --abbrev-ref HEAD` failed with `fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree`.

## 3. Subcommand Domain Models & Signatures
```go
package cli

import (
	"context"
	"io"

	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/term"
)

// DetachedHeadDisplay is the canonical human-readable output emitted to stdout
// when the repository is in a detached HEAD state.
const DetachedHeadDisplay = "No branch (detached HEAD state)"

// NameOptions encapsulates the validated parameters for the `name` subcommand.
type NameOptions struct {
	Quiet bool
}

// NameResult captures the resolved branch state of the local working tree.
type NameResult struct {
	// Branch is the shortened local branch name (e.g., "main", "feature/login")
	// when Detached is false, or empty string when Detached is true.
	Branch string

	// Detached reports whether HEAD points directly to a commit object rather
	// than a symbolic reference under refs/heads/.
	Detached bool
}

// String returns the exact line content (without trailing newline) to be written to stdout.
func (r NameResult) String() string {
	if r.Detached {
		return DetachedHeadDisplay
	}
	return r.Branch
}

// RunName executes the domain logic for `git drev name` in isolation from CLI flag parsing.
func RunName(ctx context.Context, insp *git.Inspector, logger *term.Logger, stdout io.Writer, opts NameOptions) (NameResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)
1. **Input normalization & validation:**
   - Parse global flags via the CLI dispatcher. Reject any mutually exclusive flag combinations (`--verbose` and `--quiet`) or unknown flags with `ErrUsage` (exit code `2`).
   - Assert `len(args) == 0`. If one or more positional arguments are provided, return `errcode.NewError(errcode.ErrUsage, "This command takes no arguments (got: %q)", args[0])` which logs `[git-drev] Error: This command takes no arguments` to `stderr` and exits with code `2`.
   - Verify that `ctx.Err()` is nil; if already canceled, return `errcode.ErrInterrupted` (exit code `130`).

2. **Core algorithm:**
   - **Step 2.1 — Repository validation:** Call `insp.RepoRoot(ctx, dir)` (backed by `git rev-parse --is-inside-work-tree`, `--show-toplevel`, and `--show-prefix`) to verify the process is executing inside a non-bare Git working tree. If this check fails, return `errcode.ErrNotRepository` (exit code `3`).
   - **Step 2.2 — Symbolic reference resolution:** Call `insp.CurrentBranch(ctx, repoRoot)`, which executes `git symbolic-ref --quiet --short HEAD`:
     - **Case A (Exit Code `0` — Attached Branch):** Read stdout, trim trailing `\r\n` or `\n`, and return `NameResult{Branch: branchName, Detached: false}`. Note that `git symbolic-ref --quiet --short HEAD` succeeds even on an unborn branch (`git init` before any commit), returning the configured initial branch name (e.g., `main`).
     - **Case B (Exit Code `1` — Detached `HEAD`):** Verify that `HEAD` resolves to a valid commit object via `git rev-parse --verify --quiet HEAD^{commit}` (or confirm `symbolic-ref` exited `1` with empty stderr). Return `NameResult{Branch: "", Detached: true}`.
     - **Case C (Exit Code `128` or other non-zero exit code / I/O failure):** Wrap and return `errcode.ErrGitExec` (exit code `5`).
   - **Step 2.3 — Output emission:** Write `result.String() + "\n"` to `stdout`. If writing to `stdout` fails (e.g., broken pipe `EPIPE`), return `errcode.ErrRuntime` (exit code `1`).

3. **Interruption, cleanup & rollback:**
   - `name` creates no temporary files, acquires no Git index locks, and performs no filesystem or remote mutations.
   - If `ctx.Done()` fires (`SIGINT` / `SIGTERM`) while the child `git` process is running, `exec.CommandContext` terminates the subprocess and `RunName` immediately returns `errcode.ErrInterrupted` (exit code `130`) with no rollback required.

## 5. Side Effects, Git Plumbing & I/O Contract
- **Git plumbing invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix` | Caller `PWD` | Three newline-delimited lines: `"true"`, `<abs-repo-root>`, and `<relative-prefix>` (or empty line) | Non-zero exit or first line != `"true"` returns `errcode.ErrNotRepository` (exit code `3`). |
  | `git symbolic-ref --quiet --short HEAD` | `RepoRoot` | Single line `<branch-name>\n` (e.g., `main\n`, `feature/foo\n`) when on a branch; empty output on exit code `1` | Exit code `0`: attached branch. Exit code `1`: detached `HEAD` (`NameResult{Detached: true}`). Exit code `> 1` (e.g., `128` corrupt `.git/HEAD`): returns `errcode.ErrGitExec` (exit code `5`). |

- **Filesystem & metadata mutations:**
  - **None.** `git-drev name` is completely side-effect-free. It does not read or modify `.git-drev.yaml`, `.git/config`, the Git index, or the working tree.

- **Standard output (`stdout`):**
  - On attached branch (committed or unborn): `<branch-name>\n` (e.g., `main\n`), with LF (`\n`) line ending across all platforms (Linux, macOS, Windows).
  - On detached `HEAD`: `No branch (detached HEAD state)\n`.
  - Output is identical regardless of `--verbose` or `--quiet` flags so scripts and pipelines (`branch=$(git-drev name)`) receive clean porcelain output.

- **Standard error (`stderr`):**
  - Empty (`0` bytes) on success.
  - On error, emits `[git-drev] Error: <error message>\n` (and optional `[git-drev] Hint: <remediation>\n` where applicable) formatted according to Section 1 of `docs/spec/global.md`.

## 6. Error Matrix & Exit Code Mapping
| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unexpected positional argument(s) provided (e.g., `git-drev name foo`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: This command takes no arguments (got: "foo")` |
| Unknown CLI flag provided (e.g., `git-drev name --foo`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: unknown flag: --foo` |
| Mutually exclusive global flags (`--verbose` and `--quiet`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: flags --verbose and --quiet cannot be used together` |
| Invoked outside a Git repository, inside `.git/`, or in a bare repository | `errcode.ErrNotRepository` | `3` | `[git-drev] Error: Not inside a Git working tree` |
| `git` binary missing from `$PATH` | `errcode.ErrTransport` | `7` | `[git-drev] Error: Required binary "git" not found in $PATH` |
| Corrupt `.git/HEAD` or `git symbolic-ref` fails with exit code `128` | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Failed to resolve HEAD reference: <git stderr>` |
| Standard output write failure (e.g., closed stdout / `EPIPE`) | `errcode.ErrRuntime` | `1` | `[git-drev] Error: Failed to write output: <io error>` |
| Execution canceled via `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[git-drev] Error: Operation interrupted` |

## 7. Legacy Modernization & Edge Cases
- **Fixed legacy quirks:**
  1. **Fragile `git rev-parse --abbrev-ref HEAD` Resolution (`legacy/name.sh:21`):**
     - *Legacy flaw:* `name.sh` ran `branch=$(git rev-parse --abbrev-ref HEAD)` and checked `if [[ "$branch" = "HEAD" ]]`. This had two failure modes:
       1. In a newly initialized repository (`git init`) prior to the first commit, `git rev-parse --abbrev-ref HEAD` failed with `fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree`, printing raw Git fatal output to `stderr` and then printing an empty line `""` to `stdout` with exit code `0` because `set -e` was not enabled and the exit status of `$(...)` was ignored!
       2. Ambiguous ref names or bare `HEAD` strings could not be cleanly distinguished from detached state via exit code.
     - *Modernized Go behavior:* Uses `git symbolic-ref --quiet --short HEAD` (as mandated by Section 4 of `docs/spec/global.md`), which inspects `.git/HEAD` directly, succeeds even on unborn branches, and deterministically signals detached `HEAD` via exit code `1`.
  2. **Silent Ignored Positional Arguments & `eval set -- "${params}"` Injection (`legacy/common.sh:211-243`):**
     - *Legacy flaw:* `drs::common::no_args` concatenated positional arguments into `params="${params} $1"` and ran `eval set -- "${params}"` (exposing shell injection / glob expansion if arguments contained metacharacters), and when extra arguments were passed, it logged `[drs] This command takes no argument(s), please check your input` to `stdout` and **continued execution with exit code `0`**, polluting `stdout` with two lines (the warning message followed by the branch name).
     - *Modernized Go behavior:* Rejects any positional arguments immediately before executing domain logic, writing the error to `stderr` and returning exit code `2` (`ErrUsage`).
  3. **Bare Repository / `.git` Directory False Positives (`legacy/common.sh:73`):**
     - *Legacy flaw:* `drs::common::read_repository` only checked `git rev-parse --git-dir`, which succeeded inside bare repositories or when `PWD` was inside `.git/`, where workspace-oriented commands should not operate.
     - *Modernized Go behavior:* Verifies `git rev-parse --is-inside-work-tree` == `"true"`, returning `ErrNotRepository` (exit code `3`) otherwise.
  4. **Legacy `help` Positional Argument Returning Exit Code `1` (`legacy/common.sh:219-220, 253-257`):**
     - *Legacy flaw:* Running `git drs-name help` printed a 2-line message to `stdout`/`stderr` and exited with code `1`.
     - *Modernized Go behavior:* Standard `-h` / `--help` flags and `git-drev help name` print comprehensive usage documentation to `stdout` and exit `0`.

- **Boundary conditions:**
  - **Subdirectory invocation (`GIT_PREFIX`):** Invoking `git-drev name` from a nested subdirectory (e.g., `data/subdir/nested`) resolves the enclosing worktree's `HEAD` and outputs the branch name identically to invoking from `RepoRoot`.
  - **Hierarchical branch names & special characters:** Branches containing slashes or special characters valid in `git-check-ref-format(1)` (e.g., `feature/JIRA-123_build+test`, `release/v2.0.0-rc.1`) are emitted verbatim via `git symbolic-ref --quiet --short HEAD` without stripping path segments under `refs/heads/`.
  - **Detached `HEAD` on tag or UUID commit:** When a user checks out a tag or specific revision UUID via `git-drev select <uuid>` (which places the repo in a detached `HEAD` state), `git-drev name` deterministically prints `No branch (detached HEAD state)` to `stdout` and exits `0`.
  - **Unborn branch (`git init` with 0 commits):** `git symbolic-ref --quiet --short HEAD` reads `ref: refs/heads/<initial-branch>` from `.git/HEAD` and returns `<initial-branch>` cleanly with exit code `0`.
  - **Submodule boundaries:** If invoked inside a Git submodule working tree, `git rev-parse --show-toplevel` resolves to the submodule's working tree root and `git-drev name` reports the branch/detached state of that active working tree.
  - **Symlinked repository path:** If the caller's `PWD` enters the repository through a symlink, `git symbolic-ref` still inspects `.git/HEAD` accurately without error.

## 8. Black-Box Verification Scenarios
| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid Git repository on branch `feature/build-42` with at least one commit; no `.git-drev.yaml` present. | `git-drev name` | **Exit Code:** `0`<br>**`stdout`:** `feature/build-42\n`<br>**`stderr`:** *(empty)*<br>**Repo/FS State:** Unchanged. |
| `TC-02` | Valid Git repository in detached `HEAD` state (e.g., after `git checkout --detach HEAD` or `git-drev select <uuid>`). | `git-drev name` | **Exit Code:** `0`<br>**`stdout`:** `No branch (detached HEAD state)\n`<br>**`stderr`:** *(empty)*<br>**Repo/FS State:** Remains in detached `HEAD` state. |
| `TC-03` | Newly initialized Git repository (`git init -b main`) with an unborn `HEAD` (zero commits) and a nested subdirectory `sub/dir` as caller `PWD`. | `git-drev name` (executed from `sub/dir`) | **Exit Code:** `0`<br>**`stdout`:** `main\n`<br>**`stderr`:** *(empty)*<br>**Repo/FS State:** Unchanged. |
| `TC-04` | Valid Git repository on branch `main`. | `git-drev name unexpected-arg` | **Exit Code:** `2` (`ErrUsage`)<br>**`stdout`:** *(empty)*<br>**`stderr`:** Contains `[git-drev] Error:` and message indicating `name` takes no arguments.<br>**Repo/FS State:** Unchanged. |
| `TC-05` | Caller `PWD` is `/tmp/non-git-dir` (outside any Git repository) or inside a bare repository (`repo.git`). | `git-drev name` | **Exit Code:** `3` (`ErrNotRepository`)<br>**`stdout`:** *(empty)*<br>**`stderr`:** `[git-drev] Error: Not inside a Git working tree\n`<br>**Repo/FS State:** Unchanged. |
| `TC-06` | Valid Git repository on branch `main`. | `git-drev name --unknown-flag` | **Exit Code:** `2` (`ErrUsage`)<br>**`stdout`:** *(empty)*<br>**`stderr`:** Contains `[git-drev] Error:` and unknown flag diagnostic.<br>**Repo/FS State:** Unchanged. |
