import importlib.util
import ast
import io
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('staged_guest', pathlib.Path(__file__).with_name('staged-package-guest.py'))
guest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guest)

class VMLifecycleTest(unittest.TestCase):
    def functions(self, names, values):
        source = pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()
        tree = ast.parse(source)
        nodes = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name in names]
        self.assertEqual(len(nodes), len(names))
        exec(compile(ast.Module(body=nodes, type_ignores=[]), '<vm-lifecycle>', 'exec'), values)
        return values

    def test_snapshot_stops_owned_transport_before_vm_without_changing_other_modes(self):
        for staged, expected in [(True, ['tunnel terminate', 'tunnel wait', 'vm kill', 'vm wait']),
                                 (False, ['vm kill', 'vm wait'])]:
            with self.subTest(staged=staged):
                events = []
                def process(name):
                    return SimpleNamespace(poll=lambda: None,
                        terminate=lambda: events.append(name+' terminate'),
                        kill=lambda: events.append(name+' kill'),
                        wait=lambda timeout: events.append(name+' wait'))
                scope = self.functions(['stop_vm'], {'a': SimpleNamespace(staged_package_only=staged),
                    'tunnel': process('tunnel'), 'vm': process('vm')})
                scope['stop_vm']()
                self.assertEqual(events, expected)
                # Already-exited children must not be killed or waited again.
                scope['tunnel'].poll = lambda: 0
                scope['vm'].poll = lambda: 0
                scope['stop_vm']()
                self.assertEqual(events, expected)

    def test_vm_exit_retains_bounded_actual_stderr(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            prior = b'previous intentionally killed VM\n'
            detail = b'x'*10000 + b'Failed to bind socket: Address already in use'
            (root/'qemu.stderr').write_bytes(prior+detail)
            saved = {}
            scope = self.functions(['check_vm_running'], {'root': root,
                'a': SimpleNamespace(staged_package_only=True),
                'vm': SimpleNamespace(poll=lambda: 1), 'vm_stderr_start': len(prior),
                'save': lambda name, value: saved.update({name: value})})
            with self.assertRaisesRegex(RuntimeError, 'Address already in use'):
                scope['check_vm_running']()
            self.assertEqual(saved['staged-vm-failure.json'], {'returncode': 1, 'stderr': detail[-8192:].decode()})
            # A later short/empty exit cannot report the prior process's text.
            for detail in [b'current exit', b'']:
                (root/'qemu.stderr').write_bytes(prior+detail)
                with self.assertRaises(RuntimeError):
                    scope['check_vm_running']()
                self.assertEqual(saved['staged-vm-failure.json']['stderr'], detail.decode())
            scope['vm'].poll = lambda: None
            scope['check_vm_running']()

class DeliveryFixtureTest(unittest.TestCase):
    def test_restarted_delivery_waits_for_actual_https_readiness(self):
        process = SimpleNamespace(poll=lambda: None)
        opener = Mock()
        good = json.dumps({'id': 1426211857, 'full_name': 'fased-ai/fased-app'}).encode()
        opener.open.side_effect = [ConnectionRefusedError(), io.BytesIO(good)]
        with patch.object(guest.urllib.request, 'build_opener', return_value=opener), patch.object(guest.time, 'sleep'):
            guest.wait_delivery(process)
        self.assertEqual(opener.open.call_count, 2)
        opener.open.assert_called_with('https://api.github.com/repos/fased-ai/fased-app', timeout=1)
        with self.assertRaisesRegex(RuntimeError, 'server exited'):
            guest.wait_delivery(SimpleNamespace(poll=lambda: 1))
        with self.assertRaisesRegex(RuntimeError, 'did not become ready'):
            guest.wait_delivery(process, timeout=0)

    def test_handoff_preserves_old_setup_proof_and_rejects_ambiguous_owners(self):
        setup = 'http://127.0.0.1:17482/?instance=owner#setup=proof'
        plain = 'http://127.0.0.1:17482'
        self.assertEqual(guest.browser_link(setup + '\n' + plain), (setup, 2))
        claimed = 'http://127.0.0.1:17482/?instance=owner'
        self.assertEqual(guest.browser_link(claimed + '\n' + plain), (claimed, 2))
        self.assertEqual(guest.browser_link(claimed), (claimed, 1))
        with self.assertRaisesRegex(RuntimeError, 'ambiguous'):
            guest.browser_link(setup + '\n' + setup.replace('owner', 'other'))

    def test_terminal_deadline_retains_the_unanswered_prompt_without_secrets(self):
        command = [sys.executable, '-c', 'print("disposable-staged-owner-password #setup=private-token\\nUnanswered consent? [y/N]: ", flush=True); input()']
        with self.assertRaisesRegex(TimeoutError, r'Unanswered consent\?') as caught:
            guest.terminal(command, {}, timeout=1)
        self.assertNotIn(guest.PASSWORD, str(caught.exception))
        self.assertNotIn('private-token', str(caught.exception))

    def test_failed_owner_reply_is_bounded_private_and_redacted(self):
        with tempfile.TemporaryDirectory() as temporary:
            receipt = pathlib.Path(temporary) / 'failure.json'
            error = ('x' * 10000 + guest.PASSWORD + ' #setup=private-setup-token\nPermission denied').encode()
            result = subprocess.CompletedProcess(['ssh'], 2, b'private stdout', error)
            with self.assertRaisesRegex(RuntimeError, '(?s)fased-local/install.*Permission denied'):
                guest.checked_reply(result, 'fased-local', 'install', receipt)
            saved = guest.document(receipt)
            self.assertEqual(saved['returncode'], 2)
            self.assertLessEqual(len(saved['stderr']), 8192)
            self.assertNotIn(guest.PASSWORD, saved['stderr'])
            self.assertNotIn('private-setup-token', saved['stderr'])
            self.assertNotIn('private stdout', receipt.read_text())
            self.assertEqual(receipt.stat().st_mode & 0o777, 0o600)
            success = subprocess.CompletedProcess(['ssh'], 0, b'{"result":"PASS"}', b'')
            self.assertEqual(guest.checked_reply(success, 'fased-local', 'install', receipt), {'result': 'PASS'})

    def package(self, root, key, version, packet_version):
        path = root / key
        path.mkdir()
        packet = {'version': packet_version, 'source': 'a' * 40, 'productVersion': version,
                  'release': {'sourceTree': 'b' * 40, 'runId': '123', 'runAttempt': '2', 'tagObject': ''}}
        identities = []
        for name in guest.ASSETS[:4]:
            raw = (key + ':' + name).encode()
            (path / name).write_bytes(raw)
            identities.append({'file': name, 'size': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()})
        packet['archive'], packet['installer'], packet['release']['provenance'], packet['entry'] = identities
        (path / guest.ASSETS[4]).write_text(json.dumps(packet))
        return path

    def test_delivery_roles_and_observed_failure_are_distinct(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            paths = {key: self.package(root, key, version, format) for key, version, format in
                     [('candidate', '0.1.6', 2), ('predecessor', '0.1.4', 2), ('legacy', '0.1.5', 1)]}
            guest.write_private(root / 'fixture.json', {k: str(v) for k, v in paths.items()})
            guest.write_private(root / 'selection.json', {'release': 'predecessor'})
            server = guest.Fixture(root)
            code, body = server.response('/repos/fased-ai/fased-app/releases/latest')
            self.assertEqual((code, body['tag_name']), (200, 'v0.1.4'))
            tags = []
            for version in ['0.1.6', '0.1.4', '0.1.5']:
                code, tag = server.response('/repos/fased-ai/fased-app/git/ref/tags/v' + version)
                tags.append(tag['object']['sha'])
                code, value = server.response('/repos/fased-ai/fased-app/git/tags/' + tag['object']['sha'])
                self.assertEqual(value['tag'], 'v' + version)
            self.assertEqual(len(set(tags)), 3)
            guest.write_private(root / 'selection.json', {'release': 'candidate', 'failArchiveOnce': True})
            request = '/fased-ai/fased-app/releases/download/v0.1.6/fased-app-linux-amd64.tar.gz'
            self.assertEqual(server.response(request)[0], 503)
            self.assertTrue(guest.document(root / 'selection.json')['archiveFailureObserved'])
            self.assertEqual(server.response(request)[0], 200)
            self.assertEqual(server.response('/unconfigured')[0], 404)

    def test_modified_or_linked_payload_is_not_a_package(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = self.package(pathlib.Path(temporary), 'candidate', '0.1.6', 2)
            guest.package(path)
            archive = path / guest.ASSETS[0]
            archive.write_bytes(b'altered')
            with self.assertRaisesRegex(ValueError, 'changed'):
                guest.package(path)
            archive.unlink()
            archive.symlink_to(path / guest.ASSETS[1])
            with self.assertRaisesRegex(ValueError, 'regular'):
                guest.package(path)

if __name__ == '__main__':
    unittest.main()
