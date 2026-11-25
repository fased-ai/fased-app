import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import {
  DEFAULT_SOAK_DISTROS,
  buildInstallGateProbeScript,
  parseSoakArgs,
} from "./soak-multi-os.mjs";
import { renderEntry } from "./render-install-entry.mjs";

test("parseSoakArgs validates flags and defaults cleanly", () => {
  const def = parseSoakArgs([]);
  assert.ok(["podman", "docker"].includes(def.runtime));
  assert.ok(["amd64", "arm64"].includes(def.arch));
  assert.deepEqual(def.distros, DEFAULT_SOAK_DISTROS);

  const custom = parseSoakArgs([
    "--runtime",
    "docker",
    "--arch",
    "arm64",
    "--distros",
    "debian:12,fedora:43",
    "--output",
    "./out.json",
    "--timeout-ms",
    "45000",
  ]);
  assert.equal(custom.runtime, "docker");
  assert.equal(custom.arch, "arm64");
  assert.deepEqual(custom.distros, ["debian:12", "fedora:43"]);
  assert.equal(custom.output, path.resolve("./out.json"));
  assert.equal(custom.timeoutMs, 45000);

  assert.throws(
    () => parseSoakArgs(["--runtime", "lxc"]),
    /Unsupported container runtime/,
  );
  assert.throws(
    () => parseSoakArgs(["--arch", "s390x"]),
    /Unsupported soak architecture/,
  );
  assert.throws(
    () => parseSoakArgs(["--timeout-ms", "100"]),
    /Invalid --timeout-ms/,
  );
});

test("buildInstallGateProbeScript preserves REMOTE_PLATFORMS and target arch gates", () => {
  const tpl = readFileSync(path.resolve("install.sh"), "utf8");
  for (const seedRel of [
    "release/public/ubuntu-attested-seed.json",
    "release/public/linux-arm64-attested-seed.json",
  ]) {
    const seed = readFileSync(path.resolve(seedRel), "utf8");
    const rendered = renderEntry(tpl, {
      version: "0.2.1",
      installer: Buffer.from("installer-bytes"),
      seed: Buffer.from(seed),
    });
    const probe = buildInstallGateProbeScript(rendered);
    assert.match(probe, /debian:12\)/);
    assert.match(probe, /debian:13\)/);
    assert.match(probe, /fedora:41\)/);
    assert.match(probe, /fedora:45\)/);
    assert.match(probe, /Remote requires a supported Linux release/);
    assert.match(probe, /INSTALL_GATE_PASS:/);
    execFileSync("bash", ["-n"], { input: probe, encoding: "utf8" });
  }
});
