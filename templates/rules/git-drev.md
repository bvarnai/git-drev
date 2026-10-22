# Directory Revision Guidelines for git-drev

When working in a repository managed by `git-drev`, always use the `git-drev` CLI (or `git drev`) for directory revision operations instead of raw Git or `rsync` commands:

1. **Repository Bootstrap (`init`):**
   - Bootstrap dedicated metadata repositories via `git drev init` (or `git drev init -y` in scripts).
   - Never initialize `git-drev` inside existing source code repositories.

2. **Revision Branch Initialization (`create`):**
   - Initialize new revision branches via `git drev create <branch>`.
   - Never manually create empty JSON marker commits (`{"uuid":"...","marker":"true"}`) with raw `git commit --allow-empty`.

3. **Publishing Revisions — Producer Workflow (`put`):**
   - Publish directory snapshots from `workdir` (or an explicit `<source_directory>`) using `git drev put` (or `git drev put -s <sequence>` in CI pipelines).
   - Never upload directly into `<remote.path>/<uuid>` via raw `scp`/`rsync`, and never pass `--inplace`, `--append`, or `--append-verify` to `rsync` (which would corrupt hardlinked parent baselines created with `cp -al --`).

4. **Selecting & Downloading Revisions — Consumer Workflow (`select`, `get`, `update`):**
   - Switch between revision branches, tags, or historical UUIDs using `git drev select [<branch>|<tag>|<uuid>]`.
   - Download the payload for the current `HEAD` commit into `workdir` (or `<target_directory>`) using `git drev get`.
   - Fetch the latest branch metadata and download the newest snapshot in a single step using `git drev get -l` (`--latest`).
   - Fast-forward local branch metadata without downloading the directory payload using `git drev update`.

5. **Inspection & Diagnostics (`info`, `name`, `usage`, `config`):**
   - Run project info hooks via `git drev info`.
   - Query the active revision branch name via `git drev name`.
   - Inspect per-revision physical vs. cumulative disk footprint on the remote host via `git drev usage`.
   - Inspect the resolved 3-tier YAML configuration and provenance via `git drev config` (or `git drev config --sources` for file paths only).
