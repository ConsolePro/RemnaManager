"""Pure configuration renderers. Inputs must be validated by the caller."""
import json


def site_html(style='studio'):
    themes = {
        'studio': ('North Studio', 'Thoughtful design. Lasting impressions.', '#e7e0d5'),
        'journal': ('Field Notes', 'A journal of places, ideas and everyday discoveries.', '#dbe7df'),
        'workshop': ('The Workshop', 'Carefully made, one detail at a time.', '#e7dfe8'),
    }
    title, subtitle, color = themes[style]
    return f'''<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>{title}</title>
<style>body{{margin:0;background:{color};color:#252828;font:18px system-ui}}
main{{max-width:800px;margin:15vh auto;padding:32px}}h1{{font-size:clamp(40px,8vw,80px);letter-spacing:-.05em}}
article{{border-top:1px solid #888;padding:24px 0;margin-top:50px}}p{{line-height:1.7}}</style>
<main><small>EST. 2020 / INDEPENDENT PROJECT</small><h1>{title}</h1><p>{subtitle}</p>
<article><h2>About our work</h2><p>We bring together people who value simplicity, curiosity and craft.
Our next collection is taking shape. Come back soon for new stories and projects.</p></article>
<footer>© 2026 {title}</footer></main></html>'''


def http_site(domain):
    return f'''server {{
    listen 80; listen [::]:80;
    server_name {domain};
    server_tokens off;
    location ^~ /.well-known/acme-challenge/ {{ root /var/www/remnamanager-acme; }}
    location / {{ root /var/www/remnamanager/{domain}; try_files $uri $uri/ =404; }}
}}
'''


def tls_site(domain, port=443, upstream=None, gate=None):
    listen = f'listen {port} ssl http2; listen [::]:{port} ssl http2;' if port == 443 else f'listen 127.0.0.1:{port} ssl http2;'
    prelude = ''
    locations = f'location / {{ root /var/www/remnamanager/{domain}; try_files $uri $uri/ =404; }}'
    proxy = f'''proxy_pass http://127.0.0.1:{upstream};
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Port 443;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $rm_connection;
        proxy_read_timeout 300s;'''
    if upstream and not gate:
        locations = f'location / {{ {proxy} }}'
    if gate:
        # A dedicated secret path exchanges a capability for a Secure cookie.
        # Static site remains visible for every unauthenticated path.
        prelude = f'''map $cookie_rm_access_{gate[:12]} $rm_allowed {{
    default 0;
    "{gate}" 1;
}}
'''
        locations = f'''access_log off;
    add_header Referrer-Policy no-referrer always;
    location = /enter-{gate} {{
        add_header Set-Cookie "rm_access_{gate[:12]}={gate}; Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age=86400" always;
        add_header Referrer-Policy no-referrer always;
        return 302 /auth/login;
    }}
    location / {{
        error_page 418 = @panel;
        if ($rm_allowed = 1) {{ return 418; }}
        root /var/www/remnamanager/{domain};
        try_files $uri $uri/ =404;
    }}
    location @panel {{ {proxy} }}'''
    return prelude + f'''server {{
    {listen}
    server_name {domain};
    server_tokens off;
    ssl_certificate /etc/letsencrypt/live/{domain}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/{domain}/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_tickets off;
    {locations}
}}
'''


def reality_config(domain, private_key, short_id):
    return {
        'log': {'loglevel': 'warning'},
        'inbounds': [{'tag': 'VLESS_REALITY', 'listen': '0.0.0.0', 'port': 443,
            'protocol': 'vless', 'settings': {'clients': [], 'decryption': 'none'},
            'sniffing': {'enabled': True, 'destOverride': ['http', 'tls', 'quic'], 'routeOnly': True},
            'streamSettings': {'network': 'raw', 'security': 'reality', 'realitySettings': {
                'show': False, 'target': '127.0.0.1:8443', 'xver': 0,
                'serverNames': [domain], 'privateKey': private_key, 'shortIds': [short_id]}}}],
        'outbounds': [{'tag': 'DIRECT', 'protocol': 'freedom'}, {'tag': 'BLOCK', 'protocol': 'blackhole'}],
        'routing': {'rules': [{'type': 'field', 'ip': ['geoip:private'], 'outboundTag': 'BLOCK'}]},
    }


def node_compose(secret):
    return {'services': {'remnanode': {'image': 'remnawave/node:latest', 'container_name': 'remnanode',
        'restart': 'unless-stopped', 'network_mode': 'host',
        'environment': {'NODE_PORT': '2222', 'SECRET_KEY': secret},
        'logging': {'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '3'}}}}}


def subscription_compose(image='remnawave/subscription-page:latest'):
    return {'services': {'subscription': {'image': image, 'restart': 'unless-stopped',
        'env_file': ['.env'], 'ports': ['127.0.0.1:3010:3010'], 'networks': ['remnawave-network'],
        'logging': {'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '3'}}}},
        'networks': {'remnawave-network': {'external': True}}}


def checker_compose():
    return {'services': {'checker': {'image': 'kutovoys/xray-checker:latest', 'restart': 'unless-stopped',
        'env_file': ['.env'], 'ports': ['127.0.0.1:2112:2112'],
        'logging': {'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '3'}}}}}


def warp_config(config, enabled, domains=()):
    # Remove only manager-owned objects; preserve other routes and outbounds.
    config = json.loads(json.dumps(config))
    config['outbounds'] = [o for o in config.get('outbounds', []) if o.get('tag') != 'RM_WARP']
    rules = config.setdefault('routing', {}).setdefault('rules', [])
    rules[:] = [r for r in rules if r.get('outboundTag') != 'RM_WARP']
    if enabled:
        config['outbounds'].append({'tag': 'RM_WARP', 'protocol': 'socks',
            'settings': {'servers': [{'address': '127.0.0.1', 'port': 40000}]}})
        rules.insert(0, {'type': 'field', 'domain': ['domain:' + d for d in domains], 'outboundTag': 'RM_WARP'})
    return config
