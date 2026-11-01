// Publisher library only. No CLI, implicit credentials, retries or activation.
// Input is publication.json from the existing offline release finalizer, bound
// by an independently reviewed SHA256. No keys or signing are accepted here.
import { createHash } from "node:crypto";
import { openPublisherJournal } from "./publisher-journal.mjs";
const sha = (b) => createHash("sha256").update(b).digest("hex");
const hash = /^[a-f0-9]{64}$/;
const oid = /^[a-f0-9]{40}$/;
const number = /^[1-9][0-9]*$/;
const gitBlobSHA = (bytes) =>
  createHash("sha1")
    .update(`blob ${bytes.length}\0`)
    .update(bytes)
    .digest("hex");
const repo = "/repos/fased-ai/fased-app";

export function publicationInput(raw, approval) {
  if (
    !Buffer.isBuffer(raw) ||
    raw.length > 1024 * 1024 ||
    !hash.test(approval?.sha256) ||
    sha(raw) !== approval.sha256 ||
    !oid.test(approval.previousCommit)
  )
    throw Error(
      "Reviewed exact publication and prior metadata commit required",
    );
  const p = JSON.parse(raw);
  const d = p.descriptor;
  if (
    p.version !== 1 ||
    p.signingProfile !== "single-owner-v1" ||
    p.repository !== "fased-ai/fased-app" ||
    p.repositoryId !== "1426211857" ||
    p.ownerId !== "279582504" ||
    p.metadataBranch !== "release-metadata" ||
    !hash.test(p.seedSha256) ||
    !hash.test(p.planSha256) ||
    d?.version !== 3 ||
    d.purpose !== "fased-app-first-install-production:v1" ||
    d.developmentOnly !== false ||
    d.repository !== p.repository ||
    d.channel !== "stable" ||
    d.target?.os !== "linux" ||
    d.target.arch !== "amd64" ||
    !/^[0-9]+\.[0-9]+\.[0-9]+$/.test(d.productVersion) ||
    !oid.test(d.source) ||
    !number.test(d.sequence) ||
    !number.test(d.github?.releaseId) ||
    !number.test(d.github.assetId) ||
    d.github.repositoryId !== p.repositoryId ||
    d.github.name !== "fased-app-linux-amd64.tar.gz" ||
    !hash.test(d.github.sha256) ||
    !Number.isSafeInteger(d.github.size) ||
    d.github.size < 1
  )
    throw Error("Exact finalized single-owner Ubuntu publication required");
  const files = Object.entries(p.files ?? {});
  const paths = [
    /^[1-9][0-9]*\.root\.json$/,
    /^timestamp\.json$/,
    /^[1-9][0-9]*\.snapshot\.json$/,
    /^[1-9][0-9]*\.targets\.json$/,
    /^targets\/production-bootstrap\/[a-f0-9]{64}\.descriptor\.json$/,
    /^targets\/provenance\/[a-f0-9]{64}\.sigstore\.json$/,
  ];
  if (
    files.length !== paths.length ||
    paths.some(
      (pattern) => files.filter(([name]) => pattern.test(name)).length !== 1,
    )
  )
    throw Error("Exact finalized public metadata inventory required");
  let timestamp;
  for (const [name, encoded] of files) {
    if (typeof encoded !== "string") throw Error("Public bytes required");
    const bytes = Buffer.from(encoded, "base64");
    if (
      !bytes.length ||
      bytes.length > 65536 ||
      bytes.toString("base64") !== encoded
    )
      throw Error("Bounded canonical metadata required");
    const doc = JSON.parse(bytes);
    if (name === "timestamp.json") timestamp = doc;
    if (
      /\.root\.json$/.test(name) ||
      /\.targets\.json$/.test(name) ||
      /\.snapshot\.json$/.test(name) ||
      name === "timestamp.json"
    ) {
      if (
        !Number.isFinite(Date.parse(doc.signed?.expires)) ||
        Date.parse(doc.signed.expires) <= Date.now()
      )
        throw Error("Expired finalized metadata");
    }
    if (
      name.endsWith(".descriptor.json") &&
      (sha(bytes) !== name.split("/").at(-1).split(".")[0] ||
        JSON.stringify(doc) !== JSON.stringify(d))
    )
      throw Error("Descriptor bytes changed");
  }
  return { p, files, timestamp };
}

