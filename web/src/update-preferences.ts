export type UpdatePin = { sequence: string; manifestSha256: string };
export type MaintenanceWindow = {
  timeZone: string;
  days: number[];
  start: string;
  end: string;
};
export type UpdatePolicy = {
  mode?: "manual" | "automatic";
  notify: boolean;
  pin?: UpdatePin;
  window?: MaintenanceWindow;
};
export type UpdateCandidate = {
  release: string;
  sequence: string;
  manifestSha256: string;
  target: { os: string; arch: string };
};
export type UpdateProposal = {
  candidate: UpdateCandidate;
  digest: string;
  state: "pending" | "reviewed" | "dismissed";
  createdAt: string;
  decisionAt?: string;
  pinMatch: boolean;
  verified: false;
};
export type UpdatePreferencesView = {
  version: 1;
  revision: number;
  policy: UpdatePolicy;
  proposals: UpdateProposal[];
  notifications: string[];
  evaluatedAt: string;
  window: "unrestricted" | "open" | "closed";
  installAllowed: false;
  resumeAllowed: false;
  automaticUpdates: false;
  financialExecution: false;
};
export type UpdatePreferencesRequest = { revision: number } & (
  | { action: "policy"; policy: UpdatePolicy }
  | { action: "propose"; candidate: UpdateCandidate }
  | { action: "review" | "dismiss" | "remove"; digest: string }
);
const digestOK = (s: unknown) =>
  typeof s === "string" && /^[a-f0-9]{64}$/.test(s);
const sequenceOK = (s: unknown) =>
  typeof s === "string" &&
  /^[1-9][0-9]{0,19}$/.test(s) &&
  BigInt(s) <= 18446744073709551615n;
export function readUpdatePreferences(value: unknown): UpdatePreferencesView {
  const v = value as UpdatePreferencesView | null;
  const fail = () => {
    throw new Error(
      "Unsupported update preference contract. Refresh the existing core.",
    );
  };
  if (
    !v ||
    v.version !== 1 ||
    !Number.isSafeInteger(v.revision) ||
    v.revision < 0 ||
    v.installAllowed !== false ||
    v.resumeAllowed !== false ||
    v.automaticUpdates !== false ||
    v.financialExecution !== false ||
    !v.policy ||
    typeof v.policy.notify !== "boolean" ||
    ![undefined, "manual", "automatic"].includes(v.policy.mode) ||
    !["unrestricted", "open", "closed"].includes(v.window) ||
    !Number.isFinite(Date.parse(v.evaluatedAt))
  )
    return fail();
  if (
    v.policy.pin &&
    (!digestOK(v.policy.pin.manifestSha256) ||
      !sequenceOK(v.policy.pin.sequence))
  )
    return fail();
  if (
    v.policy.window &&
    (typeof v.policy.window.timeZone !== "string" ||
      !Array.isArray(v.policy.window.days) ||
      !v.policy.window.days.every(
        (d) => Number.isInteger(d) && d >= 0 && d <= 6,
      ) ||
      !/^\d\d:\d\d$/.test(v.policy.window.start) ||
      !/^\d\d:\d\d$/.test(v.policy.window.end))
  )
    return fail();
  if (
    !Array.isArray(v.proposals) ||
    v.proposals.length > 8 ||
    !v.proposals.every(
      (p) =>
        p &&
        digestOK(p.digest) &&
        p.verified === false &&
        ["pending", "reviewed", "dismissed"].includes(p.state) &&
        typeof p.pinMatch === "boolean" &&
        p.candidate &&
        typeof p.candidate.release === "string" &&
        sequenceOK(p.candidate.sequence) &&
        digestOK(p.candidate.manifestSha256) &&
        ["linux", "darwin", "windows"].includes(p.candidate.target?.os) &&
        ["amd64", "arm64"].includes(p.candidate.target?.arch),
    )
  )
    return fail();
  if (
    !Array.isArray(v.notifications) ||
    !v.notifications.every(
      (id) =>
        digestOK(id) &&
        v.proposals.some((p) => p.digest === id && p.state === "pending"),
    ) ||
    new Set(v.notifications).size !== v.notifications.length
  )
    return fail();
  return v;
}
