export type UpdateStatus = {
  version: 1;
  observedAt: string;
  readOnly: true;
  state:
    | "observed"
    | "not-configured"
    | "paused"
    | "signer-held"
    | "recovery-required"
    | "unavailable";
  core: {
    phase: "idle" | "paused" | "released" | "unavailable";
    intentId?: string;
  };
  signer: {
    state: "available" | "unavailable" | "not-configured";
    locked: boolean | null;
    updateHeld: boolean | null;
    recoveryRequired: boolean | null;
  };
  guidance: { code: string; message: string; command?: string }[];
  walletSigner: false;
  financialExecution: false;
  automaticUpdates: false;
};

export const updateLabels: Record<UpdateStatus["state"], string> = {
  observed: "No update hold observed",
  "not-configured": "Signer not configured",
  paused: "Metadata paused",
  "signer-held": "Signer hold reported",
  "recovery-required": "Recovery review required",
  unavailable: "Status incomplete",
};

// Reject unsupported contracts instead of showing defaults as live status.
export function readUpdateStatus(value: unknown): UpdateStatus {
  const v = value as UpdateStatus | null;
  const fail = () => {
    throw new Error(
      "Unsupported update status. Refresh after checking the existing core.",
    );
  };
  if (
    !v ||
    v.version !== 1 ||
    v.readOnly !== true ||
    v.walletSigner !== false ||
    v.financialExecution !== false ||
    v.automaticUpdates !== false ||
    !Object.hasOwn(updateLabels, v.state) ||
    typeof v.observedAt !== "string" ||
    !Number.isFinite(Date.parse(v.observedAt))
  )
    return fail();
  if (
    !v.core ||
    !["idle", "paused", "released", "unavailable"].includes(v.core.phase) ||
    (v.core.intentId !== undefined && !/^[a-f0-9]{32}$/.test(v.core.intentId))
  )
    return fail();
  if (
    !v.signer ||
    !["available", "unavailable", "not-configured"].includes(v.signer.state)
  )
    return fail();
  for (const flag of [
    v.signer.locked,
    v.signer.updateHeld,
    v.signer.recoveryRequired,
  ]) {
    if (
      v.signer.state === "available" ? typeof flag !== "boolean" : flag !== null
    )
      return fail();
  }
  if (
    !Array.isArray(v.guidance) ||
    v.guidance.length === 0 ||
    !v.guidance.every(
      (g) =>
        g &&
        typeof g.code === "string" &&
        typeof g.message === "string" &&
        (g.command === undefined || typeof g.command === "string"),
    )
  )
    return fail();
  return v;
}
