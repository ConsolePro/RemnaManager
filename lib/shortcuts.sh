#!/usr/bin/env bash
# Sourced only by interactive root Bash; never installed over the system rm.
[[ $- == *i* && $EUID == 0 ]] || return 0
# The function keyword prevents expansion of an existing user alias here.
function rm {
    if (($# == 0)); then
        remna_manager
    else
        command rm "$@"
    fi
}
