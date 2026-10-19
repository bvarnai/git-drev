# Contributing to git-drev

Thank you for your interest in contributing to `git-drev`! We welcome bug reports, documentation enhancements, and pull requests that adhere to our architectural standards and workflow rules.

---

## 🎯 Guiding Principles

Before proposing new features or modifying existing code, please review our core architectural rules:

1. **Spec-Driven Parity:** All subcommands and CLI flags must strictly adhere to the technical specifications in [`docs/spec/`](docs/spec/global.md). Any change in CLI behavior must be accompanied by updates to the corresponding spec files.
2. **Deterministic Plumbing over Porcelain:** External `git` invocations must use deterministic plumbing commands (`rev-parse`, `symbolic-ref`, `show-ref`, `for-each-ref -z`, `log -z`) rather than high-level porcelain commands.
3. **Stream Separation:** 
   - `stdout` is strictly reserved for primary command results, machine-parseable porcelain (`name`, `info`, `usage` tables, `config`), and `--help`/`--version`.
   - `stderr` is used exclusively for operational progress logs (`[git-drev]`), warnings, hints, and error diagnostics.
4. **Zero Heavyweight Dependencies:** We use the Go standard library (`encoding/json`, `crypto/rand`, `os`, `path/filepath`, `context`) and standard CLI dependencies (`github.com/spf13/cobra`, `gopkg.in/yaml.v3`). We never import heavyweight Git libraries (such as `go-git`) or shell out to legacy utilities (`jq`, `sed`, `awk`, `stat`, `rm`).

---

## 🛠️ Development Setup

### Prerequisites
- [Go](https://golang.org/) >= 1.22
- [Git](https://git-scm.com/) >= 2.25.0
- [OpenSSH](https://www.openssh.com/) client (`ssh`)
- [rsync](https://rsync.samba.org/) >= 3.1.0

### Clone and Build
```bash
git clone https://github.com/bvarnai/git-drev.git
cd git-drev

# Build development binary to bin/
mkdir -p bin
go build -o ./bin/git-drev ./cmd/git-drev

# Verify build
./bin/git-drev --version
```

---

## 🧪 Testing Guidelines

We enforce rigorous test coverage across all subcommands and internal packages:

1. **Run All Unit & Integration Tests:**
   ```bash
   go test -v -race ./...
   ```

2. **Verify Code Formatting & Static Analysis:**
   ```bash
   # Code formatting (all files must be formatted with standard gofmt)
   gofmt -s -w .

   # Verify zero unformatted files exist
   test -z "$(gofmt -l .)"

   # Go static analysis
   go vet ./...
   ```

3. **Integration Tests:**
   Disposable integration tests reside in `test/integration/`. Tests must create disposable repositories using `t.TempDir()` and must never touch the host machine's `~/.gitconfig` or global Git state.

---

## 🌿 Git Branch & PR Workflow

We follow the opinionated branch workflow managed by `git-brx`:

1. **Create an Issue:** Every bug fix or feature must have a corresponding GitHub issue.
2. **Create Branch:**
   ```bash
   git brx create issue/<number> -y
   ```
3. **Synchronize & Rebase:**
   ```bash
   git brx sync
   ```
4. **Publish & Review:**
   ```bash
   git brx publish
   git brx review
   ```
5. **Clean Up After Merge:**
   ```bash
   git brx select main
   git brx update
   git brx delete issue/<number>
   ```
