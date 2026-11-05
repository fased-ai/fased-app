#!/usr/bin/env python3
"""Boot-capable disposable Ubuntu acceptance, never an owner-host install.
No project executable runs as root. Root in the guest executes OS tools only.
Evidence survives snapshot rollback; large reconstructible VM outputs are removed.
"""
import argparse, base64, selectors, hashlib, json, os, pathlib, shutil, signal, socket, subprocess, tarfile, time, urllib.request
P=pathlib.Path
repo=P.cwd(); root=repo/'.evidence/p4-fault-reboot'; root.mkdir(parents=True,exist_ok=True)
p=argparse.ArgumentParser(); p.add_argument('--fresh-remote-only',action='store_true'); p.add_argument('--local-acceptance',type=P); p.add_argument('--released-local-only',action='store_true'); p.add_argument('--full',action='store_true'); p.add_argument('--resume',action='store_true'); p.add_argument('--inspection-only',action='store_true'); p.add_argument('--keep-failed-vm',action='store_true'); p.add_argument('--managed-remote-only',action='store_true'); p.add_argument('--network-only',action='store_true'); p.add_argument('--owner-browser-only',action='store_true'); p.add_argument('--maintenance-only',action='store_true'); p.add_argument('--installed-remote-update-only',action='store_true'); p.add_argument('--installed-finalizer-update-only',action='store_true'); p.add_argument('--staged-package-only',action='store_true'); p.add_argument('--candidate',type=P); p.add_argument('--predecessor',type=P); p.add_argument('--legacy-predecessor',type=P); a=p.parse_args()
if a.fresh_remote_only:
 assert a.staged_package_only and not a.released_local_only, 'fresh Remote requires its own staged package VM'
 assert a.candidate and a.local_acceptance, 'exact candidate and completed Local acceptance required'
 import importlib.util
 spec=importlib.util.spec_from_file_location('fased_staged_prerequisite',repo/'scripts/staged-package-guest.py')
 prerequisite=importlib.util.module_from_spec(spec);spec.loader.exec_module(prerequisite)
 local_acceptance_sha=prerequisite.require_local_acceptance(prerequisite.package(a.candidate),a.local_acceptance)
 # Validate before downloads, VM creation, or any remote side effect. Only the
 # candidate is needed: a fresh setup cannot masquerade as a version upgrade.
 a.predecessor=a.candidate;a.legacy_predecessor=a.candidate
if a.released_local_only:
 assert a.staged_package_only, 'released Local proof uses the existing staged package runner'
if a.installed_finalizer_update_only:
 assert not a.installed_remote_update_only,'choose one installed update fixture profile'
 a.installed_remote_update_only=True
assert os.geteuid()>0 and os.environ.get('FASED_DISPOSABLE_VM_ACCEPTANCE')=='1'
assert not a.managed_remote_only or not (a.resume or a.full or a.inspection_only),'managed Remote acceptance requires a fresh single-case VM'
assert not a.network_only or not (a.resume or a.full or a.inspection_only or a.managed_remote_only),'network acceptance requires a fresh single-case VM'
assert not a.owner_browser_only or not (a.resume or a.full or a.inspection_only or a.managed_remote_only or a.network_only),'owner browser acceptance requires a fresh single-case VM'
assert not a.maintenance_only or not (a.resume or a.full or a.inspection_only or a.managed_remote_only or a.network_only or a.owner_browser_only),'maintenance authority requires one fresh disposable VM'
assert not a.installed_remote_update_only or not (a.resume or a.full or a.inspection_only or a.managed_remote_only or a.network_only or a.owner_browser_only or a.maintenance_only),'installed Remote update requires one fresh disposable VM'
single_case=a.staged_package_only or a.managed_remote_only or a.network_only or a.owner_browser_only or a.maintenance_only or a.installed_remote_update_only
if a.staged_package_only:
 assert not any([a.full,(a.resume and not a.fresh_remote_only),a.inspection_only,a.managed_remote_only,a.network_only,a.owner_browser_only,a.maintenance_only,a.installed_remote_update_only]),'one staged package acceptance mode required'
 assert a.candidate and a.predecessor and a.legacy_predecessor,'exact retained package directories required'
 root=repo/('.evidence/b01-staged-installed/fresh-remote' if a.fresh_remote_only else '.evidence/b01-staged-installed/local' if a.released_local_only else '.evidence/b01-staged-installed/vm'); root.mkdir(parents=True,exist_ok=True)
 assert os.path.exists('/dev/kvm') and os.access('/dev/kvm',os.R_OK|os.W_OK),'KVM staged acceptance prerequisite unavailable'
if a.maintenance_only:
 root=repo/'.evidence/b01-connected-install/vm'; root.mkdir(parents=True,exist_ok=True)
 assert os.path.exists('/dev/kvm') and os.access('/dev/kvm',os.R_OK|os.W_OK),'KVM authority prerequisite unavailable; do not substitute prolonged TCG acceptance'
if a.installed_remote_update_only:
 root=repo/('.evidence/b01-finalized-update/vm' if a.installed_finalizer_update_only else '.evidence/b01-connected-install/installed-remote'); root.mkdir(parents=True,exist_ok=True)
 assert os.path.exists('/dev/kvm') and os.access('/dev/kvm',os.R_OK|os.W_OK),'KVM installed-update prerequisite unavailable; do not substitute prolonged TCG acceptance'
if a.network_only:
 root=repo/'.evidence/b01-network/vm'; root.mkdir(parents=True,exist_ok=True)
if a.owner_browser_only:
 root=repo/'.evidence/b01-owner-browser/vm'; root.mkdir(parents=True,exist_ok=True)
 with socket.socket() as check:check.bind(('127.0.0.1',17447))
# The reviewed official checksum is immutable for this fixture, not a production trust seed.
IMAGE_SHA='b3296484e362ba2e713489a5de6225c719d9500962bde25d15f2f21e512c7658'
URL='https://cloud-images.ubuntu.com/minimal/releases/noble/release-20261017/ubuntu-24.04-minimal-cloudimg-amd64.img'
NODE='24.10.0'
sha=lambda b:hashlib.sha256(b).hexdigest()
def save(n,o):
 target=root/n;temp=target.with_name(target.name+'.pending')
 temp.write_text(json.dumps(o,indent=2)+'\n');os.replace(temp,target)
def run(args,**kw): return subprocess.run(args,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,**kw)
def download(url,path):
 if not path.exists():
  with urllib.request.urlopen(url,timeout=45) as src,path.open('wb') as dst: shutil.copyfileobj(src,dst)
image=repo/'.evidence/p4-fault-reboot/ubuntu.img'; download(URL,image); assert sha(image.read_bytes())==IMAGE_SHA,'official Ubuntu image digest changed; review a new pinned image'
if not single_case:
 nodearchive=root/f'node-v{NODE}-linux-x64.tar.xz'; download(f'https://nodejs.org/dist/v{NODE}/{nodearchive.name}',nodearchive)
 expected='2642f4428869aca32443660fd71b3918e2be1277a899bdcaeb64c93b54b5af17'
 assert sha(nodearchive.read_bytes())==expected
 node=root/'node';
 with tarfile.open(nodearchive) as arc:
  with arc.extractfile(f'node-v{NODE}-linux-x64/bin/node') as src,node.open('wb') as dst: shutil.copyfileobj(src,dst)
 node.chmod(0o700)
