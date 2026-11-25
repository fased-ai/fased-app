import test from "node:test";
import assert from "node:assert/strict";
import {
  checkReleaseTag,
  validateReleaseTagRef,
} from "./check-release-tag.mjs";

const COMMIT = "bdb7fd502e678c777ceb245e2f55e41db05a9f7f";
const TAG_OBJ_SHA = "76f44545a04c70b7e6c4c8e890ee1b19d25cb3a1";

test("validateReleaseTagRef passes valid annotated stable and desktop tags", () => {
  const res = validateReleaseTagRef({
    tag: "v0.2.3",
    expectedCommit: COMMIT,
    refObj: {
      ref: "refs/tags/v0.2.3",
      object: { type: "tag", sha: TAG_OBJ_SHA },
    },
    tagObj: {
      tag: "v0.2.3",
      object: { type: "commit", sha: COMMIT },
    },
  });
  assert.deepEqual(res, {
    ok: true,
    tag: "v0.2.3",
    tagObject: TAG_OBJ_SHA,
    commit: COMMIT,
  });

  const desktopRes = checkReleaseTag(
    { tag: "v0.2.3-desktop", commit: COMMIT },
    (apiPath) => {
      if (apiPath.endsWith("/git/refs/tags/v0.2.3-desktop")) {
        return {
          ref: "refs/tags/v0.2.3-desktop",
          object: { type: "tag", sha: TAG_OBJ_SHA },
        };
      }
      if (apiPath.endsWith(`/git/tags/${TAG_OBJ_SHA}`)) {
        return {
          tag: "v0.2.3-desktop",
          object: { type: "commit", sha: COMMIT },
        };
      }
      throw new Error(`unexpected path: ${apiPath}`);
    },
  );
  assert.equal(desktopRes.ok, true);
  assert.equal(desktopRes.tag, "v0.2.3-desktop");
});

test("validateReleaseTagRef rejects lightweight commit tags", () => {
  assert.throws(
    () =>
      checkReleaseTag({ tag: "v0.2.2", commit: COMMIT }, () => ({
        ref: "refs/tags/v0.2.2",
        object: { type: "commit", sha: COMMIT },
      })),
    /annotated release tag required/,
  );
});

test("validateReleaseTagRef rejects mismatched commit or invalid tag name", () => {
  assert.throws(
    () =>
      validateReleaseTagRef({
        tag: "v0.2.3",
        expectedCommit: COMMIT,
        refObj: {
          ref: "refs/tags/v0.2.3",
          object: { type: "tag", sha: TAG_OBJ_SHA },
        },
        tagObj: {
          tag: "v0.2.3",
          object: { type: "commit", sha: "0".repeat(40) },
        },
      }),
    /expected/,
  );

  assert.throws(
    () =>
      validateReleaseTagRef({
        tag: "0.2.3",
        expectedCommit: COMMIT,
        refObj: null,
        tagObj: null,
      }),
    /Invalid release tag format/,
  );
});
