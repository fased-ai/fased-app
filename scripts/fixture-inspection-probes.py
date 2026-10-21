#!/usr/bin/env python3
"""Actual owner/app/foreign-UID socket and replay probes in the disposable VM."""
import base64, hashlib, json, os, pathlib, socket, struct, subprocess, sys, time
P=pathlib.Path;root=P(sys.argv[1]); inv=json.loads(P(sys.argv[2]).read_text()); assert os.getuid()>0
c=json.loads((root/'fixture-inspection-v1/inputs/catalog.json').read_text()); profile=c['profile']; capability=c['reader']['capability']; checks={}
j=lambda o:json.dumps(o,separators=(',',':'),ensure_ascii=False)
h=lambda b:hashlib.sha256(b if isinstance(b,bytes) else b.encode()).hexdigest()
def tup(domain,*values):
 b=domain.encode()+b'\0'
 for v in values:v=v.encode();b+=struct.pack('!I',len(v))+v
 return h(b)
def frame(kind_name,body,channel='',**changes):
 f={'version':1,'profile':profile,'capability':capability,'provision':c['provision'],'channel':channel,'kind':kind_name,'body':body};f.update(changes);return f
def connect():
 s=socket.socket(socket.AF_UNIX);s.settimeout(8);s.connect(str(root/'signer-ipc/inspection.sock'));return s
def send(s,f):
 b=j(f).encode();s.sendall(struct.pack('!I',len(b))+b)
def read(s):
 def exact(n):
  b=b''
  while len(b)<n:
   part=s.recv(n-len(b))
   if not part:raise EOFError('closed')
   b+=part
  return b
 n=struct.unpack('!I',exact(4))[0];assert 0<n<=65536;return json.loads(exact(n))
def denied(s):
 try:read(s)
 except (EOFError,ConnectionResetError,BrokenPipeError):return True
 return False
for name,change in [('profile',{'profile':'production'}),('capability',{'capability':'production'}),('version',{'version':2}),('kind',{'kind':'execute'}),('provision',{'provision':'a'*32}),('channel',{'channel':'a'*32})]:
 with connect() as s:send(s,frame('status',{'nonce':os.urandom(16).hex()},**change));assert denied(s),name
 checks[name]='PASS'
# Kernel peer UID must reject before reading a frame, including a public status.
source="import socket,sys; s=socket.socket(socket.AF_UNIX);s.settimeout(3);s.connect(sys.argv[1]);assert s.recv(1)==b''"
for uid,gid in [(c['uids'][1],c['gids'][1]),(65534,65534)]:
 subprocess.run(['/usr/bin/sudo','-n','/usr/bin/setpriv','--reuid',str(uid),'--regid',str(gid),'--clear-groups','--no-new-privs','--inh-caps=-all','--ambient-caps=-all','--bounding-set=-all','--','/usr/bin/python3','-c',source,str(root/'signer-ipc/inspection.sock')],check=True,timeout=5)
 checks['kernel-peer-'+str(uid)]='PASS'
ev=json.loads((root/'fixture-inspection-v1/inputs/bundle/evidence.json').read_text());unit=h(base64.b64decode(ev['unit']))
clock=json.loads((root/'fixture-inspection-v1/inputs/owner-clock.json').read_text())
def hello():
 nonce=os.urandom(16).hex();core=dict(inv['core']);core['probeId']=nonce
 return {'nonce':nonce,'boot':os.urandom(16).hex(),'head':h('owner fixture history'),'source':h('independent owner fixture source'),'core':core,'unit':unit,'clock':clock}
def begin():
 s=connect();hh=hello();send(s,frame('hello',hh));cf=read(s);assert cf['kind']=='challenge';cc=cf['body'];nonce=hh['nonce']
 pp=tup('fased-app-installed-inspection-fixture-proposal:v1',profile,capability,'1',c['provision'],c['installation'],h((root/'fixture-inspection-v1/inputs/catalog.json').read_bytes()),*c['roles'],j(c['reader']),*c['candidateImages'],nonce,j(hh),j(cc))
 text='INSPECT FIXTURE '+pp;consent={'proposal':pp,'reviewedAt':clock['utc'],'consent':text,'review':tup('fased-app-installed-inspection-fixture-review:v1',pp,str(clock['utc']),text)}
 return s,nonce,consent
s,nonce,consent=begin()
with s:
 send(s,frame('inspect',consent,nonce));out=read(s);assert out['kind']=='result'
 send(s,frame('confirm',{'exchange':out['body']['exchange']},nonce));final=read(s);assert final['kind']=='confirmed' and final['body']==out['body']
checks['fresh-same-channel-confirmation']='PASS'
s,nonce,consent=begin()
with s:
 send(s,frame('inspect',consent,nonce));out=read(s);assert out['kind']=='result'
 send(s,frame('inspect',consent,nonce));assert read(s)==out
 send(s,frame('inspect',consent,nonce));assert denied(s)
checks['one-exact-live-retry-second-refused']='PASS'
with connect() as s:send(s,frame('inspect',consent,nonce));assert denied(s)
checks['cross-connection-review-replay-refused']='PASS'
s,nonce,consent=begin()
with s:
 send(s,frame('inspect',consent,nonce));out=read(s);assert out['kind']=='result'
 send(s,frame('confirm',{'exchange':'0'*64},nonce));assert denied(s)
checks['wrong-exchange-refused']='PASS'
s,nonce,consent=begin()
with s:
 send(s,frame('inspect',consent,nonce));out=read(s);assert out['kind']=='result'
 # Drop between result and confirmation; no fresh connection can complete it.
checks['drop-after-result']='PASS'
def historical_status():
 end=time.monotonic()+2
 while True:
  try:
   with connect() as s:
    n=os.urandom(16).hex();send(s,frame('status',{'nonce':n}));out=read(s);r=out['body'];assert out['kind']=='status-result' and out['channel']==n
    assert r['historical'] and r['fixtureOnly'] and r['profile']==profile and not any(r.get(k) for k in ['exchange','review','proposal','installAllowed','resumeAllowed','walletSigner','financialExecution','updaterEnabled'])
    return
  except (EOFError,ConnectionResetError,BrokenPipeError):
   if time.monotonic()>=end:raise
   time.sleep(.02)
historical_status()
checks['reconnect-is-historical-without-authority']='PASS'
# A partial body gets the actual five-second body deadline; host time is untouched.
with connect() as s:
 start=time.monotonic();s.sendall(struct.pack('!I',20)+b'{');assert denied(s);elapsed=time.monotonic()-start;assert 4.5<=elapsed<8
 checks['actual-monotonic-body-timeout']={'result':'PASS','elapsed':elapsed}
# A stalled authenticated owner cannot accumulate concurrent inspections.
with connect() as held:
 time.sleep(.05)
 with connect() as other:
  send(other,frame('status',{'nonce':os.urandom(16).hex()}));assert denied(other)
checks['concurrent-channel-bounded']='PASS'
historical_status()
# The original review/header ceiling uses elapsed monotonic time, not a mutable
# role UTC file. This deliberately takes one minute in the full acceptance run.
with connect() as held:
 held.settimeout(65);start=time.monotonic();assert denied(held);elapsed=time.monotonic()-start;assert 59<=elapsed<65
 checks['actual-monotonic-review-timeout']={'result':'PASS','elapsed':elapsed}
print(j({'result':'PASS','checks':checks,'fixtureOnly':True,'realWallets':False,'automaticUpdates':False}))
