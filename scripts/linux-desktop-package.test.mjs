import assert from "node:assert/strict";
import { test } from "node:test";
import { execFileSync } from "node:child_process";
import {
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readlink,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import {
  buildLinuxDesktopPackages,
  commandExists,
  debianVersion,
  linuxArch,
  stageLinuxAppDir,
  stageLinuxDebTree,
} from "./linux-desktop-package.mjs";

async function makeFixturePayload(dir) {
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "fased-app"), "#!/bin/sh\nexit 0\n", {
    mode: 0o700,
  });
  await writeFile(join(dir, "fased-desktop"), "#!/bin/sh\nexit 0\n", {
    mode: 0o700,
  });
  await writeFile(join(dir, "fased-signer"), "#!/bin/sh\nexit 0\n", {
    mode: 0o700,
  });
  await writeFile(join(dir, "bundle.json"), '{"schema":1,"desktop":true}\n', {
    mode: 0o600,
  });
  await writeFile(join(dir, "LICENSE"), "PolyForm-Perimeter-1.0.0\n", {
    mode: 0o600,
  });
}

test("linuxArch and debianVersion normalize platform identifiers safely", () => {
  assert.equal(linuxArch("x64"), "amd64");
  assert.equal(linuxArch("amd64"), "amd64");
  assert.equal(linuxArch("arm64"), "arm64");
  assert.throws(() => linuxArch("ia32"), /Unsupported Linux architecture/);

  assert.equal(debianVersion("0.1.34"), "0.1.34");
  assert.equal(debianVersion("v0.1.34-rc.1"), "0.1.34~rc.1");
  assert.equal(debianVersion("invalid"), "0.0.0~dev");
});

test("stageLinuxAppDir and stageLinuxDebTree create complete desktop bundles", async () => {
  const root = await mkdtemp(join(tmpdir(), "fased-linux-pkg-"));
  try {
    const payload = join(root, "payload");
    const appDir = join(root, "AppDir");
    const debRoot = join(root, "deb");
    await makeFixturePayload(payload);

    await stageLinuxAppDir({ payloadDir: payload, appDir });
    const appRunInfo = await stat(join(appDir, "AppRun"));
    assert.equal(appRunInfo.mode & 0o111, 0o111);
    const desktopText = await readFile(
      join(appDir, "fased-app.desktop"),
      "utf8",
    );
    assert.match(desktopText, /^Exec=fased-desktop$/m);
    assert.match(desktopText, /^Icon=fased-app$/m);
    assert.equal(
      (await stat(join(appDir, "usr/lib/fased-app/fased-desktop"))).mode &
        0o755,
      0o755,
    );
    assert.equal(
      (await stat(join(appDir, "usr/lib/fased-app/bundle.json"))).mode & 0o777,
      0o644,
    );
    assert.ok(
      (
        await stat(
          join(appDir, "usr/share/icons/hicolor/scalable/apps/fased-app.svg"),
        )
      ).isFile(),
    );

    if (commandExists("desktop-file-validate")) {
      execFileSync("desktop-file-validate", [
        resolve("packaging/linux/fased-app.desktop"),
      ]);
    }

    const deb = await stageLinuxDebTree({
      payloadDir: payload,
      debRoot,
      version: "v0.1.34-rc.1",
      arch: "x64",
    });
    assert.equal(deb.debArch, "amd64");
    assert.equal(deb.debVer, "0.1.34~rc.1");
    assert.match(deb.control, /^Package: fased-app$/m);
    assert.match(deb.control, /^Architecture: amd64$/m);
    assert.match(deb.control, /^Depends:.*libwebkit2gtk-4\.1-0.*bubblewrap/m);
    assert.equal(
      (await lstat(join(debRoot, "usr/bin/fased-app"))).isSymbolicLink(),
      true,
    );
    assert.equal(
      await readlink(join(debRoot, "usr/bin/fased-app")),
      "/opt/fased-app/fased-app",
    );
    assert.equal(
      await readlink(join(debRoot, "usr/bin/fased-desktop")),
      "/opt/fased-app/fased-desktop",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("buildLinuxDesktopPackages honors tool availability and fails cleanly when forced without tools", async () => {
  const root = await mkdtemp(join(tmpdir(), "fased-linux-build-"));
  try {
    const payload = join(root, "payload");
    const out = join(root, "out");
    await makeFixturePayload(payload);
    await mkdir(out, { recursive: true });

    const res = await buildLinuxDesktopPackages({
      payloadDir: payload,
      outputDir: out,
      version: "0.1.34",
      arch: "amd64",
      requireTools: false,
    });
    assert.equal(res.arch, "amd64");
    if (commandExists("dpkg-deb")) {
      assert.equal(res.deb, "fased-app-linux-amd64.deb");
      const info = await stat(join(out, res.deb));
      assert.ok(info.size > 0);
    } else {
      assert.equal(res.deb, null);
    }
    if (!commandExists("appimagetool")) {
      await assert.rejects(
        () =>
          buildLinuxDesktopPackages({
            payloadDir: payload,
            outputDir: out,
            version: "0.1.34",
            arch: "amd64",
            requireTools: true,
          }),
        /Missing required Linux packaging tools/,
      );
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
