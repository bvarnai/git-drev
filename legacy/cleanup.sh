#!/bin/bash
# drs-cleanup.sh: Server-side cleanup script for Directory Revision Storage (drs)
#
# This script purges obsolete directory revisions from the remote storage path
# based on the git metadata repository.
#
# Rules:
# 1. Keep the last N commits (default: 5) for any open branch.
# 2. Keep all commits from the last M days (default: 30) for any open branch.
# 3. Never delete artifacts for tagged commits.
# 4. If a branch is deleted, its commits are removed from the keep list,
#    meaning their revision artifacts are deleted unless kept by another active branch or tag.
#
# Integration with Cron:
# You can run this script via cron to automate cleanup.
#
# Example cron configuration for remote repositories (e.g. GitHub):
# 0 2 * * * git --git-dir=/home/drs/drs-metadata.git fetch origin --prune --tags && /home/drs/cleanup.sh --days 30 --commits 5 /home/drs/drs-metadata.git /home/drs/drs-home/myproject >> /home/drs/drs-cleanup.log 2>&1
#
# Note: Ensure the user running the cron job has read/write permissions to both the git repository and the storage directory, and has SSH key access to the remote repository.

set -euo pipefail

# Constants
readonly UUID_PATTERN='"uuid":"([^"]+)"'
readonly UUID_REGEX='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'

show_help() {
  cat << EOF
Usage: $(basename "$0") [options] <git-dir> <storage-dir>

Options:
  -n, --dry-run      Show what would be deleted without deleting anything.
  -d, --days <N>     Number of days of commits to keep on open branches (default: 30).
  -c, --commits <N>  Number of latest commits per open branch to keep (default: 5).
  -h, --help         Show this help message.

Integration with Cron (e.g. for GitHub hosted metadata):
  1. Clone your repo as a bare clone on the storage server:
     git clone --bare git@github.com:username/repo.git /home/drs/drs-metadata.git
  2. Setup a cron job to fetch updates and run cleanup:
     0 2 * * * git --git-dir=/home/drs/drs-metadata.git fetch origin --prune --tags && /home/drs/cleanup.sh /home/drs/drs-metadata.git /home/drs/drs-home/myproject
EOF
}

