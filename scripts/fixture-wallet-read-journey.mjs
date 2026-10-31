// One disposable UI/API/CLI observation journey, reused by local and installed acceptance.
import assert from "node:assert/strict";
import { join } from "node:path";

export async function walletReadJourney({
  view,
  accountId,
  password,
  ownerPassword,
  url,
  api,
  cliRead,
  browser,
  evidence,
  label,
  check = (_name, value) => assert.equal(value, true),
}) {
  const account = view.record.accounts.find((a) => a.id === accountId);
  assert.ok(account);
  const credential = await api("/api/v1/accounts/rpc-credential", "PUT", {
    accountId,
    revision: view.record.revision,
    action: "status",
    ref: "",
  });
  assert.ok(credential.saved && credential.locked && !credential.stale);
  const input = {
    accountId,
    revision: view.record.revision,
    ref: credential.ref,
    passphrase: password,
  };
  const snapshot = await api("/api/v1/accounts/read", "POST", input),
    cliSnapshot = await cliRead(input);
  const projection = (v) => ({
    accountId: v.accountId,
    revision: v.revision,
    binding: v.binding,
    ref: v.credentialRef,
    address: v.address,
    network: v.network,
    lamports: v.lamports,
    tokens: v.tokens,
    activity: v.activity,
  });
  assert.deepEqual(projection(cliSnapshot), projection(snapshot));
  check(
    `${label}:walletReadBound`,
    snapshot.address === view.addresses[accountId] &&
      snapshot.lamports === "9007199254740993" &&
      snapshot.fixtureOnly &&
      !snapshot.liveRPC,
  );
  const selection = {
    ...snapshot.activity[0],
    binding: snapshot.binding,
    fetchedAt: snapshot.fetchedAt,
    expiresAt: snapshot.expiresAt,
  };
  const detailInput = { ...input, selection };
  const detail = await api("/api/v1/accounts/read", "POST", detailInput),
    cliDetail = await cliRead(detailInput);
  assert.deepEqual(cliDetail.transaction, detail.transaction);
  check(
    `${label}:transactionBound`,
    detail.transaction.payer === snapshot.address &&
      detail.transaction.feeLamports === "5000" &&
      detail.transaction.nativeChange === "-5000" &&
      detail.transaction.tokens.length === 1 &&
      detail.transaction.tokens[0].change === "1000000",
  );
  const ctx = await browser.newContext({
    viewport: { width: 1360, height: 900 },
    locale: "en-US",
  });
  const external = [];
  try {
    await ctx.route("**/*", (route) => {
      if (new URL(route.request().url()).origin !== url) {
        external.push(route.request().url());
        return route.abort();
      }
      return route.continue();
    });
    const page = await ctx.newPage();
    page.setDefaultTimeout(10000);
    await page.goto(url + "/#wallets/accounts");
    await page
      .getByLabel("Owner password", { exact: true })
      .fill(ownerPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    const article = page.getByRole("article", {
      name: `Account ${account.name}`,
      exact: true,
    });
    await article.waitFor();
    await article.getByRole("button", { name: "Refresh", exact: true }).click();
    const unlock = page.getByRole("dialog", {
      name: "Unlock RPC",
      exact: true,
    });
    await unlock.getByLabel("Vault password", { exact: true }).fill(password);
    await unlock.getByRole("button", { name: "Refresh", exact: true }).click();
    await unlock.waitFor({ state: "hidden" });
    await article.getByText("9007199.254740993 SOL", { exact: true }).waitFor();
    await article.locator("summary").filter({ hasText: "Activity" }).click();
    const sig = snapshot.activity[0].signature;
    await article
      .getByRole("button", {
        name: `Transaction ${sig.slice(0, 8)}…${sig.slice(-6)}`,
        exact: true,
      })
      .click();
    const panel = page.getByRole("dialog", {
      name: "Transaction",
      exact: true,
    });
    await panel.getByLabel("Vault password", { exact: true }).fill(password);
    await panel.getByRole("button", { name: "View", exact: true }).click();
    await panel.getByText("0.000005 SOL", { exact: true }).waitFor();
    await panel.getByTitle(snapshot.address, { exact: true }).waitFor();
    check(
      `${label}:uiAmountPrecision`,
      (await panel.textContent()).includes("-0.000005") &&
        (await panel.textContent()).includes("+1"),
    );
    await page.screenshot({
      path: join(evidence, label + "-transaction.png"),
      fullPage: true,
    });
    await panel.getByRole("button", { name: "Close", exact: true }).click();
    check(`${label}:browserNoExternalRequests`, external.length === 0);
  } finally {
    await ctx.close();
  }
  const after = await api("/api/v1/accounts");
  assert.deepEqual(after.record, view.record);
  check(`${label}:readPreservesRecords`, true);
  return { input, selection, snapshot, transaction: detail.transaction };
}

export async function refusedWalletRead({
  input,
  api,
  cliRead,
  check = (_name, value) => assert.equal(value, true),
  label,
}) {
  let apiRefused = false,
    cliRefused = false;
  try {
    await api("/api/v1/accounts/read", "POST", input);
  } catch (error) {
    assert.match(String(error), /Wallet reference changed/);
    apiRefused = true;
  }
  try {
    await cliRead(input);
  } catch (error) {
    assert.match(String(error), /Wallet reference changed/);
    cliRefused = true;
  }
  check(`${label}:oldRPCReadRefused`, apiRefused && cliRefused);
}
