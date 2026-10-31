import { describe, expect, it } from "vitest";
import { readApplicationUpdates } from "./application-updates";
const idle = () => ({
  version: 1,
  revision: 0,
  policyRevision: 0,
  installedVersion: "0.0.0-test",
  controllerAvailable: false,
  activationEnabled: false,
  policy: { notify: true },
  operation: { id: "", phase: "idle" },
});
describe("application update admission", () => {
  it("distinguishes saved automatic preference from activation", () => {
    const v = idle();
    Object.assign(v.policy, { mode: "automatic" });
    const r = readApplicationUpdates(v);
    expect(r.policy.mode).toBe("automatic");
    expect(r.activationEnabled).toBe(false);
    expect(r.controllerAvailable).toBe(false);
  });
  it("refuses unsupported activation and an incomplete actionable release", () => {
    expect(() =>
      readApplicationUpdates({ ...idle(), activationEnabled: true }),
    ).toThrow();
    expect(() =>
      readApplicationUpdates({
        ...idle(),
        operation: { id: "1".repeat(32), phase: "available" },
      }),
    ).toThrow();
  });
  it("refuses ambiguous release identities before showing an action", () => {
    const release = {
      version: "test.2",
      sequence: "7",
      manifestSha256: "a".repeat(64),
      engineSha256: "b".repeat(64),
      target: { os: "linux", arch: "amd64" },
    };
    const v = {
      ...idle(),
      operation: { id: "1".repeat(32), phase: "available", release },
    };
    expect(readApplicationUpdates(v).operation.release?.version).toBe("test.2");
    expect(() =>
      readApplicationUpdates({
        ...v,
        operation: { ...v.operation, release: { ...release, sequence: "07" } },
      }),
    ).toThrow();
    expect(() =>
      readApplicationUpdates({
        ...v,
        operation: {
          ...v.operation,
          release: { ...release, engineSha256: "bad" },
        },
      }),
    ).toThrow();
  });
});
