// Runs only inside the caller's disposable managed Ubuntu fixture. Browser and
// PTY drivers run as its ordinary owner. No project program executes as root.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { writeFile } from "node:fs/promises";
import { randomBytes } from "node:crypto";
import { chromium } from "@playwright/test";

export async function revealFixtureAccountEntry(page) {
  const name = page.getByLabel("Name", { exact: true });
  await name.waitFor({ state: "attached" });
  if (!(await name.isVisible()))
    await page.getByText("Advanced", { exact: true }).click();
  await name.waitFor({ state: "visible" });
}

export async function refreshFixtureAccounts(page) {
  // The page refresh and a wallet's balance refresh have distinct scopes.
  // Retain the historical label for older UI fixtures without this header.
  const header = page.locator(".wallet-workspace > .section-head");
  const scope = (await header.count()) ? header : page;
  await scope
    .getByRole("button", { name: /^(Refresh|Refresh accounts and approvals)$/ })
    .click();
}

export function verifiedFixtureApproval(view, managed, proposal) {
  assert.deepEqual(
    proposal.policy.account,
    managed.signer,
    "policy signer identity changed",
  );
  assert.deepEqual(
    proposal.grant.account,
    managed.signer,
    "grant signer identity changed",
  );
  const account = view.record.accounts.find((item) => item.id === managed.id);
  assert.ok(account, "approved managed account missing from public read");
  assert.equal(
    account.mode,
    "fixture-managed",
    "approval account mode changed",
  );
  assert.deepEqual(
    account.signer,
    managed.signer,
    "approval account identity changed",
  );
  assert.equal(
    account.network,
    managed.network,
    "approval account network changed",
  );
  assert.equal(
    account.publicKey,
    managed.publicKey,
    "approval public key changed",
  );
  const drafts = view.record.proposals.filter(
    (item) => item.accountId === managed.id,
  );
  const row = drafts.findIndex((item) => item.proposal.id === proposal.id);
  assert.ok(row >= 0, "target proposal is not bound to the managed account");
  assert.deepEqual(drafts[row].proposal, proposal, "approved proposal changed");
  const proof = view.proposals.find((item) => item.id === proposal.id);
  assert.ok(proof, "target proposal receipt missing from public read");
  assert.equal(
    proof.receipt.proposalId,
    proposal.id,
    "receipt proposal identity changed",
  );
  assert.equal(
    proof.receipt.state,
    "approved",
    "target receipt is not approved",
  );
  assert.deepEqual(
    proof.receipt.account,
    managed.signer,
    "receipt signer identity changed",
  );
  assert.match(proof.digest, /^[a-f0-9]{64}$/, "invalid proposal digest");
  assert.equal(
    proof.receipt.digest,
    proof.digest,
    "approved receipt digest mismatch",
  );
  return {
    accountId: managed.id,
    proposalId: proposal.id,
    digest: proof.digest,
    row,
  };
}

export async function revealFixtureApproval(page, name, proposalId, row) {
  assert.match(proposalId, /^[a-f0-9]{32}$/);
  assert.ok(
    Number.isInteger(row) && row >= 0,
    "verified account receipt row required",
  );
  const wallet = page.getByRole("article", {
    name: `Account ${name}`,
    exact: true,
  });
  const current = wallet
    .locator(".account-permission")
    .nth(row)
    .getByText("approved", { exact: true });
  // Genuine lifecycle3 UI at d2f1fb2 renders this separate proposal article.
  // It has no .account-permission or .account-advanced container.
  const legacyArticle = page.getByRole("article", {
    name: `Proposal ${proposalId}`,
    exact: true,
    includeHidden: true,
  });
  const legacy = legacyArticle.getByText("approved", { exact: true });
  await current.or(legacy).first().waitFor({ state: "attached" });
  if (await current.count()) {
    const details = wallet.locator(".account-details");
    if (!(await details.evaluate((element) => element.open)))
      await details.locator(":scope > summary").click();
    await current.waitFor({ state: "visible" });
    return;
  }
  // Later historical UIs may put this exact proposal inside a disclosure.
  // Open only ancestors of that proposal, never an unrelated key editor.
  const disclosures = page.locator("details").filter({ has: legacyArticle });
  for (let index = 0; index < (await disclosures.count()); index++) {
    const details = disclosures.nth(index);
    if (!(await details.evaluate((element) => element.open)))
      await details.locator(":scope > summary").click();
  }
  await legacy.waitFor({ state: "visible" });
}

