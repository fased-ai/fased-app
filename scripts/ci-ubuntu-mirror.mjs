// CI runner configuration only; never used by the application or installer.
// Keep Ubuntu's signing key while avoiding Canonical DNS outages on CI runners.
import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export function mainArchiveSources(source) {
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
        "URIs: https://mirrors.edge.kernel.org/ubuntu/",
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
  const result = mainArchiveSources(source);
  await mkdir(".evidence", { recursive: true });
  await writeFile(".evidence/ubuntu.sources", result, { mode: 0o644 });
}
