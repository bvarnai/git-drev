# Subcommand Technical Specification: `git-drev usage`

## 1. Subcommand Overview & CLI Contract
- **Invocation signatures:**
  - Via Git plugin: `git drev usage [flags]`
  - Direct binary: `git-drev usage [flags]`
- **Target Go package:** `internal/cli` (`usage.go`)
- **Summary:** Queries the configured remote storage host via SSH for total disk consumption and revision directory inventory, and when verbose mode (`-v` / `--verbose`) is enabled, correlates remote UUID directories against local Git commit history to display an itemized breakdown of active versus `[orphaned]` revisions.

## 2. Arguments, Flags & Preconditions
- **Subcommand-specific flags:**
  | Flag | Short | Go Type | Default | Required | Description |
  | :--- | :--- | :--- | :--- | :--- | :--- |
  | *(None)* | — | — | — | — | `usage` accepts no subcommand-specific flags. Detailed revision breakdown table output is controlled via the global `--verbose` (`-v`) flag defined in `docs/spec/global.md`. |

- **Positional arguments:**
  - **Cardinality:** `0` (no positional arguments accepted).
  - **Path resolution behavior:** Not applicable (`usage` operates strictly against repository-level configuration and remote SSH storage paths).
  - **Validation rules:** Passing one or more positional arguments (instead of silently discarding them after logging as in legacy `drs::common::no_args`) is rejected immediately at CLI parse time with `errcode.ErrUsage` (exit code `2`). Passing `git drev usage -h` or `git-drev usage --help` prints subcommand help to `stdout` and exits `0`.

- **Preconditions & state assertions:**
  1. **Mutually exclusive global flags:** `--verbose` (`-v`) and `--quiet` (`-q`) must not be combined (`errcode.ErrUsage`, exit code `2`).
  2. **Non-bare Git working tree:** The caller must be inside a valid, non-bare Git working tree (`git rev-parse --is-inside-work-tree == "true"`). Invoking outside a Git repository or inside a bare repository fails with `errcode.ErrNotRepository` (exit code `3`).
  3. **Valid configuration:** Resolved configuration across the 3-tier YAML cascade (`~/.config/git-drev/config.yaml`, `$GIT_DREV_CONFIG`, `<repo-root>/.git-drev.yaml`) must define valid non-empty `remote.host` (not starting with `-`) and `remote.path` (absolute POSIX path, not `/`, free of shell metacharacters) values (`errcode.ErrConfig`, exit code `3`).
  4. **Detached `HEAD` state permitted:** `usage` is a read-only inspection subcommand across all refs (`--all`) and remote storage; it does **not** require `HEAD` to be a symbolic reference.

## 3. Subcommand Domain Models & Signatures
```go
package cli

import (
	"context"
	"io"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/bvarnai/git-drev/internal/transport"
)

// UsageOptions encapsulates the validated parameters for executing the usage command.
type UsageOptions struct {
	Config  domain.ProjectConfig
	All     bool
	Verbose bool
	Quiet   bool
}

// UsageRow represents a single formatted row in the verbose revisions breakdown table.
type UsageRow struct {
	UUID       string
	SizeHuman  string
	Created    string
	Commit     string
	BranchTag  string
	Message    string
	Orphaned   bool
}

// UsageResult contains the structured outcome of a remote storage usage query.
type UsageResult struct {
	Host            string
	Path            string
	TotalSize       string
	RevisionCount   int
	Revisions       []domain.RemoteRevision
	Rows            []UsageRow
}

// UsageService coordinates remote storage probing and local Git commit correlation.
type UsageService struct {
	Insp   *git.Inspector
	Ops    *git.Operations
	Storage storage.UsageScanner
	Logger *term.Logger
	Stdout io.Writer
}

// Run executes the usage business logic and writes the formatted report to u.Stdout.
func (u *UsageService) Run(ctx context.Context, opts UsageOptions) (*UsageResult, error)

// CorrelateRevisions matches discovered remote UUID directories against local Git history.
func CorrelateRevisions(revisions []domain.RemoteRevision, commits []git.CommitRecord) ([]domain.RemoteRevision, []UsageRow)
```

