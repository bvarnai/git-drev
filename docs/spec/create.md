# Subcommand Technical Specification: `git-drev create`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev create [flags] <branch>`
  - Direct binary: `git-drev create [flags] <branch>`
- **Target Go package:** `internal/cli` (`create.go`)
- **Summary:** Creates a new revision metadata branch at `HEAD` with an initial marker commit carrying the parent revision's UUID (or the zero UUID if none exists) and pushes it to `origin`, or idempotently fetches and switches to `<branch>` if it already exists locally or on `origin`.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | *(None)* | — | — | — | — | `create` defines no subcommand-specific flags; it accepts only the global flags defined in `docs/spec/global.md` (`--verbose`/`-v`, `--quiet`/`-q`, `--no-color`, `--help`/`-h`). |

- **Positional arguments:**
  1. `<branch>` (exact cardinality: **1 mandatory positional argument**):
     - **Path resolution behavior:** `<branch>` is a logical Git branch reference name (resolved under `refs/heads/<branch>`), not a filesystem path. It is independent of `CallerDir` and `GIT_PREFIX`.
     - **Validation rules:**
       - Must be provided and non-empty (`strings.TrimSpace(branch) != ""`). Missing `<branch>` fails with `ErrUsage` (exit code `2`).
       - Passing more than 1 positional argument fails with `ErrUsage` (exit code `2`).
       - Must not start with `-` unless preceded by `--`, and even after `--` a branch name starting with `-` is rejected by Git ref format validation (`ErrUsage`, exit code `2`).
       - Must be a valid Git branch name as verified by `git check-ref-format refs/heads/<branch>`, rejecting control characters, spaces, `..`, `~`, `^`, `:`, `?`, `*`, `[`, `\`, trailing `/`, `.lock` suffixes, `@`, and `HEAD`. Invalid branch names fail with `ErrUsage` (exit code `2`).

- **Preconditions & state assertions:**
  1. **Non-bare Git working tree:** Must be invoked inside a valid Git working tree (`git rev-parse --is-inside-work-tree == true`). Bare repositories or invocations outside a Git worktree fail immediately with `ErrNotRepository` (exit code `3`).
  2. **Working directory normalization:** The process resolves `RepoRoot` via `git rev-parse --show-toplevel` and changes its working directory to `RepoRoot` prior to loading configuration.
  3. **Valid configuration:** Resolved configuration across the 3-tier YAML cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `<repo-root>/.git-drev.yaml`) must pass `config.Load(repoRoot)` validation. Missing or invalid configuration fails with `ErrConfig` (exit code `3`).
  4. **HEAD state:** `create` is permitted from both an active branch and a detached `HEAD` state (e.g., after selecting a historical tag or UUID via `git drev select <uuid>`, allowing a user or CI job to branch off an exact revision). If the repository is an unborn branch (fresh `git init` with zero commits), reading `HEAD` commit metadata gracefully falls back to `domain.ZeroUUID`.

## 3. Subcommand Domain Models & Signatures

```go
package cli

import (
	"context"

	"git-drev/internal/domain"
	"git-drev/internal/git"
	"git-drev/internal/term"
)

// CreateOptions holds the validated inputs for the `create` subcommand.
type CreateOptions struct {
	Branch  string
	Verbose bool
	Quiet   bool
}

// CreateAction classifies the resolution path taken by RunCreate.
type CreateAction string

const (
	// CreateActionCheckedOutLocal indicates the branch already existed in refs/heads/<branch>.
	CreateActionCheckedOutLocal CreateAction = "checked_out_local"
	// CreateActionSwitchedRemote indicates the branch existed on origin and was tracked locally.
	CreateActionSwitchedRemote CreateAction = "switched_remote"
	// CreateActionCreatedNew indicates a new branch and marker commit were created and pushed.
	CreateActionCreatedNew CreateAction = "created_new"
)

// CreateResult captures the outcome of executing the `create` subcommand for testing and reporting.
type CreateResult struct {
	Branch     string
	Action     CreateAction
	MarkerUUID string // Populated when Action == CreateActionCreatedNew (parent UUID or domain.ZeroUUID)
}

// RunCreate executes the domain logic for `git-drev create` in isolation from CLI flag parsing.
func RunCreate(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	opts CreateOptions,
) (*CreateResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)