work=root/'vm-owned'; work.mkdir(mode=0o700,exist_ok=a.resume)
assert not work.is_symlink() and work.stat().st_uid==os.getuid() and (work.stat().st_mode & 0o777)==0o700
assert a.resume or not any(work.iterdir()),'preserve existing VM work; inspect the prior attempt before reusing'
key=work/'ssh-key'
if not a.resume:
 run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(key)])
 (work/'meta-data').write_text('instance-id: fased-disposable-p4\nlocal-hostname: fased-fixture\n')
 provider_credential_setup='/usr/bin/systemd-creds encrypt --with-key=host --name=fased-provider-task-key - -'
 provider_credential_rule='' if a.released_local_only else '       ubuntu ALL=(root) NOPASSWD: '+provider_credential_setup+'\n'
 (work/'user-data').write_text('''#cloud-config
 ssh_pwauth: false
 disable_root: true
 users:
   - name: ubuntu
     uid: 1000
     groups: [sudo]
     shell: /bin/bash
     lock_passwd: true
     ssh_authorized_keys:
       - '''+(work/'ssh-key.pub').read_text().strip()+'''
 write_files:
   - path: /etc/sudoers.d/99-fased-disposable-fixture
     owner: root:root
     permissions: '0440'
     content: |
       # Isolated VM only. No shells or project executables may run as root.
       Defaults:ubuntu verifypw=any
       Cmnd_Alias FASED_FIXTURE_OS = /usr/bin/install, /usr/bin/mkdir, /usr/bin/ln, /usr/bin/mv, /usr/bin/sync, /usr/bin/rm, /usr/bin/cat, /usr/bin/systemctl, /usr/sbin/useradd, /usr/sbin/userdel, /usr/sbin/apparmor_parser, /usr/sbin/nft, /usr/sbin/sshd, /usr/bin/cmp, /usr/bin/curl
       ubuntu ALL=(root) NOPASSWD: FASED_FIXTURE_OS
'''+provider_credential_rule+'''
       ubuntu ALL=(root) NOPASSWD: /usr/bin/passwd -S fased-vps-1000
       ubuntu ALL=(root) NOPASSWD: /usr/bin/flock --version
       ubuntu ALL=(root) NOPASSWD: /usr/bin/setpriv --version
       ubuntu ALL=(root) NOPASSWD: /usr/bin/setpriv ^--reuid [1-9][0-9]* --regid [1-9][0-9]* --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- .*$
 packages: [sudo, python3, bubblewrap, apparmor, apparmor-utils, nftables]
 ''')
 if a.network_only:
  (work/'user-data').write_text((work/'user-data').read_text().replace('nftables]','nftables, curl]'))
 if a.staged_package_only:
  data=(work/'user-data').read_text()
  users=''
  public=(work/'ssh-key.pub').read_text().strip()
  for name,uid in ([] if a.fresh_remote_only else [('fased-local',1100),('fased-fresh',1400)] if a.released_local_only else [('fased-local',1100),('fased-remote',1200),('fased-legacy',1300)]):
   users+='   - name: '+name+'\n     uid: '+str(uid)+'\n     groups: [sudo]\n     shell: /bin/bash\n     lock_passwd: true\n     ssh_authorized_keys:\n       - '+public+'\n'
  if not a.released_local_only:users+='   - name: root\n     lock_passwd: true\n     ssh_authorized_keys:\n       - '+public+'\n'
  data=data.replace(' disable_root: true',' disable_root: true' if a.released_local_only else ' disable_root: false').replace(' write_files:',users+' write_files:')
  extra=''
  utilities='/usr/bin/apt-get, /usr/bin/install, /usr/bin/mkdir, /usr/bin/ln, /usr/bin/mv, /usr/bin/sync, /usr/bin/rm, /usr/bin/cat, /usr/bin/cmp, /usr/bin/cp, /usr/bin/chmod, /usr/bin/chown, /usr/bin/find, /usr/bin/flock, /usr/bin/getent, /usr/bin/id, /usr/bin/passwd, /usr/bin/pkcheck, /usr/bin/setpriv, /usr/bin/stat, /usr/bin/systemctl, /usr/sbin/useradd, /usr/sbin/userdel, /usr/sbin/apparmor_parser, /usr/sbin/nft, /usr/sbin/ip, /usr/sbin/update-ca-certificates'
  for name in (['ubuntu','fased-local','fased-fresh'] if a.released_local_only else ['ubuntu','fased-local','fased-remote','fased-legacy']):
   extra+='       Defaults:'+name+' verifypw=any\n       '+name+' ALL=(root) NOPASSWD: '+utilities+'\n'
   if name in ('fased-remote','fased-legacy') and not a.released_local_only:
    extra+='       '+name+' ALL=(root) NOPASSWD: '+provider_credential_setup+'\n'
  if a.fresh_remote_only:
   extra+='       ubuntu ALL=(root) NOPASSWD: /usr/bin/tailscale status --json, /usr/bin/tailscale debug prefs, /usr/bin/tailscale login --timeout=120s, /usr/bin/tailscale set --operator=ubuntu, /usr/bin/tailscale logout\n'
  data=data.replace(' packages:',extra+' packages:').replace('nftables]', 'nftables, polkitd, curl, ca-certificates, openssl, iproute2]')
  (work/'user-data').write_text(data)
 if a.maintenance_only:
  # One fixed root OS switcher for the controlling ubuntu fixture. It drops to
  # the separate non-sudo maintainer before any project test code executes.
  data=(work/'user-data').read_text()
  fixed='       ubuntu ALL=(root) NOPASSWD: /usr/sbin/runuser ^-u fased-probe -- /usr/bin/env --ignore-environment --chdir=/home/fased-probe HOME=/home/fased-probe XDG_CONFIG_HOME=/home/fased-probe/.config PATH=/usr/bin:/bin FASED_DISPOSABLE_VM=1 FASED_MAINTENANCE_OWNER=1 /var/lib/fased-maintenance-probe-input/fased-app-tests -test[.]run=TestRemoteMaintenanceOwnerUbuntuVM -test[.]v$\n'
  (work/'user-data').write_text(data.replace(' packages:',fixed+' packages:'))
 if a.installed_remote_update_only:
  # Both controller actions drop to the one isolated owner before test code.
  # The controller removes that owner's temporary setup grant before launch.
  data=(work/'user-data').read_text()
  fixed=''
  for action in ['TestApplicationUpdateInstalledRemoteOwnerUbuntuVM','TestApplicationUpdateInstalledRemoteOwnerVerifyUbuntuVM']:
   fixed+='       ubuntu ALL=(root) NOPASSWD: /usr/sbin/runuser ^-u fased-probe -- /usr/bin/env --ignore-environment --chdir=/home/fased-probe HOME=/home/fased-probe XDG_CONFIG_HOME=/home/fased-probe/.config PATH=/usr/bin:/bin FASED_DISPOSABLE_VM=1 FASED_UPDATE_OWNER=1 /var/lib/fased-update-fixture/fased-app-tests -test[.]run='+action+' -test[.]v$\n'
  (work/'user-data').write_text(data.replace(' packages:',fixed+' packages:').replace('nftables]', 'nftables, polkitd]'))
 run(['xorriso','-as','mkisofs','-quiet','-r','-J','-V','cidata','-o',str(work/'seed.iso'),str(work/'user-data'),str(work/'meta-data')])
 run(['qemu-img','create','-q','-f','qcow2','-F','qcow2','-b',str(image),str(work/'disk.qcow2')])
 run(['qemu-img','resize',str(work/'disk.qcow2'),'12G'])
with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
kvm=os.path.exists('/dev/kvm') and os.access('/dev/kvm',os.R_OK|os.W_OK)
serial=(root/'vm-serial.log').open('ab' if a.fresh_remote_only and a.resume else 'wb');stderr=(root/'qemu.stderr').open('ab' if a.fresh_remote_only and a.resume else 'wb')
cmd=['qemu-system-x86_64','-machine','q35,accel='+('kvm' if kvm else 'tcg'),'-cpu','host' if kvm else 'max','-smp','2','-m','2048','-nographic','-monitor','none','-qmp',f'unix:{work}/qmp.sock,server=on,wait=off','-drive',f'file={work}/disk.qcow2,if=virtio,format=qcow2','-drive',f'file={work}/seed.iso,if=virtio,format=raw,readonly=on','-netdev',f'user,id=n,hostfwd=tcp:127.0.0.1:{port}-:22','-device','virtio-net-pci,netdev=n','-device','virtio-rng-pci','-smbios','type=1,serial=ds=nocloud']
if a.installed_remote_update_only or a.staged_package_only:
 # These modes reboot through the guest, never QMP. Do not leave an unused
 # monitor socket behind when the snapshot lifecycle terminates QEMU.
 i=cmd.index('-qmp'); del cmd[i:i+2]
vm=None;tunnel=None;vm_stderr_start=0
def start_vm():
 global vm,vm_stderr_start
 vm_stderr_start=stderr.tell()
 vm=subprocess.Popen(cmd,stdout=serial,stderr=stderr)
def stop_vm():
 if a.staged_package_only and tunnel is not None and tunnel.poll() is None:
  tunnel.terminate();tunnel.wait(10)
 if vm is not None and vm.poll() is None:
  vm.kill();vm.wait(10)
def disk_snapshot(name,restore=False):
 assert name in ['baseline','held-baseline','inspection-ready']
 stop_vm()
 if restore:
  run(['qemu-img','snapshot','-a',name,str(work/'disk.qcow2')])
 else:
  listing=run(['qemu-img','snapshot','-l',str(work/'disk.qcow2')]).stdout.decode()
  if name in listing:run(['qemu-img','snapshot','-d',name,str(work/'disk.qcow2')])
  run(['qemu-img','snapshot','-c',name,str(work/'disk.qcow2')])
if a.resume and not (a.inspection_only or a.fresh_remote_only):disk_snapshot('baseline',True)
start_vm()
def interrupted(signum, frame): raise KeyboardInterrupt("fixture runner interrupted")
signal.signal(signal.SIGTERM, interrupted)
sshbase=['ssh','-F','/dev/null','-o','BatchMode=yes','-o','ControlMaster=no','-o','ControlPath=none','-i',str(key),'-p',str(port),'-o','IdentitiesOnly=yes','-o','StrictHostKeyChecking=accept-new','-o',f'UserKnownHostsFile={work}/known_hosts','-o','ConnectTimeout=4','ubuntu@127.0.0.1']
def ssh(command,timeout=150): return run(sshbase+[command],timeout=timeout).stdout.decode()
def check_vm_running():
 code=vm.poll()
 if code is None:return
 with (root/'qemu.stderr').open('rb') as stream:
  stream.seek(0,2);stream.seek(max(vm_stderr_start,stream.tell()-8192));detail=stream.read().decode(errors='replace')
 if a.staged_package_only:
  try:
   device=os.stat('/dev/kvm');device={'uid':device.st_uid,'gid':device.st_gid,'mode':oct(device.st_mode & 0o777),'inode':device.st_ino}
  except OSError as error:device={'unavailable':True,'errno':error.errno}
  save('staged-vm-failure.json',{'returncode':code,'stderr':detail,'kvmDevice':device,'process':{'uid':os.geteuid(),'gid':os.getegid(),'groups':os.getgroups()}})
 raise RuntimeError('QEMU exited ('+str(code)+'): '+detail)
def ready(timeout=300):
 end=time.monotonic()+timeout
 while time.monotonic()<end:
  check_vm_running()
  try:
   if ssh('test -f /var/lib/cloud/instance/boot-finished && sudo -n -v && id -u',15).strip()=='1000': return
  except (subprocess.SubprocessError,OSError): pass
  time.sleep(2)
 raise RuntimeError('VM readiness deadline; inspect retained serial evidence')
def guest(mode,arg=''):
 assert mode in ['managed-remote','setup','cut','recover','rollback','observe','probes','clock','advance','configure','tamper','old-reader'] and (not arg or all(c.isalnum() or c in '-:' for c in arg))
 return ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 .evidence/bin/node scripts/fixture-vm-guest.mjs '+mode+' '+arg,240)
def receipt(name):
 data=ssh('cat /home/ubuntu/fased/.evidence/p4-fault-reboot/guest/'+name+'.json',30);return json.loads(data)
