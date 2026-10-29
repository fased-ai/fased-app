// Shared disposable acceptance helpers. No installer, transport override or ordinary runtime entry.
import assert from "node:assert/strict";
export async function enrollmentAPI(
  url,
  session,
  path,
  method = "GET",
  body,
  scrub = String,
) {
  const headers = session ? { Authorization: `Bearer ${session}` } : {};
  if (method !== "GET") {
    const state = await (
      await fetch(url + "/api/v1/auth/state", { headers })
    ).json();
    headers["X-CSRF-Token"] = state.csrfToken;
    headers["Content-Type"] = "application/json";
    headers.Origin = url;
  }
  const response = await fetch(url + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  });
  const value = await response.json();
  if (!response.ok)
    throw new Error(`fixture API ${path}: ${scrub(JSON.stringify(value))}`);
  return value;
}
export const snapshotCode = String.raw`
import hashlib,json,os,sqlite3,sys
p=sys.argv[1];hold=sys.argv[2]=='hold'
assert os.geteuid()!=0
c=sqlite3.connect('file:'+p+'/core.sqlite?mode='+('rw' if hold else 'ro'),uri=True)
if hold:
    raw,rev=c.execute('SELECT document,revision FROM agent_workspace WHERE id=1').fetchone();d=json.loads(raw)
    q=d['aiRequests'][-1];assert q['state']=='prepared'
    q['state']='unknown';q['dispatchBoot']='0123456789abcdef0123456789abcdef';q['heldMicrousd']=q['limits']['budgetMicrousd']
    c.execute('UPDATE agent_workspace SET document=?,revision=revision+1 WHERE id=1',(json.dumps(d,separators=(',',':')),));c.commit()
c.execute('BEGIN')
rows={}
for name,query in {'instance':'SELECT instance_id,setup,revision,hex(password_salt),hex(password_hash) FROM instance WHERE id=1','accounts':'SELECT document,revision FROM accounts WHERE id=1','agents':'SELECT document,revision FROM agent_workspace WHERE id=1','history':'SELECT id,profile,source_id,kind,record,sha256 FROM agent_history ORDER BY id','pause':'SELECT phase,document FROM update_pause WHERE id=1'}.items():
    rows[name]=hashlib.sha256(json.dumps(c.execute(query).fetchall(),separators=(',',':')).encode()).hexdigest()
doc=json.loads(c.execute('SELECT document FROM agent_workspace WHERE id=1').fetchone()[0])
out={'instanceId':c.execute('SELECT instance_id FROM instance WHERE id=1').fetchone()[0],'schema':c.execute('PRAGMA user_version').fetchone()[0],'rows':rows,'vaults':{},'holds':[(q['id'],q['state'],q['heldMicrousd']) for q in doc['aiRequests'] if q['state']=='unknown'],'counts':{k:len(doc[k]) for k in ['profiles','sessions','messages','memories','tasks','aiRequests']}}
for name in sorted(os.listdir(p)):
    if name=='ai-credentials.json' or name.startswith('rpc-credential-'):
        with open(p+'/'+name,'rb') as f:out['vaults'][name]=hashlib.sha256(f.read()).hexdigest()
c.close();print(json.dumps(out,separators=(',',':')))
`;

export async function seedEnrollmentSource({
  url,
  claimCode,
  password,
  key,
  vaultPassword,
  rememberSecret = () => {},
  log = () => {},
  scrub = String,
}) {
  const claimed = await enrollmentAPI(
    url,
    "",
    "/api/v1/auth/claim",
    "POST",
    { code: claimCode, password },
    scrub,
  );
  const session = claimed.sessionToken;
  assert.equal(typeof session, "string");
  rememberSecret(session);
  let view = await enrollmentAPI(url, session, "/api/v1/agents");
  const change = async (body) => {
    view = await enrollmentAPI(
      url,
      session,
      "/api/v1/agents",
      "POST",
      { revision: view.revision, profileId: view.profileId, ...body },
      scrub,
    );
    return view;
  };
  await change({ action: "session-create", name: "Enrollment conversation" });
  const sessionId = view.sessions.at(-1).id;
  await change({
    action: "message-add",
    id: sessionId,
    text: "/remember Enrollment fixture preference",
  });
  await change({
    action: "task-create",
    name: "Enrollment paused reminder",
    text: "Synthetic fixture reminder",
    intervalSeconds: 3600,
    maxRuns: 2,
  });
  await change({ action: "task-pause", id: view.tasks.at(-1).id });
  await change({
    action: "ai-prepare",
    id: sessionId,
    text: "Synthetic unfinished request",
    limits: {
      maxInputTokens: 8000,
      maxOutputTokens: 128,
      deadlineSeconds: 5,
      budgetMicrousd: 10000,
    },
  });
  try {
    await enrollmentAPI(
      url,
      session,
      "/api/v1/ai/connection",
      "PUT",
      {
        action: "save",
        credentialMode: "test",
        key,
        passphrase: vaultPassword,
      },
      scrub,
    );
  } catch (e) {
    log(scrub(e.message));
  }
  return { session, view };
}
