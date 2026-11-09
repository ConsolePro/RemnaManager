#!/bin/sh
# POSIX entrypoint; the application itself uses Bash 4.4+.
set -eu
SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
exec bash "$SCRIPT_DIR/manager.sh" "$@"
