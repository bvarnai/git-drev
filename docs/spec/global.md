# Global Technical Specification: git-drev

## 1. Binary & CLI Conventions
- **Binary name:** `git-drev` (invoked directly as `git-drev <subcommand> [flags] [args]` or via the standard Git plugin mechanism as `git drev <subcommand> [flags] [args]` when `git-drev` is on `$PATH`).
- **Global flags:**
  | Flag | Short | Type | Default | Description |
  | :--- | :--- | :--- | :--- | :--- |
  | `--verbose` | `-v` | `bool` | `false` | Enable verbose diagnostic logging and detailed command/transfer output (e.g., `rsync --itemize-changes` or detailed table breakdowns). Mutually exclusive with `--quiet`. |
  | `--quiet` | `-q` | `bool` | `false` | Suppress informational notices and hints, emitting only errors or strict porcelain output. Mutually exclusive with `--verbose`. |
  | `--no-color` | — | `bool` | `false` | Disable ANSI color styling on `stdout` and `stderr` regardless of TTY status. |
  | `--version` | — | `bool` | `false` | Print `git-drev version <semver> (<commit> <os>/<arch>)` to `stdout` and exit `0`. |
  | `--help` | `-h` | `bool` | `false` | Print usage and flag reference for `git-drev` or the active subcommand to `stdout` and exit `0`. |
