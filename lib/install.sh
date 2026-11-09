#!/usr/bin/env bash
install_commands() {
    local launcher profile=/etc/profile.d/remnamanager.sh root_home bashrc line
    for launcher in /usr/local/bin/remna_manager /usr/local/bin/re; do
        if [[ -e $launcher ]] && ! grep -Fq '# RemnaManager command' "$launcher"; then
            die "$launcher уже существует и не принадлежит RemnaManager."
        fi
    done
    if [[ -e $profile ]] && ! grep -Fq '# RemnaManager shortcuts' "$profile"; then
        die "$profile уже существует и не принадлежит RemnaManager."
    fi
    for launcher in /usr/local/bin/remna_manager /usr/local/bin/re; do
        {
            printf '%s\n' '#!/bin/bash' '# RemnaManager command'
            printf 'exec /bin/bash %q "$@"\n' "$APP_DIR/manager.sh"
        } | save "$launcher"
        chmod 755 "$launcher"
    done
    {
        printf '%s\n' '# RemnaManager shortcuts' 'case $- in *i*) ;; *) return 0 ;; esac' 'if [ -n "${BASH_VERSION:-}" ] && [ "$(id -u)" = 0 ]; then'
        printf '    . %q\n' "$APP_DIR/lib/shortcuts.sh"
        printf 'fi\n'
    } | save "$profile"
    chmod 644 "$profile"
    root_home=$(getent passwd 0 | cut -d: -f6)
    [[ -n $root_home && -d $root_home ]] || die 'Не найден домашний каталог root.'
    bashrc=$root_home/.bashrc
    line='. /etc/profile.d/remnamanager.sh # RemnaManager shortcuts'
    if [[ ! -f $bashrc ]] || ! grep -Fqx "$line" "$bashrc"; then
        printf '\n%s\n' "$line" >>"$bashrc"
    fi
}
