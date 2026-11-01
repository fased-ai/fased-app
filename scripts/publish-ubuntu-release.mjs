// Publisher library only. No CLI, implicit credentials, retries or activation.
// Input is publication.json from the existing offline release finalizer, bound
// by an independently reviewed SHA256. No keys or signing are accepted here.
import { createHash } from "node:crypto";
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
