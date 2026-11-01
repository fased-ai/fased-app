// Private publisher state only. No credentials, transport, keys or activation.
import { createHash, randomUUID } from "node:crypto";
import {
  constants,
  closeSync,
  existsSync,
  fstatSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  realpathSync,
  renameSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
const digest = (v) => createHash("sha256").update(v).digest("hex");
const max = 2 * 1024 * 1024;
const uid = () => {
  if (process.platform !== "linux" || process.getuid?.() === 0)
    throw Error("Ordinary Linux publisher required");
  return process.getuid();
};
function privateDirectory(dir, create = false) {
  const owner = uid();
  if (!path.isAbsolute(dir) || path.resolve(dir) !== dir)
    throw Error("Canonical absolute publisher directory required");
  if (create && !existsSync(dir)) mkdirSync(dir, { mode: 0o700 });
  const st = lstatSync(dir);
  if (
    !st.isDirectory() ||
    st.isSymbolicLink() ||
    st.uid !== owner ||
    (st.mode & 0o077) !== 0 ||
    realpathSync(dir) !== dir
  )
    throw Error("Private owned publisher directory required");
  // Persist the directory name before any journal or remote attempt. Repeat for
  // existing directories to cover interruption after mkdir but before sync.
  if (create) {
    const parent = openSync(
      path.dirname(dir),
      constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
    );
    try {
      fsyncSync(parent);
    } finally {
      closeSync(parent);
    }
  }
}
export function readPublisherFile(file, limit = max) {
  const fd = openSync(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const st = fstatSync(fd);
    if (
      !st.isFile() ||
      st.nlink !== 1 ||
      st.uid !== uid() ||
      (st.mode & 0o077) !== 0 ||
      st.size > limit
    )
      throw Error("Private regular publisher file required");
    return readFileSync(fd);
  } finally {
    closeSync(fd);
  }
}
function readState(dir) {
  const envelope = JSON.parse(
    readPublisherFile(path.join(dir, "operation.json")),
  );
  if (
    envelope.sha256 !== digest(JSON.stringify(envelope.state)) ||
    envelope.state?.version !== 1 ||
    !Array.isArray(envelope.state.effects) ||
    !Array.isArray(envelope.state.events) ||
    envelope.state.effects.length > 128 ||
    envelope.state.events.length > 512
  )
    throw Error("Publisher state corrupt; preserve and inspect");
  return envelope.state;
}
export function inspectPublisherJournal(dir) {
  privateDirectory(dir);
  const s = readState(dir);
  return {
    version: s.version,
    id: s.id,
    binding: s.binding,
    complete: s.complete,
    result: s.result,
    pending: s.effects.map((e, i) => ({
      index: i,
      method: e.method,
      path: e.path,
      requestSha256: e.requestSha256,
      observedId:
        e.result?.id ?? e.result?.sha ?? e.result?.object?.sha ?? null,
      pending: !e.result,
    })),
  };
}
export function openPublisherJournal(dir, binding, { resume = false } = {}) {
  privateDirectory(dir, true);
  const lock = path.join(dir, "writer.lock");
  // util-linux flock is already part of the Ubuntu installer prerequisites.
  // The inherited descriptor shares its open-file description with this process;
  // the lock survives the short helper and releases on close or process death.
  // Never unlink a lock file: that could split concurrent callers across inodes.
  let fd;
  try {
    fd = openSync(
      lock,
      constants.O_CREAT |
        constants.O_EXCL |
        constants.O_RDWR |
        constants.O_NOFOLLOW,
      0o600,
    );
  } catch (e) {
    if (e.code !== "EEXIST") throw e;
    fd = openSync(lock, constants.O_RDWR | constants.O_NOFOLLOW);
  }
  const owned = fstatSync(fd);
  if (
    !owned.isFile() ||
    owned.nlink !== 1 ||
    owned.uid !== uid() ||
    (owned.mode & 0o077) !== 0
  ) {
    closeSync(fd);
    throw Error("Private owned publisher lock required");
  }
  const locked = spawnSync("/usr/bin/flock", ["--nonblock", "3"], {
    stdio: ["ignore", "ignore", "pipe", fd],
    timeout: 5000,
  });
  const visible = lstatSync(lock);
  if (
    locked.status !== 0 ||
    visible.ino !== owned.ino ||
    visible.dev !== owned.dev
  ) {
    closeSync(fd);
    throw Error("Publisher already running or lock unavailable");
  }
  const nonce = randomUUID();
  let state,
    closed = false;
  const syncDir = () => {
    const d = openSync(
      dir,
      constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
    );
    try {
      fsyncSync(d);
    } finally {
      closeSync(d);
    }
  };
  const close = () => {
    if (closed) return;
    closed = true;
    closeSync(fd);
  };
  const save = () => {
    if (closed) throw Error("Publisher journal closed");
    if (state.effects.length > 128 || state.events.length > 512)
      throw Error("Publisher journal capacity reached");
    const bytes = Buffer.from(
      JSON.stringify({ state, sha256: digest(JSON.stringify(state)) }) + "\n",
    );
    if (bytes.length > max) throw Error("Publisher state exceeded bound");
    const target = path.join(dir, "operation.json");
    if (existsSync(target)) readPublisherFile(target);
    const tmp = path.join(dir, `.next-${nonce}`);
    const out = openSync(
      tmp,
      constants.O_WRONLY |
        constants.O_CREAT |
        constants.O_EXCL |
        constants.O_NOFOLLOW,
      0o600,
    );
    try {
      writeFileSync(out, bytes);
      fsyncSync(out);
    } finally {
      closeSync(out);
    }
    renameSync(tmp, target);
    syncDir();
  };
  try {
    syncDir();
    const file = path.join(dir, "operation.json");
    if (existsSync(file)) {
      state = readState(dir);
      if (state.binding !== binding)
        throw Error("Publisher input/authority changed");
      if (!state.complete && !resume)
        throw Error("Interrupted operation; explicit resume required");
    } else {
      if (resume)
        throw Error(
          "Missing publisher state; never recreate an interrupted operation",
        );
      state = {
        version: 1,
        id: nonce,
        binding,
        effects: [],
        events: [],
        complete: false,
        result: null,
      };
      save();
    }
    return {
      state,
      save,
      close,
      record: async (event) => {
        const value = JSON.parse(JSON.stringify(event));
        if (JSON.stringify(state.events.at(-1)) !== JSON.stringify(value)) {
          state.events.push(value);
          save();
        }
      },
    };
  } catch (e) {
    close();
    throw e;
  }
}
