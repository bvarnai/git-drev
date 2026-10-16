#!/bin/bash

# shellcheck disable=SC1091
source "${DRS_HOME}/common.sh"

# Check prerequisites
drs::common::log "Checking 'ssh' command..."
if ! ssh -V; then
  drs::common::err "No 'ssh' command found"
  exit 1
fi
drs::common::log "OK"

drs::common::log "Checking 'rsync' command..."
if ! rsync --version; then
  drs::common::err "No 'rsync' command found"
  exit 1
fi
drs::common::log "OK"

drs::common::log "Checking 'jq' command..."
if ! jq --version; then
  drs::common::err "No 'jq' command found"
  exit 1
fi
drs::common::log "OK"
