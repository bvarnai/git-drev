#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

function help()
{
  drs::common::show_help_and_exit "Puts a directory revision" "put";
}

function main()
{
  drs::common::time_start

  # preconditions
  drs::common::precondition_configuration
  drs::common::precondition_detached
  drs::common::check_remote_refs

  # process arguments
  local params
  local verbose
  local quiet
  local stats
  local sequence
  local sequence_check
  params=""
  verbose=""
  quiet=0
  stats=0
  sequence=""
  sequence_check="true"
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
      -s|--sequence)
        shift
        sequence="$1"
        shift
        ;;
      --no-sequence-check)
        shift
        sequence_check="false"
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

  source_directory=""
  # the 2nd positional argument is the local source directory (optional)
  if [[ -n "$1" ]]; then
    if [[ ! -d "$1" ]]; then
      drs::common::err "Specified source directory '$1' is not found"
      exit 1
    else
      source_directory="$1"
    fi
  fi

  workDir=$(jq -r '.workDir // "data"' "${DRS_CONFIG_FILE}")
  if [[ -z "${source_directory}" ]]; then
    source_directory="${workDir}"
  fi

  # check if source directory exists
  if [[ ! -d "${source_directory}" ]]; then
    drs::common::err "Specified source directory '${source_directory}' does not exists"
    exit 1
  fi

  local branch
  branch=$(git rev-parse --abbrev-ref HEAD)

  # adding changes
  drs::common::log "Adding changes to directory revision metadata"
  if ! git add  --all; then
    drs::common::err "Add failed (gitish: 'git add')"
    exit 1
  fi

  host=$(jq -r '.remote.host' "${DRS_CONFIG_FILE}")
  path=$(jq -r '.remote.path' "${DRS_CONFIG_FILE}")


  uuid=$(drs::common::uuidgen)
  drs::common::log "Directory revision uuid is '${uuid}'"

  # set default sequence if not specified
  if [[ -z "${sequence}" ]]; then
    sequence=$(date +%s)
  fi
  message="{\"uuid\":\"${uuid}\",\"seq\":\"${sequence}\"}"

  # copy previous build on remote host to speed up sync
  drs::common::log "Trying to use previous revision as baseline (this might take a while)"
  commit=$(git log --pretty=format:%s -1)
  last_uuid=$(jq -r '.uuid' <<< "${commit}" 2>&-)
  if [[ "$last_uuid" =~ $DRS_UUID_REGEXP ]]; then
    if ssh -T "${host}" cp -R -a "${path}/${last_uuid}"  "${path}/${uuid}"; then
      drs::common::log "Baseline revision uuid is '${last_uuid}'"
    else
      drs::common::log 'Unable to use previous revision, doing a full copy'
    fi
  else
      drs::common::log 'Unable to use previous revision, doing a full copy'
  fi

  # sync
  drs::common::log "Putting directory revision to remote host (this might take a while)"

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
  userRsyncOptions=$(jq -r '.remote.rsyncOptions.put // ""' "${DRS_CONFIG_FILE}")

  # concat options so user defined evaluated last
  rsyncOptions="$implicitRsyncOptions $userRsyncOptions"

  # shellcheck disable=SC2089
  if ! rsync $rsyncOptions -e 'ssh -T' "${source_directory}/" "${host}:${path}/${uuid}/"; then
    drs::common::err "Unable to put directory revision"
    exit 1
  fi

  # call project specific hook
  if [[ -f "${DRS_PUT_HOOK_FILE}" ]]; then
    # shellcheck source=common.sh
    source "${DRS_PUT_HOOK_FILE}"
    put_hook "$@"
  fi

  # stage files
  drs::common::log "Staging revision metadata"
  if ! git add --all; then
    drs::common::err "Staging failed (gitish: 'git add --all')"
    exit 1
  fi

  # commit
  drs::common::log "Commiting revision metadata"
  if ! git commit --allow-empty -m "${message}"; then
    drs::common::err "Commit failed (gitish: 'git commit --allow-empty -m ${uuid}')"
    exit 1
  fi

  # push changes
  drs::common::log "Pushing revision metadata"
  local stale
  if ! result=$(git push origin "${branch}" --force-with-lease 2>&1); then
    if grep 'stale info' <<< "${result}"; then
      stale="true"
    else
      drs::common::err "Push failed (gitish: 'git push origin ${branch} --force-with-lease')"
      exit 1
    fi
  else
    echo "${result}"
  fi

  # handle stale condition, loop until our commit is the latest
  while [[ -n "${stale}" ]]; do
    drs::common::log "Stale info detected, trying to resolve"

    # fetch changes
    if ! git fetch --all; then
      drs::common::err "Fetch failed (gitish: 'git fetch --all')"
      exit 1
    fi

    # check sequence (latest commit pointing to latest build)
    if [[ "${sequence_check}" == "true" ]]; then
      commit=$(git log --pretty=format:%s "origin/${branch}" -1)
      last_sequence=$(jq -r '.seq' <<< "${commit}" 2>&-)
      if [[ "${last_sequence}" =~ ^[0-9]+$ ]] && (( sequence < last_sequence )); then
        drs::common::log "Newer revision found, dropping older (hard reset)"
        drs::common::log "Hint: This is usually ok, you want to see the latest revision"

        # reset to latest commit, drop all changes
        if ! git reset --hard "origin/${branch}"; then
           drs::common::err "Reset failed (gitish: 'git reset --hard origin/${branch})"
           exit 1
        fi
        drs::common::time_took
        exit 0
      fi
    fi

    # pull
    if ! git rebase --strategy-option theirs "origin/${branch}"; then
      drs::common::err "Rebase failed (gitish: 'git rebase --strategy-option theirs origin')"
      exit 1
    fi

    # try to push again
    if ! result=$(git push origin "${branch}" --force-with-lease 2>&1); then
      if grep 'stale info' <<< "${result}"; then
        stale="true"
      else
        drs::common::err "Push failed (gitish: 'git push origin ${branch} --force-with-lease')"
        exit 1
      fi
    else
      echo "${result}"
    fi
    stale=
  done

  drs::common::time_took
}

main "$@"