// api receives fixed repository paths and must reject redirects/non-2xx. Callers
// explicitly supply a transport; importing this module never contacts GitHub.
export async function publishFinalized(raw, approval, api, record) {
  raw = Buffer.isBuffer(raw) ? Buffer.from(raw) : raw;
  approval = Object.freeze({ ...approval });
  const { p, files, timestamp } = publicationInput(raw, approval);
  if (typeof api !== "function" || typeof record !== "function")
    throw Error("Explicit authorized publisher transport required");
  const call = (method, path, body) => api(method, repo + path, body);
  const identity = await call("GET", "");
  if (
    String(identity.id) !== p.repositoryId ||
    String(identity.owner?.id) !== p.ownerId ||
    identity.full_name !== p.repository
  )
    throw Error("Publisher repository identity mismatch");
  const d = p.descriptor;
  await verifyAnnotatedTag(
    api,
    d.productVersion,
    d.release?.tagObject,
    d.source,
  );
  const release = await call("GET", `/releases/${d.github.releaseId}`);
  if (
    String(release.id) !== d.github.releaseId ||
    release.draft !== false ||
    release.prerelease !== false ||
    release.immutable !== true ||
    release.tag_name !== `v${d.productVersion}`
  )
    throw Error("Reviewed immutable public release required before metadata");
  const asset = release.assets?.filter(
    (v) => String(v.id) === d.github.assetId,
  );
  if (
    asset?.length !== 1 ||
    asset[0].name !== d.github.name ||
    asset[0].size !== d.github.size ||
    asset[0].state !== "uploaded" ||
    asset[0].digest !== `sha256:${d.github.sha256}`
  )
    throw Error("Published release asset changed");
  const ref = await call("GET", "/git/ref/heads/release-metadata");
  if (
    ref.ref !== "refs/heads/release-metadata" ||
    ref.object?.sha !== approval.previousCommit ||
    ref.object.type !== "commit"
  )
    throw Error("Metadata head changed; re-review, never force");
  const prior = await call("GET", `/git/commits/${approval.previousCommit}`);
  if (prior.sha !== approval.previousCommit || !oid.test(prior.tree?.sha))
    throw Error("Invalid previous metadata commit");
  const oldTree = await call("GET", `/git/trees/${prior.tree.sha}?recursive=1`);
  if (oldTree.truncated !== false || !Array.isArray(oldTree.tree))
    throw Error("Complete metadata tree required");
  for (const [name, content] of files) {
    const existing = oldTree.tree.filter((v) => v.path === name);
    if (
      existing.length > 1 ||
      existing.some((v) => v.type !== "blob" || v.mode !== "100644")
    )
      throw Error("Foreign metadata topology");
    const bytes = Buffer.from(content, "base64");
    const gitSHA = createHash("sha1")
      .update(`blob ${bytes.length}\0`)
      .update(bytes)
      .digest("hex");
    if (
      name !== "timestamp.json" &&
      existing.length === 1 &&
      existing[0].sha !== gitSHA
    )
      throw Error("Versioned metadata cannot be replaced");
    if (name === "timestamp.json" && existing.length === 1) {
      if (!oid.test(existing[0].sha))
        throw Error("Invalid prior timestamp identity");
      const old = await call("GET", `/git/blobs/${existing[0].sha}`);
      if (
        old.encoding !== "base64" ||
        typeof old.content !== "string" ||
        old.content.length > 90000
      )
        throw Error("Bounded prior timestamp required");
      const oldBytes = Buffer.from(old.content, "base64");
      if (
        old.sha !== existing[0].sha ||
        gitBlobSHA(oldBytes) !== existing[0].sha
      )
        throw Error("Prior timestamp blob identity changed");
      const previous = JSON.parse(oldBytes);
      if (
        !Number.isSafeInteger(previous.signed?.version) ||
        timestamp.signed.version <= previous.signed.version
      )
        throw Error("Timestamp rollback/replay refused");
    }
  }
  // Preserve old versioned metadata for catch-up; only current timestamp changes.
  const tree = [];
  for (const [path, content] of files) {
    const blob = await call("POST", "/git/blobs", {
      content,
      encoding: "base64",
    });
    if (blob.sha !== gitBlobSHA(Buffer.from(content, "base64")))
      throw Error("Invalid public metadata blob response");
    tree.push({ path, mode: "100644", type: "blob", sha: blob.sha });
  }
  const nextTree = await call("POST", "/git/trees", {
    base_tree: prior.tree.sha,
    tree,
  });
  if (!oid.test(nextTree.sha))
    throw Error("Invalid public metadata tree response");
  const commit = await call("POST", "/git/commits", {
    message: `Fased ${d.productVersion} release metadata sequence ${d.sequence}`,
    tree: nextTree.sha,
    parents: [approval.previousCommit],
  });
  if (!oid.test(commit.sha)) throw Error("Invalid metadata commit response");
  const committed = await call("GET", `/git/commits/${commit.sha}`);
  if (
    committed.sha !== commit.sha ||
    committed.tree?.sha !== nextTree.sha ||
    committed.parents?.length !== 1 ||
    committed.parents[0].sha !== approval.previousCommit
  )
    throw Error("Metadata commit differs from approved parent/tree");
  const committedTree = await call(
    "GET",
    `/git/trees/${nextTree.sha}?recursive=1`,
  );
  if (committedTree.truncated !== false || !Array.isArray(committedTree.tree))
    throw Error("Complete committed metadata tree required");
  const expectedTree = new Map(
    oldTree.tree.map((entry) => [entry.path, entry]),
  );
  for (const entry of tree) expectedTree.set(entry.path, entry);
  // Recursive trees include directory nodes; verify all leaves, preserving every
  // prior leaf outside this exact publication. Directory hashes change normally.
  const leaves = committedTree.tree.filter((entry) => entry.type !== "tree");
  const expectedLeaves = [...expectedTree.values()].filter(
    (entry) => entry.type !== "tree",
  );
  if (
    leaves.length !== expectedLeaves.length ||
    new Set(leaves.map((e) => e.path)).size !== leaves.length ||
    expectedLeaves.some(
      (e) =>
        !leaves.some(
          (v) =>
            v.path === e.path &&
            v.sha === e.sha &&
            v.mode === e.mode &&
            v.type === e.type,
        ),
    )
  )
    throw Error("Committed metadata bytes/inventory differ");
  // Recheck expiry and head immediately before the sole ref mutation. A competing
  // commit after this check still makes the non-force sibling update fail.
  publicationInput(raw, approval);
  const current = await call("GET", "/git/ref/heads/release-metadata");
  if (current.object?.sha !== approval.previousCommit)
    throw Error("Concurrent metadata publication; no ref update");
  await record({
    phase: "metadata-ref-attempted",
    previousCommit: approval.previousCommit,
    metadataCommit: commit.sha,
    publicationSha256: approval.sha256,
  });
  const updated = await call("PATCH", "/git/refs/heads/release-metadata", {
    sha: commit.sha,
    force: false,
  });
  if (updated.object?.sha !== commit.sha)
    throw Error(
      "Publication outcome uncertain; inspect exact head before retry",
    );
  const readback = await call("GET", "/git/ref/heads/release-metadata");
  if (readback.object?.sha !== commit.sha)
    throw Error("Publication readback uncertain; no retry");
  await record({ phase: "metadata-published", metadataCommit: commit.sha });
  return {
    metadataCommit: commit.sha,
    sequence: d.sequence,
    activation: false,
  };
}

