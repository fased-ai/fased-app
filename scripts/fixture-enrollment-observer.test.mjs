import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import test from "node:test";
import {
  mkdtemp,
  mkdir,
  readFile,
  rename,
  rm,
  symlink,
  unlink,
  writeFile,
} from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  readEnrollmentObservation,
  removeEnrollmentUnitLink,
} from "./fixture-enrollment-observer.mjs";
import { protectedPathMissing } from "./fixture-enrollment-helper.mjs";
import { walletReadJourney } from "./fixture-wallet-read-journey.mjs";

test("installed enrollment identity proof selects preserved password access", async () => {
  const source = await readFile(
    new URL("./check-enrollment-install.mjs", import.meta.url),
    "utf8",
  );
  const proof = source
    .split("async function browserProof(")[1]
    ?.split("async function controlledInterruption(")[0];
  assert.ok(proof, "installed browser identity proof must remain covered");
  assert.match(
    proof,
    /await page\.goto\([^;]+;\s*await openPasswordSignIn\(page\);\s*await page\.getByLabel\("Owner password"/,
  );
});

test("installed Wallets reader selects preserved password access before filling it", async (t) => {
  for (const optional of [false, true]) {
    await t.test(
      optional ? "Local password choice" : "password form",
      async () => {
        const actions = [];
        const reachedPassword = new Error(
          "fixture reached original password input",
        );
        let formVisible = !optional;
        const choice = {
          isVisible: async () => optional,
          click: async () => {
            actions.push("choose password");
            formVisible = true;
          },
          or: () => ({ first: () => ({ waitFor: async () => {} }) }),
        };
        const page = {
          setDefaultTimeout: () => {},
          goto: async () => {},
          getByRole: (_role, value) => {
            assert.equal(value.name, "Use password");
            return choice;
          },
          getByLabel: (name) => {
            assert.equal(name, "Owner password");
            return {
              fill: async (value) => {
                assert.equal(formVisible, true);
                assert.equal(value, "original fixture password");
                actions.push("fill original password");
                throw reachedPassword;
              },
            };
          },
        };
        const snapshot = {
          address: "fixture-address",
          lamports: "9007199254740993",
          fixtureOnly: true,
          liveRPC: false,
          activity: [{}],
          transaction: {
            payer: "fixture-address",
            feeLamports: "5000",
            nativeChange: "-5000",
            tokens: [{ change: "1000000" }],
          },
        };
        await assert.rejects(
          walletReadJourney({
            view: {
              record: { accounts: [{ id: "a", name: "Disposable" }] },
              addresses: { a: "fixture-address" },
            },
            accountId: "a",
            ownerPassword: "original fixture password",
            url: "http://127.0.0.1:1",
            api: async (path) =>
              path.endsWith("rpc-credential")
                ? { saved: true, locked: true }
                : snapshot,
            cliRead: async () => snapshot,
            browser: {
              newContext: async () => ({
                route: async () => {},
                newPage: async () => page,
                close: async () => actions.push("close"),
              }),
            },
          }),
          (error) => error === reachedPassword,
        );
        assert.deepEqual(actions, [
          ...(optional ? ["choose password"] : []),
          "fill original password",
          "close",
        ]);
      },
    );
  }
});

async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), "fased-enrollment-observer-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}

test("protected setup preserves existing paths and treats sudo errors as errors", async (t) => {
  const dir = await fixture(t),
    path = join(dir, "state");
  const osTool = (file, args) => execFileSync(file, args, { encoding: "utf8" });
  assert.equal(protectedPathMissing(path, osTool), true);
  await writeFile(path, "existing");
  assert.equal(protectedPathMissing(path, osTool), false);
  await unlink(path);
  await symlink(join(dir, "absent"), path);
  assert.equal(protectedPathMissing(path, osTool), false);
  const denied = Object.assign(new Error("sudo refused"), {
    status: 1,
    stderr: "sudo: access denied",
  });
  assert.throws(
    () =>
      protectedPathMissing(path, () => {
        throw denied;
      }),
    (err) => err === denied,
  );
});

test("journal observation tolerates only initial missing/empty publication, then reads atomic data", async (t) => {
  const dir = await fixture(t),
    path = join(dir, "state.json");
  assert.equal(await readEnrollmentObservation(path), undefined);
  await writeFile(path, "", { mode: 0o600 });
  assert.equal(await readEnrollmentObservation(path), undefined);
  const expected = { phase: "prepared", target: { id: "fixture" } };
  await writeFile(join(dir, "pending"), JSON.stringify(expected), {
    mode: 0o600,
  });
  await rename(join(dir, "pending"), path);
  assert.deepEqual(await readEnrollmentObservation(path), expected);
  await writeFile(path, "");
  await assert.rejects(
    readEnrollmentObservation(path, { durable: true }),
    SyntaxError,
  );
  await unlink(path);
  await assert.rejects(readEnrollmentObservation(path, { durable: true }), {
    code: "ENOENT",
  });
});

test("non-publication filesystem errors remain fatal before durable observation", async (t) => {
  const dir = await fixture(t);
  await assert.rejects(readEnrollmentObservation(dir), { code: "EISDIR" });
});

test("unit disable may already unlink exactly the declared fixture symlink", async (t) => {
  const dir = await fixture(t),
    unit = join(dir, "fixture.service"),
    link = join(dir, "enabled.service");
  await writeFile(unit, "preserved fixture unit", { mode: 0o600 });
  await symlink(unit, link);
  await unlink(link); // Actual filesystem result of systemctl disable.
  await removeEnrollmentUnitLink(link);
  await symlink(unit, link);
  await removeEnrollmentUnitLink(link);
  assert.equal(await readFile(unit, "utf8"), "preserved fixture unit");
});

test("cleanup preserves unexpected directories and propagates other errors", async (t) => {
  const dir = await fixture(t),
    unexpected = join(dir, "foreign");
  await mkdir(unexpected);
  await writeFile(join(unexpected, "preserve"), "foreign fixture bytes");
  await assert.rejects(
    removeEnrollmentUnitLink(unexpected),
    (e) => e.code !== "ENOENT",
  );
  assert.equal(
    await readFile(join(unexpected, "preserve"), "utf8"),
    "foreign fixture bytes",
  );
});
