import importlib.util
import ast
import io
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile
import urllib.parse
import unittest
from types import SimpleNamespace
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('staged_guest', pathlib.Path(__file__).with_name('staged-package-guest.py'))
guest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guest)

class RemotePreflightDriverTest(unittest.TestCase):
    prompt = 'Connect Tailscale for private Remote access? [Y/n]: '
    retry = 'Connect Tailscale and retry. Fased has not been installed.'

    def test_action_links_are_not_terminal_diagnostics(self):
        secret = 'A' * 43
        text = 'Open https://node.tail-example.ts.net/#' + secret
        self.assertNotIn(secret, guest.redacted(text))
        self.assertNotIn('https://node.tail-example.ts.net/', guest.redacted(text))

    def test_retry_is_exact_once_readiness_gated_throttled_and_finite(self):
        now = [0]
        reads = []
        ready = [False]
        def check():
            reads.append(now[0])
            return ready[0]
        retry = guest.RemoteEnrollmentRetry(check, clock=lambda: now[0])
        first = self.prompt
        second = first + 'y\n' + self.retry + '\n' + self.prompt
        self.assertIsNone(retry(first))
        self.assertEqual(reads, [])
        self.assertIsNone(retry(second))
        for _ in range(20): self.assertIsNone(retry(second))
        self.assertEqual(reads, [0])
        for moment in [5,10]:
            now[0] = moment
            self.assertIsNone(retry(second))
        now[0] = 15; ready[0] = True
        self.assertEqual(retry(second), self.prompt)
        self.assertIsNone(retry(second))
        self.assertEqual(reads, [0, 5, 10, 15])
        with self.assertRaisesRegex(RuntimeError, 'more than once'):
            retry(second + 'y\n' + self.prompt)
        pending_reads = []
        later_ready = [False]
        def pending():
            pending_reads.append(now[0])
            return later_ready[0]
        blocked = guest.RemoteEnrollmentRetry(pending, clock=lambda: now[0])
        for moment in range(20,75,5):
            now[0] = moment
            self.assertIsNone(blocked(second))
        now[0] = 75
        with self.assertRaisesRegex(RuntimeError, 'not ready; no retry'):
            blocked(second)
        later_ready[0] = True; now[0] = 100
        with self.assertRaisesRegex(RuntimeError, 'Finish enrollment before rerunning'):
            blocked(second)
        self.assertEqual(pending_reads, list(range(20,80,5)))
        now[0] = 200
        def slow_ready():
            now[0] = 261
            return True
        expired = guest.RemoteEnrollmentRetry(slow_ready, clock=lambda: now[0])
        with self.assertRaisesRegex(RuntimeError, 'not ready; no retry'):
            expired(second)
        self.assertFalse(expired.used)
        self.assertTrue(expired.pending_ended)
        unknown = guest.RemoteEnrollmentRetry(lambda: self.fail('unknown prompt inspected'))
        with self.assertRaisesRegex(RuntimeError, 'unrecognized'):
            unknown(first + '\nUnexpected failure\n' + self.prompt)

    def test_owner_handoff_does_not_consume_readiness_retry_window(self):
        now = [0]; ready = [False]; reads = []; waiting = [True]
        def check():
            reads.append(now[0]); return ready[0]
        retry = guest.RemoteEnrollmentRetry(check, clock=lambda: now[0], handoff_pending=lambda: waiting[0])
        text = self.prompt+'y\n'+self.retry+'\n'+self.prompt
        for moment in [0, 5, 60, 120, 180]:
            now[0]=moment; self.assertIsNone(retry(text))
        self.assertEqual(retry.checks, 0)
        self.assertIsNone(retry.expires_at)
        self.assertEqual(retry.waiting_checks, 5)
        now[0]=185; ready[0]=True
        self.assertEqual(retry(text), self.prompt)
        self.assertIsNone(retry(text))
        self.assertTrue(retry.used)
        self.assertEqual(reads,[0,5,60,120,180,185])
        # Once owner input ends, the original bounded readiness policy applies.
        ready[0]=False; waiting[0]=False
        bounded=guest.RemoteEnrollmentRetry(check,clock=lambda:now[0],handoff_pending=lambda:waiting[0])
        self.assertIsNone(bounded(text));now[0]+=60
        with self.assertRaisesRegex(RuntimeError,'not ready; no retry'):
            bounded(text)

    def test_tailnet_origin_requires_exact_running_online_expected_network(self):
        good = {'BackendState':'Running', 'MagicDNSSuffix':'tail-example.ts.net',
                'Self':{'Online':True, 'DNSName':'node.tail-example.ts.net.'}}
        self.assertEqual(guest.private_tailnet_origin(good, 'tail-example.ts.net'), 'https://node.tail-example.ts.net')
        for changed in [{'BackendState':'NeedsLogin'}, {'Self':{'Online':False}},
                        {'MagicDNSSuffix':'other.ts.net'},
                        {'Self':{'Online':True,'DNSName':'node.other.ts.net.'}}]:
            with self.subTest(changed=changed), self.assertRaisesRegex(RuntimeError, 'Tailscale'):
                guest.private_tailnet_origin({**good, **changed}, 'tail-example.ts.net')

    def test_terminal_preserves_default_once_and_allows_only_checked_retry(self):
        import sys
        program = "import sys; p=" + repr(self.prompt) + "; print(p,end='',flush=True); assert input()=='y'; print(" + repr(self.retry) + ",flush=True); print(p,end='',flush=True); assert input()=='y'; print('finished',flush=True)"
        with tempfile.TemporaryDirectory() as temporary:
            log = pathlib.Path(temporary)/'terminal.log'
            retry = guest.RemoteEnrollmentRetry(lambda: True)
            code, output = guest.terminal([sys.executable,'-c',program], {self.prompt:'y'}, timeout=3,
                                         prompt_retry=retry, transcript=log)
            self.assertEqual(code, 0)
            self.assertIn('finished', output)
            self.assertEqual(log.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(TimeoutError):
                guest.terminal([sys.executable,'-c',program], {self.prompt:'y'}, timeout=1, transcript=log)
            self.assertIn(self.retry, log.read_text())

class VMLifecycleTest(unittest.TestCase):
    def test_browser_workspace_resume_preserves_private_state_and_refuses_foreign_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            scope = self.functions(['staged_browser_directory'], {'os': __import__('os')})
            directory = scope['staged_browser_directory'](root, False)
            (directory/'owner-state.json').write_text('retained disposable browser state')
            self.assertEqual(scope['staged_browser_directory'](root, True), directory)
            self.assertEqual((directory/'owner-state.json').read_text(), 'retained disposable browser state')
            with self.assertRaises(FileExistsError):
                scope['staged_browser_directory'](root, False)
            directory.chmod(0o755)
            with self.assertRaisesRegex(RuntimeError, 'private owned'):
                scope['staged_browser_directory'](root, True)
            directory.chmod(0o700)
            other = root/'other';other.mkdir()
            (other/'browser').symlink_to(directory, target_is_directory=True)
            with self.assertRaisesRegex(RuntimeError, 'private owned'):
                scope['staged_browser_directory'](other, True)

    def test_remote_resume_keeps_binding_node_and_completed_phases(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = pathlib.Path(temporary) / 'remote-journey.json'
            identity = {'source': 'a'*40, 'artifactSetSha256': 'b'*64}
            tail = {'BackendState': 'Running', 'MagicDNSSuffix': 'tail-example.ts.net',
                    'Self': {'ID': 'node-one', 'Online': True, 'DNSName': 'node.tail-example.ts.net.'}}
            state = guest.RemoteJourney(file, identity, 'c'*64)
            state.enrolled(tail, 'tail-example.ts.net')
            state.advance('private-browser')
            state.advance('installed', instance_id='A'*43)
            recovered = guest.RemoteJourney(file, identity, 'c'*64)
            self.assertEqual(recovered.value['phase'], 'installed')
            self.assertEqual(recovered.value['instanceId'], 'A'*43)
            self.assertEqual(file.stat().st_mode & 0o777, 0o600)
            for changed in [dict(identity, source='d'*40), dict(identity, artifactSetSha256='d'*64)]:
                with self.assertRaisesRegex(RuntimeError, 'different package'):
                    guest.RemoteJourney(file, changed, 'c'*64)
            with self.assertRaisesRegex(RuntimeError, 'Local receipt'):
                guest.RemoteJourney(file, identity, 'd'*64)
            for changed in [{'ID':'node-two'}, {'DNSName':'other.tail-example.ts.net.'}]:
                with self.assertRaisesRegex(RuntimeError, 'identity changed'):
                    recovered.enrolled(dict(tail, Self=dict(tail['Self'], **changed)), 'tail-example.ts.net')
            with self.assertRaisesRegex(RuntimeError, 'phase'):
                recovered.advance('current')
            with self.assertRaisesRegex(RuntimeError, 'instance changed'):
                recovered.advance('installed', instance_id='B'*43)
            self.assertEqual(guest.document(file)['instanceId'], 'A'*43)
            file.chmod(0o644)
            with self.assertRaisesRegex(RuntimeError, 'private'):
                guest.RemoteJourney(file, identity, 'c'*64)

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
            service = ['fased-vps-1000']
            def ssh(command, timeout):
                calls.append(command)
                if 'echo clean' in command: return 'clean'
                if 'echo undeployed' in command: return 'undeployed'
                if command == 'fased status': return json.dumps({'installed':True,'active':True,'instanceId':'A'*43})
                if command == 'test -e /var/lib/fased-app-vps-1000 && echo present || echo absent':return 'present'
                if command == 'fased open': return 'https://fased.tail-example.ts.net/?instance='+'A'*43+'#setup='+'B'*43
                if command == 'tailscale status --json': return json.dumps({'BackendState':'Running','MagicDNSSuffix':'tail-example.ts.net','Self':{'ID':'node-one','Online':True,'DNSName':'fased.tail-example.ts.net.'}})
                if 'systemctl show' in command: return service[0]
                if 'id -u' in command:
                    self.assertEqual(command, 'id -u fased-vps-1000')
                    return '997'
                if 'getent passwd' in command: return 'fased-vps-1000:x:997:997::/var/empty:/usr/sbin/nologin'
                if 'passwd -S' in command:
                    self.assertEqual(command, 'sudo -n /usr/bin/passwd -S fased-vps-1000')
                    return 'fased-vps-1000 L 2026-11-01'
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
                repeated = guest.RemoteEnrollmentRetry.prompt + '\ny\n' + guest.RemoteEnrollmentRetry.failure + '\n' + guest.RemoteEnrollmentRetry.prompt
                self.assertEqual(options['prompt_retry'](repeated), guest.RemoteEnrollmentRetry.prompt)
                self.assertIsNone(options['prompt_retry'](repeated))
                options['observer']('https://fased.tail-example.ts.net/#' + 'A'*43 + '\n')
                options['observer']('https://fased.tail-example.ts.net/?instance=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#setup=private\n')
                options['transcript'].write_text(guest.redacted('https://login.tailscale.com/a/disposableactionsecret\nRemote setup finished'))
                options['transcript'].chmod(0o600)
                return 0, 'https://login.tailscale.com/a/disposableactionsecret\nRemote setup finished'
            def subprocess_run(command, **options):
                if command == ['tailscale','status','--json']:
                    return subprocess.CompletedProcess(command,0,json.dumps({'BackendState':'Running','MagicDNSSuffix':'tail-example.ts.net','Self':{'Online':True,'DNSName':'host.tail-example.ts.net.'}}).encode(),b'')
                self.assertEqual(command[:2],['node','scripts/staged-package-browser.mjs'])
                config=guest.document(command[2])
                self.assertEqual(config,{'fixtureOnly':True,'action':'private-preflight','url':'https://fased.tail-example.ts.net/#'+'A'*43,'expectedOrigin':'https://fased.tail-example.ts.net'})
                self.assertEqual(pathlib.Path(command[2]).stat().st_mode & 0o777,0o600)
                return subprocess.CompletedProcess(command,0,json.dumps({'result':'PASS','action':'private-preflight','privateBrowserApproved':True}).encode(),b'')
            candidate={'packet':{'source':'a'*40,'productVersion':'0.2.0','release':{'sourceTree':'b'*40,'runId':'12','runAttempt':'1'}},'assets':[]}
            health={'version':'0.2.0','capabilities':{'aiConnections':False,'financialExecution':False,'walletSigner':False}}
            response=Mock();response.__enter__=Mock(return_value=io.BytesIO(json.dumps(health).encode()));response.__exit__=Mock(return_value=False)
            opener=Mock();opener.open.return_value=response
            fixture=SimpleNamespace(RemoteJourney=guest.RemoteJourney,browser_link=guest.browser_link,binding=guest.binding,write_private=guest.write_private,redacted=guest.redacted,terminal=terminal,PREPARATION_ANSWERS=guest.PREPARATION_ANSWERS,RemoteEnrollmentRetry=guest.RemoteEnrollmentRetry,private_tailnet_origin=guest.private_tailnet_origin)
            saved={}
            scope=self.functions(['fresh_remote_acceptance'],{'a':SimpleNamespace(resume=False),'root':root,'ssh':ssh,'sshbase':['ssh','ubuntu@127.0.0.1'],'json':json,'urllib':SimpleNamespace(parse=urllib.parse,request=SimpleNamespace(build_opener=lambda _:opener,ProxyHandler=lambda _:None)),'subprocess':SimpleNamespace(run=subprocess_run,PIPE=subprocess.PIPE,SubprocessError=subprocess.SubprocessError),'save':lambda n,v:saved.update({n:v})})
            result=scope['fresh_remote_acceptance'](fixture,candidate,'/guest','/delivery','f'*64)
            self.assertEqual(result['result'],'SUPPORTING')
            self.assertFalse(result['upgradeApplied'])
            self.assertEqual(result['checks']['tailBeforeDeployment'],'PASS')
            self.assertEqual(result['checks']['predeploymentBrowser'],'PASS')
            self.assertEqual(result['localAcceptanceSha256'],'f'*64)
            self.assertEqual(result['enrollmentRetry'],{'readinessChecks':1,'resendSent':True,'outcome':'sent','ownerWaitChecks':1})
            opener.open.assert_called_once_with('https://fased.tail-example.ts.net/api/v1/health',timeout=20)
            self.assertNotIn('sudo -n /usr/bin/tailscale logout',calls)
            self.assertEqual(guest.document(root/'remote-journey.json')['phase'], 'installed')
            self.assertNotIn('private', (root/'root-entry.log').read_text())
            self.assertNotIn('https://login.tailscale.com/a/', (root/'root-entry.log').read_text())
            self.assertNotIn('disposableactionsecret', (root/'root-entry.log').read_text())
            self.assertEqual(guest.document(root/'enrollment.json')['url'],'https://login.tailscale.com/a/disposableactionsecret')
            self.assertEqual((root/'enrollment.json').stat().st_mode & 0o777,0o600)
            self.assertEqual((root/'root-entry.log').stat().st_mode & 0o777,0o600)
            interrupted = guest.document(root/'remote-journey.json')
            interrupted['phase'] = 'private-browser'
            guest.write_private(root/'remote-journey.json',interrupted)
            response.__enter__.return_value = io.BytesIO(json.dumps(health).encode())
            scope['a'].resume = True
            service[0] = '997'  # systemd may expose a numeric User= identity.
            fixture.terminal = lambda *args, **kw: self.fail('resume reran installation')
            resumed = scope['fresh_remote_acceptance'](fixture,candidate,'/guest','/delivery','f'*64)
            self.assertEqual(resumed['instanceId'], result['instanceId'])
            self.assertEqual(resumed['nodeIdentitySha256'], result['nodeIdentitySha256'])
            self.assertNotIn('sudo -n /usr/bin/tailscale logout', calls)

    def test_fresh_remote_pending_login_ends_bounded_and_retains_test_node(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary); calls = []; now = [0]; saved = {}
            tail = {'BackendState':'Running','MagicDNSSuffix':'tail-example.ts.net',
                    'Self':{'Online':True,'DNSName':'host.tail-example.ts.net.'}}
            def ssh(command, timeout):
                calls.append(command)
                if 'echo clean' in command:return 'clean'
                if 'echo undeployed' in command:return 'undeployed'
                if command=='tailscale status --json':return json.dumps({'BackendState':'NeedsLogin'})
                if 'tailscale logout' in command:return ''
                self.fail('unexpected command while enrollment pending')
            def terminal(args, answers, **options):
                repeated = guest.RemoteEnrollmentRetry.prompt + '\ny\n' + guest.RemoteEnrollmentRetry.failure + '\n' + guest.RemoteEnrollmentRetry.prompt
                try:
                    for moment in range(0,60,5):
                        now[0]=moment
                        self.assertIsNone(options['prompt_retry'](repeated))
                finally:
                    options['transcript'].write_text(repeated);options['transcript'].chmod(0o600)
            fixture=SimpleNamespace(RemoteJourney=guest.RemoteJourney,binding=lambda _: {},terminal=terminal,PREPARATION_ANSWERS={},
                private_tailnet_origin=guest.private_tailnet_origin,
                RemoteEnrollmentRetry=lambda ready,**kw:guest.RemoteEnrollmentRetry(ready,clock=lambda:now[0],**kw))
            process=SimpleNamespace(run=lambda *args,**kw:subprocess.CompletedProcess(args,0,json.dumps(tail).encode(),b''),
                PIPE=subprocess.PIPE,SubprocessError=subprocess.SubprocessError)
            scope=self.functions(['fresh_remote_acceptance'],{'a':SimpleNamespace(resume=False),'root':root,'ssh':ssh,'sshbase':['ssh','ubuntu@127.0.0.1'],
                'json':json,'subprocess':process,'save':lambda n,v:saved.update({n:v})})
            with self.assertRaisesRegex(RuntimeError,'Finish enrollment before rerunning'):
                scope['fresh_remote_acceptance'](fixture,{},'/guest','/delivery','f'*64)
            receipt=saved['fresh-remote-setup.json']
            self.assertEqual(receipt['result'],'FAIL')
            self.assertEqual(receipt['enrollmentRetry'],{'readinessChecks':12,'resendSent':False,'outcome':'pending-ended','ownerWaitChecks':0})
            self.assertEqual(calls.count('tailscale status --json'),12)
            self.assertNotIn('sudo -n /usr/bin/tailscale logout',calls)
            self.assertEqual(guest.document(root/'remote-journey.json')['phase'], 'new')

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

class ProviderBundleProducerTest(unittest.TestCase):
    def test_exact_bundle_extraction_and_preserved_attempt(self):
        import tarfile
        with tempfile.TemporaryDirectory() as d:
            root=pathlib.Path(d); engine=b"disposable-contract-engine"; manifest=json.dumps({"files":{"fased-app":{"sha256":guest.digest(engine),"bytes":len(engine)}}}).encode()
            with tarfile.open(root/guest.ASSETS[0],"w:gz") as archive:
                for name,raw in [("manifest.json",manifest),("fased-app",engine)]:
                    entry=tarfile.TarInfo(name);entry.size=len(raw);archive.addfile(entry,io.BytesIO(raw))
            candidate={"directory":str(root),"packet":{"bundleSha256":guest.digest(manifest)}}
            expected,raw=guest.verified_provider_bundle(candidate,root/"owned")
            self.assertEqual(expected,guest.digest(engine));self.assertEqual(raw,manifest)
            self.assertEqual((root/"owned/fased-app").read_bytes(),engine)
            with self.assertRaises(FileExistsError):guest.verified_provider_bundle(candidate,root/"owned")
            with self.assertRaisesRegex(RuntimeError,"manifest differs"):guest.verified_provider_bundle({**candidate,"packet":{"bundleSha256":"0"*64}},root/"wrong")

    def test_link_member_never_becomes_an_installed_engine(self):
        import tarfile
        with tempfile.TemporaryDirectory() as d:
            root=pathlib.Path(d)
            with tarfile.open(root/guest.ASSETS[0],"w:gz") as archive:
                entry=tarfile.TarInfo("manifest.json");entry.type=tarfile.SYMTYPE;entry.linkname="/etc/passwd";archive.addfile(entry)
            with self.assertRaisesRegex(RuntimeError,"bounded original"):
                guest.verified_provider_bundle({"directory":str(root),"packet":{"bundleSha256":"0"*64}},root/"owned")

if __name__ == '__main__':
    unittest.main()