## 4. Execution Flow (Step-by-Step Domain Logic)
1. **Input normalization & validation:**
   - Reject any positional arguments with `errcode.ErrUsage` (exit code `2`).
   - Verify global flag constraints (e.g., `--verbose` and `--quiet` are not both set).
   - Resolve `RepoRoot` and `Prefix` via `Insp.RepoRoot(ctx, dir)` (`git rev-parse --show-toplevel` and `--show-prefix`). If not inside a non-bare working tree, return `errcode.ErrNotRepository` (exit code `3`).
   - Change the process working directory to `RepoRoot`.
   - Load and validate `*domain.ProjectConfig` via `config.Load(repoRoot)`. Strip any trailing slashes from `Config.Remote.Path`. If `Remote.Host` or `Remote.Path` is missing or invalid, return `errcode.ErrConfig` (exit code `3`).

2. **Core algorithm:**
   - **Step 2.1: Ensure remote fetch configuration:**
     - Invoke `Ops.EnsureRemoteFetchConfig(ctx, repoRoot)` to idempotently ensure `+refs/heads/*:refs/remotes/origin/*` is present in local `.git/config` without destructive unsetting or network calls.
   - **Step 2.2: Query remote storage via SSH:**
     - Emit operational progress log to `stderr` (unless `--quiet` is active):
       `[git-drev] Connecting to remote host '<host>' to retrieve storage usage...`
     - Invoke `Storage.QueryUsage(ctx, opts.Config.Remote.Host, opts.Config.Remote.Path)`.
     - Under the hood, `QueryUsage` executes a single non-interactive SSH command (`ssh -T <host> /bin/sh -s -- <quoted-path>`) with `LC_ALL=C` and feeds a POSIX shell script on `stdin` that:
       1. Checks `[ -d "$1" ]`; if missing, prints `ERROR:Remote path '$1' does not exist.` and exits with status `66` (or dedicated sentinel parsed into `errcode.ErrRemoteNotFound`, exit code `6`).
       2. Computes human-readable total directory usage via `du -sh -- "$1" 2>/dev/null | cut -f1` and emits `TOTAL_SIZE:<size>` (defaulting to `"-"` if empty).
       3. Iterates safely over immediate child entries of `"$1"` (handling empty directories cleanly without `nullglob` errors by testing `[ -d "$d" ]`), extracts `name=$(basename -- "$d")`, validates `name` against case-insensitive RFC 4122 UUID format (`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`), queries `du -sh -- "$d"` and modification time (`stat -c %Y -- "$d"` on GNU/Linux or `stat -f %m -- "$d"` on BSD/macOS, falling back to `0`), and emits `REV:<uuid>:<size>:<mtime_epoch>`.
     - If the SSH transport fails for reasons other than the missing remote path sentinel, return `errcode.ErrTransport` (exit code `7`) including the captured remote output in the error diagnostic.
   - **Step 2.3: Render storage summary:**
     - Emit operational log to `stderr` (unless `--quiet`):
       `[git-drev] Remote storage usage for host:<host> path:<path>`
     - Write the summary block to `stdout`:
       ```text

       Summary:
         Total space used:  <total_size>

       ```
     - If `len(revisions) == 0`:
       - Emit to `stderr` (unless `--quiet`): `[git-drev] No revision directories found on remote storage.`
       - Return `UsageResult` with `nil` error (exit code `0`).
     - If `!opts.All && !opts.Verbose`:
       - Emit to `stderr` (unless `--quiet`):
         `[git-drev] Found <N> revision directory(ies) on remote storage.`
         `[git-drev] Hint: Use -a/--all to view the detailed breakdown of revisions.`
       - Return `UsageResult` with `nil` error (exit code `0`).
   - **Step 2.4: Correlate with local Git commit history (`--all` or `--verbose`):**
     - Invoke `Insp.ScanAllCommits(ctx, repoRoot)` which executes `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix`.
     - Build a lookup map `byUUID map[string]git.CommitRecord` keyed by lowercase UUID string:
       - For each `CommitRecord`:
         1. Attempt to unmarshal `record.Subject` as JSON into `domain.RevisionMeta`. If valid JSON and `meta.UUID != ""` (and `meta.UUID != domain.ZeroUUID` and `!meta.IsMarker()`), use `strings.ToLower(meta.UUID)`.
         2. Fallback for legacy/non-standard commit subjects: extract the first substring matching `(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`.
         3. Normalize the extracted UUID to lowercase. If `byUUID[uuid]` does not already exist, insert `record` (preserving the most recent commit encountered in `git log` order).
     - For each discovered remote revision `rev` (in the deterministic lexical order returned by the remote shell):
       - Format `Created`: if `rev.ModTime.IsZero()` (epoch `0`), set `Created = "-"`; otherwise format `rev.ModTime.Local()` as `"2006-01-02 15:04:05"`.
       - Look up `strings.ToLower(rev.UUID)` in `byUUID`:
         - **If not found (orphaned):**
           - `rev.Orphaned = true`
           - `Commit = "[orphaned]"`
           - `BranchTag = "-"`
           - `Message = "-"`
         - **If found (active):**
           - `rev.Orphaned = false`
           - `Commit = record.ShortHash`
           - Clean and format `record.Refs`: strip `"HEAD -> "` prefixes, strip `"tag: "` prefixes, trim whitespace on each comma-separated token, filter out `"origin/HEAD"` and empty strings, and join remaining tokens with `", "`. If no tokens remain, set `BranchTag = "-"`.
           - Format `Message`: if `record.Meta != nil && record.Meta.Seq != ""`, set `Message = "seq: " + record.Meta.Seq`; otherwise set `Message = record.Subject` (or `"-"` if empty).
   - **Step 2.5: Render verbose breakdown table (`--verbose` only):**
     - Write the table header and rows to `stdout` using exact column widths:
       ```text
       Revisions breakdown:
       UUID                                      Size  Created              Commit      Branch/Tag            Message
       ------------------------------------  --------  -------------------  ----------  --------------------  ----------------------
       <uuid-36-left>                        <size-8>  <created-19-left>    <hash-10>   <refs-20-left>        <msg>
       ```
       Format string per row: `fmt.Fprintf(stdout, "%-36s  %8s  %-19s  %-10s  %-20s  %s\n", row.UUID, row.SizeHuman, row.Created, row.Commit, row.BranchTag, row.Message)`.

