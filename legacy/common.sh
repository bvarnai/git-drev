#!/bin/bash

# Constants
# shellcheck disable=SC2034
declare -r DRS_VERSION="1.1.1"
declare -r DRS_DOC_URL="https://github.com/bvarnai/drs?tab=readme-ov-file"
declare -r DRS_LOG_PREFIX='[drs]'
# shellcheck disable=SC2034
declare -r DRS_UUID_REGEXP='^\{?[A-Z0-9a-z]{8}-[A-Z0-9a-z]{4}-[A-Z0-9a-z]{4}-[A-Z0-9a-z]{4}-[A-Z0-9a-z]{12}\}?$'
declare -r DRS_CONFIG_FILE='drs.json'
# shellcheck disable=SC2034
declare -r DRS_PUT_HOOK_FILE='drs-put-hook.sh'
# shellcheck disable=SC2034
declare -r DRS_INFO_HOOK_FILE='drs-info-hook.sh'
declare DRS_START_TIME=0

#######################################
# Platform-agnostic uuidgen.
# Globals:
#   None
# Arguments:
#   None
#######################################
function drs::common::uuidgen()
{
  local uuid
  case "$OSTYPE" in
    # Git Bash, Cygwin, or MSYS on Windows
    msys*|cygwin*|win32*)
      uuid=$(powershell.exe -NoProfile -Command "[guid]::NewGuid().ToString().ToLower()" | tr -d '\r')
      ;;
    # Native Linux or WSL
    linux-gnu*)
      if [ -f /proc/sys/kernel/random/uuid ]; then
        uuid=$(cat /proc/sys/kernel/random/uuid)
      else
        # Fallback if proc is restricted
        uuid=$(python3 -c 'import uuid; print(uuid.uuid4())')
      fi
      ;;
    # macOS
    darwin*)
      uuid=$(/usr/bin/uuidgen | tr '[:upper:]' '[:lower:]')
      ;;
    *)
      drs::common::err "Unsupported platform for uuidgen"
      return 1
      ;;
  esac

  # final check: ensure we actually got a string back
  if [[ -n "$uuid" ]]; then
    echo "$uuid"
  else
    drs::common::err "Error: Failed to generate UUID"
    return 1
  fi
}

#######################################
# Read repository information and check if it's a git repository.
# Globals:
#   None
# Arguments:
#   None
#######################################
function drs::common::read_repository()
{
  # read repository information
  # not yet implemented

  # sanity check
  if git rev-parse --git-dir > /dev/null 2>&1; then
    # this is a valid git repository (but the current working
    # directory may not be the top level)
    :
  else
    drs::common::err "Awh! This is not a git repository"
    exit 1
  fi
}

#######################################
# Sets start time.
# Globals:
#  None
# Arguments:
#  None
# Returns:
#  None
#######################################
function drs::common::fetch_and_checkout()
{
  # get all changes
  if ! git fetch --all; then
    drs::common::err "Fetch failed (gitish: 'git fetch --all' failed)"
    exit 1
  fi

  # checkout branch
  if ! git -c advice.detachedHead=false checkout "$1"; then
    drs::common::err "Checkout failed (gitish: 'git checkout $1' failed)"
    exit 1
  fi
}

function drs::common::fetch_and_switch()
{
  # get all changes
  if ! git fetch --all; then
    drs::common::err "Fetch failed (gitish: 'git fetch --all' failed)"
    exit 1
  fi

  # switch to remote branch
  if ! git switch "$1"; then
    drs::common::err "Checkout failed (gitish: 'git switch $1' failed)"
    exit 1
  fi
}

#######################################
# Sets start time.
# Globals:
#  DRS_START_TIME
# Arguments:
#  None
# Returns:
#  None
#######################################
function drs::common::time_start()
{
  DRS_START_TIME=$SECONDS
}

#######################################
# Displays elapsed time since time_start call.
# Arguments:
#  None
# Returns:
#  None
#######################################
function drs::common::time_took()
{
  local elapsed_time=$((SECONDS - DRS_START_TIME))
  drs::common::log "Took $((elapsed_time/60)) min $((elapsed_time%60)) sec"
}

#######################################
# Checks if in detached HEAD state.
# Arguments:
#  None
# Returns:
#  Exits with return value 1 if detached HEAD state detected
#######################################
function drs::common::precondition_detached()
{
  if ! git symbolic-ref -q HEAD >/dev/null; then
    drs::common::err "Detached HEAD state detected, aborting"
    drs::common::log "Hint: Select a branch first. Use \"git drs-select\" to select a branch"
    exit 1
  fi
}

#######################################
# Checks if the configuration file is present.
# Arguments:
#  None
# Returns:
#  Exits with return value 1 if no origin
#######################################
function drs::common::precondition_configuration()
{
  if [[ ! -f "${DRS_CONFIG_FILE}" ]]; then
    drs::common::err "No configuration file '${DRS_CONFIG_FILE}' found"
    exit 1
  fi
}

#######################################
# Logs a message to stdout.
# Arguments:
#   $@ - anything to log
# Returns:
#   None
#######################################
function drs::common::log()
{
  echo "${DRS_LOG_PREFIX} $*"
}

#######################################
# Logs a message to stderr.
# Arguments:
#   $@ - anything to log
# Returns:
#   None
#######################################
function drs::common::err()
{
  echo -e "${DRS_LOG_PREFIX} ! $*" >&2
}

#######################################
# Helper function for command with no arguments just help.
# Arguments:
#  $@ - all arguments
# Returns:
#  None
#######################################
function drs::common::no_args()
{
  # parse command parameters
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

  # no arguments are expected, just discard them
  if [[ "$#" != 0 ]]; then
    drs::common::log "This command takes no argument(s), please check your input"
  fi
}

#######################################
# Shows help message with link to the command reference, than exits.
# Arguments:
#  $1 - help messsage
#  $2 - command name to anchor the reference documentation
# Returns:
#  Function doesn't return
#######################################
function drs::common::show_help_and_exit()
{
  drs::common::log "$1"; \
  drs::common::log "For more information see ${DRS_DOC_URL}#$2" 1>&2; exit 1;
}

#######################################
# Check if remote refspec exists? and delete them if not.
# Arguments:
#   None
# Returns:
#   None
#######################################
function drs::common::check_remote_refs()
{
  # retrieve all remote refs from local git config file
  config_refs=$(git config  --local --get-all remote.origin.fetch)
  if [[ -n "$config_refs" ]]; then
    # remove all remote refs from local git config file
    git config --unset-all "remote.origin.fetch"
    mapfile -t refs_array <<< "$config_refs"
    for ref in "${refs_array[@]}"; do
      remote_branch_name=${ref#*+refs/heads/}
      remote_branch_name=${remote_branch_name%:*}
      # first check if remote branch exists on remote?
      if git ls-remote --exit-code origin "refs/heads/${remote_branch_name}" > /dev/null 2>&1; then
          if [[ $remote_branch_name == "*" ]]; then
              git config --add remote.origin.fetch "+refs/heads/${remote_branch_name}:refs/remotes/origin/${remote_branch_name}"
          fi
          # add a ref to existing remote branch
          if ! git config --get-regex remote.origin.fetch "refs/heads/${remote_branch_name}" > /dev/null 2>&1; then
            git config --add remote.origin.fetch "+refs/heads/${remote_branch_name}:refs/remotes/origin/${remote_branch_name}"
          fi
        else
          drs::common::err "Warning: branch ${remote_branch_name} doesn't exist on remote!"
      fi
    done
  else
    # fetch all branches
    git config --add remote.origin.fetch "+refs/heads/*:refs/remotes/origin/*"
  fi
}
