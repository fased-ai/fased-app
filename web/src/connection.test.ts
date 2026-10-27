import { expect, it } from "vitest";
import { instanceOrigin } from "./connection";
it("admits only explicit credential-free instance origins", () => {
  for (const value of [
    "https://node.owner.ts.net",
    "https://node.owner.ts.net/",
    "http://127.0.0.1:7331",
    "http://[::1]:7331",
    "http://localhost:7331",
  ])
    expect(instanceOrigin(value)).toBe(new URL(value).origin);
  for (const value of [
    "http://node.owner.ts.net",
    "javascript:alert(1)",
    "file:///tmp/a",
    "https://user:pass@node.owner.ts.net",
    "https://node.owner.ts.net/x",
    "https://node.owner.ts.net/?token=x",
    "https://node.owner.ts.net/?",
    "https://node.owner.ts.net/#x",
    "https://node.owner.ts.net:0",
    "https://node.owner.ts.net:65536",
    "https://node.owner.ts.net:",
    "https://node.owner.ts.net\\evil",
  ])
    expect(() => instanceOrigin(value), value).toThrow();
});
