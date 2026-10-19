# Chapter 5: Storage architecture and retention management

This chapter explains how `git-drev` structures files on the remote storage host, how hardlink baseline cloning eliminates storage duplication, and how to safely manage storage retention and clean up orphaned revisions.

---

## 1. Remote storage layout

On the remote storage host specified in `.git-drev.yaml` (`remote.host` and `remote.path`), revisions are stored in flat subdirectories named after their respective RFC 4122 UUIDs:

```
/srv/drev/my-project/
├── 4a123f89-8d7b-4b10-8f6a-49321ef19b12/   # Revision UUID (release/1.0, seq 1)
│   ├── bin/
│   ├── lib/
│   └── assets/
├── 9f8e7d6c-5b4a-3a21-10fe-dcba98765432/   # Revision UUID (release/1.0, seq 2)
│   ├── bin/
│   ├── lib/
│   └── assets/
└── d4e5f6a7-b8c9-0123-4567-89abcdef0123/   # Revision UUID (main, seq 1)
    ├── bin/
    └── lib/
```

### Why flat UUID directories?
1. **Branch-agnostic deduplication:** Multiple branches can branch off each other without path nesting or conflicts
2. **Immutable integrity:** Once published, a UUID directory is treated as read-only
3. **Safe concurrency:** Builders targeting different branches or revisions operate on independent UUID targets without locking collisions

---

## 2. Hardlink baseline cloning (`cp -al`)

The primary innovation of `git-drev` for large builds is **server-side baseline cloning**:

```mermaid
flowchart TD
    subgraph StorageServer["Storage server filesystem (/srv/drev/...)"]
        Parent["Parent snapshot (UUID 1)<br/>inode 1001: app.bin<br/>inode 1002: libA.so<br/>inode 1003: libB.so"]
        NewRev["New snapshot (UUID 2)<br/>inode 1001: app.bin (hardlinked)<br/>inode 1002: libA.so (hardlinked)<br/>inode 2005: libB.so (modified)"]
    end

    Parent -->|"cp -al (0 byte copy)"| NewRev
    Upload["Local builder"] -->|"rsync uploads only changed libB.so"| NewRev
```

When publishing a revision whose branch has a parent commit:
1. `git drev put` invokes `cp -al -- <parent_uuid> <new_uuid>` on the remote storage host via SSH
2. The operating system creates hard links for all files in the parent directory. This operation completes in milliseconds and consumes **zero additional disk blocks** (only standard filesystem directory entries)
3. `rsync` uploads the builder's changes into `<new_uuid>`. By default, when `rsync` transfers a modified file, it writes the updated payload to a temporary file (`.filename.XXXXXX`) in a new inode and atomically `rename()`s it over the destination path—cleanly breaking the hardlink for that specific file without mutating the parent revision. Unchanged files continue sharing storage inodes with the parent snapshot

> [!WARNING]
> **Why `--inplace`, `--append`, and `--append-verify` are prohibited on `put`:**
> If `rsync` were invoked with `--inplace`, `--append`, or `--append-verify`, it would write directly into the existing destination inode and silently mutate historical revisions that share that hardlink. To protect revision immutability, `git-drev` automatically validates `remote.rsync.put` and rejects those flags with a configuration error (`ExitCode 3`).

---

## 3. Rsync configuration & tuning

The `remote.rsync` section in `.git-drev.yaml` configures custom flags appended to `rsync` during download (`get`) and upload (`put`):

```yaml
remote:
  host: storage-server
  path: /srv/drev/my-project
  rsync:
    get: -az --delete-during --stats
    put: -az --delete-during --whole-file --stats
```

### Implicit & recommended flags explained
`git-drev` always passes `-a --delete-during -e "ssh -T"` before appending your custom `get` or `put` options:
- `-a` (`--archive`): Recursively preserves file permissions, timestamps, symlinks, and directory hierarchies
- `-z` (`--compress`): Compresses file data during network transfer
- `-H` (`--hard-links`): Preserves internal hardlinks within the snapshot
- `-A` (`--acls`): Preserves POSIX Access Control Lists
- `-X` (`--xattrs`): Preserves extended file attributes (vital for SELinux contexts or macOS metadata)
- `--delete-during`: Deletes files from the target that no longer exist in the source while minimizing memory overhead on large file trees
- `--whole-file`: Speeds up local-LAN or high-bandwidth server uploads after `cp -al` when modified build artifacts are completely regenerated

---

## 4. Auditing storage with `git drev usage`

To check storage consumption, run:

```bash
git drev usage
```
This queries the remote host via `ssh` and prints the total disk space utilized by `<remote.path>`:
```
Total size on remote is 8.1G (/srv/drev/my-project)
```

### Correlating active vs. orphaned revisions (`-v`)
When developers delete old Git branches, force-push rebases, or delete tags, some remote UUID directories may no longer be referenced by any Git commit in repository history. These are **orphaned revisions**.

Running `git drev usage -v` scans your Git commit history (`git log --all`) and correlates every known UUID against directories on the remote host:

```bash
git drev usage -v
```

Example Output:
```
Total size on remote is 8.1G (/srv/drev/my-project)

UUID                                  Size  Created     Commit      Branch/Tag    Message
4a123f89-8d7b-4b10-8f6a-49321ef19b12  4.2G  2026-10-19  a7b8c9d     release/1.0   {"uuid":"4a123f89-8d7b-4b10-8f6a-49321ef19b12","seq":"200"}
9f8e7d6c-5b4a-3a21-10fe-dcba98765432  120M  2026-10-18  1f2e3d4     main, v1.0.1  {"uuid":"9f8e7d6c-5b4a-3a21-10fe-dcba98765432","seq":"100"}
01234567-89ab-cdef-0123-456789abcdef  3.8G  2026-10-15  [orphaned]  -             -
```

---

## 5. Automated retention management

To reclaim disk space consumed by orphaned revisions, run an automated retention cleanup script on the storage host or via CI.

### Server-side retention cleanup script (`cleanup.sh`)
Deploy this script to your storage server to clean up orphaned directories:

```bash
#!/usr/bin/env bash
# cleanup.sh - Clean up orphaned git-drev revisions
set -euo pipefail

STORAGE_PATH="/srv/drev/my-project"
GIT_REPO_PATH="/srv/git/my-project.git"

echo "=== Starting git-drev retention cleanup ==="

# 1. Extract all valid revision UUIDs referenced across all Git commits
mapfile -t VALID_UUIDS < <(
  git --git-dir="${GIT_REPO_PATH}" log --all --format=%s \
    | grep -oE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' \
    | sort -u
)

echo "Found ${#VALID_UUIDS[@]} active revision UUIDs in Git history."

# 2. Iterate through physical directories on the storage host
for dir in "${STORAGE_PATH}"/*; do
  [[ -d "${dir}" ]] || continue
  uuid=$(basename "${dir}")

  # Check if directory name is a valid UUID
  if [[ "${uuid}" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]; then
    # Check if UUID exists in the valid list
    if ! printf '%s\n' "${VALID_UUIDS[@]}" | grep -qi "^${uuid}$"; then
      echo "Removing orphaned revision: ${uuid}"
      rm -rf "${dir}"
    fi
  fi
done

echo "=== Cleanup completed successfully ==="
```

### Scheduling with Cron
Set up a weekly cron job on the storage server:

```cron
# Run retention cleanup every Sunday at 03:00 AM
0 3 * * 0 /usr/local/bin/drev-cleanup.sh >> /var/log/drev-cleanup.log 2>&1
```
