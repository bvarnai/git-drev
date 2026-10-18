---
name: git-brx
description: >-
  Runbook for using git-brx for Git branch lifecycle management, ticket branch creation,
  upstream synchronization, pair programming updates, pull requests, and branch cleanup.
  Activate when creating topic branches, working on issues, syncing branches, pushing PRs,
  or managing git workflow.
---

# `git-brx` Git Workflow Runbook

This skill guides the AI assistant in using `git-brx` (or `git brx`) as the unified CLI for branch lifecycle management.

> [!IMPORTANT]
> **Never use raw Git commands** for topic branch creation (`git checkout -b`), force pushing (`git push -f`), or ad-hoc pull request creation when `git-brx` is available. Always use the corresponding `git brx` subcommands.

---

## 1. Quick Reference & Command Matrix

| Stage | `git-brx` Command | Purpose & Behavior |
| :--- | :--- | :--- |
| **Branch Creation** | `git brx create <branch> -y` | Validates against issue tracker (GitHub/Jira), enforces prefix rules (`issue/`, `feature/`, `epic/`), and creates local topic branch. |
| **Branch Selection** | `git brx select [<branch>]` | Fetches remotes and switches working tree (defaults to `main` if omitted). |
| **Upstream Sync** | `git brx sync` | Pulls remote base changes into branch (**rebase** for `issue/`, **merge** for `feature/`). |
| **Pair Update** | `git brx update` | Pulls colleague commits on the current branch using rebase with `--autostash`. |
| **Conflict Resolve** | `git brx resolve` | Launches merge tool on conflicted paths and continues in-flight rebase/merge. |
| **Conflict Abort** | `git brx reset` | Aborts in-flight rebase/merge and resets working copy to clean tracking state. |
| **Publishing** | `git brx publish` | Pushes branch using safe `--force-with-lease` and binds upstream tracking. |
| **Code Review / PR** | `git brx review [<target>]` | Opens Pull Request via API, evaluates path/label reviewer mappings, and excludes author. |
| **Branch Cleanup** | `git brx delete <branch>` | Verifies remote deletion, switches to default branch, and prunes local branch. |

---

## 2. Standard End-to-End Workflow

### Step 1: Topic Branch Creation (`create`)
When starting work on an issue or ticket:
1. Ensure your local default branch is current:
   ```bash
   git brx select main
   ```
2. Determine branch type from issue category / labels:
   - `bug` or standard task $\to$ `issue/<key>` (e.g., `issue/123`, `issue/PROJ-456`)
   - `enhancement` or collaborative feature $\to$ `feature/<key>`
   - `epic` or milestone $\to$ `epic/<key>`
3. Create the branch non-interactively using `-y`:
   ```bash
   git brx create issue/123 -y
   ```
   *(If offline or bypassing tracker validation, add `-o`/`--offline`)*

### Step 2: Local Commits
- Make incremental changes.
- Use Conventional Commits referencing the issue key in scope or title:
  ```bash
  git commit -m "feat(#123): implement user authentication validator"
  ```

### Step 3: Upstream Synchronization (`sync` & `update`)
- **Syncing with Base Branch (`main`):**
  Run regularly before publishing or opening review:
  ```bash
  git brx sync
  ```
  - For `issue/*`: Executes a linear rebase on top of latest `main`.
  - For `feature/*`: Creates a true merge commit from `main`.
- **Syncing with Collaborator on Shared Branch:**
  If a teammate pushed commits to the same topic branch:
  ```bash
  git brx update
  ```

### Step 4: Conflict Handling (`resolve` / `reset`)
If `git brx sync` or `update` encounters a conflict (Exit Code 5):
1. **To Resolve:** Inspect conflicted files (`git status`), edit and fix markers, then run:
   ```bash
   git brx resolve
   ```
2. **To Abort:** Restore working copy cleanly to pre-sync state:
   ```bash
   git brx reset
   ```

### Step 5: Publishing (`publish`)
Push the local branch and configure remote tracking safely:
```bash
git brx publish
```
*(If rejected due to newer remote commits, run `git brx update` first).*

### Step 6: Code Review & Pull Request (`review`)
Create the PR with automated reviewer assignment and merge instructions:
```bash
git brx review
```
- Targets default branch (`main`) automatically.
- Assigns reviewers defined in `.git-brx.yaml` (`review.mapping.paths`, `review.mapping.labels`).
- For `epic/*` branches, it automatically specifies non-squash true merge instructions.

### Step 7: Post-Merge Cleanup (`delete`)
Once the PR is merged and remote branch is deleted on the SCM platform:
```bash
git brx delete <branch-name>
```

---

## 3. Automation Guidelines for AI Agents

- **Flag Usage:** Always pass `-y` / `--yes` to commands that support confirmation prompts (`create`) to avoid hanging non-interactive sessions.
- **Diagnostic Inspection:** Check exit codes per spec:
  - `0`: Success / already exists
  - `3`: Git repository precondition error (detached HEAD, shallow clone)
  - `4`: Remote network / unreachable error
  - `5`: Merge/rebase conflict active (invoke `resolve` or `reset`)
  - `6`: Tracker or SCM API authorization/not found error
  - `8`: Configuration regex or branch naming violation
