// CI runner configuration only; never used by the application or installer.
// Keep Ubuntu's signing key while tolerating an unavailable CI archive mirror.
import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const archives = [
  "https://archive.ubuntu.com/ubuntu/",
  "https://mirrors.edge.kernel.org/ubuntu/",
];

export async function selectArchiveMirror(probe) {
  const reachable = await Promise.all(
    archives.map(async (uri) => {
      try {
        return await probe(uri);
      } catch {
        return false;
      }
    }),
  );
  const selected = archives.find((_, index) => reachable[index]);
  assert.ok(selected, "No reachable Ubuntu archive for CI");
  return selected;
}

export function mainArchiveSources(source, mirror) {
  assert.ok(archives.includes(mirror), "Expected a known HTTPS Ubuntu archive");
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
      return stanza.replace(ubuntuSource, `URIs: ${mirror}`);
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
  // Probe once rather than making APT retry a dead mirror for every index.
  // APT still verifies the archive with the distribution signing key.
  const mirror = await selectArchiveMirror(async (uri) => {
    const response = await fetch(new URL("dists/noble/InRelease", uri), {
      method: "GET",
      redirect: "error",
      signal: AbortSignal.timeout(5000),
    });
    await response.body?.cancel();
    return response.ok;
  });
  const result = mainArchiveSources(source, mirror);
  await mkdir(".evidence", { recursive: true });
  await writeFile(".evidence/ubuntu.sources", result, { mode: 0o644 });
}
