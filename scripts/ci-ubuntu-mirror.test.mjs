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

test("the independent Ubuntu mirror preserves suites and distribution signing", () => {
  assert.equal(
    mainArchiveSources(source),
    source
      .replace(
        "http://azure.archive.ubuntu.com/ubuntu/",
        "https://mirrors.edge.kernel.org/ubuntu/",
      )
      .replace(
        "http://security.ubuntu.com/ubuntu/",
        "https://mirrors.edge.kernel.org/ubuntu/",
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
test("release acceptance prepares its runner through the trusted Ubuntu archive", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  const setup = workflow
    .split(
      "- name: Prepare the existing disposable Ubuntu acceptance runner",
    )[1]
    .split("- name: Acquire the actual immutable public Local predecessor")[0];
  assert.match(
    setup,
    /node scripts\/ci-ubuntu-mirror.mjs\n\s+sudo install -m 0644 .evidence\/ubuntu.sources \/etc\/apt\/sources.list.d\/ubuntu.sources\n\s+sudo apt-get update/,
  );
  assert.doesNotMatch(
    setup,
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
        source
          .replaceAll("azure.archive.ubuntu.com", "other.example")
          .replaceAll("security.ubuntu.com", "other.example"),
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

test("installed enrollment uses the same signed mirror before its real installer", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const job = workflow.split("\n  inspection-installed:\n")[1];
  assert.match(
    job,
    /node scripts\/ci-ubuntu-mirror.mjs\n\s+sudo install -m 0644 .evidence\/ubuntu.sources \/etc\/apt\/sources.list.d\/ubuntu.sources/,
  );
  assert.ok(
    job.indexOf("node scripts/ci-ubuntu-mirror.mjs") <
      job.indexOf("node scripts/check-enrollment-install.mjs"),
  );
});

test("release image fallback keeps the existing VM image digest", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  const driver = readFileSync(
    new URL("./check-inspection-vm.py", import.meta.url),
    "utf8",
  );
  const pin = driver.match(/^IMAGE_SHA='([a-f0-9]{64})'$/m)?.[1];
  assert.ok(pin);
  assert.ok(workflow.includes(pin));
  assert.match(workflow, /sha256sum --check/);
  assert.match(workflow, /--proto '=https' --proto-redir '=https'/);
  assert.match(workflow, /--max-filesize 260000000/);
  assert.match(workflow, /FASED_CI_SIGNED_UBUNTU_MIRROR: "kernel"/);
});

test("inspection VM uses the same digest-pinned image fallback", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/inspection-acceptance.yml", import.meta.url),
    "utf8",
  );
  const driver = readFileSync(
    new URL("./check-inspection-vm.py", import.meta.url),
    "utf8",
  );
  const pin = driver.match(/^IMAGE_SHA='([a-f0-9]{64})'$/m)?.[1];
  assert.ok(pin);
  const fallback = workflow.split(
    "- name: Restore pinned VM image if needed",
  )[1];
  assert.ok(fallback, "inspection VM image fallback required");
  assert.ok(fallback.includes(pin));
  assert.match(fallback, /sha256sum --check/);
  assert.match(fallback, /--proto '=https' --proto-redir '=https'/);
  assert.match(fallback, /--max-filesize 260000000/);
  assert.ok(fallback.indexOf("sha256sum --check") < fallback.indexOf("mv --"));
});

test("disposable inspection guests use the signed CI mirror", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/inspection-acceptance.yml", import.meta.url),
    "utf8",
  );
  for (const name of [
    "Actual interruption, kernel reboot and protected recovery",
    "Remote maintenance kernel authority",
    "Finalized metadata installed update, reboot recovery and authenticated no-op",
  ]) {
    const step = workflow
      .split(`- name: ${name}`)[1]
      ?.split("\n      - name:")[0];
    assert.ok(step, `missing ${name}`);
    assert.match(step, /FASED_DISPOSABLE_VM_ACCEPTANCE: "1"/);
    assert.match(step, /FASED_CI_SIGNED_UBUNTU_MIRROR: "kernel"/);
  }
});
