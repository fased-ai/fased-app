// Actual packaged UI through the disposable guest's SSH-forwarded listener.
import assert from "node:assert/strict";
import { readFile, writeFile, chmod } from "node:fs/promises";
import { existsSync } from "node:fs";
import { createHash } from "node:crypto";
import { chromium, expect } from "@playwright/test";

assert.equal(process.env.FASED_DISPOSABLE_VM_ACCEPTANCE, "1");
assert.ok(process.getuid() > 0);
const config = JSON.parse(await readFile(process.argv[2], "utf8"));
assert.equal(config.fixtureOnly, true);
assert.ok(["claim", "check", "preserve"].includes(config.action));
const address = new URL(config.url);
assert.equal(address.hostname, "127.0.0.1");
assert.ok(["17481", "17482"].includes(address.port));
const password = "disposable-staged-owner-password";
const browser = await chromium.launch({
  headless: true,
  ...(process.env.FASED_TEST_BROWSER
    ? { executablePath: process.env.FASED_TEST_BROWSER }
    : {}),
});
const context = await browser.newContext({
  locale: "en-US",
  ...(existsSync(config.storageFile) ? { storageState: config.storageFile } : {}),
});
const external = [];
await context.route("**/*", async (route) => {
  if (new URL(route.request().url()).origin !== address.origin) {
    external.push(new URL(route.request().url()).origin);
    await route.abort();
    return;
  }
  await route.continue();
});
const page = await context.newPage();
const request = (path) => page.evaluate(async (path) => {
  const response = await fetch("/api/v1/" + path);
  if (!response.ok) throw new Error("authenticated fixture read failed");
  return response.json();
}, path);
function retainedWorkspace(workspace) {
  assert.ok(Array.isArray(workspace.profiles) && workspace.profiles.length > 0);
  const retained = {};
  for (const key of ["profiles", "conversations", "memories", "tasks", "runs"])
    retained[key] = workspace[key] ?? [];
  return createHash("sha256").update(JSON.stringify(retained)).digest("hex");
}
try {
  await page.goto(config.url);
  if (config.action === "claim") {
    await page.getByLabel("Create owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Claim workspace", exact: true }).click();
  }
  await expect.poll(async () => (await request("auth/state")).authenticated).toBe(true);
  const health = await request("health");
  assert.equal(health.status, "ok");
  assert.equal(health.version, config.version);
  for (const key of ["aiConnections", "financialExecution", "walletSigner"])
    assert.equal(health.capabilities[key], false);
  if (config.instanceId) assert.equal(health.instanceId, config.instanceId);
  const workspaceSha256 = retainedWorkspace(await request("agents"));
  if (config.action === "claim") {
    await writeFile(config.baselineFile, JSON.stringify({ instanceId: health.instanceId, workspaceSha256 }) + "\n", { mode: 0o600, flag: "wx" });
  } else {
    const baseline = JSON.parse(await readFile(config.baselineFile, "utf8"));
    assert.equal(health.instanceId, baseline.instanceId);
    assert.equal(workspaceSha256, baseline.workspaceSha256);
  }
  if (config.action === "check") {
    await page.goto(address.origin + "/#settings");
    const updates = page.getByRole("region", { name: "Updates", exact: true });
    await updates.getByRole("button", { name: "Check", exact: true }).click();
    await expect.poll(async () => (await request("updates/application")).operation.phase, { timeout: 120000 }).toBe("current");
    await expect(updates.getByRole("status")).toHaveText("Up to date");
    const state = await request("updates/application");
    assert.equal(state.policy.mode, "manual");
    assert.equal(state.activationEnabled, false);
    assert.equal(state.installedVersion, config.version);
  }
  assert.deepEqual(external, []);
  await context.storageState({ path: config.storageFile });
  await chmod(config.storageFile, 0o600);
  console.log(JSON.stringify({ result: "PASS", instanceId: health.instanceId, workspaceSha256, ownerAuth: true, preservation: config.action !== "claim", ...(config.action === "check" ? { uiOutcome: "alreadyCurrent", ordinaryUpdateApply: "DEFERRED_PROFILE_TRANSITION" } : {}) }));
} finally {
  await context.close();
  await browser.close();
}
