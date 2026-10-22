# Chapter 2: Getting started

This guide walks you through installing `git-drev`, configuring SSH access to your storage server, initializing a dedicated metadata repository, and running your first Producer/Consumer round-trip.

---

## 1. Prerequisites

### Client prerequisites
`git-drev` requires the following tools in your `$PATH`:
- **Git** (2.25.0 or later)
- **OpenSSH client** (`ssh`)
- **rsync** (3.1.0 or later)

#### Ubuntu / Debian
```bash
sudo apt-get update
sudo apt-get install -y git openssh-client rsync
```

#### macOS (Homebrew)
```bash
brew install git openssh rsync
```

#### Windows (Git for Windows / Scoop)
- Install [Git for Windows](https://gitforwindows.org/) (provides Git Bash and OpenSSH).
- Install `rsync` and `git-drev` via [Scoop](https://scoop.sh/):
  ```powershell
  scoop install rsync
  scoop bucket add bvarnai https://github.com/bvarnai/scoop-bucket
  scoop install git-drev
  ```

---

### Storage server prerequisites
The remote storage host (`remote.host`) must be a **Linux** (or Linux-compatible) server with:
- An OpenSSH server (`sshd`) with public key authentication enabled
- `rsync` (3.1.0 or later)
- GNU coreutils / standard Linux utilities: `/bin/sh`, `cp` (supporting `-a` and `-l` hardlink flags), `du -sh`, `stat -c %Y`, and `rm -rf`
- `bash` (4.0+) and `git` if running the server-side [`scripts/cleanup.sh`](../../scripts/cleanup.sh) retention script
- Write permissions for the SSH user on the target storage directory (e.g., `/srv/drev/`)

---

## 2. Installation

### Option A: Precompiled binary release
Download the archive for your OS and architecture from [GitHub Releases](https://github.com/bvarnai/git-drev/releases):

```bash
# Example for Linux AMD64
curl -sSL https://github.com/bvarnai/git-drev/releases/latest/download/git-drev_linux_amd64.tar.gz | tar -xz
sudo mv git-drev /usr/local/bin/
```

### Option B: Via `go install`
If you have Go 1.22+ installed:
```bash
go install github.com/bvarnai/git-drev/cmd/git-drev@latest
```

### Option C: Build from source
```bash
git clone https://github.com/bvarnai/git-drev.git
cd git-drev
go build -o git-drev ./cmd/git-drev
sudo mv git-drev /usr/local/bin/
```

Verify the installation:
```bash
git-drev --version
```

> [!TIP]
> Because the binary is named `git-drev`, Git automatically discovers it as a subcommand when placed in your `$PATH`. You can run either `git-drev <cmd>` or `git drev <cmd>`.

---

## 3. SSH configuration

`git-drev` runs non-interactive `ssh -T` and `rsync` sessions (`GIT_TERMINAL_PROMPT=0`), so passwordless SSH key authentication (or `ssh-agent`) is required.

> [!TIP]
> **Read-write Producer vs. shared read-only Consumer keys:**
> You do not need to register separate SSH keys on the server for every developer unless you prefer per-user keys. For maximum safety and minimal administrative overhead:
> - **Producer (CI):** Use a dedicated **read-write** SSH user/key (e.g., `drev-rw`) that owns `/srv/drev` (mode `0755`) and publishes snapshots via `git drev put`.
> - **Consumers (developers / testers):** Share a single **read-only** SSH user/key (e.g., `drev-ro` with read and traverse permissions on `/srv/drev`) across the team. This guarantees consumers can run `get` and `usage` while preventing accidental writes or deletions on the storage host.

### Generate and deploy an SSH key
```bash
# Generate an Ed25519 key pair if you don't already have one
ssh-keygen -t ed25519 -C "drev-client@example.com"

# Copy the public key to the storage server
ssh-copy-id -i ~/.ssh/id_ed25519.pub builduser@storage.internal.net
```

### Configure `~/.ssh/config`
Defining an SSH host alias keeps `.git-drev.yaml` portable across machines and CI runners:

```ssh-config
Host storage-server
    HostName storage.internal.net
    User builduser
    IdentityFile ~/.ssh/id_ed25519
    Port 22
```

Verify non-interactive access and check that the target base directory exists:
```bash
ssh -T storage-server "mkdir -p /srv/drev && rsync --version"
```

---

## 4. Initializing a metadata repository

As covered in [Chapter 1](philosophy.md#repository-topology-dedicated-repo-vs-git-submodule), `git-drev` operates in a dedicated metadata repository (which can be used standalone or mounted as a Git submodule).

### Step 1: Create a metadata repository and add a Git remote
```bash
mkdir my-build-storage
cd my-build-storage
git init
git remote add origin git@github.com:myorg/my-build-storage.git
```

### Step 2: Configure `.git-drev.yaml`
Create `.git-drev.yaml` in the repository root:

```yaml
# Directory containing active revision files (relative to repo root)
workdir: data

# Default branch used when no branch is specified in 'select'
branch:
  default: main

# Remote storage server configuration
remote:
  host: storage-server
  path: /srv/drev/{repo}
  rsync:
    # -c (--checksum) skips byte-identical files even when build tools update mtimes,
    # preserving server-side cp -al hardlinks across revisions
    get: -c
    put: -c
```

> [!TIP]
> `remote.path` supports `{repo}` and `{project}` placeholders, which expand to the repository name (derived from the `origin` URL slug or directory name). If `remote.path` is defined in a user-global config (`~/.config/git-drev/config.yaml`) without a placeholder, `git-drev` automatically appends `/{repo}` to isolate repositories on the server.

### Step 3: Ignore `workdir` in `.gitignore`
The working directory holds the large payload files and **must be ignored by Git**:

```bash
echo "/data" >> .gitignore
```

### Step 4: Commit initial metadata configuration
```bash
git add .git-drev.yaml .gitignore
git commit -m "chore: initialize git-drev configuration"
```

Verify your effective configuration and see which tier provided each setting:
```bash
git drev config --sources
```

---

## 5. Your first Producer & Consumer round-trip

### Publish a snapshot (Producer)
Initialize the `main` revision branch, populate `data/`, and upload your first revision:

```bash
# 1. Create or switch to the revision branch
git drev create main

# 2. Populate workdir (./data) with sample artifacts
mkdir -p data
echo "Build v1.0 payload" > data/build.txt
dd if=/dev/urandom of=data/artifact.bin bs=1M count=50

# 3. Upload data/ to storage-server and push the metadata commit
git drev put
```

Inspect the commit that `git-drev` created:
```bash
git log -n 1 --oneline
# a1b2c3d {"uuid":"018f24a0-7b32-7e9b-a012-3456789abcde","seq":"1729348200"}
```

### Download the snapshot on another machine (Consumer)
Now any teammate or test runner can clone the lightweight metadata repository and pull the snapshot into `data/`:

```bash
git clone git@github.com:myorg/my-build-storage.git
cd my-build-storage

# Select the branch and download the revision payload into ./data
git drev select main
git drev get
```
