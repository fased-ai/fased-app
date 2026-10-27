#!/usr/bin/env python3
# Controlled role time only; never changes the VM or host system clock.
import json, os, pathlib, pty, re, select, signal, subprocess, sys, time
P=pathlib.Path;root=P(sys.argv[1]);case=sys.argv[2];role,change=case.split('-',1);assert role in ['owner','signer'];assert os.getuid()>0
p=root/'fixture-inspection-v1/inputs'/(role+'-clock.json');clock=json.loads(p.read_text());policy=json.loads((p.parent/(role+'-policy.json')).read_text());before=dict(clock)
if change=='regression':clock['utc']-=1;clock['revision']=str(int(clock['revision'])+1)
elif change=='epoch':clock['epoch']='a'*32
elif change=='uncertain':clock['confident']=False
elif change=='equal-expiry':clock['utc']=policy['notAfter'];clock['revision']=str(int(clock['revision'])+1)
elif change=='skew':clock['utc']+=31;clock['revision']=str(int(clock['revision'])+1)
else:raise ValueError('closed time case')
tmp=P('.evidence/p4-fault-reboot/guest/clock-input.json');tmp.write_text(json.dumps(clock));tmp.chmod(0o600)
subprocess.run(['/usr/bin/sudo','-n','/usr/bin/install','-o','0','-g','0','-m','0644','--',str(tmp),str(p)],check=True)
pid,fd=pty.fork()
if pid==0:os.execv(str(root/'fased-app'),[str(root/'fased-app'),'custody','fixture-inspection','inspect','--disposable-fixture','--prepare-host'])
seen=b'';sent=False;done=None;deadline=time.monotonic()+15
try:
 while time.monotonic()<deadline:
  rr,_,_=select.select([fd],[],[],.05)
  if rr:
   try:b=os.read(fd,65536)
   except OSError:break
   if not b:break
   seen+=b;m=re.search(rb'Type (INSPECT FIXTURE [a-f0-9]{64}):',seen)
   if m and not sent:os.write(fd,m.group(1)+b'\n');sent=True
  q,status=os.waitpid(pid,os.WNOHANG)
  if q:done=os.waitstatus_to_exitcode(status);break
finally:
 if done is None:
  if time.monotonic()>=deadline:os.killpg(pid,signal.SIGKILL)
  _,status=os.waitpid(pid,0);done=os.waitstatus_to_exitcode(status)
 os.close(fd)
assert done!=0 and b'"exchange":' not in seen,seen.decode(errors='replace')
assert b'protected inspection terminal required' not in seen,'invalid no-TTY negative control'
print(json.dumps({'result':'PASS','case':case,'before':before,'after':clock,'hostClockChanged':False,'terminalReviewSeen':sent,'exitCode':done,'output':seen.decode(errors='replace')[-1200:]}))
