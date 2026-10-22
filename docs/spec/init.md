# Spec: git-drev init

## 1. Command Signature
- **Usage:** `git-drev init [flags]`
- **Arguments:** None.
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
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0), `ssh` (optional unless `--skip-check` is omitted).
- **Environment variables read:** `GIT_DIR`, `GIT_WORK_TREE`, `GIT_PREFIX`, `GIT_DREV_CONFIG`, `HOME`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from within an existing Git repository or from a completely uninitialized directory. All created or modified files (`.git-drev.yaml`, `.gitignore`) are positioned relative to the repository root.
- **Git state assertions:**
  - If executed outside a Git repository, prompts the user (or automatically initializes with `-y`) to run `git init -b <branch>`.
  - If executed inside an existing non-empty Git repository (one with existing commits or tracked files), emits a defensive warning that `git-drev` requires a dedicated metadata repository and requires user confirmation or `--force` before proceeding.

## 3. Core Execution Flow
1. **Argument & Context Verification:**
   - Verify no positional arguments were supplied (returns `ErrUsage` if extraneous arguments are passed).
   - Verify mutually exclusive flags (e.g. `--commit` and `--no-commit` together return `ErrUsage`).
2. **Repository Bootstrap & Defensive Guard:**
   - Check if current directory is inside a Git working tree (`checks.InsideWorkTree`).
   - If not a Git repository:
     - Prompt user: `Current directory is not a Git repository. Initialize a new dedicated git-drev metadata repository with 'git init'? [Y/n]`.
     - In non-interactive mode (`-y`), record requirement to initialize Git.
     - Note: actual `git init -b <branch>` execution is deferred until after the default branch name is chosen in step 4.
   - If inside an existing Git repository:
     - Check if the repository already contains commits (`git rev-parse --verify --quiet HEAD`) or tracked files (`git ls-files`).
     - If non-empty: emit warning that `git-drev` manages a dedicated metadata repository (not an add-on to source code repositories).
     - Require user confirmation (`[y/N]`) or `--force` (in `-y` mode without `--force`, abort with `ErrGitState`).
3. **Existing Configuration Check:**
   - Check if `.git-drev.yaml` already exists in repository root.
   - If present and `--force` is not set:
     - In `-y` mode, abort with `ErrConfig` and hint to pass `--force`.
     - In interactive mode, prompt user: `Configuration file already exists. Overwrite? [y/N]`.
4. **Configuration Data Collection:**
   - Inspect global configuration defaults from Tier 1 (`~/.config/git-drev/config.yaml`) and Tier 2 (`$GIT_DREV_CONFIG`).
   - Collect `host`: use flag `--host`, prompt interactively, or fall back to global default.
   - Collect `path`: use flag `--path`, prompt interactively with `/srv/drev/{repo}` or global default.
   - Collect `workdir`: use flag `--workdir`, prompt interactively with `data` or global default.
   - Collect `branch`: use flag `--branch`, prompt interactively with `main` or global default.
   - If deferred `git init` is pending, run `git init -b <branch>` now so the repository HEAD matches the selected branch name. If already an unborn repo, set symbolic-ref HEAD to refs/heads/<branch>.
   - Detect repository name (`DetectRepoName`) and validate configuration schema (`NormalizeAndValidate`).
5. **Remote Access Verification:**
   - Unless `--skip-check` is passed, probe remote SSH connectivity and permissions (`storage.Manager.CheckAccess`):
     - Checks if remote directory exists and whether it is readable/writable (`-d`, `-r`, `-w`, `-x`).
     - If missing: in interactive mode, prompts `Remote directory '<path>' does not exist on host '<host>'. Create it? [Y/n]`. If accepted (or in `-y` mode), runs `mkdir -p` on remote host.
     - If writable, logs `Remote storage verified (read-write access)`.
     - If read-only, logs `Warning: Remote storage path is read-only ('get' is available, 'put' requires write access)`.
