#!/bin/sh
# RemnaManager: POSIX launcher. Run from an extracted project directory.
set -eu
SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
if [ "${1:-}" = '--check' ]; then
    exec python3 "$SCRIPT_DIR/remnamanager.py" --check
fi
[ "$(id -u)" -eq 0 ] || { echo 'Запустите от root: sudo sh remnamanager.sh' >&2; exit 1; }
[ "$(uname -s)" = Linux ] || { echo 'Нужен Linux (Debian 12/13 или Ubuntu 22.04/24.04).' >&2; exit 1; }
if ! command -v python3 >/dev/null 2>&1; then
    apt-get update
    apt-get install -y python3
fi
exec python3 "$SCRIPT_DIR/remnamanager.py" "$@"
