#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Select and switch to an existing directory branch" "select";
}

function main()
{

  # preconditions
  drs::common::precondition_configuration
  drs::common::check_remote_refs

  # process arguments
  local params
  params=""
  while (( "$#" )); do
    # shellcheck disable=SC2222,SC2221
    case "$1" in
      help) # print usage
        help
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

  # check if branch name is specified, fallback to default
  local branch
  if [[ -z "$1" ]]; then
    branch=$(jq -r '.defaultBranch // "main"' "${DRS_CONFIG_FILE}")
    drs::common::log "Selecting '${branch}' branch by default"
  else
    if [[ "$1" =~ $DRS_UUID_REGEXP ]]; then
      is_found="false"
      drs::common::log "Searching for commit with uuid '$1'"
      commits=$(git log --all --format=%H --grep="$1")
      for commit in $commits; do
          commit_message=$(git log --format=%B -n 1 "$commit")
          if [[ "$commit_message" =~ $1 ]] && [[ ! "$commit_message" =~ "marker" ]]; then
            branch="$commit"
            is_found="true"
            break
          fi
      done
      commit=$(git log --all --format=%H  --grep="$1" -n 1)
      if [[ "$is_found" = "false" ]]; then
        drs::common::err "No commit was found with uuid '$1'"
        exit 1
      fi
      drs::common::log "Found commit '${branch}'"
    else
      branch="$1"
    fi
  fi

  drs::common::fetch_and_checkout "${branch}"
}

main "$@"