1. **Input normalization & validation:**
   1. Start the execution timer via `timer := term.StartTimer()`.
   2. Validate global flag mutual exclusions (`--verbose` and `--quiet` cannot both be `true`; otherwise return `ErrUsage`).
   3. Verify positional argument cardinality:
      - If `len(args) == 0` or `args[0] == ""`, return `ErrUsage` with message `"No branch name specified"`.
      - If `len(args) > 1`, return `ErrUsage` with message `fmt.Sprintf("Unexpected argument %q", args[1])`.
   4. Verify repository context via `insp.RepoRoot(ctx, dir)` (returning `ErrNotRepository` on failure) and load configuration via `config.Load(repoRoot)` (returning `ErrConfig` on failure).
   5. Validate `opts.Branch` against Git branch naming rules (`insp.ValidateBranchName(ctx, repoRoot, opts.Branch)`). If invalid, return `ErrUsage` with message `fmt.Sprintf("Invalid branch name '%s'", opts.Branch)`.

2. **Core algorithm:**
   1. **Ensure remote fetch refspec configuration:**
      - Call `ops.EnsureRemoteFetchConfig(ctx, repoRoot)`.
      - This inspects `git config --local --get-all remote.origin.fetch`. If `+refs/heads/*:refs/remotes/origin/*` is missing, it idempotently adds it (without unsetting existing refspecs or making network calls).
   2. **Check local branch existence:**
      - Log `[git-drev] Checking for existing branch (local)` to `stderr`.
      - Call `insp.LocalBranchExists(ctx, repoRoot, opts.Branch)` (`git show-ref --verify --quiet refs/heads/<branch>`).
      - **If `true` (branch exists locally):**
        - Log `fmt.Sprintf("[git-drev] Branch '%s' found (local)", opts.Branch)` to `stderr`.
        - Call `ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose)` (`git fetch --all --prune`).
        - Call `ops.Checkout(ctx, repoRoot, opts.Branch, false)` (`git -c advice.detachedHead=false checkout <branch>`).
        - Log elapsed time via `logger.Info(timer.String())` and return `&CreateResult{Branch: opts.Branch, Action: CreateActionCheckedOutLocal}, nil`.
      - **If `false` (branch does not exist locally):**
        - Log `fmt.Sprintf("[git-drev] No branch '%s' found (local)", opts.Branch)` to `stderr`.
   3. **Check remote branch existence on `origin`:**
      - Log `[git-drev] Checking for existing branch (remote)` to `stderr`.
      - Call `insp.RemoteBranchExists(ctx, repoRoot, opts.Branch)` (`git ls-remote --exit-code --heads origin refs/heads/<branch>`).
      - If `git ls-remote` fails with a transport/repository error (exit code `128`), return `ErrGitExec` (exit code `5`).
      - **If `true` (exit code `0`, branch exists on `origin`):**
        - Log `fmt.Sprintf("[git-drev] Branch '%s' found (remote)", opts.Branch)` to `stderr`.
        - Call `ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose)` (`git fetch --all --prune`).
        - Call `ops.Switch(ctx, repoRoot, opts.Branch)` (`git switch <branch>`).
        - Log elapsed time via `logger.Info(timer.String())` and return `&CreateResult{Branch: opts.Branch, Action: CreateActionSwitchedRemote}, nil`.
      - **If `false` (exit code `2`, branch does not exist on `origin`):**
        - Log `fmt.Sprintf("[git-drev] No branch '%s' found (remote)", opts.Branch)` to `stderr`.
   4. **Resolve parent revision UUID from current `HEAD`:**
      - Before creating or switching branches (or immediately after `git checkout -b`, since `HEAD` tree/commit is identical), inspect the current `HEAD` commit subject via `insp.HeadCommitMeta(ctx, repoRoot)` (`git log -1 --format=%s HEAD --`).
      - Parse the subject as JSON into `domain.RevisionMeta`.
      - If `HEAD` exists, its commit subject is valid JSON, and `meta.UUID` matches the strict RFC 4122 hex UUID regex (`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`), set `parentUUID = strings.ToLower(meta.UUID)`.
      - Otherwise (unborn `HEAD`, non-JSON commit subject such as `"Add initial drev configuration"`, or missing/invalid `.uuid`), set `parentUUID = domain.ZeroUUID` (`"00000000-0000-0000-0000-000000000000"`).
   5. **Create new local branch:**
      - Log `fmt.Sprintf("[git-drev] Creating new revision branch '%s'", opts.Branch)` to `stderr`.
      - Call `ops.CheckoutNewBranch(ctx, repoRoot, opts.Branch)` (`git checkout -b <branch>`).
   6. **Commit empty revision marker:**
      - Log `[git-drev] Committing empty revision marker` to `stderr`.
      - Construct marker payload: `domain.RevisionMeta{UUID: parentUUID, Marker: "true"}` serialized as compact JSON `{"uuid":"<parentUUID>","marker":"true"}`.
      - Call `ops.CommitAllowEmpty(ctx, repoRoot, markerJSON)` (`git commit --allow-empty -m <markerJSON>`).
   7. **Publish new branch to `origin`:**
      - Log `fmt.Sprintf("[git-drev] Pushing new revision branch '%s'", opts.Branch)` to `stderr`.
      - Determine whether `refs/heads/<branch>` already has `origin/<branch>` configured as its upstream via `insp.HasUpstream(ctx, repoRoot, opts.Branch)` (`git for-each-ref --format=%(upstream:short) refs/heads/<branch>`).
      - Call `ops.PushForceWithLease(ctx, repoRoot, opts.Branch, !hasUpstream)` (`git push [--set-upstream] --force-with-lease origin refs/heads/<branch>`).
      - If the push fails (including stale ref rejection if another client concurrently created the branch on `origin`, or transport/auth failure), return `ErrGitExec` (exit code `5`).
   8. **Completion:**
      - Log elapsed time `[git-drev] Took X min Y sec` via `logger.Info(timer.String())` to `stderr` (unless `--quiet` is set).
      - Return `&CreateResult{Branch: opts.Branch, Action: CreateActionCreatedNew, MarkerUUID: parentUUID}, nil`.

