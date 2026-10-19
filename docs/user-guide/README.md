# User Guide & Wiki

Welcome to the **`git-drev` user guide**. This documentation provides comprehensive tutorials, conceptual deep dives, command catalogs, and troubleshooting advice for software development teams and CI/CD engineers using `git-drev`.

---

## Table of contents

| Chapter | Title | Focus Area |
| :---: | :--- | :--- |
| **01** | [The git-drev Philosophy](01-philosophy.md) | Producer/Consumer paradigm, metadata-only Git repositories, and advantages over Git LFS & S3 |
| **02** | [Getting Started](02-getting-started.md) | Installation, OpenSSH configuration, repository initialization (`.git-drev.yaml`), and initial revision commit |
| **03** | [The Revision Lifecycle](03-revision-lifecycle.md) | Complete workflow: creating branches (`create`), publishing revisions (`put`), fetching updates (`update`), downloading snapshots (`get`), and switching revisions (`select`) |
| **04** | [Project Hooks & CI/CD Automation](04-hooks-and-ci.md) | `.git-drev-put-hook` and `.git-drev-info-hook`, environment injection, Jenkins pipelines, and GitHub Actions |
| **05** | [Storage Architecture & Retention](05-storage-and-retention.md) | Remote storage layout (`<path>/<uuid>`), hardlink baseline cloning (`cp -al`), rsync tuning, and retention cleanup (`cleanup.sh`) |
| **06** | [Complete Command Reference](06-command-reference.md) | Exhaustive reference for all subcommands, accepted flags, exit codes, and porcelain output |
| **07** | [Troubleshooting & FAQ](07-troubleshooting-faq.md) | Diagnosing SSH issues, detached HEAD recovery, index locks, and frequently asked questions |

---

## Reading paths

### For CI / DevOps engineers (producers)
If you are automating build pipelines and artifact distribution:
1. Read [The git-drev Philosophy](01-philosophy.md) to understand how `git-drev` avoids Git repository bloat
2. Follow [Getting Started](02-getting-started.md) to configure SSH keys and `.git-drev.yaml`
3. Review [Project Hooks & CI/CD Automation](04-hooks-and-ci.md) for Jenkins/GitHub Actions pipeline templates
4. Set up the automated cleanup script in [Storage Architecture & Retention](05-storage-and-retention.md)

### For developers & testers (consumers)
If you need to download and test exact build snapshots without compiling:
1. Start with the [Quick Start in the README](../../README.md#quick-start)
2. Read [The Revision Lifecycle](03-revision-lifecycle.md) to learn how to switch snapshots (`select`) and download revisions (`get` / `get --latest`)
3. Check [Complete Command Reference](06-command-reference.md) for flag options and porcelain queries

---

## Technical specifications
For internal Go implementation details, error code contracts, and architecture diagrams, consult the [Technical specifications](../spec/global.md).
