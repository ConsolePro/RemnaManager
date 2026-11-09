import copy
import base64
import hashlib
import io
import json
import pathlib
import re
import subprocess
import tempfile
import tarfile
import unittest
import urllib.error
from unittest.mock import Mock, patch

import remnamanager as app
from lib import provision, render, runtime, ssh


class ValidationTests(unittest.TestCase):
    def test_domain_rejects_config_and_shell_injection(self):
        for value in ('example.org;reboot', '-oProxyCommand=id', 'a.org\n}', 'https://example.org',
                      '*.example.org', 'a.org/path', 'a..org', 'a.-bad.org', '127.0.0.1'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                provision.domain(value)

    def test_domain_normalizes_case(self):
        self.assertEqual(provision.domain('  Node.Example.org '), 'node.example.org')

    def test_ssh_host_rejects_options_and_users(self):
        for value in ('-oProxyCommand=id', 'root@host', 'host;id', 'a\nwhoami', 'a b', '/tmp/a'):
            with self.subTest(value=value), self.assertRaises(ValueError): ssh.host(value)
        self.assertEqual(ssh.host('2001:db8::1'), '2001:db8::1')

    def test_ports(self):
        self.assertEqual(provision.port('65535'), 65535)
        for value in ('0', '-1', '65536', '22;id'):
            with self.subTest(value=value), self.assertRaises(ValueError): provision.port(value)


class ConfigTests(unittest.TestCase):
    def test_panel_gate_has_static_fallback_and_secure_cookie(self):
        config = render.tls_site('panel.example.org', upstream=3000, gate='a' * 64)
        self.assertIn('Secure; HttpOnly; SameSite=Strict', config)
        self.assertIn('location @panel', config)
        self.assertIn('error_page 418 = @panel', config)
        self.assertIn('try_files $uri $uri/ =404', config)
        self.assertIn('access_log off', config)
        self.assertIn('proxy_set_header X-Forwarded-For $remote_addr', config)
        self.assertNotIn('$proxy_add_x_forwarded_for', config)
        self.assertNotIn('$arg_', config)

    def test_node_tls_never_binds_public_443(self):
        config = render.tls_site('node.example.org', port=8443)
        self.assertIn('listen 127.0.0.1:8443 ssl http2', config)
        self.assertNotIn('listen 443', config)
        self.assertNotIn('listen [::]', config)

    def test_http01_is_a_webroot_location(self):
        config = render.http_site('node.example.org')
        self.assertIn('location ^~ /.well-known/acme-challenge/', config)
        self.assertIn('listen 80;', config)
        self.assertNotIn('return 301', config)

    def test_reality_targets_own_tls_site(self):
        config = render.reality_config('node.example.org', 'private', '0123456789abcdef')
        inbound = config['inbounds'][0]
        self.assertEqual(inbound['port'], 443)
        self.assertEqual(inbound['settings']['clients'], [])
        self.assertEqual(inbound['streamSettings']['network'], 'raw')
        self.assertEqual(inbound['streamSettings']['security'], 'reality')
        reality = inbound['streamSettings']['realitySettings']
        self.assertEqual(reality['target'], '127.0.0.1:8443')
        self.assertEqual(reality['serverNames'], ['node.example.org'])
        self.assertEqual(reality['xver'], 0)

    def test_container_ports_are_loopback_only(self):
        for config in (render.subscription_compose(), render.checker_compose()):
            for service in config['services'].values():
                self.assertTrue(all(p.startswith('127.0.0.1:') for p in service['ports']))

    def test_node_secret_is_json_data_and_not_a_shell_expression(self):
        secret = 'abc"$(id)\nxyz'
        config = json.loads(json.dumps(render.node_compose(secret)))
        self.assertEqual(config['services']['remnanode']['environment']['SECRET_KEY'], secret)
        self.assertEqual(config['services']['remnanode']['network_mode'], 'host')

    def test_warp_edit_preserves_unrelated_routes_and_is_idempotent(self):
        config = render.reality_config('node.example.org', 'key', 'short')
        config['outbounds'].append({'tag': 'CUSTOM', 'protocol': 'freedom'})
        original = copy.deepcopy(config)
        enabled = render.warp_config(config, True, ['example.com'])
        self.assertEqual(config, original)
        self.assertEqual(render.warp_config(enabled, True, ['example.com']), enabled)
        self.assertEqual(enabled['routing']['rules'][0]['domain'], ['domain:example.com'])
        self.assertEqual(render.warp_config(enabled, False), original)

    def test_templates_are_self_contained(self):
        for style in ('studio', 'journal', 'workshop'):
            html = render.site_html(style)
            self.assertIn('<!doctype html>', html)
            self.assertNotIn('http://', html)
            self.assertNotIn('https://', html)


class RuntimeTests(unittest.TestCase):
    def test_http_readiness_retries_server_errors(self):
        error = urllib.error.HTTPError('http://localhost', 503, 'starting', {}, None)
        response = Mock(status=200)
        response.__enter__ = Mock(return_value=response)
        response.__exit__ = Mock(return_value=False)
        with patch.object(runtime.urllib.request, 'urlopen', side_effect=[error, response]) as request, \
             patch.object(runtime.time, 'sleep'):
            runtime.wait_http('http://localhost', attempts=2)
        self.assertEqual(request.call_count, 2)

    def test_http_readiness_fails_if_process_never_starts(self):
        with patch.object(runtime.urllib.request, 'urlopen', side_effect=urllib.error.URLError('refused')), \
             patch.object(runtime.time, 'sleep'):
            with self.assertRaises(RuntimeError): runtime.wait_http('http://localhost', attempts=2)

    def test_state_is_json_never_executed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'state.json'
            data = {'secret': '$(touch /tmp/test)\n"hello"'}
            runtime.save(path, data)
            self.assertEqual(runtime.load(path), data)
            self.assertEqual(list(path.parent.iterdir()), [path])

    def test_env_updates_are_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / '.env'
            runtime.save(path, '# comment\nFOO=old\nBAR=keep\n')
            runtime.env_set(path, {'FOO': 'new', 'SECRET': 'abc'})
            runtime.env_set(path, {'FOO': 'new', 'SECRET': 'abc'})
            self.assertEqual(path.read_text(), '# comment\nFOO=new\nBAR=keep\nSECRET=abc\n')

    def test_api_error_does_not_echo_secrets(self):
        error = urllib.error.HTTPError('http://localhost', 400, 'bad', {}, io.BytesIO(b'private-secret'))
        with patch('urllib.request.urlopen', side_effect=error):
            with self.assertRaises(RuntimeError) as result:
                runtime.API('private-token').call('POST', '/nodes', {'secret': 'private-secret'})
        self.assertNotIn('private-secret', str(result.exception))
        self.assertNotIn('private-token', str(result.exception))

    def test_nginx_failed_validation_restores_previous_config(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'site.conf'
            runtime.save(path, 'working')
            with patch.object(provision, 'nginx_reload', side_effect=[RuntimeError('invalid'), None]):
                with self.assertRaises(RuntimeError): provision.nginx_write(path, 'broken')
            self.assertEqual(path.read_text(), 'working')

    def test_nginx_failed_new_config_is_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'site.conf'
            with patch.object(provision, 'nginx_reload', side_effect=[RuntimeError('invalid'), None]):
                with self.assertRaises(RuntimeError): provision.nginx_write(path, 'broken')
            self.assertFalse(path.exists())

    def test_firewall_only_restricts_node_api_and_retains_other_tables(self):
        files = {}
        with patch.object(provision, 'save', side_effect=lambda p, v: files.update({str(p): v})), \
             patch.object(provision, 'run'), patch.object(pathlib.Path, 'chmod'):
            provision.firewall('192.0.2.1')
        rules = files[str(runtime.ROOT / 'node-firewall.nft')]
        self.assertIn('tcp dport 2222 ip saddr 192.0.2.1 accept', rules)
        self.assertIn('tcp dport 2222 drop', rules)
        self.assertNotIn('flush ruleset', ''.join(files.values()))
        self.assertNotIn('dport 22 ', rules)

    def test_firewall_rejects_untrusted_ip_before_writing(self):
        with patch.object(provision, 'save') as save:
            with self.assertRaises(ValueError): provision.firewall('0.0.0.0; flush ruleset')
            save.assert_not_called()


class SSHTests(unittest.TestCase):
    def test_host_key_change_requires_exact_fingerprint(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(ssh, 'ROOT', pathlib.Path(directory)):
            connection = ssh.SSH({'ssh_host': '192.0.2.2', 'ssh_port': 22})
            runtime.save(connection.known, 'old-host-key\n')
            scan = Mock(stdout='192.0.2.2 ssh-ed25519 new-host-key\n')
            with patch.object(ssh.subprocess, 'run', return_value=scan), \
                 patch.object(ssh, 'output', return_value='256 SHA256:test test (ED25519)'), \
                 patch('builtins.input', return_value='yes'):
                with self.assertRaises(RuntimeError): connection.trust()
            self.assertEqual(connection.known.read_text(), 'old-host-key\n')
            with patch.object(ssh.subprocess, 'run', return_value=scan), \
                 patch.object(ssh, 'output', return_value='256 SHA256:test test (ED25519)'), \
                 patch('builtins.input', return_value='SHA256:test'):
                connection.trust()
            self.assertIn('new-host-key', connection.known.read_text())

    def test_ssh_never_disables_host_verification(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(ssh, 'ROOT', pathlib.Path(directory)):
            connection = ssh.SSH({'ssh_host': '192.0.2.2'})
            self.assertIn('StrictHostKeyChecking=yes', connection.options())
            self.assertIn('IdentitiesOnly=yes', connection.options())
            self.assertNotIn('StrictHostKeyChecking=no', connection.options())


class WorkflowTests(unittest.TestCase):
    def test_complete_node_creation_uses_api_and_persists_owned_profile(self):
        with tempfile.TemporaryDirectory() as directory:
            journal = {'name': 'NODA1', 'address': '192.0.2.2', 'ssh_host': '192.0.2.2', 'ssh_port': 22,
                'domain': 'node.example.org', 'email': 'a@example.org', 'panel_ip': '192.0.2.1'}
            path = pathlib.Path(directory) / 'pending.json'
            runtime.save(path, journal)
            manager = app.Manager.__new__(app.Manager)
            manager.state = {'nodes': {}}
            api = Mock()
            api.call.side_effect = [
                {'secretKey': 'node-secret'}, {'uuid': 'profile'},
                {'inbounds': [{'uuid': 'inbound', 'tag': 'VLESS_REALITY'}]},
                {'uuid': 'node'}, {'uuid': 'host'}, {'uuid': 'squad'}, {'isConnected': True}]
            with patch.object(manager, 'api', return_value=api), patch.object(manager, 'persist'), \
                 patch.object(app, 'SSH'), patch.object(app.subprocess, 'run', return_value=Mock(stdout=b'x' * 48)):
                manager.finish_node(journal, path)
            calls = api.call.call_args_list
            self.assertEqual(calls[1].args[:2], ('POST', '/config-profiles'))
            self.assertEqual(calls[1].args[2]['config']['inbounds'][0]['streamSettings']['network'], 'raw')
            self.assertEqual(calls[3].args[2]['configProfile']['activeInbounds'], ['inbound'])
            self.assertEqual(calls[4].args[2]['nodes'], ['node'])
            self.assertEqual(calls[4].args[2]['sni'], 'node.example.org')
            self.assertEqual(manager.state['nodes']['node']['profile'], 'profile')
            self.assertFalse(path.exists())

    def test_resume_does_not_duplicate_created_api_objects(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            journal = {'name': 'NODA1', 'address': '192.0.2.2', 'ssh_host': '192.0.2.2', 'ssh_port': 22,
                'domain': 'node.example.org', 'email': 'a@example.org', 'panel_ip': '192.0.2.1',
                'secret': 'secret', 'profile': 'profile-uuid', 'inbound': 'inbound-uuid',
                'uuid': 'node-uuid', 'host_uuid': 'host-uuid', 'squad': 'squad-uuid'}
            path = root / 'pending.json'
            runtime.save(path, journal)
            manager = app.Manager.__new__(app.Manager)
            manager.state = {'nodes': {}}
            fake_api = Mock()
            fake_api.call.return_value = {'isConnected': True}
            with patch.object(manager, 'api', return_value=fake_api), patch.object(manager, 'persist'), \
                 patch.object(app, 'SSH') as transport:
                manager.finish_node(journal, path)
            fake_api.call.assert_called_once_with('GET', '/nodes/node-uuid')
            self.assertFalse(path.exists())
            self.assertNotIn('secret', manager.state['nodes']['node-uuid'])
            transport.return_value.job.assert_called_once()

    def test_offline_node_retains_resume_journal(self):
        with tempfile.TemporaryDirectory() as directory:
            journal = {'name': 'NODA1', 'address': '192.0.2.2', 'ssh_host': '192.0.2.2',
                'domain': 'node.example.org', 'email': 'a@example.org', 'panel_ip': '192.0.2.1',
                'secret': 'secret', 'profile': 'p', 'inbound': 'i', 'uuid': 'n', 'host_uuid': 'h', 'squad': 's'}
            path = pathlib.Path(directory) / 'pending.json'
            runtime.save(path, journal)
            manager = app.Manager.__new__(app.Manager)
            manager.state = {'nodes': {}}
            fake_api = Mock()
            fake_api.call.return_value = {'isConnected': False}
            with patch.object(manager, 'api', return_value=fake_api), patch.object(manager, 'persist'), \
                 patch.object(app, 'SSH'), patch.object(app.time, 'sleep'):
                with self.assertRaises(RuntimeError): manager.finish_node(journal, path)
            self.assertTrue(path.exists())
            self.assertTrue(all(call.args[0] == 'GET' for call in fake_api.call.call_args_list))

    def test_warp_refuses_profile_reassigned_outside_manager(self):
        manager = app.Manager.__new__(app.Manager)
        manager.state = {'nodes': {'n': {'profile': 'owned-profile'}}}
        with self.assertRaises(RuntimeError):
            manager.warp_route({'uuid': 'n', 'configProfile': {'activeConfigProfileUuid': 'other-profile'}}, True)


class DistributionTests(unittest.TestCase):
    def test_standalone_archive_matches_sources_and_checksum(self):
        import build
        root = pathlib.Path(__file__).resolve().parent.parent
        script = (root / 'dist/remnamanager.sh').read_text(encoding='utf-8')
        encoded = script.split("<<'REMNAMANAGER_PAYLOAD'\n", 1)[1].split('\nREMNAMANAGER_PAYLOAD', 1)[0]
        payload = base64.b64decode(encoded)
        expected = re.search(r'hexdigest\(\) != "([a-f0-9]{64})"', script).group(1)
        self.assertEqual(hashlib.sha256(payload).hexdigest(), expected)
        with tarfile.open(fileobj=io.BytesIO(payload), mode='r:gz') as archive:
            self.assertEqual(sorted(archive.getnames()), sorted(build.FILES))
            for name in build.FILES:
                self.assertEqual(archive.extractfile(name).read(), (root / name).read_bytes())


if __name__ == '__main__':
    unittest.main()
