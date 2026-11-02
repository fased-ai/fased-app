// Explicit one-shot maintenance through the existing verifier and publisher.
// No timer, environment credentials, key generation or client activation.
import {
  createHash,
  createPrivateKey,
  createPublicKey,
  sign,
} from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync, existsSync } from "node:fs";
import path from "node:path";
import { isDeepStrictEqual } from "node:util";
import {
  openPublisherJournal,
  readPublisherFile,
} from "./publisher-journal.mjs";
import { runRecordedPublisher } from "./publish-ubuntu-release.mjs";
const sha = (v) => createHash("sha256").update(v).digest("hex");
const hash = /^[a-f0-9]{64}$/;
const oid = /^[a-f0-9]{40}$/;

// The Go tool alone derives/validates the plan and exact role payloads. It never
// receives a private key. Its executable is an explicit reviewed local input.
export function refreshVerifier(toolFile, toolSHA256, parent) {
  if (
    !path.isAbsolute(toolFile) ||
    !hash.test(toolSHA256) ||
    sha(readPublisherFile(toolFile, 128 * 1024 * 1024)) !== toolSHA256
  )
    throw Error("Pinned private verifier executable required");
  return async (action, input) => {
    if (!["plan", "snapshot", "timestamp", "finalize"].includes(action))
      throw Error("Online refresh operation required");
    // Recheck on every operation; no unpinned PATH/go-run or executable override.
    if (sha(readPublisherFile(toolFile, 128 * 1024 * 1024)) !== toolSHA256)
      throw Error("Verifier changed");
    const dir = mkdtempSync(path.join(parent, ".verify-"));
    try {
      const put = (name, bytes) => {
        const file = path.join(dir, name);
        writeFileSync(file, bytes, { mode: 0o600, flag: "wx" });
        return file;
      };
      const seedFile = put("seed.json", input.seed);
      const publicationFile = put("publication.json", input.publication);
      const spec = put(
        "refresh.json",
        JSON.stringify({
          version: 1,
          seedFile,
          seedSha256: input.seedSHA256,
          publicationFile,
          publicationSha256: input.publicationSHA256,
          ...(input.plan ? { plan: input.plan } : {}),
        }),
      );
      const approvals = put(
        "approvals.json",
        JSON.stringify(input.approvals ?? {}),
      );
      const command =
        action === "plan"
          ? "refresh-plan"
          : action === "finalize"
            ? "refresh-finalize"
            : "refresh-request";
      const args = [command, "--refresh-file", spec];
      if (action !== "plan") args.push("--approvals-file", approvals);
      if (["snapshot", "timestamp"].includes(action))
        args.push("--role", action);
      return JSON.parse(
        execFileSync(toolFile, args, {
          encoding: "utf8",
          timeout: 30000,
          maxBuffer: 2 * 1024 * 1024,
          stdio: ["ignore", "pipe", "pipe"],
        }),
      );
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  };
}

// Only the two pinned online keys may be opened. Root/Targets never enter this
// interface. The PEM file stays private; only detached signatures are returned.
export function onlineMetadataSigner(seed, seedSHA256, keyFiles) {
  keyFiles = { ...keyFiles };
  if (
    sha(seed) !== seedSHA256 ||
    !keyFiles ||
    Object.keys(keyFiles).sort().join(",") !== "snapshot,timestamp"
  )
    throw Error("Pinned online key files required");
  const parsed = JSON.parse(seed),
    root = JSON.parse(Buffer.from(parsed.root, "base64")).signed;
  if (parsed.signingProfile !== "single-owner-v1")
    throw Error("Explicit single-owner online roles required");
  return async (request) => {
    const role = request.role,
      grant = root.roles[role];
    const payload = Buffer.from(request.payload ?? "", "base64");
    if (
      !["snapshot", "timestamp"].includes(role) ||
      request.version !== 1 ||
      request.seedSha256 !== seedSHA256 ||
      !hash.test(request.planSha256) ||
      request.threshold !== 1 ||
      grant?.threshold !== 1 ||
      grant.keyids?.length !== 1 ||
      JSON.stringify(request.keyIds) !== JSON.stringify(grant.keyids) ||
      payload.length === 0 ||
      payload.length > 65536 ||
      payload.toString("base64") !== request.payload ||
      sha(payload) !== request.payloadSha256 ||
      JSON.parse(payload)._type !== role
    )
      throw Error("Exact online role request required");
    const file = keyFiles[role];
    if (!path.isAbsolute(file))
      throw Error("Explicit private online key path required");
    const bytes = readPublisherFile(file, 8192);
    try {
      const key = createPrivateKey(bytes);
      if (key.asymmetricKeyType !== "ed25519")
        throw Error("Online Ed25519 key required");
      const pub = Buffer.from(
        createPublicKey(key).export({ format: "jwk" }).x,
        "base64url",
      ).toString("hex");
      const id = grant.keyids[0];
      if (root.keys[id]?.keyval?.public !== pub)
        throw Error("Online key differs from pinned role");
      return [{ keyid: id, sig: sign(null, payload, key).toString("hex") }];
    } finally {
      bytes.fill(0);
    }
  };
}

// Bind the historical publication to the exact current metadata commit before
// the first signature. A valid but older signed release is not a refresh grant.
async function verifyBaseline(api, prior, previousCommit) {
  const repo = "/repos/fased-ai/fased-app";
  const identity = await api("GET", repo);
  if (
    String(identity.id) !== "1426211857" ||
    String(identity.owner?.id) !== "279582504" ||
    identity.full_name !== "fased-ai/fased-app"
  )
    throw Error("Refresh repository changed");
  const ref = await api("GET", repo + "/git/ref/heads/release-metadata");
  if (
    ref.ref !== "refs/heads/release-metadata" ||
    ref.object?.type !== "commit" ||
    ref.object.sha !== previousCommit
  )
    throw Error("Refresh metadata head changed");
  const commit = await api("GET", repo + "/git/commits/" + previousCommit);
  if (commit.sha !== previousCommit || !oid.test(commit.tree?.sha))
    throw Error("Refresh baseline commit changed");
  const tree = await api(
    "GET",
    repo + `/git/trees/${commit.tree.sha}?recursive=1`,
  );
  if (
    tree.sha !== commit.tree.sha ||
    tree.truncated !== false ||
    !Array.isArray(tree.tree)
  )
    throw Error("Complete refresh baseline required");
  for (const [name, content] of Object.entries(prior.files)) {
    const bytes = Buffer.from(content, "base64"),
      id = createHash("sha1")
        .update(`blob ${bytes.length}\0`)
        .update(bytes)
        .digest("hex");
    const entries = tree.tree.filter((e) => e.path === name);
    if (
      entries.length !== 1 ||
      entries[0].type !== "blob" ||
      entries[0].mode !== "100644" ||
      entries[0].sha !== id
    )
      throw Error("Refresh baseline bytes changed");
    const blob = await api("GET", repo + "/git/blobs/" + id);
    if (
      blob.sha !== id ||
      blob.encoding !== "base64" ||
      typeof blob.content !== "string" ||
      blob.content.length > 90000 ||
      !Buffer.from(blob.content, "base64").equals(bytes)
    )
      throw Error("Refresh baseline blob changed");
  }
}

// Transport/signing are injected, never created by import. The maintainer entry
// supplies real adapters only after its existing production-trust admission.
export async function runMetadataRefresh(input, { verify, signOnline, api }) {
  // Pin primitives and nested recovery resolutions before the first await.
  input = {
    ...input,
    resolutions: JSON.parse(JSON.stringify(input.resolutions ?? {})),
  };
  const seed = Buffer.from(input.seed ?? ""),
    publication = Buffer.from(input.publication ?? "");
  if (
    !hash.test(input.seedSHA256) ||
    sha(seed) !== input.seedSHA256 ||
    seed.length > 65536 ||
    !hash.test(input.publicationSHA256) ||
    sha(publication) !== input.publicationSHA256 ||
    publication.length > 1024 * 1024 ||
    typeof input.previousCommit !== "string" ||
    !oid.test(input.previousCommit) ||
    !hash.test(input.verifierSHA256) ||
    !["start", "resume"].includes(input.mode)
  )
    throw Error("Pinned refresh input and explicit mode required");
  const prior = JSON.parse(publication);
  const binding = sha(
    JSON.stringify({
      version: 1,
      seed: input.seedSHA256,
      publication: input.publicationSHA256,
      previousCommit: input.previousCommit,
      verifier: input.verifierSHA256,
    }),
  );
  const journal = openPublisherJournal(input.directory, binding, {
    resume: input.mode === "resume",
  });
  const state = journal.state;
  const base = {
    seed,
    seedSHA256: input.seedSHA256,
    publication,
    publicationSHA256: input.publicationSHA256,
  };
  try {
    const plan = await verify("plan", {
      ...base,
      ...(state.refresh ? { plan: state.refresh.plan } : {}),
    });
    if (!state.refresh) {
      if (plan.due !== true) {
        const result = await verify("finalize", {
          ...base,
          plan,
          approvals: {},
        });
        if (
          result.noOp !== true ||
          result.targetsUnchanged !== true ||
          result.published !== false ||
          result.activation !== false ||
          !isDeepStrictEqual(result.publication, prior)
        )
          throw Error("Invalid not-due result");
        state.complete = true;
        state.result = {
          noOp: true,
          offlineRenewalNeeded: plan.offlineRenewalNeeded,
        };
        journal.save();
        return { ...state.result, published: false, activation: false };
      }
      // A previous not-due observation signed/published nothing. Only a due
      // plan is frozen, before the first signing request, for all later resumes.
      await verifyBaseline(api, prior, input.previousCommit);
      state.refresh = { plan, approvals: {}, output: null };
      state.complete = false;
      state.result = null;
      journal.save();
    } else if (!isDeepStrictEqual(plan, state.refresh.plan))
      throw Error("Frozen refresh plan changed");
    const work = state.refresh;
    if (!work.output) {
      for (const role of [
        ...(plan.renewSnapshot ? ["snapshot"] : []),
        "timestamp",
      ]) {
        if (work.approvals[role]) continue;
        const request = await verify(role, {
          ...base,
          plan,
          approvals: work.approvals,
        });
        if (request.role !== role || request.seedSha256 !== input.seedSHA256)
          throw Error("Signing request changed authority");
        work.approvals[role] = await signOnline(request);
        journal.save();
      }
      const result = await verify("finalize", {
        ...base,
        plan,
        approvals: work.approvals,
      });
      if (
        result.targetsUnchanged !== true ||
        result.noOp !== false ||
        result.published !== false ||
        result.activation !== false ||
        result.previousPublicationSha256 !== input.publicationSHA256 ||
        !isDeepStrictEqual(result.publication.descriptor, prior.descriptor)
      )
        throw Error("Invalid refresh result");
      work.output = result;
      journal.save();
    } else {
      // Restart revalidates stored signatures/current offline authority; the
      // previous byte-for-byte output is retained, never regenerated from time.
      const check = await verify("finalize", {
        ...base,
        plan,
        approvals: work.approvals,
      });
      if (!isDeepStrictEqual(check, work.output))
        throw Error("Frozen refresh output changed");
    }
    const raw = Buffer.from(JSON.stringify(work.output.publication));
    const directory = path.join(input.directory, "metadata");
    let mode = "resume";
    if (!work.publisherStarted) {
      if (existsSync(directory))
        throw Error("Unrecorded child publisher state; preserve and inspect");
      // Persist the one permitted child start before handing off. A missing
      // child journal on recovery must refuse, never silently recreate it.
      work.publisherStarted = true;
      journal.save();
      mode = "start";
    }
    const published = await runRecordedPublisher(
      {
        action: "metadata",
        mode,
        directory,
        publication: raw,
        approval: { sha256: sha(raw), previousCommit: input.previousCommit },
        resolutions: input.resolutions,
      },
      { api },
    );
    state.complete = true;
    state.result = {
      metadataCommit: published.metadataCommit,
      sequence: published.sequence,
      publicationSha256: sha(raw),
      offlineRenewalNeeded: plan.offlineRenewalNeeded,
      activation: false,
    };
    journal.save();
    return {
      ...state.result,
      publication: work.output.publication,
      noOp: published.noOp,
      published: true,
    };
  } finally {
    journal.close();
  }
}
