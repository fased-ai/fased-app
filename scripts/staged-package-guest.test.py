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

    def test_fresh_remote_runs_literal_entry_and_never_claims_complete_acceptance(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            calls = []
            def ssh(command, timeout):
                calls.append(command)
                if 'echo clean' in command: return 'clean'
                if 'echo undeployed' in command: return 'undeployed'
                if command == 'tailscale status --json': return json.dumps({'BackendState':'Running','Self':{'Online':True}})
                if 'systemctl show' in command: return 'fased-vps-1000'
                if 'id -u' in command: return '997'
                if 'getent passwd' in command: return 'fased-vps-1000:x:997:997::/var/empty:/usr/sbin/nologin'
                if 'passwd -S' in command: return 'fased-vps-1000 L 2026-11-01'
                if command == 'tailscale serve status --json': return json.dumps({'Web':{'fased.tail-example.ts.net:443':{'Handlers':{'/':{'Proxy':'unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock'}}}}})
                if '/usr/bin/stat' in command: return 'socket:600:fased-vps-1000'
                if 'tailscale logout' in command: return ''
                self.fail('unexpected VM command: '+command)
            def terminal(args, answers, **options):
                self.assertEqual(args[-1], 'bash <(curl -fsSL https://github.com/fased-ai/fased-app/releases/latest/download/install.sh) --remote --operator ubuntu --listen 127.0.0.1:17482')
                self.assertTrue(options['password_free'])
                self.assertNotIn('ssh', answers.values())
                self.assertNotIn(guest.PASSWORD, answers.values())
                options['observer']('https://login.tailscale.com/a/disposableactionsecret\n')
                options['observer']('https://fased.tail-example.ts.net/?instance=owner#setup=private\n')
                return 0, 'https://login.tailscale.com/a/disposableactionsecret\nRemote setup finished'
            candidate={'packet':{'source':'a'*40,'productVersion':'0.2.0','release':{'sourceTree':'b'*40,'runId':'12','runAttempt':'1'}},'assets':[]}
            health={'version':'0.2.0','capabilities':{'aiConnections':False,'financialExecution':False,'walletSigner':False}}
            response=Mock();response.__enter__=Mock(return_value=io.BytesIO(json.dumps(health).encode()));response.__exit__=Mock(return_value=False)
            opener=Mock();opener.open.return_value=response
            fixture=SimpleNamespace(binding=guest.binding,write_private=guest.write_private,redacted=guest.redacted,terminal=terminal,PREPARATION_ANSWERS=guest.PREPARATION_ANSWERS)
            saved={}
            scope=self.functions(['fresh_remote_acceptance'],{'root':root,'ssh':ssh,'sshbase':['ssh','ubuntu@127.0.0.1'],'json':json,'urllib':SimpleNamespace(request=SimpleNamespace(build_opener=lambda _:opener,ProxyHandler=lambda _:None)),'subprocess':subprocess,'save':lambda n,v:saved.update({n:v})})
            result=scope['fresh_remote_acceptance'](fixture,candidate,'/guest','/delivery','f'*64)
            self.assertEqual(result['result'],'SUPPORTING')
            self.assertFalse(result['upgradeApplied'])
            self.assertEqual(result['checks']['tailBeforeDeployment'],'PASS')
            self.assertEqual(result['localAcceptanceSha256'],'f'*64)
            opener.open.assert_called_once_with('https://fased.tail-example.ts.net/api/v1/health',timeout=20)
            self.assertIn('sudo -n /usr/bin/tailscale logout',calls)
            self.assertNotIn('private', (root/'root-entry.log').read_text())
            self.assertNotIn('https://login.tailscale.com/a/', (root/'root-entry.log').read_text())
            self.assertNotIn('disposableactionsecret', (root/'root-entry.log').read_text())
            self.assertEqual(guest.document(root/'enrollment.json')['url'],'https://login.tailscale.com/a/disposableactionsecret')
            self.assertEqual((root/'enrollment.json').stat().st_mode & 0o777,0o600)
            self.assertEqual((root/'root-entry.log').stat().st_mode & 0o777,0o600)

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
                'os': SimpleNamespace(stat=lambda path: SimpleNamespace(st_uid=0, st_gid=108, st_mode=0o660, st_ino=7),
                    geteuid=lambda: 1001, getegid=lambda: 108, getgroups=lambda: [108, 1001]),
                'vm': SimpleNamespace(poll=lambda: 1), 'vm_stderr_start': len(prior),
                'save': lambda name, value: saved.update({name: value})})
            with self.assertRaisesRegex(RuntimeError, 'Address already in use'):
                scope['check_vm_running']()
            failure = saved['staged-vm-failure.json']
            self.assertEqual(failure['returncode'], 1)
            self.assertEqual(failure['stderr'], detail[-8192:].decode())
            self.assertEqual(failure['kvmDevice'], {'uid': 0, 'gid': 108, 'mode': '0o660', 'inode': 7})
            self.assertEqual(failure['process'], {'uid': 1001, 'gid': 108, 'groups': [108, 1001]})
            # A later short/empty exit cannot report the prior process's text.
            for detail in [b'current exit', b'']:
                (root/'qemu.stderr').write_bytes(prior+detail)
                with self.assertRaises(RuntimeError):
                    scope['check_vm_running']()
                self.assertEqual(saved['staged-vm-failure.json']['stderr'], detail.decode())
            def missing_device(path):
                raise FileNotFoundError(2, 'missing device', path)
            scope['os'].stat = missing_device
            with self.assertRaisesRegex(RuntimeError, 'QEMU exited'):
                scope['check_vm_running']()
            self.assertEqual(saved['staged-vm-failure.json']['kvmDevice'], {'unavailable': True, 'errno': 2})
            self.assertEqual(saved['staged-vm-failure.json']['returncode'], 1)
            self.assertEqual(saved['staged-vm-failure.json']['stderr'], '')
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
        command = [sys.executable, '-c', 'print("disposable-staged-owner-password #setup=private-token https://login.tailscale.com/a/timeout-action-secret\\nUnanswered consent? [y/N]: ", flush=True); input()']
        with self.assertRaisesRegex(TimeoutError, r'Unanswered consent\?') as caught:
            guest.terminal(command, {}, timeout=1)
        self.assertNotIn(guest.PASSWORD, str(caught.exception))
        self.assertNotIn('private-token', str(caught.exception))
        self.assertNotIn('https://login.tailscale.com/a/', str(caught.exception))
        self.assertNotIn('timeout-action-secret', str(caught.exception))

    def test_routine_terminal_refuses_password_before_sending_any_answer(self):
        for prompt in ('Owner password: ', '[sudo] password for user: ', 'Signing password: '):
            command = [sys.executable, '-c', 'print(' + repr(prompt) + ', flush=True); input(); print("credential sent")']
            with self.assertRaisesRegex(RuntimeError, 'Unexpected password prompt'):
                guest.terminal(command, {prompt: guest.PASSWORD}, timeout=2, password_free=True)

    def test_fresh_remote_requires_matching_completed_local_journey(self):
        candidate = {'packet': {'source': 'a' * 40, 'productVersion': '0.2.0', 'release':
                     {'sourceTree': 'b' * 40, 'runId': '12', 'runAttempt': '1'}}, 'assets': []}
        receipt = {**guest.binding(candidate), 'result': 'PASS', 'localOnly': True,
                   'upgradeApplied': True, 'uiOutcome': 'applied', 'cliOutcome': 'applied',
                   'automaticOutcome': 'applied', 'automaticPolicyRestored': 'manual',
                   'mining': False, 'liveFeatures': False, 'automaticUpdates': False,
                   'checks': {name: 'PASS' for name in guest.LOCAL_REQUIRED_CHECKS}}
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / 'local.json'
            guest.write_private(path, receipt)
            self.assertEqual(guest.require_local_acceptance(candidate, path), guest.digest(path.read_bytes()))
            for key, value in [('source', 'c' * 40), ('runAttempt', '2'), ('result', 'SUPPORTING'),
                               ('upgradeApplied', False), ('automaticPolicyRestored', 'automatic'),
                               ('liveFeatures', True), ('cliOutcome', 'alreadyCurrent')]:
                guest.write_private(path, {**receipt, key: value})
                with self.subTest(key=key), self.assertRaisesRegex(RuntimeError, 'Local acceptance'):
                    guest.require_local_acceptance(candidate, path)
            guest.write_private(path, {**receipt, 'checks': {**receipt['checks'], 'recovery': 'FAIL'}})
            with self.assertRaisesRegex(RuntimeError, 'Local acceptance'):
                guest.require_local_acceptance(candidate, path)

    def test_failed_owner_reply_is_bounded_private_and_redacted(self):
        with tempfile.TemporaryDirectory() as temporary:
            receipt = pathlib.Path(temporary) / 'failure.json'
            error = ('x' * 10000 + guest.PASSWORD + ' #setup=private-setup-token https://login.tailscale.com/a/failure-action-secret\nPermission denied').encode()
            result = subprocess.CompletedProcess(['ssh'], 2, b'private stdout', error)
            with self.assertRaisesRegex(RuntimeError, '(?s)fased-local/install.*Permission denied') as caught:
                guest.checked_reply(result, 'fased-local', 'install', receipt)
            saved = guest.document(receipt)
            self.assertEqual(saved['returncode'], 2)
            self.assertLessEqual(len(saved['stderr']), 8192)
            self.assertNotIn(guest.PASSWORD, saved['stderr'])
            self.assertNotIn('private-setup-token', saved['stderr'])
            for text in (saved['stderr'],str(caught.exception)):
                self.assertNotIn('https://login.tailscale.com/a/',text)
                self.assertNotIn('failure-action-secret',text)
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
