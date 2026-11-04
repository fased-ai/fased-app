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

export async function revealFixtureApproval(page, name) {
  const wallet = page.getByRole("article", {
    name: `Account ${name}`,
    exact: true,
  });
  const permission = wallet.locator(".account-permission");
  if (await permission.count()) {
    const details = wallet.locator(".account-details");
    if (!(await details.evaluate((element) => element.open)))
      await details.locator(":scope > summary").click();
    await permission.getByText("approved", { exact: true }).waitFor();
    return;
  }
  // A pinned predecessor still keeps its verified proposal history in Advanced.
  await revealFixtureAccountEntry(page);
  await page.getByText("approved", { exact: true }).waitFor();
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
    await refreshFixtureAccounts(page);
    await revealFixtureApproval(page, managed.name);
    current = JSON.parse(cli(["accounts", "--prepare-host"]));
    check(
      "accounts:verifiedApprovalSharedUIAndCLI",
      current.proposals[0].receipt.state === "approved" &&
        current.proposals[0].receipt.digest === current.proposals[0].digest,
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
