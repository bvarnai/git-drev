#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Show remote storage consumption" "usage";
}

function format_epoch() {
  local epoch="$1"
  if [[ "$epoch" -eq 0 ]]; then
    echo "-"
    return
  fi
  # Platform-agnostic date command for epoch conversion
  case "$OSTYPE" in
    msys*|cygwin*|win32*|linux-gnu*)
      date -d "@${epoch}" "+%Y-%m-%d %H:%M:%S" 2>/dev/null || echo "-"
      ;;
    darwin*)
      date -r "${epoch}" "+%Y-%m-%d %H:%M:%S" 2>/dev/null || echo "-"
      ;;
    *)
      # Fallback if unknown
      date -d "@${epoch}" "+%Y-%m-%d %H:%M:%S" 2>/dev/null || date -r "${epoch}" "+%Y-%m-%d %H:%M:%S" 2>/dev/null || echo "-"
      ;;
  esac
}

function main()
{
  # preconditions
  drs::common::precondition_configuration
  drs::common::check_remote_refs

  # process arguments
  local verbose=0
  local params=""
  while (( "$#" )); do
    # shellcheck disable=SC2222,SC2221
    case "$1" in
      help) # print usage
        help
        ;;
      -v|--verbose)
        verbose=1
        shift
        ;;
      --) # end argument parsing
        shift
        break
        ;;
      -*|--*=) # unsupported options
        drs::common::err "Unsupported option '$1'"
        exit 1
        ;;
      *) # preserve positional arguments
        params="${params} $1"
        shift
        ;;
    esac
  done
  # set positional arguments in their proper place
  eval set -- "${params}"

  # no arguments are expected, just discard them
  drs::common::no_args "$@"

  local host
  local path
  host=$(jq -r '.remote.host' "${DRS_CONFIG_FILE}")
  path=$(jq -r '.remote.path' "${DRS_CONFIG_FILE}")
  path="${path%/}"

  if [[ -z "${host}" || "${host}" == "null" ]]; then
    drs::common::err "Remote host is not configured in ${DRS_CONFIG_FILE}"
    exit 1
  fi
  if [[ -z "${path}" || "${path}" == "null" ]]; then
    drs::common::err "Remote storage path is not configured in ${DRS_CONFIG_FILE}"
    exit 1
  fi

  drs::common::log "Connecting to remote host '${host}' to retrieve storage usage..."

  local ssh_output
  local ssh_exit_code

  ssh_output=$(ssh -T "${host}" "/bin/sh -s -- \"${path}\"" 2>&1 <<'EOF'
path="$1"
if [ ! -d "$path" ]; then
  echo "ERROR:Remote path '$path' does not exist."
  exit 1
fi

total_size=$(du -sh "$path" 2>/dev/null | cut -f1)
echo "TOTAL_SIZE:$total_size"


for d in "$path"/*; do
  if [ -d "$d" ]; then
    name=$(basename "$d")
    if echo "$name" | grep -qE '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'; then
      size=$(du -sh "$d" 2>/dev/null | cut -f1)
      mtime=0
      if stat -c %Y "$d" >/dev/null 2>&1; then
        mtime=$(stat -c %Y "$d")
      elif stat -f %m "$d" >/dev/null 2>&1; then
        mtime=$(stat -f %m "$d")
      fi
      echo "REV:$name:$size:$mtime"
    fi
  fi
done
EOF
)
  ssh_exit_code=$?

  if [[ $ssh_exit_code -ne 0 ]]; then
    drs::common::err "Failed to connect to remote host '${host}' via SSH or command failed (exit code: ${ssh_exit_code})"
    drs::common::err "Details: ${ssh_output}"
    exit 1
  fi

  local total_size="-"

  # Arrays to hold remote revisions info
  local -a remote_uuids=()
  local -a remote_sizes=()
  local -a remote_mtimes=()

  # Parse the stdout line by line
  local line
  while IFS= read -r line; do
    if [[ "$line" =~ ^ERROR:(.*) ]]; then
      drs::common::err "${BASH_REMATCH[1]}"
      exit 1
    elif [[ "$line" =~ ^TOTAL_SIZE:(.*) ]]; then
      total_size="${BASH_REMATCH[1]}"
    elif [[ "$line" =~ ^REV:([^:]+):([^:]+):([^:]+)$ ]]; then
      remote_uuids+=("${BASH_REMATCH[1]}")
      remote_sizes+=("${BASH_REMATCH[2]}")
      remote_mtimes+=("${BASH_REMATCH[3]}")
    fi
  done <<< "${ssh_output}"

  # Output Summary
  drs::common::log "Remote storage usage for host:${host} path:${path}"
  echo ""
  echo "Summary:"
  echo "  Total space used:  ${total_size}"
  echo ""

  if [[ ${#remote_uuids[@]} -eq 0 ]]; then
    drs::common::log "No revision directories found on remote storage."
    return 0
  fi

  if [[ $verbose -eq 0 ]]; then
    drs::common::log "Found ${#remote_uuids[@]} revision directory(ies) on remote storage."
    drs::common::log "Hint: Use -v/--verbose to view the detailed breakdown of revisions."
    return 0
  fi

  # Parse git log to match UUIDs with commit information
  declare -A uuid_to_hash
  declare -A uuid_to_short_hash
  declare -A uuid_to_date
  declare -A uuid_to_refs
  declare -A uuid_to_msg

  local UUID_PATTERN='"uuid":"([^"]+)"'
  local UUID_REGEX_MATCH='[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'

  # Load all git log info
  local log_line
  while IFS='|' read -r hash short_hash commit_date refs msg; do
    local uuid=""
    if [[ "$msg" =~ $UUID_PATTERN ]]; then
      uuid="${BASH_REMATCH[1]}"
    else
      uuid=$(echo "$msg" | grep -oE "${UUID_REGEX_MATCH}" | head -n 1 || true)
    fi

    if [[ -n "$uuid" ]]; then
      # If duplicate, keep the first one encountered (most recent)
      if [[ -z "${uuid_to_hash["$uuid"]:-}" ]]; then
        uuid_to_hash["$uuid"]="$hash"
        uuid_to_short_hash["$uuid"]="$short_hash"
        uuid_to_date["$uuid"]="$commit_date"
        uuid_to_msg["$uuid"]="$msg"

        local cleaned_refs=""
        if [[ -n "$refs" ]]; then
          refs="${refs//HEAD -> /}"
          refs="${refs//tag: /}"
          local IFS=','
          for ref_item in $refs; do
            ref_item=$(echo "$ref_item" | xargs)
            if [[ "$ref_item" != "origin/HEAD" && -n "$ref_item" ]]; then
              if [[ -z "$cleaned_refs" ]]; then
                cleaned_refs="$ref_item"
              else
                cleaned_refs="$cleaned_refs, $ref_item"
              fi
            fi
          done
        fi
        uuid_to_refs["$uuid"]="$cleaned_refs"
      fi
    fi
  done < <(git log --all --format="%H|%h|%ad|%D|%s" --date=format:"%Y-%m-%d %H:%M:%S" 2>/dev/null || true)

  # Check if we should display the table
  local printed_header=0
  local i
  for (( i=0; i<${#remote_uuids[@]}; i++ )); do
    local uuid="${remote_uuids[$i]}"
    local size="${remote_sizes[$i]}"
    local mtime="${remote_mtimes[$i]}"

    local hash="${uuid_to_short_hash["$uuid"]:-}"
    local is_active=1
    if [[ -z "$hash" ]]; then
      is_active=0
      hash="[orphaned]"
    fi

    if [[ $printed_header -eq 0 ]]; then
      echo "Revisions breakdown:"
      printf "%-36s  %8s  %-19s  %-10s  %-20s  %s\n" "UUID" "Size" "Created" "Commit" "Branch/Tag" "Message"
      printf "%-36s  %8s  %-19s  %-10s  %-20s  %s\n" "------------------------------------" "--------" "-------------------" "----------" "--------------------" "----------------------"
      printed_header=1
    fi

    local created_date
    created_date=$(format_epoch "$mtime")
    local ref_info="${uuid_to_refs["$uuid"]:-}"
    if [[ -z "$ref_info" && $is_active -eq 1 ]]; then
      ref_info="-"
    elif [[ $is_active -eq 0 ]]; then
      ref_info="-"
    fi

    local msg_info="${uuid_to_msg["$uuid"]:-}"
    if [[ $is_active -eq 0 ]]; then
      msg_info="-"
    else
      # Try to parse seq from JSON message
      local seq
      seq=$(jq -r '.seq // empty' <<< "${msg_info}" 2>/dev/null || true)
      if [[ -n "$seq" ]]; then
        msg_info="seq: ${seq}"
      fi
    fi

    printf "%-36s  %8s  %-19s  %-10s  %-20s  %s\n" "${uuid}" "${size}" "${created_date}" "${hash}" "${ref_info}" "${msg_info}"
  done
}

main "$@"
