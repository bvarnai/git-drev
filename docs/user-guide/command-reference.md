# Chapter 6: Command reference

This chapter provides a complete reference for all `git-drev` subcommands, arguments, and flags.

---

## Global flags

The following flags are accepted by the root `git-drev` command and all subcommands:

| Flag | Short | Type | Description |
| :--- | :--- | :--- | :--- |
| `--verbose` | `-v` | `bool` | Enable verbose operational logging and detailed command/transfer output. Mutually exclusive with `--quiet`. |
| `--quiet` | `-q` | `bool` | Suppress informational progress and status logs, emitting only errors or strict machine-parseable output. Mutually exclusive with `--verbose`. |
| `--no-color` | — | `bool` | Disable ANSI color styling on `stdout` and `stderr` regardless of TTY status. |
| `--version` | — | `bool` | Print semantic version, commit hash, and build information to `stdout` and exit `0`. |
| `--help` | `-h` | `bool` | Print usage information and flag descriptions to `stdout` and exit `0`. |

---

## Subcommands

### `git drev init [flags]`
Initializes a dedicated `git-drev` metadata repository in the current directory.

- **Usage:** `git drev init [flags]`
- **Flags:**
  - `--host <host>`: Remote SSH host (e.g. `storage.example.com` or `user@host`).
  - `--path <path>`: Remote storage base path (e.g. `/srv/drev/{repo}`).
  - `--workdir <dir>`: Local working directory for tracked revisions (default: `data`).
  - `--branch <name>`: Default baseline branch name (default: `main`).
  - `-y`, `--yes`: Non-interactive mode (accepts default options and automated confirmations).
  - `--force`: Force overwrite of existing `.git-drev.yaml` or initialization inside non-empty repository.
  - `--commit`: Create Git commit for generated configuration and `.gitignore`.
  - `--no-commit`: Skip committing generated configuration and `.gitignore`.
  - `--skip-check`: Skip remote SSH connection and directory access verification.
- **Behavior:**
  - If executed outside a Git repository, prompts to run `git init` (or auto-accepts with `-y`).
  - If executed inside an existing non-empty Git repository, emits a defensive warning that `git-drev` requires a dedicated metadata repository and requires confirmation or `--force`.
  - Collects mandatory configuration fields via flags or interactive prompts with global tier defaults.
  - Verifies remote SSH read access and detects read-only vs. read-write permissions.
  - Generates `.git-drev.yaml` and idempotently updates `.gitignore`.
  - Stages and creates the initial configuration commit (unless `--no-commit` is specified).

---

### `git drev config`
Inspects and prints the effective resolved configuration across the 3-tier YAML cascade (`User Global -> $GIT_DREV_CONFIG -> Repository Local`). Running without flags prints the `# Source hierarchy:` header and the annotated YAML configuration.

- **Usage:** `git drev config [--sources]`
- **Flags:**
  - `--sources`: Output only the resolved 3-tier `# Source hierarchy:` header without printing the configuration payload.
- **Output:** Clean YAML output to `stdout`.
- **Preconditions:** Can be run inside a Git repository, or outside a repository if global configuration is defined.

---

### `git drev create <branch>`
Creates a new revision branch with an initial marker commit carrying the parent revision's UUID (or `ZeroUUID`) and pushes it to `origin`.

- **Usage:** `git drev create <branch>`
- **Arguments:** Exactly 1 argument specifying the target branch name.
- **Behavior:**
  - If `<branch>` exists locally, checks it out idempotently.
  - If `<branch>` exists on `origin`, creates a local tracking branch and switches to it.
  - If new, records an initial marker commit (`{"uuid":"<parent-uuid>","marker":"true"}`) and pushes to `origin`:
    - **From an existing revision:** Captures the current revision UUID so the first `git drev put` on the new branch can hardlink-clone the parent snapshot on the remote storage host.
    - **From an unversioned commit or empty repository:** Records `00000000-0000-0000-0000-000000000000` (`ZeroUUID`), signaling that the first `put` will perform a full initial upload.
- **Dynamic completion:** Autocompletes existing branch names.

---

### `git drev get [flags] [<target_directory>]`
Downloads the revision directory snapshot corresponding to the current Git `HEAD` commit from the remote storage server into `workdir` (or an explicit `<target_directory>`).

