"""Local state, subprocesses and panel API. Never evaluate stored configuration."""
import json
import os
import pathlib
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


ROOT = pathlib.Path('/opt/remnamanager')


def run(args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def output(args, **kwargs):
    return run(args, stdout=subprocess.PIPE, text=True, **kwargs).stdout.strip()


def save(path, value):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as stream:
            stream.write(value if isinstance(value, str) else json.dumps(value, ensure_ascii=False, indent=2))
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def load(path, default=None):
    path = pathlib.Path(path)
    return json.loads(path.read_text()) if path.exists() else default


def compose(directory, *args, capture=False):
    command = ['docker', 'compose', '--project-directory', directory, *args]
    return output(command) if capture else run(command)


def env_set(path, values):
    path = pathlib.Path(path)
    lines = path.read_text().splitlines() if path.exists() else []
    remaining = dict(values)
    result = []
    for line in lines:
        key = line.split('=', 1)[0]
        if key in values:
            result.append(f'{key}={values[key]}')
            remaining.pop(key, None)
        else:
            result.append(line)
    result.extend(f'{key}={value}' for key, value in remaining.items())
    save(path, '\n'.join(result) + '\n')


def wait_http(url, attempts=30):
    for attempt in range(attempts):
        try:
            with urllib.request.urlopen(url, timeout=3) as response:
                if response.status < 500:
                    return
        except urllib.error.HTTPError as exc:
            status = exc.code
            exc.close()
            if status in (400, 401, 403, 404):
                return  # The application is serving requests, even without a user URL.
        except (urllib.error.URLError, TimeoutError, OSError):
            pass
        time.sleep(2)
    raise RuntimeError('Приложение не отвечает на локальном HTTP-порту. Проверьте последние логи.')


class API:
    def __init__(self, token='', base='http://127.0.0.1:3000/api'):
        self.token, self.base = token, base

    def call(self, method, path, body=None):
        headers = {'Content-Type': 'application/json', 'X-Forwarded-Proto': 'https', 'X-Forwarded-For': '127.0.0.1'}
        if self.token:
            headers['Authorization'] = 'Bearer ' + self.token
        request = urllib.request.Request(self.base + path,
            data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=40) as response:
                data = response.read()
        except urllib.error.HTTPError as exc:
            # Do not print API payloads: they may echo credentials or private keys.
            status = exc.code
            exc.close()
            raise RuntimeError(f'API {method} {path}: HTTP {status}. Проверьте версию панели и API-токен.') from None
        if not data:
            return None
        decoded = json.loads(data)
        if 'response' not in decoded:
            raise RuntimeError(f'API {method} {path}: неожиданный формат ответа.')
        return decoded['response']
