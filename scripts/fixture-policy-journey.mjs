// Shared public-data journey for the two controlled, actual-UID Ubuntu checks.
// Callers own identity switching and cleanup. No key, endpoint or transaction.
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";

export function fixturePolicyJourney({ owner, app, check, fails, account }) {
  const id = () => randomBytes(16).toString("hex");
  const policy = {
    account,
    asset: "fixture-credit:v1",
    budget: "100",
    maxOperation: "70",
    destinations: ["fixture:treasury"],
    expiresAt: Math.floor(Date.now() / 1000) + 1800,
  };
  const grant = (botId) => ({
    ...policy,
    id: id(),
    botId,
    revoked: false,
  });
  const a = grant(id()),
    b = grant(id());
  const intent = (g, units) => ({
    account,
    operationId: id(),
    grantId: g.id,
    botId: g.botId,
    action: "fixture-transfer",
    asset: policy.asset,
    destination: "fixture:treasury",
    units: String(units),
    expiresAt: policy.expiresAt,
  });
  owner("fixture-policy", { policy });
  owner("fixture-grant", { grant: a });
  owner("fixture-grant", { grant: b });
  check("fixture:ownerIndependentPolicyAndGrants", true);
  const first = intent(a, 70);
  for (const [name, changed] of Object.entries({
    asset: { asset: "SOL" },
    destination: { destination: "real-address-unavailable" },
    amount: { units: "71" },
    expiry: { expiresAt: 1 },
    epoch: { account: { ...account, epoch: id() } },
    bot: { botId: id() },
    arbitrarySigning: { action: "sign" },
  }))
    check(
      `fixture:reject:${name}`,
      fails(() =>
        app("fixture-reserve", {
          intent: { ...first, ...changed },
        }),
      ),
    );
  const reserved = app("fixture-reserve", { intent: first });
  check(
    "fixture:boundedAppReservation",
    reserved.operation.state === "reserved",
  );
  check(
    "fixture:crossBotOverspendDenied",
    fails(() => app("fixture-reserve", { intent: intent(b, 40) })),
  );
  const same = app("fixture-reserve", { intent: first });
  check(
    "fixture:identityRetryKeepsReservation",
    same.operation.state === "reserved" &&
      app("fixture-state").fixture.held === "70",
  );
  check(
    "fixture:changedReplayDenied",
    fails(() =>
      app("fixture-reserve", {
        intent: { ...first, units: "69" },
      }),
    ),
  );
  owner("fixture-revoke", { account, grantId: a.id });
  check(
    "fixture:revocationBlocksReservedExecution",
    fails(() => app("fixture-execute", { operationId: first.operationId })),
  );
  check(
    "fixture:revocationRetainsHeldBudget",
    app("fixture-state").fixture.held === "70",
  );
  const released = app("fixture-reconcile", { operationId: first.operationId });
  check(
    "fixture:unsubmittedMockProofReleasesReservation",
    released.operation.state === "not-submitted",
  );
  const second = intent(b, 70);
  app("fixture-reserve", { intent: second });
  const result = app("fixture-execute", { operationId: second.operationId });
  check(
    "fixture:originalPersistedBeforeMockResult",
    result.operation.state === "confirmed" &&
      result.operation.original &&
      result.operation.originalHash.length === 64,
  );
  const again = app("fixture-execute", { operationId: second.operationId });
  assert.deepEqual(again.operation, result.operation);
  const summary = app("fixture-state").fixture;
  check(
    "fixture:replayDoesNotSpendAgain",
    summary.spent === "70" && summary.held === "0",
  );
  check(
    "fixture:allFinancialCapabilitiesDisabled",
    !result.status.walletSigner &&
      !result.status.financialExecution &&
      summary.fixtureOnly &&
      !summary.financialExecution,
  );
  return { policy, grant: b, operation: second };
}

export function fixtureRecoveryJourney({
  owner,
  app,
  check,
  fails,
  account,
  previous,
}) {
  check(
    "fixture:appCannotRecoverAuthority",
    fails(() => app("fixture-recover-authority", { account })),
  );
  owner("fixture-recover-authority", { account });
  const summary = app("fixture-state").fixture;
  check(
    "fixture:retainedHistoryReconciledAcrossEpoch",
    summary.spent === "70" &&
      summary.held === "0" &&
      !summary.recoveryRequired &&
      !summary.policy,
  );
  const policy = { ...previous.policy, account };
  owner("fixture-policy", { policy });
  const grant = {
    ...previous.grant,
    account,
    id: randomBytes(16).toString("hex"),
  };
  owner("fixture-grant", { grant });
  const fresh = {
    ...previous.operation,
    account,
    grantId: grant.id,
    operationId: randomBytes(16).toString("hex"),
    units: "31",
  };
  check(
    "fixture:recoveryCannotResetLifetimeSpend",
    fails(() => app("fixture-reserve", { intent: fresh })),
  );
  check(
    "fixture:oldEpochAndGrantStayDenied",
    fails(() =>
      app("fixture-reserve", {
        intent: {
          ...previous.operation,
          operationId: randomBytes(16).toString("hex"),
          units: "1",
        },
      }),
    ),
  );
  const held = app("fixture-reserve", { intent: { ...fresh, units: "30" } });
  check(
    "fixture:onlyRemainingLifetimeBudgetAvailable",
    held.operation.state === "reserved" &&
      app("fixture-state").fixture.held === "30",
  );
}
