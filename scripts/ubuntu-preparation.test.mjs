import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { publicationObserver } from "./ubuntu-preparation.mjs";

test("publication observer handles adjacent events before ready and only once", () => {
  const state = mkdtempSync(join(tmpdir(), "fased-observer-"));
  try {
    const path = join(state, "host-preparation.json");
    let calls = 0;
    const observe = publicationObserver(state, () => calls++);
    observe(); // Notification before the first receipt exists.
    writeFileSync(path, "{");
    observe();
    writeFileSync(path, JSON.stringify({ phase: "staging" }));
    observe();
    assert.equal(calls, 0);
    writeFileSync(path, JSON.stringify({ phase: "published" }));
    observe();
    assert.equal(
      calls,
      1,
      "interruption must happen before the callback returns",
    );
    observe();
    writeFileSync(path, JSON.stringify({ phase: "ready" }));
    observe();
    assert.equal(calls, 1);
  } finally {
    rmSync(state, { recursive: true, force: true });
  }
});

test("ready receipt alone is not accepted as an interrupted publication", () => {
  const state = mkdtempSync(join(tmpdir(), "fased-observer-"));
  try {
    writeFileSync(
      join(state, "host-preparation.json"),
      JSON.stringify({ phase: "ready" }),
    );
    let called = false;
    publicationObserver(state, () => (called = true))();
    assert.equal(called, false);
  } finally {
    rmSync(state, { recursive: true, force: true });
  }
});
