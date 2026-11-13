set -Eeuo pipefail
manager=$1
acme="$manager/acme/acme.sh"
systemctl enable --now cron
if [[ -x $manager/renew-dns.sh ]]; then
    # Replace only this installation's known ACME job and our own marker.
    entries=$(crontab -l 2>/dev/null || true)
    kept=""
    while IFS= read -r line; do
        normalized=${line//\"/}; normalized=${normalized//\'/}
        if [[ $normalized == *" $acme --cron "* || $line == *"# RemnaManager DNS $manager" ]]; then continue; fi
        kept+="$line"$'\n'
    done <<< "$entries"
    printf '%s30 3 * * * %q # RemnaManager DNS %s\n' "$kept" "$manager/renew-dns.sh" "$manager" | crontab -
    exit 0
fi
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
