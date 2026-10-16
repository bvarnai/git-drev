#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Gets a directory revision" "get";
}

function main()
{
  # time longer commands
  drs::common::time_start

  # process arguments
  local params
  local verbose
  local quiet
  local stats
  local latest
  params=""
  verbose=""
  quiet=0
  stats=0
  latest=0
  while (( "$#" )); do
    # shellcheck disable=SC2222,SC2221
    case "$1" in
      help) # print usage
        help
        ;;
      -v|--verbose)
        verbose='--itemize-changes'
        shift
        ;;
      -q|--quiet)
        quiet=1
        shift
        ;;
      --stats)
        stats=1
        shift
        ;;
      -l|--latest)
        latest=1
        shift
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

  drs::common::precondition_configuration
  drs::common::check_remote_refs

  # shortcut for update/get
  if [[ ${latest} == 1 ]]; then
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
  fi

  # get metadata uuid from commit title
  commit=$(git log --pretty=format:%s -1)
  uuid=$(jq -r '.uuid' <<< "${commit}" 2>&-)
  if [[ ! "${uuid}" =~ $DRS_UUID_REGEXP ]]; then
    drs::common::err "Unable to read directory metadata (missing uuid)"
    exit 1
  fi

  # check for empty marker (no revision put yet)
  marker=$(jq -r '.marker' <<< "${commit}" 2>&-)
  if [[ "${marker}" == "true" ]]; then
    drs::common::log "No directory revision available yet on current branch"
    exit 1
  fi

  drs::common::log "Directory revision uuid is '${uuid}'"

  workDir=$(jq -r '.workDir // "data"' "${DRS_CONFIG_FILE}")
  host=$(jq -r '.remote.host' "${DRS_CONFIG_FILE}")
  path=$(jq -r '.remote.path' "${DRS_CONFIG_FILE}")

  target_directory="${workDir}"
  # the 2nd positional argument is the local source directory (optional)
  if [[ -n "$1" ]]; then
    if [[ ! -d "$1" ]]; then
      if ! mkdir -p "$1"; then
        drs::common::err "Failed to create target directory '$1'"
        exit 1
      fi
    fi
    target_directory="$1"
  fi

  # locate revision
  drs::common::log "Checking revision on remote host"
  # shellcheck disable=SC2029
  if ! ssh "${host}" "[ -d ${path}/${uuid} ]"; then
    drs::common::err "Unable to find revision"
    drs::common::log "Hint: Revision might be gone already (that's normal depending on your retention policy)"
    exit 1
  fi

  drs::common::log "Getting directory revision from remote host (this might take a while)"

  # implicit rsync flags and options
  implicitRsyncOptions=""

  # verbose options
  if [[ ${quiet} == 1 ]]; then
    implicitRsyncOptions+=" --quiet"
  else
    implicitRsyncOptions+=" --info=progress2"
    implicitRsyncOptions+=${verbose:+" -v" "${verbose}"}
  fi

  # statistics option
  if [[ ${stats} == 1 ]]; then
    implicitRsyncOptions+=" --stats"
  fi

  # force archive and delete
  implicitRsyncOptions+=" -a --delete-during"

  # user defined rsync flags and options
  userRsyncOptions=$(jq -r '.remote.rsyncOptions.get // ""' "${DRS_CONFIG_FILE}")

  # concat options so user defined evaluated last
  rsyncOptions="$implicitRsyncOptions $userRsyncOptions"

  # shellcheck disable=SC2089
  if ! rsync $rsyncOptions -e 'ssh -T' "${host}:${path}/${uuid}/" "${target_directory}/"; then
    drs::common::err "Unable to get directory revision"
    exit 1
  fi

  drs::common::time_took
}

main "$@"
