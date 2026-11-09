#!/usr/bin/env bash
# Migrate the previous managed shortcut; executable launchers need no aliases.
[[ $- == *i* && $EUID == 0 ]] || return 0
legacy_rm=$(declare -f rm 2>/dev/null) || legacy_rm=''
if [[ $legacy_rm == *'remna_manager'* && $legacy_rm == *'command rm "$@"'* && $legacy_rm == *'(($# == 0))'* ]]; then
    unset -f rm
fi
unset legacy_rm
