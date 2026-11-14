import test from "node:test";
import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  copyFile,
  writeFile,
  readFile,
  rm,
  access,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";

test("installer embeds a bounded source-bound signer and cleans only its own staging", async () => {
  const dir = await mkdtemp(join(tmpdir(), "fased-wallet-build-"));
  try {
    for (const p of ["scripts", "cmd/fased-install", "bin"])
      await mkdir(join(dir, p), { recursive: true });
    await copyFile(
      new URL("./build-core.mjs", import.meta.url),
      join(dir, "scripts/build-core.mjs"),
    );
    const payload = Buffer.from("inert disposable signer");
    await writeFile(join(dir, "signer"), payload);
    await writeFile(
      join(dir, "bin/go"),
      '#!/bin/sh\nprintf "%s\\n" "$@" > "$ARGS_FILE"\nif [ -e cmd/fased-install/wallet-signer.bin ]; then /usr/bin/cmp signer cmd/fased-install/wallet-signer.bin || exit 2; fi\nif [ -e cmd/fased-install/wallet-desktop.bin ]; then /usr/bin/cmp desktop cmd/fased-install/wallet-desktop.bin || exit 3; fi\nexit "${GO_RESULT:-0}"\n',
      { mode: 0o700 },
    );
    const env = {
      ...process.env,
      PATH: join(dir, "bin") + ":" + process.env.PATH,
      ARGS_FILE: join(dir, "args"),
    };
    const args = [
      "scripts/build-core.mjs",
      "--installer",
      "--version=0.1.12",
      "--wallet-signer=signer",
      "--source=" + "a".repeat(40),
    ];
    for (const fail of [false, true]) {
      const r = spawnSync(process.execPath, args, {
        cwd: dir,
        env: { ...env, GO_RESULT: fail ? "1" : "0" },
        encoding: "utf8",
      });
      assert.equal(r.status, fail ? 1 : 0, r.stderr);
      const called = await readFile(join(dir, "args"), "utf8");
      assert.match(called, /-tags\nwallet_payload/);
      assert.ok(called.includes("main.compiledWalletSource=" + "a".repeat(40)));
      assert.ok(
        called.includes(
          "main.compiledWalletSHA256=" +
            createHash("sha256").update(payload).digest("hex"),
        ),
      );
      await assert.rejects(
        access(join(dir, "cmd/fased-install/wallet-signer.bin")),
      );
    }
    const native = Buffer.from("inert native client");
    await writeFile(join(dir, "desktop"), native);
    for (const fail of [false, true]) {
      const r = spawnSync(
        process.execPath,
        [...args, "--wallet-desktop=desktop"],
        {
          cwd: dir,
          env: { ...env, GO_RESULT: fail ? "1" : "0" },
          encoding: "utf8",
        },
      );
      assert.equal(r.status, fail ? 1 : 0, r.stderr);
      const called = await readFile(join(dir, "args"), "utf8");
      assert.match(called, /-tags\nwallet_payload,wallet_native/);
      assert.ok(
        called.includes(
          "main.compiledDesktopSHA256=" +
            createHash("sha256").update(native).digest("hex"),
        ),
      );
      await assert.rejects(
        access(join(dir, "cmd/fased-install/wallet-desktop.bin")),
      );
      await assert.rejects(
        access(join(dir, "cmd/fased-install/wallet-signer.bin")),
      );
    }
    const coreArgs = args.filter((value) => value !== "--installer");
    const core = spawnSync(
      process.execPath,
      [...coreArgs, "--wallet-desktop=desktop"],
      {
        cwd: dir,
        env,
        encoding: "utf8",
      },
    );
    assert.equal(core.status, 0, core.stderr);
    const coreCall = await readFile(join(dir, "args"), "utf8");
    assert.ok(coreCall.includes("main.compiledNativeSource=" + "a".repeat(40)));
    assert.ok(
      coreCall.includes(
        "main.compiledNativeSignerSHA256=" +
          createHash("sha256").update(payload).digest("hex"),
      ),
    );
    assert.ok(
      coreCall.includes(
        "main.compiledNativeDesktopSHA256=" +
          createHash("sha256").update(native).digest("hex"),
      ),
    );
    assert.ok(!coreCall.includes("wallet_payload"));
    await writeFile(
      join(dir, "cmd/fased-install/wallet-signer.bin"),
      "unrelated",
    );
    const refused = spawnSync(process.execPath, args, {
      cwd: dir,
      env,
      encoding: "utf8",
    });
    assert.notEqual(refused.status, 0);
    assert.equal(
      await readFile(join(dir, "cmd/fased-install/wallet-signer.bin"), "utf8"),
      "unrelated",
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
