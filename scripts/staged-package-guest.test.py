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

    def test_owner_approval_reuses_pending_request_after_cli_timeout(self):
        pending={'BackendState':'NeedsLogin','AuthURL':'https://login.tailscale.com/a/existing'}
        running={'BackendState':'Running'}
        begin=Mock(); announce=Mock(); now=[0]
        states=iter([pending,pending,running])
        result=guest.await_tailnet_enrollment(lambda:next(states),begin,announce,clock=lambda:now[0],pause=lambda seconds:now.__setitem__(0,now[0]+seconds))
        self.assertEqual(result,running); begin.assert_not_called()
        self.assertEqual([c.args[0] for c in announce.call_args_list],[pending['AuthURL'],pending['AuthURL']])

    def test_owner_approval_starts_only_once_and_never_retries_expired_input(self):
        begin=Mock(); now=[0]; states=iter([{'BackendState':'NeedsLogin'},{'BackendState':'NeedsLogin','AuthURL':'https://login.tailscale.com/a/new'}])
        last=[None]
        def status():
            last[0]=next(states,last[0]);return last[0]
        with self.assertRaisesRegex(RuntimeError,'SAME node/request'):
            guest.await_tailnet_enrollment(status,begin,Mock(),clock=lambda:now[0],pause=lambda seconds:now.__setitem__(0,now[0]+seconds))
        begin.assert_called_once(); self.assertEqual(now[0],1200)
        stopped_begin=Mock()
        with self.assertRaisesRegex(RuntimeError,'enrollment stopped'):
            guest.await_tailnet_enrollment(lambda:{'BackendState':'Stopped'},stopped_begin,Mock())
        stopped_begin.assert_not_called()
        failed_begin=Mock()
        with self.assertRaisesRegex(RuntimeError,'no pending request'):
            guest.await_tailnet_enrollment(lambda:{'BackendState':'NeedsLogin'},failed_begin,Mock())
        failed_begin.assert_called_once()

    def test_tls_readiness_waits_for_certificate_but_never_approves_or_bypasses_errors(self):
        import ssl
        now=[0];urls=[]
        class Ready:
            status=200
            def __enter__(self):return self
            def __exit__(self,*args):pass
        replies=[TimeoutError('certificate pending'),Ready()]
        def opened(url,timeout):
            urls.append(url);now[0]+=2;reply=replies.pop(0)
            if isinstance(reply,Exception):raise reply
            return reply
        result=guest.wait_private_https('https://node.tail-example.ts.net',clock=lambda:now[0],pause=lambda n:now.__setitem__(0,now[0]+n),opener=SimpleNamespace(open=opened))
        self.assertEqual(result['attempts'],2);self.assertTrue(all(url.endswith('/') and '/approve' not in url for url in urls))
        for failure in [urllib.error.HTTPError('https://node',403,'forbidden',{},None),urllib.error.HTTPError('https://node',302,'redirect',{},None),urllib.error.URLError(ssl.SSLCertVerificationError('invalid'))]:
            with self.subTest(failure=failure),self.assertRaisesRegex(RuntimeError,'refused|verification'):
                guest.wait_private_https('https://node.tail-example.ts.net',opener=SimpleNamespace(open=Mock(side_effect=failure)))
        def never(*args,**kwargs):now[0]+=10;raise TimeoutError('pending')
        with self.assertRaisesRegex(RuntimeError,'readiness deadline'):
            guest.wait_private_https('https://node.tail-example.ts.net',clock=lambda:now[0],pause=lambda n:None,opener=SimpleNamespace(open=never))
        with patch.object(guest.urllib.request,'build_opener',return_value=SimpleNamespace(open=Mock(side_effect=urllib.error.HTTPError('https://node',302,'redirect',{},None)))) as built:
            with self.assertRaisesRegex(RuntimeError,'refused with status 302'):
                guest.wait_private_https('https://node.tail-example.ts.net')
            handler=next(value for value in built.call_args.args if isinstance(value,guest.PrivateHTTPSNoRedirect))
            self.assertIsNone(handler.redirect_request(None,None,302,'redirect',{},'https://outside.example/'))

    def test_real_terminal_repeats_setup_consent_only_after_verified_enrollment_retry(self):
        import sys
        setup=guest.RemoteEnrollmentRetry.setup_prompt
        program="import sys; p="+repr(self.prompt)+"; s="+repr(setup)+"; print(p,end='',flush=True); assert input()=='y'; print(s,end='',flush=True); assert input()=='y'; print("+repr(self.retry)+",flush=True); print(p,end='',flush=True); assert input()=='y'; print(s,end='',flush=True); assert input()=='y'; print('finished',flush=True)"
        with tempfile.TemporaryDirectory() as directory:
            log=pathlib.Path(directory)/'live.log';seen=[]
            def observe(text):
                self.assertTrue(log.exists());self.assertIn(guest.redacted(text),log.read_bytes().decode());seen.append(text)
            retry=guest.RemoteEnrollmentRetry(lambda:True)
            code,out=guest.terminal([sys.executable,'-c',program],{self.prompt:'y',setup:'y'},
                                    timeout=4,prompt_retry=retry,observer=observe,transcript=log)
            self.assertEqual(code,0);self.assertIn('finished',out);self.assertTrue(retry.setup_retried);self.assertTrue(seen)
            with self.assertRaisesRegex(RuntimeError,'more than once'):
                retry(out+setup)

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
    def test_network_owner_uses_installed_admin_entry_not_short_update_launcher(self):
        tree=ast.parse(pathlib.Path(__file__).with_name('staged-package-guest.py').read_text())
        owner=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='owner')
        branch=next(n for n in owner.body if isinstance(n,ast.If) and 'owner_action.startswith' in ast.unparse(n.test))
        function=ast.FunctionDef(name='route',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='args')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=[branch],decorator_list=[])
        calls=[]
        def forbidden():self.fail('administration used short customer launcher')
        scope={'terminal':lambda args,*a,**kw:calls.append(args) or (0,'{"phase":"open"}'),'command_path':forbidden,'json':json,'redacted':lambda v:v,'state_dir':pathlib.Path('/private-fixture-state')}
        exec(compile(ast.fix_missing_locations(ast.Module(body=[function],type_ignores=[])),'<actual-admin-entry>','exec'),scope)
        self.assertEqual(scope['route'](SimpleNamespace(profile='private',owner_action='network-network')),{'phase':'open'})
        self.assertEqual(calls[0],['/var/lib/fased-app-vps-1000/fased-app','vps','network'])

    def test_resume_uses_private_pin_without_touching_closed_bootstrap(self):
        import os
        with tempfile.TemporaryDirectory() as directory:
            work=pathlib.Path(directory);pin=work/'private-known-hosts'
            pin.write_text('node.tail-example.ts.net ssh-ed25519 AAAA\n');pin.chmod(0o600)
            def forbidden(*args,**kwargs):self.fail('resume touched the bootstrap or connected before readiness')
            scope=self.functions(['private_remote_connection'],{'work':work,'key':work/'key','os':os,'run':forbidden,'ssh':forbidden,'sshbase':['ssh','ubuntu@127.0.0.1']})
            value=scope['private_remote_connection'](None,'https://node.tail-example.ts.net',resume=True,connect=False)
            self.assertEqual(value[-1],'ubuntu@node.tail-example.ts.net')
            self.assertEqual(scope['sshbase'],value)
            with self.assertRaisesRegex(AssertionError,'origin differs'):
                scope['private_remote_connection'](None,'https://other.tail-example.ts.net',resume=True,connect=False)

    def test_empty_successful_namespace_list_prepares_owned_probe(self):
        peer=[{'flags':['UP'],'addr_info':[{'local':'198.18.20.2'},{'local':'fd00:fa5e:b020::2'}]}]
        host=[{'flags':['UP'],'addr_info':[{'local':'198.18.20.1'},{'local':'fd00:fa5e:b020::1'}]}]
        with tempfile.TemporaryDirectory() as directory, patch.object(guest.P,'home',return_value=pathlib.Path(directory)):
            guest.write_private(pathlib.Path(directory)/'.cache/fased-staged/state.json',{'instanceId':'I'*43})
            with patch.object(guest,'run',side_effect=[SimpleNamespace(stdout=b''),SimpleNamespace(stdout=json.dumps(peer).encode()),SimpleNamespace(stdout=json.dumps(host).encode())]), patch.object(guest,'os_tool') as commands, patch.object(guest.subprocess,'run',return_value=SimpleNamespace(returncode=0,stdout=b'public-test-key',stderr=b'')):
                result=guest.remote_public_probe(True)
                self.assertTrue(result['ipv4']['reachable'] and result['ipv6']['reachable'])
                self.assertEqual(commands.call_args_list[0].args,('/usr/sbin/ip','netns','add','fased_staged_probe'))
            saved=guest.document(pathlib.Path(directory)/'.cache/fased-staged/state.json')
            self.assertEqual(saved['publicProbe']['instanceId'],'I'*43)

    def test_preboot_resume_creates_delivery_but_retained_config_uses_resume(self):
        parent=next(n for n in ast.parse(pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='staged_package_acceptance')
        index=next(i for i,n in enumerate(parent.body) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='retained_delivery' for t in n.targets))
        code=compile(ast.Module(body=parent.body[index:index+2],type_ignores=[]),'<delivery-resume>','exec')
        for reply in ['new','retained']:
            calls=[]
            values={'a':SimpleNamespace(fresh_remote_only=True,released_remote_only=False,released_local_only=False,resume=True),
                    'ssh':lambda *args:reply,'shlex':__import__('shlex'),'remote_work':'/guest/delivery','guest':'/guest',
                    'guest_call':lambda args:calls.append(args)}
            exec(code,values)
            self.assertEqual('--resume' in calls[0],reply=='retained')

    def test_console_directory_fd_keeps_long_worktree_socket_private(self):
        import os,socket
        with tempfile.TemporaryDirectory() as directory:
            long=pathlib.Path(directory)/('long-private-worktree-'*6);long.mkdir(mode=0o700)
            descriptor=os.open(long,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
            address='/proc/self/fd/'+str(descriptor)+'/console.sock'
            try:
                with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as server, socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as client:
                    server.bind(address);server.listen(1);client.connect(address)
                    peer,_=server.accept();peer.close()
                self.assertTrue((long/'console.sock').is_socket())
                self.assertLess(len(address),108)
            finally:os.close(descriptor)

    def test_actual_security_resume_prepares_probe_before_denial(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory);calls=[]
            journal=guest.RemoteJourney(root/'journal.json',{'source':'a'*40},'b'*64)
            journal.value['phase']='rebooted'
            guest.write_private(root/'remote-public-baseline.json',{'original':'retained'})
            metadata={'passwordRecordSha256':'b'*64,'bootId':'boot','engineSha256':'c'*64,'manifest':{'id':'d'*32}}
            fixture=SimpleNamespace(document=guest.document,package_engine_sha=lambda candidate:'c'*64)
            scope=self.functions(['complete_remote_security'],{'root':root,'save':lambda name,value:None,
                 'private_remote_connection':lambda *args:None,'remote_console':lambda fixture:{'bootId':'boot'}})
            def public(args):
                calls.append(args[0]);raise RuntimeError('probe-ready-stop')
            with self.assertRaisesRegex(RuntimeError,'probe-ready-stop'):
                scope['complete_remote_security'](fixture,{'packet':{'productVersion':'0.1.11'}}, {'instanceId':'I'*43},journal,
                    {'origin':'https://node.tail-example.ts.net'},public,
                    lambda action,*args:metadata if action=='metadata' else None,
                    lambda *args:{'instanceId':'I'*43,'protectedOwnerDevice':True,'ownerState':{'remoteEnrolled':True,'authenticated':True}},None)
            self.assertEqual(calls,['public-probe-prepare'])

    def test_fresh_claim_interruption_resumes_the_same_baseline(self):
        parent=next(n for n in ast.parse(pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='staged_package_acceptance')
        entry=next(n for n in parent.body if isinstance(n,ast.FunctionDef) and n.name=='finish_fresh')
        with tempfile.TemporaryDirectory() as directory:
            base=pathlib.Path(directory)/'private-baseline.json'
            guest.write_private(base,{'instanceId':'I'*43,'sourceVersion':'0.1.11','workspaceSha256':'a'*64})
            observed=[]
            values={'host_browser':base.parent,'fixture':guest,'version':'0.1.11','packages':{'candidate':{}},
                    'remote_browser_factory':lambda *args:None,'owner_call':lambda *args:None,
                    'guest_call':None,'reboot':None,'save':lambda *args:None,
                    'complete_remote_security':lambda *args:observed.append(args[4]) or {'result':'PASS','remaining':['earlier provisional state']}}
            exec(compile(ast.Module(body=[entry],type_ignores=[]),'<fresh-resume-entry>','exec'),values)
            final=values['finish_fresh']({'instanceId':'I'*43},None,{'origin':'https://node.tail-example.ts.net'})
            self.assertNotIn('remaining',final)
            self.assertTrue(observed[0]['preservedOwner'])
            self.assertEqual(guest.document(base)['workspaceSha256'],'a'*64)
            with self.assertRaisesRegex(AssertionError,'foreign or older'):
                values['finish_fresh']({'instanceId':'J'*43},None,{'origin':'https://node.tail-example.ts.net'})

    def test_actual_guest_runner_accepts_a_bounded_timeout(self):
        self.assertEqual(guest.run(['/usr/bin/true'], timeout=5).returncode, 0)

    def test_tailnet_preparation_allows_only_fixed_additional_os_commands(self):
        with patch.object(guest, 'run') as invoked:
            for command in [('/usr/bin/apt-get','update','-qq'),('/usr/bin/apt-get','install','-y','tailscale'),
                            ('/usr/bin/systemctl','enable','--now','tailscaled.service'),
                            ('/usr/bin/tailscale','set','--operator=ubuntu')]:
                guest.os_tool(*command)
            self.assertEqual(invoked.call_count, 4)
            for command in [('/usr/bin/systemctl','stop','ssh.service'),('/usr/bin/tailscale','up','--advertise-exit-node'),('/usr/bin/apt-get','install','-y','anything'),('/bin/bash','project.sh')]:
                with self.assertRaisesRegex(RuntimeError, 'fixed guest OS'):
                    guest.os_tool(*command)

    def test_actual_replacement_branch_entry_does_not_restore_active_disk(self):
        parent=next(n for n in ast.parse(pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='released_remote_acceptance')
        entry=next(n for n in parent.body if isinstance(n,ast.FunctionDef) and n.name=='start_branch')
        with tempfile.TemporaryDirectory() as directory:
            journal=guest.RemoteJourney(pathlib.Path(directory)/'journal.json', {'source':'a'*40}, 'b'*64)
            restored=[];calls=[]
            values={'journal':journal,'restore':lambda: restored.append('restore'), 'ready':lambda timeout:calls.append('ready'),
                    'guest_call':lambda args:calls.append(args[0]),'select_package':lambda name:calls.append(name),'remote_work':'/delivery'}
            exec(compile(ast.Module(body=[entry],type_ignores=[]),'<actual-branch-entry>','exec'),values)
            self.assertFalse(values['start_branch']('ui'));self.assertEqual(restored,['restore'])
            self.assertTrue(values['start_branch']('ui'));self.assertEqual(restored,['restore'])
            self.assertEqual(calls,['ready','network','candidate'])

    def test_released_entry_answers_actual_tailscale_os_consent(self):
        parent=next(n for n in ast.parse(pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='released_remote_acceptance')
        assignment=next(n for n in ast.walk(parent) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='answers' for t in n.targets))
        values={'fixture':guest}
        exec(compile(ast.Module(body=[assignment],type_ignores=[]),'<actual-released-answers>','exec'),values)
        self.assertEqual(values['answers'][guest.RemoteEnrollmentRetry.setup_prompt],'y')

    def test_actual_security_projection_requires_original_owner_metadata(self):
        parent=next(n for n in ast.parse(pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='released_remote_acceptance')
        block=next(n for n in ast.walk(parent) if isinstance(n,ast.If) and isinstance(n.test,ast.Call) and isinstance(n.test.func,ast.Attribute) and isinstance(n.test.func.value,ast.Name) and n.test.func.value.id=='security_path')
        nodes=block.orelse
        start=next(i for i,n in enumerate(nodes) if isinstance(n,ast.Expr) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Attribute) and isinstance(n.value.func.value,ast.Name) and n.value.func.value.id=='security')
        code=compile(ast.Module(body=nodes[start:],type_ignores=[]),'<actual-security-projection>','exec')
        security={'checks':{}};save=Mock();verified=Mock(side_effect=RuntimeError('owner record changed'))
        values={'security':security,'bound_metadata':verified,'save':save,'bind':lambda value:value}
        with self.assertRaisesRegex(RuntimeError,'owner record changed'):exec(code,values)
        self.assertNotIn('authenticationPreservation',security['checks']);save.assert_not_called()
        verified.side_effect=None;exec(code,values)
        self.assertEqual(security['checks']['authenticationPreservation'],'PASS');save.assert_called_once()

    def test_remote_branch_resume_never_authorizes_a_predecessor_restore(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory)/'journey.json'
            original = guest.RemoteJourney(path, {'source': 'a'*40}, 'b'*64)
            self.assertEqual(original.begin_branch('ui'), 'preparing')
            original.activate_branch('ui')
            resumed = guest.RemoteJourney(path, {'source': 'a'*40}, 'b'*64)
            self.assertEqual(resumed.begin_branch('ui'), 'active')
            with self.assertRaisesRegex(RuntimeError, 'preserve the active'):
                resumed.begin_branch('cli')
            resumed.finish_branch('ui', 'c'*32)
            self.assertEqual(resumed.begin_branch('cli'), 'preparing')
            with self.assertRaisesRegex(RuntimeError, 'preserve Remote branch'):
                resumed.finish_branch('cli', 'd'*32)
            resumed.activate_branch('cli');resumed.finish_branch('cli', 'd'*32)
            with self.assertRaisesRegex(RuntimeError, 'preserve the active'):
                resumed.begin_branch('ui')

    def test_probe_infrastructure_failure_is_not_public_denial(self):
        with patch.object(guest, 'run', side_effect=subprocess.CalledProcessError(1, ['ip'])):
            with self.assertRaises(subprocess.CalledProcessError): guest.remote_public_probe()
        peer = [{'flags': ['UP'], 'addr_info': [{'local': '198.18.20.2'}, {'local': 'fd00:fa5e:b020::2'}]}]
        host = [{'flags': ['UP'], 'addr_info': [{'local': '198.18.20.1'}, {'local': 'fd00:fa5e:b020::1'}]}]
        with patch.object(guest, 'run', side_effect=[SimpleNamespace(stdout=json.dumps(peer).encode()), SimpleNamespace(stdout=json.dumps(host).encode())]), patch.object(guest.subprocess, 'run', return_value=SimpleNamespace(returncode=1, stdout=b'', stderr=b'sudo: permission denied')):
            with self.assertRaisesRegex(RuntimeError, 'before a verifiable network denial'): guest.remote_public_probe()
        with patch.object(guest, 'run', side_effect=[SimpleNamespace(stdout=json.dumps(peer).encode()), SimpleNamespace(stdout=json.dumps(host).encode())]), patch.object(guest.subprocess, 'run', return_value=SimpleNamespace(returncode=1, stdout=b'', stderr=b'')):
            proof = guest.remote_public_probe()
            self.assertEqual(set(proof), {'ipv4', 'ipv6'})
            self.assertFalse(any(v['reachable'] for v in proof.values()))
        with patch.object(guest, 'run', return_value=SimpleNamespace(stdout=b'[]')):
            with self.assertRaisesRegex(RuntimeError, 'not ready'): guest.remote_public_probe()

    def test_foreign_firewall_order_is_preserved_but_owned_restriction_is_separate(self):
        identity='a'*32
        first={'rule': {'family': 'inet', 'table': 'foreign', 'chain': 'input', 'handle': 1, 'expr': [{'accept': None}]}}
        second={'rule': {'family': 'inet', 'table': 'foreign', 'chain': 'input', 'handle': 2, 'expr': [{'drop': None}]}}
        rules={'nftables': [first, second]}
        expected=guest.foreign_firewall_digest(rules, identity)
        self.assertEqual(expected, guest.foreign_firewall_digest({'nftables': [first, second, {'table': {'family':'inet','name':'fased_vps_1000_'+identity[:12]+'_'+'b'*32}}]}, identity))
        self.assertNotEqual(expected, guest.foreign_firewall_digest({'nftables': [second, first]}, identity))
        self.assertNotEqual(expected, guest.foreign_firewall_digest({'nftables': [first]}, identity))

    def test_retained_observations_cannot_inherit_a_different_package(self):
        candidate={'packet': {'source':'a'*40,'productVersion':'0.1.11','release':{'sourceTree':'b'*40,'runId':'12','runAttempt':'1'}},'assets':[]}
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory)/'observation.json'
            value=guest.bind_remote_observation({'result':'PASS'}, candidate, 'c'*64, 'I'*43)
            guest.write_private(path,value)
            self.assertEqual(guest.read_remote_observation(path,candidate,'c'*64,'I'*43),value)
            for field,bad in [('source','d'*40),('localReceiptSha256','e'*64),('instanceId','J'*43)]:
                guest.write_private(path,{**value,field:bad})
                with self.assertRaisesRegex(RuntimeError,'binding differs'):
                    guest.read_remote_observation(path,candidate,'c'*64,'I'*43)

    def test_actual_guest_entry_preserves_private_dns_and_drops_only_username(self):
        source = pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()
        parent = next(n for n in ast.parse(source).body if isinstance(n, ast.FunctionDef) and n.name == 'staged_package_acceptance')
        entry = next(n for n in parent.body if isinstance(n, ast.FunctionDef) and n.name == 'guest_call')
        commands = []
        fixture = SimpleNamespace(checked_reply=lambda reply, owner, action, path: {'result': 'PASS'})
        values = {'guest': '/var/tmp/fased-staged', 'root': pathlib.Path('/private-evidence'),
                  'sshbase': ['ssh', '-F', '/dev/null', 'ubuntu@vm.tail-example.ts.net'],
                  'fixture': fixture, 'shlex': __import__('shlex'),
                  'subprocess': SimpleNamespace(PIPE=-1, run=lambda args, **kwargs: commands.append(args))}
        exec(compile(ast.Module(body=[entry], type_ignores=[]), '<actual-private-entry>', 'exec'), values)
        values['guest_call'](['owner', '--owner-action', 'state'], 'ubuntu')
        self.assertEqual(commands[0][-2], 'ubuntu@vm.tail-example.ts.net')
        self.assertNotIn('127.0.0.1', ' '.join(commands[0]))

    def test_remote_completion_requires_actual_completed_application(self):
        operation = {'id': 'a' * 32, 'phase': 'completed', 'automatic': False,
                     'release': {'version': '0.1.11', 'manifestSha256': 'b' * 64,
                                 'engineSha256': 'c' * 64, 'sequence': '1000012'}}
        operation['receipt'] = {'id': operation['id'], 'phase': 'completed',
                                **{k: operation['release'][k] for k in ('manifestSha256', 'engineSha256', 'sequence')}}
        state = {'installedVersion': '0.1.11', 'operation': operation}
        self.assertEqual(guest.completed_remote_application(state, '0.1.11', False), operation)
        for changed in [{'phase': 'current'}, {'id': 'd' * 32}, {'automatic': True},
                        {'receipt': {**operation['receipt'], 'engineSha256': 'e' * 64}}]:
            with self.subTest(changed=changed), self.assertRaisesRegex(RuntimeError, 'completed'):
                guest.completed_remote_application({**state, 'operation': {**operation, **changed}}, '0.1.11', False)
        with self.assertRaisesRegex(RuntimeError, 'completed'):
            guest.completed_remote_application({**state, 'installedVersion': '0.1.10'}, '0.1.11', False)

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

    def test_public_helper_normalizes_actual_ordinary_tar_extraction(self):
        import tarfile
        for mode in [0o700, 0o555]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory)
                helper = root / "private-build-output"
                helper.write_bytes(b"nonsecret-disposable-helper")
                helper.chmod(0o700)
                payload = root / "payload.tar"
                with tarfile.open(payload, "w") as archive:
                    entry = archive.gettarinfo(helper, "fased-release-fixture")
                    entry.mode = mode
                    with helper.open("rb") as stream: archive.addfile(entry, stream)
                destination = root / "guest"
                destination.mkdir()
                subprocess.run(['/bin/sh', '-c', 'umask 077; tar -xf "$1" -C "$2"', 'extract', str(payload), str(destination)], check=True)
                shared = destination / 'fased-release-fixture'
                self.assertEqual(shared.stat().st_mode & 0o777, mode & ~0o077)
                def ssh(command, timeout):
                    return subprocess.run(['/bin/sh', '-c', command], check=True,
                                          capture_output=True, text=True, timeout=timeout).stdout
                scope = self.functions(['shared_provider_helper'], {'ssh': ssh})
                scope['shared_provider_helper'](str(destination))
                self.assertEqual(shared.stat().st_mode & 0o777, 0o555)
                self.assertEqual(shared.read_bytes(), helper.read_bytes())
                self.assertEqual(helper.stat().st_mode & 0o777, 0o700)
                shared.unlink(); shared.symlink_to(helper)
                with self.assertRaises(subprocess.CalledProcessError):
                    scope['shared_provider_helper'](str(destination))
                self.assertEqual(helper.stat().st_mode & 0o777, 0o700)

    def test_verified_public_installers_remain_readable_by_other_fixture_owners(self):
        source = pathlib.Path(__file__).with_name('check-inspection-vm.py').read_text()
        tree = ast.parse(source)
        parent = next(node for node in tree.body
                      if isinstance(node, ast.FunctionDef) and node.name == 'staged_package_acceptance')
        verification = next(node for node in parent.body
                            if isinstance(node, ast.For) and isinstance(node.target, ast.Name)
                            and node.target.id == 'label' and isinstance(node.iter, ast.List)
                            and [item.value for item in node.iter.elts] == ['candidate', 'predecessor'])
        # Execute the actual driver commands, including their mode transition.
        # These are public attested assets, not credentials or private state.
        code = compile(ast.Module(body=[verification], type_ignores=[]), '<shared-verification>', 'exec')
        for status in [0, 17]:
            with self.subTest(status=status), tempfile.TemporaryDirectory() as temporary:
                root = pathlib.Path(temporary)
                installers = []
                for label in ['candidate', 'predecessor']:
                    directory = root / 'packages' / label
                    directory.mkdir(parents=True)
                    installer = directory / 'fased-install-linux-amd64'
                    installer.write_text('#!/bin/sh\ntest "$1" = --verify-candidate || exit 23\nexit ' + str(status) + '\n')
                    installer.chmod(0o700)
                    installers.append(installer)
                calls = []
                def ssh(command, timeout):
                    calls.append(command)
                    return subprocess.run(['/bin/sh', '-c', command], check=True,
                                          capture_output=True, text=True, timeout=timeout).stdout
                if status:
                    with self.assertRaises(subprocess.CalledProcessError) as failed:
                        exec(code, {'guest': str(root), 'ssh': ssh})
                    self.assertEqual(failed.exception.returncode, status)
                    self.assertEqual(len(calls), 1)
                    self.assertEqual(installers[1].stat().st_mode & 0o777, 0o700)
                else:
                    exec(code, {'guest': str(root), 'ssh': ssh})
                    self.assertEqual(len(calls), 2)
                    for installer in installers:
                        self.assertEqual(installer.stat().st_mode & 0o777, 0o555)
                        self.assertTrue(installer.stat().st_mode & 0o004)
                        self.assertEqual(installer.read_bytes()[:9], b'#!/bin/sh')

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
                self.assertEqual(config,{'fixtureOnly':True,'action':'private-preflight','url':'https://fased.tail-example.ts.net/#'+'A'*43,'expectedOrigin':'https://fased.tail-example.ts.net',
                                         'diagnosticFile':str(root/'private-preflight-response.json'),'screenshotFile':str(root/'private-preflight.png')})
                self.assertIn('TLS-ready https://fased.tail-example.ts.net',calls)
                self.assertEqual(pathlib.Path(command[2]).stat().st_mode & 0o777,0o600)
                return subprocess.CompletedProcess(command,0,json.dumps({'result':'PASS','action':'private-preflight','privateBrowserApproved':True}).encode(),b'')
            candidate={'packet':{'source':'a'*40,'productVersion':'0.2.0','release':{'sourceTree':'b'*40,'runId':'12','runAttempt':'1'}},'assets':[]}
            health={'version':'0.2.0','capabilities':{'aiConnections':False,'financialExecution':False,'walletSigner':False}}
            response=Mock();response.__enter__=Mock(return_value=io.BytesIO(json.dumps(health).encode()));response.__exit__=Mock(return_value=False)
            opener=Mock();opener.open.return_value=response
            fixture=SimpleNamespace(RemoteJourney=guest.RemoteJourney,browser_link=guest.browser_link,binding=guest.binding,write_private=guest.write_private,redacted=guest.redacted,terminal=terminal,PREPARATION_ANSWERS=guest.PREPARATION_ANSWERS,RemoteEnrollmentRetry=guest.RemoteEnrollmentRetry,private_tailnet_origin=guest.private_tailnet_origin,
                                    wait_private_https=lambda origin:calls.append('TLS-ready '+origin) or {'result':'PASS','scope':'TLS readiness only'})
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
    def test_provider_readiness_waits_only_for_observed_startup_refusal(self):
        import urllib.error
        now = [0]
        def pause(value): now[0] += value
        refused = urllib.error.URLError(ConnectionRefusedError(111, 'starting'))
        with patch.object(guest, 'health', side_effect=[refused, refused, {'version': '0.1.11'}]) as health:
            self.assertEqual(guest.provider_health(17485, '0.1.11', lambda: now[0], pause), {'version': '0.1.11'})
            self.assertEqual(health.call_count, 3)
        self.assertAlmostEqual(now[0], 0.1)
        now[0] = 0
        def advance(value): now[0] += 5
        with patch.object(guest, 'health', side_effect=refused) as health:
            with self.assertRaises(urllib.error.URLError):
                guest.provider_health(17485, '0.1.11', lambda: now[0], advance)
            self.assertEqual(health.call_count, 3)
        for failure in [RuntimeError('held live capabilities enabled'), urllib.error.URLError('denied')]:
            with patch.object(guest, 'health', side_effect=failure) as health:
                with self.assertRaises(type(failure)):
                    guest.provider_health(17485, '0.1.11', lambda: 0, lambda value: self.fail('invalid health retried'))
                self.assertEqual(health.call_count, 1)
        with patch.object(guest, 'health', return_value={'version': 'foreign'}):
            with self.assertRaisesRegex(RuntimeError, 'version differs'):
                guest.provider_health(17485, '0.1.11')

    def test_exact_bundle_extraction_and_preserved_attempt(self):
        import tarfile
        with tempfile.TemporaryDirectory() as d:
            root=pathlib.Path(d); engine=b"disposable-contract-engine"; manifest=json.dumps({"files":{"fased-app":{"sha256":guest.digest(engine),"bytes":len(engine)}}}).encode()
            with tarfile.open(root/guest.ASSETS[0],"w:gz") as archive:
                for name,raw in [("bundle.json",manifest),("fased-app",engine)]:
                    entry=tarfile.TarInfo("fased-app-linux-amd64/"+name);entry.size=len(raw);archive.addfile(entry,io.BytesIO(raw))
            candidate={"directory":str(root),"packet":{"bundleSha256":guest.digest(manifest)}}
            expected,raw=guest.verified_provider_bundle(candidate,root/"owned")
            self.assertEqual(expected,guest.digest(engine));self.assertEqual(raw,manifest)
            self.assertEqual((root/"owned/fased-app").read_bytes(),engine)
            self.assertEqual((root/"owned/bundle.json").read_bytes(),manifest)
            with self.assertRaises(FileExistsError):guest.verified_provider_bundle(candidate,root/"owned")
            with self.assertRaisesRegex(RuntimeError,"manifest differs"):guest.verified_provider_bundle({**candidate,"packet":{"bundleSha256":"0"*64}},root/"wrong")

    def test_link_member_never_becomes_an_installed_engine(self):
        import tarfile
        with tempfile.TemporaryDirectory() as d:
            root=pathlib.Path(d)
            with tarfile.open(root/guest.ASSETS[0],"w:gz") as archive:
                entry=tarfile.TarInfo("fased-app-linux-amd64/bundle.json");entry.type=tarfile.SYMTYPE;entry.linkname="/etc/passwd";archive.addfile(entry)
            with self.assertRaisesRegex(RuntimeError,"bounded original"):
                guest.verified_provider_bundle({"directory":str(root),"packet":{"bundleSha256":"0"*64}},root/"owned")

if __name__ == '__main__':
    unittest.main()
