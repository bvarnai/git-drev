# Chapter 1: The git-drev philosophy

`git-drev` is a Git CLI extension that versions and synchronizes large directory trees using Git for revision metadata and SSH + `rsync` for storage and transfer.

---

## The problem: large build directories in Git

Many engineering workflows produce directory trees that are too large for standard Git but still need branch, tag, and commit semantics:
- **Embedded firmware images and OS root filesystems** (often 2–20 GB per build)
- **Prebuilt toolchains, sysroots, and cross-compilation SDKs** (tens of thousands of header and library files that rarely change between releases)
- **JVM / Java application bundles** containing hundreds of `.jar` archives where only a handful change per build
- **Game engine asset trees, audio banks, and compiled shaders**

Tracking these directories directly in Git degrades repository performance:
1. **Slow clones and fetches:** Git stores every historical blob inside `.git/objects`. A few multi-gigabyte builds turn `git clone` into a slow, bandwidth-heavy download.
2. **Packfile thrashing:** Git's packfile compression is tuned for source code; repacking multi-gigabyte binary trees consumes gigabytes of RAM and CPU time.
3. **Why Git LFS and S3 fall short for directory trees:**
   - **Git LFS** versions individual files rather than cohesive directory snapshots. When files change slightly, LFS transfers full file blobs without block-level delta compression.
   - **Cloud object stores (S3, GCS)** treat every file as an independent object key. Without a custom manifest tool, you have to tar/untar archives or re-upload unchanged files on every build.

---

## The core concept: metadata in Git, data on remote storage

`git-drev` splits directory versioning into two layers:

```mermaid
flowchart TD
    subgraph Local["Local machine"]
        GitRepo["Git metadata repository<br/>(lightweight commits, branches, tags)"]
        WorkDir["Local workdir<br/>(active directory snapshot)"]
    end

    subgraph Remote["Remote infrastructure"]
        GitRemote["Git remote<br/>(GitHub / GitLab / Gitea)"]
        StorageServer["SSH storage host<br/>(remote directory snapshots by UUID)"]
    end

    GitRepo <-->|"git push / fetch (metadata only)"| GitRemote
    WorkDir <-->|"rsync over SSH"| StorageServer
    GitRepo -.->|"correlates UUID"| StorageServer
```

1. **Git as the revision ledger:**
   - Each published directory snapshot is assigned an RFC 4122 UUID (`018f24a0-7b32-7e9b-a012-3456789abcde`).
   - `git drev put` creates a commit whose subject is a compact JSON string (while Git natively records the author and timestamp in the commit header):
     ```json
     {"uuid":"018f24a0-7b32-7e9b-a012-3456789abcde","seq":"1729332000"}
     ```
   - Because the working directory (`workdir`) is ignored via `.gitignore`, the Git repository stays under a few megabytes regardless of how many builds are published.
   - **Permanent build provenance:** Any small files placed outside `workdir/`—such as `build.log`, compiler flags, test summaries, or SBOMs—are staged (`git add --all`) and committed alongside the revision UUID. Even after old binary snapshots are pruned from the storage server, the Git history retains the build logs and metadata.

2. **Server-side deduplication via hardlinks (`cp -al`):**
   - Before uploading a new revision, `git-drev` reads the UUID of the parent commit on the active branch and runs `cp -al -- <parent_uuid> <new_uuid>` on the storage server.
   - Because hardlinks point to existing inodes, cloning a large baseline directory takes milliseconds and uses zero additional data blocks.
   - When `rsync` uploads the new build, unchanged files keep their shared inodes, while modified files are written to temporary files and atomically renamed into place.

3. **Incremental transfers (`rsync` over SSH):**
   - Both `put` (upload) and `get` (download) transfer files over SSH using `rsync`.
   - If a Consumer switches from build `#101` to build `#102` and 95% of the files are identical, `rsync` skips the unchanged files and transfers only the 5% that changed.

---

## Repository topology: dedicated repo vs. Git submodule

