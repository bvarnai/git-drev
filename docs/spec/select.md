# Subcommand Technical Specification: `git-drev select`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev select [flags] [<branch>|<tag>|<uuid>]`
  - Direct binary: `git-drev select [flags] [<branch>|<tag>|<uuid>]`
- **Target Go package:** `internal/cli` (`selectcmd.go`)
- **Summary:** Selects and checks out an existing target branch, tag, or specific revision commit resolved by UUID, falling back to the configured `branch.default` (`"main"` by default) when no target argument is supplied.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | *(None)* | — | — | — | — | `select` defines no subcommand-specific flags; only global flags from `docs/spec/global.md` Section 1 apply. |

- **Positional arguments:**
  - `<target>` (`[<branch>|<tag>|<uuid>]`, optional, cardinality `0..1`):
    - **Omitted (`0` arguments):** Resolves the target reference to `cfg.Branch.Default` (which defaults to `"main"` if unset across the 3-tier YAML cascade).
    - **UUID argument (`1` argument matching UUID format):** Recognized when the argument matches the case-insensitive RFC 4122 hex UUID pattern `^\{?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}?$` (optional surrounding braces `{` and `}` are stripped before matching). Resolves to the full 40-character Git commit SHA-1 hash whose commit subject JSON (`domain.RevisionMeta`) has a matching `uuid` field and is not a branch creation marker (`!meta.IsMarker()`). Does not match `domain.ZeroUUID` (`00000000-0000-0000-0000-000000000000`) when associated only with marker commits.
    - **Branch or tag reference (`1` argument not matching UUID format):** Treated as a Git branch or tag reference name. Must not be empty or start with a leading dash `-` (rejected via `git check-ref-format --allow-onelevel` validation or leading-dash guard to prevent Git flag injection).
    - **Path resolution:** Not applicable. `<target>` is a logical Git reference or revision UUID, never a filesystem path; `GIT_PREFIX` does not alter `<target>`.
    - **Dynamic autocompletion:** Shell completion dynamically provides local branch names, remote-tracking branch names (stripped of `origin/`), and tags.
    - **Excess arguments (`> 1` arguments):** Fails immediately with `errcode.ErrUsage` (exit code `2`).

- **Preconditions & state assertions:**
  1. **Non-bare Git working tree (`checks.InsideWorkTree`):** Must be invoked inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`). Invoking outside a Git repository or inside a bare repository fails with `errcode.ErrNotRepository` (exit code `3`).
  2. **Configuration presence & validity:** Resolved configuration across the 3-tier YAML cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `<repo-root>/.git-drev.yaml`) must pass `config.Load(repoRoot)` validation. Missing or invalid configuration fails with `errcode.ErrConfig` (exit code `3`).
  3. **Detached `HEAD` state allowed:** Unlike `put` and `update`, `select` MUST permit execution when `HEAD` is already detached. It is the designated recovery command (`Use "git drev select <branch>" to select a branch first.`) and also intentionally detaches `HEAD` when selecting a tag or UUID commit.
  4. **Working tree cleanliness:** Standard `git checkout` semantics apply; untracked files in the ignored `workdir` are unaffected, while uncommitted modifications to tracked metadata files that conflict with the target commit cause `git checkout` to fail with `errcode.ErrGitExec` (exit code `5`).

## 3. Subcommand Domain Models & Signatures
```go
package cli

import (
	"context"

	"git-drev/internal/domain"
	"git-drev/internal/git"
	"git-drev/internal/term"
)

// SelectTargetKind classifies how the user-supplied target argument was interpreted.
type SelectTargetKind int

const (
	// SelectTargetDefault indicates no argument was supplied and cfg.Branch.Default was used.
	SelectTargetDefault SelectTargetKind = iota
	// SelectTargetRef indicates the argument was treated as a branch or tag reference.
	SelectTargetRef
	// SelectTargetUUID indicates the argument matched the UUID pattern and was resolved to a commit hash.
	SelectTargetUUID
)

// SelectOptions encapsulates the validated inputs for the select subcommand.
type SelectOptions struct {
	// Target is the raw optional positional argument (<branch>|<tag>|<uuid>).
	// Empty when invoked with no positional arguments.
	Target string
	Quiet  bool
	Verbose bool
}

// SelectResult captures the outcome of a select execution for testing and reporting.
type SelectResult struct {
	// Kind indicates whether the target was defaulted, a direct ref, or a resolved UUID.
	Kind SelectTargetKind
	// RequestedTarget is the effective target string (Branch.Default if Target was empty).
	RequestedTarget string
	// ResolvedRef is the branch name, tag name, or commit SHA passed to git checkout.
	ResolvedRef string
}