- **Output protocols:**
  - **Standard output (`stdout`) conventions:**
    - Reserved strictly for primary command results, machine-parseable porcelain output (e.g., `git-drev name`, `git-drev info`, `git-drev usage` tables, `git-drev config`, `git-drev completion`), and requested `--help`/`--version` text.
    - When piped or redirected (non-TTY), tables and outputs omit decorative spinners or dynamic carriage-return progress bars (`\r`).
  - **Standard error (`stderr`) conventions:**
    - All operational progress logs, elapsed time summaries (`[git-drev] Took X min Y sec`), warnings, hints, and error diagnostics MUST be written to `stderr`.
    - Operational log format: `[git-drev] <message>` (suppressed when `--quiet` is active).
    - Warning format: `[git-drev] Warning: <message>`.
    - Error format: `[git-drev] Error: <message>` followed by optional `[git-drev] Hint: <actionable remediation>`.
  - **Terminal ergonomics & color handling:**
    - Clickable hyperlinks: Emits OSC 8 ANSI hyperlinks (`\033]8;;URL\033\TEXT\033]8;;\033\`) for web and CI URLs when color is enabled.
    - Word wrapping: Terminal width is dynamically inspected via `$COLUMNS` (fallback 80 columns) for hanging indentation formatting.
    - Color output is enabled by default only when the target stream (`stdout` or `stderr` evaluated independently via `os.File.Stat()` and `os.ModeCharDevice`) is an interactive TTY.
    - Color MUST be disabled if any of the following hold:
      1. `--no-color` flag is passed.
      2. `NO_COLOR` environment variable is present and non-empty (per https://no-color.org/).
      3. `GIT_DREV_COLOR=never` or `TERM=dumb`.
    - Setting `GIT_DREV_COLOR=always` forces ANSI color output even on non-TTY streams.

## 2. Environment & Runtime Context
- **Required host binaries:**
  - `git` (minimum version **2.25.0+**, required for modern `git switch`, `git status --porcelain=v2`, and reliable `--force-with-lease`).
  - `ssh` (OpenSSH client compatible with `-T` pseudo-terminal disablement and `~/.ssh/config` host aliases).
  - `rsync` (minimum version **3.1.0+** on client and remote host, required for `--info=progress2` and `--delete-during`).
  - **Zero external shell utility dependencies:** `jq`, `uuidgen`, `powershell.exe`, `python3`, `date`, `stat`, `du`, `sed`, `awk`, `grep`, `cut`, `tr`, `xargs`, and `rm` are **not required on the client host**; all client-side processing uses native Go 1.22+ standard library packages (`encoding/json`, `crypto/rand`, `time`, `os`, `path/filepath`, `regexp`, `strings`).
- **Environment variables read:**
  | Variable | Purpose | Default Fallback | Validation Rules |
  | :--- | :--- | :--- | :--- |
  | `GIT_DREV_CONFIG` | Explicit file path for shared global configuration overlay (Tier 2) | Search standard 3-tier configuration cascade | Must be a readable YAML file if provided. |
  | `GIT_DREV_COLOR` | Control ANSI color emission (`auto`, `always`, `never`) | `auto` | Case-insensitive enum: `auto`, `always`, `never`, `true`, `false`, `1`, `0`. Invalid values log a warning and fall back to `auto`. |
  | `GIT_DREV_SSH_COMMAND` | Override SSH executable or command prefix used by `git-drev` and `rsync -e` | `ssh -T` | Parsed via POSIX shell word-splitting rules; executable must exist in `$PATH` or as an absolute path. |
  | `GIT_DREV_HOOK_TIMEOUT` | Maximum execution duration for project hook scripts | `5m` | Parsed via `time.ParseDuration`; must be `> 0`. |
  | `NO_COLOR` | Standard CLI flag to disable ANSI colors when non-empty | `""` | Any non-empty string disables color unless overridden by `GIT_DREV_COLOR=always`. |
  | `GIT_DIR` | Standard Git variable specifying `.git` directory location | Discovered via `git rev-parse --git-dir` | Must point to a valid Git directory if set. |
  | `GIT_WORK_TREE` | Standard Git variable specifying working tree root | Discovered via `git rev-parse --show-toplevel` | Must point to an existing directory if set. |
  | `GIT_PREFIX` | Set by `git` binary when invoking `git-drev` from a subdirectory | `""` (worktree root) | Relative path from worktree root to caller's original `PWD`. |
  | `PATH` | System executable search path for `git`, `ssh`, `rsync`, and hooks | OS default | Standard OS path list (`filepath.SplitList`). |
- **Environment variables injected/propagated:**
  - Every child `git`, `ssh`, and `rsync` process spawned by `git-drev` inherits a sanitized environment built from `os.Environ()` with the following explicit overrides:
    - `LC_ALL=C` and `LANG=C`: Enforces deterministic, unlocalized ASCII output from `git`, `ssh`, and remote POSIX commands (critical for parsing `git push` stale-ref rejections and remote storage probes).
    - `GIT_TERMINAL_PROMPT=0`: Prevents `git fetch`/`git push` from hanging indefinitely on interactive credential prompts in CI/headless contexts unless interactive TTY mode is explicitly active.
    - `GIT_ADVICE=0`: Suppresses Git informational advice blocks (e.g., detached HEAD banners).
  - When executing project hooks (`.git-drev-put-hook`, `.git-drev-info-hook`), `git-drev` additionally injects:
    - `GIT_DREV_VERSION`: Current `git-drev` semantic version.
    - `GIT_DREV_UUID`: Active revision UUID (for `put` and `info` hooks).
    - `GIT_DREV_SEQ`: Active revision sequence string (for `put` and `info` hooks).
    - `GIT_DREV_BRANCH`: Current branch name (or `HEAD` if detached).
    - `GIT_DREV_WORK_DIR`: Absolute path to the configured `workdir`.
    - `GIT_DREV_ROOT`: Absolute path to the repository working tree root.
- **Git context & path resolution:**
  - **Repository validation assertions:**
    - Repository subcommands (`create`, `get`, `info`, `name`, `put`, `select`, `update`, `usage`) require a non-bare Git working tree (`git rev-parse --is-inside-work-tree` == `true`). Invoking inside a bare repository or inside the `.git/` directory itself fails immediately with `ErrNotRepository` (exit code `3`).
    - Subcommands that mutate branch state or require an upstream tracking branch (`put`, `update`) assert that `HEAD` is a symbolic reference (`git symbolic-ref -q HEAD`). If `HEAD` is detached, they fail with `ErrDetachedHead` (exit code `4`) and emit the remediation hint: `Use "git-drev select <branch>" to select a branch first.`
  - **Handling of `GIT_PREFIX`, `GIT_DIR`, and `GIT_WORK_TREE`:**
    - When invoked as `git drev <subcommand>` from a subdirectory, Git changes the process `PWD` to the top-level working tree root and sets `GIT_PREFIX` to the caller's original relative subdirectory (with trailing slash).
    - When invoked directly as `git-drev <subcommand>` from a subdirectory, `GIT_PREFIX` is unset. `git-drev` MUST normalize both invocation modes at startup by querying `git rev-parse --show-toplevel` and `git rev-parse --show-prefix`, computing `CallerDir = filepath.Join(RepoRoot, Prefix)`, and chdir'ing the process to `RepoRoot` after resolving user-supplied paths.
  - **Working directory enforcement:**
    - Repository metadata files (`.git-drev.yaml`, `.git-drev-put-hook`, `.git-drev-info-hook`) and the configured `workdir` (default `data`) ALWAYS resolve relative to `RepoRoot`.
    - User-supplied CLI path arguments (`<target_directory>` in `get`, `<source_directory>` in `put`) ALWAYS resolve relative to `CallerDir` (the user's `PWD` at invocation time) before changing directory to `RepoRoot`.
  - **Built-in help integration:**
    - When a user runs `git drev --help` or `git help drev`, the `git` wrapper binary intercepts `--help` before invoking `git-drev` and attempts to display the `git-drev(1)` man page (`man git-drev`).
    - Passing `-h` (`git drev -h` or `git drev <subcommand> -h`), `git drev help [<subcommand>]`, or invoking the binary directly (`git-drev --help` / `git-drev <subcommand> --help`) bypasses Git's man-page interceptor and prints the built-in CLI help directly to `stdout` with exit code `0`.

## 3. Standard Exit Code Matrix
All subcommands map errors to standard exit codes using `errors.Is` against sentinel error categories defined in package `drev/internal/errcode`:

| Exit Code | Go Error Category | Semantic Meaning | Common Triggers |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Success | Command completed cleanly; `--help` or `--version` displayed; `put` dropped older revision due to newer sequence on remote. |
| `1` | `ErrRuntime` | General Runtime Error | Unhandled I/O error, filesystem permission failure, hook script non-zero exit. |
| `2` | `ErrUsage` | CLI Usage / Validation Error | Unknown flag, missing mandatory argument (`<branch>`), mutually exclusive flags (`-v` and `-q`), invalid integer/sequence format. |
| `3` | `ErrConfig` / `ErrNotRepository` | Repository or Configuration Error | Not inside a Git working tree, missing or malformed `.git-drev.yaml`, missing required `remote.host` or `remote.path` keys. |
| `4` | `ErrGitState` / `ErrDetachedHead` | Invalid Git State | Detached `HEAD` during `put` or `update`, missing revision UUID in `HEAD` commit message, empty branch marker encountered on `get`, target UUID/branch not found in history. |
| `5` | `ErrGitExec` | Git Subprocess Failure | `git fetch`, `git checkout`, `git switch`, `git commit`, `git reset`, `git rebase`, or non-stale `git push` failure. |
| `6` | `ErrRemoteNotFound` | Remote Revision Missing | Target revision directory `<path>/<uuid>` does not exist on remote server during `get` or remote base path missing during `usage`. |
| `7` | `ErrTransport` | SSH / Rsync Transport Error | SSH connection failure, host key verification error, `rsync` transfer failure, or missing `ssh`/`rsync`/`git` binary on `$PATH`. |
| `130` | `ErrInterrupted` | Process Interrupted (`SIGINT` / `SIGTERM`) | Context canceled via `Ctrl+C` (`SIGINT`) or `SIGTERM` during execution. |

## 4. External Git Execution Boundary & Safety
- **Git invocation standards:**
  - Always prefer deterministic Git plumbing commands over porcelain commands:
    - Repository root & prefix: `git rev-parse --show-toplevel`, `git rev-parse --show-prefix`, `git rev-parse --git-dir`.
    - Detached HEAD check: `git symbolic-ref --quiet --short HEAD` (instead of parsing `git branch` or `git rev-parse --abbrev-ref HEAD` which ambiguously outputs `"HEAD"`).
    - Branch existence (local): `git show-ref --verify --quiet refs/heads/<branch>`.
    - Branch existence (remote): `git ls-remote --exit-code --heads origin refs/heads/<branch>`.
    - Ref enumeration: `git for-each-ref --format=%(refname)%00%(objectname) -z refs/heads/ refs/remotes/` (using NUL-byte `-z` / `%00` delimiters to prevent whitespace/newline injection).
    - Commit log scanning: `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix` (NUL-delimited fields and records so commit subjects containing pipes `|` or newlines cannot corrupt parsing).
  - Always pass `--` before positional ref or path arguments where supported to prevent leading-dash argument injection (e.g., branch names starting with `-`).
  - Always enforce `LC_ALL=C` in subprocess environments.
- **Preflight safety assertions (`internal/checks`):**
  - Standardized preflight integrity checks protect repository and command state prior to execution:
    - **`InsideWorkTree`:** Asserts the command is invoked inside a valid non-bare Git repository and returns normalized `RepoRoot` and `Prefix`.
    - **`AttachedBranch`:** Asserts `HEAD` is attached to a concrete local branch (enforced on `put` and `update`; detached `HEAD` fails with `errcode.ErrDetachedHead`).
    - **`NoIndexLock`:** Asserts `.git/index.lock` is not present, failing early with actionable remediation if another Git process is running.
    - **`RemoteConfigured`:** Asserts remote host and remote path are non-empty in configuration prior to executing network commands.
    - **`NoActiveOperation`:** Asserts no interactive Git operation (merge, rebase, cherry-pick, revert) is currently active.
- **Fuzzy typo suggestions (`internal/suggest`):**
  - When user-supplied branch, tag, or subcommand arguments fail lookup, the CLI computes Levenshtein edit distance against candidate names within max distance `3`.
  - On match, enriches `errcode.AppError` with `[git-drev] Hint: Did you mean '<candidate>'?`.
- **Interruption & signal handling:**
  - `main()` initializes a root `context.Context` via `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`.
  - All external commands (`exec.CommandContext`) bind to this context. On cancellation:
    - Child `ssh`, `rsync`, or `git` processes receive `SIGTERM` (followed by `SIGKILL` after a 3-second grace period via `cmd.WaitDelay` / `cmd.Cancel`).
    - If `put` is interrupted during remote baseline copy (`cp -al --`) or `rsync` upload before the metadata commit is pushed, a deferred cleanup handler attempts a best-effort remote removal of the incomplete `<path>/<uuid>` staging directory.
    - If `put` fails or is interrupted during a `git rebase` in the stale-push retry loop, `git rebase --abort` is executed via `defer` to restore the repository to a clean state.

## 5. Persistent Storage, Config Schema & Shared Domain Models
- **Metadata & storage format:**
  - **Configuration Cascade & Schema (`.git-drev.yaml`):**
    - Resolved across a 3-tier YAML cascade (`internal/config`):
      1. **Tier 1 (User Global):** `~/.config/git-drev/config.yaml`, `~/.config/git-drev/config.yml`, `~/.git-drev.yaml`, `~/.git-drev.yml`, `~/.git-drev/config.yaml`, `~/.git-drev/config.yml`.
      2. **Tier 2 (Shared Global):** File path specified by `$GIT_DREV_CONFIG`.
      3. **Tier 3 (Repository Local):** `<repo-root>/.git-drev.yaml`, `<repo-root>/.git-drev.yml`, `<repo-root>/.git-drev/config.yaml`, `<repo-root>/.git-drev/config.yml`.
    - Schema:
      ```yaml
      workdir: data
      branch:
        default: main
      remote:
        host: drev-server
        path: /var/drev/myproject
        rsync:
          get: -c
          put: -c
      ```
    - Validation rules:
      - `remote.host` (required, non-empty string): SSH host alias or hostname; must not start with `-` (prevents SSH option injection).
      - `remote.path` (required, non-empty string): Absolute POSIX path on remote host; trailing slashes are stripped automatically; must not be `/` or contain shell metacharacters (`` ` ``, `$`, `;`, `&`, `|`, `\n`, `\x00`). Supports `{repo}` and `{project}` template placeholders (e.g. `/var/drev/{repo}`) which expand to the repository name (derived from `origin` remote URL slug or working tree basename). To guarantee repository storage isolation, if `remote.path` is sourced from global configuration (Tier 1 or Tier 2) and not explicitly overridden in repository configuration (Tier 3), `git-drev` automatically appends `/{repo}` if no placeholder is present.
      - `workdir` (optional, default `"data"`): Relative path within `<repo-root>`; must not escape `<repo-root>` (`..` traversal rejected).
      - `branch.default` (optional, default `"main"`): Default branch name for `select` when no argument is provided.
      - `remote.rsync.get` / `remote.rsync.put` (optional, default `""`): Space-separated custom `rsync` flags appended after implicit flags (`-a --delete-during` and verbosity/progress flags) so user flags take precedence. In `remote.rsync.put`, flags that mutate destination inodes in-place (`--inplace`, `--append`, `--append-verify`) are rejected with `errcode.ErrConfig` to prevent corrupting hardlinked (`cp -al`) baseline revisions.
  - **Commit Message Metadata Schema:**
    - Every `git-drev` revision commit stores a compact single-line JSON object as the commit subject (`%s`):
      - Standard revision commit (`put`): `{"uuid":"<uuid-v4>","seq":"<uint64-string>"}`
      - Branch creation marker commit (`create`): `{"uuid":"<parent-uuid-or-zero-uuid>","marker":"true"}`
      - Zero UUID constant for initial branches with no parent revision: `"00000000-0000-0000-0000-000000000000"`.
      - UUID format: Lowercase RFC 4122 UUID v4 (`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).
      - Sequence (`seq`): Unsigned 64-bit decimal integer serialized as a JSON string (defaults to Unix epoch seconds `time.Now().Unix()` if `--sequence` is not provided).
  - **Project Hook Scripts:**
    - `.git-drev-put-hook` (located at `<repo-root>/.git-drev-put-hook`, permissions `0755` or executed via `sh` / `bash`): Invoked by `put` after `rsync` completes and before `git add --all` / `git commit`.
    - `.git-drev-info-hook` (located at `<repo-root>/.git-drev-info-hook`, permissions `0755` or executed via `sh` / `bash`): Invoked by `info` to print custom build metadata to `stdout`.
    - Hook execution contract: `git-drev` supports both standalone executable files (`#!<interpreter>` with `0755` mode) and shell scripts defining `put_hook` / `info_hook` functions (executed via `bash -c 'source "$1"; if declare -F <hook_fn> >/dev/null; then <hook_fn>; fi' -- <hook-path>`).
  - **Remote Storage Layout:**
    - Remote revisions are stored on `<remote.host>` as flat subdirectories under `<remote.path>/<uuid>/` with mode `0755` (preserving uploaded payload permissions via `rsync -a`).
- **Target Go package layout:**
  ```text
  git-drev/
  ├── cmd/
  │   └── git-drev/
  │       └── main.go              # Entrypoint, signal context, exit code mapping
  ├── internal/
  │   ├── cli/                     # Root dispatcher, global flags, subcommand handlers
  │   │   ├── root.go
  │   │   ├── config.go            # git-drev config
  │   │   ├── create.go            # git-drev create
  │   │   ├── get.go               # git-drev get
  │   │   ├── info.go              # git-drev info
  │   │   ├── name.go              # git-drev name
  │   │   ├── put.go               # git-drev put
  │   │   ├── selectcmd.go         # git-drev select
  │   │   ├── update.go            # git-drev update
  │   │   └── usage.go             # git-drev usage
  │   ├── config/                  # 3-tier YAML config loader, merger, provenance report & validator
  │   │   ├── loader.go
  │   │   ├── merge.go
  │   │   └── report.go
  │   ├── domain/                  # Shared value types (ProjectConfig, RevisionMeta, RemoteRevision)
  │   │   ├── config.go
  │   │   └── domain.go
  │   ├── errcode/                 # Sentinel errors and exit code mapping
  │   ├── git/                     # GitRunner interface & plumbing implementation
  │   │   ├── runner.go            # Runner interface and subprocess execution core
  │   │   ├── inspection.go        # Read-only plumbing queries (refs, log, rev-parse)
  │   │   └── operations.go        # Mutating Git operations (fetch, checkout, commit, push)
  │   ├── hook/                    # Hook runner for .git-drev-put-hook and .git-drev-info-hook
  │   ├── storage/                 # Remote revision storage manager (Manager, Client, usage discovery)
  │   ├── term/                    # Structured stderr logger, TTY & NO_COLOR detection, timer
  │   └── transport/               # SSHRunner and RsyncRunner interfaces & implementations
  ├── scripts/
  │   └── cleanup.sh               # Reference retention policy and remote storage cleanup script
  └── go.mod
  ```
  - **Package dependency boundaries:**
    - `internal/domain` and `internal/errcode` depend only on the Go standard library.
    - `internal/config`, `internal/git`, `internal/transport`, `internal/storage`, and `internal/hook` depend on `internal/domain`, `internal/errcode`, and `internal/term`, never on `internal/cli`.
    - `internal/cli` wires flags and invokes domain/runner interfaces, enabling 100% unit testability with mock runners.
- **Shared Go interfaces & structs:**
  ```go
  package domain

  import "time"

  // ProjectConfig defines the unified application configuration.
  type ProjectConfig struct {
      WorkDir string       `yaml:"workdir,omitempty"`
      Branch  BranchConfig `yaml:"branch,omitempty"`
      Remote  RemoteConfig `yaml:"remote,omitempty"`
  }

  // BranchConfig defines branch configuration settings.
  type BranchConfig struct {
      Default string `yaml:"default,omitempty"`
  }

  // RemoteConfig defines the SSH/rsync storage backend parameters.
  type RemoteConfig struct {
      Host  string      `yaml:"host,omitempty"`
      Path  string      `yaml:"path,omitempty"`
      Rsync RsyncConfig `yaml:"rsync,omitempty"`
  }

  // RsyncConfig defines per-direction custom rsync arguments.
  type RsyncConfig struct {
      Get string `yaml:"get,omitempty"`
      Put string `yaml:"put,omitempty"`
  }

  // ZeroUUID is the placeholder UUID used on empty branch marker commits.
  const ZeroUUID = "00000000-0000-0000-0000-000000000000"

  // RevisionMeta represents the JSON payload stored in a git-drev commit subject.
  type RevisionMeta struct {
      UUID   string `json:"uuid"`
      Seq    string `json:"seq,omitempty"`
      Marker string `json:"marker,omitempty"`
  }

  // IsMarker reports whether the commit is an empty branch creation marker.
  func (m RevisionMeta) IsMarker() bool {
      return m.Marker == "true"
  }

  // RemoteRevision represents a revision directory discovered on remote storage or local disk.
  type RemoteRevision struct {
      UUID       string
      SizeHuman  string
      SizeBytes  int64
      ModTime    time.Time
      CommitHash string
      ShortHash  string
      CommitDate time.Time
      Refs       []string
      Meta       RevisionMeta
      RawSubject string
      Orphaned   bool
  }
  ```
  ```go
  package git

  import (
      "context"
      "io"
      "github.com/bvarnai/git-drev/internal/domain"
  )

  // CommitRecord represents a parsed Git commit from git log.
  type CommitRecord struct {
      Hash      string
      ShortHash string
      Timestamp int64
      Refs      []string
      Subject   string
      Meta      *domain.RevisionMeta
  }

  // Runner represents the direct Git execution boundary interface.
  type Runner interface {
      // Run executes a git command in dir and returns combined stdout, or an error.
      Run(ctx context.Context, dir string, args ...string) (string, error)
      // RunLines executes a git command in dir and returns stdout split into lines.
      RunLines(ctx context.Context, dir string, args ...string) ([]string, error)
      // RunWithEnv executes a git command in dir with additional environment variables.
      RunWithEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error)
      // RunStream executes a git command streaming stdout and stderr to the provided writers.
      RunStream(ctx context.Context, dir string, stdout, stderr io.Writer, extraEnv []string, args ...string) error
  }

  // Inspector provides read-only repository inspection methods using Git plumbing.
  type Inspector struct {
      runner Runner
  }

  // Operations provides safe Git mutating methods.
  type Operations struct {
      runner Runner
  }
  ```
  ```go
  package transport

  import (
      "context"
      "io"
  )

  // SSHRunner executes remote commands over SSH.
  type SSHRunner interface {
      Run(ctx context.Context, host, cmd string) (stdout string, stderr string, err error)
      RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (stderr string, err error)
  }

  // RsyncRunner executes rsync synchronization.
  type RsyncRunner interface {
      Sync(ctx context.Context, opts RsyncOptions) error
  }
  ```
  ```go
  package storage

  import (
      "context"
      "github.com/bvarnai/git-drev/internal/domain"
  )

  // Manager abstracts high-level remote storage operations.
  type Manager interface {
      HasRevision(ctx context.Context, host, basePath, uuid string) (bool, error)
      CloneBaseline(ctx context.Context, host, basePath, srcUUID, dstUUID string) error
      RemoveRevision(ctx context.Context, host, basePath, uuid string) error
      QueryUsage(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (totalSize string, revisions []domain.RemoteRevision, err error)
      Download(ctx context.Context, req TransferRequest) error
      Upload(ctx context.Context, req TransferRequest) error
  }
  ```

## 6. Subcommand Registry
All subcommands are dispatched from the single `git-drev` binary:

| Subcommand | Target Go Package | Core Responsibility |
| :--- | :--- | :--- |
| `create` | `internal/cli` (`create.go`) | Creates a new revision metadata branch with an initial marker commit (or switches to it if it already exists locally/remotely) and pushes it to `origin`. |
| `get` | `internal/cli` (`get.go`) | Synchronizes the local working directory (or `<target_directory>`) with the remote directory revision referenced by `HEAD` (optionally updating to `--latest` first). |
| `put` | `internal/cli` (`put.go`) | Uploads local directory contents to a new remote UUID directory (cloning the previous revision as a server-side baseline when possible), runs `.git-drev-put-hook`, commits metadata with sequence number, and pushes with optimistic concurrency retry. |
| `select` | `internal/cli` (`selectcmd.go`) | Selects and checks out a target branch, tag, or specific revision commit resolved by UUID (falling back to `branch.default` when omitted). |
| `update` | `internal/cli` (`update.go`) | Fetches all remote changes and hard-resets the current active branch to `origin/<branch>`. |
| `info` | `internal/cli` (`info.go`) | Executes the repository's `.git-drev-info-hook` to display project-specific revision metadata. |
| `name` | `internal/cli` (`name.go`) | Prints the current local branch name (or detached `HEAD` status) to `stdout`. |
| `usage` | `internal/cli` (`usage.go`) | Queries the remote host via SSH for total storage consumption and (`-a` / `-v`) correlates remote UUID directories against local Git history to display active vs. `[orphaned]` revisions. |
| `config` | `internal/cli` (`config.go`) | Prints the effective resolved cascading configuration and source provenance in YAML format. |
| `completion` | `internal/cli` (`completion.go`) | Generates shell autocompletion scripts for `bash`, `zsh`, `fish`, and `powershell` with dynamic branch/tag completion. |
