#!/usr/bin/env python3
"""One fresh Local custody/update fixture; never modifies an owner instance.

The VM is retained on failure and after Local success until the affected
Remote result has been recorded. Project code runs unprivileged except the
reviewed, hash-verified installed maintenance helper.
"""
import argparse
import hashlib
import io
import json
import mmap
import os
import pathlib
import re
import selectors
import signal
import shlex
import shutil
import socket
import stat
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request

P = pathlib.Path
REPO = P(__file__).resolve().parent.parent
EVIDENCE = REPO / '.evidence/b05-owner-window'
IMAGE = REPO / '.evidence/p4-fault-reboot/ubuntu.img'
IMAGE_SHA = 'b3296484e362ba2e713489a5de6225c719d9500962bde25d15f2f21e512c7658'


def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def command(args, *, timeout=120, input=None):
    return subprocess.run([str(x) for x in args], input=input, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=timeout, check=True)


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def owned_file(path, limit):
    s = path.lstat()
    if not stat.S_ISREG(s.st_mode) or s.st_uid != os.getuid() or s.st_nlink != 1 or s.st_size <= 0 or s.st_size > limit:
        raise RuntimeError('owned bounded fixture file required: ' + path.name)
    return path


def matching_source(helper, build, inputs):
    source = helper.get('declaredSourceCommit')
    return (isinstance(source, str) and re.fullmatch(r'[a-f0-9]{40}', source) is not None and
            source == build.get('declaredSourceCommit') == inputs.get('declaredSourceCommit'))


def fixture_failure_detail(stderr):
    labels = ('protected enrollment source validation refused', 'protected enrollment source frame refused',
              'enrollment source lease was lost', 'original verified enrollment package unavailable',
              'original verified core unavailable', 'original verified signer unavailable')
    allowed = '|'.join(re.escape(label) for label in labels)
    pattern = re.compile(r'fixture: (?:'
                         r'(fixture command failed: (?:fased-app|fased-signer|fased) exit [0-9]{1,3}(?:; (?:' + allowed + r'))?)|'
                         r'(fixture API /api/v1/(?:auth/(?:claim|login|state)|agents|accounts(?:/(?:setup|rpc|rpc-credential|read))?) HTTP [1-5][0-9]{2}))')
    for line in reversed(stderr[-8192:].decode(errors='replace').splitlines()):
        match = pattern.fullmatch(line)
        if match:
            return match.group(1) or match.group(2)
    return ''