def capture_installed_update_failure():
 # Public transaction/OS diagnostics only. Never copy DB, provider ciphertext,
 # CLI sessions or owner leases into host logs. Keep the failed disk for those.
 paths={
  'controller':'/home/fased-probe/.config/fased-app-vps-installer/application-update.json',
  'maintenance':'/home/fased-probe/.config/fased-app-vps-installer/application-maintenance.json',
  'cut':'/var/lib/fased-app-vps-1100/app-state/update-fixture-cut.json',
  'resumed':'/var/lib/fased-app-vps-1100/app-state/update-fixture-resumed.json'}
 diagnostics={}
 for name,path in paths.items():
  try:
   r=subprocess.run(sshbase+['sudo -n -- /usr/bin/cat -- '+path],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=8)
   (root/('failure-'+name+'.json')).write_bytes(r.stdout)
   (root/('failure-'+name+'.stderr')).write_bytes(r.stderr)
   diagnostics[name]={'exitCode':r.returncode}
  except subprocess.SubprocessError as e:diagnostics[name]={'error':str(e)}
 try:
  r=subprocess.run(sshbase+['sudo -n -- /usr/bin/systemctl status --no-pager --full fased-vps-1100.service fased-vps-maintenance-1100@*.service'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=8)
  (root/'failure-services.log').write_bytes(r.stdout+r.stderr)
  diagnostics['services']={'exitCode':r.returncode}
 except subprocess.SubprocessError as e:diagnostics['services']={'error':str(e)}
 save('failure-diagnostics.json',diagnostics)
def check_inspection_faults():
  for role in ['owner','signer']:
   for case in ['regression','epoch','uncertain','equal-expiry','skew']:
    name=role+'-'+case
    if previous and previous.get('checks',{}).get(name,{}).get('result')=='PASS' and previous['inputs']['scripts/fixture-inspection-clock-probe.py']==report['inputs']['scripts/fixture-inspection-clock-probe.py']:
     continue
    print('VM independent time: '+name,flush=True)
    disk_snapshot('inspection-ready',True);start_vm();ready(90);guest('clock',name)
    report['checks'][name]=receipt('clock');save('vm-progress.json',report)
  for case in ['catalog-mode','catalog-owner','role-substitution','capability','config-pin','capsule-pin','seed-pin','source-index','source-subject','state-missing','anchor-corrupt','state-rollback','foreign-namespace','legacy-head']:
   print('VM sealed input/checkpoint fault: '+case,flush=True)
   disk_snapshot('inspection-ready',True);start_vm();ready(90);guest('tamper',case)
   report['checks'][case]=receipt('tamper');save('vm-progress.json',report)
def verify_public_ssh_blocked(label):
 command=sshbase[:-1]+['-vv',sshbase[-1],'true']
 try:
  probe=subprocess.run(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=4)
  code,output=probe.returncode,probe.stderr
 except subprocess.TimeoutExpired as e:
  code,output=255,e.stderr or b''
 (root/(label+'-public-ssh-probe.stderr')).write_bytes(output)
 assert code==255 and b'remote protocol version' not in output.lower(),'public SSH protocol was reachable: '+str(code)+' '+output[-1200:].decode(errors='replace')
def network_guest():
 command='cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config .evidence/bin/network-tests -test.run=^TestNetworkUbuntuVM$ -test.v'
 process=subprocess.Popen(sshbase+[command],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,bufsize=0)
 output=[];blocked=False;committed=None;deadline=time.monotonic()+220
 selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ)
 try:
  while time.monotonic()<deadline:
   events=selector.select(1)
   if not events:
    if process.poll() is not None:break
    continue
   line=process.stdout.readline()
   if not line:break
   output.append(line)
   if b'FASED_NETWORK_COMMITTED ' in line:
    committed=line.decode().strip().split()[-1]
    assert len(committed)==32 and all(c in '0123456789abcdef' for c in committed)
   if b'FASED_NETWORK_PENDING' in line:
    assert not blocked,'duplicate external probe request'
    print('Ubuntu network: verify NEW public SSH is blocked while existing connection survives',flush=True)
    verify_public_ssh_blocked('pending')
    blocked=True;process.stdin.write(b'PUBLIC_SSH_BLOCKED\n');process.stdin.flush()
  assert process.poll() is not None or process.wait(timeout=5)==0,'network test did not exit'
  assert process.returncode==0 and blocked and committed,'network kernel/rollback case failed; inspect network-test.log'
 finally:
  selector.close()
  if process.poll() is None:process.kill();process.wait(10)
  process.stdin.close();process.stdout.close()
  (root/'network-test.log').write_bytes(b''.join(output))
 return blocked,committed
def observe_committed_boot(attempt,offset,failed=False):
 marker=('Fased network '+attempt+(' persist-failed' if failed else ' commit')).encode();deadline=time.monotonic()+38
 while time.monotonic()<deadline:
  if marker in (root/'vm-serial.log').read_bytes()[offset:]:
   verify_public_ssh_blocked('failed-boot' if failed else 'committed-boot')
   return
  assert vm.poll() is None,'QEMU exited during committed boot'
  time.sleep(.5)
 raise RuntimeError('committed firewall service did not reach actual boot readiness; console rescue will recover this fixture')
# Same pinned VM lifecycle; this mode executes the literal packaged customer
# entries and records installed outcomes, rather than only verifying files.
def staged_browser_directory(work,resume):
 directory=work/'browser'
 if directory.is_symlink():raise RuntimeError('private owned browser directory required')
 directory.mkdir(mode=0o700,exist_ok=resume)
 info=directory.stat()
 if not directory.is_dir() or info.st_uid!=os.getuid() or info.st_mode&0o777!=0o700:
  raise RuntimeError('private owned browser directory required')
 return directory

