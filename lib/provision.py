"""Idempotent host provisioning, used locally and over authenticated SSH."""
import hashlib
import ipaddress
import json
import os
import pathlib
import re
import secrets
import shutil
import socket
import time
import urllib.request

from .runtime import API, ROOT, compose, env_set, load, output, run, save
from .render import http_site, tls_site, site_html, node_compose


def domain(value):
    value = value.strip().lower()
    if len(value) > 253 or not re.fullmatch(r'(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?', value):
        raise ValueError('Нужен DNS-домен без схемы, пути и специальных символов.')
    return value


def port(value):
    value = int(value)
    if not 1 <= value <= 65535:
        raise ValueError('Порт должен быть от 1 до 65535.')
    return value


def download(url, target):
    request = urllib.request.Request(url, headers={'User-Agent': 'RemnaManager/0.1'})
    with urllib.request.urlopen(request, timeout=90) as response:
        content = response.read(32 * 1024 * 1024)
    target = pathlib.Path(target)
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    target.write_bytes(content)
    target.chmod(0o600)


def platform_check():
    values = {}
    for line in pathlib.Path('/etc/os-release').read_text().splitlines():
        if '=' in line:
            k, v = line.split('=', 1)
            values[k] = v.strip('"')
    if (values.get('ID'), values.get('VERSION_ID')) not in {
            ('debian', '12'), ('debian', '13'), ('ubuntu', '22.04'), ('ubuntu', '24.04')}:
        raise RuntimeError('Поддерживаются Debian 12/13 и Ubuntu 22.04/24.04.')
    return values


def packages(node=False):
    values = platform_check()
    run(['apt-get', 'update'])
    run(['apt-get', 'install', '-y', 'ca-certificates', 'curl', 'openssl', 'gnupg', 'nginx', 'certbot',
         'openssh-client', 'nftables', 'unzip'], env={**os.environ, 'DEBIAN_FRONTEND': 'noninteractive'})
    if shutil.which('docker'):
        run(['docker', 'compose', 'version'])
    else:
        key = pathlib.Path('/etc/apt/keyrings/docker.asc')
        download(f'https://download.docker.com/linux/{values["ID"]}/gpg', key)
        key.parent.chmod(0o755)
        key.chmod(0o644)
        arch = output(['dpkg', '--print-architecture'])
        save('/etc/apt/sources.list.d/remnamanager-docker.list',
             f'deb [arch={arch} signed-by={key}] https://download.docker.com/linux/{values["ID"]} {values["VERSION_CODENAME"]} stable\n')
        pathlib.Path('/etc/apt/sources.list.d/remnamanager-docker.list').chmod(0o644)
        run(['apt-get', 'update'])
        run(['apt-get', 'install', '-y', 'docker-ce', 'docker-ce-cli', 'containerd.io', 'docker-compose-plugin'])
    run(['systemctl', 'enable', '--now', 'docker', 'nginx'])
    # Remove only the distribution's symlink, never an arbitrary site named default.
    enabled = pathlib.Path('/etc/nginx/sites-enabled/default')
    if enabled.is_symlink() and str(enabled.resolve()) == '/etc/nginx/sites-available/default':
        enabled.unlink()
    common = pathlib.Path('/etc/nginx/conf.d/remnamanager-common.conf')
    if not common.exists():
        reject = ''
        if not node:
            import subprocess
            version_output = subprocess.run(['nginx', '-v'], capture_output=True, text=True, check=True)
            match = re.search(r'nginx/(\d+)\.(\d+)\.(\d+)', version_output.stderr + version_output.stdout)
            version = tuple(map(int, match.groups())) if match else (0, 0, 0)
            if version >= (1, 19, 4):
                reject = 'ssl_reject_handshake on;'
            else:
                # Ubuntu 22.04 ships nginx 1.18, which lacks ssl_reject_handshake.
                key, cert = ROOT / 'default.key', ROOT / 'default.crt'
                if not key.exists() or not cert.exists():
                    run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '3650',
                         '-subj', '/CN=localhost', '-keyout', key, '-out', cert],
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                reject = f'ssl_certificate {cert}; ssl_certificate_key {key}; return 444;'
        save(common, '''map $http_upgrade $rm_connection { default upgrade; '' close; }
server { listen 80 default_server; listen [::]:80 default_server; server_name _; return 444; }
''' + ('' if node else 'server { listen 443 ssl default_server; listen [::]:443 ssl default_server; server_name _; ' + reject + ' }\n'))
    nginx_reload()
    hook = pathlib.Path('/etc/letsencrypt/renewal-hooks/deploy/remnamanager-nginx')
    save(hook, '#!/bin/sh\nset -eu\n/usr/sbin/nginx -t\n/bin/systemctl reload nginx\n')
    hook.chmod(0o700)
    run(['systemctl', 'enable', '--now', 'certbot.timer'])


def nginx_reload():
    run(['nginx', '-t'])
    run(['systemctl', 'reload', 'nginx'])


