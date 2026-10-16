#!/bin/bash

# Parse options
QUIET=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -q|--quiet)
      QUIET=1
      shift
      ;;
    *)
      shift
      ;;
  esac
done

function log() {
  if [[ "${QUIET}" -ne 1 ]]; then
    echo "$@"
  fi
}

log "Installing 'drs' command aliases"
# Remove commands (do not delete entires from here to always get a clean state)
git config --unset alias.drs-get
git config --unset alias.drs-put
git config --unset alias.drs-update
git config --unset alias.drs-select
git config --unset alias.drs-create
git config --unset alias.drs-help
git config --unset alias.drs-info
git config --unset alias.drs-name
git config --unset alias.drs-usage

# Install commands (don't forget to update config-example)
log "Adding drs-get"
git config --add alias.drs-get "!f() { ( \$DRS_HOME/get.sh \$@ ); }; f"
log "Adding drs-put"
git config --add alias.drs-put "!f() { ( \$DRS_HOME/put.sh \$@ ); }; f"
log "Adding drs-select"
git config --add alias.drs-select "!f() { ( \$DRS_HOME/select.sh \$@ ); }; f"
log "Adding drs-create"
git config --add alias.drs-create "!f() { ( \$DRS_HOME/create.sh \$@ ); }; f"
log "Adding drs-update"
git config --add alias.drs-update "!f() { ( \$DRS_HOME/update.sh \$@ ); }; f"
log "Adding drs-info"
git config --add alias.drs-info "!f() { ( \$DRS_HOME/info.sh \$@ ); }; f"
log "Adding drs-name"
git config --add alias.drs-name "!f() { ( \$DRS_HOME/name.sh \$@ ); }; f"
log "Adding drs-usage"
git config --add alias.drs-usage "!f() { ( \$DRS_HOME/usage.sh \$@ ); }; f"

