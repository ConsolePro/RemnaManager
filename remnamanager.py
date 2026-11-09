#!/usr/bin/env python3
"""RemnaManager interactive console. Linux, root, Python 3.10+."""
import base64
import getpass
import ipaddress
import json
import os
import pathlib
import re
import secrets
import shutil
import subprocess
import sys
import time
import urllib.error

from lib.runtime import API, ROOT, compose, env_set, load, output, run, save, wait_http
from lib.provision import domain, port, download, packages, site, nginx_write, platform_check
from lib.render import reality_config, subscription_compose, checker_compose, warp_config, site_html
from lib.ssh import SSH, host


PANEL = pathlib.Path('/opt/remnawave')
SUB = PANEL / 'subscription'
CHECKER = ROOT / 'checker'
STATE = ROOT / 'state.json'


def ask(label, default=None, secret=False):
    prompt = label + (f' [{default}]' if default is not None else '') + ': '
    value = (getpass.getpass(prompt) if secret else input(prompt)).strip()
    value = value or default
    if value is None or not str(value):
        raise ValueError('Значение не может быть пустым.')
    if any(c in str(value) for c in '\r\n\0'):
        raise ValueError('Недопустимые символы.')
    return value


def confirm(text):
    return input(text + ' Введите ДА: ').strip() == 'ДА'


def menu(title, entries):
    print('\n' + title)
    for key, label in entries:
        print(f'{key}. {label}')
    return input('> ').strip()