def nginx_write(path, content):
    path = pathlib.Path(path)
    previous = path.read_text() if path.exists() else None
    save(path, content)
    try:
        nginx_reload()
    except Exception:
        if previous is None:
            path.unlink()
        else:
            save(path, previous)
        nginx_reload()
        raise


def site(domain_name, email, *, upstream=None, gate=None, node=False):
    domain_name = domain(domain_name)
    if not re.fullmatch(r'[^\s@]+@[^\s@]+\.[^\s@]+', email):
        raise ValueError('Неверный email ACME.')
    socket.getaddrinfo(domain_name, 80)
    webroot = pathlib.Path('/var/www/remnamanager-acme')
    webroot.mkdir(parents=True, exist_ok=True, mode=0o755)
    webroot.chmod(0o755)
    dest = pathlib.Path('/var/www/remnamanager') / domain_name
    dest.mkdir(parents=True, exist_ok=True, mode=0o755)
    # Nginx's www-data must traverse the public content directory.
    dest.parent.chmod(0o755)
    dest.chmod(0o755)
    if not (dest / 'index.html').exists():
        save(dest / 'index.html', site_html())
        (dest / 'index.html').chmod(0o644)
    conf = pathlib.Path('/etc/nginx/conf.d') / f'rm-{domain_name}.conf'
    # Preserve an already working TLS vhost while renewing or reinstalling.
    if not conf.exists():
        nginx_write(conf, http_site(domain_name))
    run(['certbot', 'certonly', '--webroot', '-w', webroot, '-d', domain_name,
         '--email', email, '--agree-tos', '--non-interactive', '--keep-until-expiring'])
    nginx_write(conf, http_site(domain_name) + tls_site(domain_name,
        port=8443 if node else 443, upstream=upstream, gate=gate))


def firewall(panel_ip):
    ip = ipaddress.ip_address(panel_ip)
    family = 'ip' if ip.version == 4 else 'ip6'
    rules = f'''table inet remnamanager {{
 chain input {{ type filter hook input priority -10; policy accept;
  iifname "lo" accept
  tcp dport 2222 {family} saddr {ip} accept
  tcp dport 2222 drop
 }}
}}
'''
    path = ROOT / 'node-firewall.nft'
    save(path, rules)
    # An independent service owns exactly one table and leaves existing policies intact.
    script = ROOT / 'node-firewall.sh'
    save(script, '#!/bin/sh\nset -eu\nif nft list table inet remnamanager >/dev/null 2>&1; then\n'
         ' { echo "delete table inet remnamanager"; cat /opt/remnamanager/node-firewall.nft; } | nft -f -\n'
         'else\n nft -f /opt/remnamanager/node-firewall.nft\nfi\n')
    script.chmod(0o700)
    save('/etc/systemd/system/remnamanager-firewall.service', '''[Unit]
Description=RemnaManager node API restriction
After=network-pre.target nftables.service
Before=docker.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/opt/remnamanager/node-firewall.sh
ExecReload=/opt/remnamanager/node-firewall.sh
[Install]
WantedBy=multi-user.target
''')
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'enable', 'remnamanager-firewall'])
    run([script])


def node_install(job):
    if pathlib.Path('/opt/remnawave/docker-compose.yml').exists():
        raise RuntimeError('Нода должна быть на отдельном сервере: здесь найдена панель Remnawave.')
    directory = pathlib.Path('/opt/remnanode')
    if directory.exists() and not (directory / '.remnamanager').exists():
        raise RuntimeError('/opt/remnanode уже существует и не управляется RemnaManager.')
    packages(node=True)
    firewall(job['panel_ip'])
    site(job['domain'], job['email'], node=True)
    directory.mkdir(exist_ok=True, mode=0o700)
    save(directory / '.remnamanager', 'managed\n')
    path = directory / 'compose.yaml'
    if path.exists():
        save(directory / 'compose.previous.json', json.loads(path.read_text()))
    save(path, node_compose(job['secret']))
    compose(directory, 'config', '--quiet')
    compose(directory, 'pull')
    compose(directory, 'up', '-d', '--wait', '--wait-timeout', '120')
    save(ROOT / 'node.json', {k: job[k] for k in ('panel_ip', 'domain', 'email')})


