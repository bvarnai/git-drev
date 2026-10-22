# Chapter 4: Project hooks and CI/CD automation

`git-drev` provides customizable lifecycle hooks that allow teams and CI/CD pipelines to inject custom build metadata, notify external systems, and correlate revision snapshots with CI build runs.

---

## 1. Supported project hooks

`git-drev` looks for executable hook scripts in the root directory of your repository:

| Hook script | Trigger point | Execution context | Common use cases |
| :--- | :--- | :--- | :--- |
| `.git-drev-put-hook` | Immediately after `rsync` upload succeeds during `git drev put`, before metadata staging and `git commit` | Invoked in repository root. Subprocess environment receives revision metadata | Injecting CI build URLs, commit hashes, build logs, or generating checksum manifest files |
| `.git-drev-info-hook` | Triggered when `git drev info` is executed | Invoked in repository root. Output is streamed directly to `stdout` | Displaying linked CI build numbers, test results, or download metrics |

---

## 2. Injected environment variables

When executing a hook, `git-drev` sanitizes the environment (`LC_ALL=C`, `LANG=C`) and injects the following contextual variables:

| Environment variable | Description | Example value |
| :--- | :--- | :--- |
| `GIT_DREV_VERSION` | Semantic version of the running `git-drev` binary | `1.0.0` |
| `GIT_DREV_UUID` | Active revision UUID | `4a123f89-8d7b-4b10-8f6a-49321ef19b12` |
| `GIT_DREV_SEQ` | Monotonic revision sequence number on the active branch | `1729348200` |
| `GIT_DREV_BRANCH` | Current branch name (or `HEAD` if detached in `info`) | `main` |
| `GIT_DREV_WORK_DIR` | Absolute path to the configured working directory | `/workspace/repo/data` |
| `GIT_DREV_ROOT` | Absolute path to the Git repository root | `/workspace/repo` |

---

## 3. Hook execution rules & timeout

1. **Executable scripts & legacy compatibility:** Hooks can be standalone executable scripts (`chmod +x .git-drev-put-hook` with a `#!` shebang) or shell scripts defining `put_hook` / `info_hook` functions.
2. **Timeout control:** By default, hook execution is capped at **5 minutes**. You can adjust this timeout by setting `GIT_DREV_HOOK_TIMEOUT`:
   ```bash
   export GIT_DREV_HOOK_TIMEOUT=10m
   ```
3. **Failure handling:** If `.git-drev-put-hook` exits with a non-zero status code:
   - `git drev put` immediately halts execution.
   - The pending Git commit is aborted.
   - The remote staging directory is removed so half-published snapshots are not left on the storage host.
4. **Background processes & file descriptors:** If your hook script forks background or asynchronous tasks (e.g., reporting metrics or triggering webhook daemons), you must explicitly decouple all standard streams:
   ```bash
   # Decouple standard streams when backgrounding tasks
   nohup /path/to/notifier >/dev/null 2>&1 </dev/null &
   ```
   > [!TIP]
   > Subprocess execution in `git-drev` enforces process group management and a 3-second grace period on standard stream closure. Background jobs that retain inherited file descriptors will prevent clean stream teardown until the grace timer forces a termination.

---

## 4. Hook examples

### Example 1: Checksum manifest generation (`.git-drev-put-hook`)
Generate a tracked `SHA256SUMS` manifest of `workdir/` in the repository root so it is committed alongside the revision metadata:

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "[hook] Generating sha256 checksums for revision ${GIT_DREV_UUID}..."
(cd "${GIT_DREV_WORK_DIR}" && find . -type f -print0 | sort -z | xargs -0 sha256sum) > "${GIT_DREV_ROOT}/SHA256SUMS"
echo "[hook] Checksums written to SHA256SUMS."
```

### Example 2: Correlating Jenkins CI build info (`.git-drev-put-hook`)
If running inside Jenkins, record the build URL and executor details in a tracked metadata file:

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${BUILD_URL:-}" ]]; then
  cat <<EOF > .ci-metadata.json
{
  "ci_system": "Jenkins",
  "build_number": "${BUILD_NUMBER}",
  "build_url": "${BUILD_URL}",
  "git_commit": "${GIT_COMMIT}",
  "drev_uuid": "${GIT_DREV_UUID}",
  "drev_sequence": "${GIT_DREV_SEQ}"
}
EOF
fi
```

### Example 3: Permanent build logs & SBOMs in Git (`.git-drev-put-hook`)
Because `git drev put` stages repository metadata (excluding `workdir/`) before committing, any lightweight files placed outside `workdir/` are committed directly into Git. This preserves build provenance even after older remote snapshots are purged:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Archive the build log and compiler options directly in the Git repository
mkdir -p build-logs
cp /tmp/jenkins-build-${BUILD_NUMBER:-0}.log "build-logs/rev-${GIT_DREV_SEQ}-${GIT_DREV_UUID:0:8}.log"
```

### Example 4: Custom info display (`.git-drev-info-hook`)
When developers run `git drev info`, display the revision metadata and linked CI build URL:

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "Branch:    ${GIT_DREV_BRANCH:-<detached>}"
echo "UUID:      ${GIT_DREV_UUID}"
echo "Sequence:  ${GIT_DREV_SEQ}"

METADATA_FILE=".ci-metadata.json"
if [[ -f "${METADATA_FILE}" ]]; then
  BUILD_URL=$(grep '"build_url"' "${METADATA_FILE}" | cut -d'"' -f4)
  echo "CI Build:  ${BUILD_URL}"
fi
```

---

## 5. CI/CD integration patterns

### Jenkins pipeline (`Jenkinsfile`)
Here is a declarative Jenkins pipeline using `git-drev` as a Producer:

```groovy
pipeline {
    agent { label 'linux-builder' }

    environment {
        GIT_DREV_HOOK_TIMEOUT = '10m'
    }

    stages {
        stage('Checkout Revision Repository') {
            steps {
                git branch: 'main', credentialsId: 'github-deploy-key', url: 'git@github.com:myorg/build-storage.git'
            }
        }

        stage('Compile & Package') {
            steps {
                sh '''
                    # Ensure branch is selected
                    git drev create release/3.0

                    # Run build into the configured workdir
                    mkdir -p data
                    make build-embedded-image OUTPUT_DIR=data/
                '''
            }
        }

        stage('Publish Revision') {
            steps {
                sshagent(['storage-server-key']) {
                    sh '''
                        git drev put -s "${BUILD_NUMBER}"
                    '''
                }
            }
        }
    }
}
```

### GitHub Actions workflow (`.github/workflows/build.yml`)
```yaml
name: Nightly Build Publisher

on:
  schedule:
    - cron: '0 2 * * *'
  workflow_dispatch:

jobs:
  build-and-publish:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout Metadata Repository
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Install git-drev
        run: |
          curl -sSL https://github.com/bvarnai/git-drev/releases/latest/download/git-drev_linux_amd64.tar.gz | tar -xz
          sudo mv git-drev /usr/local/bin/

      - name: Configure SSH Key
        uses: webfactory/ssh-agent@v0.9.0
        with:
          ssh-private-key: ${{ secrets.STORAGE_SSH_KEY }}

      - name: Build Artifacts
        run: |
          mkdir -p data
          python3 generate_large_datasets.py --out data/

      - name: Publish Revision
        run: |
          git drev put -s "${{ github.run_number }}"
```
