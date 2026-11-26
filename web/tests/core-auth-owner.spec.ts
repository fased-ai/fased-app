import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { once } from "node:events";
import {
  installCoreFixture,
  captureScreenshot,
  choosePasswordSignIn,
  revealControl,
  exec,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  output,
  start,
  stop,
  privateCLI,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("Local owner setup opens without a password and keeps logout final", async ({
  page,
}) => {
  await stop();
  await start(false, "127.0.0.1:0", "local", false);
  await page.goto(url);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Chat", exact: true }).first().click();
  await expect(
    page.getByRole("heading", { name: "Chat", exact: true }),
  ).toBeVisible();
  const connectCard = page.getByRole("region", {
    name: "Connect AI",
    exact: true,
  });
  await expect(connectCard).toBeVisible();
  await expect(
    connectCard.getByRole("button", {
      name: "Connect provider",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    connectCard.getByRole("button", { name: "Skip", exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Open Fased", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Open Fased", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Instance details")).toHaveCount(0);
  await expect(page.getByText("Open Fased on this computer.")).toHaveCount(0);
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toHaveCount(0);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Open Fased", exact: true }),
  ).toBeVisible();

  const missingHeader = await page.request.post(url + "/api/v1/auth/local", {
    headers: { Origin: url },
    data: { action: "browser" },
  });
  expect(missingHeader.status()).toBe(403);
  for (const sfs of ["cross-site", "same-site"]) {
    const blocked = await page.request.post(url + "/api/v1/auth/local", {
      headers: {
        Origin: url,
        "X-Fased-Local": "1",
        "Sec-Fetch-Site": sfs,
      },
      data: { action: "browser" },
    });
    expect(blocked.status()).toBe(403);
  }

  await page.getByRole("button", { name: "Open Fased", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();

  const { stdout } = await exec(binary, ["setup-link", "--data-dir", dir]);
  const link = stdout.trim(),
    proof = new URL(link).hash.slice("#local=".length);
  expect(new URL(link).hash).toMatch(/^#local=[A-Za-z0-9_-]{43}$/);
  const requests: string[] = [];
  page.on("request", (request) => requests.push(request.url()));
  await page.goto(link);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  expect(page.url()).not.toContain(proof);
  expect(requests.some((address) => address.includes(proof))).toBe(false);
  expect(
    await page.evaluate(
      (proof) =>
        [...Object.keys(sessionStorage), ...Object.keys(localStorage)].some(
          (key) =>
            (
              sessionStorage.getItem(key) ??
              localStorage.getItem(key) ??
              ""
            ).includes(proof),
        ),
      proof,
    ),
  ).toBe(false);
  const replay = await page.request.post(url + "/api/v1/auth/local", {
    headers: { Origin: url },
    data: { action: "exchange", code: proof },
  });
  expect(replay.status()).toBe(401);
  // Opening an already-authenticated tab must not retain a latent sign-in.
  const reopened = (
    await exec(binary, ["setup-link", "--data-dir", dir])
  ).stdout.trim();
  await page.goto(reopened);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Open Fased", exact: true }),
  ).toBeVisible();
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toHaveCount(0);
  const next = (
    await exec(binary, ["setup-link", "--data-dir", dir])
  ).stdout.trim();
  await page.goto(next);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  // The CLI uses the same protected OS owner and Update API, without login.
  await expect(readFile(join(dir, "cli-session.json"))).rejects.toThrow();
  const view = await privateCLI(["updates", "status"]);
  expect(view.instanceId).toBeTruthy();
  const saved = await readFile(join(dir, "cli-session.json"), "utf8");
  await privateCLI(["updates", "status"]);
  expect(await readFile(join(dir, "cli-session.json"), "utf8")).toBe(saved);
});

test("expired owner access returns to sign-in without replay or stale-session restoration", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  const writes: string[] = [];
  page.on("request", (request) => {
    if (!ownedFixtureOrigins.has(new URL(request.url()).origin))
      external.push(request.url());
    if (request.method() !== "GET")
      writes.push(`${request.method()} ${new URL(request.url()).pathname}`);
  });
  await page.goto(url + "/#agents/tasks");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  const signOut = page.getByRole("button", { name: "Sign out", exact: true });
  const signIn = page.getByRole("button", { name: "Sign in", exact: true });
  const password = page.getByLabel("Owner password", { exact: true });
  await expect(signOut).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const readAll = () =>
    Promise.all(
      ["wen-operator", "trading-analyst", "news-researcher"].map((id) =>
        privateCLI(["agents", "--profile", id]),
      ),
    );
  const initial = await readAll();
  await privateCLI(["agents", "--request-stdin"], {
    action: "task-create",
    profileId: "trading-analyst",
    revision: initial[1].revision,
    name: "Owner-only fixture reminder",
    text: "Retain this fixture record through authentication changes.",
    intervalSeconds: 3600,
    maxRuns: 1,
  });
  const baseline = await readAll();
  const trading = () => page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByText("Owner-only fixture reminder", { exact: true }),
  ).toBeVisible();
  const headers = async () => {
    const token = await page.evaluate(() =>
      sessionStorage.getItem("fased.core.session.v1"),
    );
    expect(token).toBeTruthy();
    return { Authorization: `Bearer ${token}`, Origin: url };
  };
  const revoke = async () => {
    const h = await headers();
    const state = await page.request.get(url + "/api/v1/auth/state", {
      headers: h,
    });
    expect(state.status()).toBe(200);
    const response = await page.request.post(url + "/api/v1/auth/logout", {
      headers: { ...h, "X-CSRF-Token": (await state.json()).csrfToken },
      data: {},
    });
    expect(response.status()).toBe(200);
  };
  const enter = async () => {
    await revealControl(page, password);
    await password.fill(fixturePassword);
    await signIn.click();
    await expect(signOut).toBeEnabled();
  };
  await revoke();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await choosePasswordSignIn(page);
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("alert")).toHaveText(
    "Your session ended. Sign in again.",
  );
  await expect(password).toBeFocused();
  await expect(
    page.getByText("Owner-only fixture reminder", { exact: true }),
  ).toHaveCount(0);
  expect(
    await page.evaluate(() => sessionStorage.getItem("fased.core.session.v1")),
  ).toBeNull();
  expect(writes.filter((s) => s.endsWith("/auth/login"))).toHaveLength(0);
  await captureScreenshot(page, {
    path: `.evidence/owner-sign-in-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await revealControl(page, password);
  await password.fill("incorrect-disposable-password");
  await signIn.click();
  await expect(page.getByRole("alert")).toContainText("authentication failed");
  await expect(signIn).toBeEnabled();
  await enter();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  expect(await readAll()).toEqual(baseline);

  // A committed write and an old 401 may arrive after the owner signs in again.
  let releaseSetup = () => {};
  let releaseOldRead = () => {};
  const setupGate = new Promise<void>((resolve) => {
    releaseSetup = resolve;
  });
  const oldReadGate = new Promise<void>((resolve) => {
    releaseOldRead = resolve;
  });
  let setupHeld = false;
  let setupDelivered = false;
  let oldReadStarted = false;
  let oldReadHeld = false;
  let oldReadDelivered = false;
  const setupURL = url + "/api/v1/setup";
  const agentsURL = "**/api/v1/agents?**";
  await page.route(setupURL, async (route) => {
    if (route.request().method() !== "PUT") return route.continue();
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    setupHeld = true;
    await setupGate;
    await route.fulfill({ response });
    setupDelivered = true;
  });
  try {
    await page.goto(url + "/#settings");
    await page.getByRole("button", { name: "Setup", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Instance", exact: true })
      .getByRole("button", { name: "Open Chat", exact: true })
      .click();
    await expect.poll(() => setupHeld).toBe(true);
    await page.keyboard.press("Escape");
    await expect(
      page.getByRole("dialog", { name: "Instance", exact: true }),
    ).toHaveCount(0);
    await exec(binary, ["setup", "--data-dir", dir, "--host", "vps"]);
    const current = await page.request.get(setupURL, {
      headers: await headers(),
    });
    const currentSetup = await current.json();
    expect(currentSetup.settings.host).toBe("vps");
    await page.goto(url + "/#agents/tasks");
    await page.route(agentsURL, async (route) => {
      if (oldReadStarted) return route.continue();
      oldReadStarted = true;
      oldReadHeld = true;
      await oldReadGate;
      const response = await route.fetch();
      expect(response.status()).toBe(401);
      await route.fulfill({ response });
      oldReadDelivered = true;
    });
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect.poll(() => oldReadHeld).toBe(true);
    await revoke();
    // A subsequent protected poll ends the session; retain its original 12s poll allowance.
    await choosePasswordSignIn(page, 12000);
    await expect(
      page.getByRole("heading", { name: "Sign in", exact: true }),
    ).toBeVisible({ timeout: 12000 });
    await expect(signIn).toBeEnabled();
    await enter();
    await page.goto(url + "/#settings");
    await page.getByText("Diagnostics", { exact: true }).click();
    await expect(page.getByTestId("saved-deployment-preference")).toHaveText(
      "Remote",
    );
    releaseOldRead();
    releaseSetup();
    await expect.poll(() => setupDelivered && oldReadDelivered).toBe(true);
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(signOut).toBeEnabled();
    await expect(password).toHaveCount(0);
    await expect(page.getByTestId("saved-deployment-preference")).toHaveText(
      "Remote",
    );
    const after = await page.request.get(setupURL, {
      headers: await headers(),
    });
    expect(await after.json()).toEqual(currentSetup);
  } finally {
    releaseOldRead();
    releaseSetup();
    if (!page.isClosed()) {
      await page.unroute(agentsURL);
      await page.unroute(setupURL);
    }
  }
  await page.route(agentsURL, (route) =>
    route.fulfill({
      status: 403,
      json: { error: "fixture permission denied" },
    }),
  );
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "fixture permission denied",
  );
  await expect(signOut).toBeVisible();
  await expect(password).toHaveCount(0);
  await page.unroute(agentsURL);
  await page
    .getByRole("button", { name: "Refresh records", exact: true })
    .click();
  await expect(trading()).toBeEnabled();
  expect(await readAll()).toEqual(baseline);
  expect(writes.filter((s) => s === "PUT /api/v1/setup")).toHaveLength(1);
  expect(writes.filter((s) => s === "POST /api/v1/auth/login")).toHaveLength(3);
  expect(writes.filter((s) => s === "POST /api/v1/agents")).toEqual([]);
  expect(external).toEqual([]);
});

test("claim, UI/CLI setup, restart and logout preserve one Local instance", async ({
  page,
}) => {
  await page.goto(url + "/#overview");
  await expect(page.getByRole("heading", { name: "Open Fased" })).toBeVisible();
  await captureScreenshot(page, {
    path: `.evidence/claim-${test.info().project.name}.png`,
    fullPage: true,
  });
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const health = await (await page.request.get(`${url}/api/v1/health`)).json();
  expect(health.capabilities.walletSigner).toBe(false);
  expect(health.capabilities.financialExecution).toBe(false);
  expect(
    await page.evaluate(() =>
      Object.keys(localStorage).some((key) => key.includes("session")),
    ),
  ).toBe(false);
  expect(output).not.toContain(claim.code);
  expect(output).not.toContain(fixturePassword);
  await page.getByRole("button", { name: "Setup", exact: true }).click();
  const instance = page.getByRole("dialog", { name: "Instance", exact: true });
  await expect(instance.getByText("Local", { exact: true })).toBeVisible();
  await expect(
    instance.getByRole("button", { name: /Remote Always-on/ }),
  ).toHaveCount(0);
  await instance.getByText("Instance details", { exact: true }).click();
  await expect(
    instance.getByText(health.instanceId, { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  // CLI credentials use the protected socket. Saved preferences cannot move
  // this runtime or grant wallet/provider authority.
  await new Promise<void>((resolveLogin, reject) => {
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin.end(fixturePassword + "\n");
    login.on("error", reject);
    login.on("exit", (code) =>
      code === 0
        ? resolveLogin()
        : reject(new Error("fixture CLI login failed")),
    );
  });
  const current = JSON.parse(
    (await exec(binary, ["setup", "--data-dir", dir])).stdout,
  );
  expect(current.settings.host).toBe("local");
  await exec(binary, [
    "setup",
    "--data-dir",
    dir,
    "--host",
    "vps",
    "--wallet-mode",
    "watch",
  ]);
  await page.reload();
  await page.getByRole("button", { name: "Setup", exact: true }).click();
  await expect(instance.getByText("Local", { exact: true })).toBeVisible();
  const saveSetup = instance.getByRole("button", { name: /Open chat/i });
  // Preserve the existing stale-review contract through the compact setup UI.
  await exec(binary, ["setup", "--data-dir", dir, "--provider", "anthropic"]);
  await saveSetup.click();
  await expect(instance.getByRole("alert")).toHaveText(
    /settings changed in another client/,
  );
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.complete,
  ).toBe(false);
  await saveSetup.click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const saved = JSON.parse(
    (await exec(binary, ["setup", "--data-dir", dir])).stdout,
  ).settings;
  expect(saved).toMatchObject({
    complete: true,
    host: "vps",
    walletMode: "watch",
    provider: "anthropic",
  });
  await page.goto(url + "/#settings");
  await page.getByText("Diagnostics", { exact: true }).click();
  await expect(page.getByTestId("saved-deployment-preference")).toHaveText(
    "Remote",
  );
  await expect(page.getByLabel("Instance location")).toHaveText("Local");
  const unchangedHealth = await (
    await page.request.get(`${url}/api/v1/health`)
  ).json();
  expect(unchangedHealth.instanceId).toBe(health.instanceId);
  expect(unchangedHealth.connection).toEqual({
    location: "local",
    transport: "loopback",
  });
  expect(unchangedHealth.capabilities.aiConnections).toBe(true);
  for (const key of ["walletSigner", "financialExecution", "updater"])
    expect(unchangedHealth.capabilities[key]).toBe(false);
  const duplicate = await exec(binary, [
    "serve",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    dir,
  ]).then(
    () => "unexpected",
    (e: Error) => e.message,
  );
  expect(duplicate).toContain("already owns");

  await stop();
  await start();
  const restarted = JSON.parse(
    (await exec(binary, ["status", "--data-dir", dir])).stdout,
  );
  expect(restarted.instanceId).toBe(health.instanceId);
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.walletMode,
  ).toBe("watch");
  await page.goto(url);
  await choosePasswordSignIn(page);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await choosePasswordSignIn(page);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  expect((await page.request.get(`${url}/api/v1/setup`)).status()).toBe(401);
});

test("production UI fails closed when the core API is unavailable", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/state", (route) =>
    route.fulfill({
      status: 503,
      contentType: "text/html",
      body: "unavailable",
    }),
  );
  await page.goto(url);
  await expect(
    page.getByRole("heading", { name: "Connection unavailable" }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Setup" })).toHaveCount(0);
});

test("installation connection rejects Remote tunnels without changing Local data", async ({
  page,
  isMobile,
}) => {
  await page.goto(url + "/#overview");
  await expect(page.getByText("Local", { exact: true })).toBeVisible();
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  await expect(page.locator("html")).toHaveCSS("font-family", /Fased Inter/);
  const rows = page.locator('[data-widget="agents"] .dashboard-row');
  await expect(rows.first()).toHaveCSS("display", "flex");
  const name = await rows.first().locator("strong").boundingBox();
  const card = await page.locator('[data-widget="agents"]').boundingBox();
  expect(name!.x + name!.width).toBeLessThanOrEqual(card!.x + card!.width);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  const mutatePreference = await page.evaluate(async () => {
    const headers: Record<string, string> = {
      Authorization:
        "Bearer " + sessionStorage.getItem("fased.core.session.v1"),
    };
    const auth = await (await fetch("/api/v1/auth/state", { headers })).json();
    const setup = await (await fetch("/api/v1/setup", { headers })).json();
    const response = await fetch("/api/v1/setup", {
      method: "PUT",
      headers: {
        ...headers,
        "Content-Type": "application/json",
        "X-CSRF-Token": auth.csrfToken,
      },
      body: JSON.stringify({
        ...setup,
        settings: { ...setup.settings, host: "vps" },
      }),
    });
    return response.status;
  });
  expect(mutatePreference).toBe(200);
  await page.reload();
  await expect(page.getByLabel("Instance location")).toHaveText("Local");
  const before = await page.evaluate(async () => {
    const headers = {
      Authorization:
        "Bearer " + sessionStorage.getItem("fased.core.session.v1"),
    };
    return (await fetch("/api/v1/setup", { headers })).json();
  });
  await page.goto(url + "/#settings");
  await page.getByRole("button", { name: "Setup", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Instance", exact: true });
  await expect(dialog.getByText("Local", { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("Wallet preference")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Open Chat", exact: true }).click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  await expect(
    page
      .getByRole("navigation", { name: "Main navigation", exact: true })
      .getByRole("link", { name: "Chat", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#settings");
  await page.getByText("Connect to Remote", { exact: true }).click();
  const connect = page.getByRole("button", { name: "Connect", exact: true });
  await expect(connect).toHaveCSS("background-color", "rgb(212, 54, 90)");
  await connect.hover();
  await expect(connect).toHaveCSS("background-color", "rgb(194, 43, 78)");
  await page.getByLabel("Instance URL").fill("http://untrusted.example");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("HTTPS");
  // A second ordinary process is a disposable remote-topology fixture, not a VPS.
  const remoteDir = await mkdtemp(join(tmpdir(), "fcr-"));
  let remoteOutput = "";
  const remote = spawn(binary, [
    "serve",
    "--instance-location",
    "remote",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    remoteDir,
  ]);
  remote.stdout!.on("data", (data) => (remoteOutput += String(data)));
  remote.stderr!.on("data", (data) => (remoteOutput += String(data)));
  try {
    await expect
      .poll(() => remoteOutput.includes('"event":"listening"'))
      .toBe(true);
    const remoteURL = JSON.parse(
      remoteOutput
        .trim()
        .split("\n")
        .find((line) => line.includes('"event":"listening"'))!,
    ).url;
    const health = await (
      await page.request.get(remoteURL + "/api/v1/health")
    ).json();
    await expect(
      exec(binary, [
        "connect",
        "--url",
        remoteURL,
        "--instance-id",
        health.instanceId,
        "--print",
      ]),
    ).rejects.toThrow(/Tailscale HTTPS/);
    await page.getByLabel("Instance URL").fill(remoteURL);
    const pageCount = page.context().pages().length;
    await page.getByRole("button", { name: "Connect", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("Tailscale HTTPS");
    expect(page.context().pages()).toHaveLength(pageCount);
    const after = await page.evaluate(async () => {
      const headers = {
        Authorization:
          "Bearer " + sessionStorage.getItem("fased.core.session.v1"),
      };
      return (await fetch("/api/v1/setup", { headers })).json();
    });
    expect(after.settings.host).toBe(before.settings.host);
    expect(after.settings.walletMode).toBe(before.settings.walletMode);
    expect(after.settings.provider).toBe(before.settings.provider);
    await expect(page.getByLabel("Instance location")).toHaveText("Local");
    await captureScreenshot(page, {
      path: `.evidence/installation-local-${isMobile ? "mobile" : "desktop"}.png`,
      fullPage: true,
    });
    expect(health.capabilities.aiConnections).toBe(true);
    for (const name of ["financialExecution", "walletSigner", "updater"])
      expect(health.capabilities[name]).toBe(false);
  } finally {
    if (remote.exitCode === null) {
      const done = once(remote, "exit");
      remote.kill("SIGTERM");
      await done;
    }
    await rm(remoteDir, { recursive: true, force: true });
  }
  await stop();
  await start(false, "127.0.0.1:0", "remote");
  await page.goto(url);
  await expect(page.getByText("Remote", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Owner password")).toBeVisible();
});

test("installation connection refuses changed identity before sending an owner password", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await choosePasswordSignIn(page);
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  let sent = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/auth/login") && r.method() === "POST") sent++;
  });
  await page.route("**/api/v1/health", async (route) => {
    expect(route.request().headers()["authorization"]).toBeUndefined();
    expect(route.request().headers()["cookie"]).toBeUndefined();
    const response = await route.fetch();
    const h = await response.json();
    h.instanceId = "x".repeat(43);
    await route.fulfill({ response, json: h });
  });
  await choosePasswordSignIn(page);
  await page.getByLabel("Owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("identity changed");
  expect(sent).toBe(0);
});

for (const restored of [false, true]) {
  test(
    restored
      ? "Remote restored session stays signed out"
      : "Remote approved device connects without a password and logout stays final",
    async ({ page }) => {
      await exec(binary, ["setup", "--data-dir", dir]);
      const saved = JSON.parse(
        await readFile(join(dir, "cli-session.json"), "utf8"),
      ) as { token: string };
      const auth = await page.request.get(url + "/api/v1/auth/state", {
        headers: { Authorization: `Bearer ${saved.token}` },
      });
      const state = (await auth.json()) as { csrfToken: string };
      let signedIn = restored,
        connections = 0;
      await page.route("**/api/v1/health", async (route) => {
        const response = await route.fetch();
        const health = await response.json();
        await route.fulfill({
          json: {
            ...health,
            connection: { location: "remote", transport: "private-https" },
          },
        });
      });
      await page.route("**/api/v1/auth/state", async (route) => {
        await route.fulfill({
          json: {
            claimed: true,
            authenticated:
              signedIn &&
              route.request().headers().authorization ===
                `Bearer ${saved.token}`,
            csrfToken: state.csrfToken,
            remoteAccess: true,
            remoteEnrolled: true,
          },
        });
      });
      await page.route("**/api/v1/auth/remote", async (route) => {
        expect(route.request().method()).toBe("POST");
        expect(route.request().postDataJSON()).not.toHaveProperty("password");
        connections++;
        signedIn = true;
        await route.fulfill({
          json: {
            authenticated: true,
            csrfToken: state.csrfToken,
            sessionToken: saved.token,
            sessionMode: "bearer",
          },
        });
      });
      await page.route("**/api/v1/auth/logout", async (route) => {
        signedIn = false;
        await route.fulfill({ json: { authenticated: false } });
      });
      if (restored)
        await page.addInitScript(
          (token) => sessionStorage.setItem("fased.core.session.v1", token),
          saved.token,
        );
      await page.goto(url);
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      await expect(page.locator('input[type="password"]')).toHaveCount(0);
      expect(connections).toBe(restored ? 0 : 1);
      await page.getByRole("button", { name: "Sign out", exact: true }).click();
      await expect(
        page.getByRole("heading", { name: "Open Fased", exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Connect", exact: true }),
      ).toBeVisible();
      expect(connections).toBe(restored ? 0 : 1);
      await page.getByRole("button", { name: "Connect", exact: true }).click();
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      expect(connections).toBe(restored ? 1 : 2);
      await expect(page.locator('input[type="password"]')).toHaveCount(0);
    },
  );
}