def save_failure_stderr(work, stderr):
    """Retain bounded fixture diagnostics privately without replacing evidence."""
    name = 'acceptance-failure.stderr'
    try:
        fd = os.open(work / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as f:
            f.write(stderr[-32768:])
            f.flush()
            os.fsync(f.fileno())
        return name
    except OSError:
        return None


def resume_inputs(work, previous_result, receipt_pin, helper_hash):
    info = work.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise RuntimeError('original private VM directory required')
    previous = json.loads(owned_file(previous_result, 65536).read_text())
    if (previous.get('result') != 'FAIL' or previous.get('fixtureOnly') is not True or
            previous.get('sourceReceiptSha256') != receipt_pin or previous.get('helperSha256') != helper_hash or
            any(previous.get(flag) is not False for flag in ('mining', 'liveFeatures', 'automaticUpdates'))):
        raise RuntimeError('original failed Local fixture receipt required')
    setup = re.search(r'confined-custody-fixture\.py setup --port ([0-9]{4,5})(?:\x27|\s)', previous.get('error', ''))
    hosts = owned_file(work / 'known_hosts', 8192).read_text().splitlines()
    host_public = owned_file(work / 'host-key.pub', 8192).read_text().split()[:2]
    if len(hosts) != 1 or len(host_public) != 2:
        raise RuntimeError('original host identity required')
    host = re.fullmatch(r'\[127\.0\.0\.1\]:([0-9]{4,5}) (\S+) (\S+)', hosts[0])
    app_port = previous.get('appPort')
    if app_port is None and setup:
        app_port = int(setup.group(1))
    if (not host or [host.group(2), host.group(3)] != host_public or
            isinstance(app_port, bool) or not isinstance(app_port, int)):
        raise RuntimeError('original fixture listener or host identity differs')
    ssh_port = int(host.group(1))
    if not (1024 <= ssh_port <= 65535 and 1024 <= app_port <= 65535) or ssh_port == app_port:
        raise RuntimeError('original fixture ports are invalid')
    for name, limit in (('disk.qcow2', 16 << 30), ('seed.iso', 256 << 20), ('owner-key', 8192), ('owner-key.pub', 8192)):
        owned_file(work / name, limit)
    owner_key = work / 'owner-key'
    if stat.S_IMODE(owner_key.stat().st_mode) != 0o600:
        raise RuntimeError('original owner key is not private')
    derived = command(['ssh-keygen', '-y', '-f', owner_key]).stdout.decode().split()[:2]
    if derived != (work / 'owner-key.pub').read_text().split()[:2]:
        raise RuntimeError('original owner key differs')
    snapshot = owned_file(work / 'offline-inspection/partition.raw', 16 << 30)
    if stat.S_IMODE(snapshot.stat().st_mode) != 0o600:
        raise RuntimeError('private original filesystem witness required')
    original_host = command(['debugfs', '-R', 'cat /etc/ssh/ssh_host_ed25519_key.pub', snapshot]).stdout.decode().split()[:2]
    machine = command(['debugfs', '-R', 'cat /etc/machine-id', snapshot]).stdout.decode().strip()
    if original_host != host_public or not re.fullmatch(r'[a-f0-9]{32}', machine):
        raise RuntimeError('original VM filesystem identity differs')
    chain = json.loads(command(['qemu-img', 'info', '--output=json', '--backing-chain', work / 'disk.qcow2']).stdout)
    if (len(chain) != 2 or chain[0].get('format') != 'qcow2' or chain[0].get('virtual-size') != 12 << 30 or
            chain[0].get('full-backing-filename') != str(IMAGE.resolve()) or chain[0].get('dirty-flag') or
            chain[1].get('format') != 'qcow2' or P(chain[1].get('filename', '')).resolve() != IMAGE.resolve() or
            chain[1].get('backing-filename')):
        raise RuntimeError('original VM backing chain differs')
    return ssh_port, app_port, machine


def preflight(directory, helper_pin):
    if os.getuid() <= 0 or os.getuid() != os.geteuid() or os.environ.get('FASED_DISPOSABLE_VM_ACCEPTANCE') != '1':
        raise RuntimeError('ordinary owner and explicit disposable VM flag required')
    if '..' in directory.parts:
        raise RuntimeError('fixture path cannot contain parent traversal')
    directory = directory.absolute()
    if not directory.is_relative_to(EVIDENCE) or directory == EVIDENCE:
        raise RuntimeError('fixture must remain under the existing evidence root')
    for item in (directory, *directory.parents):
        if item == REPO:
            break
        if item.is_symlink():
            raise RuntimeError('fixture path cannot contain symlinks')
    s = directory.stat()
    if s.st_uid != os.getuid() or stat.S_IMODE(s.st_mode) != 0o700:
        raise RuntimeError('private fixture directory required')
    if not re.fullmatch(r'[a-f0-9]{64}', helper_pin):
        raise RuntimeError('reviewed helper receipt pin required')
    receipt_path = owned_file(directory / 'helper-receipt.json', 65536)
    if sha(receipt_path) != helper_pin:
        raise RuntimeError('helper receipt changed')
    helper = json.loads(receipt_path.read_text())
    build = json.loads(owned_file(directory / 'build-receipt.json', 65536).read_text())
    inputs = json.loads(owned_file(directory / 'input-receipt.json', 65536).read_text())
    trust = json.loads(owned_file(directory / 'fixture.json', 65536).read_text())
    if helper.get('fixtureOnly') is not True or build.get('fixtureOnly') is not True or inputs.get('fixtureOnly') is not True or not trust.get('connected'):
        raise RuntimeError('explicit controlled fixture receipts required')
    if not matching_source(helper, build, inputs):
        raise RuntimeError('fixture source identities differ')
    if inputs.get('privateReleaseSigningKeysRetained') is not False or inputs.get('disposableTLSPrivateKeyRetained') is not True:
        raise RuntimeError('fixture key-retention scope differs')
    for name in ('fased-app-tests', 'fased-app-next', 'fased-signer', 'fased-signer-next'):
        path = owned_file(directory / name, 64 << 20)
        if sha(path) != build.get('artifacts', {}).get(name):
            raise RuntimeError('fixture build artifact changed: ' + name)
    for name, field in (('fased-signer', 'fased-signer'), ('fased-signer-next', 'fased-signer-next')):
        if inputs.get('signerHashes', {}).get(field) != build['artifacts'][name] or helper.get('signerHashes', {}).get(field) != build['artifacts'][name]:
            raise RuntimeError('fixture signer binding differs')
    for name, key in (('seed.json', 'seedSha256'), ('ca.pem', 'caSha256')):
        if sha(owned_file(directory / name, 65536)) != trust.get(key) or helper.get(key) != trust.get(key):
            raise RuntimeError('fixture trust input differs: ' + name)
    if trust.get('sourceSha256') != build['artifacts']['fased-app-tests'] or trust.get('targetSha256') != build['artifacts']['fased-app-next']:
        raise RuntimeError('fixture app image binding differs')
    if trust.get('targetSignerSha256') != build['artifacts']['fased-signer-next']:
        raise RuntimeError('fixture target signer binding differs')
    for label, name, version, sequence in (('base', 'fased-app-tests', '0.1.11', 8), ('target', 'fased-app-next', '0.1.12', 9)):
        release = inputs.get('releases', {}).get(label, {})
        if release.get('coreSha256') != build['artifacts'][name] or release.get('version') != version or release.get('sequence') != sequence:
            raise RuntimeError('fixture release differs: ' + label)
    seed = json.loads((directory / 'seed.json').read_text())
    if seed.get('version') != 1 or seed.get('purpose') != 'fased-app-first-install-development:v1' or seed.get('channel') != 'development' or seed.get('origin') != 'https://127.0.0.1:17449' or seed.get('developmentOnly') is not True:
        raise RuntimeError('loopback-only development channel required')
    if stat.S_IMODE(owned_file(directory / 'key.pem', 65536).stat().st_mode) != 0o600:
        raise RuntimeError('disposable TLS key must remain private')
    for name in ('repo-base', 'repo-target'):
        repo = directory / name
        info = repo.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise RuntimeError('private fixture repository required: ' + name)
    image = owned_file(directory / 'fased-install', 128 << 20)
    image_hash = sha(image)
    if image_hash != helper.get('helperSha256'):
        raise RuntimeError('protected helper bytes differ')
    with image.open('rb') as stream, mmap.mmap(stream.fileno(), 0, access=mmap.ACCESS_READ) as data:
        for name in ('fased-signer', 'fased-signer-next'):
            if data.find((directory / name).read_bytes()) < 0:
                raise RuntimeError('helper lacks compiled signer bytes: ' + name)
    if sha(IMAGE) != IMAGE_SHA:
        raise RuntimeError('recorded Ubuntu image differs')
    for tool in ('qemu-system-x86_64', 'qemu-img', 'xorriso', 'ssh', 'ssh-keygen', 'node'):
        if shutil.which(tool) is None:
            raise RuntimeError('required fixture OS tool unavailable: ' + tool)
    if not os.access('/dev/kvm', os.R_OK | os.W_OK):
        raise RuntimeError('KVM is unavailable to the ordinary VM runner')
    return directory, image_hash


def safe_files(bases):
    files, total, entries = [], 0, 0
    for base in bases:
        for path in base.rglob('*'):
            entries += 1
            if entries > 256:
                raise RuntimeError('fixture archive entry limit exceeded')
            if path.is_symlink():
                raise RuntimeError('fixture archive contains a symlink')
            if not path.is_dir():
                owned_file(path, 128 << 20)
                total += path.stat().st_size
                if total > 256 << 20:
                    raise RuntimeError('fixture archive size limit exceeded')
                files.append((base, path))
    return files


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--fixture-dir', type=P, required=True)
    p.add_argument('--helper-receipt-sha256', required=True)
    p.add_argument('--resume-claimed', action='store_true', help='resume the original pre-enrollment fixture')
    a = p.parse_args()
    directory, helper_hash = preflight(a.fixture_dir, a.helper_receipt_sha256)
    archive_files = safe_files((directory / 'repo-base', directory / 'repo-target'))
    work = directory / 'vm-owned'
    result_path = directory / ('local-resume-result.json' if a.resume_claimed else 'local-result.json')
    if result_path.exists():
        raise RuntimeError('existing acceptance result must be preserved')
    consumer_names = ('check-confined-custody-vm.py', 'confined-custody-fixture.py', 'confined-custody-browser.mjs',
                      'staged-package-guest.py', 'staged-acceptance-contract.json')
    consumers = {name: owned_file(REPO / 'scripts' / name, 128 << 10).read_bytes() for name in consumer_names}
    if a.resume_claimed:
        ssh_port, app_port, original_machine = resume_inputs(work, directory / 'local-result.json', a.helper_receipt_sha256, helper_hash)
    else:
        work.mkdir(mode=0o700)  # Exclusive: never replace an unknown disk.
    result = {'result': 'FAIL', 'fixtureOnly': True, 'sourceReceiptSha256': a.helper_receipt_sha256,
              'helperSha256': helper_hash, 'mining': False, 'liveFeatures': False, 'automaticUpdates': False,
              'consumerSha256': {name: hashlib.sha256(data).hexdigest() for name, data in consumers.items()}}
    vm = tunnel = watcher = browser = None
    serial = stderr = None
    try:
        key, hostkey = work / 'owner-key', work / 'host-key'
        protected = '/var/lib/fased-maintenance/images/' + helper_hash + '/fased-install'
        if not a.resume_claimed:
            for path in (key, hostkey):
                command(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', path])
            ssh_port, app_port = port(), port()
            while app_port == ssh_port:
                app_port = port()
            public = (work / 'owner-key.pub').read_text().strip()
            host_public = (work / 'host-key.pub').read_text().split()[:2]
            (work / 'known_hosts').write_text('[127.0.0.1]:' + str(ssh_port) + ' ' + ' '.join(host_public) + '\n')
            private_lines = '\n'.join('       ' + line for line in hostkey.read_text().splitlines())
            unit = ('[Unit]\nDescription=Fased disposable fixture bootstrap\nAfter=network-online.target\n'
                    '[Service]\nType=oneshot\nExecStartPre=/usr/bin/sha256sum -c /var/lib/fased-update-fixture/helper.sha256\n'
                    'ExecStart=' + protected + ' maintenance-register --owner 1100 --profile local\n'
                    'ExecStart=' + protected + ' maintenance-bootstrap --owner 1100\nRemainAfterExit=yes\n')
            user_data = ("#cloud-config\nssh_pwauth: false\ndisable_root: true\nusers:\n"
                         "  - name: fased-local\n    uid: 1100\n    shell: /bin/bash\n    lock_passwd: true\n"
                         "    ssh_authorized_keys:\n      - " + public + "\n"
                         "ssh_keys:\n  ed25519_private: |\n" + private_lines + "\n  ed25519_public: " + ' '.join(host_public) + "\n"
                         "packages: [sudo, python3, bubblewrap, apparmor, apparmor-utils, polkitd, dbus-user-session]\n"
                         "write_files:\n  - path: /etc/systemd/system/fased-fixture-bootstrap.service\n"
                         "    owner: root:root\n    permissions: '0644'\n    content: |\n" +
                         '\n'.join('      ' + line for line in unit.splitlines()) + "\n"
                         "runcmd:\n"
                         "  - [mkdir, -p, /mnt/fased-fixture]\n"
                         "  - [mount, -o, ro, /dev/disk/by-label/cidata, /mnt/fased-fixture]\n"
                         "  - [install, -d, -m, '0755', /var/lib/fased-update-fixture, /var/lib/fased-maintenance, /var/lib/fased-maintenance/images, /var/lib/fased-maintenance/images/" + helper_hash + "]\n"
                         "  - [install, -m, '0644', /mnt/fased-fixture/seed.json, /var/lib/fased-update-fixture/seed.json]\n"
                         "  - [install, -m, '0644', /mnt/fased-fixture/ca.pem, /var/lib/fased-update-fixture/ca.pem]\n"
                         "  - [install, -m, '0644', /mnt/fased-fixture/fixture.json, /var/lib/fased-update-fixture/fixture.json]\n"
                         "  - [install, -m, '0644', /mnt/fased-fixture/helper.sha256, /var/lib/fased-update-fixture/helper.sha256]\n"
                         "  - [install, -m, '0555', /mnt/fased-fixture/fased-install, " + protected + "]\n"
                         "  - [systemctl, daemon-reload]\n"
                         "  - [systemctl, start, fased-fixture-bootstrap.service]\n"
                         "  - [loginctl, enable-linger, '1100']\n"
                         "  - [umount, /mnt/fased-fixture]\n")
            (work / 'user-data').write_text(user_data)
            (work / 'meta-data').write_text('instance-id: fased-confined-disposable\nlocal-hostname: fased-fixture\n')
            (work / 'helper.sha256').write_text(helper_hash + '  ' + protected + '\n')
            os.chmod(work / 'user-data', 0o600)
            iso = work / 'seed.iso'
            command(['xorriso', '-as', 'mkisofs', '-quiet', '-r', '-J', '-V', 'cidata', '-o', iso,
                     work / 'user-data', work / 'meta-data', work / 'helper.sha256', directory / 'fased-install',
                     directory / 'seed.json', directory / 'ca.pem', directory / 'fixture.json'])
            disk = work / 'disk.qcow2'
            command(['qemu-img', 'create', '-q', '-f', 'qcow2', '-F', 'qcow2', '-b', IMAGE, disk])
            command(['qemu-img', 'resize', disk, '12G'])
        else:
            iso, disk = work / 'seed.iso', work / 'disk.qcow2'
        result['appPort'] = app_port
        if a.resume_claimed:
            serial = os.fdopen(os.open(work / 'serial-resume.log', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb')
            stderr = os.fdopen(os.open(work / 'qemu-resume.stderr', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb')
        else:
            serial = (work / 'serial.log').open('ab')
            stderr = (work / 'qemu.stderr').open('ab')
        qemu = ['qemu-system-x86_64', '-machine', 'q35,accel=kvm', '-cpu', 'host', '-smp', '2', '-m', '2048',
                '-nographic', '-monitor', 'none', '-drive', f'file={disk},if=virtio,format=qcow2',
                '-drive', f'file={iso},if=virtio,format=raw,readonly=on', '-netdev',
                f'user,id=n,hostfwd=tcp:127.0.0.1:{ssh_port}-:22', '-device', 'virtio-net-pci,netdev=n',
                '-device', 'virtio-rng-pci', '-smbios', 'type=1,serial=ds=nocloud']
        ssh = ['ssh', '-F', '/dev/null', '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
               '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(work / 'known_hosts'),
               '-o', 'ConnectTimeout=4', '-i', str(key), '-p', str(ssh_port), 'fased-local@127.0.0.1']

        def start():
            return subprocess.Popen(qemu, stdout=serial, stderr=stderr)

        def remote(args, timeout=180, stdin=None):
            return command(ssh + [' '.join(shlex.quote(str(x)) for x in args)], timeout=timeout, input=stdin)

        def ready(process, first_boot):
            end, notice = time.monotonic() + 420, 0
            while time.monotonic() < end:
                if process.poll() is not None:
                    raise RuntimeError('disposable Ubuntu VM exited during startup')
                try:
                    out = remote(['/usr/bin/test', '-f', '/var/lib/cloud/instance/boot-finished'], timeout=8)
                    if out.returncode == 0:
                        if first_boot:
                            remote(['/usr/bin/systemctl', 'is-active', '--quiet', 'fased-fixture-bootstrap.service'], timeout=8)
                        else:
                            remote(['/usr/bin/test', '-x', protected], timeout=8)
                        return
                except (subprocess.SubprocessError, OSError):
                    pass
                if time.monotonic() >= notice:
                    print('Waiting for disposable Ubuntu and protected helper setup…', flush=True)
                    notice = time.monotonic() + 30
                time.sleep(2)
            raise RuntimeError('Ubuntu/helper readiness deadline; preserve this VM')

        vm = start()
        ready(vm, not a.resume_claimed)
        guest_script = 'custody-fixture/confined-custody-fixture.py'
        if not a.resume_claimed:
            tarpath = work / 'guest-inputs.tar'
            with tarfile.open(tarpath, 'w') as archive:
                for base, path in archive_files:
                    archive.add(path, arcname=str(P(base.name) / path.relative_to(base)), recursive=False)
                for name in ('ca.pem', 'key.pem', 'fased-signer'):
                    archive.add(owned_file(directory / name, 64 << 20), arcname=name, recursive=False)
                for name in ('confined-custody-fixture.py', 'staged-package-guest.py', 'staged-acceptance-contract.json'):
                    info = tarfile.TarInfo(name)
                    info.mode, info.size = 0o600, len(consumers[name])
                    archive.addfile(info, io.BytesIO(consumers[name]))
                info = tarfile.TarInfo('helper-path')
                contents = (protected + '\n').encode()
                info.mode, info.size = 0o600, len(contents)
                archive.addfile(info, io.BytesIO(contents))
            remote(['/usr/bin/mkdir', '-m', '0700', 'custody-fixture'])
            with tarpath.open('rb') as f:
                subprocess.run(ssh + ['tar -xf - -C custody-fixture'], stdin=f, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, timeout=180, check=True)
            remote(['/usr/bin/python3', '-m', 'py_compile', 'custody-fixture/confined-custody-fixture.py'])
        else:
            machine = remote(['/usr/bin/cat', '/etc/machine-id'], timeout=15).stdout.decode().strip()
            if machine != original_machine:
                raise RuntimeError('running VM does not match original filesystem witness')
            guest_script = 'custody-fixture/confined-custody-fixture-resume.py'
            script = consumers['confined-custody-fixture.py']
            if len(script) > 128 << 10:
                raise RuntimeError('fixture resume consumer is too large')
            transfer = ("import os,stat,sys; data=sys.stdin.buffer.read(131073); assert len(data)<=131072; "
                        "d=os.open('custody-fixture',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW); "
                        "i=os.fstat(d); assert i.st_uid==os.getuid() and stat.S_IMODE(i.st_mode)==0o700; "
                        "f=os.open('confined-custody-fixture-resume.py',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=d); "
                        "s=os.fdopen(f,'wb'); s.write(data); s.flush(); os.fsync(s.fileno()); s.close(); os.close(d)")
            remote(['/usr/bin/python3', '-c', transfer], stdin=script)
            actual = remote(['/usr/bin/sha256sum', guest_script]).stdout.decode().split()[0]
            if actual != hashlib.sha256(script).hexdigest():
                raise RuntimeError('fixture resume consumer differs after transfer')
            actual_helper = remote(['/usr/bin/sha256sum', protected], timeout=15).stdout.decode().split()[0]
            if actual_helper != helper_hash:
                raise RuntimeError('original protected helper differs')
        for name in ('staged-package-guest.py', 'staged-acceptance-contract.json'):
            actual = remote(['/usr/bin/sha256sum', 'custody-fixture/' + name], timeout=15).stdout.decode().split()[0]
            if actual != result['consumerSha256'][name]:
                raise RuntimeError('fixture setup consumer differs: ' + name)
        server_output = '>/dev/null' if a.resume_claimed else '>>custody-fixture/https.log'
        command(ssh + ['nohup /usr/bin/python3 ' + shlex.quote(guest_script) + ' server ' + server_output + ' 2>&1 </dev/null &'])
        remote(['/usr/bin/python3', guest_script, 'probe'], timeout=30)
        print('Installing and enrolling the disposable Local fixture…', flush=True)
        ready_data = json.loads(remote(['/usr/bin/python3', guest_script, 'resume-claimed' if a.resume_claimed else 'setup', '--port', app_port], timeout=900).stdout)
        if ready_data.get('result') != 'READY' or ready_data.get('fixtureOnly') is not True:
            raise RuntimeError('Local enrollment did not reach its real entry')
        recovered = ready_data.get('enrollmentRecovery', {})
        if (recovered.get('simulated') is not True or recovered.get('pendingRecords') != 2 or
                recovered.get('protectedTargetMatched') is not True or not re.fullmatch(r'[a-f0-9]{32}', recovered.get('operationId', ''))):
            raise RuntimeError('same-operation prepared recovery was not observed')
        result['enrollmentRecovery'] = recovered
        result['instanceId'] = ready_data['instanceId']
        result['beforeBoot'] = remote(['/usr/bin/cat', '/proc/sys/kernel/random/boot_id']).stdout.decode().strip()
        remote(['/usr/bin/python3', guest_script, 'target'])
        tunnel = subprocess.Popen(ssh[:-1] + ['-o', 'ExitOnForwardFailure=yes', '-N', '-L', f'127.0.0.1:{app_port}:127.0.0.1:{app_port}', ssh[-1]], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        tunnel_deadline = time.monotonic() + 20
        while time.monotonic() < tunnel_deadline:
            if tunnel.poll() is not None:
                raise RuntimeError('fixture tunnel could not bind its private listener')
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{app_port}/api/v1/health', timeout=2) as response:
                    identity = json.load(response)
                if identity.get('instanceId') != result['instanceId']:
                    raise RuntimeError('fixture tunnel reached a different instance')
                break
            except urllib.error.URLError as e:
                if not isinstance(e.reason, (ConnectionRefusedError, TimeoutError)):
                    raise
                time.sleep(0.2)
        else:
            raise RuntimeError('fixture tunnel did not become ready')
        watcher = subprocess.Popen(ssh + ['/usr/bin/python3 ' + shlex.quote(guest_script) + ' watch'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        browser_script = work / 'browser-consumer.mjs'
        fd = os.open(browser_script, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as f:
            f.write(consumers['confined-custody-browser.mjs'])
            f.flush()
            os.fsync(f.fileno())
        browser = subprocess.Popen(['node', str(browser_script), str(app_port), result['instanceId']],
                                   cwd=REPO, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        browser_out, _ = browser.communicate(timeout=180)
        accepted = json.loads(browser_out) if browser.returncode == 0 else {}
        if accepted.get('result') != 'APPLY_SENT' or not re.fullmatch(r'[a-f0-9]{32}', accepted.get('operationId', '')):
            raise RuntimeError('browser did not confirm the original Update request')
        browser = None
        selection = selectors.DefaultSelector()
        selection.register(watcher.stdout, selectors.EVENT_READ)
        deadline, notice = time.monotonic() + 300, 0
        cut = None
        while time.monotonic() < deadline:
            if watcher.poll() not in (None, 0):
                raise RuntimeError('fixture did not observe an update hold; preserve this VM')
            if selection.select(0.2):
                line = watcher.stdout.readline()
                if line:
                    cut = json.loads(line)
                    break
            if time.monotonic() >= notice:
                print('Waiting for the original update hold…', flush=True)
                notice = time.monotonic() + 30
        if not cut or cut.get('result') != 'HOLD_OBSERVED' or cut.get('operationId') != accepted['operationId']:
            raise RuntimeError('no durable update hold observed; no retry sent')
        if cut.get('bootId') != result['beforeBoot'] or not isinstance(cut.get('signerPid'), int) or cut['signerPid'] <= 1:
            raise RuntimeError('controlled signer cut identity differs')
        # The test signer withholds the real held-status response until power
        # loss. Freeze the whole VM before killing it so no guest can advance.
        vm.send_signal(signal.SIGSTOP)
        stopped_deadline = time.monotonic() + 5
        while time.monotonic() < stopped_deadline:
            if '\nState:\tT' in (P('/proc') / str(vm.pid) / 'status').read_text():
                break
            time.sleep(0.02)
        else:
            raise RuntimeError('disposable VM did not freeze at the controlled cut')
        vm.kill()
        vm.wait(timeout=20)
        result['cut'] = cut
        if tunnel.poll() is None:
            tunnel.terminate()
            tunnel.wait(timeout=10)
        tunnel = None
        vm = start()
        ready(vm, False)
        result['afterBoot'] = remote(['/usr/bin/cat', '/proc/sys/kernel/random/boot_id']).stdout.decode().strip()
        if result['beforeBoot'] == result['afterBoot']:
            raise RuntimeError('new Ubuntu boot was not observed')
        command(ssh + ['nohup /usr/bin/python3 ' + shlex.quote(guest_script) + ' server ' + server_output + ' 2>&1 </dev/null &'])
        remote(['/usr/bin/python3', guest_script, 'probe'], timeout=30)
        end, notice = time.monotonic() + 300, 0
        while time.monotonic() < end:
            observed = json.loads(remote(['/usr/bin/python3', guest_script, 'inspect'], timeout=15).stdout)
            if observed['operationId'] != cut['operationId']:
                raise RuntimeError('original update identity changed after reboot')
            if observed['phase'] == 'completed':
                break
            if observed['phase'] in ('refused', 'rolled-back'):
                raise RuntimeError('original update did not complete: ' + observed['phase'])
            if time.monotonic() >= notice:
                print('Waiting for the original update recovery…', flush=True)
                notice = time.monotonic() + 30
            time.sleep(1)
        else:
            raise RuntimeError('original update remains held; no retry sent')
        proof = json.loads(remote(['/usr/bin/python3', guest_script, 'verify', '--operation', cut['operationId']], timeout=300).stdout)
        if proof.get('result') != 'PASS' or proof.get('operationId') != cut['operationId']:
            raise RuntimeError('Local recovery proof incomplete')
        result['proof'] = proof
        result['interruptionAtPowerLoss'] = 'controlled signer status gate; no external process interference'
        result['result'] = 'PASS'
        print('Local controlled fixture update and reboot recovery passed.', flush=True)
    except BaseException as e:
        result['error'] = str(e)
        if isinstance(e, subprocess.CalledProcessError) and e.stderr:
            private_failure = save_failure_stderr(work, e.stderr)
            if private_failure:
                result['privateFailureStderr'] = private_failure
            detail = fixture_failure_detail(e.stderr)
            if detail:
                result['error'] = detail[:700]
                print('Disposable fixture: ' + result['error'], file=sys.stderr, flush=True)
        raise
    finally:
        for child in (browser, watcher, tunnel):
            if child is not None and child.poll() is None:
                child.terminate()
                child.wait(timeout=10)
        if vm is not None and vm.poll() is None:
            vm.terminate()
            vm.wait(timeout=20)
        if serial is not None:
            serial.close()
        if stderr is not None:
            stderr.close()
        # This directory is exclusively task-created. Keep its disk and input
        # evidence for the Local→Remote parent journey, even on failure.
        result['cleanup'] = 'VM stopped; owned disk and fixture retained until Remote acceptance and review'
        fd = os.open(result_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as f:
            json.dump(result, f, indent=2)
            f.write('\n')
            f.flush()
            os.fsync(f.fileno())


if __name__ == '__main__':
    main()
