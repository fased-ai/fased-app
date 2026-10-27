import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { mainArchiveSources } from "./ci-ubuntu-mirror.mjs";

const source = `Types: deb
URIs: http://azure.archive.ubuntu.com/ubuntu/
Suites: noble noble-updates noble-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: http://security.ubuntu.com/ubuntu/
Suites: noble-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`;

test("only the Azure archive URI changes; suites, security and signing stay intact", () => {
  assert.equal(
    mainArchiveSources(source),
    source.replace(
      "http://azure.archive.ubuntu.com/ubuntu/",
      "https://archive.ubuntu.com/ubuntu/",
    ),
  );
});
test("VM prerequisites use the same trusted archive without extending the job", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/inspection-acceptance.yml", import.meta.url),
    "utf8",
  );
  assert.match(workflow, /timeout-minutes: 35\n/);
  const prerequisites = workflow
    .split("- name: Disposable VM prerequisites")[1]
    .split("- uses: actions/cache")[0];
  assert.match(prerequisites, /node --test scripts\/ci-ubuntu-mirror.test.mjs/);
  assert.match(
    prerequisites,
    /node scripts\/ci-ubuntu-mirror.mjs\n\s+sudo install -m 0644 .evidence\/ubuntu.sources \/etc\/apt\/sources.list.d\/ubuntu.sources\n\s+sudo apt-get update/,
  );
  assert.doesNotMatch(
    prerequisites,
    /sudo (?:node|npm|bash)|allow-unauthenticated|trusted=yes/,
  );
});
test("the official runner mirror-list source bypasses Azure without changing trust", () => {
  const mirrorList = source.replace(
    "http://azure.archive.ubuntu.com/ubuntu/",
    "mirror+file:/etc/apt/apt-mirrors.txt",
  );
  assert.equal(mainArchiveSources(mirrorList), mainArchiveSources(source));
  assert.throws(
    () =>
      mainArchiveSources(mirrorList.replace("apt-mirrors.txt", "unknown.txt")),
    /Expected/,
  );
});
test("runner changes and unsigned or trust-bypassing sources fail closed", () => {
  assert.throws(
    () =>
      mainArchiveSources(
        source.replaceAll("azure.archive.ubuntu.com", "other.example"),
      ),
    /Expected/,
  );
  assert.throws(
    () => mainArchiveSources(source.replaceAll("Signed-By:", "Other:")),
    /signing key/,
  );
  assert.throws(() =>
    mainArchiveSources(
      source.replace("Types: deb", "Types: deb\nTrusted: yes"),
    ),
  );
});
test("workflow prepares sources without root project execution or timeout expansion", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const desktop = workflow
    .split("\n  desktop:\n")[1]
    .split(/\n  [a-z][\w-]*:\n/)[0];
  assert.match(desktop, /timeout-minutes: 15\n/);
  assert.match(desktop, /node --test scripts\/ci-ubuntu-mirror.test.mjs/);
  assert.match(
    desktop,
    /node scripts\/ci-ubuntu-mirror.mjs\n\s+sudo install -m 0644 .evidence\/ubuntu.sources \/etc\/apt\/sources.list.d\/ubuntu.sources\n\s+sudo apt-get update/,
  );
  assert.doesNotMatch(
    desktop,
    /sudo (?:node|npm|bash)|allow-unauthenticated|trusted=yes/,
  );
});