// Production connector is dormant: explicit token/call required, no environment
// credential lookup or CLI. No override of host/TLS/redirects in the public path.
export function githubPublisher(token) {
  if (typeof token !== "string" || token.length < 8 || /\s/.test(token))
    throw Error("Explicit publisher credential required");
  return async (method, path, body) => {
    const upload =
      method === "UPLOAD" &&
      /^\/repos\/fased-ai\/fased-app\/releases\/[1-9][0-9]*\/assets\?name=(fased-app-linux-amd64\.tar\.gz|fased-install-linux-amd64|fased-app-linux-amd64\.provenance\.sigstore\.json|install\.sh)$/.test(
        path,
      ) &&
      Buffer.isBuffer(body);
    if (
      !upload &&
      (!["GET", "POST", "PATCH"].includes(method) ||
        !/^\/repos\/fased-ai\/fased-app(?:\/[A-Za-z0-9._/-]+)?(?:\?recursive=1)?$/.test(
          path,
        ) ||
        path.includes(".."))
    )
      throw Error("Fixed repository publisher route required");
    const response = await fetch(
      `https://${upload ? "uploads.github.com" : "api.github.com"}${path}`,
      {
        method: upload ? "POST" : method,
        redirect: "error",
        signal: AbortSignal.timeout(30000),
        headers: {
          Accept: "application/vnd.github+json",
          Authorization: `Bearer ${token}`,
          "X-GitHub-Api-Version": "2022-11-28",
          ...(body
            ? {
                "Content-Type": upload
                  ? "application/octet-stream"
                  : "application/json",
              }
            : {}),
        },
        ...(body ? { body: upload ? body : JSON.stringify(body) } : {}),
      },
    );
    if (!response.ok) {
      await response.body?.cancel();
      throw Error(`Publisher refused (${response.status}); no automatic retry`);
    }
    const reader = response.body.getReader();
    let size = 0;
    const chunks = [];
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.length;
        if (size > 1024 * 1024)
          throw Error("Publisher response exceeded bound");
        chunks.push(Buffer.from(value));
      }
    } finally {
      await reader.cancel();
    }
    return JSON.parse(Buffer.concat(chunks).toString());
  };
}

