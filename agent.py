#!/usr/bin/env python3
"""Remote endpoint. Jobs arrive on SSH stdin; no TCP daemon or shell interpolation."""
import json
import os
import sys
from lib.provision import execute_job

if __name__ == '__main__':
    os.umask(0o077)
    try:
        execute_job(json.load(sys.stdin))
    except Exception as exc:
        print(f'Ошибка агента: {exc}', file=sys.stderr)
        sys.exit(1)
