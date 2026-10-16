#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Gets additional, project specific information of the directory revision" "info";
}

function main()
{
  # preconditions
  drs::common::precondition_configuration

  # process arguments
  drs::common::no_args "$@"

 # call project specific hook
  if [[ -f "${DRS_INFO_HOOK_FILE}" ]]; then
    # shellcheck source=common.sh
    source "${DRS_INFO_HOOK_FILE}"
    info_hook "$@"
  else
    drs::common::log 'No info hook found'
  fi

}

main "$@"