async function verifyAnnotatedTag(api, version, tagObject, source) {
  if (!oid.test(tagObject) || !oid.test(source))
    throw Error("Reviewed annotated tag/source required");
  const ref = await api("GET", repo + `/git/ref/tags/v${version}`);
  if (
    ref.ref !== `refs/tags/v${version}` ||
    ref.object?.type !== "tag" ||
    ref.object.sha !== tagObject
  )
    throw Error(
      "Reviewed existing annotated tag required; never create/move tag",
    );
  const tag = await api("GET", repo + `/git/tags/${tagObject}`);
  if (
    tag.sha !== tagObject ||
    tag.tag !== `v${version}` ||
    tag.object?.type !== "commit" ||
    tag.object.sha !== source
  )
    throw Error("Release tag/source changed");
}

// The draft asset phase precedes signing because the finalizer binds immutable
// GitHub release/asset IDs. Explicit reviewed inputs; no overwrite or retry.
export async function stageCandidate(
  raw,
  approvedSHA256,
  readArtifact,
  api,
  record,
) {
  if (
    !Buffer.isBuffer(raw) ||
    raw.length > 65536 ||
    sha(raw) !== approvedSHA256 ||
    !hash.test(approvedSHA256)
  )
    throw Error("Reviewed candidate inventory required");
  const c = JSON.parse(raw);
  const names = [
    "fased-app-linux-amd64.tar.gz",
    "fased-install-linux-amd64",
    "fased-app-linux-amd64.provenance.sigstore.json",
    "install.sh",
  ];
  if (
    c.repository !== "fased-ai/fased-app" ||
    c.repositoryId !== "1426211857" ||
    c.ownerId !== "279582504" ||
    !/^[0-9]+\.[0-9]+\.[0-9]+$/.test(c.version) ||
    !oid.test(c.source) ||
    !oid.test(c.tagObject) ||
    !Array.isArray(c.assets) ||
    c.assets.length !== names.length ||
    names.some(
      (name) => c.assets.filter((a) => a.name === name).length !== 1,
    ) ||
    typeof record !== "function"
  )
    throw Error(
      "Exact reviewed candidate and durable operation recorder required",
    );
  // Freeze/check all local files before any remote mutation.
  const assets = [];
  for (const a of c.assets) {
    const bytes = await readArtifact(a.name);
    if (
      !Buffer.isBuffer(bytes) ||
      !Number.isSafeInteger(a.size) ||
      a.size < 1 ||
      a.size > 128 * 1024 * 1024 ||
      bytes.length !== a.size ||
      !hash.test(a.sha256) ||
      sha(bytes) !== a.sha256
    )
      throw Error("Candidate bytes differ from reviewed inventory");
    assets.push({ ...a, bytes: Buffer.from(bytes) });
  }
  const identity = await api("GET", repo);
  if (
    String(identity.id) !== c.repositoryId ||
    String(identity.owner?.id) !== c.ownerId ||
    identity.full_name !== c.repository
  )
    throw Error("Candidate repository changed");
  await verifyAnnotatedTag(api, c.version, c.tagObject, c.source);
  await record({
    phase: "create-draft-attempted",
    tag: `v${c.version}`,
    source: c.source,
  });
  const release = await api("POST", repo + "/releases", {
    tag_name: `v${c.version}`,
    target_commitish: c.source,
    name: `Fased ${c.version}`,
    draft: true,
    prerelease: false,
  });
  if (
    !number.test(String(release.id)) ||
    release.draft !== true ||
    release.tag_name !== `v${c.version}`
  )
    throw Error("Draft outcome uncertain; inspect before retry");
  await record({ phase: "draft-created", releaseId: String(release.id) });
  const ids = [];
  for (const a of assets) {
    await record({
      phase: "asset-upload-attempted",
      releaseId: String(release.id),
      name: a.name,
      sha256: a.sha256,
    });
    const uploaded = await api(
      "UPLOAD",
      repo + `/releases/${release.id}/assets?name=${a.name}`,
      a.bytes,
    );
    if (
      !number.test(String(uploaded.id)) ||
      uploaded.name !== a.name ||
      uploaded.size !== a.size ||
      uploaded.state !== "uploaded" ||
      uploaded.digest !== `sha256:${a.sha256}`
    )
      throw Error("Asset upload outcome uncertain; retain draft and inspect");
    ids.push({ ...a, bytes: undefined, id: String(uploaded.id) });
    await record({
      phase: "asset-uploaded",
      releaseId: String(release.id),
      name: a.name,
      id: String(uploaded.id),
    });
  }
  return {
    releaseId: String(release.id),
    assets: ids,
    published: false,
    activation: false,
  };
}

