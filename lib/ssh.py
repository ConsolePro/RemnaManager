"""OpenSSH transport: pinned host keys, one per-node identity, interactive password fallback."""
import hashlib
import io
import json
import pathlib
import re
import shlex
import subprocess
import tarfile
import tempfile

from .runtime import ROOT, output, run, save
from .provision import port


SOURCE = pathlib.Path(__file__).resolve().parent.parent


def host(value):
    # SSH target is a host only, never a URI, user@host or option.
    if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9.:-]{0,252}', value):
        raise ValueError('Неверный SSH-адрес.')
    return value


class SSH:
    def __init__(self, node):
        self.host = host(node['ssh_host'])
        self.port = port(node.get('ssh_port', 22))
        # Stable identity follows SSH endpoint, even before panel UUID exists.
        identity = hashlib.sha256(f'{self.host}:{self.port}'.encode()).hexdigest()[:24]
        self.directory = ROOT / 'ssh' / identity
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.key = self.directory / 'id_ed25519'
        self.known = self.directory / 'known_hosts'

    def options(self, batch=True):
        return ['-p', str(self.port), '-i', str(self.key),
            '-o', f'UserKnownHostsFile={self.known}', '-o', 'GlobalKnownHostsFile=/dev/null',
            '-o', 'StrictHostKeyChecking=yes', '-o', 'IdentitiesOnly=yes', '-o', 'ConnectTimeout=10',
            '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=3',
            '-o', 'ControlMaster=no', '-o', f'BatchMode={"yes" if batch else "no"}']

    def trust(self):
        scan = subprocess.run(['ssh-keyscan', '-T', '10', '-p', str(self.port), '-t', 'ed25519', self.host],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, check=True).stdout
        lines = [line for line in scan.splitlines() if line and not line.startswith('#')]
        if not lines:
            raise RuntimeError('Сервер не предоставил SSH Ed25519 host key.')
        candidate = lines[0] + '\n'
        if self.known.exists() and self.known.read_text() == candidate:
            return
        with tempfile.NamedTemporaryFile(mode='w', delete=False) as stream:
            stream.write(candidate)
            temporary = pathlib.Path(stream.name)
        try:
            fingerprint = output(['ssh-keygen', '-lf', temporary, '-E', 'sha256']).split()[1]
        finally:
            temporary.unlink()
        print(f'\nSSH {self.host}:{self.port}: {fingerprint}')
        if self.known.exists():
            print('HOST KEY ИЗМЕНИЛСЯ. Сверьте отпечаток через консоль хостинга; возможна подмена сервера.')
        else:
            print('Первое подключение. Сверьте отпечаток через консоль хостинга.')
        if input('Введите полный SHA256:... отпечаток для доверия: ').strip() != fingerprint:
            raise RuntimeError('Отпечаток не подтверждён. Подключение отменено.')
        save(self.known, candidate)

    def connect(self):
        self.trust()
        if not self.key.exists():
            run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'remnamanager', '-f', self.key])
        args = ['ssh', *self.options(), f'root@{self.host}', 'true']
        if subprocess.run(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            return
        print('Ключ не принят. Введите актуальный root-пароль в запросе OpenSSH. Пароль не сохраняется.')
        public_key = self.key.with_suffix('.pub').read_text().strip()
        # Native SSH uses /dev/tty for password; the command has no password data.
        command = ('umask 077; mkdir -p /root/.ssh; touch /root/.ssh/authorized_keys; '
            'chmod 700 /root/.ssh; chmod 600 /root/.ssh/authorized_keys; '
            f'grep -qxF {shlex.quote(public_key)} /root/.ssh/authorized_keys || '
            f'printf "%s\\n" {shlex.quote(public_key)} >> /root/.ssh/authorized_keys')
        run(['ssh', *self.options(batch=False), '-o', 'PubkeyAuthentication=no', f'root@{self.host}', command])
        run(args)

    def command(self, command, **kwargs):
        return run(['ssh', *self.options(), f'root@{self.host}', command], **kwargs)

    def deploy(self):
        self.connect()
        self.command('command -v python3 >/dev/null || (apt-get update && apt-get install -y python3)')
        payload = io.BytesIO()
        with tarfile.open(fileobj=payload, mode='w:gz') as bundle:
            for name in ('agent.py', 'lib/__init__.py', 'lib/runtime.py', 'lib/render.py', 'lib/provision.py'):
                bundle.add(SOURCE / name, arcname=name)
        self.command('umask 077; mkdir -p /opt/remnamanager-agent; '
                     'tar xzf - -C /opt/remnamanager-agent', input=payload.getvalue())

    def job(self, job, deploy=True):
        if deploy:
            self.deploy()
        self.command('python3 /opt/remnamanager-agent/agent.py', input=json.dumps(job).encode())
