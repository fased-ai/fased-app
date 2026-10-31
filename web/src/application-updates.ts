import type { UpdatePolicy } from "./update-preferences";
export type ApplicationRelease = {
  engineSha256: string;
  version: string;
  sequence: string;
  manifestSha256: string;
  target: { os: string; arch: string };
};
export type ApplicationUpdateView = {
  notice?: { id: string; title: string; read: boolean };
  version: 1;
  revision: number;
  policyRevision: number;
  installedVersion: string;
  controllerAvailable: boolean;
  activationEnabled: false;
  policy: UpdatePolicy;
  operation: {
    id: string;
    phase: string;
    release?: ApplicationRelease;
    automatic?: boolean;
    checkedAt?: number;
    finishedAt?: number;
    failure?: string;
  };
};
const phases = [
  "idle",
  "check-queued",
  "checking",
  "available",
  "current",
  "check-failed",
  "queued",
  "applying",
  "completed",
  "rolled-back",
  "recovery",
];
export function readApplicationUpdates(value: unknown): ApplicationUpdateView {
  const v = value as ApplicationUpdateView | null;
  if (
    !v ||
    v.version !== 1 ||
    !Number.isSafeInteger(v.revision) ||
    v.revision < 0 ||
    !Number.isSafeInteger(v.policyRevision) ||
    v.policyRevision < 0 ||
    typeof v.installedVersion !== "string" ||
    typeof v.controllerAvailable !== "boolean" ||
    v.activationEnabled !== false ||
    !v.operation ||
    !phases.includes(v.operation.phase) ||
    typeof v.operation.id !== "string" ||
    !v.policy ||
    typeof v.policy.notify !== "boolean" ||
    !["manual", "automatic", undefined].includes(v.policy.mode)
  )
    throw new Error("Update status unavailable. Refresh and try again.");
  const r = v.operation.release;
  if (
    r &&
    (typeof r.version !== "string" ||
      !/^[a-f0-9]{64}$/.test(r.engineSha256) ||
      !/^[1-9][0-9]{0,19}$/.test(r.sequence) ||
      BigInt(r.sequence) <= 0n ||
      BigInt(r.sequence) > 18446744073709551615n ||
      !/^[a-f0-9]{64}$/.test(r.manifestSha256) ||
      !r.target ||
      !["linux", "darwin", "windows"].includes(r.target.os) ||
      !["amd64", "arm64"].includes(r.target.arch))
  )
    throw new Error(
      "Invalid release identity. Preserve the current installation.",
    );
  if (
    [
      "available",
      "queued",
      "applying",
      "completed",
      "rolled-back",
      "recovery",
    ].includes(v.operation.phase) &&
    !r
  )
    throw new Error(
      "Missing release identity. Preserve the current installation.",
    );
  return v;
}
export const applicationUpdateLabels: Record<string, string> = {
  idle: "Ready",
  "check-queued": "Checking…",
  checking: "Checking…",
  available: "Update available",
  current: "Up to date",
  "check-failed": "Check failed",
  queued: "Waiting",
  applying: "Updating…",
  completed: "Updated",
  "rolled-back": "Previous version restored",
  recovery: "Recovery required",
};