6. **File Generation & Local Directory Preparation:**
   - Write `.git-drev.yaml` with clean, documented YAML formatting (including commented `# rsync:` examples and preserving `{repo}` template variable in the file if typed by the user).
   - Idempotently update `.gitignore` to ignore `/<workdir>`.
   - Ensure local working directory (`<workdir>/`) exists on disk (`mkdir -p`).
7. **Initial Commit:**
   - Unless `--no-commit` is specified, prompt to commit or auto-commit when `--commit` or `-y` is passed.
   - Stages `.git-drev.yaml` and `.gitignore` and creates commit: `chore: initialize git-drev configuration` (skips if working tree clean).
8. **Completion:**
   - If no remote `origin` is configured in Git, log helpful hint: `Hint: Add a Git remote when ready: git remote add origin <url>`.
   - Log `git-drev repository initialization complete` to `stderr`.
   - Exit with Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Repository initialized successfully. | None (operational logs on `stderr`). |
| `2` | `errcode.ErrUsage` | Positional arguments passed (`git drev init foo`). | `[git-drev] Error: unknown command "foo" for "git-drev init"` |
| `2` | `errcode.ErrUsage` | Mutually exclusive flags passed (`--commit` and `--no-commit`). | `[git-drev] Error: Flags --commit and --no-commit cannot be used together` |
| `2` | `errcode.ErrUsage` | Missing `--host` in non-interactive mode without global defaults. | `[git-drev] Error: Missing required flag --host in non-interactive mode` |
| `3` | `errcode.ErrNotRepository` | Outside Git repository and user declined `git init`. | `[git-drev] Error: Current directory is not a Git repository` |
| `3` | `errcode.ErrConfig` | Existing `.git-drev.yaml` without `--force` in non-interactive mode. | `[git-drev] Error: Configuration file already exists: <path>` |
| `3` | `errcode.ErrConfig` | Invalid configuration parameters (e.g. host starts with dash, relative path). | `[git-drev] Error: Invalid remote.host ...` |
| `4` | `errcode.ErrGitState` | Non-empty repository initialized in non-interactive mode without `--force`. | `[git-drev] Error: Repository already contains commits or tracked files` |
| `5` | `errcode.ErrGitExec` | Git initialization or commit failure. | `[git-drev] Error: Failed to initialize Git repository` |
| `7` | `errcode.ErrTransport` | Remote SSH host unreachable or authentication failure. | `[git-drev] Error: SSH check failed on host ...` |
| `7` | `errcode.ErrTransport` | Remote storage directory does not exist and cannot be created. | `[git-drev] Error: Remote directory ... does not exist and could not be created` |
| `130`| `errcode.ErrInterrupted` | Execution interrupted via `SIGINT` / `SIGTERM`. | `[git-drev] Error: Operation interrupted` |

## 5. Edge Cases & Behavioral Notes
- **Dedicated Metadata Repository Guard:** `git-drev` stores revision pointers as empty Git commits and uses Git branches as data streams. It is not designed to be run inside existing source repositories. `init` defensively warns users and prevents accidental initialization in existing repositories with commits or tracked files unless explicitly overridden with `--force`.
- **Read-Only vs. Read-Write Storage Mode:** Read access to the remote storage directory is strictly mandatory. Write access is optional: if the SSH user cannot write to the storage path, `init` issues a clear warning that the repository will operate in Consumer (read-only) mode where `get` works but `put` will fail until write permissions are granted.
- **Idempotent `.gitignore`:** If `/<workdir>` (or `<workdir>/`, `/<workdir>/`) is already ignored in `.gitignore`, `init` will not append duplicate entries. Newly added entries are formatted canonically as `/<workdir>`.
- **Preservation of `{repo}` Template:** If a user specifies `{repo}` or `{project}` in the path (e.g. `/srv/drev/{repo}`), the probe expands it to test permissions on the actual directory, but the literal template placeholder is preserved in `.git-drev.yaml` for portability.
- **Offline Initialization:** The `--skip-check` flag allows users or CI scripts to initialize repositories without active SSH network connectivity.
