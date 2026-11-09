#!/usr/bin/env bash
set -Eeuo pipefail
cd -- "$(dirname -- "$0")/.."
for command in jq bash curl openssl tar; do command -v "$command" >/dev/null || { printf 'Missing test dependency: %s\n' "$command"; exit 1; }; done
passed=0 failed=0
for test in validation storage configs warp ssh_pinning nginx_rollback api_errors readiness resume notifications fail_fast distribution bundle; do
    if bash tests/cases.sh "$test"; then printf 'PASS %s\n' "$test"; ((passed+=1))
    else printf 'FAIL %s\n' "$test" >&2; ((failed+=1)); fi
done
printf '%s passed, %s failed\n' "$passed" "$failed"
((failed == 0))
