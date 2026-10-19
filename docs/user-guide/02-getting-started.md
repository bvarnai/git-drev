# Chapter 2: Getting Started

This guide walks you through setting up `git-drev`, configuring SSH access to your remote storage server, and initializing your first revision repository.

---

## 1. Prerequisites

### Client Prerequisites
`git-drev` requires the following standard CLI tools in your system `$PATH`:
- **Git** (version 2.25.0 or later)
- **OpenSSH client** (`ssh`)
- **rsync** (version 3.1.0 or later)

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
- Install [Git for Windows](https://gitforwindows.org/) (which provides Git Bash and OpenSSH).
- Install `rsync` via [Scoop](https://scoop.sh/):
  ```powershell
  scoop install rsync
  ```
- Or install `git-drev` directly via the Scoop bucket:
  ```powershell
  scoop bucket add bvarnai https://github.com/bvarnai/scoop-bucket
  scoop install git-drev
  ```

---

### Storage Server Prerequisites
The remote storage host requires:
- An OpenSSH server (`sshd`) with public key authentication enabled.
- `rsync` (version 3.1.0 or later).
- Standard POSIX utilities: `cp` (supporting `-a` and `-l` hardlink flags) and `mkdir`.
- Write permissions for the connecting user on the target storage directory.

---

## 2. Installation

### Option A: Binary Release
Download the precompiled archive for your OS and architecture from [GitHub Releases](https://github.com/bvarnai/git-drev/releases):

```bash
# Example for Linux AMD64
curl -sSL https://github.com/bvarnai/git-drev/releases/latest/download/git-drev_linux_amd64.tar.gz | tar -xz
sudo mv git-drev /usr/local/bin/
```

### Option B: Using Go Install
If you have Go 1.22+ installed:
```bash
go install github.com/bvarnai/git-drev/cmd/git-drev@latest
```

### Option C: Build from Source
```bash
git clone https://github.com/bvarnai/git-drev.git
cd git-drev
go build -o git-drev ./cmd/git-drev
sudo mv git-drev /usr/local/bin/
```

Verify the installation:
```bash
git-drev --version
# Output: git-drev version 1.0.0 (commit: abc1234, built: 2026-10-19)
```

> [!TIP]
> Since the binary is named `git-drev`, Git automatically recognizes it as a Git extension. You can invoke it either as `git-drev <cmd>` or as `git drev <cmd>`.

---

## 3. SSH Configuration

`git-drev` communicates with the storage host over OpenSSH. For seamless automated operation without interactive password prompts, configure SSH key-based authentication.

### Generate and Deploy an SSH Key
```bash
# Generate a modern Ed25519 key pair if you don't already have one
ssh-keygen -t ed25519 -C "drev-client@example.com"

# Copy the public key to the storage server
ssh-copy-id -i ~/.ssh/id_ed25519.pub user@storage.internal.net
```

### Configure `~/.ssh/config`
Using SSH host aliases simplifies configuration across all team members:

```ssh-config
Host storage-server
    HostName storage.internal.net
    User builduser
    IdentityFile ~/.ssh/id_ed25519
    Port 22
```

Test the connection:
```bash
ssh storage-server "rsync --version"
```

---

## 4. Initializing a Metadata Repository

### Step 1: Create or Clone a Git Repository
Create a fresh Git repository to store revision metadata:
```bash
mkdir my-build-storage
cd my-build-storage
git init
```

### Step 2: Configure `.git-drev.yaml`
Create a `.git-drev.yaml` configuration file in the repository root:

```yaml
# Directory containing active revision files (relative to repo root)
workdir: data

# Default branch used when no branch is specified in 'select'
branch:
  default: main

# Remote storage server configuration
remote:
  host: storage-server
  path: /srv/drev/my-build-storage
  rsync: -aHAX --info=progress2 --delete-during
```

### Step 3: Configure `.gitignore`
The working directory contains the actual large files, which **must never be committed to Git**:

```bash
echo "/data" >> .gitignore
echo ".git-drev.local.yaml" >> .gitignore
```

### Step 4: Commit Initial Metadata Configuration
```bash
git add .git-drev.yaml .gitignore
git commit -m "chore: initialize git-drev configuration"
```

Verify your configuration with `git drev config`:
```bash
git drev config
```
This prints the cascading configuration and provenance source for each parameter.

---

## 5. Your First Revision

Now that your repository is set up, create your initial revision branch and push your first directory snapshot:

```bash
# 1. Create a new revision branch
git drev create main

# 2. Populate your working directory with build files
mkdir -p data
echo "Build v1.0 payload" > data/build.txt
dd if=/dev/urandom of=data/artifact.bin bs=1M count=100

# 3. Publish the revision to remote storage and record the metadata commit
git drev put -m "feat: initial v1.0 build payload"
```

Congratulations! Your first revision is securely stored on the remote host and tracked via a lightweight Git commit.