3. **Interruption, cleanup & rollback:**
   - All `git` subprocesses are bound to the signal-aware `ctx` (`SIGINT`/`SIGTERM`).
   - **Transactional rollback on new-branch creation failure:**
     - Before step 2.5 (`git checkout -b <branch>`), `RunCreate` records the caller's starting ref (`origBranch, detached, _ := gitRunner.CurrentBranch(ctx)` and `origHeadHash` via `git rev-parse --verify -q HEAD`).
     - If step 2.6 (`git commit --allow-empty`) or step 2.7 (`git push`) fails or is interrupted via `ctx.Done()` after `refs/heads/<branch>` has been created locally:
       - A deferred rollback handler (executing with a short detached cleanup context `context.WithTimeout(context.Background(), 5*time.Second)`) restores the original `HEAD` (`git checkout <origBranch>` or `git -c advice.detachedHead=false checkout <origHeadHash>`) and deletes the unpushed local branch (`git branch -D <branch>`).
       - Why: In the legacy `create.sh`, if `git commit` (e.g., missing `user.name`/`user.email`) or `git push` (e.g., network failure) failed after `git checkout -b <branch>`, the repository was left stranded on a local-only branch. A subsequent re-run of `git-drev create <branch>` saw the local branch in `refs/heads/<branch>`, entered the "Branch found (local)" path, skipped the `git push`, and silently exited `0` without ever publishing the branch to `origin`! Rolling back the unpushed local branch preserves idempotency on retry.

## 5. Side Effects, Git Plumbing & I/O Contract

- **Git plumbing invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree` | Caller `PWD` | `"true\n"` | Non-zero exit or `"false"` returns `ErrNotRepository` (exit `3`). |
  | `git rev-parse --show-toplevel` | Caller `PWD` | `<abs-repo-root>\n` | Non-zero exit returns `ErrNotRepository` (exit `3`). |
  | `git check-ref-format --branch <branch>` | `RepoRoot` | `<branch>\n` | Non-zero exit returns `ErrUsage` (exit `2`). |
  | `git config --local --get-all remote.origin.fetch` | `RepoRoot` | Newline-delimited refspec strings | Exit `1` (key absent) treated as empty list; other non-zero returns `ErrGitExec` (exit `5`). |
  | `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` | `RepoRoot` | Empty | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git show-ref --verify --quiet refs/heads/<branch>` | `RepoRoot` | Empty | Exit `0` = exists locally; non-zero = does not exist locally. |
  | `git fetch --all --prune` | `RepoRoot` | Progress on `stderr` | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git -c advice.detachedHead=false checkout <branch> --` | `RepoRoot` | Branch switch diagnostic | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git ls-remote --exit-code --heads origin refs/heads/<branch>` | `RepoRoot` | `<sha>\trefs/heads/<branch>\n` | Exit `0` = exists on remote; exit `2` = does not exist on remote; exit `128` or other = returns `ErrGitExec` (exit `5`). |
  | `git switch <branch>` | `RepoRoot` | Branch switch diagnostic | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git log -1 --format=%s HEAD` | `RepoRoot` | `<commit-subject>\n` | Non-zero exit (e.g., unborn `HEAD`) falls back to `domain.ZeroUUID`. |
  | `git checkout -b <branch>` | `RepoRoot` | Branch creation diagnostic | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git commit --allow-empty -m {"uuid":"<uuid>","marker":"true"}` | `RepoRoot` | Commit summary | Non-zero exit triggers local branch rollback and returns `ErrGitExec` (exit `5`). |
  | `git for-each-ref --format=%(upstream:short) refs/heads/<branch>` | `RepoRoot` | `origin/<branch>\n` or empty | Non-zero exit returns `ErrGitExec` (exit `5`). |
  | `git push --set-upstream origin <branch> --force-with-lease` | `RepoRoot` | Push summary on `stderr` | Non-zero exit triggers local branch rollback and returns `ErrGitExec` (exit `5`). |

