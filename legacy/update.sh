#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Update a directory branch to latest" "update";
}

function main()
{
  # preconditions
  drs::common::precondition_configuration
  drs::common::precondition_detached
  drs::common::check_remote_refs

  # process arguments
  drs::common::no_args "$@"

  # get all changes
  if ! git fetch --all; then
    drs::common::err "Fetch failed (gitish: 'git fetch --all')"
    exit 1
  fi

  # update to latest
  local branch
  branch=$(git rev-parse --abbrev-ref HEAD)

  if ! git reset --hard "origin/${branch}"; then
    drs::common::err "Reset failed (gitish: 'git reset --hard')"
    exit 1
  fi

}

main "$@"