// RunSelect executes the domain logic for `git drev select`.
func RunSelect(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	opts SelectOptions,
) (SelectResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)
1. **Input normalization & validation:**
   - Parse flags via the subcommand flag set. Reject any unknown flags (`-*` or `--*=`) with `errcode.ErrUsage` (exit code `2`) and message `[git-drev] Error: Unsupported option '<flag>'`.
   - Handle `--` end-of-options delimiter per POSIX conventions.
   - Verify positional argument count is `0` or `1`. If `len(args) > 1`, return `errcode.ErrUsage` (exit code `2`) with `[git-drev] Error: Too many arguments: expected at most 1 target (<branch>|<tag>|<uuid>), got <N>`.
   - If `len(args) == 1` and `strings.HasPrefix(args[0], "-")` (possible when passed after `--`), reject with `errcode.ErrUsage` (exit code `2`) to prevent invalid ref or option injection.
   - Resolve `RepoRoot` and `Prefix` via `insp.RepoRoot(ctx, dir)` and change the process working directory to `RepoRoot`. If not inside a non-bare working tree, return `errcode.ErrNotRepository` (exit code `3`).
   - Load and validate `cfg` via `config.Load(repoRoot)`. On failure, return `errcode.ErrConfig` (exit code `3`).

2. **Core algorithm:**
   - **Step 2.1 — Ensure remote fetch refspec configuration:**
     - Invoke `ops.EnsureRemoteFetchConfig(ctx, repoRoot)`. This inspects `git config --local --get-all remote.origin.fetch` and idempotently adds `+refs/heads/*:refs/remotes/origin/*` only if missing (without unsetting existing fetch refspecs or performing per-branch `ls-remote` network calls).
   - **Step 2.2 — Synchronize remote refs (`FetchAll`):**
     - Invoke `ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose)` (`git fetch --all --prune`) so that remote branches, tags, and newly pushed revision commits on `origin` are present in the local object database before UUID commit resolution or branch checkout.
     - If `FetchAll` fails, return `errcode.ErrGitExec` (exit code `5`) with `[git-drev] Error: Fetch failed (git fetch --all --prune)`.
   - **Step 2.3 — Resolve target reference:**
     - **Case A: `opts.Target == ""` (no positional argument):**
       - Set `requested = cfg.Branch.Default` (guaranteed non-empty, defaulting to `"main"`).
       - Set `resolvedRef = requested` and `kind = SelectTargetDefault`.
       - Log `[git-drev] Selecting '<requested>' branch by default` to `stderr`.
     - **Case B: `opts.Target` matches UUID format (`domain.ParseUUID(opts.Target)` succeeds):**
       - Normalize `targetUUID` by stripping optional `{}` braces and lowercasing.
       - Log `[git-drev] Searching for commit with uuid '<opts.Target>'` to `stderr`.
       - Invoke `commitHash, err := insp.FindCommitByUUID(ctx, repoRoot, targetUUID)`:
         - Executes `git log --all -z -F -i --grep=<targetUUID> --format=%H%x00%s`.
         - Streams NUL-delimited records; for each record, splits `<hash>\x00<subject>` and unmarshals `<subject>` into `domain.RevisionMeta`.
         - Selects the first commit (in topological/reverse-chronological order) where `strings.EqualFold(meta.UUID, targetUUID)` and `!meta.IsMarker()`.
       - If no matching non-marker commit is found (`errors.Is(err, errcode.ErrGitState)` or `commitHash == ""`), log `[git-drev] Error: No commit was found with uuid '<opts.Target>'` to `stderr` and return `errcode.ErrGitState` (exit code `4`).
       - Log `[git-drev] Found commit '<commitHash>'` to `stderr`.
       - Set `resolvedRef = commitHash` and `kind = SelectTargetUUID`.
     - **Case C: `opts.Target` does not match UUID format (branch or tag name):**
       - Set `requested = opts.Target`, `resolvedRef = opts.Target`, and `kind = SelectTargetRef`.
   - **Step 2.4 — Checkout resolved reference:**
     - Invoke `ops.Checkout(ctx, repoRoot, resolvedRef, false)` (`git -c advice.detachedHead=false checkout <resolvedRef>`).
     - If `Checkout` fails:
       - When `kind == SelectTargetRef`, inspect available candidate branch names (`insp.ListAllBranchNames`) and tags (`insp.ListAllTags`).
       - If a close candidate exists (`suggest.Closest(requested, candidates, 3)`), attach `[git-drev] Hint: Did you mean '<closest>'?` to the error.
       - Return `errcode.ErrGitExec` (exit code `5`) with `[git-drev] Error: Checkout failed (git checkout <resolvedRef>)`.
   - **Step 2.5 — Return result:**
     - Return `SelectResult{Kind: kind, RequestedTarget: requested, ResolvedRef: resolvedRef}, nil`.