// Only a finalized, owner-approved release may leave draft state. Failed or
// uncertain attempts retain their draft/journal; never delete or recreate IDs.
export async function releaseCandidate(
  candidate,
  candidateSHA256,
  publication,
  approval,
  api,
  record,
) {
  publication = Buffer.isBuffer(publication)
    ? Buffer.from(publication)
    : publication;
  approval = Object.freeze({ ...approval });
  const { p } = publicationInput(publication, approval);
  if (
    !hash.test(candidateSHA256) ||
    sha(candidate) !== candidateSHA256 ||
    typeof record !== "function"
  )
    throw Error("Reviewed candidate and durable recorder required");
  const c = JSON.parse(candidate),
    d = p.descriptor;
  if (
    c.repository !== p.repository ||
    c.repositoryId !== p.repositoryId ||
    c.ownerId !== p.ownerId ||
    c.version !== d.productVersion ||
    c.source !== d.source ||
    c.tagObject !== d.release?.tagObject ||
    !Array.isArray(c.assets) ||
    c.assets.length !== 4
  )
    throw Error("Candidate differs from finalized release");
  const release = await api("GET", repo + `/releases/${d.github.releaseId}`);
  if (
    release.draft !== true ||
    release.tag_name !== `v${c.version}` ||
    release.prerelease !== false ||
    release.assets?.length !== 4
  )
    throw Error("Exact complete draft required");
  const names = [
    "fased-app-linux-amd64.tar.gz",
    "fased-install-linux-amd64",
    "fased-app-linux-amd64.provenance.sigstore.json",
    "install.sh",
  ];
  for (const name of names) {
    const expected = c.assets.filter((a) => a.name === name),
      found = release.assets.filter((a) => a.name === name);
    if (
      expected.length !== 1 ||
      found.length !== 1 ||
      !number.test(String(found[0].id)) ||
      !hash.test(expected[0].sha256) ||
      found[0].digest !== `sha256:${expected[0].sha256}` ||
      found[0].size !== expected[0].size ||
      found[0].state !== "uploaded"
    )
      throw Error("Draft assets changed");
    if (
      name === d.github.name &&
      (String(found[0].id) !== d.github.assetId ||
        expected[0].sha256 !== d.github.sha256 ||
        expected[0].size !== d.github.size)
    )
      throw Error("Finalized archive identity changed");
  }
  const identity = await api("GET", repo);
  if (
    String(identity.id) !== p.repositoryId ||
    String(identity.owner?.id) !== p.ownerId ||
    identity.full_name !== p.repository
  )
    throw Error("Release repository changed");
  await verifyAnnotatedTag(api, c.version, c.tagObject, c.source);
  publicationInput(publication, approval);
  await record({
    phase: "release-publish-attempted",
    releaseId: d.github.releaseId,
    publicationSha256: approval.sha256,
    assetIds: release.assets.map((a) => ({ name: a.name, id: String(a.id) })),
  });
  const published = await api(
    "PATCH",
    repo + `/releases/${d.github.releaseId}`,
    { draft: false },
  );
  if (
    published.draft !== false ||
    published.immutable !== true ||
    String(published.id) !== d.github.releaseId
  )
    throw Error(
      "Release outcome uncertain or not immutable; inspect, do not retry",
    );
  await record({ phase: "release-published", releaseId: d.github.releaseId });
  return { releaseId: d.github.releaseId, activation: false };
}

