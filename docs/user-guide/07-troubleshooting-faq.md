# Chapter 7: Troubleshooting and FAQ

This chapter addresses common operational questions, edge cases, and diagnostic steps for `git-drev`.

---

## 1. Common troubleshooting scenarios

### 1. SSH authentication failure (`Permission denied (publickey)`)

**Symptom:**
```
[git-drev] Error: SSH command failed: Permission denied (publickey).
[git-drev] Hint: Verify SSH access to 'storage-server' using 'ssh -Tv storage-server'.
```

**Diagnosis & Remediation:**
1. Test SSH connectivity directly using verbose mode:
   ```bash
   ssh -Tv storage-server
   ```
2. Verify that your SSH private key permissions are restrictive:
   ```bash
   chmod 600 ~/.ssh/id_ed25519
   chmod 700 ~/.ssh
   ```
3. Ensure the SSH public key is added to `~/.ssh/authorized_keys` on the storage server.
4. If using non-default SSH key files, define them in `~/.ssh/config`:
   ```ssh-config
   Host storage-server
       HostName storage.internal.net
       User builduser
       IdentityFile ~/.ssh/id_ed25519
   ```

---

### 2. Detached HEAD state (`HEAD is detached`)

**Symptom:**
```
[git-drev] Error: Cannot execute put: HEAD is detached
[git-drev] Hint: Use "git drev select <branch>" to select an existing branch or "git drev create <branch>" to start a new branch.
```

**Why this happens:**
Mutating commands like `put` and `update` require an attached branch so that new commits can be pushed to `origin`. `HEAD` becomes detached when you checkout a specific commit SHA, tag, or UUID via `git drev select <uuid>`.

**Remediation:**
- To switch to an existing branch:
  ```bash
  git drev select main
  ```
- To start a new branch from this exact snapshot:
  ```bash
  git drev create feature/hotfix-patch
  ```

---

### 3. Git index lock error (`.git/index.lock exists`)

**Symptom:**
```
[git-drev] Error: Git index is locked (.git/index.lock)
[git-drev] Hint: Another Git process may be running. If not, delete .git/index.lock manually.
```

**Why this happens:**
Another Git process crashed, was forcefully killed, or an IDE extension is currently querying Git.

**Remediation:**
1. Check if any background Git processes are still running:
   ```bash
   pgrep -fl git
   ```
2. If no Git processes are active, delete the stale lock file:
   ```bash
   rm -f .git/index.lock
   ```

---

### 4. Interrupted uploads or network disconnections

**What happens if `git drev put` is interrupted (Ctrl+C or network drop)?**
1. Subprocesses receive `SIGTERM` followed by `SIGKILL` after 3 seconds.
2. `git-drev` executes transactional cleanup:
   - A best-effort remote command removes the incomplete staging directory `<path>/<uuid>` from the storage server.
   - No Git commit is created.
   - Your local working tree and repository remain untouched.
3. You can safely re-run `git drev put` once the connection is restored.

---

### 5. Stale-push concurrency conflict

**Symptom:**
Multiple builders pushed a revision to the same branch at almost the exact same second.

**Resolution:**
`git-drev` features automatic sequence-aware optimistic concurrency control (up to 5 attempts). When `git push --force-with-lease` is rejected due to a concurrent push:
1. `git-drev` logs:
   ```
   [git-drev] Stale info detected, trying to resolve
   ```
2. Fetches upstream changes (`git fetch --all --prune`) and compares sequence numbers (`seq`) unless `--no-sequence-check` is set:
   - **If `remoteSeq > localSeq`:** A newer build has already landed on `origin/<branch>`. `git-drev` logs `Newer revision found, dropping older (hard reset)`, resets the local branch to `origin/<branch>`, and exits `0`.
   - **If `localSeq >= remoteSeq`:** `git-drev` rebases your local commit onto `origin/<branch>` using `git rebase --strategy-option=theirs` and retries the leased push with exponential backoff. If a rebase error occurs, it safely restores the worktree via `git rebase --abort`.

---

## 2. Frequently asked questions (FAQ)

### How does `git-drev` compare to Git LFS?

| Feature | `git-drev` | Git LFS |
| :--- | :--- | :--- |
| **Storage unit** | Cohesive directory trees | Individual file blobs |
| **Delta efficiency** | Rsync block-level deltas + hardlink baselines (`cp -al`) | Full file uploads for every modification |
| **Git clone size** | Compact JSON commits (<10 MB total repository) | Pointer history grows with file count |
| **Infrastructure** | Standard OpenSSH + `rsync` (no server daemons) | Specialized LFS HTTP server / S3 backend |
| **Best for** | Large software builds, firmware, toolchains, test datasets | Mixed assets in web apps (images, videos) |

---

### Should I run `git-drev` inside my main source-code repository?
No. `git-drev` creates commits whose subjects are JSON strings (`{"uuid":"...","seq":"..."}`) and uses `git reset --hard` and `git rebase -X theirs` to synchronize branches. Always run `git-drev` in a **dedicated metadata repository** (either standalone or mounted as a Git submodule inside your main source tree). See [Chapter 1: Repository topology](01-philosophy.md#repository-topology-dedicated-repo-vs-git-submodule).

---

### Can I have multiple branches with different baselines?
Yes. Every revision has a unique UUID. When you create a new branch with `git drev create <branch>`, it records the UUID of the commit you branched from as its baseline. When you publish a revision on that branch, `git drev put` hardlink-clones from that specific baseline.

---

### Does `git-drev` work on Windows?
Yes. `git-drev` is supported on Windows:
- Run within [Git Bash](https://gitforwindows.org/) with `rsync` installed via [Scoop](https://scoop.sh/) (`scoop install rsync`).
- Terminal colors, hyperlinks, and dynamic path conversions (`/c/Users/...` to Windows paths) are handled natively.

---

### What happens when I delete a Git branch?
Deleting a Git branch removes the ref pointer in Git, while the remote snapshot directories on the storage server remain intact until pruned.
To inspect snapshots that are no longer referenced by any Git commit, run:
```bash
git drev usage -v
```
To prune unreferenced or expired snapshots, use the retention cleanup script in [Chapter 5: Storage architecture & retention](05-storage-and-retention.md).
