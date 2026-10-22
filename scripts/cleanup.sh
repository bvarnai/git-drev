#!/usr/bin/env bash
# cleanup.sh: Server-side revision retention and cleanup script for git-drev
#
# This script purges obsolete directory revisions from the remote storage path
# based on the Git metadata repository.
#
# Retention Rules:
# 1. Keep the last N commits (default: 5) for every open branch.
# 2. Keep all commits from the last M days (default: 30) for every open branch.
# 3. Never delete revision directories for tagged commits (lightweight or annotated).
# 4. Protect parent baseline revisions referenced by retained branch marker commits
#    ({"uuid":"<parent_uuid>","marker":"true"}).
# 5. If a branch is deleted, its commits are removed from the keep list and their
#    revision directories are purged unless retained by another active branch or tag.
#
# Cron Integration Example (for remote repositories such as GitHub/GitLab):
# 0 2 * * * git --git-dir=/srv/drev/myproject-meta.git fetch origin '+refs/heads/*:refs/heads/*' --prune --tags && /srv/drev/cleanup.sh --days 30 --commits 5 /srv/drev/myproject-meta.git /srv/drev/storage/myproject >> /var/log/git-drev-cleanup.log 2>&1

set -euo pipefail

readonly ZERO_UUID="00000000-0000-0000-0000-000000000000"
readonly UUID_HEX='[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'
readonly UUID_JSON_PATTERN="\"uuid\":\"(${UUID_HEX})\""
readonly UUID_EXACT_REGEX="^${UUID_HEX}$"

show_help() {
  cat << EOF
Usage: $(basename "$0") [options] <git-dir> <storage-dir>

Purges obsolete git-drev revision directories from <storage-dir> using the
Git metadata repository at <git-dir> as the source of truth.

Options:
  -n, --dry-run      Show what would be deleted without deleting anything.
  -d, --days <N>     Number of days of commits to keep on open branches (default: 30).
  -c, --commits <N>  Number of latest commits per open branch to keep (default: 5).
  -h, --help         Show this help message.

Integration with Cron (e.g., for GitHub-hosted metadata):
  1. Clone your metadata repository as a bare clone on the storage server:
     git clone --bare git@github.com:org/myproject-meta.git /srv/drev/myproject-meta.git
  2. Configure a daily cron job to fetch pruned refs/tags and run cleanup:
     0 2 * * * git --git-dir=/srv/drev/myproject-meta.git fetch origin '+refs/heads/*:refs/heads/*' --prune --tags && /srv/drev/cleanup.sh --days 30 --commits 5 /srv/drev/myproject-meta.git /srv/drev/storage/myproject
EOF
}

main() {
  local dry_run="false"
  local days=30
  local commits=5

  # Parse CLI options
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -n|--dry-run)
        dry_run="true"
        shift
        ;;
      -d|--days)
        if [[ -z "${2:-}" || ! "$2" =~ ^[0-9]+$ ]]; then
          echo "Error: --days requires a non-negative integer value" >&2
          exit 1
        fi
        days="$2"
        shift 2
        ;;
      -c|--commits)
        if [[ -z "${2:-}" || ! "$2" =~ ^[0-9]+$ ]]; then
          echo "Error: --commits requires a non-negative integer value" >&2
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

  echo "Scanning Git repository history to resolve commit UUIDs..."

  local fetch_head_ref=""
  if git --git-dir="${git_dir}" rev-parse --verify --quiet FETCH_HEAD >/dev/null 2>&1; then
    fetch_head_ref="FETCH_HEAD"
  fi

  declare -A commit_to_uuid
  local line commit_hash msg uuid

  while IFS= read -r line; do
    if [[ -z "$line" ]]; then
      continue
    fi
    commit_hash="${line%% *}"
    msg="${line#* }"

    uuid=""
    if [[ "$msg" =~ $UUID_JSON_PATTERN ]]; then
      uuid="${BASH_REMATCH[1],,}"
      # If this is a branch marker commit with ZeroUUID, it references no parent baseline
      if [[ "$uuid" == "$ZERO_UUID" ]]; then
        uuid=""
      fi
    else
      uuid=$(printf '%s\n' "$msg" | grep -oE "$UUID_HEX" | head -n 1 | tr '[:upper:]' '[:lower:]' || true)
    fi

    if [[ -n "$uuid" && "$uuid" != "$ZERO_UUID" ]]; then
      commit_to_uuid["$commit_hash"]="$uuid"
    fi
  done < <(git --git-dir="${git_dir}" log --all ${fetch_head_ref:+"$fetch_head_ref"} --format="%H %s" 2>/dev/null || true)

  declare -A keep_commits
  local commit ref branches

  # Rule 3: Keep all tagged commits (peeling annotated tags without walking ancestor history)
  echo "Collecting tagged commits..."
  # shellcheck disable=SC2046
  for commit in $(git --git-dir="${git_dir}" rev-list --no-walk --tags 2>/dev/null || true); do
    keep_commits["$commit"]=1
  done

  # Rules 1 & 2: For each open branch (and FETCH_HEAD if present), keep the last N commits and commits within last M days
  echo "Collecting active branch commits (last ${commits} commits and last ${days} days)..."
  branches=$(git --git-dir="${git_dir}" for-each-ref --format='%(refname)' refs/heads/ refs/remotes/ 2>/dev/null || true)

  for ref in $branches $fetch_head_ref; do
    if (( commits > 0 )); then
      # shellcheck disable=SC2046
      for commit in $(git --git-dir="${git_dir}" rev-list -n "${commits}" "$ref" 2>/dev/null || true); do
        keep_commits["$commit"]=1
      done
    fi

    if (( days > 0 )); then
      # shellcheck disable=SC2046
      for commit in $(git --git-dir="${git_dir}" rev-list --since="${days} days ago" "$ref" 2>/dev/null || true); do
        keep_commits["$commit"]=1
      done
    fi
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
  local dir_path dir_name norm_uuid

  for dir_path in "${storage_dir}"/*; do
    if [[ ! -d "$dir_path" ]]; then
      continue
    fi

    dir_name=$(basename -- "$dir_path")
    if [[ "$dir_name" =~ $UUID_EXACT_REGEX ]]; then
      norm_uuid="${dir_name,,}"
      if [[ "$norm_uuid" == "$ZERO_UUID" ]]; then
        continue
      fi

      if [[ -z "${keep_uuids["$norm_uuid"]:-}" ]]; then
        if [[ "$dry_run" == "true" ]]; then
          echo "[DRY RUN] Would delete: $dir_path"
        else
          echo "Deleting obsolete revision: $dir_path"
          rm -rf -- "$dir_path"
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