def warp_install():
    values = platform_check()
    key = pathlib.Path('/etc/apt/keyrings/cloudflare-warp.gpg')
    temporary = ROOT / 'cloudflare-key.asc'
    download('https://pkg.cloudflareclient.com/pubkey.gpg', temporary)
    key.parent.mkdir(parents=True, exist_ok=True)
    key.parent.chmod(0o755)
    run(['gpg', '--batch', '--yes', '--dearmor', '--output', key, temporary])
    key.chmod(0o644)
    save('/etc/apt/sources.list.d/remnamanager-warp.list',
        f'deb [signed-by={key}] https://pkg.cloudflareclient.com/ {values["VERSION_CODENAME"]} main\n')
    pathlib.Path('/etc/apt/sources.list.d/remnamanager-warp.list').chmod(0o644)
    run(['apt-get', 'update'])
    run(['apt-get', 'install', '-y', 'cloudflare-warp'])
    run(['systemctl', 'enable', '--now', 'warp-svc'])
    # Registration is created only if absent. Existing registrations are retained.
    import subprocess
    if subprocess.run(['warp-cli', '--accept-tos', 'registration', 'show'],
                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        run(['warp-cli', '--accept-tos', 'registration', 'new'])
    run(['warp-cli', '--accept-tos', 'mode', 'proxy'])
    run(['warp-cli', '--accept-tos', 'proxy', 'port', '40000'])
    run(['warp-cli', '--accept-tos', 'connect'])
    for attempt in range(12):
        import subprocess
        result = subprocess.run(['curl', '--fail', '--silent', '--max-time', '10',
            '--socks5-hostname', '127.0.0.1:40000', 'https://www.cloudflare.com/cdn-cgi/trace'],
            stdout=subprocess.PIPE, text=True)
        if result.returncode == 0 and 'warp=on' in result.stdout:
            return
        time.sleep(2)
    raise RuntimeError('WARP не прошёл проверку. Маршрутизация Xray не изменена.')


def xray_update():
    """Verified upstream archive, persistent Compose override, rollback on failed restart."""
    import subprocess
    import zipfile
    directory = pathlib.Path('/opt/remnanode')
    metadata = ROOT / 'xray-release.json'
    download('https://api.github.com/repos/XTLS/Xray-core/releases/latest', metadata)
    release = json.loads(metadata.read_text())
    arch = output(['uname', '-m'])
    asset_name = {'x86_64': 'Xray-linux-64.zip', 'aarch64': 'Xray-linux-arm64-v8a.zip'}.get(arch)
    if not asset_name:
        raise RuntimeError('Обновление Xray поддерживает amd64 и arm64.')
    assets = {a['name']: a['browser_download_url'] for a in release['assets']}
    archive, checksums = ROOT / asset_name, ROOT / (asset_name + '.dgst')
    download(assets[asset_name], archive)
    download(assets[asset_name + '.dgst'], checksums)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    if digest.lower() not in checksums.read_text().lower():
        raise RuntimeError('SHA256 Xray не совпадает.')
    binaries = directory / 'xray'
    binaries.mkdir(exist_ok=True, mode=0o755)
    target = binaries / release['tag_name']
    with zipfile.ZipFile(archive) as bundle:
        target.write_bytes(bundle.read('xray'))
    target.chmod(0o755)
    run([target, 'version'])
    override = directory / 'compose.override.yaml'
    old = override.read_text() if override.exists() else None
    try:
        merged = json.loads(old) if old else {}
    except json.JSONDecodeError:
        raise RuntimeError('Найден сторонний YAML override. Сначала объедините его с настройками менеджера вручную.') from None
    service = merged.setdefault('services', {}).setdefault('remnanode', {})
    mounts = service.setdefault('volumes', [])
    mounts[:] = [mount for mount in mounts if not (
        isinstance(mount, str) and mount.endswith(':/usr/local/bin/xray:ro'))]
    mounts.append(f'{target}:/usr/local/bin/xray:ro')
    save(override, merged)
    try:
        compose(directory, 'up', '-d', '--force-recreate', '--wait', '--wait-timeout', '90')
        time.sleep(4)
        run(['docker', 'exec', 'remnanode', '/usr/local/bin/xray', 'version'])
    except Exception:
        if old is None:
            override.unlink()
        else:
            save(override, old)
        compose(directory, 'up', '-d', '--force-recreate')
        raise


def execute_job(job):
    action = job['action']
    if action == 'install':
        node_install(job)
    elif action in ('update', 'restart', 'start', 'stop', 'status'):
        directory = '/opt/remnanode'
        if action == 'update':
            compose(directory, 'pull')
            compose(directory, 'up', '-d', '--wait', '--wait-timeout', '120')
        elif action == 'restart':
            compose(directory, 'restart')
        elif action == 'status':
            compose(directory, 'ps')
            compose(directory, 'logs', '--tail', '50', '--no-color')
        else:
            compose(directory, action)
    elif action == 'template':
        state = load(ROOT / 'node.json')
        path = pathlib.Path('/var/www/remnamanager') / state['domain'] / 'index.html'
        save(path, job['html'] if 'html' in job else site_html(job['style']))
        path.chmod(0o644)
    elif action == 'xray':
        xray_update()
    elif action == 'warp-install':
        warp_install()
    elif action == 'warp-remove':
        run(['warp-cli', '--accept-tos', 'disconnect'])
        run(['apt-get', 'remove', '-y', 'cloudflare-warp'])
    elif action == 'bbr':
        run(['modprobe', 'tcp_bbr'])
        save('/etc/sysctl.d/90-remnamanager-bbr.conf',
             'net.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n')
        run(['sysctl', '-p', '/etc/sysctl.d/90-remnamanager-bbr.conf'])
    else:
        raise ValueError('Неизвестная операция агента.')
