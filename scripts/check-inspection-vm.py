#!/usr/bin/env python3
"""Boot-capable disposable Ubuntu acceptance, never an owner-host install.
No project executable runs as root. Root in the guest executes OS tools only.
Evidence survives snapshot rollback; large reconstructible VM outputs are removed.
"""
import argparse, base64, selectors, hashlib, json, os, pathlib, shutil, signal, socket, subprocess, tarfile, time, urllib.request
P=pathlib.Path
repo=P.cwd(); root=repo/'.evidence/p4-fault-reboot'; root.mkdir(parents=True,exist_ok=True)
p=argparse.ArgumentParser(); p.add_argument('--full',action='store_true'); p.add_argument('--resume',action='store_true'); p.add_argument('--inspection-only',action='store_true'); p.add_argument('--keep-failed-vm',action='store_true'); p.add_argument('--managed-remote-only',action='store_true'); p.add_argument('--network-only',action='store_true'); p.add_argument('--owner-browser-only',action='store_true'); a=p.parse_args()
assert os.geteuid()>0 and os.environ.get('FASED_DISPOSABLE_VM_ACCEPTANCE')=='1'
assert not a.managed_remote_only or not (a.resume or a.full or a.inspection_only),'managed Remote acceptance requires a fresh single-case VM'
assert not a.network_only or not (a.resume or a.full or a.inspection_only or a.managed_remote_only),'network acceptance requires a fresh single-case VM'
assert not a.owner_browser_only or not (a.resume or a.full or a.inspection_only or a.managed_remote_only or a.network_only),'owner browser acceptance requires a fresh single-case VM'
single_case=a.managed_remote_only or a.network_only or a.owner_browser_only
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
       Cmnd_Alias FASED_FIXTURE_OS = /usr/bin/install, /usr/bin/mkdir, /usr/bin/ln, /usr/bin/mv, /usr/bin/sync, /usr/bin/rm, /usr/bin/cat, /usr/bin/systemctl, /usr/sbin/useradd, /usr/sbin/userdel, /usr/sbin/apparmor_parser, /usr/sbin/nft, /usr/sbin/sshd, /usr/bin/cmp
       ubuntu ALL=(root) NOPASSWD: FASED_FIXTURE_OS
       ubuntu ALL=(root) NOPASSWD: /usr/bin/passwd -S fased-vps-1000
       ubuntu ALL=(root) NOPASSWD: /usr/bin/flock --version
       ubuntu ALL=(root) NOPASSWD: /usr/bin/setpriv --version
       ubuntu ALL=(root) NOPASSWD: /usr/bin/setpriv ^--reuid [1-9][0-9]* --regid [1-9][0-9]* --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- .*$
 packages: [sudo, python3, bubblewrap, apparmor, apparmor-utils, nftables]
 ''')
 run(['xorriso','-as','mkisofs','-quiet','-r','-J','-V','cidata','-o',str(work/'seed.iso'),str(work/'user-data'),str(work/'meta-data')])
 run(['qemu-img','create','-q','-f','qcow2','-F','qcow2','-b',str(image),str(work/'disk.qcow2')])
 run(['qemu-img','resize',str(work/'disk.qcow2'),'12G'])
with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
kvm=os.path.exists('/dev/kvm') and os.access('/dev/kvm',os.R_OK|os.W_OK)
serial=(root/'vm-serial.log').open('wb');stderr=(root/'qemu.stderr').open('wb')
cmd=['qemu-system-x86_64','-machine','q35,accel='+('kvm' if kvm else 'tcg'),'-cpu','host' if kvm else 'max','-smp','2','-m','2048','-nographic','-monitor','none','-qmp',f'unix:{work}/qmp.sock,server=on,wait=off','-drive',f'file={work}/disk.qcow2,if=virtio,format=qcow2','-drive',f'file={work}/seed.iso,if=virtio,format=raw,readonly=on','-netdev',f'user,id=n,hostfwd=tcp:127.0.0.1:{port}-:22','-device','virtio-net-pci,netdev=n','-device','virtio-rng-pci','-smbios','type=1,serial=ds=nocloud']
vm=None;tunnel=None
def start_vm():
 global vm
 vm=subprocess.Popen(cmd,stdout=serial,stderr=stderr)
def stop_vm():
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
if a.resume and not a.inspection_only:disk_snapshot('baseline',True)
start_vm()
def interrupted(signum, frame): raise KeyboardInterrupt("fixture runner interrupted")
signal.signal(signal.SIGTERM, interrupted)
sshbase=['ssh','-F','/dev/null','-o','BatchMode=yes','-o','ControlMaster=no','-o','ControlPath=none','-i',str(key),'-p',str(port),'-o','IdentitiesOnly=yes','-o','StrictHostKeyChecking=accept-new','-o',f'UserKnownHostsFile={work}/known_hosts','-o','ConnectTimeout=4','ubuntu@127.0.0.1']
def ssh(command,timeout=150): return run(sshbase+[command],timeout=timeout).stdout.decode()
def ready(timeout=300):
 end=time.monotonic()+timeout
 while time.monotonic()<end:
  assert vm.poll() is None,'QEMU exited; inspect serial/stderr'
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
  core=repo/'.evidence/p4-fault-reboot/fased-app'
  assert core.is_file() and not core.is_symlink() and core.stat().st_uid==os.getuid(),'owned current test executable required'
  with tarfile.open(bundle,'w') as arc:
   arc.add(core,'.evidence/bin/fased-app')
   if a.network_only:
    network=repo/'.evidence/b01-network/network-tests'
    assert network.is_file() and not network.is_symlink() and network.stat().st_uid==os.getuid(),'owned current network test executable required'
    arc.add(network,'.evidence/bin/network-tests')
    report['inputs']['.evidence/bin/network-tests']=sha(network.read_bytes())
  report['inputs']['.evidence/bin/fased-app']=sha(core.read_bytes())
  save('vm-inputs.json',report['inputs'])
  with bundle.open('rb') as stream:run(sshbase+['umask 077; mkdir -p fased/.evidence/p4-fault-reboot/guest && cd fased && tar -xf -'],stdin=stream,timeout=120)
  if a.owner_browser_only:
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
   for name in ['predecessor-app','predecessor-signer']:arc.add(repo/'.evidence/p4-installed-inspection'/name,'.evidence/p4-installed-inspection/'+name)
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
 if isinstance(e,subprocess.CalledProcessError):
  (root/'guest-failure.stdout').write_bytes(e.stdout or b'')
  (root/'guest-failure.stderr').write_bytes(e.stderr)
  report['commandStdout']=(e.stdout or b'').decode(errors='replace')[-8000:]
  report['commandStderr']=e.stderr.decode(errors='replace')[-8000:]
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
