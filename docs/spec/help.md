# Spec: git-drev help

## 1. Command Signature
- **Usage:**
  - Root help: `git-drev --help`, `git-drev -h`, `git-drev help`
  - Subcommand help: `git-drev <subcommand> --help`, `git-drev <subcommand> -h`, `git-drev help <subcommand>`
- **Arguments:**
  - `[<subcommand>]` *(optional)*: Registered subcommand name to view specific help for.
- **Flags:**
  - Standard global flags: `--help` (`-h`), `--version`, `--verbose` (`-v`), `--quiet` (`-q`), `--no-color`.

## 2. Prerequisites & Environment
- **Required host binaries:** None.
- **Environment variables read:** `NO_COLOR`, `GIT_DREV_COLOR`.
- **Working directory requirements (GIT_PREFIX behavior):** Can be invoked from any directory on the host filesystem; repository context is not required.
- **Git state assertions:** Safe to execute in any state or outside a Git repository.

## 3. Core Execution Flow
1. **Argument Inspection:**
   - If no argument is provided (or root `--help`/`-h` flag is used), Cobra generates the root command catalog listing all subcommands and global flags.
   - If a subcommand name is specified, Cobra routes to the corresponding subcommand's help specification.
2. **Standard Output Emission:**
   - Emits formatted usage synopsis, command description, and flag tables directly to `stdout`.
   - Output contains zero log prefixes (`[git-drev]`), ensuring clean formatting in terminal pagers (`man`, `less`).
3. **Exit:**
   - Returns Exit Code `0`.

## 4. Error Handling & Exit Codes
| Exit Code | Error Category | Scenario | Emitted Stderr Pattern |
| :--- | :--- | :--- | :--- |
| `0` | `nil` | Help text printed cleanly to `stdout`. | None. |
| `2` | `errcode.ErrUsage` | Unknown subcommand passed to `help` (e.g., `git-drev help foobar`). | `[git-drev] Error: unknown command "<name>" for "git-drev"` |

## 5. Edge Cases & Behavioral Notes
- **Flag Parsing Priority:** Passing `--help` or `-h` anywhere in argument lists takes precedence over subcommand execution, guaranteeing that accidental destructive commands are never run if help was requested.
- **Terminal Pager Compatibility:** Emits unbuffered, unadorned text directly to `stdout` without decorative ANSI escapes if standard output is redirected or piped.