class Manager:
    def __init__(self):
        self.state = load(STATE, {'nodes': {}})

    def persist(self):
        save(STATE, self.state)

    def api(self):
        token = self.state.get('token')
        if not token:
            raise RuntimeError('Сначала установите панель или подключите её API через Remnawave CLI.')
        return API(token)

    def backup(self):
        directory = ROOT / 'backups' / time.strftime('%Y%m%d-%H%M%S')
        directory.mkdir(parents=True, mode=0o700)
        # pg_dump inside the DB container uses its own POSTGRES_* variables.
        with (directory / 'database.dump').open('wb') as stream:
            run(['docker', 'compose', '--project-directory', PANEL, 'exec', '-T', 'remnawave-db',
                 'sh', '-c', 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc'], stdout=stream)
        for path in (PANEL / '.env', PANEL / 'docker-compose.yml', STATE):
            if path.exists():
                shutil.copy2(path, directory / path.name)
        print(f'Резервная копия: {directory}')
        return directory

    def panel_install(self):
        if (PANEL / 'docker-compose.yml').exists() and not self.state.get('panel'):
            raise RuntimeError('Найдена чужая установка /opt/remnawave. Автоматическая перезапись запрещена.')
        if self.state.get('panel', {}).get('ready'):
            compose(PANEL, 'up', '-d', '--wait', '--wait-timeout', '180')
            print('Панель уже установлена и запущена.')
            return
        pending = self.state.get('panel', {})
        panel_domain = domain(ask('Домен панели', pending.get('domain')))
        sub_domain = domain(ask('Домен страницы подписки', pending.get('sub_domain')))
        if panel_domain == sub_domain:
            raise ValueError('Панели и подписке нужны разные домены.')
        email = ask('Email для ACME', pending.get('email'))
        panel_ip = str(ipaddress.ip_address(ask('Публичный IP управляющего сервера', pending.get('ip'))))
        admin = ask('Логин администратора', pending.get('admin', 'admin'))
        if not re.fullmatch(r'[A-Za-z0-9_]{3,30}', admin):
            raise ValueError('Логин: 3–30 латинских букв, цифр или _.')
        if (PANEL / '.env').exists() and any(pending.get(key) != value for key, value in (
                ('domain', panel_domain), ('sub_domain', sub_domain), ('admin', admin))):
            raise ValueError('Установка уже подготовлена. Для её продолжения сохраните прежние домены и логин.')
        password = pending.get('password') or ('Rm9' + secrets.token_urlsafe(30))
        gate = pending.get('gate') or secrets.token_hex(32)
        self.state['panel'] = {**pending, 'domain': panel_domain, 'sub_domain': sub_domain,
            'email': email, 'ip': panel_ip, 'admin': admin, 'password': password, 'gate': gate}
        self.persist()  # Checkpoint credentials before network or service operations.
        packages()
        PANEL.mkdir(exist_ok=True, mode=0o700)
        if not (PANEL / '.env').exists():
            release_path = ROOT / 'backend-release.json'
            download('https://api.github.com/repos/remnawave/backend/releases/latest', release_path)
            version = json.loads(release_path.read_text())['tag_name']
            base = f'https://raw.githubusercontent.com/remnawave/backend/{version}/'
            staging = ROOT / 'panel-bootstrap'
            download(base + '.env.sample', staging / '.env')
            download(base + 'docker-compose-prod.yml', staging / 'docker-compose.yml')
            db_password = secrets.token_hex(32)
            env_set(staging / '.env', {
                'APP_SECRET': secrets.token_hex(64), 'METRICS_PASS': secrets.token_hex(32),
                'WEBHOOK_SECRET_HEADER': secrets.token_hex(32), 'POSTGRES_PASSWORD': db_password,
                'DATABASE_URL': f'"postgresql://postgres:{db_password}@remnawave-db:5432/postgres"',
                'PANEL_DOMAIN': panel_domain, 'FRONT_END_DOMAIN': panel_domain, 'SUB_PUBLIC_DOMAIN': sub_domain})
            # Keep DB and cache versions from this upstream release; pin backend itself.
            text = (staging / 'docker-compose.yml').read_text()
            text = re.sub(r'image: (?:ghcr.io/)?remnawave/backend:[^\s]+',
                          f'image: remnawave/backend:{version.lstrip("v")}', text)
            save(PANEL / 'docker-compose.yml', text)
            save(PANEL / '.env', (staging / '.env').read_text())
            shutil.rmtree(staging)
            self.state['panel']['version'] = version
            self.persist()
        compose(PANEL, 'config', '--quiet')
        compose(PANEL, 'pull')
        compose(PANEL, 'up', '-d', '--wait', '--wait-timeout', '240')
        if not self.state.get('token'):
            api = API()
            status = api.call('GET', '/auth/status')
            if status.get('isRegisterAllowed'):
                token = api.call('POST', '/auth/register', {'username': admin, 'password': password})['accessToken']
            else:
                token = api.call('POST', '/auth/login', {'username': admin, 'password': password})['accessToken']
            api.token = token
            self.state['token'] = api.call('POST', '/tokens',
                {'name': 'RemnaManager', 'expiresInDays': 3650, 'scopes': ['*']})['token']
            self.persist()
            self.state['subscription_token'] = api.call('POST', '/tokens',
                {'name': 'RemnaManager Subpage', 'expiresInDays': 3650,
                 'scopes': ['subscriptions:read', 'subscription-page-configs:read', 'system:read']})['token']
            self.persist()
        site(panel_domain, email, upstream=3000, gate=gate)
        self.state['panel']['ready'] = True
        self.persist()
        self.credentials()
        if confirm('Сразу установить страницу подписки?'):
            self.sub_install()

    def credentials(self):
        panel = self.state['panel']
        print(f'\nВход: https://{panel["domain"]}/enter-{panel["gate"]}')
        print(f'Логин: {panel["admin"]}\nПароль: {panel["password"]}')
        print(f'Секреты сохранены в {STATE} (root, 0600).')

    def panel_update(self):
        self.backup()
        metadata = ROOT / 'backend-release.json'
        download('https://api.github.com/repos/remnawave/backend/releases/latest', metadata)
        version = json.loads(metadata.read_text())['tag_name']
        print(f'Целевая версия: {version}. Changelog: https://github.com/remnawave/backend/releases/tag/{version}')
        if not confirm('Применить обновление панели? Миграции БД могут требовать восстановления из копии при откате.'):
            return
        path = PANEL / 'docker-compose.yml'
        text = path.read_text()
        updated, count = re.subn(r'image: (?:ghcr.io/)?remnawave/backend:[^\s]+',
            f'image: remnawave/backend:{version.lstrip("v")}', text)
        if count != 1:
            raise RuntimeError('Не удалось однозначно определить образ backend.')
        save(path, updated)
        try:
            compose(PANEL, 'config', '--quiet')
            compose(PANEL, 'pull', 'remnawave')
            compose(PANEL, 'up', '-d', '--wait', '--wait-timeout', '240')
        except Exception:
            save(path, text)
            print('Compose восстановлен. При миграции БД автоматический откат не выполняется; копия сохранена.')
            raise
        self.state['panel']['version'] = version
        self.persist()

    def cli(self):
        while True:
            choice = menu('Remnawave CLI', [('1', 'Статус компонентов'), ('2', 'Показать доступ к панели'),
                ('3', 'Резервная копия БД'), ('4', 'Сменить API-токен'), ('5', 'Запустить панель'),
                ('6', 'Штатный Remnawave CLI (в контейнере)'), ('0', 'Назад')])
            if choice == '0': return
            if choice == '1': compose(PANEL, 'ps')
            elif choice == '2': self.credentials()
            elif choice == '3': self.backup()
            elif choice == '4':
                token = ask('API-токен панели', secret=True)
                API(token).call('GET', '/nodes')
                self.state['token'] = token
                self.persist()
            elif choice == '5': compose(PANEL, 'up', '-d', '--wait', '--wait-timeout', '180')
            elif choice == '6': compose(PANEL, 'exec', 'remnawave', 'cli')

    def panel_menu(self):
        while True:
            choice = menu('Панель и компоненты', [('1', 'Установить панель'), ('2', 'Обновить панель'),
                ('3', 'Остановить панель'), ('4', 'Логи панели'), ('5', 'Remnawave CLI'), ('0', 'Назад')])
            if choice == '0': return
            if choice == '1': self.panel_install()
            elif choice == '2': self.panel_update()
            elif choice == '3': compose(PANEL, 'stop')
            elif choice == '4': compose(PANEL, 'logs', '--tail', '100', '--no-color')
            elif choice == '5': self.cli()

    def sub_install(self):
        panel = self.state['panel']
        if not self.state.get('subscription_token'):
            auth = API()
            password = ask('Пароль администратора для создания отдельного токена подписки', secret=True)
            auth.token = auth.call('POST', '/auth/login',
                {'username': panel['admin'], 'password': password})['accessToken']
            self.state['subscription_token'] = auth.call('POST', '/tokens',
                {'name': 'RemnaManager Subpage', 'expiresInDays': 3650,
                 'scopes': ['subscriptions:read', 'subscription-page-configs:read', 'system:read']})['token']
            self.persist()
        token = self.state['subscription_token']
        SUB.mkdir(exist_ok=True, mode=0o700)
        compose_path = SUB / 'compose.yaml'
        if not compose_path.exists(): save(compose_path, subscription_compose())
        env_set(SUB / '.env', {'APP_PORT': '3010', 'REMNAWAVE_PANEL_URL': 'http://remnawave:3000',
            'REMNAWAVE_API_TOKEN': token, 'TRUST_PROXY': '1'})
        compose(SUB, 'config', '--quiet')
        compose(SUB, 'pull')
        compose(SUB, 'up', '-d', '--wait', '--wait-timeout', '120')
        wait_http('http://127.0.0.1:3010/')
        site(panel['sub_domain'], panel['email'], upstream=3010)
        env_set(PANEL / '.env', {'SUB_PUBLIC_DOMAIN': panel['sub_domain']})
        compose(PANEL, 'up', '-d', '--wait', '--wait-timeout', '180')
        self.state['subscription'] = True
        self.persist()
        print(f'Страница подписки: https://{panel["sub_domain"]}/<shortUuid>')

    def sub_custom(self):
        choice = menu('Кастомизация подписки', [('1', 'Официальный шаблон'),
            ('2', 'Свой совместимый Docker-образ'), ('3', 'Открыть сведения о Subpage Builder'), ('0', 'Назад')])
        if choice == '3':
            print('В панели: Subpage Builder — логотип, тексты, приложения и инструкции.\n'
                  'https://docs.rw/install/subscription-page/bundled/#configuring-subscription-page-optional')
            return
        if choice not in ('1', '2'): return
        image = 'remnawave/subscription-page:latest' if choice == '1' else ask('Docker-образ (совместимый с API и портом 3010)')
        if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,255}', image):
            raise ValueError('Неверное имя образа.')
        if choice == '2' and not confirm(f'Запустить сторонний образ {image}? Он получит API-токен подписки.'):
            return
        path = SUB / 'compose.yaml'
        old = path.read_text()
        save(path, subscription_compose(image))
        try:
            compose(SUB, 'pull')
            compose(SUB, 'up', '-d', '--wait', '--wait-timeout', '120')
            wait_http('http://127.0.0.1:3010/')
        except Exception:
            save(path, old)
            compose(SUB, 'up', '-d')
            raise

    def sub_menu(self):
        while True:
            choice = menu('Страница подписки', [('1', 'Установка / запуск'), ('2', 'Обновление'),
                ('3', 'Кастомизация'), ('4', 'Остановить'), ('0', 'Назад')])
            if choice == '0': return
            if choice == '1': self.sub_install()
            elif choice == '2':
                compose(SUB, 'pull'); compose(SUB, 'up', '-d', '--wait', '--wait-timeout', '120')
                wait_http('http://127.0.0.1:3010/')
            elif choice == '3': self.sub_custom()
            elif choice == '4': compose(SUB, 'stop')

    def nodes(self):
        response = self.api().call('GET', '/nodes')
        return response if isinstance(response, list) else response['nodes']

    def select_node(self):
        nodes = self.nodes()
        entries = [(str(i), f'{n["name"]} ({n["address"]}, '
                    f'{"остановлена" if n.get("isDisabled") else "online" if n.get("isConnected") else "offline"})')
                    for i, n in enumerate(nodes, 1)]
        choice = menu('Ноды панели', entries + [('a', 'Добавить ноду'), ('0', 'Назад')])
        if choice == 'a':
            self.node_add(); return None
        if choice.isdigit() and 1 <= int(choice) <= len(nodes):
            return nodes[int(choice) - 1]
        return None

    def connection(self, node):
        stored = self.state['nodes'].get(node['uuid'])
        if stored is None:
            stored = {'ssh_host': host(ask('SSH IP/домен', node['address'])),
                      'ssh_port': port(ask('SSH порт', '22'))}
            self.state['nodes'][node['uuid']] = stored
            self.persist()
        return SSH(stored)

    def node_add(self):
        panel = self.state['panel']
        name = ask('Имя ноды (3–30 символов)')
        if not 3 <= len(name) <= 30:
            raise ValueError('Имя должно содержать 3–30 символов.')
        address = host(ask('Публичный IP или домен ноды'))
        ssh_port = port(ask('SSH порт', '22'))
        cover = domain(ask('Домен сайта-заглушки (DNS должен указывать на ноду)'))
        if address == panel['ip'] or cover in (panel['domain'], panel['sub_domain']):
            raise ValueError('В этой версии нужна отдельная нода и отдельный домен заглушки.')
        # The journal makes API creation resumable after a network/provisioning failure.
        journal_path = ROOT / 'pending' / (secrets.token_hex(8) + '.json')
        journal = {'name': name, 'address': address, 'ssh_host': address, 'ssh_port': ssh_port,
                   'domain': cover, 'email': panel['email'], 'panel_ip': panel['ip']}
        save(journal_path, journal)
        self.finish_node(journal, journal_path)

    def finish_node(self, journal, journal_path):
        api = self.api()
        transport = SSH(journal)
        transport.deploy()
        if 'secret' not in journal:
            journal['secret'] = api.call('GET', '/keygen')['secretKey']
            save(journal_path, journal)
        transport.job({'action': 'install', **{key: journal[key] for key in (
            'secret', 'domain', 'email', 'panel_ip')}}, deploy=False)
        if 'profile' not in journal:
            # OpenSSL emits 32 raw X25519 bytes at the end of PKCS#8 DER.
            private = subprocess.run(['openssl', 'genpkey', '-algorithm', 'X25519', '-outform', 'DER'],
                                     check=True, stdout=subprocess.PIPE).stdout[-32:]
            key = base64.urlsafe_b64encode(private).decode().rstrip('=')
            profile_name = 'RM_' + secrets.token_hex(6)
            config = reality_config(journal['domain'], key, secrets.token_hex(8))
            profile = api.call('POST', '/config-profiles', {'name': profile_name, 'config': config})
            journal['profile'] = profile['uuid']
            save(journal_path, journal)
        if 'inbound' not in journal:
            inbounds = api.call('GET', f'/config-profiles/{journal["profile"]}/inbounds')['inbounds']
            journal['inbound'] = next(i['uuid'] for i in inbounds if i['tag'] == 'VLESS_REALITY')
            save(journal_path, journal)
        if 'uuid' not in journal:
            node = api.call('POST', '/nodes', {'name': journal['name'], 'address': journal['address'], 'port': 2222,
                'configProfile': {'activeConfigProfileUuid': journal['profile'], 'activeInbounds': [journal['inbound']]}})
            journal['uuid'] = node['uuid']
            save(journal_path, journal)
        if 'host_uuid' not in journal:
            created = api.call('POST', '/hosts', {'remark': journal['name'], 'address': journal['address'],
                'port': 443, 'sni': journal['domain'], 'fingerprint': 'chrome', 'nodes': [journal['uuid']],
                'inbound': {'configProfileUuid': journal['profile'], 'configProfileInboundUuid': journal['inbound']}})
            journal['host_uuid'] = created['uuid']
            save(journal_path, journal)
        if 'squad' not in journal:
            squad = api.call('POST', '/internal-squads',
                {'name': 'RM_' + journal['uuid'][:8], 'inbounds': [journal['inbound']]})
            journal['squad'] = squad['uuid']
            save(journal_path, journal)
        self.state['nodes'][journal['uuid']] = {k: v for k, v in journal.items() if k != 'secret'}
        self.persist()
        for attempt in range(20):
            node = api.call('GET', '/nodes/' + journal['uuid'])
            if node.get('isConnected'):
                journal_path.unlink()
                print('Нода подключена. Созданы профиль RAW + Reality, хост и internal squad.\n'
                      'Назначьте созданную squad нужным пользователям; используется Vision.')
                return
            time.sleep(3)
        raise RuntimeError('Нода создана, но ещё offline. Проверьте маршрутизацию панель→нода:2222 и firewall хостинга. '
                           'Операция сохранена в «Незавершённые установки».')

    def pending(self):
        paths = sorted((ROOT / 'pending').glob('*.json')) if (ROOT / 'pending').exists() else []
        if not paths:
            print('Незавершённых установок нет.'); return
        choice = menu('Незавершённые установки', [(str(i), load(p)['name']) for i, p in enumerate(paths, 1)] + [('0', 'Назад')])
        if choice.isdigit() and 1 <= int(choice) <= len(paths):
            path = paths[int(choice) - 1]
            self.finish_node(load(path), path)

    def node_template(self, node):
        choice = menu('Сайт-заглушка', [('1', 'Студия'), ('2', 'Журнал'), ('3', 'Мастерская'),
                                       ('4', 'Свой HTML-файл'), ('0', 'Назад')])
        if choice == '0': return
        if choice == '4':
            path = pathlib.Path(ask('Путь к index.html на управляющем сервере'))
            if path.stat().st_size > 2 * 1024 * 1024:
                raise ValueError('Лимит HTML: 2 МБ. Используйте самодостаточный HTML.')
            job = {'action': 'template', 'html': path.read_text()}
        elif choice in ('1', '2', '3'):
            job = {'action': 'template', 'style': {'1': 'studio', '2': 'journal', '3': 'workshop'}[choice]}
        else: return
        self.connection(node).job(job)

    def node_menu(self, node):
        while True:
            node = self.api().call('GET', '/nodes/' + node['uuid'])
            stopped = node.get('isDisabled') or not node.get('isConnected')
            choice = menu(node['name'], [('1', 'Шаблон для selfsteal'), ('2', 'Ядро Xray (актуальный релиз)'),
                ('3', 'Переименовать'), ('4', 'Переустановить'), ('5', 'Обновить'),
                ('6', 'Запустить' if stopped else 'Остановить'), ('7', 'Перезагрузить Xray и контейнер'),
                ('8', 'Статус и последние логи'), ('9', 'Изменить SSH-подключение'), ('0', 'Назад')])
            if choice == '0': return
            if choice == '1': self.node_template(node)
            elif choice == '2': self.connection(node).job({'action': 'xray'})
            elif choice == '3':
                name = ask('Новое имя')
                if not 3 <= len(name) <= 30: raise ValueError('Имя: 3–30 символов.')
                self.api().call('PATCH', '/nodes', {'uuid': node['uuid'], 'name': name})
            elif choice == '4':
                stored = self.state['nodes'].get(node['uuid'], {})
                cover = domain(ask('Домен selfsteal', stored.get('domain')))
                if stored.get('domain') and cover != stored['domain']:
                    raise ValueError('При переустановке сохраните домен профиля. Для смены домена создайте новую ноду.')
                secret = self.api().call('GET', '/keygen')['secretKey']
                panel = self.state['panel']
                self.connection(node).job({'action': 'install', 'domain': cover, 'secret': secret,
                    'panel_ip': panel['ip'], 'email': panel['email']})
                stored.update({'domain': cover, 'email': panel['email'], 'panel_ip': panel['ip']})
                self.persist()
            elif choice == '5': self.connection(node).job({'action': 'update'})
            elif choice == '6':
                enabling = stopped
                transport = self.connection(node)
                if enabling:
                    transport.job({'action': 'start'})
                    self.api().call('POST', f'/nodes/{node["uuid"]}/actions/enable')
                else:
                    self.api().call('POST', f'/nodes/{node["uuid"]}/actions/disable')
                    transport.job({'action': 'stop'})
            elif choice == '7': self.connection(node).job({'action': 'restart'})
            elif choice == '8': self.connection(node).job({'action': 'status'})
            elif choice == '9':
                stored = self.state['nodes'].setdefault(node['uuid'], {})
                stored.update({'ssh_host': host(ask('SSH IP/домен', node['address'])),
                               'ssh_port': port(ask('SSH порт', '22'))})
                self.persist()

    def nodes_menu(self):
        while True:
            nodes = self.nodes()
            entries = [(str(i), f'{n["name"]} ({n["address"]}, '
                f'{"остановлена" if n.get("isDisabled") else "online" if n.get("isConnected") else "offline"})')
                for i, n in enumerate(nodes, 1)]
            add_key = str(len(nodes) + 1)
            choice = menu('Ноды и расширения', entries + [(add_key, 'Добавить ноду'),
                ('p', 'Незавершённые установки'), ('0', 'Назад')])
            if choice == '0': return
            if choice == add_key: self.node_add()
            elif choice == 'p': self.pending()
            elif choice.isdigit() and 1 <= int(choice) <= len(nodes): self.node_menu(nodes[int(choice) - 1])

    def warp_route(self, node, enabled):
        stored = self.state['nodes'].get(node['uuid'], {})
        profile_uuid = stored.get('profile')
        if not profile_uuid:
            raise RuntimeError('Изменение маршрутов разрешено только для отдельного профиля, созданного RemnaManager.')
        if node.get('configProfile', {}).get('activeConfigProfileUuid') != profile_uuid:
            raise RuntimeError('Профиль ноды изменён через панель. Автоматическое изменение маршрутов остановлено.')
        profile = self.api().call('GET', '/config-profiles/' + profile_uuid)
        if any(n['uuid'] != node['uuid'] for n in profile.get('nodes', [])):
            raise RuntimeError('Этот профиль используется несколькими нодами. Создайте отдельный профиль перед изменением WARP.')
        config = profile['config']
        domains = [domain(s.strip()) for s in ask('Домены через WARP, через запятую').split(',')] if enabled else []
        if enabled:
            self.connection(node).deploy()
            self.connection(node).command('curl --fail --silent --max-time 15 --socks5-hostname 127.0.0.1:40000 '
                                          'https://www.cloudflare.com/cdn-cgi/trace | grep -q "warp=on"')
        updated = warp_config(config, enabled, domains)
        save(ROOT / 'backups' / f'profile-{profile_uuid}-{time.time_ns()}.json', profile)
        self.api().call('PATCH', '/config-profiles', {'uuid': profile_uuid, 'config': updated})
        stored['warp'] = enabled
        self.persist()
        print('Профиль обновлён; WARP применяется к выбранным доменам.')

    def warp_menu(self):
        while True:
            choice = menu('WARP Native', [('1', 'Установить WARP Native на ноду'), ('2', 'Удалить WARP Native'),
                ('3', 'Добавить в конфигурацию ноды'), ('4', 'Удалить из конфигурации ноды'), ('0', 'Назад')])
            if choice == '0': return
            if choice not in ('1', '2', '3', '4'): continue
            node = self.select_node()
            if not node: continue
            if choice == '1':
                if confirm('Установка WARP подразумевает принятие условий Cloudflare. Продолжить?'):
                    self.connection(node).job({'action': 'warp-install'})
            elif choice == '2':
                if self.state['nodes'].get(node['uuid'], {}).get('warp'):
                    self.warp_route(node, False)
                self.connection(node).job({'action': 'warp-remove'})
            elif choice == '3': self.warp_route(node, True)
            elif choice == '4': self.warp_route(node, False)

    def checker_install(self):
        url = ask('HTTPS-ссылка подписки отдельного пользователя для мониторинга', secret=True)
        if not url.startswith('https://') or any(c.isspace() for c in url):
            raise ValueError('Нужна HTTPS-ссылка без пробелов.')
        CHECKER.mkdir(exist_ok=True, mode=0o700)
        save(CHECKER / 'compose.yaml', checker_compose())
        env_set(CHECKER / '.env', {'SUBSCRIPTION_URL': url})
        compose(CHECKER, 'pull'); compose(CHECKER, 'up', '-d', '--wait', '--wait-timeout', '120')
        wait_http('http://127.0.0.1:2112/')
        print('Xray Checker слушает 127.0.0.1:2112. Логи доступны в этом меню.')

    def telegram(self):
        token = ask('Telegram bot token', secret=True)
        chat = ask('Telegram chat ID')
        if not re.fullmatch(r'\d+:[A-Za-z0-9_-]+', token) or not re.fullmatch(r'-?\d+', chat):
            raise ValueError('Неверный token/chat ID.')
        # Telegram alerts are delivered by our own timer, not undocumented Checker variables.
        save(ROOT / 'telegram.json', {'token': token, 'chat': chat})
        app = pathlib.Path(__file__).resolve()
        if any(c.isspace() for c in str(app)):
            raise RuntimeError('Для systemd разместите проект в пути без пробелов.')
        save('/etc/systemd/system/remnamanager-monitor.service', f'''[Unit]
Description=RemnaManager node state notifications
After=docker.service network-online.target
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 {app} --notify
UMask=0077
''')
        save('/etc/systemd/system/remnamanager-monitor.timer', '''[Unit]
Description=Check Remnawave node status every minute
[Timer]
OnBootSec=2min
OnUnitActiveSec=60s
Persistent=true
[Install]
WantedBy=timers.target
''')
        run(['systemctl', 'daemon-reload'])
        run(['systemctl', 'enable', '--now', 'remnamanager-monitor.timer'])
        print('Уведомления об изменении online/offline нод настроены. Первое состояние — исходное, без сообщения.')

    def checker_menu(self):
        while True:
            installed = (CHECKER / 'compose.yaml').exists()
            entries = [('1', 'Статус и последние логи'), ('2', 'Перезапустить'), ('3', 'Обновить образы'),
                ('4', 'Настроить Telegram-бота'), ('5', 'Удалить')] if installed else [('1', 'Установка Xray Checker')]
            choice = menu('Мониторинг нод', entries + [('0', 'Назад')])
            if choice == '0': return
            if not installed:
                if choice == '1': self.checker_install()
                continue
            if choice == '1':
                compose(CHECKER, 'ps'); compose(CHECKER, 'logs', '--tail', '100', '--no-color')
            elif choice == '2': compose(CHECKER, 'restart')
            elif choice == '3':
                compose(CHECKER, 'pull'); compose(CHECKER, 'up', '-d', '--wait', '--wait-timeout', '120')
            elif choice == '4': self.telegram()
            elif choice == '5' and confirm('Удалить Xray Checker и настройки уведомлений?'):
                compose(CHECKER, 'down')
                timer = pathlib.Path('/etc/systemd/system/remnamanager-monitor.timer')
                if timer.exists():
                    run(['systemctl', 'disable', '--now', 'remnamanager-monitor.timer'])
                    run(['systemctl', 'stop', 'remnamanager-monitor.service'])
                    timer.unlink()
                    pathlib.Path('/etc/systemd/system/remnamanager-monitor.service').unlink(missing_ok=True)
                    run(['systemctl', 'daemon-reload'])
                (ROOT / 'telegram.json').unlink(missing_ok=True)
                shutil.rmtree(CHECKER)

    def plugins(self):
        while True:
            choice = menu('Плагины ноды', [('1', 'Включить BBR + fq'), ('2', 'Диагностика и логи ноды'), ('0', 'Назад')])
            if choice == '0': return
            if choice not in ('1', '2'): continue
            node = self.select_node()
            if node: self.connection(node).job({'action': 'bbr' if choice == '1' else 'status'})

    def main(self):
        while True:
            try:
                choice = menu('RemnaManager', [('1', 'Панель и компоненты'), ('2', 'Ноды и расширения'),
                    ('3', 'Страница подписки'), ('4', 'WARP Native'), ('5', 'Мониторинг нод'),
                    ('6', 'Плагины (дополнительные функции для ноды)'), ('0', 'Выход')])
                if choice == '0': return
                action = {'1': self.panel_menu, '2': self.nodes_menu, '3': self.sub_menu,
                          '4': self.warp_menu, '5': self.checker_menu, '6': self.plugins}.get(choice)
                if action: action()
            except (KeyError, ValueError, OSError, RuntimeError, subprocess.SubprocessError, urllib.error.URLError) as exc:
                print(f'\nОперация не завершена: {exc}\nНастройки сохранены. Возврат в основное меню.')
            except KeyboardInterrupt:
                print('\nОперация прервана. Возврат в основное меню.')


def notify():
    """Report panel-observed node availability transitions, retry failed notifications."""
    import urllib.parse
    import urllib.request
    config = load(ROOT / 'telegram.json')
    if not config: return
    manager = Manager()
    nodes = manager.nodes()
    previous = load(ROOT / 'monitor-state.json', {})
    current = {n['uuid']: bool(n.get('isConnected')) and not n.get('isDisabled', False) for n in nodes}
    changes = [f'{n["name"]}: {"ONLINE" if current[n["uuid"]] else "OFFLINE"}' for n in nodes
               if n['uuid'] in previous and previous[n['uuid']] != current[n['uuid']]]
    if changes:
        request = urllib.request.Request('https://api.telegram.org/bot' + config['token'] + '/sendMessage',
            data=urllib.parse.urlencode({'chat_id': config['chat'], 'text': '\n'.join(changes)}).encode())
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                if not json.load(response).get('ok'): raise RuntimeError('Telegram отклонил уведомление.')
        except Exception:
            raise RuntimeError('Не удалось отправить Telegram-уведомление. Повтор при следующей проверке.') from None
    save(ROOT / 'monitor-state.json', current)


if __name__ == '__main__':
    if '--check' in sys.argv:
        print('Импорты и конфигурация Python: OK. Компоненты Remnawave не устанавливались.')
        sys.exit(0)
    if sys.platform != 'linux' or os.geteuid() != 0:
        sys.exit('Нужен Linux и запуск от root.')
    import fcntl
    os.umask(0o077)
    ROOT.mkdir(exist_ok=True, mode=0o700)
    ROOT.chmod(0o700)
    lock_path = ROOT / ('notify.lock' if '--notify' in sys.argv else 'manager.lock')
    with lock_path.open('w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            sys.exit('Другой экземпляр RemnaManager уже работает.')
        try:
            if '--notify' in sys.argv: notify()
            else:
                platform_check()
                Manager().main()
        except (EOFError, KeyboardInterrupt):
            print('\nВыход.')