3. **Interruption, cleanup & rollback:**
   - `usage` performs no local or remote filesystem mutations and creates no temporary files.
   - If `ctx.Done()` fires (`SIGINT` / `SIGTERM`) while the SSH subprocess or `git log` subprocess is running, `exec.CommandContext` sends `SIGTERM` to the child process (escalating to `SIGKILL` after the 3-second grace period), aborts output rendering immediately, and exits with code `130` (`errcode.ErrInterrupted`).

## 5. Side Effects, Git Plumbing & I/O Contract
- **Git plumbing invocations:**
  | Command & Flags | Working Dir | Expected Output Format | Failure Handling |
  | :--- | :--- | :--- | :--- |
  | `git rev-parse --is-inside-work-tree --show-toplevel --show-prefix` | Caller `PWD` | 3 newline-separated lines (`true`, `<abs-root>`, `<rel-prefix>`) | Non-zero exit or `false` maps to `errcode.ErrNotRepository` (exit `3`). |
  | `git config --local --get-all remote.origin.fetch` | `RepoRoot` | Newline-separated refspec strings | Non-zero exit (key absent) triggers `git config --local --add remote.origin.fetch +refs/heads/*:refs/remotes/origin/*`. |
  | `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix` | `RepoRoot` | NUL-delimited records (`\x00` between fields and `\x00` between commits); only executed when `--verbose` is active and remote revisions exist | Empty repository (`HEAD` unborn) or non-zero exit is treated gracefully as zero known commits (all remote revisions marked `[orphaned]`). |

