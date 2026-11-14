// One real Settings update in the disposable Local VM, never a production UI.
import { chromium, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";

// Remote requests use the actual device-bound HTTPS session. No Local session,
// cookie, test CA or SSH-forwarded application listener is imported.
async function remoteFixture(path) {
  const config = JSON.parse(await readFile(path, "utf8"));
  const origin = new URL(config.origin);
  if (
    config.fixtureOnly !== true ||
    origin.protocol !== "https:" ||
    origin.href !== origin.origin + "/" ||
    !/^[a-z0-9-]+\.[a-z0-9-]+\.ts\.net$/.test(origin.hostname) ||
    origin.port ||
    !["claim", "wallet", "apply", "verify", "proof"].includes(config.action) ||
    !/^[A-Za-z0-9_-]{43}$/.test(config.instance ?? "")
  )
    throw new Error("exact private fixture identity required");
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({
      locale: "en-US",
      viewport: { width: 1200, height: 850 },
    });
    await context.route("**/*", (route) =>
      new URL(route.request().url()).origin === origin.origin
        ? route.continue()
        : route.abort(),
    );
    const page = await context.newPage();
    page.setDefaultTimeout(30000);
    if (config.action === "proof") {
      const proof = new URL(config.url);
      if (
        proof.origin !== origin.origin ||
        proof.username ||
        proof.password ||
        proof.pathname !== "/.fased-private-access" ||
        proof.search ||
        !/^#[A-Za-z0-9_-]{43}$/.test(proof.hash)
      )
        throw new Error("exact private proof link required");
      const accepted = page.waitForResponse(
        (r) =>
          r.url() === origin.origin + "/api/v1/private-access/confirm" &&
          r.request().method() === "POST",
      );
      await page.goto(proof.href);
      if (!(await accepted).ok())
        throw new Error("private device proof was refused");
      return { result: "PASS", action: "proof", fixtureOnly: true };
    }
    let url = `${origin.origin}/?instance=${config.instance}`;
    if (config.action === "claim") {
      const setup = new URL(config.url);
      if (
        setup.origin !== origin.origin ||
        setup.username ||
        setup.password ||
        setup.pathname !== "/" ||
        setup.search !== `?instance=${config.instance}` ||
        !/^#setup=[A-Za-z0-9_-]{43}$/.test(setup.hash)
      )
        throw new Error("exact Remote owner handoff required");
      url = setup.href;
    }
    await page.goto(url);
    const request = async (route, method = "GET", body) =>
      page.evaluate(
        async ({ route, method, body }) => {
          const token = sessionStorage.getItem("fased.core.session.v1"),
            headers = {};
          if (token) headers.Authorization = `Bearer ${token}`;
          if (method !== "GET") {
            const state = await (
              await fetch("/api/v1/auth/state", {
                headers,
                credentials: "same-origin",
              })
            ).json();
            if (!state.authenticated || !state.csrfToken)
              throw Error("device authentication required");
            headers["Content-Type"] = "application/json";
            headers["X-CSRF-Token"] = state.csrfToken;
          }
          const response = await fetch("/api/v1/" + route, {
            method,
            headers,
            credentials: "same-origin",
            body: body === undefined ? undefined : JSON.stringify(body),
          });
          if (!response.ok)
            throw Error(`private request failed: HTTP ${response.status}`);
          return response.json();
        },
        { route, method, body },
      );
    await expect
      .poll(
        async () => {
          try {
            return (await request("auth/state")).authenticated;
          } catch {
            return false;
          }
        },
        { timeout: 120000 },
      )
      .toBe(true);
    const health = await request("health");
    if (
      health.instanceId !== config.instance ||
      health.connection?.location !== "remote" ||
      health.connection?.transport !== "private-https"
    )
      throw new Error("Remote instance or transport differs");
    if (config.action === "claim") {
      let view = await request("agents");
      const before = new Set(view.tasks.map((t) => t.id));
      view = await request("agents", "POST", {
        action: "task-create",
        revision: view.revision,
        profileId: view.profileId,
        name: "Disposable preserved task",
        text: "Fixture reminder",
        intervalSeconds: 3600,
        maxRuns: 2,
      });
      const task = view.tasks.find((t) => !before.has(t.id));
      if (!task) throw new Error("preserved fixture task missing");
      await request("agents", "POST", {
        action: "task-pause",
        revision: view.revision,
        profileId: view.profileId,
        id: task.id,
      });
      return {
        result: "PASS",
        action: "claim",
        fixtureOnly: true,
        taskId: task.id,
      };
    }
    if (config.action === "wallet") {
      let view = await request("accounts");
      view = await request("accounts/setup", "PUT", {
        revision: view.record.revision,
        action: "attach",
        name: "Disposable wallet",
        network: "devnet",
        reference: config.signerAccount,
      });
      const account = view.record.accounts.find(
        (a) => a.signer?.epoch === config.signerAccount?.epoch,
      );
      if (!account) throw new Error("original fixture signer account missing");
      view = await request("accounts/rpc", "PUT", {
        revision: view.record.revision,
        accountId: account.id,
        network: "devnet",
        endpoint: "https://devnet.helius-rpc.com",
      });
      const credential = await request("accounts/rpc-credential", "PUT", {
        revision: view.record.revision,
        accountId: account.id,
        action: "save",
        key: "disposable-rpc-fixture-only",
        passphrase: "Disposable custody RPC 5CJmX8sB",
      });
      if (
        !credential.saved ||
        !credential.locked ||
        credential.liveRPC ||
        typeof credential.ref !== "string" ||
        !credential.ref
      )
        throw new Error("locked fixture RPC required");
      const accounts = await request("accounts");
      return {
        result: "PASS",
        action: "wallet",
        fixtureOnly: true,
        accountId: account.id,
        revision: accounts.record.revision,
        credentialRef: credential.ref,
        accounts: accounts.record,
      };
    }
    if (config.action === "apply") {
      await page.goto(`${origin.origin}/?instance=${config.instance}#settings`);
      const panel = page.locator(
        'section[aria-labelledby="application-updates-title"]',
      );
      await panel.getByRole("button", { name: "Check", exact: true }).click();
      await panel
        .getByRole("button", { name: "Update", exact: true })
        .waitFor();
      await panel.getByText("0.1.12", { exact: true }).waitFor();
      await panel.getByRole("button", { name: "Update", exact: true }).click();
      const response = page.waitForResponse(
        (r) =>
          r.url().endsWith("/api/v1/updates/application") &&
          r.request().method() === "POST" &&
          r.request().postDataJSON()?.action === "apply",
      );
      await panel
        .getByRole("button", { name: "Update now", exact: true })
        .click();
      const accepted = await response;
      if (!accepted.ok())
        throw new Error(`update request refused: HTTP ${accepted.status()}`);
      const operationId = (await accepted.json()).operation?.id;
      if (!/^[a-f0-9]{32}$/.test(operationId ?? ""))
        throw new Error("original update identity missing");
      return {
        result: "APPLY_SENT",
        action: "apply",
        fixtureOnly: true,
        operationId,
      };
    }
    if (config.action === "verify") {
      const agents = await request("agents");
      const accounts = await request("accounts");
      if (
        !agents.tasks.some((t) => t.id === config.taskId) ||
        accounts.record.revision !== config.revision ||
        !accounts.record.accounts.some((a) => a.id === config.accountId)
      )
        throw new Error("original Remote records changed");
      const read = await request("accounts/read", "POST", {
        accountId: config.accountId,
        revision: config.revision,
        ref: config.credentialRef,
        passphrase: "Disposable custody RPC 5CJmX8sB",
      });
      if (read.fixtureOnly !== true || read.liveRPC !== false)
        throw new Error("fixture-only RPC result required");
      return {
        result: "PASS",
        action: "verify",
        fixtureOnly: true,
        accounts: accounts.record,
        walletRead: read,
      };
    }
    throw new Error("unknown Remote fixture action");
  } finally {
    await browser.close();
  }
}

