# Chapter 4: Project Hooks and CI/CD Automation

`git-drev` provides customizable lifecycle hooks that allow teams and CI/CD pipelines to inject custom build metadata, notify external systems, and correlate revision snapshots with CI build runs.

---

## 1. Supported Project Hooks

`git-drev` looks for executable hook scripts in the root directory of your repository:

| Hook Script | Trigger Point | Execution Context | Common Use Cases |
| :--- | :--- | :--- | :--- |
| `.git-drev-put-hook` | Immediately after `rsync` upload succeeds during `git drev put`, before the Git commit is finalized. | Invoked in repository root. Subprocess environment receives revision metadata. | Injecting CI build URLs, commit hashes, or generating checksum manifest files inside the revision directory. |
| `.git-drev-info-hook` | Triggered when `git drev info` is executed. | Invoked in repository root. Output is appended to the standard `info` output. | Displaying linked CI build numbers, test results, or download metrics. |

---

## 2. Injected Environment Variables

When executing a hook, `git-drev` sanitizes the environment (`LC_ALL=C`, `LANG=C`) and injects the following contextual variables:

| Environment Variable | Description | Example Value |
| :--- | :--- | :--- |
| `GIT_DREV_VERSION` | Semantic version of the running `git-drev` binary. | `1.0.0` |
| `GIT_DREV_UUID` | Active revision UUID. | `4a123f89-8d7b-4b10-8f6a-49321ef19b12` |
| `GIT_DREV_SEQ` | Monotonic revision sequence number on the active branch. | `14` |
| `GIT_DREV_BRANCH` | Current branch name (or `HEAD` if detached). | `main` |

---

## 3. Hook Execution Rules & Timeout

1. **Executable Permissions:** On POSIX systems (Linux, macOS, Git Bash), hooks must have the executable bit set (`chmod +x .git-drev-put-hook`).
2. **Timeout Control:** By default, hook execution is capped at **5 minutes**. You can adjust this timeout by setting `GIT_DREV_HOOK_TIMEOUT`:
   ```bash
   export GIT_DREV_HOOK_TIMEOUT=10m
   ```
3. **Failure Handling:** If `.git-drev-put-hook` exits with a non-zero status code:
   - `git drev put` immediately halts execution.
   - The pending Git commit is aborted.
   - The remote staging directory is safely cleaned up to prevent leaving half-published snapshots on the storage host.

---

## 4. Hook Examples

### Example 1: Checksum Manifest Generation (`.git-drev-put-hook`)
Generate a `SHA256SUMS` manifest inside `workdir/` before recording the revision:

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "[hook] Generating sha256 checksums for revision ${GIT_DREV_UUID}..."
cd data
sha256sum $(find . -type f ! -name "SHA256SUMS") > SHA256SUMS
echo "[hook] Checksums generated successfully."
```

### Example 2: Correlating Jenkins CI Build Info (`.git-drev-put-hook`)
If running inside Jenkins, record the build URL and executor details in a metadata file:

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${BUILD_URL:-}" ]]; then
  cat <<EOF > data/.ci-metadata.json
{
  "ci_system": "Jenkins",
  "build_number": "${BUILD_NUMBER}",
  "build_url": "${BUILD_URL}",
  "git_commit": "${GIT_COMMIT}",
  "drev_uuid": "${GIT_DREV_UUID}",
  "drev_sequence": ${GIT_DREV_SEQ}
}
EOF
fi
```

### Example 3: Permanent Build Logs & SBOMs in Git (`.git-drev-put-hook`)
Because `git drev put` automatically stages the repository root (`git add --all`) before committing, any lightweight files placed outside `workdir/` are committed directly into Git. This permanently preserves build provenance even if older remote snapshots are eventually purged:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Archive the build log and compiler options directly in the Git repository
mkdir -p build-logs
cp /tmp/jenkins-build-${BUILD_NUMBER:-0}.log "build-logs/rev-${GIT_DREV_SEQ}-${GIT_DREV_UUID:0:8}.log"
```

### Example 4: Custom Info Display (`.git-drev-info-hook`)
When developers run `git drev info`, display the linked CI build URL from the snapshot:

```bash
#!/usr/bin/env bash
set -euo pipefail

METADATA_FILE="data/.ci-metadata.json"
if [[ -f "${METADATA_FILE}" ]]; then
  BUILD_URL=$(grep '"build_url"' "${METADATA_FILE}" | cut -d'"' -f4)
  echo "CI Build:  ${BUILD_URL}"
fi
```

---

## 5. CI/CD Integration Patterns

### Jenkins Pipeline (`Jenkinsfile`)
Here is a complete declarative Jenkins pipeline using `git-drev` as a Producer:

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
                        git drev put -m "build(#${BUILD_NUMBER}): compiled release 3.0 artifacts"
                    '''
                }
            }
        }
    }
}
```

### GitHub Actions Workflow (`.github/workflows/build.yml`)
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
          git drev put -m "nightly: build run ${{ github.run_id }}"
```