main() {
  local dry_run="false"
  local days=30
  local commits=5

  # Parse options
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -n|--dry-run)
        dry_run="true"
        shift
        ;;
      -d|--days)
        if [[ -z "${2:-}" || ! "$2" =~ ^[0-9]+$ ]]; then
          echo "Error: --days requires a positive integer value" >&2
          exit 1
        fi
        days="$2"
        shift 2
        ;;
      -c|--commits)
        if [[ -z "${2:-}" || ! "$2" =~ ^[0-9]+$ ]]; then
          echo "Error: --commits requires a positive integer value" >&2
          exit 1
        fi
        commits="$2"
        shift 2
        ;;
      -h|--help)
        show_help
        exit 0
        ;;
      -*)
        echo "Unknown option: $1" >&2
        show_help
        exit 1
        ;;
      *)
        break
        ;;
    esac
  done

  if [[ $# -ne 2 ]]; then
    echo "Error: Missing arguments." >&2
    show_help
    exit 1
  fi

  local git_dir="$1"
  local storage_dir="${2%/}"

  if [[ ! -d "${git_dir}" ]]; then
    echo "Error: Git directory '${git_dir}' does not exist or is not a directory." >&2
    exit 1
  fi

  if [[ ! -d "${storage_dir}" ]]; then
    echo "Error: Storage directory '${storage_dir}' does not exist or is not a directory." >&2
    exit 1
  fi

  if ! command -v git >/dev/null 2>&1; then
    echo "Error: 'git' command not found." >&2
    exit 1
  fi

  echo "Scanning git repository history to resolve commit UUIDs..."

  # Check if FETCH_HEAD exists and is a valid ref
  local fetch_head_log=""
  if git --git-dir="${git_dir}" rev-parse --verify FETCH_HEAD >/dev/null 2>&1; then
    fetch_head_log="FETCH_HEAD"
  fi

  declare -A commit_to_uuid
  local line commit_hash msg uuid
  # Read git log output line-by-line
  while read -r line; do
    if [[ -z "$line" ]]; then
      continue
    fi
    commit_hash="${line%% *}"
    msg="${line#* }"
    
    uuid=""
    # Try to parse the UUID from JSON commit message ({"uuid":"<uuid>"...})
    if [[ "$msg" =~ $UUID_PATTERN ]]; then
      uuid="${BASH_REMATCH[1]}"
    else
      # Fallback to general UUID regex extraction
      uuid=$(echo "$msg" | grep -oE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | head -n 1 || true)
    fi
    
    if [[ -n "$uuid" ]]; then
      commit_to_uuid["$commit_hash"]="$uuid"
    fi
  done < <(git --git-dir="${git_dir}" log --all ${fetch_head_log} --format="%H %s" 2>/dev/null || true)

  declare -A keep_commits
  local commit ref branches

  # Rule 3: Keep all tagged commits
  echo "Collecting tagged commits..."
  # shellcheck disable=SC2046
  for commit in $(git --git-dir="${git_dir}" rev-list --tags 2>/dev/null || true); do
    keep_commits["$commit"]=1
  done

  # Check if FETCH_HEAD exists and is a valid ref
  local fetch_head_ref=""
  if git --git-dir="${git_dir}" rev-parse --verify FETCH_HEAD >/dev/null 2>&1; then
    fetch_head_ref="FETCH_HEAD"
  fi

  # Rule 1: For each open branch (and FETCH_HEAD if present), keep the last N commits and all commits within last M days
  echo "Collecting active branch commits (last ${commits} commits and last ${days} days)..."
  branches=$(git --git-dir="${git_dir}" for-each-ref --format='%(refname)' refs/heads/ refs/remotes/ 2>/dev/null || true)

  for ref in $branches $fetch_head_ref; do
    # Last N commits
    # shellcheck disable=SC2046
    for commit in $(git --git-dir="${git_dir}" rev-list -n "${commits}" "$ref" 2>/dev/null || true); do
      keep_commits["$commit"]=1
    done
    
    # Commits in last M days
    # shellcheck disable=SC2046
    for commit in $(git --git-dir="${git_dir}" rev-list --since="${days} days ago" "$ref" 2>/dev/null || true); do
      keep_commits["$commit"]=1
    done
  done

  declare -A keep_uuids
  for commit in "${!keep_commits[@]}"; do
    uuid="${commit_to_uuid["$commit"]:-}"
    if [[ -n "$uuid" ]]; then
      keep_uuids["$uuid"]=1
    fi
  done

  echo "Found ${#keep_uuids[@]} unique UUIDs to retain."
  echo "Scanning storage directory for revision artifacts..."

  local deleted_count=0
  local kept_count=0
  local dir_path basename

  # Loop through subdirectories of STORAGE_DIR
  for dir_path in "${storage_dir}"/*; do
    if [[ ! -d "$dir_path" ]]; then
      continue
    fi
    
    basename=$(basename "$dir_path")
    # Validate that the folder name is a UUID
    if [[ "$basename" =~ $UUID_REGEX ]]; then
      if [[ -z "${keep_uuids["$basename"]:-}" ]]; then
        if [[ "$dry_run" == "true" ]]; then
          echo "[DRY RUN] Would delete: $dir_path"
        else
          echo "Deleting obsolete revision: $dir_path"
          rm -rf "$dir_path"
        fi
        ((deleted_count++)) || true
      else
        echo "Keeping active revision: $dir_path"
        ((kept_count++)) || true
      fi
    fi
  done

  if [[ "$dry_run" == "true" ]]; then
    echo "Dry run complete: ${deleted_count} revisions would be deleted, ${kept_count} kept."
  else
    echo "Cleanup complete: ${deleted_count} revisions deleted, ${kept_count} kept."
  fi
}

main "$@"
