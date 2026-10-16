# Engineering Rules & Guidelines for git-drev

## Project Identity & Architecture
- **Tool:** `git-drev` — Git CLI extension written in Go 1.22+ (`git-drev <subcommand>` or `git drev <subcommand>`).
- **CLI Framework:** `github.com/spf13/cobra`.
- **Package Layout:**
  - `cmd/git-drev/main.go`: Thin entrypoint only (signal context `SIGINT`/`SIGTERM`, exit code mapping).
  - `internal/cli/`: Root dispatcher, global flags (`-v`, `-q`, `--no-color`, `--version`, `-h`), and subcommand handlers (`create`, `get`, `put`, `select`, `update`, `info`, `name`, `usage`, `config`).
  - `internal/domain/`: Pure Go value types (`ProjectConfig`, `BranchConfig`, `RemoteConfig`, `RsyncConfig`, `RevisionMeta`, `RemoteRevision`, `ZeroUUID`). Depends only on Go stdlib.
  - `internal/errcode/`: Sentinel error categories (`ErrRuntime`=1, `ErrUsage`=2, `ErrConfig`/`ErrNotRepository`=3, `ErrGitState`/`ErrDetachedHead`=4, `ErrGitExec`=5, `ErrRemoteNotFound`=6, `ErrTransport`=7, `ErrInterrupted`=130). Depends only on Go stdlib.
  - `internal/config/`: 3-tier YAML configuration loader (`loader.go`), cascading merger (`merge.go`), provenance reporter (`report.go`), and schema validator.
  - `internal/git/`: Thin `Runner` interface (`Run`, `RunLines`, `RunWithEnv`, `RunStream`), `Inspector` (read-only plumbing inspection), and `Operations` (mutating Git operations).
  - `internal/transport/`: `RemoteRunner` interface and SSH/rsync execution engine.
  - `internal/hook/`: Project hook runner (`.drev-put-hook`, `.drev-info-hook`) with timeout (`GIT_DREV_HOOK_TIMEOUT`, default `5m`) and `GIT_DREV_*` environment injection.
  - `internal/term/`: Structured `stderr` logger, TTY/`NO_COLOR`/`GIT_DREV_COLOR` detection, and decoupled `Timer`.
  - `test/integration/`: End-to-end integration tests using isolated Git repositories in `t.TempDir()`.
- **Dependency Boundaries:** `internal/config`, `internal/git`, `internal/transport`, and `internal/hook` may depend on `domain`, `errcode`, and `term`, but NEVER on `internal/cli`.

## Coding Standards & Idioms
In accordance with the [Google Go Style Guide](https://google.github.io/styleguide/go/guide) and `docs/spec/global.md`:

- **External Execution Boundary (`git`, `ssh`, `rsync`, hooks):**
  - NEVER call `os/exec.Command` directly in `internal/cli` or `internal/domain`.
  - ALWAYS invoke external binaries through `internal/git.Runner`, `internal/transport.RemoteRunner`, or `internal/hook`.
  - Sanitize subprocess environments with `LC_ALL=C`, `LANG=C`, `GIT_TERMINAL_PROMPT=0`, and `GIT_ADVICE=0`.
  - Prefer deterministic Git plumbing (`rev-parse`, `symbolic-ref`, `show-ref`, `ls-remote`, `for-each-ref -z`, `log -z` with `\x00` delimiters) over porcelain.
  - Prevent injection: always pass `--` before positional refs/paths, strictly validate UUIDs (RFC 4122 hex) and `remote.path`, and tokenize `remote.rsync` options without shell `eval` or glob expansion.
- **Context, Cancellation & Cleanup:**
  - Every external execution must accept a `context.Context` (`exec.CommandContext`) bound to `SIGINT`/`SIGTERM` (`SIGTERM` followed by `SIGKILL` after 3s grace).
  - Ensure transactional cleanup via `defer` on failure or interruption (e.g., `git rebase --abort` on stale-push rebase failure, best-effort remote staging directory removal on interrupted `put`).
- **Path & Repository Resolution:**
  - Normalize `GIT_PREFIX` and worktree root at startup via `git rev-parse --show-toplevel` and `--show-prefix`.
  - Resolve user CLI path arguments (`<target_directory>`, `<source_directory>`) relative to `CallerDir` before changing directory to `RepoRoot`. Resolve `.git-drev.yaml`, hooks, and `workdir` relative to `RepoRoot`.
- **Error Handling:**
  - Never call `log.Fatal()`, `panic()`, or `os.Exit()` inside `internal/`. Return explicit `error` values up to `main.go`.
  - Construct categorized errors with `errcode.NewError(category, ...)` / `errcode.WrapError(category, err, ...)` and optional `.WithHint(...)` for deterministic exit code mapping at the CLI boundary.
- **Libraries & Dependencies:**
  - Use the Go standard library (`encoding/json`, `crypto/rand`, `time`, `os`, `path/filepath`, `regexp`, `strings`, `context`)—never shell out to legacy utilities (`jq`, `uuidgen`, `sed`, `awk`, `grep`, `du`, `stat`, `rm`).
  - Allowed third-party dependencies: `github.com/spf13/cobra`, `github.com/stretchr/testify`, `gopkg.in/yaml.v3`. Do NOT import heavyweight Git libraries (`go-git`).

## Testing Rules
- Every subcommand and package must include:
  1. Table-driven unit tests for pure logic, parsers, and CLI flows using mock `git.Runner` and `transport.RemoteRunner` interfaces.
  2. Integration tests creating disposable repositories in `t.TempDir()` via `test/integration/harness.go`.
- Tests MUST NOT modify or rely on the host machine's `~/.gitconfig`, `~/.ssh/config`, or global Git state.

## Workflow & Output Rules
- Check `docs/spec/global.md` and `docs/spec/<subcommand>.md` for behavioral requirements before writing code; implement strict spec parity first.
- Enforce stream separation:
  - `stdout`: strictly for primary command output, machine-parseable porcelain (`name`, `info`, `usage` tables), and `--help`/`--version`.
  - `stderr`: all operational logs (`[drev] <message>`), warnings (`[drev] WARN: <message>`), errors (`[drev] ! <message>`), hints (`[drev] Hint: <remediation>`), and elapsed time summaries.
