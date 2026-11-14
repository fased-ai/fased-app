#!/usr/bin/env python3
"""Ordinary-owner guest actions for the confined custody VM acceptance.

This is disposable-fixture code, not an installer or a production entrypoint.
The host runner owns the VM lifecycle and performs the actual interruption.
"""
import argparse
import fcntl
import hashlib
import http.server
import json
import os
import pathlib
import re
import ssl
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.request

P = pathlib.Path
OWNER = 1100
HOME = P('/home/fased-local')
WORK = HOME / 'custody-fixture'
TRUST = P('/var/lib/fased-update-fixture')
ROOT = P('/var/lib/fased-app-custody-1100')
REMOTE = P('/var/lib/fased-app-vps-1100')
PASSWORD = 'Disposable custody owner 7WQfE2tV'
WALLET_PASSWORD = 'Disposable custody wallet 6YDkP9nH'
RPC_PASSWORD = 'Disposable custody RPC 5CJmX8sB'
RPC_KEY = 'disposable-rpc-fixture-only'


def guard():
    if os.getuid() != OWNER or os.geteuid() != OWNER or P.home() != HOME:
        raise RuntimeError('fixed ordinary fixture owner required')
    product = P('/sys/class/dmi/id/product_name').read_text()
    if 'QEMU' not in product and 'Standard PC' not in product:
        raise RuntimeError('disposable QEMU receiver required')
    for path in (TRUST, TRUST / 'seed.json', TRUST / 'ca.pem', TRUST / 'fixture.json'):
        s = path.lstat()
        if s.st_uid != 0 or stat.S_ISLNK(s.st_mode) or s.st_mode & 0o022 or (path != TRUST and (not stat.S_ISREG(s.st_mode) or s.st_nlink != 1)):
            raise RuntimeError('protected fixture trust required')
    trust = json.loads((TRUST / 'fixture.json').read_text())
    if not trust.get('connected') or trust.get('seedSha256') != digest(TRUST / 'seed.json') or trust.get('caSha256') != digest(TRUST / 'ca.pem'):
        raise RuntimeError('protected fixture trust identity differs')
    if WORK.is_symlink() or WORK.stat().st_uid != OWNER or stat.S_IMODE(WORK.stat().st_mode) != 0o700:
        raise RuntimeError('private fixture workspace required')


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def require_launcher(path, target):
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != OWNER or
            stat.S_IMODE(info.st_mode) != 0o700 or info.st_nlink != 1 or info.st_size > 8192):
        raise RuntimeError('installed command identity differs')
    lines = path.read_text().splitlines()
    if [line for line in lines if line.startswith('core=')] != ["core='" + str(target) + "'"]:
        raise RuntimeError('installed command does not use the enrolled executable')