- **Filesystem & metadata mutations:**
  - `.git/config`: Updated only if `+refs/heads/*:refs/remotes/origin/*` was missing from `remote.origin.fetch`.
  - `.git/HEAD` & `.git/refs/heads/<branch>`: Pointed to `<branch>` (either existing or newly created with the marker commit `{"uuid":"<parent-or-zero-uuid>","marker":"true"}`).
  - `.git/refs/remotes/origin/<branch>`: Created/updated upon `git fetch --all --prune` or `git push --set-upstream origin <branch> --force-with-lease`.
  - Working tree & `workdir`: Untouched by `create` (unless switching to an existing branch that has tracked metadata file differences outside the gitignored `workdir`). Remote SSH/rsync storage is never contacted or mutated by `create`.

- **Standard output (`stdout`):**
  - Strictly empty on normal execution (`create` produces no machine-parseable `stdout` payload).
  - Contains usage/help text only when `--help` / `-h` is passed.

- **Standard error (`stderr`):**
  - All operational progress logs and elapsed time summaries are emitted to `stderr` (suppressed when `--quiet` / `-q` is set):
    ```text
    [git-drev] Checking for existing branch (local)
    [git-drev] No branch '<branch>' found (local)
    [git-drev] Checking for existing branch (remote)
    [git-drev] No branch '<branch>' found (remote)
    [git-drev] Creating new revision branch '<branch>'
    [git-drev] Committing empty revision marker
    [git-drev] Pushing new revision branch '<branch>'
    [git-drev] Took 0 min 1 sec
    ```
  - Errors are emitted to `stderr` even when `--quiet` is active:
    ```text
    [git-drev] Error: <error message>
    ```

## 6. Error Matrix & Exit Code Mapping

| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown CLI flag or mutually exclusive `-v` and `-q` | `errcode.ErrUsage` | `2` | `[git-drev] Error: Unsupported option '<flag>'` or `[git-drev] Error: Flags --verbose and --quiet are mutually exclusive` |
| Missing `<branch>` positional argument | `errcode.ErrUsage` | `2` | `[git-drev] Error: No branch name specified` |
| Extra positional arguments (`len(args) > 1`) | `errcode.ErrUsage` | `2` | `[git-drev] Error: Unexpected argument '<arg>'` |
| Invalid Git branch name (`git check-ref-format --branch` fails) | `errcode.ErrUsage` | `2` | `[git-drev] Error: Invalid branch name '<branch>'` |
| Invoked outside a Git worktree or inside a bare repository | `errcode.ErrNotRepository` | `3` | `[git-drev] Error: Not a git repository (or any of the parent directories)` |
| Missing or malformed `.git-drev.yaml` configuration | `errcode.ErrConfig` | `3` | `[git-drev] Error: No configuration file found` or `[git-drev] Error: <validation error>` |
| `git ls-remote origin` fails (missing remote `origin`, network/auth failure) | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Remote listing failed (git ls-remote --exit-code --heads origin refs/heads/<branch>)` |
| `git fetch --all --prune` fails when switching to existing local/remote branch | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Fetch failed (git fetch --all --prune)` |
| `git checkout <branch>` or `git switch <branch>` fails (e.g., dirty tracked file conflict) | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Checkout failed (git checkout <branch>)` |
| `git checkout -b <branch>` fails | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Checkout failed (git checkout -b <branch>)` |
| `git commit --allow-empty` fails (e.g., missing Git `user.name`/`user.email` identity, index lock) | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Commit failed (git commit --allow-empty)` |
| `git push` fails (auth error, remote hook rejection, or stale ref race) | `errcode.ErrGitExec` | `5` | `[git-drev] Error: Push failed (git push --set-upstream origin <branch> --force-with-lease)` |
| Context canceled via `SIGINT` / `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[git-drev] Error: Operation interrupted` |

## 7. Legacy Modernization & Edge Cases
- **Fixed legacy quirks:**
  1. **`eval set -- "${params}"` Shell Injection (`create.sh:35,42`):**
     - *Legacy flaw:* `create.sh` accumulated positional arguments in a string and evaluated `eval set -- "${params}"`. Passing a branch argument containing spaces, quotes, globs, or `$(...)` executed arbitrary shell commands or split arguments before Git ever saw them.
     - *Resolution:* Arguments are parsed directly from `os.Args` string slices and validated via `git check-ref-format --branch <branch>` before any Git command is invoked.
  2. **Ambiguous `git for-each-ref` & `git ls-remote` Matching (`create.sh:60,74`):**
     - *Legacy flaw:*
       - `git for-each-ref --format='%(refname:short)' refs/heads/"${branch}"` treated `${branch}` as a prefix/glob pattern! If a branch `feature/a` existed and the user ran `create feature`, `git for-each-ref refs/heads/feature` matched `refs/heads/feature/a` and falsely reported that branch `feature` already existed locally, causing `git checkout feature` to fail or behave unexpectedly.
       - `git ls-remote origin "${branch}"` matched tags (`refs/tags/<branch>`), pull request refs, or partial ref suffixes in addition to heads, and relied on capturing output before checking `$?` after `local` declaration.
     - *Resolution:* Use exact ref verification: `git show-ref --verify --quiet refs/heads/<branch>` for local branches and `git ls-remote --exit-code --heads origin refs/heads/<branch>` for remote branches.
  3. **External `jq` Dependency & Loose UUID Regex (`create.sh:99-102`, `common.sh:9`):**
     - *Legacy flaw:* `create.sh` spawned `jq -r '.uuid' <<< "${commit}"` and validated against `DRS_UUID_REGEXP='^\{?[A-Z0-9a-z]{8}-...'` (which accepted non-hex letters `G-Z` and optional curly braces `{}`).
     - *Resolution:* Unmarshal the `HEAD` commit subject in-process via Go's `encoding/json` into `domain.RevisionMeta`, validate against strict RFC 4122 hex (`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`), and normalize to lowercase.
  4. **Destructive `drs::common::check_remote_refs` (`create.sh:54`, `common.sh:266-294`):**
     - *Legacy flaw:* Unset `remote.origin.fetch` and looped over network calls before running `create`.
     - *Resolution:* Replaced by `gitRunner.EnsureRemoteFetchConfig(ctx)` as specified in Section 7.2 of `docs/spec/global.md`.
  5. **Partial Local Branch Stranding on Commit/Push Failure (`create.sh:92-125`):**
     - *Legacy flaw:* If `git commit --allow-empty` or `git push` failed after `git checkout -b "${branch}"`, the unpushed local branch remained checked out. Re-running `create "${branch}"` saw the local branch, entered the local-branch path (`fetch_and_checkout`), and exited `0` without ever pushing the branch to `origin`.
     - *Resolution:* If committing the marker or pushing to `origin` fails after `git checkout -b`, `RunCreate` rolls back to the original `HEAD` and deletes the unpushed local branch (`git branch -D <branch>`).
  6. **Operational Logs on `stdout` & Typo Fix (`create.sh:104`):**
     - *Legacy flaw:* `drs::common::log` wrote operational messages to `stdout`, and line 104 logged `"Commiting empty revision marker"` (misspelled).
     - *Resolution:* All operational logs are emitted to `stderr` with `[git-drev]` prefix, and spelling is standardized to `"Committing empty revision marker"` (while keeping the structure human-friendly).

- **Boundary conditions:**
  - **Subdirectory invocation (`GIT_PREFIX`):** Running `git-drev create <branch>` from `<repo-root>/data/subdir` locates `<repo-root>/.git-drev.yaml` via `RepoRoot` resolution rather than failing with `"No configuration file found"`.
  - **Detached `HEAD` state:** Running `git-drev create hotfix/v1` while in detached `HEAD` (e.g., after `git-drev select <uuid>`) succeeds: `git checkout -b hotfix/v1` branches directly from the detached commit and copies its `uuid` into the new branch's marker commit `{"uuid":"<uuid>","marker":"true"}`.
  - **Initial commit / non-revision `HEAD`:** When `HEAD` is the initial repository setup commit (e.g., `"Add initial drev configuration"`) or an unborn branch with no commits, `HeadCommitMeta` fails JSON/UUID validation and safely falls back to `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`.
  - **Branch off another empty marker branch:** When creating branch `B` while `HEAD` is on branch `A`'s marker commit (`{"uuid":"<uuid-or-zero>","marker":"true"}`), `HeadCommitMeta` extracts `meta.UUID` (preserving either the ancestor's real UUID or `domain.ZeroUUID`) and writes `{"uuid":"<uuid-or-zero>","marker":"true"}` on branch `B`.
  - **Hierarchical branch names (`feature/foo`):** Creating `feature` when `refs/heads/feature/foo` exists (no `refs/heads/feature`) accurately detects that `refs/heads/feature` does not exist locally (fixing the legacy `git for-each-ref` prefix bug). Conversely, attempting to create `feature/sub` when `refs/heads/feature` already exists as a file ref fails cleanly at `git checkout -b` with `ErrGitExec` (exit code `5`).
  - **Symlinks, empty directories, and submodules:** Because `create` only creates an `--allow-empty` metadata commit and does not stage working tree files or invoke `rsync`, untracked files, symlinks, empty directories, and submodules in the working tree remain untouched.

## 8. Black-Box Verification Scenarios

| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid `.git-drev.yaml`; repo on `main` whose `HEAD` commit subject is `"Add initial drev configuration"` (non-JSON); `origin` bare repo attached; branch `release-1.0` does not exist locally or on `origin`. | `git-drev create release-1.0` | **Exit Code:** `0`<br>**`stdout`:** Empty.<br>**`stderr`:** Logs local check (not found), remote check (not found), creating branch `release-1.0`, committing empty revision marker, pushing branch `release-1.0`, and elapsed time.<br>**Repo/FS State:** Active branch is `release-1.0`; `HEAD` commit subject is `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`; `origin/release-1.0` exists on `origin` and is set as upstream of `release-1.0`. |
| `TC-02` | Valid `.git-drev.yaml`; repo on `main` (or detached `HEAD`) at a revision commit with subject `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"1700000000"}`; branch `hotfix/login` does not exist locally or on `origin`. | `git-drev create hotfix/login` | **Exit Code:** `0`<br>**`stdout`:** Empty.<br>**`stderr`:** Logs creation, marker commit, and push of `hotfix/login`.<br>**Repo/FS State:** Active branch is `hotfix/login`; `HEAD` is a new empty commit with subject `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","marker":"true"}` whose parent is the `main` revision commit; `refs/heads/hotfix/login` is pushed to `origin`. |
| `TC-03` | Valid `.git-drev.yaml`; repo currently on `main`; branch `existing-local` already exists in `refs/heads/existing-local`. | `git-drev create existing-local` | **Exit Code:** `0`<br>**`stdout`:** Empty.<br>**`stderr`:** Contains `[git-drev] Branch 'existing-local' found (local)`.<br>**Repo/FS State:** `git fetch --all --prune` was executed; active branch is switched to `existing-local`; no new marker commit is created on `existing-local`. |
| `TC-04` | Valid `.git-drev.yaml`; repo currently on `main`; branch `remote-only` does not exist in `refs/heads/remote-only` but exists on `origin` (`refs/heads/remote-only`). | `git-drev create remote-only` | **Exit Code:** `0`<br>**`stdout`:** Empty.<br>**`stderr`:** Contains `[git-drev] No branch 'remote-only' found (local)` and `[git-drev] Branch 'remote-only' found (remote)`.<br>**Repo/FS State:** `git fetch --all --prune` was executed; local branch `remote-only` is created tracking `origin/remote-only` at the remote's existing commit without creating a new marker commit. |
| `TC-05` | Valid `.git-drev.yaml`; repo on `main`; branch `refs/heads/feat/sub` exists locally, but `refs/heads/feat` does not exist; `origin` is unreachable or rejects pushes (e.g., invalid remote URL or pre-receive hook rejection). | `git-drev create new-branch` (and separately `git-drev create` with no args) | **Case A (`git-drev create` with no args):** Exit code `2` (`ErrUsage`), `stderr` contains `[git-drev] Error: No branch name specified`.<br>**Case B (`git-drev create new-branch` when push fails):** Exit code `5` (`ErrGitExec`), `stderr` contains `[git-drev] Error: Push failed`; rollback restores active branch to `main` and deletes the unpushed local `refs/heads/new-branch` so a subsequent retry does not falsely take the local-branch shortcut. |
