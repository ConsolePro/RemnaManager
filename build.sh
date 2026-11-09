#!/usr/bin/env bash
# Self-contained Shell distribution; repeatable with the same GNU tar version.
set -Eeuo pipefail
umask 077
cd -- "$(dirname -- "$0")"
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
files=(manager.sh agent.sh lib/runtime.sh lib/render.sh lib/provision.sh lib/ssh.sh lib/panel.sh lib/nodes.sh lib/monitor.sh lib/menu.sh README.md)
for file in remnamanager.sh build.sh "${files[@]}"; do [[ $file != *.sh ]] || bash -n "$file"; done
tar --format=ustar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner --mode=0600 -cf "$temporary/app.tar" "${files[@]}"
digest=$(sha256sum "$temporary/app.tar"); digest=${digest%% *}
mkdir -p dist
cat >"$temporary/distribution" <<'HEADER'
#!/bin/sh
# Built by build.sh from Shell sources. Run: sudo sh remnamanager.sh
set -eu
[ "$(uname -s)" = Linux ] && [ "$(id -u)" -eq 0 ] || { echo 'Нужны Linux и запуск от root.' >&2; exit 1; }
umask 077
root=/opt/remnamanager
mkdir -p "$root"
chmod 700 "$root"
exec 8>"$root/install.lock"
flock -n 8 || { echo 'Другой установщик уже работает.' >&2; exit 1; }
exec 9>"$root/manager.lock"
flock -n 9 || { echo 'Менеджер уже работает. Закройте его перед обновлением.' >&2; exit 1; }
temporary=$(mktemp -d "$root/install.XXXXXXXX")
trap 'rm -rf -- "$temporary"' EXIT HUP INT TERM
sed '1,/^__REMNAMANAGER_PAYLOAD__$/d' "$0" | base64 -d >"$temporary/app.tar"
HEADER
# shellcheck disable=SC2129
printf 'printf '\''%%s  %%s\\n'\'' '\''%s'\'' "$temporary/app.tar" | sha256sum -c - >/dev/null\n' "$digest" >>"$temporary/distribution"
cat >>"$temporary/distribution" <<'HEADER'
mkdir "$temporary/app"
tar --no-same-owner -xf "$temporary/app.tar" -C "$temporary/app"
if [ -L "$root/app" ] || [ -L "$root/app/lib" ]; then echo 'Каталог приложения не должен быть символьной ссылкой.' >&2; exit 1; fi
mkdir -p "$root/app/lib"
for file in manager.sh agent.sh lib/runtime.sh lib/render.sh lib/provision.sh lib/ssh.sh lib/panel.sh lib/nodes.sh lib/monitor.sh lib/menu.sh README.md; do
    install -m 600 "$temporary/app/$file" "$root/app/$file"
done
rm -rf -- "$temporary"
trap - EXIT HUP INT TERM
flock -u 9
exec 9>&-
flock -u 8
exec 8>&-
exec bash "$root/app/manager.sh" "$@"
exit 1
__REMNAMANAGER_PAYLOAD__
HEADER
base64 "$temporary/app.tar" >>"$temporary/distribution"
mv -f "$temporary/distribution" dist/remnamanager.sh
chmod 755 dist/remnamanager.sh
printf 'dist/remnamanager.sh: Shell bundle, SHA256 %s\n' "$digest"