- **Usage:** `git drev get [-l|--latest] [--stats] [<target_directory>]`
- **Arguments:** Optional `0..1` positional argument `<target_directory>` (resolved relative to the caller's working directory; defaults to `<repo-root>/<workdir>`).
- **Flags:**
  - `-l`, `--latest`: Fetch and hard-reset the active local branch to `origin/<branch>` before downloading the revision payload.
  - `--stats`: Append `--stats` to `rsync` to print transfer statistics.
- **Preconditions:** `HEAD` commit must carry valid `git-drev` revision metadata (not `ZeroUUID` or a branch creation marker).

---

### `git drev info`
Executes the project's `.git-drev-info-hook` script to display custom revision metadata for the current Git `HEAD` commit.

- **Usage:** `git drev info`
- **Arguments:** None
- **Flags:** None (inherits global flags)
- **Behavior:**
  - Parses `HEAD` commit metadata and injects `GIT_DREV_VERSION`, `GIT_DREV_UUID`, `GIT_DREV_SEQ`, `GIT_DREV_BRANCH`, `GIT_DREV_WORK_DIR`, and `GIT_DREV_ROOT`.
  - Executes `<repo-root>/.git-drev-info-hook` if present, streaming its output to `stdout`.
  - If no `.git-drev-info-hook` exists, logs `[git-drev] No info hook found` to `stderr` and exits `0`.

---

### `git drev name`
Outputs the short name of the current local Git branch to `stdout`, or fails if `HEAD` is detached.

- **Usage:** `git drev name`
- **Arguments:** None
- **Output:** Exact branch name string (e.g., `main\n`) to `stdout`; exits with code `4` (`ErrDetachedHead`) if `HEAD` is detached.

---

### `git drev put [flags] [<source_directory>]`
Uploads the local `workdir` (or `<source_directory>`) to the remote storage server using hardlink baseline cloning (`cp -al --`) and `rsync`, executes `.git-drev-put-hook` if present, and publishes a metadata commit to `origin`.

- **Usage:** `git drev put [-s|--sequence <uint64>] [--no-sequence-check] [--stats] [<source_directory>]`
- **Arguments:** Optional `0..1` positional argument `<source_directory>` (resolved relative to the caller's working directory; defaults to `<repo-root>/<workdir>`).
- **Flags:**
  - `-s`, `--sequence <uint64>`: Explicit monotonically increasing sequence number (`uint64` decimal string) stored in commit JSON (`{"uuid":"...","seq":"..."}`). Defaults to current Unix epoch seconds if omitted.
  - `--no-sequence-check`: Bypass sequence comparison when resolving concurrent stale pushes, forcing a rebase onto `origin/<branch>`.
  - `--stats`: Append `--stats` to `rsync` to print transfer statistics.
- **Behavior:**
  - Validates attached branch and index lock cleanliness.
  - Clones parent baseline via `cp -al --` on the remote host.
  - Uploads modified files via `rsync` (rejecting `--inplace`, `--append`, and `--append-verify` to protect hardlinked baselines).
  - Executes `.git-drev-put-hook` if present.
  - Stages repository root metadata (`git add --all`), commits JSON subject, and pushes to `origin` with `--force-with-lease`.
  - On concurrent push conflict (up to 5 attempts): drops older build via hard reset if `remoteSeq > localSeq`, or rebases with `--strategy-option=theirs`.

---

### `git drev select [<branch>|<tag>|<uuid>]`
Fetches from `origin` and checks out an existing target branch, tag, or specific historical revision commit resolved by UUID.

- **Usage:** `git drev select [<branch>|<tag>|<uuid>]`
- **Arguments:** Optional `0..1` argument. If omitted, falls back to the configured `branch.default` (`main`).
- **Features:**
  - **UUID resolution:** If argument matches RFC 4122 UUID format, scans repository history and checks out the commit hash matching that UUID (detaching `HEAD`).
  - **Fuzzy typo suggestions:** If a branch or tag name is mistyped, suggests the closest existing candidate within Levenshtein distance 3 (e.g., `Did you mean 'main'?`).
  - **Dynamic completion:** Autocompletes branch and tag names.

---

### `git drev update`
Fetches the latest refs from `origin` and hard-resets the active local branch to `origin/<branch>` (metadata only, without downloading the directory payload).

- **Usage:** `git drev update`
- **Arguments:** None
- **Preconditions:** `HEAD` must be attached to a local branch with a corresponding `origin/<branch>` remote tracking branch.

---

### `git drev usage`
Queries the remote storage host via SSH to inspect storage consumption and optionally correlate remote revision directories against local Git history.

- **Usage:** `git drev usage [-a|--all]`
- **Flags:**
  - `-a`, `--all`: Scans local Git history (`git log --all`) to correlate remote UUID directories against commits, branches, and tags, printing a 6-column table (`UUID`, `Size`, `Created`, `Commit`, `Branch/Tag`, `Message`) where unreferenced directories show `[orphaned]` in the `Commit` column.

---

### `git drev completion [bash|zsh|fish|powershell]`
Generates shell autocompletion scripts for supported interactive shells.

- **Usage:** `git drev completion [bash|zsh|fish|powershell]`
- **Integration examples:**
  - **Bash:** `source <(git-drev completion bash)`
  - **Zsh:** `source <(git-drev completion zsh)`
  - **Fish:** `git-drev completion fish | source`
  - **PowerShell:** `git-drev completion powershell | Out-String | Invoke-Expression`

---

## Exit code reference

`git-drev` adheres to standardized semantic exit codes:

| Exit Code | Category | Description |
| :---: | :--- | :--- |
| `0` | Success | Command completed successfully. |
| `1` | `ErrRuntime` | General unexpected runtime error. |
| `2` | `ErrUsage` | Invalid CLI syntax, unexpected arguments, or unsupported flags. |
| `3` | `ErrConfig` / `ErrNotRepository` | Missing or invalid `.git-drev.yaml`, or invoked outside a Git repository. |
| `4` | `ErrGitState` / `ErrDetachedHead` | Invalid Git state (detached `HEAD` on mutating command, uncommitted merge, or UUID not found). |
| `5` | `ErrGitExec` | Git plumbing subprocess failure (`git fetch`, `git checkout`, `git push`). |
| `6` | `ErrRemoteNotFound` | Configured remote host or target directory path cannot be resolved. |
| `7` | `ErrTransport` | SSH connection, `rsync` binary, or network transmission failure. |
| `130` | `ErrInterrupted` | Execution interrupted via `SIGINT` (Ctrl+C) or `SIGTERM`. |