- **Filesystem & metadata mutations:**
  - None on remote host (strictly read-only `du` and `stat` over SSH).
  - Local `.git/config` is only updated if `remote.origin.fetch` lacks `+refs/heads/*:refs/remotes/origin/*`.

- **Standard output (`stdout`):**
  - Default mode (`--verbose=false`):
    ```text

    Summary:
      Total space used:  1.4G

    ```
  - Verbose mode (`--verbose=true` with revisions present):
    ```text

    Summary:
      Total space used:  1.4G

    Revisions breakdown:
    UUID                                      Size  Created              Commit      Branch/Tag            Message
    ------------------------------------  --------  -------------------  ----------  --------------------  ----------------------
    550e8400-e29b-41d4-a716-446655440000      700M  2026-10-15 14:22:10  a1b2c3d     main, origin/main     seq: 1792074130
    7c9e6679-7425-40de-944b-e07fc1f90ae7      700M  2026-10-14 09:15:00  [orphaned]  -                     -
    ```

- **Standard error (`stderr`):**
  - Progress and informational messages (suppressed when `--quiet` is active):
    - `[git-drev] Connecting to remote host '<host>' to retrieve storage usage...`
    - `[git-drev] Remote storage usage for host:<host> path:<path>`
    - When `RevisionCount == 0`: `[git-drev] No revision directories found on remote storage.`
    - When `RevisionCount > 0` and `!Verbose`:
      - `[git-drev] Found <N> revision directory(ies) on remote storage.`
      - `[git-drev] Hint: Use -v/--verbose to view the detailed breakdown of revisions.`
  - Error messages (never suppressed):
    - `[git-drev] Error: <error summary>`
    - `[git-drev] Error: Details: <ssh/command stderr output>` (when applicable)

## 6. Error Matrix & Exit Code Mapping
| Failure Condition | Go Error Sentinel / Type | Exit Code | Expected `stderr` Message Pattern |
| :--- | :--- | :--- | :--- |
| Unknown flag, unexpected positional argument, or `--verbose` combined with `--quiet` | `errcode.ErrUsage` | `2` | `[git-drev] Error: <details>` |
| Invoked outside a Git worktree or inside a bare repository | `errcode.ErrNotRepository` | `3` | `[git-drev] Error: not a git repository (or any of the parent directories)` |
| Missing/malformed `.git-drev.yaml` | `errcode.ErrConfig` | `3` | `[git-drev] Error: No configuration file found` or `[git-drev] Error: <details>` |
| Missing or invalid `remote.host` or `remote.path` in configuration | `errcode.ErrConfig` | `3` | `[git-drev] Error: missing required configuration key '<remote.host\|remote.path>'` |
| Remote storage base path `<remote.path>` does not exist on `<remote.host>` | `errcode.ErrRemoteNotFound` | `6` | `[git-drev] Error: Remote path '<path>' does not exist.` |
| SSH connection failure, authentication failure, or missing `ssh` binary | `errcode.ErrTransport` | `7` | `[git-drev] Error: Failed to connect to remote host '<host>' via SSH or command failed (exit code: <code>)`<br>`[git-drev] Error: Details: <output>` |
| Context canceled via `SIGINT` or `SIGTERM` | `errcode.ErrInterrupted` | `130` | `[git-drev] Error: operation interrupted` |

