// Local publisher keys only. No transport, wallet, scheduler or automatic unlock.
import {
  createHash,
  randomBytes,
  generateKeyPairSync,
  createPrivateKey,
  createPublicKey,
  createCipheriv,
  createDecipheriv,
  scryptSync,
  sign,
  verify,
} from "node:crypto";
import {
  constants,
  openSync,
  closeSync,
  writeFileSync,
  readFileSync,
  fsyncSync,
  existsSync,
  mkdirSync,
  lstatSync,
  realpathSync,
  linkSync,
  unlinkSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { ReadStream } from "node:tty";
import { Writable } from "node:stream";
import { createInterface } from "node:readline";
import {
  openPublisherJournal,
  readPublisherFile,
  ensurePublisherDirectory,
} from "./publisher-journal.mjs";

const roles = ["root", "targets", "snapshot", "timestamp"];
const purpose = "fased-local-release-signing:v1",
  profile = "single-owner-v1",
  repository = "fased-ai/fased-app";
const max = 65536,
  hash = /^[a-f0-9]{64}$/;
const sha = (b) => createHash("sha256").update(b).digest("hex");
const canonical = (v) =>
  JSON.stringify(
    v && typeof v === "object"
      ? Array.isArray(v)
        ? v.map((x) => JSON.parse(canonical(x)))
        : Object.fromEntries(
            Object.keys(v)
              .sort()
              .map((k) => [k, JSON.parse(canonical(v[k]))]),
          )
      : v,
  );
const bytes = (v) => Buffer.from(canonical(v) + "\n");
const exact = (v, fields) =>
  v &&
  typeof v === "object" &&
  !Array.isArray(v) &&
  Object.keys(v).sort().join(",") === fields.slice().sort().join(",");
function strict(b) {
  if (b.length > max) throw Error("Local signing document exceeds bound");
  const v = JSON.parse(b);
  if (!bytes(v).equals(b))
    throw Error("Canonical local signing document required");
  return v;
}
function ordinary() {
  if (process.platform !== "linux" || process.getuid?.() === 0)
    throw Error("Ordinary Linux maintainer required");
}
function syncDir(p) {
  const fd = openSync(
    p,
    constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
  );
  try {
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}
const privateDir = ensurePublisherDirectory;
function keyClass(v) {
  if (!["disposable", "production"].includes(v))
    throw Error("Explicit signing key class required");
  return v;
}
function passKey(pass, salt) {
  if (!Buffer.isBuffer(pass) || pass.length < 16 || pass.length > 1024)
    throw Error("Private passphrase must contain 16–1024 UTF8 bytes");
  const copy = Buffer.from(pass);
  try {
    return scryptSync(copy, salt, 32, {
      N: 131072,
      r: 8,
      p: 1,
      maxmem: 256 * 1024 * 1024,
    });
  } finally {
    copy.fill(0);
  }
}
function decode(v, size) {
  if (typeof v !== "string")
    throw Error("Canonical encoded key material required");
  const b = Buffer.from(v, "base64");
  if (b.toString("base64") !== v || (size !== undefined && b.length !== size))
    throw Error("Invalid encoded key material");
  return b;
}
function inventory(keys) {
  const result = {};
  for (const role of roles) {
    const k = keys[role];
    if (k?.asymmetricKeyType !== "ed25519")
      throw Error("Four distinct Ed25519 release keys required");
    const pub = Buffer.from(
      createPublicKey(k).export({ format: "jwk" }).x,
      "base64url",
    ).toString("hex");
    const key = {
      keytype: "ed25519",
      scheme: "ed25519",
      keyval: { public: pub },
    };
    result[role] = { keyid: sha(Buffer.from(canonical(key))), key };
  }
  if (new Set(Object.values(result).map((v) => v.keyid)).size !== 4)
    throw Error("Signing roles cannot share a key");
  return { version: 1, repository, signingProfile: profile, roles: result };
}
function validIdentity(i) {
  if (
    !exact(i, ["version", "repository", "signingProfile", "roles"]) ||
    i.version !== 1 ||
    i.repository !== repository ||
    i.signingProfile !== profile ||
    !exact(i.roles, roles)
  )
    throw Error("Release identity required");
  for (const role of roles) {
    const r = i.roles[role];
    if (
      !exact(r, ["keyid", "key"]) ||
      !exact(r.key, ["keytype", "scheme", "keyval"]) ||
      !exact(r.key.keyval, ["public"]) ||
      r.key.keytype !== "ed25519" ||
      r.key.scheme !== "ed25519" ||
      !hash.test(r.key.keyval.public) ||
      r.keyid !== sha(Buffer.from(canonical(r.key)))
    )
      throw Error("Invalid role identity");
  }
  if (new Set(roles.map((r) => i.roles[r].keyid)).size !== 4)
    throw Error("Distinct release roles required");
}
const identityHash = (i) => sha(bytes(i));
function rootFor(keys, identity, now) {
  const signed = {
    _type: "root",
    spec_version: "1.0.31",
    version: 1,
    expires: new Date(now + 364 * 86400000).toISOString(),
    consistent_snapshot: true,
    keys: {},
    roles: {},
  };
  for (const role of roles) {
    const r = identity.roles[role];
    signed.keys[r.keyid] = r.key;
    signed.roles[role] = { keyids: [r.keyid], threshold: 1 };
  }
  return {
    signed,
    signatures: [
      {
        keyid: identity.roles.root.keyid,
        sig: sign(null, Buffer.from(canonical(signed)), keys.root).toString(
          "hex",
        ),
      },
    ],
  };
}
function seal(keys, kind, pass) {
  const identity = inventory(keys),
    salt = randomBytes(32),
    nonce = randomBytes(12);
  const header = {
    version: 1,
    purpose,
    repository,
    signingProfile: profile,
    keyClass: keyClass(kind),
    identity,
    kdf: {
      name: "scrypt",
      N: 131072,
      r: 8,
      p: 1,
      salt: salt.toString("base64"),
    },
    cipher: { name: "aes-256-gcm", nonce: nonce.toString("base64") },
  };
  const rawKeys = {};
  const exported = [];
  let key, plain;
  try {
    for (const role of roles) {
      const b = keys[role].export({ format: "der", type: "pkcs8" });
      exported.push(b);
      rawKeys[role] = b.toString("base64");
    }
    plain = bytes({
      version: 1,
      identitySHA256: identityHash(identity),
      keys: rawKeys,
    });
    key = passKey(pass, salt);
    const cipher = createCipheriv("aes-256-gcm", key, nonce);
    cipher.setAAD(bytes(header));
    const encrypted = Buffer.concat([cipher.update(plain), cipher.final()]);
    return bytes({
      ...header,
      ciphertext: encrypted.toString("base64"),
      tag: cipher.getAuthTag().toString("base64"),
    });
  } finally {
    key?.fill(0);
    plain?.fill(0);
    for (const b of exported) b.fill(0);
  }
}
function unseal(raw, pass, expectedIdentitySHA256, expectedClass) {
  ordinary();
  if (!hash.test(expectedIdentitySHA256))
    throw Error("Independently retained public identity digest required");
  const e = strict(raw);
  if (
    !exact(e, [
      "version",
      "purpose",
      "repository",
      "signingProfile",
      "keyClass",
      "identity",
      "kdf",
      "cipher",
      "ciphertext",
      "tag",
    ]) ||
    e.version !== 1 ||
    e.purpose !== purpose ||
    e.repository !== repository ||
    e.signingProfile !== profile ||
    e.keyClass !== keyClass(expectedClass) ||
    !exact(e.kdf, ["name", "N", "r", "p", "salt"]) ||
    e.kdf.name !== "scrypt" ||
    e.kdf.N !== 131072 ||
    e.kdf.r !== 8 ||
    e.kdf.p !== 1 ||
    !exact(e.cipher, ["name", "nonce"]) ||
    e.cipher.name !== "aes-256-gcm"
  )
    throw Error("Unsupported local signing envelope");
  validIdentity(e.identity);
  if (identityHash(e.identity) !== expectedIdentitySHA256)
    throw Error("Local signing identity changed");
  const header = { ...e };
  delete header.tag;
  delete header.ciphertext;
  let derived, plain;
  const keys = {};
  try {
    derived = passKey(pass, decode(e.kdf.salt, 32));
    const cipher = createDecipheriv(
      "aes-256-gcm",
      derived,
      decode(e.cipher.nonce, 12),
    );
    cipher.setAAD(bytes(header));
    cipher.setAuthTag(decode(e.tag, 16));
    plain = Buffer.concat([
      cipher.update(decode(e.ciphertext)),
      cipher.final(),
    ]);
    const v = strict(plain);
    if (
      !exact(v, ["version", "identitySHA256", "keys"]) ||
      v.version !== 1 ||
      v.identitySHA256 !== expectedIdentitySHA256 ||
      !exact(v.keys, roles)
    )
      throw Error("Invalid encrypted role inventory");
    for (const role of roles) {
      const der = decode(v.keys[role]);
      try {
        keys[role] = createPrivateKey({
          key: der,
          format: "der",
          type: "pkcs8",
        });
      } finally {
        der.fill(0);
      }
    }
    if (!bytes(inventory(keys)).equals(bytes(e.identity)))
      throw Error("Private roles differ from public identity");
    return { keys, identity: e.identity };
  } catch {
    for (const r of roles) delete keys[r];
    throw Error("Invalid local keystore or passphrase");
  } finally {
    derived?.fill(0);
    plain?.fill(0);
  }
}

// Every filesystem write is registered before dispatch. Temporary ciphertext is
// classified by that journal, so interrupted partial writes can be safely replaced.
function persist(journal, target, data) {
  privateDir(path.dirname(target));
  journal.state.files ??= {};
  const records = journal.state.files;
  const wanted = sha(data);
  if (!records[target]) {
    records[target] = {
      sha256: wanted,
      temp: path.join(
        path.dirname(target),
        ".local-signing-" + randomBytes(16).toString("hex"),
      ),
    };
    journal.save();
  }
  const r = records[target];
  if (r.sha256 !== wanted) throw Error("Signing write bytes changed");
  const temp = r.temp;
  if (
    path.dirname(temp) !== path.dirname(target) ||
    !/^\.local-signing-[a-f0-9]{32}$/.test(path.basename(temp))
  )
    throw Error("Invalid signing recovery locator");
  if (existsSync(target)) {
    if (existsSync(temp)) {
      const a = lstatSync(target),
        b = lstatSync(temp);
      if (
        a.ino !== b.ino ||
        a.dev !== b.dev ||
        a.nlink !== 2 ||
        b.nlink !== 2 ||
        !a.isFile() ||
        a.uid !== process.getuid() ||
        (a.mode & 0o077) !== 0 ||
        sha(readFileSync(target)) !== wanted
      )
        throw Error("Uncertain signing file; preserve and inspect");
      unlinkSync(temp);
      syncDir(path.dirname(target));
    }
    if (sha(readPublisherFile(target, max)) !== wanted)
      throw Error("Existing signing file differs; never overwrite");
    return;
  }
  if (existsSync(temp)) {
    const b = readPublisherFile(temp, max);
    if (sha(b) !== wanted) {
      unlinkSync(temp);
      syncDir(path.dirname(target));
    }
  }
  if (!existsSync(temp)) {
    const fd = openSync(
      temp,
      constants.O_CREAT |
        constants.O_EXCL |
        constants.O_WRONLY |
        constants.O_NOFOLLOW,
      0o600,
    );
    try {
      writeFileSync(fd, data);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
  }
  // Re-sync the verified inode even when a previous attempt wrote all bytes
  // but died before its fsync. Directory durability does not persist file data.
  const ready = openSync(temp, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    fsyncSync(ready);
  } finally {
    closeSync(ready);
  }
  linkSync(temp, target);
  unlinkSync(temp);
  syncDir(path.dirname(target));
}
function parentDir(p) {
  ordinary();
  if (typeof p !== "string" || !path.isAbsolute(p) || path.resolve(p) !== p)
    throw Error("Canonical signing parent required");
  const st = lstatSync(p);
  if (
    !st.isDirectory() ||
    st.isSymbolicLink() ||
    ![0, process.getuid()].includes(st.uid) ||
    (st.mode & 0o022) !== 0 ||
    realpathSync(p) !== p
  )
    throw Error("Owned non-writable signing parent required");
}
function productionLocation(p, kind) {
  if (kind === "production") {
    const repo = fileURLToPath(new URL("../", import.meta.url));
    if (p === repo.slice(0, -1) || p.startsWith(repo))
      throw Error("Production release keys cannot live in this repository");
  }
}
function validateLocations(directory, archiveDirectory, kind) {
  parentDir(path.dirname(directory));
  parentDir(path.dirname(archiveDirectory));
  if (
    directory === archiveDirectory ||
    directory.startsWith(archiveDirectory + "/") ||
    archiveDirectory.startsWith(directory + "/")
  )
    throw Error("Separate local working/recovery directories required");
  productionLocation(directory, kind);
  productionLocation(archiveDirectory, kind);
}
export function setupLocalSigning(
  input,
  {
    generate = () => generateKeyPairSync("ed25519").privateKey,
    afterArchive = () => {},
  } = {},
) {
  const { directory, archiveDirectory, passphrase } = input,
    kind = keyClass(input.keyClass);
  validateLocations(directory, archiveDirectory, kind);
  const binding = sha(
    bytes({
      operation: "setup-local-signing",
      directory,
      archiveDirectory,
      keyClass: kind,
    }),
  );
  const journal = openPublisherJournal(directory, binding, {
    resume: input.mode === "resume",
  });
  const wasComplete = journal.state.complete;
  let opened;
  try {
    if (!journal.state.localSigning) {
      const keys = {};
      try {
        for (const r of roles) keys[r] = generate(r);
        const artifact = seal(keys, kind, passphrase);
        journal.state.localSigning = {
          artifact: artifact.toString("base64"),
          identity: inventory(keys),
          initialRoot: rootFor(keys, inventory(keys), Date.now()),
        };
        journal.save();
      } finally {
        for (const r of roles) delete keys[r];
      }
    }
    const w = journal.state.localSigning,
      raw = decode(w.artifact);
    opened = unseal(raw, passphrase, identityHash(w.identity), kind);
    privateDir(archiveDirectory, true);
    const archiveFile = path.join(
      archiveDirectory,
      `keys-${identityHash(w.identity)}-${sha(raw)}.enc`,
    );
    persist(journal, archiveFile, raw);
    afterArchive();
    const storeFile = path.join(directory, "keys.enc");
    persist(journal, storeFile, raw);
    persist(
      journal,
      path.join(directory, "identity.json"),
      bytes({
        identity: w.identity,
        identitySHA256: identityHash(w.identity),
        initialRoot: w.initialRoot,
      }),
    );
    const result = {
      version: 1,
      keyClass: kind,
      identity: w.identity,
      identitySHA256: identityHash(w.identity),
      initialRoot: w.initialRoot,
      storeFile,
      archiveFile,
      archiveSHA256: sha(raw),
      published: false,
      activation: false,
    };
    journal.state.complete = true;
    journal.state.result = result;
    journal.save();
    return { ...result, noOp: wasComplete };
  } finally {
    if (opened) for (const r of roles) delete opened.keys[r];
    journal.close();
  }
}
export function recoverLocalSigning(input) {
  const { archiveFile, archiveSHA256, identitySHA256, directory, passphrase } =
      input,
    kind = keyClass(input.keyClass);
  validateLocations(directory, path.dirname(archiveFile), kind);
  if (!hash.test(archiveSHA256))
    throw Error("Pinned encrypted archive digest required");
  const raw = readPublisherFile(archiveFile, max);
  if (sha(raw) !== archiveSHA256) throw Error("Recovery archive bytes changed");
  const unlocked = unseal(raw, passphrase, identitySHA256, kind);
  try {
    const challenge = Buffer.from(
      "fased-local-signing-recovery:v1:" +
        identitySHA256 +
        ":" +
        randomBytes(32).toString("hex"),
    );
    for (const role of roles) {
      const sig = sign(null, challenge, unlocked.keys[role]);
      if (!verify(null, challenge, createPublicKey(unlocked.keys[role]), sig))
        throw Error("Recovered key proof failed");
    }
    const binding = sha(
      bytes({
        operation: "recover-local-signing",
        archiveSHA256,
        identitySHA256,
        directory,
        keyClass: kind,
      }),
    );
    const journal = openPublisherJournal(directory, binding, {
      resume: input.mode === "resume",
    });
    const wasComplete = journal.state.complete;
    try {
      const storeFile = path.join(directory, "keys.enc");
      persist(journal, storeFile, raw);
      const result = {
        version: 1,
        keyClass: kind,
        identitySHA256,
        storeFile,
        archiveSHA256,
        keysVerified: true,
        published: false,
        activation: false,
      };
      journal.state.result = result;
      journal.state.complete = true;
      journal.save();
      return { ...result, noOp: wasComplete };
    } finally {
      journal.close();
    }
  } finally {
    for (const r of roles) delete unlocked.keys[r];
  }
}

export function unlockLocalSigning({
  storeFile,
  identitySHA256,
  keyClass: kind,
  passphrase,
  seed,
  seedSHA256,
  allowedRoles,
}) {
  productionLocation(path.dirname(storeFile), kind);
  const raw = readPublisherFile(storeFile, max);
  const opened = unseal(raw, passphrase, identitySHA256, kind);
  const selected = [...(allowedRoles ?? [])];
  try {
    if (
      !selected.length ||
      new Set(selected).size !== selected.length ||
      selected.some((r) => !["targets", "snapshot", "timestamp"].includes(r))
    )
      throw Error("Explicit release/online signing scope required");
    if (!hash.test(seedSHA256) || sha(seed) !== seedSHA256)
      throw Error("Pinned signing seed required");
    const s = JSON.parse(seed),
      root = JSON.parse(Buffer.from(s.root, "base64")).signed;
    if (
      s.signingProfile !== profile ||
      !exact(root.roles, roles) ||
      Object.keys(root.keys).length !== 4
    )
      throw Error("Single-owner root required");
    for (const role of roles) {
      const grant = root.roles[role],
        entry = opened.identity.roles[role];
      if (
        grant.threshold !== 1 ||
        grant.keyids?.length !== 1 ||
        grant.keyids[0] !== entry.keyid ||
        canonical(root.keys[entry.keyid]) !== canonical(entry.key)
      )
        throw Error("Keystore differs from pinned root identity");
    }
    for (const role of roles)
      if (!selected.includes(role)) delete opened.keys[role];
    let closed = false,
      plan = null;
    return {
      sign(request) {
        if (closed) throw Error("Local signing session closed");
        const payload = decode(request.payload);
        if (
          request.version !== 1 ||
          !selected.includes(request.role) ||
          request.seedSha256 !== seedSHA256 ||
          !hash.test(request.planSha256) ||
          request.threshold !== 1 ||
          request.keyIds?.length !== 1 ||
          request.keyIds[0] !== opened.identity.roles[request.role].keyid ||
          payload.length > max ||
          sha(payload) !== request.payloadSha256 ||
          JSON.parse(payload)._type !== request.role
        )
          throw Error("Exact scoped release signing request required");
        if (plan !== null && plan !== request.planSha256)
          throw Error("Signing plan changed while unlocked");
        plan = request.planSha256;
        return [
          {
            keyid: request.keyIds[0],
            sig: sign(null, payload, opened.keys[request.role]).toString("hex"),
          },
        ];
      },
      close() {
        if (closed) return;
        closed = true;
        for (const r of roles) delete opened.keys[r];
      },
    };
  } catch (e) {
    for (const r of roles) delete opened.keys[r];
    throw e;
  }
}

// Private terminal only: no command argument, environment variable, history or
// visible echo carries the passphrase. readline restores raw mode when closed.
export async function privateSigningPassphrase(label = "Release passphrase: ") {
  ordinary();
  const fd = openSync("/dev/tty", constants.O_RDWR | constants.O_NOCTTY);
  const input = new ReadStream(fd);
  const output = new Writable({
    write(_b, _e, cb) {
      cb();
    },
  });
  let rl, cancel;
  const interrupted = () => cancel?.(Error("Signing cancelled"));
  for (const signal of ["SIGTERM", "SIGHUP", "SIGINT"])
    process.once(signal, interrupted);
  try {
    rl = createInterface({ input, output, terminal: true, historySize: 0 });
    process.stderr.write(label);
    const answer = await new Promise((resolve, reject) => {
      cancel = reject;
      rl.once("line", resolve);
      rl.once("SIGINT", () => reject(Error("Signing cancelled")));
      rl.once("close", () => reject(Error("Signing terminal closed")));
    });
    const b = Buffer.from(answer, "utf8");
    if (b.length < 16 || b.length > 1024) {
      b.fill(0);
      throw Error("Private passphrase must contain 16–1024 UTF8 bytes");
    }
    return b;
  } finally {
    for (const signal of ["SIGTERM", "SIGHUP", "SIGINT"])
      process.removeListener(signal, interrupted);
    rl?.close();
    input.destroy();
    output.destroy();
    process.stderr.write("\n");
  }
}

// Lazy owner unlock: a not-due refresh never reads/decrypts the keystore or asks
// for a password. One operation unlocks once and closes in its caller's finally.
export function localSigningSession(
  config,
  { getPassphrase = privateSigningPassphrase } = {},
) {
  const frozen = {
    ...config,
    allowedRoles: [...config.allowedRoles],
    seed: Buffer.from(config.seed),
  };
  let session,
    opening,
    closed = false;
  const unlock = () => {
    opening ??= (async () => {
      const pass = await getPassphrase();
      try {
        if (closed) throw Error("Local signing session closed");
        if (!Buffer.isBuffer(pass))
          throw Error("Private passphrase input required");
        session = unlockLocalSigning({ ...frozen, passphrase: pass });
      } finally {
        pass?.fill?.(0);
      }
    })();
    return opening;
  };
  return {
    async sign(request) {
      if (closed) throw Error("Local signing session closed");
      request = JSON.parse(JSON.stringify(request));
      if (!frozen.allowedRoles.includes(request.role))
        throw Error("Scoped local signing role required");
      await unlock();
      if (closed) throw Error("Local signing session closed");
      return session.sign(request);
    },
    close() {
      closed = true;
      session?.close();
      session = null;
    },
  };
}
