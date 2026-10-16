# Subcommand Technical Specification: `git-drev update`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev update [flags]`
  - Direct binary: `git-drev update [flags]`
- **Target Go package:** `internal/cli` (`update.go`), invoking shared Git abstractions in `internal/git`.
- **Summary:** Ensures the local repository's `origin` fetch refspec is configured for all branches, fetches and prunes all remote tracking references, and hard-resets the currently checked-out local branch to match `refs/remotes/origin/<branch>`.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | *(None)* | — | — | — | — | `update` accepts only the global flags defined in `docs/spec/global.md` (`--verbose`/`-v`, `--quiet`/`-q`, `--dry-run`/`-n`, `--no-color`, `--help`/`-h`). |

- **Positional arguments:**
  - **Cardinality:** Exact `0` positional arguments.
  - **Validation rules:** Unlike legacy `drs::common::no_args` (which logged a warning to `stdout` and silently ignored unexpected positional arguments), passing any positional argument to `git drev update` MUST fail immediately with `errcode.ErrUsage` (exit code `2`) before performing any Git or network operations.

- **Preconditions & state assertions:**
  1. **Argument & Flag Validation:** Verify no positional arguments or unknown flags are present, and global flags (`--verbose` and `--quiet`) are not mutually exclusively set.
  2. **Non-Bare Working Tree:** Must be invoked inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `"true"`). Invoking outside a Git repository or inside a bare repository fails with `errcode.ErrNotRepository` (exit code `3`).
  3. **Repository Configuration (`.drev.json` & `drev.*`):** Must load and validate the repository configuration at `<repo-root>/.drev.json` (or `GIT_DREV_CONFIG` override) merged with `git config drev.*` overrides. Missing or invalid configuration fails with `errcode.ErrConfig` (exit code `3`).
  4. **Symbolic `HEAD` (Attached Branch):** `HEAD` must be attached to a local branch (`git symbolic-ref --quiet --short HEAD`). If `HEAD` is detached (e.g., after selecting a tag or specific revision UUID), execution fails immediately with `errcode.ErrDetachedHead` (exit code `4`) and emits the remediation hint: `Use "git drev select <branch>" to select a branch first.`
  5. **Remote Tracking Branch Existence (Post-Fetch):** After fetching from remotes, `refs/remotes/origin/<branch>` must exist (`git show-ref --verify --quiet refs/remotes/origin/<branch>`). If the current local branch has no corresponding branch on `origin`, execution fails with `errcode.ErrGitState` (exit code `4`) before attempting `git reset --hard`.

