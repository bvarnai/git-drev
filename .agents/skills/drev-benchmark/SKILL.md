---
name: drev-benchmark
description: >-
  Deterministic benchmark agent and runbook for executing the 5-build Eclipse SDK
  real-world performance and storage deduplication benchmark defined in
  docs/user-guide/storage-and-retention.md. Activate when the user asks to run,
  replay, or generate a report for the git-drev benchmark.
---

# `git-drev` Eclipse SDK Benchmark Agent Runbook

This skill defines the deterministic, repeatable procedure for running the real-world **Eclipse SDK integration builds (`I-builds`)** benchmark documented in [Chapter 5: Storage architecture and retention management](../../../docs/user-guide/storage-and-retention.md#6-real-world-benchmark-eclipse-sdk-builds) and generating the benchmark report.

---

## Agent Task Protocol (Execute in Order)

Whenever the user asks to run or replay the benchmark, follow these 4 tasks strictly in sequence:

### Task 1: Prompt User for Target SSH Server & Path
Before running any commands on a remote server, **always ask the user** for the required target parameters (unless already provided in the user's prompt):
1. **Remote SSH host (`SSH_HOST`)**: The SSH host or `~/.ssh/config` alias (e.g., `drs`).
2. **Remote storage path (`REMOTE_PATH`)**: The absolute directory path on the SSH server to use for the benchmark revisions (e.g., `/volume1/storage_drs/data/git-drev-benchmark`).
3. **Cleanup preference (optional)**: Whether to keep the 5 benchmark revisions on the remote server after the report is generated or remove `<REMOTE_PATH>` upon completion (default: keep so `git drev usage -a` state remains inspectable, wiping any pre-existing contents at the start of the run).

---

### Task 2: Build Local `git-drev` Binary & Verify SSH Pre-flight
1. Compile the current `git-drev` binary from the workspace root so the benchmark tests the exact working tree:
   ```bash
   go build -ldflags "-s -w -X 'github.com/bvarnai/git-drev/internal/version.Version=v0.1.0-dev' -X 'github.com/bvarnai/git-drev/internal/version.Commit=$(git rev-parse --short=7 HEAD)' -X 'github.com/bvarnai/git-drev/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" -trimpath -o bin/git-drev ./cmd/git-drev
   ```
2. Verify non-interactive SSH connectivity and check whether `<REMOTE_PATH>` already exists:
   ```bash
   ssh -T "<SSH_HOST>" "echo connected && ls -la '<REMOTE_PATH>' 2>/dev/null || true"
   ```
   - If `<REMOTE_PATH>` already exists and is non-empty, confirm with the user before passing `--clean-before` to wipe previous benchmark artifacts.

---

### Task 3: Execute the Deterministic Benchmark Script
Run the self-contained benchmark script [`scripts/run_benchmark.sh`](./scripts/run_benchmark.sh):

```bash
./.agents/skills/drev-benchmark/scripts/run_benchmark.sh \
  --host "<SSH_HOST>" \
  --path "<REMOTE_PATH>" \
  --bin "./bin/git-drev" \
  --clean-before \
  --output "/tmp/git-drev-benchmark-report.md"
```

#### Supported Script Flags
| Flag | Default | Description |
| :--- | :--- | :--- |
| `--host <host>` | *(Required)* | Target SSH server name or alias. |
| `--path <path>` | *(Required)* | Remote directory path on `<host>` for storing benchmark UUIDs. |
| `--bin <path>` | `./bin/git-drev` | Path to the compiled `git-drev` executable. |
| `--builds <id1,id2,...>` | *(Auto-discover 5)* | Comma-separated list of 5 Eclipse `I-build` IDs (if omitted, automatically discovers the 5 latest consecutive `I-builds` from `download.eclipse.org`). |
| `--cache-dir <dir>` | *(Temp dir)* | Optional directory to cache downloaded `.tar.gz` archives and timings across runs. |
| `--clean-before` | `false` | Recreate `<path>` cleanly on `<host>` before starting the benchmark. |
| `--clean-after` | `false` | Delete `<path>` on `<host>` after capturing the report. |
| `--output <file>` | `stdout` | File path to write the rendered Markdown benchmark report. |

#### What `scripts/run_benchmark.sh` Deterministically Measures
1. **Discovery:** Queries `https://download.eclipse.org/eclipse/downloads/` and selects 5 consecutive daily `Eclipse SDK` integration builds (`eclipse-SDK-<BUILD_ID>-linux-gtk-x86_64.tar.gz`).
2. **Baseline (`curl` + `tar -xzf`):**
   - Downloads each `.tar.gz` archive via `curl` and records download duration (`s`) and compressed archive size (`MiB` and `MB`).
   - Extracts each archive via `tar -xzf` and records extraction duration (`s`), unpacked byte size (`MiB`), disk footprint (`du -sh`), and file count.
   - Compares consecutive unpacked trees ($i-1$ vs. $i$) byte-for-byte using `cmp -s` to compute exact identical file percentages, identical file counts/MiB, and changed file counts/MiB.
3. **Producer (`git drev init` + `create` + `put --stats`):**
   - Initializes a local bare `origin.git` and a Producer metadata repository using `git-drev init -y --host <SSH_HOST> --path <REMOTE_PATH> --workdir data --branch main`.
   - Configures `remote.rsync.get: -c` and `remote.rsync.put: -c` in `.git-drev.yaml` and runs `git drev create main`.
   - Sequentially populates `data/` with builds $1 \dots 5$ and runs `git drev put -s $i --stats`, recording wall-clock time (`s`), `Total bytes sent` (`MiB`), and `Number of regular files transferred`.
4. **Consumer (`git drev select` + `get --stats`):**
   - Clones `origin.git` into an isolated Consumer repository and configures `.git-drev.yaml` (`get: -c`, `put: -c`).
   - Sequentially runs `git drev select <uuid_i>` + `git drev get --stats` for builds $1 \dots 5$ (Build 1 = cold sync into empty `data/`; Builds 2–5 = incremental delta updates), recording wall-clock time (`s`), `Total bytes received` (`MiB`), and `Number of regular files transferred`.
5. **Storage Deduplication (`git drev usage -a`):**
   - Runs `git drev usage -a` and captures the exact output table and server-side hardlinked disk usage (`Total space used`).

---

### Task 4: Report Delivery & Optional Documentation Update
1. Read the generated Markdown report from `/tmp/git-drev-benchmark-report.md` and present it to the user.
2. Ask the user if they would like to update the benchmark tables in:
   - [`docs/user-guide/storage-and-retention.md`](../../../docs/user-guide/storage-and-retention.md#6-real-world-benchmark-eclipse-sdk-builds)
   - [`README.md`](../../../README.md#benchmark-real-world-eclipse-sdk-builds)
