import { expect, it } from "vitest";
import { instanceOrigin, remoteInstanceOrigin } from "./connection";
it("admits only credential-free Tailscale HTTPS Remote origins", () => {
  for (const value of [
    "https://node.owner.ts.net",
    "https://node.owner.ts.net/",
    "https://node.owner.ts.net:8443",
  ])
    expect(instanceOrigin(value)).toBe(new URL(value).origin);
  for (const value of [
    "http://node.owner.ts.net",
    "http://127.0.0.1:7331",
    "http://[::1]:7331",
    "http://localhost:7331",
    "https://untrusted.example",
    "https://node.owner.ts.net:443",
    "https://node.owner.ts.net:22",
    "https://node.owner.ts.net:08443",
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
it("admits Tailscale, Cloudflare, and custom domain HTTPS Remote origins", () => {
  for (const value of [
    "https://node.owner.ts.net",
    "https://fased.example.com/",
    "https://tunnel-1.trycloudflare.com",
    "https://fased.example.com:8443",
  ])
    expect(remoteInstanceOrigin(value)).toBe(new URL(value).origin);
  for (const value of [
    "http://fased.example.com",
    "http://127.0.0.1:7331",
    "https://127.0.0.1:7331",
    "https://localhost:7331",
    "https://intranet",
    "https://fased.local",
    "https://fased.example.com:443",
    "https://fased.example.com:08443",
    "https://user:pass@fased.example.com",
    "https://fased.example.com/path",
  ])
    expect(() => remoteInstanceOrigin(value), value).toThrow();
});
