import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createMacDiskImage } from "./macos-dmg.mjs";

const args = ["create", "-srcfolder", "fixture", "-ov", "fixture.dmg"];
const busy = () =>
  Object.assign(new Error("hdiutil failed"), {
    status: 1,
    signal: null,
    stderr: "hdiutil: create failed - Resource busy\n",
  });

test("disk-image creation retries only the observed busy condition within a finite budget", async () => {
  const calls = [],
    delays = [],
    logs = [];
  await createMacDiskImage(args, {
    run: (command, actual, options) => {
      calls.push(command);
      assert.equal(command, "hdiutil");
      assert.deepEqual(actual, args);
      assert.deepEqual(options.stdio, ["ignore", "pipe", "pipe"]);
      if (calls.length < 3) throw busy();
      return "created fixture.dmg\n";
    },
    wait: async (delay) => delays.push(delay),
    log: (text) => logs.push(text),
  });
  assert.equal(calls.length, 3);
  assert.deepEqual(delays, [1000, 2000]);
  assert.ok(logs.join("").includes("Resource busy"));
  assert.ok(logs.join("").includes("created fixture.dmg"));
});

test("persistent busy failure is returned after three attempts", async () => {
  let attempts = 0;
  const failure = busy();
  const delays = [];
  await assert.rejects(
    createMacDiskImage(args, {
      run: () => {
        attempts++;
        throw failure;
      },
      wait: async (delay) => delays.push(delay),
      log: () => {},
    }),
    (err) => err === failure,
  );
  assert.equal(attempts, 3);
  assert.deepEqual(delays, [1000, 2000]);
});

test("other failures and terminated processes stop without delay or retry", async () => {
  for (const failure of [
    Object.assign(new Error("disk full"), {
      status: 1,
      stderr: "hdiutil: create failed - No space left on device",
    }),
    Object.assign(busy(), { signal: "SIGTERM" }),
    Object.assign(busy(), { status: 2 }),
    new Error("hdiutil unavailable"),
  ]) {
    let attempts = 0;
    await assert.rejects(
      createMacDiskImage(args, {
        run: () => {
          attempts++;
          throw failure;
        },
        wait: async () => assert.fail("unexpected delay"),
        log: () => {},
      }),
      (err) => err === failure,
    );
    assert.equal(attempts, 1);
  }
});

test("successful creation executes once without waiting", async () => {
  let attempts = 0;
  await createMacDiskImage(args, {
    run: () => {
      attempts++;
      return "created";
    },
    wait: async () => assert.fail("unexpected delay"),
    log: () => {},
  });
  assert.equal(attempts, 1);
});
