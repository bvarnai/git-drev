# User guide & wiki

Welcome to the **`git-drev` user guide**. These chapters cover day-to-day workflows, repository layout patterns, CI/CD automation, storage retention, and the complete CLI contract.

---

## Table of contents

| Chapter | Title | Focus area |
| :---: | :--- | :--- |
| **01** | [The git-drev philosophy](philosophy.md) | Architecture, repository layout patterns (standalone vs. submodule), and comparison with Git LFS & S3 |
| **02** | [Getting started](getting-started.md) | Installation, OpenSSH setup, initializing a metadata repository, and your first Producer/Consumer round-trip |
| **03** | [The revision lifecycle](revision-lifecycle.md) | Creating branches (`create`), publishing snapshots (`put`), switching revisions (`select`), and downloading payloads (`get` / `update`) |
| **04** | [Project hooks & CI/CD automation](hooks-and-ci.md) | `.git-drev-put-hook` and `.git-drev-info-hook`, injected environment variables, Jenkins pipelines, and GitHub Actions |
| **05** | [Storage architecture & retention](storage-and-retention.md) | Remote UUID layout, hardlink baseline cloning (`cp -al`), `rsync` tuning, and retention cleanup scripts |
| **06** | [Complete command reference](command-reference.md) | Reference for all subcommands, flags, arguments, and exit codes |
| **07** | [Troubleshooting & FAQ](troubleshooting-faq.md) | Diagnosing SSH issues, detached `HEAD` state, index locks, concurrency retries, and FAQs |

---

## Reading paths

### For CI / DevOps engineers (Producers)
1. Read [The git-drev philosophy](philosophy.md) to choose between a standalone metadata repository or a Git submodule layout.
2. Follow [Getting started](getting-started.md) to configure SSH keys and `.git-drev.yaml`.
3. Review [Project hooks & CI/CD automation](hooks-and-ci.md) for Jenkins and GitHub Actions pipeline templates.
4. Set up a retention policy using [Storage architecture & retention](storage-and-retention.md).

### For developers & testers (Consumers)
1. Start with the [Quick start in the README](../../README.md#quick-start).
2. Read [The revision lifecycle](revision-lifecycle.md) to switch revisions (`select`) and synchronize your working directory (`get` / `get --latest`).
3. Keep [Complete command reference](command-reference.md) handy for flags and exit codes.

---

## Technical specifications
For internal Go implementation details, plumbing contracts, and error matrices, see the [Technical specifications](../spec/global.md).
