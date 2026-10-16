# Spec: git-drev config

## 1. Command Signature
- **Usage:** `git-drev config [flags]`
- **Arguments:** None. (Positional arguments are rejected).
- **Flags:**
  - `--sources`: Outputs solely the resolved configuration sources hierarchy (file paths across the 3-tier cascade) without dumping the configuration payload.
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** `git` (>= 2.25.0, optional if user/shared global configuration files exist).
- **Environment variables read:** `GIT_DREV_CONFIG`, `HOME`, `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any repository subdirectory, or from outside a repository if global configuration is defined.
- **Git state assertions:** Read-only inspection command; safe to execute in any repository state (clean, dirty, rebase, merge, detached HEAD).

## 3. Core Execution Flow
1. **Working Directory & Worktree Context:**
   - Detect repository root (`RepoRoot`) if inside a Git worktree.
2. **Cascade Evaluation:**
   - Resolve configuration across the 3-tier cascade via `config.Inspect(targetDir)`:
     - **Tier 1:** User Global (`~/.config/git-drev/config.yaml`, `~/.config/git-drev/config.yml`, `~/.git-drev.yaml`, `~/.git-drev.yml`, `~/.git-drev/config.yaml`, `~/.git-drev/config.yml`).
     - **Tier 2:** Shared Global (`GIT_DREV_CONFIG` environment variable).
     - **Tier 3:** Repo Local (`.git-drev.yaml`, `.git-drev.yml`, `.git-drev/config.yaml`, `.git-drev/config.yml` in repo root).
   - If `remote.path` is sourced from global configuration without a repo-level override, automatically appends `/{repo}` to ensure repository storage isolation. Expands `{repo}` and `{project}` variables using the detected repository name.
3. **Provenance Annotation:**
   - Prepend the Source hierarchy header as YAML comments via `config.FormatReport(report, opts.Sources)`:
     ```yaml
     # Source hierarchy:
     #   Tier 1 (User global [~/.config/git-drev/config.yaml, ~/.git-drev.yaml]): <path or "none">
     #   Tier 2 (Shared global [$GIT_DREV_CONFIG]):                               <path or "unset">
     #   Tier 3 (Repo local [.git-drev.yaml, .git-drev/config.yaml]):             <path or "none">
     ```
   - If `--sources` is specified, emit only the Source hierarchy header and exit cleanly with Exit Code `0`.
   - Otherwise, serialize the effective configuration to YAML and annotate defaulted and inferred fields inline with YAML comments (`# default convention`, `# expanded {repo} -> <name>`, `# appended /{repo} for repository isolation`).
4. **Standard Output Protocol:**
   - Output directly to `stdout`. The resulting output is 100% valid YAML and safe to redirect directly into a `.git-drev.yaml` file (`git-drev config > .git-drev.yaml`).

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Configuration evaluated and printed successfully. | None. |
| `2` | `errcode.ErrUsage` | Unexpected positional arguments passed (`git-drev config extra`). | `[git-drev] Error: Unexpected argument: <arg>` |
| `3` | `errcode.ErrConfig` | No configuration files found across any tier. | `[git-drev] Error: No configuration file found` |
| `3` | `errcode.ErrConfig` | `GIT_DREV_CONFIG` path specified but file does not exist or is unreadable. | `[git-drev] Error: configuration file specified by GIT_DREV_CONFIG not found: <path>` |
| `3` | `errcode.ErrConfig` | Malformed YAML or schema validation failure (e.g., missing `remote.host` or `remote.path`). | `[git-drev] Error: <validation error>` |