def save(name, value):
    target = WORK / name
    pending = WORK / (name + '.pending')
    fd = os.open(pending, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(value, f, separators=(',', ':'))
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())
    os.link(pending, target, follow_symlinks=False)
    fd = os.open(WORK, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
        pending.unlink()
        os.fsync(fd)
    finally:
        os.close(fd)


def safe_fixture_error(text):
    known = (
        ('enrollment source payload validation:', 'protected enrollment source validation refused'),
        ('enrollment source helper frame:', 'protected enrollment source frame refused'),
        ('source lock helper exited; preserve both copies', 'enrollment source lease was lost'),
        ('original verified enrollment package is unavailable', 'original verified enrollment package unavailable'),
        ('original verified core is unavailable; preserve operation', 'original verified core unavailable'),
        ('original verified signer is unavailable; preserve operation', 'original verified signer unavailable'),
    )
    return next((label for message, label in known if message in text), '')


def run(args, data=None, timeout=180, expect_failure=False):
    env = {'HOME': str(HOME), 'USER': 'fased-local', 'LOGNAME': 'fased-local',
           'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LANG': 'C.UTF-8',
           'XDG_RUNTIME_DIR': '/run/user/1100', 'DBUS_SESSION_BUS_ADDRESS': 'unix:path=/run/user/1100/bus'}
    p = subprocess.run([str(a) for a in args], input=data, text=True, capture_output=True, timeout=timeout, env=env)
    if expect_failure:
        if p.returncode == 0:
            raise RuntimeError('intentional fixture interruption unexpectedly completed')
        save('intentional-enrollment-failure.json', {'exit': p.returncode, 'stderr': p.stderr[-8192:]})
        return p.stdout
    if p.returncode:
        if len(args) >= 3 and list(args[1:3]) == ['custody', 'enroll']:
            # Fixture-only diagnostics stay in the existing private workspace;
            # never print command input or replace a previous failure.
            try:
                save('enrollment-failure.json', {'exit': p.returncode, 'stderr': p.stderr[-8192:]})
            except OSError:
                pass
        detail = safe_fixture_error(p.stderr)
        raise RuntimeError('fixture command failed: ' + pathlib.Path(str(args[0])).name + ' exit ' + str(p.returncode) + ('; ' + detail if detail else ''))
    return p.stdout


def api(origin, route, method='GET', body=None, session=None):
    headers = {'Origin': origin}
    if session:
        headers['Authorization'] = 'Bearer ' + session['sessionToken']
    if body is not None:
        headers['Content-Type'] = 'application/json'
        if session:
            headers['X-CSRF-Token'] = session['csrfToken']
        payload = json.dumps(body, separators=(',', ':')).encode()
    else:
        payload = None
    req = urllib.request.Request(origin + route, data=payload, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        raise RuntimeError('fixture API ' + route + ' HTTP ' + str(e.code)) from e


def health(origin, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            return api(origin, '/api/v1/health')
        except (OSError, RuntimeError):
            time.sleep(0.2)
    raise RuntimeError('installed fixture core did not become ready')


def login(origin):
    return api(origin, '/api/v1/auth/login', 'POST', {'password': PASSWORD})


def seed_source(origin, binary, session=None):
    if session is None:
        # Reuse the setup proof already issued by this core. The claim API
        # verifies its hash and expiry; never rotate it during a fixture retry.
        fd = os.open(HOME / '.config/fased-app/claim-code.json',
                     os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
        with os.fdopen(fd, 'rb') as f:
            info = os.fstat(f.fileno())
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != OWNER or
                    stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1 or info.st_size > 4096):
                raise RuntimeError('original private fixture claim required')
            code = json.load(f).get('code')
        if not isinstance(code, str) or not re.fullmatch(r'[A-Za-z0-9_-]{43}', code):
            raise RuntimeError('original fixture claim unavailable')
        session = api(origin, '/api/v1/auth/claim', 'POST', {'code': code, 'password': PASSWORD})
    view = api(origin, '/api/v1/agents', session=session)
    if any(t.get('name') == 'Disposable preserved task' for t in view['tasks']):
        raise RuntimeError('fixture task already exists; preserve it')
    body = {'revision': view['revision'], 'profileId': view['profileId'], 'action': 'task-create',
            'name': 'Disposable preserved task', 'text': 'Fixture reminder', 'intervalSeconds': 3600, 'maxRuns': 2}
    view = api(origin, '/api/v1/agents', 'POST', body, session)
    task_id = view['tasks'][-1]['id']
    api(origin, '/api/v1/agents', 'POST', {'revision': view['revision'], 'profileId': view['profileId'],
                                          'action': 'task-pause', 'id': task_id}, session)
    return task_id


def server():
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            try:
                selection = (WORK / 'selection').read_text().strip()
            except FileNotFoundError:
                selection = 'base'
            if selection not in ('base', 'target') or not re.fullmatch(r'/[A-Za-z0-9._/-]+', self.path) or '..' in self.path:
                self.send_error(404)
                return
            base = (WORK / ('repo-' + selection)).resolve()
            path = (base / self.path.lstrip('/')).resolve()
            if not path.is_relative_to(base) or not path.is_file():
                self.send_error(404)
                return
            b = path.read_bytes()
            self.send_response(200)
            self.send_header('Content-Length', str(len(b)))
            self.end_headers()
            self.wfile.write(b)

        def log_message(self, *_):
            pass

    service = http.server.ThreadingHTTPServer(('127.0.0.1', 17449), Handler)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(WORK / 'ca.pem', WORK / 'key.pem')
    service.socket = context.wrap_socket(service.socket, server_side=True)
    service.serve_forever()


def probe():
    context = ssl.create_default_context(cafile=str(TRUST / 'ca.pem'))
    deadline = time.monotonic() + 10
    while True:
        try:
            with urllib.request.urlopen('https://127.0.0.1:17449/timestamp.json', context=context, timeout=5) as r:
                if r.status != 200 or len(r.read(65537)) > 65536:
                    raise RuntimeError('bounded loopback fixture is not ready')
            break
        except urllib.error.URLError as e:
            if not isinstance(e.reason, (ConnectionRefusedError, TimeoutError)) or time.monotonic() >= deadline:
                raise
            time.sleep(0.2)
    print(json.dumps({'result': 'READY', 'fixtureOnly': True}))


def target():
    states = [path for path in (WORK / 'state.json', WORK / 'remote-state.json') if path.is_file()]
    if (WORK / 'selection').read_text() != 'base\n' or len(states) != 1:
        raise RuntimeError('original enrolled fixture required before target selection')
    pending = WORK / 'selection.pending'
    fd = os.open(pending, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write('target\n')
        f.flush()
        os.fsync(f.fileno())
    os.replace(pending, WORK / 'selection')
    fd = os.open(WORK, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    print(json.dumps({'result': 'TARGET_SELECTED', 'fixtureOnly': True}))


def inspect():
    j = json.loads((P('/var/lib/fased-maintenance/owners/1100') / 'custody.json').read_text())
    print(json.dumps({'operationId': j['operationId'], 'phase': j['phase'], 'start': j.get('start'),
                      'installationPhase': (j.get('installation') or {}).get('phase')}))


def setup(port):
    import importlib.util
    spec = importlib.util.spec_from_file_location('staged', WORK / 'staged-package-guest.py')
    staged = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(staged)
    helper = P((WORK / 'helper-path').read_text().strip())
    if not re.fullmatch(r'/var/lib/fased-maintenance/images/[a-f0-9]{64}/fased-install', str(helper)):
        raise RuntimeError('fixed protected helper path required')
    info = helper.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o555 or info.st_nlink != 1 or digest(helper) != helper.parent.name:
        raise RuntimeError('protected helper image differs')
    if (WORK / 'state.json').exists():
        raise RuntimeError('existing fixture state must be preserved')
    with (WORK / 'selection').open('x') as f:
        f.write('base\n')
    trust = json.loads((TRUST / 'fixture.json').read_text())
    output = WORK / 'verified-install'
    args = [str(helper), '--development', '--seed-file', str(TRUST / 'seed.json'), '--seed-sha256', trust['seedSha256'],
            '--tls-ca-file', str(TRUST / 'ca.pem'), '--output', str(output), '--install', '--profile', 'local',
            '--listen', '127.0.0.1:' + str(port)]
    status, _ = staged.terminal(args, {'Start Fased at login? [y/N]: ': 'y',
                                      'Authorize required Ubuntu preparation with sudo? [y/N]: ': 'n'},
                                timeout=300, password_free=True)
    if status:
        raise RuntimeError('fixture Local installer exited ' + str(status))
    return complete_setup(port)


def resume_claimed(port):
    if (WORK / 'selection').read_text() != 'base\n' or (WORK / 'state.json').exists() or (WORK / 'wallet-backup.json').exists():
        raise RuntimeError('original pre-enrollment fixture required')
    if os.path.lexists(HOME / '.config/fased-app-enrollment') or os.path.lexists(ROOT):
        raise RuntimeError('existing enrollment must be preserved')
    binary = HOME / '.local/share/fased-app/fased-app'
    trust = json.loads((TRUST / 'fixture.json').read_text())
    manifest = json.loads((HOME / '.local/share/fased-app/install.json').read_text())
    if digest(binary) != trust['sourceSha256'] or manifest.get('phase') != 'ready' or manifest.get('location') != 'local' or manifest.get('listen') != '127.0.0.1:' + str(port) or manifest.get('data') != str(HOME / '.config/fased-app'):
        raise RuntimeError('original installed fixture differs')
    origin = 'http://127.0.0.1:' + str(port)
    claimed = api(origin, '/api/v1/auth/state').get('claimed')
    if claimed is not True and claimed is not False:
        raise RuntimeError('original fixture owner state is unknown')
    return complete_setup(port, login(origin) if claimed else None)


def remote_enroll(instance):
    """Resume one simulated first-write interruption on the real Remote entry."""
    if not re.fullmatch(r'[A-Za-z0-9_-]{43}', instance):
        raise RuntimeError('exact Remote instance required')
    binary = REMOTE / 'fased-app'
    trust = json.loads((TRUST / 'fixture.json').read_text())
    manifest = json.loads((REMOTE / 'install.json').read_text())
    status = json.loads(run([binary, 'vps', 'status']))
    if (digest(binary) != trust['sourceSha256'] or manifest.get('location', 'remote') != 'remote' or
            not status.get('active') or status.get('instanceId') != instance or
            not re.fullmatch(r'https://[a-z0-9-]+\.[a-z0-9-]+\.ts\.net', status.get('url', ''))):
        raise RuntimeError('original active Remote source differs')
    if (WORK / 'remote-state.json').exists() or os.path.lexists(ROOT):
        raise RuntimeError('existing Remote enrollment must be preserved')
    enrollment = [binary, 'custody', 'enroll', '--source', 'remote', '--expected-instance', instance,
                  '--network', 'devnet', '--signer-binary', WORK / 'fased-signer', '--prepare-host',
                  '--disposable-fixture', '--ordinary-custody']
    selection = WORK / 'selection'
    if not os.path.lexists(selection):
        with selection.open('x') as f:
            f.write('base\n')
            f.flush()
            os.fsync(f.fileno())
    fd = os.open(selection, os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC)
    with os.fdopen(fd, 'r+', encoding='utf-8') as f:
        identity = os.fstat(f.fileno())
        if not stat.S_ISREG(identity.st_mode) or identity.st_uid != OWNER or identity.st_nlink != 1 or f.read() != 'base\n':
            raise RuntimeError('original base fixture required before interruption')
        f.seek(0); f.truncate(); f.write('interrupted\n'); f.flush(); os.fsync(f.fileno())
        try:
            run(enrollment, timeout=180, expect_failure=True)
        finally:
            f.seek(0); f.truncate(); f.write('base\n'); f.flush(); os.fsync(f.fileno())
        if not os.path.samestat(identity, selection.lstat()):
            raise RuntimeError('fixture repository selection changed')
    directory = HOME / '.config/fased-app-enrollment'
    journal, pending = directory / 'state.json', directory / '.pending-fixture-first-write'
    directory_info, journal_info = directory.lstat(), journal.lstat()
    if (not stat.S_ISDIR(directory_info.st_mode) or directory_info.st_uid != OWNER or
            stat.S_IMODE(directory_info.st_mode) != 0o700 or not stat.S_ISREG(journal_info.st_mode) or
            journal_info.st_uid != OWNER or stat.S_IMODE(journal_info.st_mode) != 0o600 or
            journal_info.st_nlink != 1 or not 0 < journal_info.st_size <= 16384):
        raise RuntimeError('private original Remote enrollment journal required')
    raw = journal.read_bytes()
    prepared = json.loads(raw)
    if (prepared.get('phase') != 'prepared' or prepared.get('source', {}).get('expectedInstanceId') != instance or
            prepared.get('source', {}).get('profile') != 'remote' or prepared.get('coreHash') != digest(binary) or
            prepared.get('signerHash') != digest(WORK / 'fased-signer') or prepared.get('target', {}).get('id') or
            not re.fullmatch(r'[a-f0-9]{32}', prepared.get('id', '')) or list(directory.glob('.pending-*'))):
        raise RuntimeError('original prepared Remote enrollment differs')
    lock = os.open(directory / 'controller.lock', os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if (os.path.lexists(pending) or not os.path.samestat(directory_info, directory.lstat()) or
                not os.path.samestat(journal_info, journal.lstat()) or journal.read_bytes() != raw or
                {p.name for p in directory.iterdir()} != {'controller.lock', 'state.json'}):
            raise RuntimeError('original prepared Remote enrollment changed')
        # Simulate an interrupted first publication without deleting its bytes.
        os.rename(journal, pending)
        fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        os.close(lock)
    result = json.loads(run(enrollment, timeout=600))
    completed = json.loads(journal.read_text())
    if (result.get('instanceId') != instance or result.get('walletSigner') or result.get('financialExecution') or
            completed.get('id') != prepared['id'] or completed.get('phase') != 'complete' or pending.read_bytes() != raw):
        raise RuntimeError('original Remote enrollment did not recover')
    backup = WORK / 'wallet-backup.json'
    wallet = json.loads(run([ROOT / 'fased-app', 'custody', 'setup', '--disposable-fixture', '--password-stdin', '--backup-file', backup],
                            (WALLET_PASSWORD + '\n') * 4, timeout=180))
    signer = json.loads(run([ROOT / 'fased-signer', 'owner', '--config', ROOT / 'identity.json', '--disposable-fixture']))
    if not wallet['backupVerified'] or not signer['locked'] or not signer['recoveryRequired'] or signer['walletSigner']:
        raise RuntimeError('locked Remote fixture wallet and backup required')
    maintenance = json.loads((P('/var/lib/fased-maintenance/owners/1100') / 'custody.json').read_text())
    if maintenance['phase'] != 'idle' or maintenance['package']['sequence'] != '8':
        raise RuntimeError('original Remote maintenance journal required')
    save('remote-state.json', {'instanceId': instance, 'initialOperation': maintenance['operationId'],
         'signerAccount': signer['account'], 'recoveryRequired': signer['recoveryRequired'],
         'admissionHeld': signer['admissionHeld'], 'walletBackupSha256': digest(backup)})
    print(json.dumps({'result': 'READY', 'fixtureOnly': True, 'instanceId': instance,
                      'signerAccount': signer['account'], 'enrollmentRecovery': {'simulated': True,
                      'pendingOnly': True, 'operationId': prepared['id']}}))


def remote_verify(operation):
    state = json.loads((WORK / 'remote-state.json').read_text())
    trust = json.loads((TRUST / 'fixture.json').read_text())
    signer = json.loads(run([ROOT / 'fased-signer', 'owner', '--config', ROOT / 'identity.json', '--disposable-fixture']))
    if (not signer['locked'] or signer['account'] != state['signerAccount'] or
            signer['recoveryRequired'] != state['recoveryRequired'] or signer['admissionHeld'] != state['admissionHeld'] or
            digest(WORK / 'wallet-backup.json') != state['walletBackupSha256']):
        raise RuntimeError('original Remote wallet, lock or backup changed')
    journal = json.loads((P('/var/lib/fased-maintenance/owners/1100') / 'custody.json').read_text())
    if (journal['operationId'] != operation or journal['phase'] != 'completed' or journal.get('start') != 'target' or
            journal['package']['sequence'] != '9' or digest(ROOT / 'fased-app') != trust['targetSha256'] or
            digest(ROOT / 'fased-signer') != trust['targetSignerSha256']):
        raise RuntimeError('original Remote paired update is not complete')
    print(json.dumps({'result': 'PASS', 'fixtureOnly': True, 'instanceId': state['instanceId'],
                      'operationId': operation, 'lockedWallet': True, 'backupPreserved': True,
                      'targetCoreSignerMatched': True}))


def complete_setup(port, session=None):
    binary = HOME / '.local/share/fased-app/fased-app'
    origin = 'http://127.0.0.1:' + str(port)
    original = health(origin)
    task_id = seed_source(origin, binary, session)
    enrollment = [binary, 'custody', 'enroll', '--source', 'local', '--expected-instance', original['instanceId'],
                  '--network', 'devnet', '--signer-binary', WORK / 'fased-signer', '--prepare-host',
                  '--disposable-fixture', '--ordinary-custody', '--native-owner']
    # In this disposable guest only, hold acquisition after the real
    # controller records its prepared operation. Then use the protected helper
    # to obtain the exact target for a simulated interrupted journal write.
    selection = WORK / 'selection'
    fd = os.open(selection, os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC)
    with os.fdopen(fd, 'r+', encoding='utf-8') as f:
        identity = os.fstat(f.fileno())
        if (not stat.S_ISREG(identity.st_mode) or identity.st_uid != OWNER or identity.st_nlink != 1 or
                identity.st_size > 16 or f.read() != 'base\n'):
            raise RuntimeError('original base fixture required before interruption')
        f.seek(0)
        f.truncate()
        f.write('interrupted\n')
        f.flush()
        os.fsync(f.fileno())
        try:
            run(enrollment, timeout=180, expect_failure=True)
        finally:
            f.seek(0)
            f.truncate()
            f.write('base\n')
            f.flush()
            os.fsync(f.fileno())
        current = selection.lstat()
        if current.st_dev != identity.st_dev or current.st_ino != identity.st_ino or not stat.S_ISREG(current.st_mode):
            raise RuntimeError('fixture repository selection changed')
    directory = HOME / '.config/fased-app-enrollment'
    journal = directory / 'state.json'
    dir_info, journal_info = directory.lstat(), journal.lstat()
    if (not stat.S_ISDIR(dir_info.st_mode) or dir_info.st_uid != OWNER or stat.S_IMODE(dir_info.st_mode) != 0o700 or
            not stat.S_ISREG(journal_info.st_mode) or journal_info.st_uid != OWNER or
            stat.S_IMODE(journal_info.st_mode) != 0o600 or journal_info.st_nlink != 1 or not 0 < journal_info.st_size <= 16384):
        raise RuntimeError('private original enrollment journal required')
    raw = journal.read_bytes()
    prepared = json.loads(raw)
    if (prepared.get('phase') != 'prepared' or prepared.get('source', {}).get('expectedInstanceId') != original['instanceId'] or
            prepared.get('coreHash') != digest(binary) or prepared.get('signerHash') != digest(WORK / 'fased-signer') or
            prepared.get('target', {}).get('id') or not re.fullmatch(r'[a-f0-9]{32}', prepared.get('id', '')) or
            list(directory.glob('.pending-*'))):
        raise RuntimeError('original prepared enrollment identity differs')
    source = prepared['source']
    request = {'version': 1, 'action': 'custody-enrollment-prepare', 'enrollment': {
        'operationId': prepared['id'], 'network': prepared['network'], 'listen': source['listen'],
        'nativeOwnerAccess': True, 'coreSha256': prepared['coreHash'], 'signerSha256': prepared['signerHash'],
        'sequence': str(source['applicationSequence']), 'releaseSha256': source['applicationReleaseSha256']}}
    helper = P((WORK / 'helper-path').read_text().strip())
    helper_info = helper.lstat()
    if (not re.fullmatch(r'/var/lib/fased-maintenance/images/[a-f0-9]{64}/fased-install', str(helper)) or
            not stat.S_ISREG(helper_info.st_mode) or helper_info.st_uid != 0 or
            stat.S_IMODE(helper_info.st_mode) != 0o555 or helper_info.st_nlink != 1 or digest(helper) != helper.parent.name):
        raise RuntimeError('protected fixture helper differs')
    lock = os.open(directory / 'controller.lock', os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        response = json.loads(run(['/usr/bin/sudo', '-n', '--', helper, 'maintenance-request', '--owner', str(OWNER)],
                                  json.dumps(request, separators=(',', ':')), timeout=600))
        target = response.get('payload', {})
        if (response.get('version') != 1 or response.get('action') != request['action'] or response.get('complete') is not True or
                target.get('id') != prepared['id'] or target.get('coreHash') != prepared['coreHash'] or
                target.get('signerHash') != prepared['signerHash'] or journal.read_bytes() != raw):
            raise RuntimeError('protected prepared target differs')
        interrupted = dict(prepared)
        interrupted['target'] = target
        writes = {'.pending-fixture-witness': raw,
                  '.pending-fixture-interrupted': (json.dumps(interrupted, separators=(',', ':')) + '\n').encode()}
        for name, contents in writes.items():
            fd = os.open(directory / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, 'wb') as f:
                f.write(contents)
                f.flush()
                os.fsync(f.fileno())
        fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
        if journal.read_bytes() != raw:
            raise RuntimeError('prepared fixture journal changed during interruption')
    finally:
        os.close(lock)
    result = json.loads(run(enrollment, timeout=600))
    if result['instanceId'] != original['instanceId'] or result['walletSigner'] or result['financialExecution']:
        raise RuntimeError('enrolled fixture identity differs')
    completed = json.loads(journal.read_text())
    if completed.get('id') != prepared['id'] or completed.get('phase') != 'complete':
        raise RuntimeError('original enrollment operation did not complete')
    for name, contents in writes.items():
        if (directory / name).read_bytes() != contents:
            raise RuntimeError('prepared fixture witness changed')
    require_launcher(HOME / '.local/bin/fased', ROOT / 'fased-app')
    health(origin)
    backup = WORK / 'wallet-backup.json'
    wallet = json.loads(run([binary, 'custody', 'setup', '--disposable-fixture', '--password-stdin', '--backup-file', backup],
                            (WALLET_PASSWORD + '\n') * 4, timeout=180))
    status = json.loads(run([ROOT / 'fased-signer', 'owner', '--config', ROOT / 'identity.json', '--disposable-fixture']))
    if not wallet['backupVerified'] or not status['locked'] or not status['recoveryRequired'] or status['walletSigner']:
        raise RuntimeError('locked fixture wallet and backup required')
    session = login(origin)
    view = api(origin, '/api/v1/accounts', session=session)
    view = api(origin, '/api/v1/accounts/setup', 'PUT', {'revision': view['record']['revision'], 'action': 'attach',
                'name': 'Disposable wallet', 'network': 'devnet', 'reference': status['account']}, session)
    account = next(a for a in view['record']['accounts'] if a.get('signer', {}).get('epoch') == status['account']['epoch'])
    view = api(origin, '/api/v1/accounts/rpc', 'PUT', {'revision': view['record']['revision'], 'accountId': account['id'],
                'network': 'devnet', 'endpoint': 'https://devnet.helius-rpc.com'}, session)
    credential = api(origin, '/api/v1/accounts/rpc-credential', 'PUT', {'revision': view['record']['revision'], 'accountId': account['id'],
                     'action': 'save', 'key': RPC_KEY, 'passphrase': RPC_PASSWORD}, session)
    if not credential['saved'] or not credential['locked'] or credential['liveRPC']:
        raise RuntimeError('locked fixture RPC credential required')
    accounts = api(origin, '/api/v1/accounts', session=session)
    wallet_read_parity(origin, session, account['id'], accounts['record']['revision'], credential['ref'])
    record_hash = hashlib.sha256(json.dumps(accounts['record'], sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    journal = json.loads((P('/var/lib/fased-maintenance/owners/1100') / 'custody.json').read_text())
    if journal['phase'] != 'idle' or journal['package']['sequence'] != '8':
        raise RuntimeError('authenticated initial maintenance journal required')
    save('state.json', {'instanceId': original['instanceId'], 'taskId': task_id, 'accountId': account['id'],
                        'signerAccount': status['account'], 'recoveryRequired': status['recoveryRequired'],
                        'admissionHeld': status['admissionHeld'], 'walletBackupSha256': digest(backup), 'port': port,
                        'initialOperation': journal['operationId'], 'accountsRecordSha256': record_hash})
    print(json.dumps({'result': 'READY', 'fixtureOnly': True, 'instanceId': original['instanceId'], 'port': port,
                      'enrollmentRecovery': {'operationId': prepared['id'], 'simulated': True, 'pendingRecords': 2,
                                             'protectedTargetMatched': True}}))


def wallet_read_parity(origin, session, account_id, revision, credential_ref):
    read = {'accountId': account_id, 'revision': revision, 'ref': credential_ref, 'passphrase': RPC_PASSWORD}
    observed = api(origin, '/api/v1/accounts/read', 'POST', read, session)
    cli = json.loads(run([HOME / '.local/bin/fased', 'accounts', '--read-stdin'], json.dumps(read)))
    for key in ('accountId', 'revision', 'binding', 'credentialRef', 'address', 'network', 'lamports', 'tokens', 'activity'):
        if key not in observed or key not in cli or observed[key] != cli[key]:
            raise RuntimeError('HTTP/CLI wallet read differs: ' + key)
    if (observed['accountId'] != account_id or observed['revision'] != revision or observed['credentialRef'] != credential_ref or
            any(x.get('fixtureOnly') is not True or x.get('liveRPC') is not False for x in (observed, cli))):
        raise RuntimeError('unexpected live RPC capability')


def matching_held_update(j, state, trust):
    installation = j.get('installation') or {}
    binding = installation.get('binding') or {}
    return (j.get('operationId') != state['initialOperation'] and
            re.fullmatch(r'[a-f0-9]{32}', j.get('operationId', '')) is not None and
            installation.get('phase') in ('active', 'healthy-held', 'committed-held') and
            j.get('phase') not in ('idle', 'completed', 'refused', 'signer-completed') and
            re.fullmatch(r'[a-f0-9]{64}', installation.get('digest', '')) is not None and
            binding.get('targetCore') == trust['targetSha256'] and
            binding.get('targetSigner') == trust['targetSignerSha256'])


def require_stopped_signer(status, signer_uid):
    uid_lines = [line for line in status.splitlines() if line.startswith('Uid:')]
    if len(status) > 65536 or len(uid_lines) != 1 or uid_lines[0].split()[1:] != [str(signer_uid)] * 4:
        raise RuntimeError('fixture signer process identity differs')
    states = [line.split() for line in status.splitlines() if line.startswith('State:')]
    if len(states) != 1 or len(states[0]) < 2 or states[0][1] != 'T':
        raise BlockingIOError('fixture signer has not stopped after its durable marker')


def parked_signer(j, trust):
    identity = json.loads((ROOT / 'identity.json').read_text())
    signer_uid = identity['signerUid']
    marker_path = ROOT / 'signer-ipc' / ('fixture-cut-' + j['operationId'] + '.json')
    info = marker_path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != signer_uid or stat.S_IMODE(info.st_mode) != 0o644 or info.st_size > 4096:
        raise RuntimeError('foreign fixture cut marker')
    if info.st_nlink != 1:
        raise BlockingIOError('fixture cut marker is still being published')
    lines = marker_path.read_bytes().splitlines()
    if len(lines) != 2 or lines[1] != b'READY':
        raise BlockingIOError('fixture cut marker is not durable and ready')
    marker = json.loads(lines[0])
    boot = P('/proc/sys/kernel/random/boot_id').read_text().strip()
    if (marker.get('version') != 1 or marker.get('operationId') != j['operationId'] or
            marker.get('installationDigest') != j['installation']['digest'] or marker.get('bootId') != boot or
            marker.get('targetCore') != trust['targetSha256'] or marker.get('targetSigner') != trust['targetSignerSha256'] or
            not isinstance(marker.get('pid'), int) or marker['pid'] <= 1):
        raise RuntimeError('fixture cut marker does not match the held operation')
    proc = P('/proc') / str(marker['pid'])
    status = (proc / 'status').read_text()
    require_stopped_signer(status, signer_uid)
    argv = (proc / 'cmdline').read_bytes().split(b'\0')
    if b'custody-maintenance-status' not in argv or j['operationId'].encode() not in argv:
        raise RuntimeError('fixture signer is not waiting on the held operation')
    return marker


def watch():
    states = [path for path in (WORK / 'state.json', WORK / 'remote-state.json') if path.is_file()]
    if len(states) != 1:
        raise RuntimeError('one original enrolled fixture required')
    state = json.loads(states[0].read_text())
    trust = json.loads((TRUST / 'fixture.json').read_text())
    path = P('/var/lib/fased-maintenance/owners/1100/custody.json')
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        try:
            data = path.read_bytes()
            j = json.loads(data)
            installation = j.get('installation') or {}
            if matching_held_update(j, state, trust):
                marker = parked_signer(j, trust)
                print(json.dumps({'result': 'HOLD_OBSERVED', 'operationId': j['operationId'], 'phase': j['phase'],
                                  'installationPhase': installation['phase'], 'installationDigest': installation['digest'],
                                  'signerPid': marker['pid'], 'bootId': marker['bootId'],
                                  'journalSha256': hashlib.sha256(data).hexdigest()}), flush=True)
                return
        except (OSError, ValueError, KeyError):
            pass
        time.sleep(0.02)
    raise RuntimeError('no durable signer-held update boundary observed; do not retry')


def verify(operation):
    state = json.loads((WORK / 'state.json').read_text())
    trust = json.loads((TRUST / 'fixture.json').read_text())
    origin = 'http://127.0.0.1:' + str(state['port'])
    current = health(origin, 180)
    if current['instanceId'] != state['instanceId']:
        raise RuntimeError('instance identity changed after restart')
    session = login(origin)
    view = api(origin, '/api/v1/agents', session=session)
    if not any(t['id'] == state['taskId'] for t in view['tasks']):
        raise RuntimeError('original task missing after restart')
    accounts = api(origin, '/api/v1/accounts', session=session)
    if not any(a['id'] == state['accountId'] for a in accounts['record']['accounts']):
        raise RuntimeError('original account missing after restart')
    record_hash = hashlib.sha256(json.dumps(accounts['record'], sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    if record_hash != state['accountsRecordSha256']:
        raise RuntimeError('original account and RPC configuration changed')
    credential = api(origin, '/api/v1/accounts/rpc-credential', 'PUT', {'accountId': state['accountId'],
                     'revision': accounts['record']['revision'], 'action': 'status', 'ref': ''}, session)
    if not credential['saved'] or not credential['locked'] or credential['stale']:
        raise RuntimeError('locked original RPC credential unavailable')
    wallet_read_parity(origin, session, state['accountId'], accounts['record']['revision'], credential['ref'])
    status = json.loads(run([ROOT / 'fased-signer', 'owner', '--config', ROOT / 'identity.json', '--disposable-fixture']))
    if not status['locked'] or status['account'] != state['signerAccount'] or status['recoveryRequired'] != state['recoveryRequired'] or status['admissionHeld'] != state['admissionHeld'] or digest(WORK / 'wallet-backup.json') != state['walletBackupSha256']:
        raise RuntimeError('wallet identity, lock or original backup changed')
    j = json.loads((P('/var/lib/fased-maintenance/owners/1100') / 'custody.json').read_text())
    if j['operationId'] != operation or j['phase'] != 'completed' or j.get('start') != 'target' or j['package']['sequence'] != '9':
        raise RuntimeError('original protected update is not complete')
    if digest(ROOT / 'fased-app') != trust['targetSha256'] or digest(ROOT / 'fased-signer') != trust['targetSignerSha256']:
        raise RuntimeError('installed core/signer differ from the target fixture')
    require_launcher(HOME / '.local/bin/fased', ROOT / 'fased-app')
    result = run([HOME / '.local/bin/fased', 'update'], data='', timeout=180)
    if 'Already current' not in result:
        raise RuntimeError('installed CLI did not report Already current')
    print(json.dumps({'result': 'PASS', 'fixtureOnly': True, 'instanceId': state['instanceId'],
                      'operationId': j['operationId'], 'lockedWallet': True, 'backupPreserved': True,
                      'taskPreserved': True, 'accountPreserved': True, 'rpcPreserved': True,
                      'httpCliWalletRead': True, 'targetCoreSignerMatched': True, 'cliUsesTarget': True, 'cliAlreadyCurrent': True}))


def main():
    p = argparse.ArgumentParser()
    p.add_argument('mode', choices=['server', 'probe', 'setup', 'resume-claimed', 'remote-enroll', 'remote-verify', 'target', 'watch', 'inspect', 'verify'])
    p.add_argument('--port', type=int)
    p.add_argument('--operation')
    p.add_argument('--instance')
    a = p.parse_args()
    guard()
    if a.mode == 'server':
        server()
    elif a.mode == 'probe':
        probe()
    elif a.mode in ('setup', 'resume-claimed'):
        if a.port is None or not 1024 <= a.port <= 65535:
            raise RuntimeError('fixed private listener required')
        if a.mode == 'setup':
            setup(a.port)
        else:
            resume_claimed(a.port)
    elif a.mode == 'watch':
        watch()
    elif a.mode == 'target':
        target()
    elif a.mode == 'inspect':
        inspect()
    elif a.mode == 'remote-enroll':
        remote_enroll(a.instance or '')
    else:
        if not a.operation or not re.fullmatch(r'[a-f0-9]{32}', a.operation):
            raise RuntimeError('original cut operation required')
        if a.mode == 'remote-verify':
            remote_verify(a.operation)
        else:
            verify(a.operation)


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print('fixture:', str(e), file=sys.stderr)
        sys.exit(1)
