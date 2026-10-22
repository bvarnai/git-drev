# Changelog

All notable changes to `git-drev` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Interactive and non-interactive `git-drev init` subcommand to bootstrap dedicated metadata repositories, verify remote SSH read/write access permissions, and generate `.git-drev.yaml` and `.gitignore` (`#53`, `#54`).
- Automated Windows Scoop bucket release publication workflow (`.github/workflows/publish-scoop.yml`) (`#45`, `#46`).
- Deterministic Eclipse SDK benchmark agent skill and runner script (`.agents/skills/drev-benchmark/`) (`#55`, `#56`).

### Changed
- Comprehensive User Guide reorganization, architecture documentation, and shared read-only consumer SSH deployment patterns (`#43`, `#44`, `#49`).
- Exclude `workdir` payload path during `git add --all` staging in `git-drev put` (`#57`).

### Fixed
- Wire `--latest` (`-l`) flag in `git-drev get` to fetch upstream commits and hard-reset the active branch prior to downloading payload (`#42`).
- Prevent redundant `+commit` build metadata duplication in version string (`#54`).
- Remove redundant, undocumented `GIT_DREV_HOOK_TYPE` environment variable from hook runner (`#57`).
- Correct benchmark table storage savings percentage to `64.6%` (`#57`).

## [0.1.0] - 2026-10-21

### Added
- Busy-wait terminal spinner, streaming SSH progress feedback, and fast `usage` summary mode (`#10`, `#11`).
- Server-side revision retention script (`scripts/cleanup.sh`) with tag pinning, parent baseline marker protection, and cron setup guide (`#25`, `#26`).
- Structured operational debug logging (`Debug` / `Debugf`) on `stderr` when `-v` / `--verbose` is passed (`#29`, `#30`).
- Cross-platform CI build and test matrix across `ubuntu-latest`, `macos-latest`, and `windows-latest` (`#33`, `#34`).
- Stable tagless release archive aliases (`git-drev_<os>_<arch>.tar.gz` / `.zip`), PR template, and `CHANGELOG.md` (`#31`, `#32`).
- Real-world Eclipse SDK `I-builds` benchmark report in `README.md` and User Guide Chapter 5 (`#23`, `#24`).
- AI coding assistant rule and skill templates (`templates/rules/git-drev.md`, `templates/skills/git-drev/SKILL.md`).

### Changed
- Decoupled `internal/transport` generic SSH/rsync execution from `internal/storage` domain operations (`#12`, `#13`, `#14`, `#15`, `#17`, `#18`).
- Decoupled `-v` (`--verbose`) from `-a` (`--all`) in `git drev usage` so `-v` emits diagnostic traces to `stderr` while `-a` controls itemized revision table rendering on `stdout` (`#29`, `#30`).

### Fixed
- Reuse parent revision UUID from branch creation marker commits (`{"uuid":"...","marker":"true"}`) for server-side `cp -al` baseline cloning on the first `put` of a new branch (`#7`, `#20`).
- Match all 12 trailing hexadecimal characters in remote `usage` UUID directory glob (`#21`, `#22`).
- Default revision sequence (`seq`) to current Unix epoch seconds when `--sequence` (`-s`) is omitted in `git drev put` (`#29`, `#30`).

## [0.0.1] - 2026-10-18

### Added
- Initial Go implementation of `git-drev` (`create`, `put`, `get`, `select`, `update`, `info`, `name`, `usage`, `config`, `completion`).
- 3-tier cascading YAML configuration loader (`User Global -> $GIT_DREV_CONFIG -> .git-drev.yaml`) with provenance reporting (`config --sources`).
- Server-side hardlink baseline cloning (`cp -al --`) and incremental `rsync` transfers over OpenSSH.
- Sequence-aware optimistic concurrency control loop on `git drev put` using `--force-with-lease`.
- Project lifecycle hooks (`.git-drev-put-hook`, `.git-drev-info-hook`) with timeout enforcement (`GIT_DREV_HOOK_TIMEOUT`).

[Unreleased]: https://github.com/bvarnai/git-drev/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bvarnai/git-drev/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/bvarnai/git-drev/releases/tag/v0.0.1
