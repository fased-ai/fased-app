import assert from "node:assert/strict";
import test from "node:test";
import { currentFixtureGate } from "./handoff-transport-evidence.mjs";

test("next fixture waits for its own armed barrier despite retained prior marker", () => {
  let expected = 0;
  let marker = { count: 16 };
  assert.equal(currentFixtureGate(expected, marker), false);
  expected = 18;
  assert.equal(currentFixtureGate(expected, marker), false);
  marker = { count: 18 };
  assert.equal(currentFixtureGate(expected, marker), true);
  expected = 0;
  assert.equal(currentFixtureGate(expected, marker), false);
});
test("unknown or malformed barrier is never current", () => {
  for (const value of [null, {}, { count: "18" }, { count: 17 }]) {
    assert.equal(currentFixtureGate(18, value), false);
  }
  assert.equal(currentFixtureGate(NaN, { count: NaN }), false);
});