3. **Interruption, cleanup & rollback:**
   - All `git` subprocesses are bound to the signal-aware `ctx` (`SIGINT` / `SIGTERM`).
   - If interrupted during `git fetch --all --prune` or `git checkout`, the child `git` process receives `SIGTERM` (and `SIGKILL` after the 3-second grace period), and `git-drev` returns `errcode.ErrInterrupted` (exit code `130`).
   - Git natively manages `.git/index.lock` atomicity during `git checkout`; no custom temporary files or staging directories are created by `select`.

## 5. Side Effects, Git Plumbing & I/O Contract
- **Git plumbing invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix` | Caller `PWD` | Newline-separated boolean (`true`), absolute repo root path, and relative prefix | Non-zero exit or `false` returns `errcode.ErrNotRepository` (exit code `3`). |
  | `git config --local --get-all remote.origin.fetch` | `RepoRoot` | Newline-separated fetch refspec strings | Exit code `1` (unset) triggers adding wildcard fetch refspec; other errors return `errcode.ErrGitExec` (exit code `5`). |
  | `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` | `RepoRoot` | Empty (`stdout`) | Executed only if wildcard refspec is missing. Non-zero exit returns `errcode.ErrGitExec` (exit code `5`). |
  | `git fetch --all --prune` | `RepoRoot` | Standard `git fetch` progress/summary on `stderr` | Non-zero exit returns `errcode.ErrGitExec` (exit code `5`). |
  | `git log --all -z -F -i --grep=<uuid> --format=%H%x00%s` | `RepoRoot` | NUL-delimited records of `<40-hex-hash>\x00<subject>\x00` matching `<uuid>` | Executed only when `<target>` is a UUID. Non-zero exit (e.g., unborn repository with no commits) returns `errcode.ErrGitState` (exit code `4`) if no commit matches. |
  | `git -c advice.detachedHead=false checkout <resolvedRef>` | `RepoRoot` | Standard `git checkout` status message | Non-zero exit returns `errcode.ErrGitExec` (exit code `5`). |

- **Filesystem & metadata mutations:**
  - `.git/config`: Updated via `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` only if the wildcard fetch refspec was absent.
  - `.git/HEAD`, `.git/index`, `.git/refs/remotes/origin/*`, and tracked metadata files in `RepoRoot`: Updated by `git fetch --all --prune` and `git checkout <resolvedRef>`.
  - `<repo-root>/<workdir>`: Untouched by `select` (remains ignored by Git until `git-drev get` is invoked).

- **Standard output (`stdout`):**
  - Emits nothing (`0` bytes) during normal operation, `--verbose`, or `--quiet`. (Only `--help` / `-h` or `--version` writes to `stdout`).

- **Standard error (`stderr`):**
  - Default branch selection log (when `<target>` is omitted and `--quiet` is false):
    `[git-drev] Selecting '<branch.default>' branch by default`
  - UUID search logs (when `<target>` is a UUID and `--quiet` is false):
    `[git-drev] Searching for commit with uuid '<target>'`
    `[git-drev] Found commit '<commitHash>'`
  - Error messages (always emitted regardless of `--quiet`):
    `[git-drev] Error: <error message>`

## 6. Error Matrix & Exit Code Mapping
| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown flag (`-x`, `--foo`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: Unsupported option '<flag>'` |
| Mutually exclusive global flags (`--verbose` and `--quiet`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: Flags --verbose and --quiet cannot be used together` |
| More than 1 positional argument supplied | `errcode.ErrUsage` | `2` | `[git-drev] Error: Too many arguments: expected at most 1 target (<branch>\|<tag>\|<uuid>), got <N>` |
| Target argument starts with `-` (e.g., `git-drev select -- -bad`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: Invalid target reference '<target>': must not start with '-'` |
| Invoked outside a Git working tree or inside a bare repository | `errcode.ErrNotRepository` | `3` | `[git-drev] Error: Not inside a Git working tree` |
| Missing, unreadable, or malformed `.git-drev.yaml` | `errcode.ErrConfig` | `3` | `[git-drev] Error: <configuration error>` |
| Target UUID not found in any non-marker commit in repository history | `errcode.ErrGitState` | `4` | `[git-drev] Error: No commit was found with uuid '<uuid>'` |
| `git config` refspec update fails | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Failed to configure remote.origin.fetch: <details>` |
| `git fetch --all --prune` fails (network/auth error or invalid remote) | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Fetch failed (git fetch --all --prune)` |
| `git checkout <resolvedRef>` fails (missing branch/tag or dirty tracked files) | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Checkout failed (git checkout <resolvedRef>)`<br>*(optional)* `[git-drev] Hint: Did you mean '<closest>'?` |
| `git` binary missing from `$PATH` | `errcode.ErrTransport` | `7` | `[git-drev] Error: Required binary 'git' not found in $PATH` |
| Context canceled via `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[git-drev] Error: Operation interrupted` |

## 7. Legacy Modernization & Edge Cases
- **Fixed legacy quirks:**
  1. **Shell word-splitting & glob injection via `eval set -- "${params}"` (`legacy/select.sh:19-42`):**
     - *Legacy flaw:* Positional arguments were concatenated into `params="${params} $1"` and evaluated via `eval set -- "${params}"`, causing shell injection or word splitting on branch names or arguments containing spaces, quotes, or shell metacharacters, and silently ignoring extra positional arguments (`$2`, `$3`, ...).
     - *Modernized behavior:* Slice-based argument handling preserves exact strings without shell evaluation and strictly rejects `len(args) > 1` with `errcode.ErrUsage` (exit code `2`).
  2. **Destructive `drs::common::check_remote_refs` (`legacy/select.sh:16`, `legacy/common.sh:266-294`):**
     - *Legacy flaw:* Unset all `remote.origin.fetch` entries in `.git/config` and ran `git ls-remote` over the network per refspec before re-adding them; a network failure left `.git/config` corrupted without fetch refspecs.
     - *Modernized behavior:* `EnsureRemoteFetchConfig` checks local config read-only and idempotently adds `+refs/heads/*:refs/remotes/origin/*` if absent; remote pruning is handled atomically by `git fetch --all --prune`.
  3. **Pre-fetch UUID lookup ordering bug (`legacy/select.sh:50-73`):**
     - *Legacy flaw:* `legacy/select.sh` searched local `git log --all` for the target UUID *before* calling `drs::common::fetch_and_checkout` (`git fetch --all`). If a CI job had just pushed a new revision commit to `origin`, `select <uuid>` failed with `No commit was found with uuid` because `origin` had not been fetched yet—even though `select.sh` would have fetched immediately afterward on line 73.
     - *Modernized behavior:* `FetchAll` (`git fetch --all --prune`) runs *before* `FindCommitByUUID`, guaranteeing that newly pushed remote revision commits are present locally when resolving `<uuid>`.
  4. **Redundant `git log` calls, dead code, and regex substring matching (`legacy/select.sh:50-62`):**
     - *Legacy flaw:* `select.sh` ran `git log --all --format=%H --grep="$1"`, spawned a separate `git log --format=%B -n 1 "$commit"` subprocess per candidate commit inside a loop, checked substring `[[ ! "$commit_message" =~ "marker" ]]`, and then executed a dead-code line `commit=$(git log --all --format=%H --grep="$1" -n 1)` on line 62 whose result was never read. Furthermore, `DRS_UUID_REGEXP` matched arbitrary alphanumeric `[A-Z0-9a-z]` characters rather than valid hexadecimal UUID digits `[0-9a-fA-F]`.
     - *Modernized behavior:* Restricts UUID recognition to valid RFC 4122 hexadecimal digits (`[0-9a-fA-F]`), executes a single streaming `git log --all -z -F -i --grep=<uuid> --format=%H%x00%s` command, parses each subject with `encoding/json` into `domain.RevisionMeta`, and verifies `strings.EqualFold(meta.UUID, targetUUID) && !meta.IsMarker()`.
  5. **Operational logs written to `stdout` (`legacy/select.sh:48,52,67`):**
     - *Legacy flaw:* `drs::common::log` wrote `[drs] Selecting ...` and `[drs] Found commit ...` to `stdout`.
     - *Modernized behavior:* All `[git-drev]` operational logs are emitted to `stderr` (and suppressed when `--quiet` is active), keeping `stdout` completely clean.
  6. **Subdirectory invocation failure (`legacy/common.sh:172-178`):**
     - *Legacy flaw:* Checked `[[ ! -f "drs.json" ]]` relative to the caller's `PWD`, failing when invoked from a subdirectory.
     - *Modernized behavior:* Resolves `RepoRoot` via `git rev-parse --show-toplevel` first and loads `.git-drev.yaml` from `RepoRoot`.

- **Boundary conditions:**
  - **Braced or uppercase UUIDs:** If the user passes `{C1CA82B1-7F34-4F4C-9A76-05E3297B2A23}` or `C1CA82B1-7F34-4F4C-9A76-05E3297B2A23`, `select` strips optional `{}` braces and performs a case-insensitive comparison (`strings.EqualFold`) against `RevisionMeta.UUID` while logging the user's input string in status messages.
  - **Marker commits sharing a parent's UUID:** When `git-drev create <new-branch>` creates a branch from an existing branch whose latest commit has UUID `U`, the new branch's initial marker commit has `{"uuid":"U","marker":"true"}`. Because `FindCommitByUUID` enforces `!meta.IsMarker()`, `git-drev select U` always resolves to the actual revision commit that produced `U`, never the child branch's marker commit.
  - **Zero UUID (`00000000-0000-0000-0000-000000000000`):** Empty branch marker commits carry `ZeroUUID` with `"marker":"true"`. Passing `00000000-0000-0000-0000-000000000000` to `select` will skip all marker commits and fail with `errcode.ErrGitState` (`No commit was found with uuid '00000000-0000-0000-0000-000000000000'`).
  - **Detached `HEAD` transitions:**
    - Selecting a UUID or a Git tag transitions the repository into a detached `HEAD` state without printing Git's detached-HEAD advice banner (`-c advice.detachedHead=false`).
    - Selecting a branch name (`main`, `feature/x`) from a detached `HEAD` state cleanly re-attaches `HEAD` to `refs/heads/<branch>` (creating a local tracking branch automatically via `git checkout <branch>` if `origin/<branch>` exists uniquely on `origin`).
  - **Subdirectory & symlink execution:** Running `git-drev select` from a nested subdirectory (including inside `<workdir>` or via a symlinked directory) resolves `RepoRoot` via `git rev-parse --show-toplevel` and executes all Git operations from `RepoRoot`.

## 8. Black-Box Verification Scenarios
| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid repo on branch `feature-a`; `.git-drev.yaml` has `branch.default: main`; `origin/main` exists. | `git-drev select` | Exit code `0`; `stdout` is empty; `stderr` contains `[git-drev] Selecting 'main' branch by default`; `git symbolic-ref --short HEAD` is `main`. |
| `TC-02` | Valid repo on `main`; remote `origin` has branch `release/1.0` not yet checked out locally. | `git-drev select release/1.0` | Exit code `0`; `stdout` is empty; `git fetch --all --prune` fetches `origin/release/1.0` and `git checkout release/1.0` switches `HEAD` to local tracking branch `release/1.0`. |
| `TC-03` | Valid repo where commit `a1b2c3d4...` has subject `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"100"}` and child branch `feat` has marker commit `e5f6a7b8...` with subject `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","marker":"true"}`. | `git-drev select c1ca82b1-7f34-4f4c-9a76-05e3297b2a23` | Exit code `0`; `stdout` is empty; `stderr` contains `[git-drev] Searching for commit with uuid 'c1ca82b1-7f34-4f4c-9a76-05e3297b2a23'` and `[git-drev] Found commit 'a1b2c3d4...'`; `HEAD` is detached at `a1b2c3d4...` (marker commit `e5f6a7b8...` is ignored). |
| `TC-04` | Valid repo with no commit matching UUID `ffffffff-ffff-4fff-bfff-ffffffffffff`. | `git-drev select ffffffff-ffff-4fff-bfff-ffffffffffff` | Exit code `4` (`ErrGitState`); `stdout` is empty; `stderr` contains `[git-drev] Error: No commit was found with uuid 'ffffffff-ffff-4fff-bfff-ffffffffffff'`; `HEAD` remains unchanged. |
| `TC-05` | Valid repo; user invokes command from nested subdirectory `sub/dir` with an nonexistent branch `no-such-branch`. | `git-drev select no-such-branch` | Exit code `5` (`ErrGitExec`); `stdout` is empty; `stderr` contains `[git-drev] Error: Checkout failed (git checkout no-such-branch)`; `HEAD` remains on original branch. |
| `TC-06` | Valid repo; user passes unsupported flag `--invalid` or two positional arguments `main develop`. | `git-drev select --invalid` / `git-drev select main develop` | Exit code `2` (`ErrUsage`); `stdout` is empty; `stderr` contains `[git-drev] Error: Unsupported option '--invalid'` or `[git-drev] Error: Too many arguments...`; no Git fetch or checkout is executed. |
