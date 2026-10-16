# Subcommand Technical Specification: `git-drev get`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev get [flags] [<target_directory>]`
  - Direct binary: `git-drev get [flags] [<target_directory>]`
- **Target Go package:** `internal/cli` (`internal/cli/get.go`)
- **Summary:** Synchronizes the local working directory (or an explicit `<target_directory>`) with the remote directory revision referenced by the current `HEAD` commit via SSH and `rsync`, optionally fetching and hard-resetting the current branch to its latest `origin/<branch>` state first when `--latest` is specified.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | `--latest` | `-l` | `bool` | `false` | No | Fetch all remote refs and hard-reset the active branch to `origin/<branch>` (equivalent to running `git drev update`) before synchronizing the directory revision. |
  | `--stats` | — | `bool` | `false` | No | Pass `--stats` to `rsync` to print detailed file-transfer statistics at the end of the synchronization. |

  *(Global flags `--verbose` / `-v`, `--quiet` / `-q`, `--dry-run` / `-n`, `--no-color`, `--help` / `-h`, and `--version` are inherited from `docs/spec/global.md` Section 1.)*

- **Positional arguments:**
  1. `[<target_directory>]` (** optional**, cardinality `0..1`):
     - **Omitted (`0` arguments):** Defaults to the repository's configured `workDir` resolved relative to `RepoRoot` (`filepath.Join(RepoRoot, cfg.WorkDir)`, where `cfg.WorkDir` defaults to `"data"`).
     - **Provided (`1` argument):** Resolved relative to `CallerDir` (`filepath.Join(RepoRoot, Prefix)`, the user's `PWD` at invocation time) before the process changes working directory to `RepoRoot`. If the user supplies an absolute path, it is cleaned via `filepath.Clean(arg)` and used directly.
     - **Validation rules:**
       - Passing more than 1 positional argument fails immediately with `ErrUsage` (exit code `2`).
       - An empty string argument (`""`) fails immediately with `ErrUsage` (exit code `2`).
       - The resolved target directory path MUST NOT equal `<RepoRoot>` itself, `<GitDir>`, or any path inside `<GitDir>/...`; attempting to synchronize into the repository root or `.git` metadata tree fails with `ErrUsage` (exit code `2`) to prevent `rsync --delete-during` from destroying repository metadata.
       - If the resolved target path already exists on the local filesystem as a non-directory (e.g., regular file, FIFO, socket, or symlink resolving to a non-directory), validation fails with `ErrRuntime` (exit code `1`).

- **Preconditions & state assertions:**
  1. **Non-bare Git working tree:** Must be invoked inside a valid Git working tree (`git rev-parse --is-inside-work-tree` == `true`). Bare repositories or invocations inside `.git/` fail with `ErrNotRepository` (exit code `3`).
  2. **Configuration validity:** `<RepoRoot>/.drev.json` (or `$GIT_DREV_CONFIG`) merged with `git config drev.*` overrides must exist, parse as valid UTF-8 JSON, and satisfy all schema constraints defined in `docs/spec/global.md` Section 5 (`remote.host`, `remote.path`, `workDir`). Missing or invalid configuration fails with `ErrConfig` (exit code `3`).
  3. **Branch attachment check (conditional on `--latest`):**
     - When `--latest` (`-l`) is `false`: Detached `HEAD` state is **permitted** so users can retrieve revisions after running `git drev select <tag>` or `git drev select <uuid>`.
     - When `--latest` (`-l`) is `true`: `HEAD` MUST be a symbolic reference to a local branch (`git symbolic-ref --quiet --short HEAD`). If `HEAD` is detached, execution aborts before any network call with `ErrDetachedHead` (exit code `4`) and emits the remediation hint: `Use "git drev select <branch>" to select a branch first.`
  4. **Valid non-marker revision commit at `HEAD`:**
     - After any optional `--latest` branch update, `HEAD` must resolve to a commit whose subject (`%s`) unmarshals into a valid `domain.RevisionMeta` JSON object.
     - If `meta.IsMarker()` is `true` (`"marker":"true"` or `meta.UUID == domain.ZeroUUID`), execution fails with `ErrGitState` (exit code `4`) because no revision payload has been uploaded to the branch yet.
     - If `meta.UUID` is missing or does not match the canonical RFC 4122 UUID v4 hex pattern (`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`), execution fails with `ErrGitState` (exit code `4`).

## 3. Subcommand Domain Models & Signatures

```go
package cli

import (
	"context"
	"io/fs"

	"git-drev/internal/config"
	"git-drev/internal/git"
	"git-drev/internal/term"
	"git-drev/internal/transport"
)

// GetOptions encapsulates the validated inputs for the `get` subcommand.
type GetOptions struct {
	// TargetDir is the absolute path to the local destination directory.
	// If empty prior to resolution, it defaults to filepath.Join(RepoRoot, Config.WorkDir).
	TargetDir string

	// Latest instructs Get to fetch all remotes and hard-reset the active branch
	// to origin/<branch> before reading HEAD metadata.
	Latest bool

	// Stats enables rsync transfer statistics (--stats).
	Stats bool

	// Verbose enables detailed rsync itemization (-v --itemize-changes).
	Verbose bool

	// Quiet suppresses informational stderr logs and passes --quiet to rsync.
	Quiet bool

	// DryRun simulates git mutations and passes --dry-run (-n) to rsync without
	// creating local directories or writing files.
	DryRun bool
}

// GetResult captures the outcome of a completed `get` execution for testing and telemetry.
type GetResult struct {
	// UUID is the normalized lowercase RFC 4122 UUID of the synchronized revision.
	UUID string

	// TargetDir is the canonical absolute path where the revision was (or would be) synchronized.
	TargetDir string

	// UpdatedBranch is the branch name reset to origin/<branch> when Latest is true, or "" otherwise.
	UpdatedBranch string

	// DryRun reports whether the operation was executed in simulation mode.
	DryRun bool
}

// FS abstracts local filesystem mutations required by `get` for deterministic unit testing.
type FS interface {
	Stat(name string) (fs.FileInfo, error)
	MkdirAll(path string, perm fs.FileMode) error
}

// GetDeps bundles external boundary interfaces injected into RunGet.
type GetDeps struct {
	Git    git.Runner
	Remote transport.RemoteRunner
	FS     FS
	Logger *term.Logger
	Config config.Config
}

// ResolveGetTargetDir resolves the user-supplied targetDirArg (if any) relative to callerDir,
// or falls back to cfg.WorkDir relative to repoRoot, validating that it does not overlap
// repoRoot or gitDir.
func ResolveGetTargetDir(repoRoot, gitDir, callerDir, workDir, targetDirArg string) (string, error)

// RunGet executes the domain logic of `git drev get` independently of CLI flag parsing.
func RunGet(ctx context.Context, deps GetDeps, opts GetOptions) (GetResult, error)
```

## 4. Execution Flow (Step-by-Step Domain Logic)

1. **Input normalization & validation:**
   - Start execution timer via `timer := term.StartTimer()`.
   - Verify global flag mutual exclusion (`opts.Verbose && opts.Quiet` returns `ErrUsage`, exit code `2`).
   - Verify positional argument count (`len(args) <= 1`; if `> 1`, return `ErrUsage`, exit code `2`).
   - Query `deps.Git.RepoRoot(ctx)` to obtain `repoRoot`, `prefix`, and validate that the caller is inside a non-bare working tree.
   - Compute `callerDir := filepath.Join(repoRoot, prefix)` and resolve `<target_directory>` via `ResolveGetTargetDir`:
     - If positional argument `targetDirArg` is provided, resolve `absTarget = filepath.Clean(filepath.Join(callerDir, targetDirArg))` (or `filepath.Clean(targetDirArg)` if already absolute).
     - If omitted, resolve `absTarget = filepath.Clean(filepath.Join(repoRoot, deps.Config.WorkDir))`.
     - Reject `absTarget` with `ErrUsage` if `absTarget == repoRoot`, `absTarget == gitDir`, or `strings.HasPrefix(absTarget, gitDir + string(filepath.Separator))`.
   - Load and validate `.drev.json` + `git config drev.*` overrides from `repoRoot`.

2. **Core algorithm:**
   - **Step 2.1: Ensure remote fetch refspec configuration:**
     - Call `deps.Git.EnsureRemoteFetchConfig(ctx)` to ensure `remote.origin.fetch` includes `+refs/heads/*:refs/remotes/origin/*` (skipping actual `git config` write if `opts.DryRun` is true).
   - **Step 2.2: Optional branch update (`opts.Latest == true`):**
     - Call `deps.Git.CurrentBranch(ctx)` (`git symbolic-ref --quiet --short HEAD`).
     - If `detached == true`, abort with `ErrDetachedHead` (exit code `4`) and hint `Use "git drev select <branch>" to select a branch first.`
     - Call `deps.Git.FetchAll(ctx)` (`git fetch --all --prune`). On failure, return `ErrGitExec` (exit code `5`).
     - Verify `origin/<branch>` exists locally (`refs/remotes/origin/<branch>`). If missing, return `ErrGitState` (exit code `4`).
     - Call `deps.Git.ResetHard(ctx, "origin/"+branch)` (`git reset --hard origin/<branch>`). On failure, return `ErrGitExec` (exit code `5`).
   - **Step 2.3: Read and validate `HEAD` revision metadata:**
     - Call `deps.Git.HeadCommitMeta(ctx)` (`git log -1 --format=%s HEAD`) and unmarshal JSON into `domain.RevisionMeta`.
     - Note: When `opts.Latest && opts.DryRun` is active (so `git reset --hard origin/<branch>` was simulated rather than executed), ` deps.Git.BranchCommitMeta(ctx, "refs/remotes/origin/"+branch)` is read instead of local `HEAD` so the dry-run inspects the actual latest remote revision metadata!
     - If JSON unmarshaling fails or `meta.UUID` is empty: return `ErrGitState` (exit code `4`) with message `Unable to read directory metadata (missing uuid)`.
     - Check `meta.IsMarker()` (`meta.Marker == "true"`) BEFORE or alongside UUID validation: if `meta.IsMarker()` is `true` (or `meta.UUID == domain.ZeroUUID`), return `ErrGitState` (exit code `4`) with message `No directory revision available yet on current branch`.
     - Validate `meta.UUID` against the RFC 4122 UUID v4 regex (`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`) and normalize to lowercase (`uuid = strings.ToLower(meta.UUID)`). If invalid, return `ErrGitState` (exit code `4`).
     - Log to `stderr`: `[drev] Directory revision uuid is '<uuid>'`.
   - **Step 2.4: Verify remote revision existence via SSH BEFORE mutating local directories:**
     - Log to `stderr`: `[drev] Checking revision on remote host`.
     - Construct `remoteRevPath := path.Join(deps.Config.Remote.Path, uuid)`.
     - Call `deps.Remote.CheckDirExists(ctx, deps.Config.Remote.Host, remoteRevPath)`.
     - If an SSH transport error occurs (exit status `255` or connection failure), return `ErrTransport` (exit code `7`).
     - If the remote directory does not exist (`exists == false`), return `ErrRemoteNotFound` (exit code `6`) with error message `Unable to find revision '<uuid>' on remote host` and remediation hint `Revision might be gone already (that's normal depending on your retention policy)`.
     - *(Ordering improvement over legacy `get.sh`: verifying remote revision existence before calling `MkdirAll` prevents leaving behind an empty newly created `<target_directory>` when the remote revision has expired or been purged.)*
   - **Step 2.5: Prepare local target directory:**
     - Inspect `opts.TargetDir` via `deps.FS.Stat(opts.TargetDir)`.
     - If it exists and `!info.IsDir()`, return `ErrRuntime` (exit code `1`) with message `Target path '<path>' exists and is not a directory`.
     - If it does not exist (`errors.Is(err, fs.ErrNotExist)`):
       - When `!opts.DryRun`, create it via `deps.FS.MkdirAll(opts.TargetDir, 0o755)`. If creation fails, return `ErrRuntime` (exit code `1`) with message `Failed to create target directory '<path>'`.
       - When `opts.DryRun` is `true`, log `[drev] [DRY RUN] Would create target directory: <path>` to `stderr` without modifying the filesystem.
   - **Step 2.6: Synchronize payload via `rsync`:**
     - Log to `stderr`: `[drev] Getting directory revision from remote host (this might take a while)`.
     - Invoke `deps.Remote.RsyncGet(ctx, transport.SyncRequest{...})` with:
       - `Host`: `deps.Config.Remote.Host`
       - `RemotePath`: `remoteRevPath + "/"` (trailing slash mandatory so `rsync` copies directory contents into `LocalPath`)
       - `LocalPath`: `filepath.ToSlash(opts.TargetDir) + "/"` (converted to MSYS/cygpath format on Windows if required by the `rsync` binary)
       - `Verbose`: `opts.Verbose`
       - `Quiet`: `opts.Quiet`
       - `Stats`: `opts.Stats`
       - `DryRun`: `opts.DryRun`
       - `UserOptions`: `deps.Config.Remote.RsyncOptions.Get`
     - If `RsyncGet` fails, return `ErrTransport` (exit code `7`) with message `Unable to get directory revision`.
   - **Step 2.7: Completion logging:**
     - Emit elapsed duration to `stderr` via `deps.Logger.Info(timer.String())` (`[drev] Took X min Y sec`, suppressed under `--quiet`).
     - Return `GetResult{UUID: uuid, TargetDir: opts.TargetDir, UpdatedBranch: updatedBranch, DryRun: opts.DryRun}, nil`.

3. **Dry-run behavior (`--dry-run` / `-n`):**
   - Precondition checks, `.drev.json` validation, `CurrentBranch`, and `CheckDirExists` (read-only SSH probe) execute normally against the live repository and remote host.
   - If `EnsureRemoteFetchConfig` detects a missing fetch refspec, it logs `[drev] [DRY RUN] Would execute: git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` instead of mutating `.git/config`.
   - When `--latest` (`-l`) is combined with `--dry-run`:
     - `git fetch --all --prune` is NOT executed if avoiding all local `.git` mutations is required, OR if remote ref inspection is needed: `git-drev` logs `[drev] [DRY RUN] Would execute: git fetch --all --prune` and `[drev] [DRY RUN] Would execute: git reset --hard origin/<branch>` without mutating `HEAD` or local refs, reading commit metadata from `HEAD` (or `refs/remotes/origin/<branch>` if already fetched).
   - If `<target_directory>` does not exist locally:
     - `os.MkdirAll` is skipped and `[drev] [DRY RUN] Would create target directory: <target_directory>` is logged to `stderr`.
     - Because `rsync --dry-run` requires the destination parent path or directory to be addressable without failing on missing intermediate directories, if `<target_directory>` does not exist on disk during `--dry-run`, `RsyncGet` logs `[drev] [DRY RUN] Would execute: rsync ...` rather than failing on a missing local destination directory; if `<target_directory>` already exists on disk, `rsync` is invoked with `--dry-run` (`-n`) appended so the user sees the exact transfer delta (`--itemize-changes` / `--stats`) without modifying any local files.

4. **Interruption, cleanup & rollback:**
   - All subprocesses (`git`, `ssh`, `rsync`) are bound to the signal-aware `ctx` (`SIGINT` / `SIGTERM`).
   - On context cancellation:
     - Active `ssh` or `rsync` child processes receive `SIGTERM` followed by `SIGKILL` after the 3-second grace period (`cmd.WaitDelay`).
     - `git-drev` aborts immediately and returns `ErrInterrupted` (exit code `130`).
     - Note on partial `rsync` transfers: Because `rsync` updates `<target_directory>` in place (using `--delete-during` and temporary dot-files per file in transfer), `rsync` automatically unlinks its own in-flight temporary file on `SIGTERM`, leaving already-transferred files intact as a valid baseline so a subsequent `git drev get` resumes incrementally without re-downloading completed files.

## 5. Side Effects, Git Plumbing & I/O Contract

- **Git plumbing & external invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix --git-dir` | Caller `PWD` | Newline-delimited boolean and paths | Non-zero exit or `false` $\rightarrow$ `ErrNotRepository` (exit `3`) |
  | `git config --local --get-all remote.origin.fetch` | `RepoRoot` | Newline-delimited refspec strings | Exit code `1` (key missing) triggers refspec addition; other non-zero $\rightarrow$ `ErrGitExec` (exit `5`) |
  | `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*` | `RepoRoot` | Empty (only if wildcard refspec missing) | Non-zero exit $\rightarrow$ `ErrGitExec` (exit `5`) |
  | `git symbolic-ref --quiet --short HEAD` | `RepoRoot` | Branch name on `stdout` (only when `--latest` is set) | Exit code `1` $\rightarrow$ `ErrDetachedHead` (exit `4`); other non-zero $\rightarrow$ `ErrGitExec` (exit `5`) |
  | `git fetch --all --prune` | `RepoRoot` | Standard progress on `stderr` (only when `--latest` is set) | Non-zero exit $\rightarrow$ `ErrGitExec` (exit `5`) |
  | `git show-ref --verify --quiet refs/remotes/origin/<branch>` | `RepoRoot` | Empty (only when `--latest` is set) | Non-zero exit $\rightarrow$ `ErrGitState` (exit `4`) |
  | `git reset --hard refs/remotes/origin/<branch> --` | `RepoRoot` | Standard reset line (discarded/logged only in `-v`) | Non-zero exit $\rightarrow$ `ErrGitExec` (exit `5`) |
  | `git log -1 --format=%s HEAD --` | `RepoRoot` | Single-line JSON string `{"uuid":"...","seq":"..."}` | Non-zero exit or invalid JSON $\rightarrow$ `ErrGitState` (exit `4`) |
  | `ssh -T <host> sh -c 'test -d "$1"' -- <remote.path>/<uuid>` | `RepoRoot` | Empty (`0` if directory exists, `1` if missing, `255` on SSH error) | Exit `1` $\rightarrow$ `ErrRemoteNotFound` (exit `6`); exit `255`/other $\rightarrow$ `ErrTransport` (exit `7`) |
  | `rsync [implicit-flags] [user-flags] -e "ssh -T" -- <host>:<remote.path>/<uuid>/ <target_dir>/` | `RepoRoot` | Progress/stats stream routed to `stderr` | Non-zero exit $\rightarrow$ `ErrTransport` (exit `7`) |

  - **Implicit `rsync` flag construction order:**
    1. Verbosity/progress flags:
       - If `--quiet` (`-q`): `--quiet`
       - Else: `--info=progress2` (if `stderr` is a TTY) and, if `--verbose` (`-v`) is set, `-v --itemize-changes`
    2. Statistics flag:
       - If `--stats`: `--stats`
    3. Dry-run flag:
       - If `--dry-run` (`-n`): `--dry-run`
    4. Core archive & deletion flags:
       - `-a --delete-during`
    5. User-configured flags (appended last so user overrides win):
       - Tokenized `remote.rsyncOptions.get` (or `drev.remote.rsyncGetOptions`).
    6. Transport & path arguments:
       - `-e`, `<resolved-ssh-command>` (default `ssh -T` or `$GIT_DREV_SSH_COMMAND`)
       - `--`
       - `<host>:<remote.path>/<uuid>/`
       - `<target_directory>/`

- **Filesystem & metadata mutations:**
  - **`.git/config`:** Adds `remote.origin.fetch = +refs/heads/*:refs/remotes/origin/*` only if missing.
  - **`.git/HEAD` & working tree (when `--latest` is set):** Hard-resets tracked metadata files in `<RepoRoot>` to match `refs/remotes/origin/<branch>`. (Ignored files in `workDir` are untouched by `git reset --hard`.)
  - **`<target_directory>`:**
    - Created with mode `0755` (`drwxr-xr-x` minus umask) via `os.MkdirAll` if not already present (only after remote revision existence is confirmed).
    - Synchronized in-place by `rsync -a --delete-during`: extraneous local files inside `<target_directory>` not present in `<remote.path>/<uuid>/` are deleted, and remote files, symlinks, permissions, and modification times are replicated locally.

- **Standard output (`stdout`):**
  - `git drev get` produces **no output on `stdout`** during normal, verbose, quiet, or dry-run execution (all operational logs, `rsync` progress/stats, and timer summaries are routed to `stderr` per `docs/spec/global.md` Section 1).
  - Only `git drev get -h` / `git-drev get --help` writes usage documentation to `stdout`.

- **Standard error (`stderr`):**
  - Normal mode (`!Quiet`):
    ```text
    [drev] Directory revision uuid is '<uuid>'
    [drev] Checking revision on remote host
    [drev] Getting directory revision from remote host (this might take a while)
    <rsync progress / itemize-changes / stats output>
    [drev] Took <M> min <S> sec
    ```
  - Quiet mode (`--quiet` / `-q`):
    - No `[drev]` informational lines or `rsync` progress are emitted; `stderr` remains completely silent unless a warning or error occurs.

## 6. Error Matrix & Exit Code Mapping

| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown flag, extra positional args (`> 1`), or mutually exclusive `-v` and `-q` | `errcode.ErrUsage` | `2` | `[drev] ! <flag/argument validation error>` |
| Target directory resolves to `<RepoRoot>` or inside `<GitDir>` | `errcode.ErrUsage` | `2` | `[drev] ! Invalid target directory '<path>': cannot overwrite repository root or .git directory` |
| Invoked outside a Git working tree or inside a bare repository | `errcode.ErrNotRepository` | `3` | `[drev] ! Not inside a Git working tree` |
| Missing or malformed `.drev.json`, or missing `remote.host` / `remote.path` | `errcode.ErrConfig` | `3` | `[drev] ! Configuration error: <details>` |
| `--latest` passed while `HEAD` is detached | `errcode.ErrDetachedHead` | `4` | `[drev] ! Detached HEAD state detected, aborting`<br>`[drev] Hint: Use "git drev select <branch>" to select a branch first.` |
| `--latest` passed when `origin/<branch>` does not exist on remote | `errcode.ErrGitState` | `4` | `[drev] ! Remote branch 'origin/<branch>' does not exist` |
| `HEAD` commit is an empty branch creation marker (`"marker":"true"`) | `errcode.ErrGitState` | `4` | `[drev] ! No directory revision available yet on current branch` |
| `HEAD` commit subject is not valid JSON or lacks a valid RFC 4122 `uuid` | `errcode.ErrGitState` | `4` | `[drev] ! Unable to read directory metadata (missing or invalid uuid)` |
| `git fetch --all --prune` or `git reset --hard origin/<branch>` fails | `errcode.ErrGitExec` | `5` | `[drev] ! Fetch failed: <git stderr>` or `[drev] ! Reset failed: <git stderr>` |
| Remote directory `<remote.path>/<uuid>` does not exist on remote server | `errcode.ErrRemoteNotFound` | `6` | `[drev] ! Unable to find revision '<uuid>' on remote host`<br>`[drev] Hint: Revision might be gone already (that's normal depending on your retention policy)` |
| Target path exists locally as a regular file instead of a directory | `errcode.ErrRuntime` | `1` | `[drev] ! Target path '<path>' exists and is not a directory` |
| `os.MkdirAll(<target_directory>, 0755)` fails (e.g., `EACCES` / `EROFS`) | `errcode.ErrRuntime` | `1` | `[drev] ! Failed to create target directory '<path>': <os error>` |
| SSH connection failure or `rsync` subprocess non-zero exit | `errcode.ErrTransport` | `7` | `[drev] ! Unable to get directory revision: <ssh/rsync error>` |
| Execution canceled via `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[drev] ! Operation interrupted` |

## 7. Legacy Modernization & Edge Cases

- **Fixed legacy quirks:**
  1. **Detached `HEAD` corruption with `--latest` (`legacy/get.sh:78-83`):**
     - *Legacy bug:* `legacy/get.sh` omitted `drs::common::precondition_detached` when `--latest` was passed and ran `branch=$(git rev-parse --abbrev-ref HEAD)` followed by `git reset --hard "origin/${branch}"`. In a detached `HEAD` state, `git rev-parse --abbrev-ref HEAD` outputs `"HEAD"`, causing `git reset --hard origin/HEAD` to silently reset the user's detached `HEAD` to the remote's default branch (`origin/main`) instead of failing!
     - *Modernized fix:* When `--latest` is passed, `git-drev get` explicitly verifies `git symbolic-ref --quiet --short HEAD` first and fails with `ErrDetachedHead` (exit code `4`) if `HEAD` is detached, while continuing to allow detached `HEAD` when `--latest` is not passed.
  2. **Premature `<target_directory>` creation before remote probe (`legacy/get.sh:109-126`):**
     - *Legacy bug:* `legacy/get.sh` ran `mkdir -p "$1"` at line 111 *before* checking `ssh "${host}" "[ -d ${path}/${uuid} ]"` at line 122. If the remote revision had been purged by retention cleanup, `get.sh` left behind an empty newly created `<target_directory>` on disk.
     - *Modernized fix:* `git-drev get` probes remote revision existence first (Step 2.4) and only creates `<target_directory>` (Step 2.5) after confirming the remote revision exists.
  3. **Empty branch marker error routed to `stdout` (`legacy/get.sh:96-99`):**
     - *Legacy bug:* When `marker == "true"`, `get.sh` called `drs::common::log "No directory revision available yet on current branch"` (which wrote to `stdout`) and then exited `1`.
     - *Modernized fix:* Emits a structured error `[drev] ! No directory revision available yet on current branch` to `stderr` and exits with code `4` (`ErrGitState`).
  4. **Shell word-splitting on `<target_directory>` and `$rsyncOptions` (`legacy/get.sh:58,64,156`):**
     - *Legacy bug:* `eval set -- "${params}"` split `<target_directory>` arguments containing spaces and silently discarded any words after `$1`; unquoted `rsync $rsyncOptions` expanded shell globs against the local working directory.
     - *Modernized fix:* Positional arguments are validated for exact cardinality (`0..1`) without shell evaluation, and `rsync` arguments are passed directly as a `[]string` slice to `exec.CommandContext`.
  5. **Unsafe remote shell interpolation (`legacy/get.sh:122`):**
     - *Legacy bug:* `ssh "${host}" "[ -d ${path}/${uuid} ]"` interpolated `${path}` and `${uuid}` into a remote shell string without quoting.
     - *Modernized fix:* Strictly validates `uuid` as RFC 4122 hex and passes the remote path as a positional parameter to `sh -c 'test -d "$1"' -- <remotePath>`.
  6. **Stdout pollution from `rsync` (`legacy/get.sh:156`):**
     - *Legacy bug:* `rsync` wrote `--info=progress2`, `--itemize-changes`, and `--stats` to `stdout` alongside `drs::common::log`.
     - *Modernized fix:* Both `term.Logger` and the `rsync` subprocess `cmd.Stdout` are bound to `stderr` so `stdout` remains clean.

- **Boundary conditions:**
  - **Subdirectory invocation (`GIT_PREFIX`):** If a user invokes `git drev get ./my-output` from `<repo-root>/sub/dir`, `<target_directory>` resolves to `<repo-root>/sub/dir/my-output`, whereas omitting `<target_directory>` always resolves `workDir` to `<repo-root>/<workDir>`.
  - **Symlinks:**
    - If `<target_directory>` itself is a symlink pointing to an existing directory, `os.Stat` follows the symlink (`info.IsDir() == true`) and `rsync` synchronizes into the linked directory (trailing slash `<target_directory>/` ensures `rsync` traverses the symlink rather than replacing the symlink itself).
    - Symlinks *inside* the remote revision payload are preserved verbatim as symlinks by `rsync -a` (`-l` / `--links`).
  - **Empty remote revision directory:** If `<remote.path>/<uuid>/` exists on the remote server but contains zero files, `git drev get` succeeds (`exit 0`) and empties `<target_directory>` via `rsync --delete-during`.
  - **Paths with spaces, Unicode, or special characters:** Handled natively via Go `[]string` argument passing and `sh -c 'test -d "$1"' --` remote parameter binding; `--` is passed to `rsync` so a relative `<target_directory>` starting with `-` is not interpreted as an `rsync` flag.
  - **Submodule boundaries:** `git rev-parse --show-toplevel` resolves the innermost Git working tree containing the caller's `PWD`.

## 8. Black-Box Verification Scenarios

| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid repo with `.drev.json` (`workDir: "data"`), `HEAD` commit `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"100"}`, remote directory `<path>/c1ca82b1-.../` containing `app.bin`, local `data/` contains stale `old.bin`. | `git drev get` | **Exit Code:** `0`<br>**`stdout`:** Empty<br>**`stderr`:** Contains `[drev] Directory revision uuid is 'c1ca82b1-7f34-4f4c-9a76-05e3297b2a23'`, `[drev] Checking revision on remote host`, `[drev] Getting directory revision from remote host`, `[drev] Took ...`<br>**FS State:** `data/app.bin` exists; `data/old.bin` is deleted by `--delete-during`. |
| `TC-02` | Caller `PWD` is `<repo-root>/subdir`; user specifies a relative `<target_directory>` `./custom out` (containing a space) that does not yet exist. | `git drev get "./custom out"` | **Exit Code:** `0`<br>**`stdout`:** Empty<br>**`stderr`:** Standard progress logs.<br>**FS State:** `<repo-root>/subdir/custom out/app.bin` is created and populated; default `<repo-root>/data` is untouched. |
| `TC-03` | Local branch `main` is 1 commit behind `origin/main` (which points to revision UUID `b2b282b1-7f34-4f4c-9a76-05e3297b2a23` present on remote). | `git drev get --latest --quiet` | **Exit Code:** `0`<br>**`stdout`:** Empty<br>**`stderr`:** Empty (`--quiet` suppresses informational logs and `rsync` progress).<br>**Repo & FS State:** Local `main` `HEAD` equals `origin/main`; `data/` matches revision `b2b282b1-...`. |
| `TC-04` | Repository is in detached `HEAD` state (e.g., after `git drev select v1.0`). | `git drev get --latest` | **Exit Code:** `4` (`ErrDetachedHead`)<br>**`stdout`:** Empty<br>**`stderr`:** `[drev] ! Detached HEAD state detected, aborting` and `[drev] Hint: Use "git drev select <branch>" to select a branch first.`<br>**Repo & FS State:** `HEAD` and `data/` remain completely unchanged; no network calls made. |
| `TC-05` | Current branch was just created via `git drev create feat` (`HEAD` commit subject is `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`). | `git drev get` | **Exit Code:** `4` (`ErrGitState`)<br>**`stdout`:** Empty<br>**`stderr`:** `[drev] ! No directory revision available yet on current branch`<br>**FS State:** No SSH connection opened; `data/` not created or modified. |
| `TC-06` | `HEAD` points to valid commit `{"uuid":"deadbeef-7f34-4f4c-9a76-05e3297b2a23","seq":"50"}`, but `<remote.path>/deadbeef-...` was purged by `git drev cleanup` on the remote server; local `out_dir` does not exist. | `git drev get out_dir` | **Exit Code:** `6` (`ErrRemoteNotFound`)<br>**`stdout`:** Empty<br>**`stderr`:** `[drev] ! Unable to find revision 'deadbeef-7f34-4f4c-9a76-05e3297b2a23' on remote host` and retention policy hint.<br>**FS State:** `out_dir` is **not** created on the local filesystem. |
| `TC-07` | Valid repo and remote revision `c1ca82b1-...`; local `data/` exists with `old.bin`. | `git drev get --dry-run --verbose --stats` | **Exit Code:** `0`<br>**`stdout`:** Empty<br>**`stderr`:** Contains `rsync --dry-run -v --itemize-changes --stats` preview output.<br>**FS State:** `data/old.bin` is still present and `data/app.bin` is not written to disk. |
