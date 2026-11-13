set -Eeuo pipefail
manager=$1
acme="$manager/acme/acme.sh"
systemctl enable --now cron
renewal_installed() {
    local entries line
    entries=$(crontab -l 2>/dev/null) || return 1
    while IFS= read -r line; do
        line=${line#"${line%%[![:space:]]*}"}
        [[ -n $line && $line != \#* ]] || continue
        # acme.sh writes "/path/to/home"/acme.sh, not /path/to/home/acme.sh.
        # Normalize quoting for comparison only; never execute crontab contents.
        line=${line//\"/}
        line=${line//\'/}
        if [[ $line == *" $acme --cron "* ]]; then return 0; fi
    done <<< "$entries"
    return 1
}
# Let acme.sh maintain its own job and preserve all unrelated crontab entries.
if ! renewal_installed; then
    "$acme" --install-cronjob --home "$manager/acme" --config-home "$manager/acme"
fi
renewal_installed || { echo 'ACME renewal cron is missing for this installation; certificates were preserved.' >&2; exit 1; }