## 7. Legacy Modernization & Edge Cases
- **Fixed legacy quirks:**
  1. **`IFS='|' read -r` Commit Log Parsing Bug (`legacy/usage.sh:179,214`):**
     - *Legacy flaw:* `git log --all --format="%H|%h|%ad|%D|%s"` was parsed with `IFS='|'`. Any commit subject containing a pipe character (`|`) shifted fields or truncated the commit subject.
     - *Modernized Go behavior:* `ScanAllCommits` uses `git log --all -z --format=%H%x00%h%x00%ad%x00%D%x00%s --date=unix` and splits strictly on NUL (`\x00`) bytes.
  2. **Subprocess Spawning Inside Loops (`legacy/usage.sh:11-30,184,202,253`):**
     - *Legacy flaw:* For every commit and every remote revision, `usage.sh` spawned multiple external processes (`grep -oE`, `head -n 1`, `xargs`, `jq -r`, and platform-sniffing `date -d` / `date -r` calls), causing severe slowdowns on repositories with hundreds of commits or revisions.
     - *Modernized Go behavior:* All JSON unmarshaling (`encoding/json`), regex matching (`regexp`), string trimming (`strings.TrimSpace`), and epoch timestamp formatting (`time.Unix(mtime, 0).Local().Format("2006-01-02 15:04:05")`) run in-memory in native Go with zero child processes per row.
  3. **Operational Logs Mixed into `stdout` (`legacy/usage.sh:86,150,157,162-163`):**
     - *Legacy flaw:* `drs::common::log` wrote `[drs] Connecting to remote host...` and `[drs] Hint: ...` to `stdout`, polluting redirected or piped `usage` reports.
     - *Modernized Go behavior:* All `[git-drev] ...` operational logs and hints are emitted to `stderr`; only the structured `Summary:` and `Revisions breakdown:` sections are written to `stdout`.
  4. **Unescaped Remote Path in SSH Invocation (`legacy/usage.sh:91`):**
     - *Legacy flaw:* `ssh -T "${host}" "/bin/sh -s -- \"${path}\""` interpolated `${path}` inside double quotes without escaping embedded spaces, quotes, or `$` characters.
     - *Modernized Go behavior:* `remote.path` is validated at config load time against shell metacharacters and passed as a POSIX single-quoted token (`'...'`) to `/bin/sh -s --`.
  5. **Destructive `drs::common::check_remote_refs` (`legacy/usage.sh:36`):**
     - *Legacy flaw:* Unset all `remote.origin.fetch` entries and ran `git ls-remote` network queries before checking usage.
     - *Modernized Go behavior:* Replaced by non-destructive, offline `EnsureRemoteFetchConfig`.
  6. **Silent Ignore of Extra Positional Arguments (`legacy/usage.sh:40-69`):**
     - *Legacy flaw:* Used `eval set -- "${params}"` (vulnerable to shell glob/injection) and `drs::common::no_args`, which merely logged a message to `stdout` and continued execution when extra arguments were passed.
     - *Modernized Go behavior:* Rejects unexpected positional arguments immediately with exit code `2` (`errcode.ErrUsage`).
  7. **Missing Repository Check from Subdirectories (`legacy/usage.sh:35`):**
     - *Legacy flaw:* Checked `[[ ! -f "drs.json" ]]` in the current working directory without resolving the Git top-level directory, failing when invoked from a subdirectory.
     - *Modernized Go behavior:* Resolves `RepoRoot` via `git rev-parse --show-toplevel` first and loads `.git-drev.yaml` from `RepoRoot`.