if (process.argv[2] === "--remote") {
  process.stdout.write(
    JSON.stringify(await remoteFixture(process.argv[3])) + "\n",
  );
} else {
  const [port, instance] = process.argv.slice(2);
  if (
    !/^[0-9]{4,5}$/.test(port ?? "") ||
    Number(port) < 1024 ||
    Number(port) > 65535 ||
    !/^[A-Za-z0-9_-]{43}$/.test(instance ?? "")
  )
    throw new Error("fixed fixture listener and instance required");
  const origin = `http://127.0.0.1:${port}`;
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({
      locale: "en-US",
      viewport: { width: 1200, height: 850 },
    });
    await context.route("**/*", (route) =>
      new URL(route.request().url()).origin === origin
        ? route.continue()
        : route.abort(),
    );
    const page = await context.newPage();
    page.setDefaultTimeout(30000);
    await page.goto(`${origin}/?instance=${instance}#settings`);
    await page
      .getByLabel("Owner password", { exact: true })
      .fill("Disposable custody owner 7WQfE2tV");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    const panel = page.locator(
      'section[aria-labelledby="application-updates-title"]',
    );
    await panel.getByRole("button", { name: "Check", exact: true }).click();
    await panel.getByRole("button", { name: "Update", exact: true }).waitFor();
    await panel.getByText("0.1.12", { exact: true }).waitFor();
    await panel.getByRole("button", { name: "Update", exact: true }).click();
    const response = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/v1/updates/application") &&
        r.request().method() === "POST" &&
        r.request().postDataJSON()?.action === "apply",
    );
    await panel
      .getByRole("button", { name: "Update now", exact: true })
      .click();
    const accepted = await response;
    if (!accepted.ok())
      throw new Error(`update request refused: HTTP ${accepted.status()}`);
    const operationId = (await accepted.json()).operation?.id;
    if (!/^[a-f0-9]{32}$/.test(operationId ?? ""))
      throw new Error(
        "update response did not identify the original operation",
      );
    process.stdout.write(
      JSON.stringify({
        result: "APPLY_SENT",
        fixtureOnly: true,
        version: "0.1.12",
        operationId,
      }) + "\n",
    );
  } finally {
    await browser.close();
  }
}