def staged_package_acceptance():
 import importlib.util, shlex
 spec=importlib.util.spec_from_file_location('fased_staged_guest',repo/'scripts/staged-package-guest.py')
 fixture=importlib.util.module_from_spec(spec);spec.loader.exec_module(fixture)
 packages={k:fixture.package(v) for k,v in [('candidate',a.candidate),('predecessor',a.predecessor),('legacy',a.legacy_predecessor)]}
 target=packages['candidate']['packet'];version=target['productVersion']
 assert target['version']==2 and packages['predecessor']['packet']['version']==2
 if not (a.released_local_only or a.fresh_remote_only):assert packages['predecessor']['packet']['productVersion']=='0.1.4'
 if not (a.released_local_only or a.fresh_remote_only):assert packages['legacy']['packet']['version']==1 and packages['legacy']['packet']['productVersion']=='0.1.5'
 for field in ['source','productVersion']:
  assert isinstance(target[field],str)
 for field in ([] if a.released_local_only else ['source','sourceTree','runId','runAttempt']):
  left=target if field=='source' else target['release'];right=packages['predecessor']['packet'] if field=='source' else packages['predecessor']['packet']['release']
  assert left[field]==right[field],'compatibility fixture must have the exact target source/run identity'
 result={**fixture.binding(packages['candidate']),'result':'FAIL','fixtureOnly':True,'mining':False,'liveFeatures':False,'automaticUpdates':False,'checks':{},'profiles':{}}
 # Shared public inputs must not sit below ubuntu's private (0750) home.
 # The neutral directory is ubuntu-owned; delivery state remains mode0700.
 checks=result['checks']; guest='/var/tmp/fased-staged'; remote_work=guest+'/delivery'
 host_browser=staged_browser_directory(work,a.resume)
 def guest_call(args,owner='ubuntu',timeout=360):
  command='FASED_DISPOSABLE_VM=1 python3 '+shlex.quote(guest+'/scripts/staged-package-guest.py')+' '+shlex.join(args)
  connection=sshbase[:-1]+[owner+'@127.0.0.1',command]
  reply=subprocess.run(connection,timeout=timeout,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
  return fixture.checked_reply(reply,owner,args[0],root/'staged-owner-failure.json')
 def owner_call(profile,action,expected=None):
  return guest_call(['owner','--profile',profile,'--owner-action',action,*(['--version',expected] if expected else [])],fixture.PROFILES[profile][0])
 def select_package(name,fail=False):
  return guest_call(['select','--work',remote_work,'--selection',name,*(['--fail-archive-once'] if fail else [])])
 def start_tunnel():
  global tunnel
  if tunnel is not None and tunnel.poll() is None:tunnel.terminate();tunnel.wait(10)
  forwards=[]
  for _,_,listen in ([fixture.PROFILES['local'],fixture.PROFILES['fresh']] if a.released_local_only else [fixture.PROFILES[name] for name in ['local','remote','legacy']]):forwards+=['-L',f'127.0.0.1:{listen}:127.0.0.1:{listen}']
  # Model a logged-in Local desktop while retaining the same private forwards.
  # Remote's system service must still boot without its operator logging in.
  tunnel=subprocess.Popen(sshbase[:-1]+['-o','ExitOnForwardFailure=yes',*forwards,'fased-local@127.0.0.1','sleep 3600'],stdout=subprocess.DEVNULL,stderr=(root/'staged-tunnel.stderr').open('ab'))
 def browser(profile,action,expected,link=None):
  listen=fixture.PROFILES[profile][2]
  cfg={'fixtureOnly':True,'action':action,'url':link or 'http://127.0.0.1:'+str(listen),'version':expected,'storageFile':str(host_browser/(profile+'-storage.json')),'baselineFile':str(host_browser/(profile+'-baseline.json'))}
  if action in ['ui-update','automatic-update']:cfg['fromVersion']=packages['predecessor']['packet']['productVersion']
  if a.released_local_only and action=='auth-cycle':cfg['reopenUrl']=owner_call(profile,'open')['url']
  if a.released_local_only and action=='automatic-update':cfg['closeForAutomatic']=True
  path=host_browser/(profile+'-request.json');fixture.write_private(path,cfg)
  reply=subprocess.run(['node','scripts/staged-package-browser.mjs',str(path)],timeout=360,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
  value=fixture.checked_reply(reply,profile,'browser-'+action,root/'staged-browser-failure.json');assert value['result']=='PASS';return value
 def reboot():
  previous=ssh('cat /proc/sys/kernel/random/boot_id',15).strip()
  subprocess.run(sshbase+['sudo -n -- /usr/bin/systemctl reboot'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15)
  # Wait for this recorded boot to change, then ordinary SSH readiness.
  deadline=time.monotonic()+90
  while time.monotonic()<deadline:
   try:
    current=ssh('cat /proc/sys/kernel/random/boot_id',5).strip()
    if current!=previous:break
   except (subprocess.SubprocessError,OSError):pass
   time.sleep(1)
  else:raise RuntimeError('disposable Remote reboot did not complete')
  ready(60);guest_call(['network','--work',remote_work]);start_tunnel()
  return {'before':previous,'after':current}
 payload=work/'staged-payload.tar'
 with tarfile.open(payload,'w') as arc:
  for name in ['staged-package-guest.py','staged-acceptance-contract.json']:arc.add(repo/'scripts'/name,'scripts/'+name)
  for label,value in packages.items():
   directory=P(value['directory'])
   for asset in fixture.ASSETS:arc.add(directory/asset,'packages/'+label+'/'+asset)
   seed=directory/'first-install-seed.json'
   if seed.exists():arc.add(seed,'packages/'+label+'/first-install-seed.json')
 result['inputs']={name:{'source':v['packet']['source'],'version':v['packet']['productVersion'],'artifactSetSha256':fixture.binding(v)['artifactSetSha256']} for name,v in packages.items()}
 with payload.open('rb') as stream:run(sshbase+['umask 077; mkdir '+('-p -- ' if a.fresh_remote_only and a.resume else '-- ')+guest+'; tar -xf - -C '+guest],stdin=stream,timeout=120)
 # Private disposable source is readable by the three fixture owners. They
 # execute as themselves; only the verified shell entry's OS bootstrap is root.
 ssh('chmod 755 '+guest+' '+guest+'/scripts '+guest+'/packages; chmod -R a+rX '+guest+'/packages '+guest+'/scripts',30)
 guest_call(['setup',*(['--resume'] if a.fresh_remote_only and a.resume else []),'--work',remote_work,'--candidate',guest+'/packages/candidate','--predecessor',guest+'/packages/predecessor','--legacy',guest+'/packages/legacy',*(['--released-local-only'] if a.released_local_only or a.fresh_remote_only else [])])
 for label in ['candidate','predecessor']:
  ssh('chmod 700 '+guest+'/packages/'+label+'/fased-install-linux-amd64; '+guest+'/packages/'+label+'/fased-install-linux-amd64 --verify-candidate '+guest+'/packages/'+label,90)
 if a.fresh_remote_only:
  select_package('candidate');return fresh_remote_acceptance(fixture,packages['candidate'],guest,remote_work,local_acceptance_sha)
 start_tunnel();select_package('predecessor')
 installed={}
 installed['local']=owner_call('local','install',packages['predecessor']['packet']['productVersion'])
 print('Staged package: Local URL wizard installed',flush=True)
 if a.released_local_only:
  pred=packages['predecessor']['packet'];assert pred['productVersion']=='0.1.6' and pred['source']=='738bd835b8c86c7cf7b3abfb2945240635f51fe7'
  browser('local','claim',pred['productVersion'],installed['local']['url']);owner_call('local','login')
  result['releasedPredecessor']={'version':pred['productVersion'],'source':pred['source'],'artifactSetSha256':result['inputs']['predecessor']['artifactSetSha256']}
  result.update(localOnly=True,remoteTested=False,publicAcceptance=False,legacyInitialCLIAuthentication=True)
  if version==pred['productVersion']:
   assert owner_call('local','check')['cliOutcome']=='alreadyCurrent';assert browser('local','check',version)['uiOutcome']=='alreadyCurrent'
   owner_call('local','restart');browser('local','preserve',version)
   result['reboot']=reboot();assert browser('local','reconnect',version)['reconnected'] is True
   result.update(result='SUPPORTING',checks={'releasedLocalInstall':'PASS','uiCliAlreadyCurrent':'PASS','restartRebootPreservation':'PASS','noExtraPassword':'FAIL'},targetUpgrade='NOT_RUN')
   save('released-local-acceptance.json',result);return result
  def passwordless_current(profile):
   state=owner_call(profile,'check');assert state['cliOutcome']=='alreadyCurrent' and state['passwordPrompt'] is False;return state
  for suffix in ['storage.json','storage.json.session.json']:
   shutil.copyfile(host_browser/('local-'+suffix),host_browser/('initial-'+suffix))
  ssh('sync');disk_snapshot('baseline');start_vm();ready(90);guest_call(['network','--work',remote_work]);start_tunnel();select_package('candidate')
  # Recover a refused acquisition through the ordinary UI, without bootstrap.
  select_package('candidate',True);failure=browser('local','failed-check',pred['productVersion'])
  assert failure['acquisitionFailurePreserved'] is True
  assert json.loads(ssh('cat '+remote_work+'/selection.json',15)).get('archiveFailureObserved') is True,'check failed before the injected archive acquisition'
  select_package('candidate');manual=browser('local','ui-update',version);assert manual['uiOutcome']=='applied'
  owner_call('local','restart')
  # A held candidate can be ahead of public latest. Exercise ordinary UI/CLI
  # Check against the actual older published predecessor, without reapplying it.
  select_package('predecessor')
  passwordless_current('local');assert browser('local','check',version)['uiOutcome']=='alreadyCurrent'
  checks['installedAheadOfPublic']='PASS'
  result['olderPublicHead']={'version':pred['productVersion'],'uiOutcome':'alreadyCurrent','cliOutcome':'alreadyCurrent','downgradeApplied':False}
  select_package('candidate')
  assert browser('local','auth-cycle',version)['logoutSignIn'] is True
  result['reboot']=reboot();assert browser('local','reconnect',version)['reconnected'] is True
  checks.update(entry='PASS',local='PASS',ownerAuth='PASS',uiUpdate='PASS',restart='PASS',alreadyCurrent='PASS',preservation='PASS',recovery='PASS',logoutSignIn='PASS',rebootReconnect='PASS')
  # Reuse that same actual public-reader baseline for independent CLI and Auto.
  def restore_local():
   disk_snapshot('baseline',True);start_vm();ready(90);guest_call(['network','--work',remote_work]);start_tunnel();select_package('candidate')
   for suffix in ['storage.json','storage.json.session.json']:shutil.copyfile(host_browser/('initial-'+suffix),host_browser/('local-'+suffix))
  restore_local();cli=owner_call('local','apply',version);assert cli['cliOutcome']=='applied' and cli['passwordPrompt'] is False
  browser('local','preserve',version);checks['cliUpdate']='PASS'
  restore_local();automatic=browser('local','automatic-update',version)
  assert automatic['automaticOutcome']=='applied' and automatic['browserClosedDuringApply'] is True and automatic['policyRestored']=='manual'
  checks['controlledAutomatic']='PASS'
  # New Local setup must not inherit the predecessor's password requirement.
  fresh_session=subprocess.Popen(sshbase[:-1]+['fased-fresh@127.0.0.1','sleep 3600'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  try:
   fresh=owner_call('fresh','install',version);opened=browser('fresh','claim',version,fresh['url']);assert opened['passwordFreeSetup'] is True
   passwordless_current('fresh');browser('fresh','check',version)
  finally:
   if fresh_session.poll() is None:fresh_session.terminate();fresh_session.wait(10)
  checks['passwordFreeLocalSetup']='PASS'
  result.update(result='PASS',uiOutcome='applied',cliOutcome='applied',automaticOutcome='applied',automaticPolicyRestored='manual',upgradeApplied=True,customerActivation=False,controlledAutomatic=[{'profile':'local',**automatic}],predecessorPacketVersion=2,candidatePacketVersion=2)
  save('installed-acceptance.json',result);return result
 # Exact URL entry in a real root SSH terminal. The reviewed shell performs
 # fixed OS preparation and drops to the selected ordinary operator before Go.
 entry='bash <(curl -fsSL https://github.com/fased-ai/fased-app/releases/download/v0.1.4/install.sh) --operator fased-remote --listen 127.0.0.1:17482'
 answers={'Choose [local/remote':'','Private access [':'ssh','Use a dedicated app account':'y','Keep Fased running':'y','Start Fased at login?':'y',**fixture.PREPARATION_ANSWERS}
 code,output=fixture.terminal(sshbase[:-1]+['-tt','root@127.0.0.1',entry],answers,timeout=300)
 (root/'root-entry.log').write_text(fixture.redacted(output))
 assert code==0,'root-safe URL entry failed; preserve terminal output'
 installed['remote']=owner_call('remote','open')
 print('Staged package: root Remote URL wizard and private handoff installed',flush=True)
 for profile in ['local','remote']:
  browser(profile,'claim','0.1.4',installed[profile]['url']);owner_call(profile,'login')
  shutil.copyfile(host_browser/(profile+'-storage.json'),host_browser/(profile+'-initial-storage.json'))
  shutil.copyfile(host_browser/(profile+'-storage.json.session.json'),host_browser/(profile+'-initial-session.json'))
  result['profiles'][profile]={'installed':'PASS','ordinaryOwnerUID':fixture.PROFILES[profile][1]}
  print('Staged package: '+profile+' owner claim and CLI sign-in passed',flush=True)
 select_package('legacy');legacy_install=owner_call('legacy','install','0.1.5')
 browser('legacy','claim','0.1.5',legacy_install['url']);owner_call('legacy','login')
 app_user=ssh('systemctl show --property User --value fased-vps-1200.service',15).strip()
 assert app_user and ssh('id -u '+shlex.quote(app_user),15).strip() not in ['0','1200'],'Remote runtime must be a separate non-root app identity'
 checks.update(entry='PASS',local='PASS',remote='PASS',ownerAuth='PASS',rootEntry='PASS')
 # The only browser endpoints are SSH-forwarded loopback listeners.
 listeners=ssh('ss -ltn',15)
 assert all('127.0.0.1:'+str(v[2]) in listeners for v in [fixture.PROFILES[name] for name in ['local','remote','legacy']])
 assert all(('0.0.0.0:'+str(v[2])) not in listeners and ('[::]:'+str(v[2])) not in listeners for v in [fixture.PROFILES[name] for name in ['local','remote','legacy']])
 checks['privateAccess']='PASS'
 print('Staged package: three installed profiles and private listeners passed',flush=True)
 ssh('sync',15);disk_snapshot('baseline');start_vm();ready(90);guest_call(['network','--work',remote_work]);start_tunnel()
 select_package('candidate',True)
 failed=owner_call('legacy','failed-bootstrap',version)
 observed=json.loads(ssh('cat '+remote_work+'/selection.json',15))
 if failed['result']!='PASS' or observed.get('archiveFailureObserved') is not True:
  detail=fixture.redacted(failed.get('failure','missing original failure'))[-8192:]
  fixture.write_private(root/'staged-owner-failure.json',{'owner':'legacy','action':'archive-recovery','returncode':failed.get('returncode'),'stderr':detail})
  raise RuntimeError('failure proof did not reach its injected archive request: '+detail[-4096:])
 result['recovery']={'kind':'failed-verified-acquisition','archiveFailureObserved':True,'instancePreserved':True}
 checks['recovery']='PASS'
 select_package('candidate');owner_call('legacy','bootstrap',version)
 browser('legacy','preserve',version);checks['legacyBootstrap']='PASS'
 print('Staged package: public0.1.5 failure recovery and reader bridge passed',flush=True)
 result['legacyBootstrap']={'result':'PASS','productVersion':'0.1.5','predecessorPacketVersion':1,'candidatePacketVersion':2,'upgradeApplied':True,'predecessorArtifactSetSha256':result['inputs']['legacy']['artifactSetSha256']}
 local_apply=browser('local','ui-update',version);assert local_apply['uiOutcome']=='applied'
 root_status=json.loads(run(sshbase[:-1]+['root@127.0.0.1','fased update status'],timeout=30).stdout.decode())
 assert root_status['controllerAvailable'] is True and root_status['installedVersion']=='0.1.4'
 code,output=fixture.terminal(sshbase[:-1]+['-tt','root@127.0.0.1','fased update'],{'Owner password: ':fixture.PASSWORD,'Update to '+version+'? [y/N]: ':'y'},timeout=360)
 (root/'root-cli-update.log').write_text(fixture.redacted(output))
 assert code==0 and 'Updated' in output,'standard root-safe installed CLI did not apply'
 remote_after=owner_call('remote','check');assert remote_after['health']['version']==version
 browser('remote','preserve',version)
 checks.update(uiUpdate='PASS',cliUpdate='PASS',preservation='PASS')
 print('Staged package: actual UI and root-safe installed CLI apply passed',flush=True)
 for profile in ['local','remote']:
  owner_call(profile,'restart');assert owner_call(profile,'check')['cliOutcome']=='alreadyCurrent';assert browser(profile,'check',version)['uiOutcome']=='alreadyCurrent'
  assert browser(profile,'auth-cycle',version)['logoutSignIn'] is True
 checks.update(restart='PASS',alreadyCurrent='PASS',logoutSignIn='PASS')
 result['reboot']=reboot()
 for profile in ['local','remote']:assert browser(profile,'reconnect',version)['reconnected'] is True
 checks['rebootReconnect']='PASS'
 print('Staged package: restart, no-op, sign-out/in and reboot reconnect passed',flush=True)
 # Reuse the exact pre-update disk/data/auth baseline for opt-in automatic
 # operation; do not create another product version or public release.
 disk_snapshot('baseline',True);start_vm();ready(90);guest_call(['network','--work',remote_work]);start_tunnel();select_package('candidate')
 auto=[]
 for profile in ['local','remote']:
  shutil.copyfile(host_browser/(profile+'-initial-storage.json'),host_browser/(profile+'-storage.json'))
  shutil.copyfile(host_browser/(profile+'-initial-session.json'),host_browser/(profile+'-storage.json.session.json'))
  outcome=browser(profile,'automatic-update',version)
  assert outcome['automaticOutcome']=='applied' and outcome['policyRestored']=='manual' and outcome['customerActivation'] is False
  auto.append({'profile':profile,**outcome})
  print('Staged package: '+profile+' automatic apply and Manual restoration passed',flush=True)
 for profile in ['local','remote']:
  assert owner_call(profile,'open')['urlCount']==1,'updated installation must present one usable owner link'
 checks['controlledAutomatic']='PASS'
 result.update(result='PASS',upgradeRoute='installed',predecessorPacketVersion=2,candidatePacketVersion=2,upgradeApplied=True,uiOutcome='applied',cliOutcome='applied',ordinaryUpdateApply='PASS',automaticOutcome='applied',automaticPolicyRestored='manual',customerActivation=False,controlledAutomatic=auto)
 pred=packages['predecessor']['packet'];result['compatibilityPredecessor']={'fixtureOnly':True,'published':False,'packetVersion':2,'productVersion':'0.1.4','source':pred['source'],'sourceTree':pred['release']['sourceTree'],'runId':pred['release']['runId'],'runAttempt':pred['release']['runAttempt'],'artifactSetSha256':result['inputs']['predecessor']['artifactSetSha256']}
 save('installed-acceptance.json',result)
 return result

def fresh_remote_acceptance(fixture,candidate,guest,remote_work,local_receipt_sha):
 # Run the actual official shell entry. Only HTTPS delivery is controlled;
 # Tailscale packages, daemon, enrollment, identity and private TLS are real.
 import re,shlex,os
 receipt={**fixture.binding(candidate),'result':'FAIL','fixtureOnly':True,'freshRemote':True,
          'mining':False,'liveFeatures':False,'automaticUpdates':False,
          'localAcceptanceSha256':local_receipt_sha,'checks':{}}
 journal=fixture.RemoteJourney(root/'remote-journey.json',fixture.binding(candidate),local_receipt_sha)
 assert journal.value['phase']=='new' or a.resume,'resume the retained Remote journey explicitly'
 if not a.resume:
  assert ssh('test ! -e /var/lib/fased-app-vps-1000 && test ! -e /usr/bin/tailscale && echo clean',15).strip()=='clean','fresh setup requires a clean disposable baseline'
 host_tail=json.loads(subprocess.run(['tailscale','status','--json'],check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15).stdout)
 suffix=host_tail.get('MagicDNSSuffix','')
 fixture.private_tailnet_origin(host_tail,suffix)
 observed={}
 if journal.value['phase']!='new':
  observed.update(origin=journal.value['origin'],enrolled=True)
  if journal.phases.index(journal.value['phase'])>=journal.phases.index('private-browser'):observed['browserApproved']=True
 def enrollment_ready():
  tail=json.loads(ssh('tailscale status --json',15))
  assert ssh('test ! -e /var/lib/fased-app-vps-1000 && echo undeployed',15).strip()=='undeployed','Fased deployed before private browser proof'
  if tail.get('BackendState')=='NeedsLogin':return False
  origin=fixture.private_tailnet_origin(tail,suffix)
  assert observed.get('origin',origin)==origin,'enrolled Tailscale node identity changed before deployment'
  journal.enrolled(tail,suffix)
  observed.update(origin=origin,enrolled=True)
  return True
 def waiting_for_owner():
  return bool(observed.get('enrollment')) and json.loads((root/'enrollment.json').read_text()).get('waitingForOwner') is True
 retry=fixture.RemoteEnrollmentRetry(enrollment_ready,handoff_pending=waiting_for_owner)
 def observe(text):
  links=re.findall(r'https://login\.tailscale\.com/a/[A-Za-z0-9]+',text)
  if links and links[-1]!=observed.get('enrollment'):
   assert ssh('test ! -e /var/lib/fased-app-vps-1000 && echo undeployed',15).strip()=='undeployed','Fased deployed before Tailscale enrollment'
   observed['enrollment']=links[-1]
   fixture.write_private(root/'enrollment.json',{'url':links[-1],'realDaemon':True,'fasedNotDeployed':True,'waitingForOwner':True})
   print('Fresh Remote: Tailscale approval required; private enrollment.json contains the URL',flush=True)
  # Predeployment browser proof and final owner handoff are distinct URLs.
  # Never open an enrollment or final setup URL as the private browser probe.
  links=re.findall(r'https://[a-z0-9.-]+\.ts\.net/[^\s]*',text)
  for link in links:
   parsed=urllib.parse.urlsplit(link)
   if re.fullmatch(r'[A-Za-z0-9_-]{43}',parsed.fragment) and not parsed.query:
    if observed.get('preflight')==link:continue
    assert not observed.get('preflight'),'a second private browser proof is not authorized'
    assert enrollment_ready(),'Tailscale enrollment must precede private browser proof'
    assert parsed.scheme+'://'+parsed.netloc==observed['origin'] and parsed.path=='/','unexpected private browser origin'
    observed['preflight']=link
    request=root/'private-preflight-request.json'
    fixture.write_private(request,{'fixtureOnly':True,'action':'private-preflight','url':link,'expectedOrigin':observed['origin']})
    print('Fresh Remote: verifying private browser access before deployment',flush=True)
    reply=subprocess.run(['node','scripts/staged-package-browser.mjs',str(request)],timeout=360,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    fixture.write_private(root/'private-preflight-browser.json',{'returncode':reply.returncode,'stdout':fixture.redacted(reply.stdout.decode(errors='replace')),'stderr':fixture.redacted(reply.stderr.decode(errors='replace'))})
    assert reply.returncode==0,'private browser check failed; inspect private-preflight-browser.json'
    proof=json.loads(reply.stdout)
    assert proof=={'result':'PASS','action':'private-preflight','privateBrowserApproved':True},'private browser approval was not confirmed'
    observed['browserApproved']=True
    journal.advance('private-browser')
   elif parsed.query and parsed.fragment.startswith('setup='):
    assert observed.get('browserApproved'),'final setup appeared before private browser approval'
    assert parsed.scheme+'://'+parsed.netloc==observed['origin'] and parsed.path=='/','unexpected final private origin'
    observed['private']=link
    fixture.write_private(root/'private-setup.json',{'url':link,'transport':'direct-tailscale-https'})
 entry='bash <(curl -fsSL https://github.com/fased-ai/fased-app/releases/latest/download/install.sh) --remote --operator ubuntu --listen 127.0.0.1:17482'
 answers={
  'Connect Tailscale for private Remote access? [Y/n]: ':'y',
  'Use a dedicated app account with no login or sudo? [Y/n]: ':'y',
  'Keep Fased running after logout and reboot? [Y/n]: ':'y',
  'Set up Tailscale packages, service, browser sign-in and access for this OS user? [y/N]: ':'y',
  'Authorize this operation with sudo? [y/N]: ':'y',**fixture.PREPARATION_ANSWERS}
 try:
  if journal.phases.index(journal.value['phase'])>=journal.phases.index('installed'):
   # Resume the actual installation, not a snapshot or a second node.
   tail=json.loads(ssh('tailscale status --json',15));journal.enrolled(tail,suffix)
   observed['private'],_=fixture.browser_link(ssh('fased open',30))
  else:
   present=a.resume and ssh('test -e /var/lib/fased-app-vps-1000 && echo present || echo absent',15).strip()=='present'
   if present:
    # The process may have completed before its final journal write. Inspect
    # the actual protected installation; never re-claim, reinstall or rewind it.
    status=json.loads(ssh('fased status',30))
    assert status.get('installed') is True and status.get('active') is True,'retained installation is not active; inspect fased status and its existing update/recovery transaction'
    tail=json.loads(ssh('tailscale status --json',15));journal.enrolled(tail,suffix)
    observed['private'],_=fixture.browser_link(ssh('fased open',30))
    assert urllib.parse.parse_qs(urllib.parse.urlsplit(observed['private']).query).get('instance')==[status.get('instanceId')],'installed owner handoff differs from protected service identity'
   else:
    code,output=fixture.terminal(sshbase[:-1]+['-tt','root@127.0.0.1',entry],answers,timeout=1200,password_free=True,observer=observe,prompt_retry=retry,transcript=root/'root-entry.log')
    assert code==0,'literal fresh Remote entry failed; inspect the redacted terminal log'
  assert observed.get('enrolled') and observed.get('browserApproved') and observed.get('private'),'real enrollment, browser proof and private HTTPS handoff required'
  tail=json.loads(ssh('tailscale status --json',15))
  assert tail.get('BackendState')=='Running' and tail.get('Self',{}).get('Online') is True,'Tailscale must remain running'
  assert fixture.private_tailnet_origin(tail,suffix)==observed['origin'],'enrolled node identity changed'
  service=ssh('systemctl show --property User --value fased-vps-1000.service',15).strip()
  assert service,'dedicated non-root app identity required'
  passwd=ssh('getent passwd '+shlex.quote(service),15).strip().split(':')
  assert len(passwd)==7 and re.fullmatch(r'[a-z_][a-z0-9_-]*',passwd[0]) and passwd[-1]=='/usr/sbin/nologin','app account must have no login'
  account_name=passwd[0]
  assert passwd[2].isdigit() and passwd[2] not in ('0','1000') and (service==passwd[2] if service.isdigit() else service==account_name),'dedicated non-root app identity required'
  assert ssh('id -u '+shlex.quote(account_name),15).strip()==passwd[2],'app UID must match the installed service'
  account=ssh('sudo -n /usr/bin/passwd -S '+shlex.quote(account_name),15).split()
  assert len(account)>1 and account[1]=='L','app account must be locked'
  serve=json.loads(ssh('tailscale serve status --json',15))
  proxies=[handler.get('Proxy') for web in serve.get('Web',{}).values() for handler in web.get('Handlers',{}).values()]
  assert 'unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock' in proxies,'fresh Serve must target the protected Unix proxy'
  assert not any(serve.get('AllowFunnel',{}).values()),'public Funnel must remain disabled'
  protected=ssh("sudo -n /usr/bin/stat -c '%F:%a:%U' /var/lib/fased-app-vps-1000/app-state/tailscale.sock",15).strip().split(':')
  assert len(protected)==3 and protected[0]=='socket' and int(protected[1],8)&0o007==0 and protected[2]==account_name,'Unix proxy must belong to the isolated app, without other-user access'
  # Never use a port forward, claimed owner cookie, or test TLS exception.
  endpoint=observed['private'].split('#',1)[0].split('?',1)[0]+'api/v1/health'
  opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
  with opener.open(endpoint,timeout=20) as response:health=json.load(response)
  assert health.get('version')==candidate['packet']['productVersion'],'direct private HTTPS must reach the installed candidate'
  assert all(health.get('capabilities',{}).get(name) is False for name in ('aiConnections','financialExecution','walletSigner')),'live capabilities remain held'
  identity=urllib.parse.parse_qs(urllib.parse.urlsplit(observed['private']).query).get('instance',[])
  assert len(identity)==1,'one Remote owner identity required'
  journal.advance('installed',instance_id=identity[0])
  receipt.update(instanceId=identity[0],nodeIdentitySha256=journal.value['nodeIdentitySha256'],transport='tailscale-https',normalTLS=True)
  receipt['checks'].update(literalEntry='PASS',tailBeforeDeployment='PASS',predeploymentBrowser='PASS',freshProtectedProxy='PASS',directPrivateHTTPS='PASS',lockedAppIdentity='PASS',noExtraPassword='PASS')
  # This first fresh-install leg is not whole Remote/upgrade/lockdown acceptance.
  # Continue the existing private UI/closure/update runner only with the SAME VM
  # and independent console proof; never promote this narrower receipt.
  receipt.update(result='SUPPORTING',publicAcceptance=False,upgradeApplied=False,
                 remaining=['owner-device enrollment','private-admin and independent console recovery','public IPv4/IPv6 closure and rollback','actual predecessor UI/CLI upgrade','reboot/reconnect'])
  return receipt
 finally:
  receipt['enrollmentRetry']={'readinessChecks':retry.checks+retry.waiting_checks,'resendSent':retry.used,
                            'outcome':'sent' if retry.used else 'pending-ended' if retry.pending_ended else 'not-needed-or-not-sent'}
  receipt['enrollmentRetry']['ownerWaitChecks']=retry.waiting_checks
  save('fresh-remote-setup.json',receipt)
  # This is a leg of the parent journey. Stop the owned VM at runner exit, but
  # retain its encrypted state/enrollment and journal until all legs complete.
  # Never log out here: that destroys the next phase's original identity.
  receipt['retainedPhase']=journal.value['phase']
  save('fresh-remote-setup.json',receipt)

if a.staged_package_only:
 started=time.time();staged_result={'result':'FAIL','fixtureOnly':True}
 try:
  print('Ubuntu staged package: actual released Local setup and no-op' if a.released_local_only else 'Ubuntu staged package: literal Local/Remote URL wizard and installed UI/CLI Update',flush=True)
  ready();staged_result=staged_package_acceptance()
 except BaseException as error:
  staged_result['error']=str(error)
  if isinstance(error,(subprocess.CalledProcessError,subprocess.TimeoutExpired)):
   (root/'staged-failure.stdout').write_bytes(error.stdout or b'')
   (root/'staged-failure.stderr').write_bytes(error.stderr or b'')
  raise
 finally:
  # Flush private guest diagnostics before terminating this disposable VM.
  # Deliberate crash/reboot probes inside the journey keep their own semantics.
  try:ssh('sync',10)
  except (subprocess.SubprocessError,OSError):pass
  if tunnel is not None and tunnel.poll() is None:tunnel.terminate();tunnel.wait(10)
  stop_vm();serial.close();stderr.close()
  staged_result['durationSeconds']=round(time.time()-started,3)
  if not a.fresh_remote_only and (staged_result['result']=='PASS' or not a.keep_failed_vm):
   assert work.is_dir() and not work.is_symlink() and work.stat().st_uid==os.getuid()
   shutil.rmtree(work);staged_result['cleanup']='PASS: disposable VM, payload, disk, authentication and SSH keys removed'
  else:staged_result['cleanup']='RETAINED: same Remote node/disk/journal until parent journey acceptance; VM stopped'
  save('vm-result.json',staged_result)
 raise SystemExit(0)

previous=json.loads((root/'vm-result.json').read_text()) if a.inspection_only else None
report={'startedAt':time.time(),'result':'FAIL','fixtureOnly':True,'realWallets':False,'automaticUpdates':False,'accelerator':'kvm' if kvm else 'tcg','imageSHA256':IMAGE_SHA,'checks':{},'cleanup':'PENDING'}
if previous:
 report['checks']=previous['checks']
 report['oldReaderRefusals']=previous.get('oldReaderRefusals',{})
 report['expiredRecovery']=previous.get('expiredRecovery')
 report['reusedBootReceiptSHA256']=sha((root/'vm-result.json').read_bytes())
try:
 print('VM boot: '+report['accelerator'],flush=True);ready()
 if single_case:
  report['inputs']={}
  bundle=work/'inputs.tar'
  core=root/'inputs/fased-app-tests' if a.installed_remote_update_only else repo/'.evidence/p4-fault-reboot/fased-app'
  assert core.is_file() and not core.is_symlink() and core.stat().st_uid==os.getuid(),'owned current test executable required'
  with tarfile.open(bundle,'w') as arc:
   arc.add(core,'.evidence/bin/fased-app')
   if a.installed_remote_update_only:
    fixture_inputs=root/'inputs'
    assert {entry.name for entry in fixture_inputs.iterdir()}=={'fased-app-tests','repo','seed.json','fixture.json','ca.pem','cert.pem','key.pem'},'finite signed input set required'
    entries=sorted(fixture_inputs.rglob('*'))
    assert len(entries)<=64 and all(not entry.is_symlink() for entry in entries),'finite regular fixture inputs required'
    for entry in entries:
     rel=entry.relative_to(repo)
     assert entry.is_file() or entry.is_dir(),'regular fixture entry required'
     if entry.is_file():report['inputs'][str(rel)]=sha(entry.read_bytes())
     guest_rel=P('.evidence/b01-connected-install/installed-remote/inputs')/entry.relative_to(fixture_inputs) if a.installed_finalizer_update_only else rel
     arc.add(entry,str(guest_rel),recursive=False)
   if a.network_only:
    network=repo/'.evidence/b01-network/network-tests'
    assert network.is_file() and not network.is_symlink() and network.stat().st_uid==os.getuid(),'owned current network test executable required'
    arc.add(network,'.evidence/bin/network-tests')
    report['inputs']['.evidence/bin/network-tests']=sha(network.read_bytes())
  report['inputs']['.evidence/bin/fased-app']=sha(core.read_bytes())
  save('vm-inputs.json',report['inputs'])
  with bundle.open('rb') as stream:run(sshbase+['umask 077; mkdir -p fased/.evidence/p4-fault-reboot/guest && cd fased && tar -xf -'],stdin=stream,timeout=120)
  if a.installed_remote_update_only:
   print('Ubuntu installed Remote update: verified signed fixture, separate owner1100 and AppUID; remove setup grant before dispatch',flush=True)
   guest_root='/home/ubuntu/fased/.evidence/b01-connected-install/installed-remote'
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 .evidence/bin/fased-app -test.run=^TestApplicationUpdateInstalledRemotePrepareUbuntuVM$ -test.v',150)
   (root/'installed-prepare-test.log').write_text(output)
   prepared=json.loads(ssh('cat '+guest_root+'/prepared.json',30))
   assert prepared['result']=='PASS' and prepared['fixtureOnly'] and prepared['temporarySetupGrantRemoved']
   if a.installed_finalizer_update_only:
    generated=json.loads((root/'input-receipt.json').read_text());assert generated['finalizerProduced'] and not generated['privateSigningKeysRetained'] and not generated['productionKeys']
    report['finalizerInput']=generated
   manifest=prepared['manifest'];assert manifest['ownerUid']==1100 and manifest['appUid'] not in [0,1000,1100] and manifest['data']=='/var/lib/fased-app-vps-1100/app-state'
   before=ssh('cat /proc/sys/kernel/random/boot_id').strip()
   deadline=time.monotonic()+60;cut=None
   while time.monotonic()<deadline:
    try:
     cut=json.loads(ssh('sudo -n -- /usr/bin/cat -- '+manifest['data']+'/update-fixture-cut.json',10));break
    except (subprocess.SubprocessError,ValueError):time.sleep(.2)
   assert cut and cut['result']=='CUT' and cut['fixtureOnly'] and cut['phase']=='healthy-held' and cut['bootId']==before,'no durable protected completion cut'
   assert len(cut['id'])==32 and all(c in '0123456789abcdef' for c in cut['id'])
   report['checks']['before-reboot']={'prepared':prepared,'cut':cut};save('vm-progress.json',report)
   print('Ubuntu installed Remote update: abrupt power cut at healthy-held, resume original operation on same disk/new kernel',flush=True)
   ssh('sync');stop_vm();start_vm();ready(180)
   after=ssh('cat /proc/sys/kernel/random/boot_id').strip();assert before!=after,'not an actual new kernel boot'
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 .evidence/bin/fased-app -test.run=^TestApplicationUpdateInstalledRemoteVerifyUbuntuVM$ -test.v',100)
   (root/'installed-verify-test.log').write_text(output)
   verified=json.loads(ssh('cat '+guest_root+'/verified.json',30))
   assert verified['result']=='PASS' and verified['fixtureOnly'] and verified['ciphertextPreserved'] and verified['originalCheckpointPreserved']
   assert verified['cutBootId']==before and verified['resumedBootId']==after
   if a.installed_finalizer_update_only:
    assert verified['finalizerProduced'] and verified['authenticatedNoop'] and verified['metadataRequests']>0
    assert verified['finalizedDescriptorSha256']==generated['descriptorSha256']==cut['manifestSha256']
    assert verified['provenanceSha256']==generated['provenanceSha256'] and verified['finalizerPlanSha256']==generated['planSha256']
   for field in ['id','sequence','manifestSha256','engineSha256']:assert verified[field]==cut[field],'recovery changed original '+field
   report['checks']['installed-remote-update']={'result':'PASS','beforeBoot':before,'afterBoot':after,'cut':cut,'verified':verified}
   report['scope']='one signed test app-only installed Remote transaction through protected ordinary owner process, interrupted healthy-held completion and same-ID offline new-kernel resume; no production source, owner-host, real provider/wallet/RPC/mining or public activation'
   if a.installed_finalizer_update_only:report['scope']='finalizer-produced v3/schema2 installed Remote manual update, exact-ID offline new-kernel recovery and authenticated no-op using fixture-build-only transport; no owner-host mutation, production key or public/live activation'
  elif a.maintenance_only:
   print('Ubuntu maintenance authority: isolated owner1100, removed setup grant, actual NNP AppUID Polkit start',flush=True)
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 .evidence/bin/fased-app -test.run=^TestRemoteMaintenanceAuthorityUbuntuVM$ -test.v',150)
   (root/'maintenance-authority-test.log').write_text(output)
   result=receipt('maintenance-authority');assert result['result']=='PASS' and result['fixtureOnly'] and result['temporarySetupGrantRemoved'] and result['appUIDNoNewPrivilegesPolkitStart'] and not result['signedInstalledUpdateAcceptance']
   report['checks']['maintenance-authority']=result
   report['scope']='single actual Ubuntu limited-sudo/Polkit/template UID boundary with isolated ordinary maintainer1100 and inert actor; no signed installed update or public activation acceptance'
  elif a.owner_browser_only:
   print('Ubuntu managed owner browser: install actual isolated service',flush=True)
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config .evidence/bin/fased-app -test.run=^TestManagedOwnerBrowserUbuntuVM$ -test.v',150)
   (root/'owner-provision-test.log').write_text(output)
   provision=receipt('managed-owner-provision');assert provision['result']=='PASS' and provision['fixtureOnly']
   tunnel=subprocess.Popen(sshbase[:-1]+['-N','-o','ExitOnForwardFailure=yes','-L','127.0.0.1:17447:127.0.0.1:17447',sshbase[-1]],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
   target='http://127.0.0.1:17447';deadline=time.monotonic()+15;health=None
   while time.monotonic()<deadline:
    assert tunnel.poll() is None,'private SSH forwarding failed'
    try:
     with urllib.request.urlopen(target+'/api/v1/health',timeout=2) as response:health=json.load(response)
     break
    except (OSError,ValueError):time.sleep(.2)
   assert health and health['instanceId']==provision['instanceId'] and health['connection']['ownerUid']==1000
   assert health['connection']['location']=='remote'
   # Default config-dir spelling is supplied inside a fixed remote command; no
   # owner auth/session/private key bytes are placed in this host metadata file.
   command_prefix='XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config '
   browser_ssh=sshbase[1:]
   config={'fixtureOnly':True,'url':target,'core':provision['manifest']['core'],'ssh':browser_ssh,'commandPrefix':command_prefix,'health':health}
   config_path=repo/'.evidence/b01-owner-browser/browser-config.json'
   config_path.write_text(json.dumps(config,indent=2)+'\n');config_path.chmod(0o600)
   print('Ubuntu managed owner browser: claim, sign-in, protected recovery and preserved state',flush=True)
   result=run(['node','scripts/check-managed-owner-browser.mjs'],env=dict(os.environ,FASED_DISPOSABLE_VM_ACCEPTANCE='1'),timeout=180)
   (root/'browser.stdout').write_bytes(result.stdout);(root/'browser.stderr').write_bytes(result.stderr)
   browser=json.loads((repo/'.evidence/b01-owner-browser/browser-result.json').read_text());assert browser['result']=='PASS'
   report['checks']['managed-owner-browser']=browser;report['scope']='actual isolated Ubuntu service and SSH-forwarded browser claim/recovery; no Tailscale/real VPS/owner browser/public release acceptance'
  elif a.network_only:
   print('Ubuntu recoverable network acceptance (ordinary owner; controlled private proof)',flush=True)
   before=ssh('cat /proc/sys/kernel/random/boot_id').strip()
   blocked,committed=network_guest()
   print('Ubuntu network: committed boot ingress, then disposable console rescue',flush=True)
   stop_vm();offset=(root/'vm-serial.log').stat().st_size;start_vm()
   observe_committed_boot(committed,offset);ready(90)
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config .evidence/bin/network-tests -test.run=^TestNetworkUbuntuVMCommittedBoot$ -test.v',90)
   (root/'network-committedboot-test.log').write_text(output)
   fault=[line.split()[-1] for line in output.splitlines() if line.startswith('FASED_NETWORK_FAULT_COMMITTED ')]
   assert len(fault)==1 and len(fault[0])==32 and all(c in '0123456789abcdef' for c in fault[0])
   print('Ubuntu network: failed firewall boot must refuse SSH until console recovery',flush=True)
   stop_vm();offset=(root/'vm-serial.log').stat().st_size;start_vm()
   observe_committed_boot(fault[0],offset,True);ready(90)
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config .evidence/bin/network-tests -test.run=^TestNetworkUbuntuVMFailedBoot$ -test.v',90)
   (root/'network-failedboot-test.log').write_text(output)
   print('Ubuntu network: abrupt pending power loss and new-kernel rollback',flush=True)
   stop_vm();start_vm();ready(180)
   after=ssh('cat /proc/sys/kernel/random/boot_id').strip();assert before!=after,'not an actual reboot'
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config .evidence/bin/network-tests -test.run=^TestNetworkUbuntuVMPostBoot$ -test.v',60)
   (root/'network-postboot-test.log').write_text(output)
   result=receipt('network');assert result['result']=='PASS' and result['pendingBootRollback'] and result['fixtureOnly'] and not result['realWallets']
   result['newPublicSSHBlocked']=blocked;result['committedBootIngressBlocked']=True;result['failedPersistBootSSHBlocked']=True;result['newPublicSSHRestoredAfterBoot']=True
   report['checks']['network']=result
   report['scope']='actual Ubuntu nft/sshd rollback/retry/commit and pending boot recovery with controlled private-access proof; no real Tailscale/owner-host mutation'
  else:
   print('Ubuntu managed Remote runtime acceptance (ordinary owner)',flush=True)
   output=ssh('cd /home/ubuntu/fased && FASED_DISPOSABLE_VM=1 XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config .evidence/bin/fased-app -test.run=^TestManagedRemoteUbuntuVM$ -test.v',150)
   (root/'managed-remote-test.log').write_text(output)
   result=receipt('managed-remote');assert result['result']=='PASS' and result['fixtureOnly'] and not result['realWallets']
   report['checks']['managed-remote']=result
   report['scope']='single app-only managed Remote systemd/UID/sandbox/claim-code/restart/removal case; no public tailnet or bundle acquisition acceptance'
 else:
  # Upload only the finite fixture bundle; no repository credentials/real state.
  report['inputs']={}
  bundle=work/'inputs.tar'
  with tarfile.open(bundle,'w') as arc:
   for name in ['fixture-vm-guest.mjs','fixture-development-envelope.mjs','fixture-inspection-journey.mjs','handoff-transport-evidence.mjs','fixture-inspection-probes.py','fixture-inspection-clock-probe.py']:arc.add(repo/'scripts'/name,'scripts/'+name)
   for name in ['fased-app','fased-signer','update-tests']:arc.add((repo/'.evidence/p4-fault-reboot'/name if name in ['fased-app','update-tests'] else repo/'.evidence/bin'/name),'.evidence/bin/'+name)
   for name in ['predecessor-app','predecessor-signer','guarded-app']:arc.add(repo/'.evidence/p4-installed-inspection'/name,'.evidence/p4-installed-inspection/'+name)
   arc.add(node,'.evidence/bin/node')
  with tarfile.open(bundle) as arc:
   for entry in arc.getmembers():
    if entry.isfile():report['inputs'][entry.name]=sha(arc.extractfile(entry).read())
  save('vm-inputs.json',report['inputs'])
  with bundle.open('rb') as stream:run(sshbase+['umask 077; mkdir -p fased && cd fased && tar -xf -'],stdin=stream,timeout=120)
  if a.resume:
   # The offline baseline was applied before boot; no RAM receipt is reused.
   ready(90)
   # Restore the explicitly disposable baseline before updating owner fixture files.
   with bundle.open('rb') as stream:run(sshbase+['cd fased && umask 077 && tar -xf -'],stdin=stream,timeout=120)
  else:
   print('VM fixture setup (ordinary owner)',flush=True);guest('setup')
   guest('managed-remote');report['managedRemote']=receipt('managed-remote')
  guest('configure');report['osFixture']=receipt('os-fixture')
  report['setup']=receipt('setup');save('vm-progress.json',report)
  if not a.inspection_only:
   ssh('sync');disk_snapshot('baseline')
  phases=['maintenance-held','stopped','guarded-app-unit-0','guarded-app-unit-1','guarded-signer-unit-0','guarded-signer-unit-1','guarded-manager-reloaded','guarded-units','cold-verified','forward-only','fased-app-replaced','fased-signer-replaced','root-install-record','signer-initialized','owner-initialized','final-app-unit-0','final-app-unit-1','final-signer-unit-0','final-signer-unit-1','final-manager-reloaded','owner-install-record','ready-record','converged-held','maintenance-aborted','reader-ready']
  selected=[] if a.inspection_only else (phases if a.full else ['maintenance-held','cold-verified','forward-only','reader-ready'])
  if a.inspection_only:
   for name in ['.evidence/bin/fased-app','.evidence/bin/fased-signer']:
    assert report['inputs'][name]==previous['inputs'][name],'reader bytes changed; cannot reuse boot evidence'
   cut=previous.get('activeCut') or previous['checks']['reader-ready']['cut']
   if previous.get('protocol',{}).get('result')=='PASS':
    disk_snapshot('inspection-ready',True);start_vm();ready(90)
    with bundle.open('rb') as stream:run(sshbase+['cd fased && umask 077 && tar -xf -'],stdin=stream,timeout=120)
   guest('recover',cut['bridgeId']);recovered=receipt('recover')
   before=cut['boot'];after=ssh('cat /proc/sys/kernel/random/boot_id').strip();assert before!=after
   report['checks']['reader-ready']={'result':'PASS','beforeBoot':before,'afterBoot':after,'cut':cut,'recovered':recovered}
   ssh('sync');disk_snapshot('inspection-ready');start_vm();ready(90)
   if previous.get('protocol',{}).get('result')=='PASS' and previous['inputs']['scripts/fixture-inspection-probes.py']==report['inputs']['scripts/fixture-inspection-probes.py']:
    report['protocol']=previous['protocol'];report['protocolReused']=True
   else:
    guest('probes');report['protocol']=receipt('protocol')
   save('vm-progress.json',report)
   check_inspection_faults()
  shared_id=None
  for number,phase in enumerate(selected):
   print('VM cut / reboot / recovery: '+phase,flush=True)
   disk_snapshot('baseline' if number==0 else 'held-baseline',True);start_vm();ready(90)
   guest('cut' if number==0 else 'advance',phase if number==0 else phase+':'+shared_id);cut=receipt('installed-result');assert cut['result']=='CUT' and cut['cut']==phase
   shared_id=cut['bridgeId'];report['activeCut']=cut;save('vm-progress.json',report)
   if number==0:
    disk_snapshot('held-baseline')
   before=cut.get('boot') or report['setup']['boot']
   # Abrupt power loss: persistently synced boundaries must survive a new kernel.
   # For the first cut the held disk snapshot already stopped QEMU above.
   if number!=0:before=ssh('cat /proc/sys/kernel/random/boot_id').strip();stop_vm()
   start_vm();ready(180)
   after=ssh('cat /proc/sys/kernel/random/boot_id').strip();assert before!=after,'not an actual new kernel boot'
   if phase=='forward-only':
    capsule=json.loads(ssh('cat /var/lib/fased-app-custody-1000/fixture-inspection-v1/capsules/'+cut['bridgeId']+'/transaction.json'))
    expiry=json.loads(base64.b64decode(capsule['maintenance']))['payload']['expiresAt']
    remaining=expiry-int(ssh('date +%s').strip())+1
    assert remaining<=120,'unexpected fixture lifetime'
    if remaining>0:
     print('Awaiting the original maintenance envelope expiry: '+str(remaining)+'s',flush=True);time.sleep(remaining)
    observed=int(ssh('date +%s').strip());assert observed>=expiry
    report['expiredRecovery']={'result':'SUPPORTING','expiresAt':expiry,'observedAt':observed,'systemClockChanged':False}
   if phases.index(phase)>=phases.index('forward-only'):
    guest('old-reader');report.setdefault('oldReaderRefusals',{})[phase]=receipt('old-reader')
   # Recover only through the immutable, hash-bound candidate owner runner.
   guest('rollback' if phase=='cold-verified' else 'recover',cut['bridgeId'])
   recovered=receipt('rollback' if phase=='cold-verified' else 'recover')
   assert recovered['result']=='PASS'
   if phase=='forward-only':report['expiredRecovery']['result']='PASS'
   if number==0:
    ssh('sync');disk_snapshot('inspection-ready');start_vm();ready(90)
    guest('probes');report['protocol']=receipt('protocol')
    check_inspection_faults()
   report['checks'][phase]={'result':'PASS','beforeBoot':before,'afterBoot':after,'cut':cut,'recovered':recovered}
   save('vm-progress.json',report)
 report['result']='PASS'
except BaseException as e:
 report['error']=str(e)
 if isinstance(e,(subprocess.CalledProcessError,subprocess.TimeoutExpired)):
  (root/'guest-failure.stdout').write_bytes(e.stdout or b'')
  (root/'guest-failure.stderr').write_bytes(e.stderr or b'')
  report['commandStdout']=(e.stdout or b'').decode(errors='replace')[-8000:]
  report['commandStderr']=(e.stderr or b'').decode(errors='replace')[-8000:]
 if a.installed_remote_update_only and vm is not None and vm.poll() is None:
  capture_installed_update_failure()
 raise
finally:
 if tunnel is not None and tunnel.poll() is None:tunnel.terminate();tunnel.wait(10)
 stop_vm()
 serial.close();stderr.close()
 report['durationSeconds']=round(time.time()-report['startedAt'],3)
 save('vm-result.json',report)
 if report['result']=='PASS' or not a.keep_failed_vm:
  # Entire directory created exclusively here; no owner installation/state.
  expected={'disk.qcow2','seed.iso','ssh-key','ssh-key.pub','meta-data','user-data','qmp.sock','known_hosts','inputs.tar'}
  assert {p.name for p in work.iterdir()} <= expected,'unknown VM work preserved'
  assert not work.is_symlink() and work.stat().st_uid==os.getuid()
  shutil.rmtree(work);report['cleanup']='PASS: owned VM process/disk/seed/disposable SSH key removed'
 else:report['cleanup']='RETAINED: inspect failed VM; delete only vm-owned after evidence diagnosis'
 save('vm-result.json',report)
