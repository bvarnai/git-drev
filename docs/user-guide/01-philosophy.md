# Chapter 1: The git-drev Philosophy

`git-drev` (Directory Revision Storage) is a lightweight Git CLI extension designed to manage and synchronize large directory snapshots efficiently without bloating your Git repository.

---

## The Problem: Large Build Artifacts in Git

Modern software engineering frequently involves large directory artifacts:
- Embedded firmware images and cross-compiled root filesystems (>5GB).
- Machine learning model weights, training checkpoints, and datasets.
- Java application builds containing hundreds of third-party `.jar` dependencies where only 1 or 2 change between builds.
- Game engine asset directories and pre-rendered textures.

Attempting to track these directory snapshots in standard Git repositories quickly leads to catastrophic repository bloat:
1. **Cloning slowdown:** Git stores the complete history of every version in `.git/objects`. A few 5GB builds turn a clone into an hours-long, multi-gigabyte download.
2. **Poor delta compression:** Git's packfile delta compression is designed for text files and small objects; packing massive multi-gigabyte binary directories causes high memory usage and packfile thrashing.
3. **Git LFS and S3 overhead:** 
   - Git LFS tracks individual files rather than cohesive directory trees. When hundreds of files change slightly across builds, LFS still re-uploads entire files without block-level delta compression.
   - Cloud blob storage (Amazon S3, Google Cloud Storage) lacks native block-level delta synchronization, requiring full object uploads/downloads and custom orchestration scripts.

---

## The Core Concept: Metadata in Git, Data on Remote Storage

`git-drev` solves this by decoupling **metadata** from **data**:

```mermaid
flowchart TD
    subgraph Local["Local Machine"]
        GitRepo["Git Metadata Repository<br/>(Lightweight commits, branches, tags)"]
        WorkDir["Local Workdir<br/>(Active directory snapshot)"]
    end

    subgraph Remote["Remote Infrastructure"]
        GitRemote["Git Remote Server<br/>(GitHub / GitLab / Bitbucket)"]
        StorageServer["SSH Storage Host<br/>(Remote directory snapshots by UUID)"]
    end

    GitRepo <-->|"git push / pull (metadata only)"| GitRemote
    WorkDir <-->|"rsync over SSH (block deltas)"| StorageServer
    GitRepo -.->|"Correlates UUID"| StorageServer
```

1. **Git as a Transactional Metadata Database:**
   - Every directory revision is assigned an RFC 4122 UUID (`018f24a0-7b32-7e9b-a012-3456789abcde`).
   - Git commits carry pure JSON metadata in their commit subjects:
     ```json
     {"uuid":"018f24a0-7b32-7e9b-a012-3456789abcde","timestamp":"2026-10-19 10:00:00","author":"ci-builder","message":"Nightly build #42"}
     ```
   - The Git repository stays feather-light (under a few megabytes), cloning in seconds.

2. **Rsync Delta Synchronization over OpenSSH:**
   - Directory contents are stored on a dedicated remote storage server accessible via SSH.
   - When uploading (`git drev put`) or downloading (`git drev get`), `git-drev` uses the proven `rsync` algorithm to transfer **only modified byte blocks**, reducing multi-gigabyte transfers to seconds.

3. **Server-Side Baseline Hardlink Cloning:**
   - Before uploading new files, `git drev put` copies the parent revision on the remote server using hardlinks (`cp -al` or `rsync --link-dest`).
   - Unchanged files consume **zero additional disk space** on the remote server.
   - Build-to-build storage overhead is limited strictly to the actual bytes modified.

---

## The Producer & Consumer Paradigm

`git-drev` workflows are structured around two distinct operational roles:

```mermaid
sequenceDiagram
    autonumber
    actor Producer as Producer (CI / Build Machine)
    participant Git as Git Remote
    participant Storage as SSH Storage Server
    actor Consumer as Consumer (Tester / Developer)

    Note over Producer: Step 1: Initialize revision branch
    Producer->>Git: git drev create release/1.0
    Producer->>Producer: Execute build pipeline into workdir/
    Note over Producer: Step 2: Upload revision
    Producer->>Storage: cp -al parent_uuid new_uuid (server-side)
    Producer->>Storage: rsync workdir/ -> new_uuid/ (deltas)
    Producer->>Git: git commit (metadata) & git push

    Note over Consumer: Step 3: Consume revision
    Consumer->>Git: git drev select release/1.0
    Consumer->>Storage: rsync new_uuid/ -> workdir/ (deltas)
    Note over Consumer: Local workdir is now byte-for-byte identical
```

### The Producer (CI / Release Automation)
- Runs in headless CI pipelines (Jenkins, GitHub Actions, GitLab CI).
- Automatically creates branches via `git drev create <branch>`.
- Executes compilation or packaging to populate the local `workdir`.
- Publishes snapshots via `git drev put -m "Build details"`.
- Interacts with project hooks (`.git-drev-put-hook`) to inject CI build metadata into commit records.

### The Consumer (Tester / Developer)
- Developers or QA engineers who need to test exact builds without recompiling.
- Switches between builds instantly via `git drev select <branch>|<uuid>`.
- Synchronizes the local working directory via `git drev update` or `git drev get`.
- Only changed files are fetched over the network; if 95% of dependencies are identical to the previous build, only 5% of data is downloaded.

---

## Guiding Principles

1. **Zero Daemon Overhead:** No custom servers, daemons, or background agents. `git-drev` uses standard `git`, OpenSSH (`ssh`), and `rsync`.
2. **POSIX & Cross-Platform Integrity:** Built with Go 1.22+, supporting Linux, macOS, and Windows (Git Bash).
3. **Deterministic & Safe:** All Git mutations validate working tree state and index locks; all pushes use safe leasing; all subprocesses respect strict cancellation timeouts.
4. **Transparent Retention:** Server-side revisions are simple directories named after UUIDs (`<path>/<uuid>`). Correlating active versus orphaned revisions is easily inspected with `git drev usage -v`.
