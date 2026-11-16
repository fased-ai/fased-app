// CI runner configuration only; never used by the application or installer.
// Keep Ubuntu's signing key while tolerating an unavailable CI archive mirror.
import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export function archiveMirrorList() {
  return [
    "https://mirrors.edge.kernel.org/ubuntu/\tpriority:1",
    "https://archive.ubuntu.com/ubuntu/\tpriority:2",
    "",
  ].join("\n");
}

export function mainArchiveSources(source, mirrorListPath) {
  assert.match(
    mirrorListPath,
    /^\/tmp\/fased-ci-ubuntu-[A-Za-z0-9]+\/mirrors\.txt$/,
    "Expected the CI-owned mirror list",
  );
  const ubuntuSource =
    /^URIs: (?:(?:http|https):\/\/(?:azure\.archive|archive|security)\.ubuntu\.com\/ubuntu\/?|mirror\+file:\/etc\/apt\/apt-mirrors\.txt)[ \t]*$/m;
  let replaced = 0;
  const result = source
    .split(/(\n\s*\n)/)
    .map((stanza) => {
      if (!ubuntuSource.test(stanza)) {
        assert.ok(
          !(
            /^URIs:/m.test(stanza) &&
            /^Signed-By: \/usr\/share\/keyrings\/ubuntu-archive-keyring\.gpg[ \t]*$/m.test(
              stanza,
            )
          ),
          "Expected a supported Ubuntu archive URI",
        );
        return stanza;
      }
      assert.match(
        stanza,
        /^Signed-By: \/usr\/share\/keyrings\/ubuntu-archive-keyring\.gpg\s*$/m,
        "Ubuntu mirror must retain the distribution signing key",
      );
      assert.doesNotMatch(
        stanza,
        /^(Trusted|Allow-Insecure|Allow-Weak|Allow-Downgrade-To-Insecure):/im,
      );
      replaced++;
      return stanza.replace(
        ubuntuSource,
        `URIs: mirror+file:${mirrorListPath}`,
      );
    })
    .join("");
  assert.ok(
    replaced > 0,
    "Expected GitHub Ubuntu archive or image mirror list; inspect runner sources before changing acquisition",
  );
  return result;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  assert.equal(process.env.GITHUB_ACTIONS, "true");
  assert.equal(process.platform, "linux");
  assert.notEqual(process.getuid(), 0);
  assert.match(
    await readFile("/etc/os-release", "utf8"),
    /^VERSION_ID="24.04"$/m,
  );
  const source = await readFile(
    "/etc/apt/sources.list.d/ubuntu.sources",
    "utf8",
  );
  console.log(
    source
      .split("\n")
      .filter((line) => line.startsWith("URIs:"))
      .join("\n"),
  );
  // _apt must be able to traverse the private CI-created directory, while
  // only the runner owner can alter the list. The hosted runner removes it.
  const mirrorDir = await mkdtemp("/tmp/fased-ci-ubuntu-");
  await chmod(mirrorDir, 0o755);
  const mirrorListPath = `${mirrorDir}/mirrors.txt`;
  await writeFile(mirrorListPath, archiveMirrorList(), {
    flag: "wx",
    mode: 0o644,
  });
  const result = mainArchiveSources(source, mirrorListPath);
  await mkdir(".evidence", { recursive: true });
  await writeFile(".evidence/ubuntu.sources", result, { mode: 0o644 });
}