function publicApprovalView(view) {
  return {
    revision: view.record.revision,
    accounts: view.record.accounts.map(
      ({ id, name, version, mode, network, publicKey, signer }) => ({
        id,
        name,
        version,
        mode,
        network,
        publicKey,
        signer,
      }),
    ),
    proposals: view.record.proposals,
    receipts: view.proposals,
    fixtureAvailable: view.fixture.available,
    accountStates: view.accountStates,
  };
}

export async function fixtureAccountJourney({
  url,
  engine,
  cli,
  app,
  ownerUnlock,
  ownerLock,
  ownerStatus,
  check,
  fails,
  account,
  appPassword,
  walletPassword,
  backups,
  evidence,
  env,
}) {
  const browser = await chromium.launch({ headless: true });
  let page;
  const approvalDiagnostic = {};
  try {
    const context = await browser.newContext({
      viewport: { width: 1400, height: 1000 },
    });
    page = await context.newPage();
    await page.goto(url + "/#wallets/accounts");
    await page.getByLabel("Owner password", { exact: true }).fill(appPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await page
      .getByRole("heading", { name: "Wallets", level: 1, exact: true })
      .waitFor();
    // Seed historical disposable records through the existing owner CLI. The
    // ordinary Wallets page no longer exposes test-key/proposal editors.
    cli(["login", "--prepare-host", "--password-stdin"], appPassword + "\n");
    let current = JSON.parse(cli(["accounts", "--prepare-host"]));
    current.record.accounts.push({
      version: 1,
      id: randomBytes(16).toString("hex"),
      name: "Disposable UI watch",
      mode: "watch",
      network: account.network,
      publicKey: account.publicKey,
    });
    cli(
      ["accounts", "--prepare-host", "--request-stdin"],
      JSON.stringify(current.record),
    );
    await refreshFixtureAccounts(page);
    await page
      .getByRole("heading", { name: "Disposable UI watch", exact: true })
      .waitFor();
    check("accounts:CLISeedUIRead", true);
    current = JSON.parse(cli(["accounts", "--prepare-host"]));
    const stale = structuredClone(current.record);
    await page.getByRole("button", { name: "Rename", exact: true }).click();
    await page.getByLabel("New account name").fill("Shared UI and CLI watch");
    await page
      .getByRole("button", { name: "Save account name", exact: true })
      .click();
    await page
      .getByRole("heading", { name: "Shared UI and CLI watch", exact: true })
      .waitFor();
    check(
      "accounts:UIWriteCLIRead",
      JSON.parse(cli(["accounts", "--prepare-host"])).record.accounts[0]
        .name === "Shared UI and CLI watch",
    );
    check(
      "accounts:staleCLIRevisionDenied",
      fails(() =>
        cli(
          ["accounts", "--prepare-host", "--request-stdin"],
          JSON.stringify(stale),
        ),
      ),
    );
    current = JSON.parse(cli(["accounts", "--prepare-host"]));
    current.record.accounts.push({
      version: 1,
      id: randomBytes(16).toString("hex"),
      name: "CLI external manual",
      mode: "external",
      network: account.network,
      publicKey: account.publicKey,
    });
    cli(
      ["accounts", "--prepare-host", "--request-stdin"],
      JSON.stringify(current.record),
    );
    await refreshFixtureAccounts(page);
    await page
      .getByRole("heading", { name: "CLI external manual", exact: true })
      .waitFor();
    check("accounts:CLIWriteUIRead", true);
    current = JSON.parse(cli(["accounts", "--prepare-host"]));
    const managed = {
      version: 1,
      id: randomBytes(16).toString("hex"),
      name: "Wallet",
      mode: "fixture-managed",
      publicKey: account.publicKey,
      network: account.network,
      signer: account,
    };
    current.record.accounts.push(managed);
    cli(
      ["accounts", "--prepare-host", "--request-stdin"],
      JSON.stringify(current.record),
    );
    await refreshFixtureAccounts(page);
    await page
      .getByRole("heading", { name: managed.name, exact: true })
      .waitFor();
    current = JSON.parse(cli(["accounts", "--prepare-host"]));
    check(
      "accounts:realConfiguredPublicReference",
      current.record.accounts.find((a) => a.id === managed.id).signer.epoch ===
        account.epoch && managed.publicKey === account.publicKey,
    );
    const id = randomBytes(16).toString("hex");
    const policy = {
      account,
      asset: "fixture-credit:v1",
      budget: "150",
      maxOperation: "10",
      destinations: ["fixture:treasury"],
      expiresAt: Math.floor(Date.now() / 1000) + 900,
    };
    const proposal = {
      version: 1,
      id,
      policy,
      grant: {
        ...policy,
        id,
        botId: randomBytes(16).toString("hex"),
        budget: "20",
        revoked: false,
      },
    };
    current.record.proposals.push({ accountId: managed.id, proposal });
    cli(
      ["accounts", "--prepare-host", "--request-stdin"],
      JSON.stringify(current.record),
    );
    const proposalFile = backups + "/public-proposal.json";
    await writeFile(proposalFile, JSON.stringify(proposal) + "\n", {
      mode: 0o600,
    });
    check(
      "accounts:proposalHasNoAuthority",
      app("fixture-approvals", { proposalIds: [proposal.id] }).approvals[0]
        .state === "not-approved",
    );
    const tty = String.raw`
import os,pty,select,sys,time,re,termios,signal,json
assert os.geteuid()!=0
binary,path,choice=sys.argv[1:];password=sys.stdin.readline().rstrip('\n');pid,fd=pty.fork()
if pid==0: os.execv(binary,[binary,'custody','approve','--disposable-fixture','--proposal-file',path])
seen=b'';stage=0;done=0;deadline=time.time()+20
try:
 while time.time()<deadline:
  ready,_,_=select.select([fd],[],[],.05)
  if ready:
   try:chunk=os.read(fd,8192)
   except OSError:break
   if not chunk:break
   seen+=chunk
  if stage==0:
   match=re.search(rb'Type APPROVE ([0-9a-f]{64})',seen)
   if match:
    os.write(fd,(b'APPROVE '+match[1] if choice=='approve' else b'CANCEL')+b'\n');stage=1
  if stage==1 and choice=='approve' and b'Fixture unlock password:' in seen:
   if not (termios.tcgetattr(fd)[3]&termios.ECHO):os.write(fd,password.encode()+b'\n');stage=2
  done,status=os.waitpid(pid,os.WNOHANG)
  if done:break
 else:raise Exception('owner approval timed out')
 if not done:done,status=os.waitpid(pid,0)
 code=os.waitstatus_to_exitcode(status)
 assert password.encode() not in seen,'wallet password echoed'
 assert b'"budget": "150"' in seen and b'"budget": "20"' in seen,'review omitted limits'
 if choice=='approve':assert code==0 and stage==2,'approval failed: '+seen.decode(errors='replace').replace(password,'[redacted]')
 else:assert code!=0 and stage==1 and b'approval cancelled' in seen,'cancel failed'
 print(json.dumps({'terminal':True,'passwordEcho':False,'choice':choice,'exactLimitsReviewed':True}))
finally:
 os.close(fd)
 if not done:
  os.killpg(pid,signal.SIGKILL);os.waitpid(pid,0)
`;
    const terminal = (choice) =>
      JSON.parse(
        execFileSync(
          "/usr/bin/python3",
          ["-c", tty, engine, proposalFile, choice],
          {
            env,
            input: walletPassword + "\n",
            encoding: "utf8",
            timeout: 25000,
          },
        ),
      );
    check(
      "accounts:actualOwnerTTYCancel",
      terminal("cancel").choice === "cancel",
    );
    check(
      "accounts:cancelDidNotGrant",
      app("fixture-approvals", { proposalIds: [proposal.id] }).approvals[0]
        .state === "not-approved",
    );
    check(
      "accounts:actualOwnerTTYApprovalNoEcho",
      terminal("approve").passwordEcho === false,
    );
    check("accounts:approvalCompletionLocksVault", ownerStatus().locked);
    approvalDiagnostic.target = {
      accountId: managed.id,
      proposalId: proposal.id,
    };
    current = JSON.parse(cli(["accounts", "--prepare-host"]));
    approvalDiagnostic.cli = publicApprovalView(current);
    const cliProof = verifiedFixtureApproval(current, managed, proposal);
    const origin = new URL(url).origin;
    const refreshed = page.waitForResponse((response) => {
      const endpoint = new URL(response.url());
      return (
        response.request().method() === "GET" &&
        endpoint.origin === origin &&
        endpoint.pathname === "/api/v1/accounts"
      );
    });
    await refreshFixtureAccounts(page);
    const response = await refreshed;
    approvalDiagnostic.httpStatus = response.status();
    assert.equal(response.status(), 200, "public approval read failed");
    const api = await response.json();
    approvalDiagnostic.api = publicApprovalView(api);
    const apiProof = verifiedFixtureApproval(api, managed, proposal);
    assert.deepEqual(
      apiProof,
      cliProof,
      "public CLI/API approval snapshots disagree",
    );
    await revealFixtureApproval(page, managed.name, proposal.id, apiProof.row);
    check(
      "accounts:verifiedApprovalSharedUIAndCLI",
      apiProof.digest === cliProof.digest,
    );
    ownerUnlock(walletPassword);
    const operation = {
      account,
      operationId: randomBytes(16).toString("hex"),
      grantId: proposal.grant.id,
      botId: proposal.grant.botId,
      action: "fixture-transfer",
      asset: "fixture-credit:v1",
      destination: "fixture:treasury",
      units: "5",
      expiresAt: proposal.grant.expiresAt,
    };
    app("fixture-reserve", { intent: operation });
    check(
      "accounts:ownerApprovedBoundedMockOperation",
      app("fixture-execute", { operationId: operation.operationId }).operation
        .state === "confirmed",
    );
    ownerLock();
    check(
      "accounts:realExecutionStillDisabled",
      !ownerStatus().walletSigner && !ownerStatus().financialExecution,
    );
    await page.screenshot({
      path: evidence + "/accounts-ui-desktop.png",
      fullPage: true,
    });
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
      true,
      "mobile account layout overflow",
    );
    await page.screenshot({
      path: evidence + "/accounts-ui-mobile.png",
      fullPage: true,
    });
    check("accounts:actualManagedUIResponsive", true);
  } catch (error) {
    if (page) {
      // Public receipt DTOs and structural names only: never auth headers,
      // input values, private storage, URLs, passwords or backup material.
      const dom = await page
        .evaluate(() => ({
          language: document.documentElement.lang,
          accounts: [
            ...document.querySelectorAll('article[aria-label^="Account "]'),
          ].map((node) => ({
            name: node.getAttribute("aria-label"),
            receipts: [
              ...node.querySelectorAll(".account-permission .badge"),
            ].map((badge) => badge.textContent),
          })),
          proposals: [
            ...document.querySelectorAll('article[aria-label^="Proposal "]'),
          ].map((node) => ({
            name: node.getAttribute("aria-label"),
            states: [...node.querySelectorAll(".badge")].map(
              (badge) => badge.textContent,
            ),
          })),
          disclosures: [...document.querySelectorAll("details")].map(
            (node) => ({ class: node.className, open: node.open }),
          ),
        }))
        .catch(() => ({ unavailable: true }));
      await writeFile(
        evidence + "/accounts-approval-public.json",
        JSON.stringify({ ...approvalDiagnostic, dom }, null, 2) + "\n",
        { mode: 0o600 },
      ).catch(() => {});
    }
    if (page)
      await page
        .screenshot({
          path: evidence + "/accounts-ui-failure.png",
          fullPage: true,
        })
        .catch(() => {});
    throw error;
  } finally {
    await browser.close();
  }
}
