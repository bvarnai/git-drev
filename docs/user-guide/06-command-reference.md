# Chapter 6: Command reference

This chapter provides a complete reference for all `git-drev` subcommands, arguments, and flags.

---

## Global flags

The following flags are accepted by the root `git-drev` command and all subcommands:

| Flag | Short | Type | Description |
| :--- | :--- | :--- | :--- |
| `--verbose` | `-v` | `bool` | Enable verbose diagnostic logging (`[git-drev] [debug]`) and detailed command/transfer output. Mutually exclusive with `--quiet` |
| `--quiet` | `-q` | `bool` | Suppress informational progress and status logs, emitting only errors or strict machine-parseable output. Mutually exclusive with `--verbose` |
| `--no-color` | — | `bool` | Disable ANSI color styling on `stdout` and `stderr` regardless of TTY status |
| `--version` | — | `bool` | Print semantic version, commit hash, and build information to `stdout` and exit `0` |
| `--help` | `-h` | `bool` | Print usage information and flag descriptions to `stdout` and exit `0` |

---

## Subcommands

### `git drev config`
Inspects and prints the effective resolved configuration across the 3-tier YAML cascade, including provenance origin for each key.

- **Usage:** `git drev config`
- **Output:** Clean YAML output to `stdout`
- **Preconditions:** Must be run inside a valid Git repository containing `.git-drev.yaml`

---

### `git drev create <branch>`
Creates a new revision branch with an initial marker commit carrying the parent revision's UUID (or `ZeroUUID`) and pushes it to `origin`.

- **Usage:** `git drev create <branch>`
- **Arguments:** Exactly 1 argument specifying the target branch name
- **Behavior:**
  - If `<branch>` exists locally, checks it out idempotently
  - If `<branch>` exists on `origin`, creates a tracking branch and switches to it
  - If new, records an initial marker commit (`seq: 0`) and pushes to `origin`
- **Dynamic completion:** Autocompletes existing branch names

---

### `git drev get`
Downloads the revision directory snapshot corresponding to the current Git `HEAD` commit from the remote storage server into `workdir`.

- **Usage:** `git drev get`
- **Arguments:** None
- **Preconditions:** `HEAD` commit must carry valid `git-drev` revision metadata

---

### `git drev info`
Displays formatted revision metadata for the current Git `HEAD` commit.

- **Usage:** `git drev info [flags]`
- **Flags:**
  - `-s`, `--short`: Print only the revision sequence number to `stdout`
  - `--uuid`: Print only the raw 36-character UUID string to `stdout`
- **Hooks:** Executes `.git-drev-info-hook` if present, appending its output

---

### `git drev name`
Outputs the name of the current local Git branch to `stdout`, or reports detached `HEAD` status.

- **Usage:** `git drev name`
- **Arguments:** None
- **Output:** Exact branch name string (e.g. `main\n`)

---

### `git drev put`
Uploads the local `workdir` to the remote storage server using hardlink baseline cloning and rsync, and creates a metadata commit on the current branch.

- **Usage:** `git drev put [-m <message>]`
- **Flags:**
  - `-m`, `--message <text>`: Revision description message stored in the commit metadata.
- **Behavior:**
  - Validates attached branch and index lock cleanliness
  - Clones parent baseline via `cp -al` on remote host
  - Uploads modified files via `rsync`
  - Executes `.git-drev-put-hook` if present
  - Pushes metadata commit to `origin` with `--force-with-lease`
  - Retries with linear rebase if concurrent pushes occurred on upstream

---

### `git drev select [<branch>|<tag>|<uuid>]`
Checks out an existing target branch, tag, or specific historical revision commit resolved by UUID.

- **Usage:** `git drev select [<branch>|<tag>|<uuid>]`
- **Arguments:** Optional `0..1` argument. If omitted, falls back to the configured `branch.default` (`main`)
- **Features:**
  - **UUID resolution:** If argument matches UUID format, scans repository history and checks out the commit hash matching that UUID
  - **Fuzzy typo suggestions:** If a branch name is mistyped, suggests the closest existing branch/tag (e.g., `Did you mean 'main'?`)
  - **Dynamic completion:** Autocompletes branch and tag names

---

### `git drev update`
Fetches the latest commits from `origin` for the active branch, fast-forwards the local branch, and downloads the updated directory snapshot into `workdir`.

- **Usage:** `git drev update`
- **Arguments:** None
- **Equivalent to:** `git pull` followed by `git drev get`

---

### `git drev usage`
Queries the remote storage host via SSH to inspect storage consumption.

- **Usage:** `git drev usage [-v]`
- **Flags:**
  - `-v`, `--verbose`: Scans local Git history to correlate remote UUID directories against active branches/tags, displaying active versus `[orphaned]` status for each directory

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
| `0` | Success | Command completed successfully |
| `1` | `ErrRuntime` | General unexpected runtime error |
| `2` | `ErrUsage` | Invalid CLI syntax, unexpected arguments, or unsupported flags |
| `3` | `ErrConfig` / `ErrNotRepository` | Missing or invalid `.git-drev.yaml`, or invoked outside a Git repository |
| `4` | `ErrGitState` / `ErrDetachedHead` | Invalid Git state (detached HEAD on mutating command, uncommitted merge, or UUID not found) |
| `5` | `ErrGitExec` | Git plumbing subprocess failure (`git fetch`, `git checkout`, `git push`) |
| `6` | `ErrRemoteNotFound` | Configured remote host or target directory path cannot be resolved |
| `7` | `ErrTransport` | SSH connection, rsync binary, or network transmission failure |
| `130` | `ErrInterrupted` | Execution interrupted via `SIGINT` (Ctrl+C) or `SIGTERM` |