- **Boundary conditions:**
  - **Empty branch marker commits:** Commits created by `git-drev create` with `{"uuid":"...","marker":"true"}` or `domain.ZeroUUID` (`00000000-0000-0000-0000-000000000000`) are ignored when building the active revision commit map (unless a real `put` commit shares the parent UUID, in which case the `put` commit represents the revision), ensuring marker commits do not shadow actual revision metadata.
  - **Case-insensitive UUID correlation:** If a remote directory name or commit JSON uses uppercase hex characters, correlation normalizes keys via `strings.ToLower` while preserving the remote directory's exact UUID string in the display column.
  - **Non-UUID entries on remote storage:** Any files, symlinks to non-directories, or subdirectories in `<remote.path>` whose basenames do not match the 36-character RFC 4122 UUID regex are ignored in the revision breakdown (though their bytes remain reflected in `du -sh <remote.path>` total space used).
  - **Symlinked revision directories:** On the remote server, `[ -d "$d" ]` evaluates to true for both real directories and symlinks pointing to directories; `stat` and `du -sh` operate on the directory entry without following nested symlinks outside the revision payload.
  - **Paths with spaces:** `<remote.path>` containing spaces (e.g., `/var/drev/my project`) is safely quoted in the SSH `/bin/sh -s --` invocation and inside the remote script (`"$path"`, `"$d"`).
  - **Detached `HEAD` & unborn branches:** `usage` succeeds in detached `HEAD` state or in a newly initialized repository with zero commits; in the latter case, `git log --all` returns no records and any remote UUID directories are reported as `[orphaned]`.

## 8. Black-Box Verification Scenarios
| Scenario ID | Given (Repo & Filesystem State) | When (CLI Invocation) | Then (Expected Exit Code, `stdout`/`stderr`, & Repo/FS State) |
| :--- | :--- | :--- | :--- |
| `TC-01` | Valid repo with `.git-drev.yaml` (`remote.host: store`, `remote.path: /data/repo`); remote `/data/repo` occupies `2.5G` and contains 2 UUID directories (`uuid-1`, `uuid-2`). | `git-drev usage` | **Exit Code:** `0`<br>**`stdout`:** Contains `Summary:` and `Total space used:  2.5G` (no table).<br>**`stderr`:** Contains `[git-drev] Connecting to remote host 'store'...`, `[git-drev] Found 2 revision directory(ies) on remote storage.`, and `[git-drev] Hint: Use -v/--verbose...`. |
| `TC-02` | Same state as `TC-01`; `uuid-1` matches local `HEAD` commit `a1b2c3d` on `main` (`{"uuid":"<uuid-1>","seq":"1700000000"}`), while `uuid-2` has no matching commit in `git log --all`. | `git-drev usage --verbose` | **Exit Code:** `0`<br>**`stdout`:** Contains `Summary:` followed by `Revisions breakdown:` table where `uuid-1` shows commit `a1b2c3d`, branch `main`, message `seq: 1700000000`, and `uuid-2` shows commit `[orphaned]`, branch `-`, message `-`. |
| `TC-03` | Valid repo with `.git-drev.yaml`, invoked from nested subdirectory `src/pkg/`; `.git/config` is missing the wildcard fetch refspec. | `git-drev usage` | **Exit Code:** `0`<br>**`stdout`:** Prints `Summary:` with remote total space.<br>**Repo State:** `.git/config` contains `+refs/heads/*:refs/remotes/origin/*`. |
| `TC-04` | Valid repo with `.git-drev.yaml` pointing to `remote.path: /data/nonexistent`, which does not exist on the remote server. | `git-drev usage` | **Exit Code:** `6` (`ErrRemoteNotFound`)<br>**`stdout`:** Empty.<br>**`stderr`:** Contains `[git-drev] Error: Remote path '/data/nonexistent' does not exist.` |
| `TC-05` | Valid repo with `.git-drev.yaml`; remote `/data/repo` exists and occupies `4.0K` but contains zero UUID subdirectories (only a `.keep` file). | `git-drev usage -v` | **Exit Code:** `0`<br>**`stdout`:** Contains `Summary:` and `Total space used:  4.0K` (no `Revisions breakdown:` table printed).<br>**`stderr`:** Contains `[git-drev] No revision directories found on remote storage.` |
| `TC-06` | Valid repo with `.git-drev.yaml`; caller passes an unexpected positional argument or conflicting flags. | `git-drev usage extra-arg` | **Exit Code:** `2` (`ErrUsage`)<br>**`stdout`:** Empty.<br>**`stderr`:** Contains `[git-drev] Error:` usage error diagnostic rejecting positional arguments. |