> [!IMPORTANT]
> **Do not run `git-drev` directly inside your primary source-code branch.**
> Because `git drev put`, `update`, and `select` manage their own commit subjects (`{"uuid":"...","seq":"..."}`) and use `git reset --hard` / `git rebase -X theirs` to synchronize state, `git-drev` should always live in a **dedicated metadata repository**.

Teams typically use one of two layouts:

1. **Standalone artifact repository (simplest for QA / test teams):**
   - A dedicated Git repository (e.g., `myproject-builds.git`) containing `.git-drev.yaml`, optional hooks (`.git-drev-put-hook`, `.git-drev-info-hook`), and the ignored `workdir/` (`data/`).
   - CI pipelines push new build revisions to `myproject-builds.git`, and testers clone `myproject-builds.git` to pull ready-to-run builds without needing source code or compilers.
2. **Git submodule inside your source repository (best for prebuilt SDKs / toolchains):**
   - Add the `git-drev` metadata repository as a Git submodule inside your main source tree (for example, at `third_party/sdk/`).
   - Developers clone the source repository, run `git drev get` inside `third_party/sdk/` to fetch the prebuilt toolchain, and pin the exact SDK revision in the parent source repo using standard submodule commits.

---

## The Producer & Consumer workflow

```mermaid
sequenceDiagram
    autonumber
    actor Producer as Producer (CI / Build)
    participant Git as Git Remote
    participant Storage as SSH Storage Host
    actor Consumer as Consumer (Developer / QA)

    rect rgba(59, 135, 235, 0.12)
        Note over Producer,Git: Producer runs: git drev create release/1.0
        Producer->>Git: git checkout -b release/1.0 & git push (marker commit)
    end

    Producer->>Producer: Build artifacts into workdir/

    rect rgba(229, 147, 58, 0.12)
        Note over Producer,Storage: Producer runs: git drev put
        Producer->>Storage: ssh: cp -al -- <parent_uuid> <new_uuid>
        Producer->>Storage: rsync: workdir/ -> <new_uuid>/
        Producer->>Git: git commit (JSON subject) & git push --force-with-lease
    end

    rect rgba(92, 198, 141, 0.12)
        Note over Git,Consumer: Consumer runs: git drev select release/1.0
        Consumer->>Git: git fetch --all & git checkout release/1.0
    end

    rect rgba(92, 198, 141, 0.12)
        Note over Storage,Consumer: Consumer runs: git drev get
        Consumer->>Storage: rsync: <new_uuid>/ -> workdir/
    end
```

### The Producer (CI / build automation)
- Runs in CI pipelines (Jenkins, GitHub Actions, GitLab CI).
- Creates or switches to target branches with `git drev create <branch>`.
- Compiles or packages artifacts into `workdir`.
- Publishes snapshots with `git drev put` (optionally passing `-s <sequence>` to attach a monotonic CI build number) and uses `.git-drev-put-hook` to record build metadata.

### The Consumer (developer / QA engineer)
- Pulls prebuilt artifacts without compiling locally.
- Switches between branches, tags, or historical UUIDs with `git drev select [<branch>|<tag>|<uuid>]`.
- Downloads the snapshot into `workdir` with `git drev get`, or runs `git drev get --latest` (`-l`) to update the active branch from `origin` and download in one command.

---

## Design principles

1. **No custom server daemons:** Requires only standard `git`, OpenSSH (`ssh`), and `rsync` on the client, and `sshd`, `rsync`, and POSIX `cp` / `du` on the storage host.
2. **Cross-platform client:** Written in Go 1.22+ with zero external runtime dependencies beyond `git`, `ssh`, and `rsync`; runs on Linux, macOS, and Windows (Git Bash).
3. **Transactional safety:** All mutating commands check worktree state and `.git/index.lock` before running, push with `--force-with-lease`, and clean up incomplete remote staging directories if interrupted (`SIGINT` / `SIGTERM`).
4. **Simple retention:** Remote revisions are plain `<path>/<uuid>/` directories on disk. Auditing active vs. orphaned snapshots takes a single command (`git drev usage -v`).