// Reuse the same verified publisher functions through private, fsynced state.
// Production callers still supply transport/credentials explicitly.
export async function runRecordedPublisher(input, { api, readArtifact } = {}) {
  const action = input.action,
    mode = input.mode ?? "start";
  if (
    !["stage", "release", "metadata"].includes(action) ||
    !["start", "resume"].includes(mode) ||
    typeof api !== "function"
  )
    throw Error("Explicit publisher action/mode/transport required");
  const candidate = Buffer.isBuffer(input.candidate)
    ? Buffer.from(input.candidate)
    : undefined;
  const publication = Buffer.isBuffer(input.publication)
    ? Buffer.from(input.publication)
    : undefined;
  const approval = Object.freeze({ ...input.approval });
  const candidateSHA256 = input.candidateSHA256;
  if (
    action !== "metadata" &&
    (!candidate || sha(candidate) !== candidateSHA256)
  )
    throw Error("Exact candidate approval required");
  if (action !== "stage") publicationInput(publication, approval);
  const binding = sha(
    JSON.stringify({
      action,
      candidate: candidate ? sha(candidate) : null,
      publication: publication ? sha(publication) : null,
      approval: action === "stage" ? null : approval,
    }),
  );
  const resolutions = JSON.parse(JSON.stringify(input.resolutions ?? {}));
  const journal = openPublisherJournal(input.directory, binding, {
    resume: mode === "resume",
  });
  let cursor = 0;
  const state = journal.state;
  const c = candidate ? JSON.parse(candidate) : null;
  const p = publication ? publicationInput(publication, approval).p : null;
  const finish = (result) => {
    const publicResult =
      action === "stage"
        ? {
            releaseId: result.releaseId,
            assets: result.assets.map((a) => ({
              name: a.name,
              id: a.id,
              size: a.size,
              sha256: a.sha256,
            })),
            published: false,
            activation: false,
          }
        : action === "release"
          ? { releaseId: result.releaseId, activation: false }
          : {
              metadataCommit: result.metadataCommit,
              sequence: result.sequence,
              activation: false,
            };
    state.result = JSON.parse(JSON.stringify(publicResult));
    state.complete = true;
    journal.save();
    return { ...state.result, noOp: false };
  };
  const request = (method, path, body) => ({
    method,
    path,
    requestSha256: sha(
      Buffer.isBuffer(body) ? body : JSON.stringify(body ?? null),
    ),
    body: Buffer.isBuffer(body)
      ? { size: body.length, sha256: sha(body) }
      : JSON.parse(JSON.stringify(body ?? null)),
  });
  const safeResult = (method, path, r) => {
    if (method === "UPLOAD")
      return {
        id: r.id,
        name: r.name,
        size: r.size,
        state: r.state,
        digest: r.digest,
      };
    if (path.endsWith("/releases"))
      return { id: r.id, draft: r.draft, tag_name: r.tag_name };
    if (path.includes("/git/refs/")) return { object: { sha: r.object?.sha } };
    if (method === "PATCH")
      return { id: r.id, draft: r.draft, immutable: r.immutable };
    return { sha: r.sha };
  };
  const identity = async () => {
    const v = await api("GET", repo);
    if (
      String(v.id) !== "1426211857" ||
      String(v.owner?.id) !== "279582504" ||
      v.full_name !== "fased-ai/fased-app"
    )
      throw Error("Publisher repository changed");
  };
  const compareAssets = (release, expected, ids) => {
    if (
      !Array.isArray(release.assets) ||
      release.assets.length !== expected.length
    )
      throw Error("Publisher asset inventory changed");
    for (const a of expected) {
      const found = release.assets.filter((v) => v.name === a.name);
      const id = ids?.find((v) => v.name === a.name)?.id;
      if (
        found.length !== 1 ||
        !number.test(String(found[0].id)) ||
        found[0].state !== "uploaded" ||
        found[0].size !== a.size ||
        found[0].digest !== `sha256:${a.sha256}` ||
        (id !== undefined && String(found[0].id) !== String(id))
      )
        throw Error("Publisher asset bytes/ID changed");
    }
  };
  const inspectCompleted = async (result) => {
    await identity();
    const d = p?.descriptor;
    await verifyAnnotatedTag(
      api,
      c?.version ?? d.productVersion,
      c?.tagObject ?? d.release.tagObject,
      c?.source ?? d.source,
    );
    const id = action === "stage" ? result.releaseId : d.github.releaseId;
    const release = await api("GET", repo + `/releases/${id}`);
    if (
      String(release.id) !== id ||
      release.tag_name !== `v${c?.version ?? d.productVersion}` ||
      release.prerelease !== false
    )
      throw Error("Publisher release identity changed");
    if (action === "stage") {
      if (
        release.draft !== true &&
        !(release.draft === false && release.immutable === true)
      )
        throw Error("Publisher release state changed");
      compareAssets(release, c.assets, result.assets);
    } else {
      if (release.draft !== false || release.immutable !== true)
        throw Error("Publication not confirmed; no replay");
      if (action === "release") {
        const event = state.events.findLast(
          (e) => e.phase === "release-publish-attempted",
        );
        if (!event?.assetIds)
          throw Error("Missing recorded release asset identities");
        compareAssets(release, c.assets, event.assetIds);
      }
      const asset = release.assets?.filter(
        (v) => String(v.id) === d.github.assetId,
      );
      if (
        asset?.length !== 1 ||
        asset[0].name !== d.github.name ||
        asset[0].size !== d.github.size ||
        asset[0].digest !== `sha256:${d.github.sha256}` ||
        asset[0].state !== "uploaded"
      )
        throw Error("Finalized archive identity changed");
    }
    if (action !== "metadata") return;
    const commitStep = state.effects.findLast(
      (e) =>
        e.path === repo + "/git/commits" &&
        e.result?.sha === result.metadataCommit,
    );
    if (!commitStep) throw Error("Missing original metadata commit identity");
    const committed = await api(
      "GET",
      repo + `/git/commits/${result.metadataCommit}`,
    );
    if (
      committed.sha !== result.metadataCommit ||
      committed.tree?.sha !== commitStep.body.tree ||
      committed.parents?.length !== 1 ||
      committed.parents[0].sha !== approval.previousCommit
    )
      throw Error("Recorded metadata commit changed");
    const prior = await api(
      "GET",
      repo + `/git/commits/${approval.previousCommit}`,
    );
    if (prior.sha !== approval.previousCommit || !oid.test(prior.tree?.sha))
      throw Error("Prior metadata identity changed");
    const old = await api(
      "GET",
      repo + `/git/trees/${prior.tree.sha}?recursive=1`,
    );
    const tree = await api(
      "GET",
      repo + `/git/trees/${committed.tree.sha}?recursive=1`,
    );
    if (
      old.truncated !== false ||
      tree.truncated !== false ||
      !Array.isArray(old.tree) ||
      !Array.isArray(tree.tree)
    )
      throw Error("Complete recovery trees required");
    const expected = new Map(
      old.tree.filter((v) => v.type !== "tree").map((v) => [v.path, v]),
    );
    for (const [path, content] of Object.entries(p.files))
      expected.set(path, {
        path,
        type: "blob",
        mode: "100644",
        sha: gitBlobSHA(Buffer.from(content, "base64")),
      });
    const leaves = tree.tree.filter((v) => v.type !== "tree");
    if (
      leaves.length !== expected.size ||
      new Set(leaves.map((v) => v.path)).size !== leaves.length ||
      [...expected.values()].some(
        (v) =>
          !leaves.some(
            (e) =>
              e.path === v.path &&
              e.sha === v.sha &&
              e.type === v.type &&
              e.mode === v.mode,
          ),
      )
    )
      throw Error("Recovered metadata bytes/history changed");
    const ref = await api("GET", repo + "/git/ref/heads/release-metadata");
    if (
      ref.ref !== "refs/heads/release-metadata" ||
      ref.object?.type !== "commit" ||
      ref.object.sha !== result.metadataCommit
    )
      throw Error("Metadata outcome pending/changed; no replay");
  };
  const observed = async (step, body, index) => {
    let id = step.result?.id ?? step.result?.sha;
    const resolution = resolutions[index];
    if (!step.result && resolution) {
      if (resolution.requestSha256 !== step.requestSha256)
        throw Error("Recovery observation does not match original request");
      id = resolution.id;
    }
    let result;
    if (step.path === repo + "/releases") {
      if (!number.test(String(id)))
        throw Error(
          "Uncertain draft creation: explicitly inspect/bind its release ID; never recreate",
        );
      result = await api("GET", repo + `/releases/${id}`);
      if (
        String(result.id) !== String(id) ||
        result.draft !== true ||
        result.prerelease !== false ||
        result.tag_name !== body.tag_name
      )
        throw Error("Original draft identity/state changed");
    } else if (step.method === "UPLOAD") {
      const match = step.path.match(
        /\/releases\/([1-9][0-9]*)\/assets\?name=(.+)$/,
      );
      if (!match) throw Error("Unsupported recovery upload");
      const release = await api("GET", repo + `/releases/${match[1]}`);
      if (String(release.id) !== match[1] || release.draft !== true)
        throw Error("Original upload draft changed");
      const assets = release.assets?.filter((v) => v.name === match[2]);
      if (
        assets?.length !== 1 ||
        assets[0].digest !== `sha256:${step.body.sha256}` ||
        assets[0].size !== step.body.size ||
        assets[0].state !== "uploaded" ||
        (id !== undefined && String(assets[0].id) !== String(id))
      )
        throw Error("Upload outcome unresolved/changed; no replay");
      result = assets[0];
    } else if (step.path === repo + "/git/blobs") {
      const bytes = Buffer.from(body.content, "base64"),
        expected = gitBlobSHA(bytes);
      if (id !== undefined && id !== expected)
        throw Error("Recorded metadata blob identity changed");
      result = await api("GET", repo + `/git/blobs/${expected}`);
      if (
        result.sha !== expected ||
        result.encoding !== "base64" ||
        typeof result.content !== "string" ||
        result.content.length > 90000 ||
        gitBlobSHA(Buffer.from(result.content, "base64")) !== expected
      )
        throw Error("Metadata blob outcome unresolved/changed");
    } else if (step.path === repo + "/git/trees") {
      if (!oid.test(id))
        throw Error(
          "Uncertain tree: explicitly inspect/bind its original SHA; never recreate",
        );
      result = await api("GET", repo + `/git/trees/${id}?recursive=1`);
      if (
        result.sha !== id ||
        result.truncated !== false ||
        !Array.isArray(result.tree)
      )
        throw Error("Original metadata tree changed");
      const prior = await api(
        "GET",
        repo + `/git/trees/${body.base_tree}?recursive=1`,
      );
      if (prior.truncated !== false || !Array.isArray(prior.tree))
        throw Error("Original base tree unavailable");
      const expected = new Map(
        prior.tree.filter((e) => e.type !== "tree").map((e) => [e.path, e]),
      );
      for (const e of body.tree) expected.set(e.path, e);
      const leaves = result.tree.filter((e) => e.type !== "tree");
      if (
        leaves.length !== expected.size ||
        new Set(leaves.map((e) => e.path)).size !== leaves.length ||
        [...expected.values()].some(
          (e) =>
            !leaves.some(
              (v) =>
                v.path === e.path &&
                v.sha === e.sha &&
                v.mode === e.mode &&
                v.type === e.type,
            ),
        )
      )
        throw Error("Recovered tree bytes changed");
    } else if (step.path === repo + "/git/commits") {
      if (!oid.test(id))
        throw Error(
          "Uncertain commit: explicitly inspect/bind its original SHA; never recreate",
        );
      result = await api("GET", repo + `/git/commits/${id}`);
      if (
        result.sha !== id ||
        result.tree?.sha !== body.tree ||
        result.parents?.length !== 1 ||
        result.parents[0].sha !== body.parents[0] ||
        result.message !== body.message
      )
        throw Error("Original metadata commit changed");
    } else
      throw Error(
        "Unresolved mutation; inspect original operation without replay",
      );
    const safe = safeResult(step.method, step.path, result);
    if (step.result && JSON.stringify(safe) !== JSON.stringify(step.result))
      throw Error("Recorded API result changed");
    if (!step.result) {
      step.result = safe;
      step.recovered = true;
      journal.save();
    }
    return safe;
  };
  const wire = async (method, path, body) => {
    if (method === "GET") return api(method, path, body);
    const next = request(method, path, body),
      index = cursor++;
    let step = state.effects[index];
    if (step) {
      if (
        step.method !== next.method ||
        step.path !== next.path ||
        step.requestSha256 !== next.requestSha256
      )
        throw Error("Publisher request order/bytes changed");
      return observed(step, body, index);
    }
    if (index !== state.effects.length)
      throw Error("Publisher request index changed");
    step = { ...next };
    state.effects.push(step);
    journal.save();
    const result = await api(method, path, body);
    step.result = safeResult(method, path, result);
    journal.save();
    return result;
  };
  try {
    if (state.complete) {
      await inspectCompleted(state.result);
      return { ...state.result, noOp: true };
    }
    const final = state.effects.findLast((e) => e.method === "PATCH");
    if (final) {
      const result =
        action === "release"
          ? { releaseId: p.descriptor.github.releaseId, activation: false }
          : action === "metadata"
            ? {
                metadataCommit: final.body.sha,
                sequence: p.descriptor.sequence,
                activation: false,
              }
            : null;
      if (!result) throw Error("Unexpected final publisher mutation");
      await inspectCompleted(result);
      final.result ??=
        action === "release"
          ? { id: result.releaseId, draft: false, immutable: true }
          : { object: { sha: result.metadataCommit } };
      final.recovered = true;
      return finish(result);
    }
    const result =
      action === "stage"
        ? await stageCandidate(
            candidate,
            candidateSHA256,
            readArtifact,
            wire,
            journal.record,
          )
        : action === "release"
          ? await releaseCandidate(
              candidate,
              candidateSHA256,
              publication,
              approval,
              wire,
              journal.record,
            )
          : await publishFinalized(publication, approval, wire, journal.record);
    if (cursor !== state.effects.length)
      throw Error("Recorded operation has unconsumed requests");
    // Verify actual terminal objects before persisting completed/no-op authority.
    await inspectCompleted(result);
    return finish(result);
  } finally {
    journal.close();
  }
}
