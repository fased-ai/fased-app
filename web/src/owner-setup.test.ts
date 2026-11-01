import { expect, it } from "vitest";
import { captureOwnerSetup } from "./owner-setup";
it("captures a bound setup proof once and scrubs it from browser history", () => {
  const proof = "x".repeat(43),
    id = "i".repeat(43);
  let address = `http://127.0.0.1:7331/?instance=${id}#setup=${proof}&view=agents/chat`;
  const result = captureOwnerSetup(address, (next) => {
    address = new URL(next, address).href;
  });
  expect(result).toEqual({ instanceId: id, code: proof });
  expect(address).toBe(`http://127.0.0.1:7331/?instance=${id}#agents/chat`);
  expect(
    captureOwnerSetup(address, () => {
      throw Error("already consumed");
    }),
  ).toBeNull();
});
it("scrubs malformed, unbound and duplicate proofs without granting authority", () => {
  for (const value of [
    "#setup=bad",
    "#setup=" + "x".repeat(43),
    "?instance=" + "i".repeat(43) + "#setup=" + "x".repeat(43) + "&setup=other",
  ]) {
    let cleaned = "";
    expect(
      captureOwnerSetup("http://localhost/" + value, (next) => {
        cleaned = next;
      }),
    ).toBeNull();
    expect(cleaned).not.toContain("setup=");
  }
  expect(
    captureOwnerSetup("http://localhost/#agents/chat", () => {}),
  ).toBeNull();
});
