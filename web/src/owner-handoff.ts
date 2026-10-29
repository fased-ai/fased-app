export type OwnerHandoff = {
  location: string;
  ownerUid?: number;
  ownerKind?: "custody";
};

export function validOwnerHandoff(c: OwnerHandoff): boolean {
  if (c.ownerKind !== undefined && c.ownerKind !== "custody") return false;
  const uid = c.ownerUid ?? 0;
  return (
    Number.isInteger(uid) &&
    uid >= 0 &&
    uid <= 4294967294 &&
    (c.ownerKind !== "custody" || uid > 0) &&
    (uid === 0 ||
      c.location === "remote" ||
      (c.location === "local" && c.ownerKind === "custody"))
  );
}

export function ownerHandoff(
  c?: OwnerHandoff | null,
): { claim: string; recovery: string[] } | undefined {
  if (!c || !validOwnerHandoff(c) || !c.ownerUid) return;
  if (c.ownerKind === "custody") {
    const command = `/var/lib/fased-app-custody-${c.ownerUid}/fased-app custody`;
    const flags = "--disposable-fixture --prepare-host";
    return {
      claim: `${command} claim-code ${flags}`,
      recovery: [`${command} reset-app-password ${flags}`],
    };
  }
  const command = `/var/lib/fased-app-vps-${c.ownerUid}/fased-app vps`;
  return {
    claim: `${command} claim-code`,
    recovery: ["stop", "recover", "start"].map(
      (action) => `${command} ${action}`,
    ),
  };
}