## 3. Subcommand Domain Models & Signatures
```go
package cli

import (
	"context"

	"git-drev/internal/config"
	"git-drev/internal/git"
	"git-drev/internal/logui"
)

// UpdateOptions encapsulates the validated inputs for the `update` subcommand.
type UpdateOptions struct {
	DryRun  bool
	Verbose bool
	Quiet   bool
}

// UpdateResult records the outcome of the branch update operation.
type UpdateResult struct {
	Branch       string // Short branch name (e.g., "main" or "feature/x").
	UpstreamRef  string // Canonical remote-tracking ref (e.g., "refs/remotes/origin/main").
	PreviousHash string // Commit SHA-1 before reset (empty if unborn).
	UpdatedHash  string // Commit SHA-1 of UpstreamRef after fetch/reset.
	DryRun       bool   // True if mutating operations were skipped due to --dry-run.
}

// RunUpdate executes the `git drev update` domain workflow (and the `--latest`
// phase of `git drev get`) in isolation from CLI flag parsing.
func RunUpdate(
	ctx context.Context,
	gitRunner git.Runner,
	cfg *config.Config,
	logger *logui.Logger,
	opts UpdateOptions,
) (*UpdateResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)
1. **Input normalization & validation:**
   - Parse flags and verify `len(args) == 0`. If `len(args) > 0`, return `errcode.Wrap(errcode.ErrUsage, "update takes no positional arguments")`.
   - Resolve `RepoRoot` and `Prefix` via `gitRunner.RepoRoot(ctx)` and chdir to `RepoRoot` as specified in Section 2 of `docs/spec/global.md`.
   - Load and validate `*config.Config` from `<repo-root>/.drev.json` (or `GIT_DREV_CONFIG`) and `drev.*` Git config keys.
   - Resolve the active local branch via `branch, detached, err := gitRunner.CurrentBranch(ctx)` (`git symbolic-ref --quiet --short HEAD`).
     - If `detached` is `true`, log error `Detached HEAD state detected, aborting` and hint `Use "git drev select <branch>" to select a branch first.` to `stderr`, returning `errcode.ErrDetachedHead` (exit code `4`).
     - If `err != nil`, return `errcode.Wrap(errcode.ErrGitExec, ...)` (exit code `5`).

2. **Core algorithm:**
   - **Step 2.1 — Ensure wildcard remote fetch refspec:**
     - Invoke `gitRunner.EnsureRemoteFetchConfig(ctx)`.
     - Reads `git config --local --get-all remote.origin.fetch`.
     - If `+refs/heads/*:refs/remotes/origin/*` is not present among existing values, executes `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` without unsetting existing refspecs or performing per-branch `ls-remote` network calls.
   - **Step 2.2 — Fetch and prune remote tracking branches:**
     - Log operational progress (when not `--quiet`): `[drev] Fetching latest metadata from origin`.
     - Invoke `gitRunner.FetchAll(ctx)` which executes `git fetch --all --prune --quiet` (or without `--quiet` when `--verbose` is active, streaming subprocess output to `stderr`).
     - If `FetchAll` fails, return `errcode.Wrap(errcode.ErrGitExec, "fetch failed")` (exit code `5`).
   - **Step 2.3 — Verify upstream tracking reference exists:**
     - Construct `upstreamRef := "refs/remotes/origin/" + branch`.
     - Verify `upstreamRef` exists locally via `git rev-parse --verify --quiet "refs/remotes/origin/<branch>^{commit}"` (or `git show-ref --verify --quiet refs/remotes/origin/<branch>`).
     - If the remote-tracking ref does not exist (e.g., the branch was never pushed to `origin` or was deleted on `origin` and pruned), return `errcode.Wrapf(errcode.ErrGitState, "remote branch %q does not exist on origin", "origin/"+branch)` (exit code `4`).
   - **Step 2.4 — Hard-reset working tree and branch ref to upstream:**
     - Invoke `gitRunner.ResetHard(ctx, "refs/remotes/origin/"+branch)` which executes `git reset --hard refs/remotes/origin/<branch> --`.
     - If `ResetHard` fails (e.g., due to `.git/index.lock` contention or filesystem permission errors in the working tree), return `errcode.Wrap(errcode.ErrGitExec, "reset failed")` (exit code `5`).
     - Return `&UpdateResult{Branch: branch, UpstreamRef: upstreamRef, ...}, nil`.

3. **Dry-run behavior (`--dry-run` / `-n`):**
   - Precondition checks (Steps 1.1–1.4: repository check, `.drev.json` validation, and `CurrentBranch` symbolic-ref check) execute normally.
   - In Step 2.1, if `+refs/heads/*:refs/remotes/origin/*` is missing from local Git config, `git-drev` does NOT mutate `.git/config`; instead it logs to `stderr`:
     `[drev] [DRY RUN] Would execute: git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*`
   - In Step 2.2, `git fetch` (which mutates `.git/FETCH_HEAD` and `refs/remotes/origin/*`) is NOT executed; instead `git-drev` performs a read-only remote probe via `gitRunner.RemoteBranchExists(ctx, branch)` (`git ls-remote --exit-code --heads origin refs/heads/<branch>`) to validate that the upstream branch exists on `origin`, and logs:
     `[drev] [DRY RUN] Would execute: git fetch --all --prune`
     - If `RemoteBranchExists` reports the branch is missing on `origin`, it returns `errcode.ErrGitState` (exit code `4`).
   - In Step 2.4, `git reset --hard` is NOT executed; instead `git-drev` logs:
     `[drev] [DRY RUN] Would execute: git reset --hard refs/remotes/origin/<branch> --`
   - Exits with code `0`.

4. **Interruption, cleanup & rollback:**
   - All Git subprocesses (`git config`, `git fetch`, `git ls-remote`, `git reset`) are spawned with `exec.CommandContext(ctx, ...)` bound to the root signal context (`SIGINT`/`SIGTERM`).
   - If `ctx.Done()` fires during `git fetch --all --prune` or `git reset --hard`, the child `git` process receives `SIGTERM` (followed by `SIGKILL` after 3 seconds).
   - Git natively manages lockfiles (`.git/index.lock`, `.git/config.lock`) and cleans them up on `SIGTERM`. `RunUpdate` immediately aborts any subsequent steps and returns `errcode.ErrInterrupted` (exit code `130`).
   - No temporary files or staging directories are created by `update`.

## 5. Side Effects, Git Plumbing & I/O Contract
- **Git plumbing invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix` | Caller `PWD` | Newline-delimited boolean (`true`), absolute path, and relative prefix | Non-zero exit or `!= "true"` returns `ErrNotRepository` (exit `3`). |
  | `git symbolic-ref --quiet --short HEAD` | `RepoRoot` | Single line `<branch>\n` on `stdout` | Exit code `1` (detached `HEAD`) returns `ErrDetachedHead` (exit `4`); other non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git config --local --get-all remote.origin.fetch` | `RepoRoot` | Zero or more lines of refspecs on `stdout` | Exit code `1` (key absent) treated as empty list; other non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` | `RepoRoot` | Empty `stdout` (executed only if wildcard refspec is missing and not `--dry-run`) | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git fetch --all --prune --quiet` | `RepoRoot` | Empty `stdout` (or fetch progress on `stderr` when `--verbose`) | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git show-ref --verify --quiet refs/remotes/origin/<branch>` | `RepoRoot` | Empty `stdout` | Non-zero exit returns `ErrGitState` (exit `4`) with message that `origin/<branch>` does not exist. |
  | `git ls-remote --exit-code --heads origin refs/heads/<branch>` | `RepoRoot` | `<sha1>\trefs/heads/<branch>\n` (used in `--dry-run` mode instead of `git fetch`) | Exit code `2` (missing remote branch) returns `ErrGitState` (exit `4`); exit code `128` returns `ErrGitExec` (exit `5`). |
  | `git reset --hard refs/remotes/origin/<branch> --` | `RepoRoot` | `HEAD is now at <short-sha> <subject>\n` (captured; logged to `stderr` only in `--verbose` mode) | Non-zero exit returns `ErrGitExec` (exit `5`). |

- **Filesystem & metadata mutations:**
  - **`.git/config`:** Appends `fetch = +refs/heads/*:refs/remotes/origin/*` under `[remote "origin"]` if and only if not already present (retaining existing `.git/config` permissions, typically `0644`).
  - **`.git/refs/remotes/origin/*` and `.git/packed-refs`:** Updated and pruned by `git fetch --all --prune`.
  - **`.git/HEAD` target ref (`refs/heads/<branch>`), `.git/index`, and tracked files in `<repo-root>`:** Reset by `git reset --hard` to match the tree of `refs/remotes/origin/<branch>`.
  - **Untracked / Ignored `workDir` (e.g., `<repo-root>/data`):** Untouched by `git reset --hard` because `workDir` is gitignored metadata payload storage; synchronizing `workDir` requires `git drev get`.

- **Standard output (`stdout`):**
  - Emits **0 bytes** on normal or `--dry-run` execution. (Only `--help` / `-h` or `--version` writes to `stdout`.)

- **Standard error (`stderr`):**
  - Default mode:
    - `[drev] Updating branch '<branch>' to origin/<branch>`
  - Verbose mode (`--verbose` / `-v`):
    - Includes subprocess diagnostics from `git fetch` and `git reset --hard` prefixed or forwarded to `stderr`.
  - Quiet mode (`--quiet` / `-q`):
    - Suppresses all informational `[drev] ...` messages; emits only `[drev] ! <error>` and `[drev] Hint: <hint>` on failure.
  - Dry-run mode (`--dry-run` / `-n`):
    - `[drev] [DRY RUN] Would execute: git fetch --all --prune`
    - `[drev] [DRY RUN] Would execute: git reset --hard refs/remotes/origin/<branch> --`

## 6. Error Matrix & Exit Code Mapping
| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown flag, unexpected positional argument, or `-v` with `-q` | `errcode.ErrUsage` | `2` | `[drev] ! <flag/argument validation error>` |
| Executed outside a Git worktree or inside a bare repo | `errcode.ErrNotRepository` | `3` | `[drev] ! Not inside a Git working tree` |
| Missing, unreadable, or schema-invalid `.drev.json` / `drev.*` config | `errcode.ErrConfig` | `3` | `[drev] ! Configuration error: <details>` |
| Repository is in a detached `HEAD` state | `errcode.ErrDetachedHead` | `4` | `[drev] ! Detached HEAD state detected, aborting`<br>`[drev] Hint: Use "git drev select <branch>" to select a branch first.` |
| Current branch `<branch>` does not exist on `origin` after fetch | `errcode.ErrGitState` | `4` | `[drev] ! Remote branch 'origin/<branch>' does not exist` |
| `git config` read/write failure | `errcode.ErrGitExec` | `5` | `[drev] ! Failed to configure remote fetch refspec: <git stderr>` |
| `git fetch --all --prune` fails (network, auth, or missing `origin`) | `errcode.ErrGitExec` | `5` | `[drev] ! Fetch failed: <git stderr>` |
| `git reset --hard` fails (e.g., `.git/index.lock` exists or I/O error) | `errcode.ErrGitExec` | `5` | `[drev] ! Reset failed: <git stderr>` |
| `git` binary missing from `$PATH` | `errcode.ErrTransport` | `7` | `[drev] ! Required binary 'git' not found in PATH` |
| Context canceled via `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[drev] ! Operation interrupted` |

## 7. Legacy Modernization & Edge Cases
- **Fixed legacy quirks:**
  1. **Destructive `drs::common::check_remote_refs` (`common.sh:266-294`):**
     - *Legacy bug:* `update.sh` called `drs::common::check_remote_refs`, which unconditionally ran `git config --unset-all remote.origin.fetch` and then looped over each prior refspec running `git ls-remote --exit-code origin` over the network before re-adding it. If the network dropped during `update`, `.git/config` was left with **zero fetch refspecs**, breaking subsequent `git fetch` calls.
     - *Modernized behavior:* `EnsureRemoteFetchConfig` performs a non-destructive local config check and idempotently adds `+refs/heads/*:refs/remotes/origin/*` if absent; `git fetch --all --prune` handles pruning stale remote-tracking branches atomically in a single network connection.
  2. **Silent Ignored Positional Arguments & `eval set --` Injection (`common.sh:211-243`):**
     - *Legacy bug:* `drs::common::no_args` accumulated positional arguments into `params="${params} $1"`, evaluated `eval set -- "${params}"` (exposing shell metacharacter/glob expansion bugs), and if arguments were passed (e.g., `git drs-update mybranch` when the user confused `update` with `select`), printed `[drs] This command takes no argument(s), please check your input` to `stdout` and **proceeded to `git reset --hard` the current branch anyway**!
     - *Modernized behavior:* Any positional argument is rejected before any state mutation with `errcode.ErrUsage` (exit code `2`) on `stderr`.
  3. **Ambiguous Branch Name Resolution (`update.sh:29`, `update.sh:31`):**
     - *Legacy bug:* `update.sh` resolved the branch via `git rev-parse --abbrev-ref HEAD` and passed `"origin/${branch}"` to `git reset --hard`. If a repository contained a local tag or directory named `origin/<branch>`, Git ref disambiguation could resolve the wrong object or treat it as a pathspec without `--`.
     - *Modernized behavior:* Uses `git symbolic-ref --quiet --short HEAD` for unambiguous branch extraction, verifies `refs/remotes/origin/<branch>` via `git show-ref --verify --quiet`, and passes the full canonical ref `refs/remotes/origin/<branch> --` to `git reset --hard`.
  4. **Subdirectory Invocation Failure (`common.sh:172-178`):**
     - *Legacy bug:* `update.sh` checked `[[ ! -f "drs.json" ]]` relative to the current working directory without calling `git rev-parse --show-toplevel`, failing whenever invoked from a subdirectory via `$DRS_HOME/update.sh`.
     - *Modernized behavior:* Resolves `RepoRoot` via `git rev-parse --show-toplevel` at startup and locates `.drev.json` at `RepoRoot`.

- **Boundary conditions:**
  - **Hierarchical branch names (`feature/foo/bar`):** `git symbolic-ref --quiet --short HEAD` preserves slashes (`feature/foo/bar`), and `refs/remotes/origin/feature/foo/bar` resolves accurately.
  - **Branch names starting with `-` or containing special characters:** Passing canonical `refs/remotes/origin/<branch>` followed by `--` to `git reset --hard` prevents leading-dash option injection and ref/path ambiguity.
  - **Subdirectory invocation (`GIT_PREFIX`):** Works identically whether invoked from `<repo-root>` or any nested subdirectory (including inside `workDir`).
  - **Uncommitted tracked metadata modifications:** Because `origin` takes precedence in `git-drev` (see `docs/legacy/README.md` "Origin has precedence"), `git reset --hard refs/remotes/origin/<branch> --` intentionally discards uncommitted changes to tracked metadata files while leaving untracked/gitignored directories (such as `workDir`) intact.
  - **Detached `HEAD` state:** Checked before `EnsureRemoteFetchConfig` or `FetchAll` so that no network call is wasted when `HEAD` is detached.

## 8. Black-Box Verification Scenarios
| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Local repo with valid `.drev.json` is on branch `main` at commit `A`. Remote `origin/main` has advanced to commit `B`. Tracked file `env.json` has local uncommitted edits; gitignored `data/build.bin` exists locally. | `git drev update` | Exit code `0`.<br>`stdout`: empty.<br>`stderr`: contains `[drev] Updating branch 'main' to origin/main`.<br>Repo state: `HEAD` and `refs/heads/main` point to commit `B`; `env.json` matches commit `B`; `data/build.bin` remains intact. |
| `TC-02` | Same setup as `TC-01` (`main` at `A`, `origin/main` at `B`), with `--dry-run` enabled. | `git drev update --dry-run` | Exit code `0`.<br>`stdout`: empty.<br>`stderr`: contains `[drev] [DRY RUN] Would execute: git fetch --all --prune` and `[drev] [DRY RUN] Would execute: git reset --hard refs/remotes/origin/main --`.<br>Repo state: `HEAD` remains at commit `A`; `env.json` retains local edits. |
| `TC-03` | Local repo with valid `.drev.json` is in detached `HEAD` state (e.g., checked out at a specific revision UUID or tag). | `git drev update` | Exit code `4` (`ErrDetachedHead`).<br>`stdout`: empty.<br>`stderr`: contains `[drev] ! Detached HEAD state detected, aborting` and `[drev] Hint: Use "git drev select <branch>" to select a branch first.`.<br>Repo state: `HEAD` remains unchanged; no `git fetch` or `git reset` is executed. |
| `TC-04` | Local repo is on branch `main`, and the user accidentally passes a branch name argument (`feature-x`). | `git drev update feature-x` | Exit code `2` (`ErrUsage`).<br>`stdout`: empty.<br>`stderr`: contains `[drev] ! update takes no positional arguments`.<br>Repo state: `HEAD` and working tree remain completely untouched (unlike legacy `drs-update` which warned and reset `main` anyway). |
| `TC-05` | Local repo is on a newly created local branch `local-only` (created via `git checkout -b local-only`) that has never been pushed to `origin` (or was deleted on `origin`). | `git drev update` | Exit code `4` (`ErrGitState`).<br>`stdout`: empty.<br>`stderr`: contains `[drev] ! Remote branch 'origin/local-only' does not exist`.<br>Repo state: `refs/remotes/origin/*` is updated/pruned by fetch, `git reset --hard` is not executed, and `HEAD` remains on `local-only` at its existing commit. |
| `TC-06` | Local repo was cloned with `git clone --single-branch` (so `remote.origin.fetch` only maps `main`), then switched to branch `release/1.0` which has new commits on `origin`, and command is invoked from subdirectory `data/sub/`. | `git-drev update -q` | Exit code `0`.<br>`stdout`: empty.<br>`stderr`: empty (`--quiet`).<br>Repo state: `.git/config` contains `+refs/heads/*:refs/remotes/origin/*`, `refs/remotes/origin/release/1.0` is fetched, and `HEAD` is hard-reset to `origin/release/1.0`. |
