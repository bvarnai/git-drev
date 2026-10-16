#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Creates a new revision metadata branch" "create";
}

function main()
{
  drs::common::time_start

  # preconditions
  drs::common::precondition_configuration

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
      -*|--*=) # unsupported flags
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

  # the 1st positional argument is the branch name (mandatory)
  local branch
  if [[ -z "$1" ]]; then
    drs::common::err "No branch name specified"
    exit 1
  else
    branch="$1"
  fi

  # check current remote refs and delete them if not they don't exist on remote.
  drs::common::check_remote_refs

  local working_branch
  # check if branch is already avaiable on local
  drs::common::log "Checking for existing branch (local)"
  local local_branch
  local_branch=$(git for-each-ref --format='%(refname:short)' refs/heads/"${branch}")
  if [[ -z "${local_branch}" ]]; then
    drs::common::log "No branch '${branch}' found (local)"
  else
    drs::common::log "Branch '${branch}' found (local)"
    drs::common::fetch_and_checkout "${branch}"
    exit 0
  fi

  # check if branch is already avaiable on remote
  if [[ -z "${local_branch}" ]]; then
    # check if remote already contains this branch
    drs::common::log "Checking for existing branch (remote)"
    local remote_branch
    remote_branch=$(git ls-remote origin "${branch}")
    if [[ $? != 0 ]]; then
      drs::common::err "Remote listing failed (gitish: 'git ls-remote origin ${branch}')"
      exit 1
    fi

    if [[ -z "${remote_branch}" ]]; then
      drs::common::log "No branch '${branch}' found (remote)"
    else
      drs::common::log "Branch '${branch}' found (remote)"
      drs::common::fetch_and_switch "${branch}"
      exit 0
    fi
  fi

  # if no branch found create new branch
  drs::common::log "Creating new revision branch '${branch}'"
  working_branch="${branch}"
  if ! git checkout -b "${working_branch}"; then
    drs::common::err "Checkout failed (gitish: 'git checkout -b ${branch}')"
    exit 1
  fi

  # copy uuid of previous commit
  commit=$(git log --pretty=format:%s -1)
  last_uuid=$(jq -r '.uuid' <<< "${commit}" 2>&-)
  if [[ ! "$last_uuid" =~ $DRS_UUID_REGEXP  ]]; then
    last_uuid="00000000-0000-0000-0000-000000000000"
  fi

  drs::common::log "Commiting empty revision marker"
  if ! git commit --allow-empty -m "{\"uuid\":\"${last_uuid}\",\"marker\":\"true\"}"; then
    drs::common::err "Commit failed (gitish: 'git commit --allow-empty -m ...')"
    exit 1
  fi

  # publish new branch
  drs::common::log "Pushing new revision branch '${branch}'"
  local tracking
  tracking=$(git for-each-ref --format='%(upstream:short)' "$(git symbolic-ref -q HEAD)")
  if [[ "${tracking}" == "origin/${working_branch}" ]]; then
    if ! git push origin "${working_branch}" --force-with-lease; then
      drs::common::err "Push failed (gitish: 'git push origin ${working_branch} --force-with-lease')"
      exit 1
    fi
  else
    # set upstream if necessary
    if ! git push --set-upstream origin "${working_branch}" --force-with-lease; then
      drs::common::err "Push failed (gitish: 'git push --set-upstream origin ${working_branch} --force-with-lease')"
      exit 1
    fi
  fi

  drs::common::time_took
}

main "$@"
