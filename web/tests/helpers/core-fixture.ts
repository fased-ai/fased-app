import { expect, test, type Page } from "@playwright/test";
import { spawn, type ChildProcess, execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { once } from "node:events";

export async function openProviders(page: import("@playwright/test").Page) {
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  if (!(await providers.isVisible())) {
    let opened = false;
    for (const button of await page
      .getByRole("button", { name: "Providers", exact: true })
      .all()) {
      if (await button.isVisible()) {
        await button.click();
        opened = true;
        break;
      }
    }
    if (!opened) {
      await page.getByRole("button", { name: "More", exact: true }).click();
      await page
        .getByRole("navigation", { name: "More pages", exact: true })
        .getByRole("button", { name: "Providers", exact: true })
        .click();
    }
  }
  await expect(providers).toBeVisible();
}

export async function openNewsSources(
  page: import("@playwright/test").Page,
  add = false,
) {
  const dialog = page.getByRole("dialog", { name: "Sources", exact: true });
  if (!(await dialog.isVisible()))
    await page.getByRole("button", { name: "Sources", exact: true }).click();
  if (
    add &&
    !(await dialog.getByLabel("Feed URL", { exact: true }).isVisible())
  )
    await dialog
      .getByRole("button", { name: "Add source", exact: true })
      .click();
}
export async function closeNewsSources(page: import("@playwright/test").Page) {
  const dialog = page.getByRole("dialog", { name: "Sources", exact: true });
  if (await dialog.isVisible())
    await dialog
      .getByRole("button", { name: "Close dialog", exact: true })
      .click();
}
export async function newsFetchControl(
  page: import("@playwright/test").Page,
  enabled: boolean,
) {
  await openNewsSources(page);
  const control = page
    .getByRole("dialog", { name: "Sources", exact: true })
    .getByRole("button", { name: "Fetch", exact: true })
    .first();
  if (enabled) await expect(control).toBeEnabled();
  else await expect(control).toBeDisabled();
  await closeNewsSources(page);
}

export async function exactReplyReview(page: import("@playwright/test").Page) {
  const control = page.getByRole("checkbox", {
    name: "Review each reply",
    exact: true,
    includeHidden: true,
  });
  await revealControl(page, control, "Options");
  await control.check();
}

export async function openConversationList(
  page: import("@playwright/test").Page,
) {
  const toggle = page.getByRole("button", {
    name: "Conversations",
    exact: true,
  });
  if (
    (await toggle.isVisible()) &&
    (await toggle.getAttribute("aria-expanded")) !== "true"
  )
    await toggle.click();
}

export async function choosePasswordSignIn(
  page: import("@playwright/test").Page,
  timeout = 5000,
) {
  const option = page.getByRole("button", {
    name: "Use password",
    exact: true,
  });
  const password = page.getByLabel("Owner password", { exact: true });
  await expect
    .poll(
      async () => (await option.isVisible()) || (await password.isVisible()),
      { timeout },
    )
    .toBe(true);
  if (await option.isVisible()) await option.click();
}

export async function revealControl(
  page: import("@playwright/test").Page,
  control: import("@playwright/test").Locator,
  hint = "",
) {
  if (control.toString().includes("Owner password"))
    await choosePasswordSignIn(page);
  if (await control.isVisible()) return;
  if ((await control.count()) === 0) {
    if (hint.includes("Conversation name")) {
      const toggle = page.getByRole("button", {
        name: "Conversations",
        exact: true,
      });
      const create = page.getByRole("button", {
        name: "New conversation",
        exact: true,
      });
      // Reload can finish before Chat's navigation is attached. A one-shot
      // isVisible() must not skip opening the mobile conversation disclosure.
      await expect
        .poll(
          async () =>
            (await control.isVisible()) ||
            (await create.isVisible()) ||
            (await toggle.isVisible()),
        )
        .toBe(true);
      if (!(await control.isVisible())) {
        await openConversationList(page);
        await create.click();
      }
    } else if (hint.includes("Fact") || hint.includes("Note"))
      await page.getByRole("button", { name: "Remember", exact: true }).click();
    else if (hint.includes("Task name"))
      await page.getByRole("button", { name: "New task", exact: true }).click();
    else if (control.toString().includes("API key")) {
      const apiKeyMethod = page.getByRole("button", {
        name: "API key",
        exact: true,
      });
      if (await apiKeyMethod.isVisible()) await apiKeyMethod.click();
    }
  }
  // Let navigation attach its control before choosing the owning disclosure.
  await expect(control).toBeAttached();
  if (
    !(await control.isVisible()) &&
    (await control
      .locator("xpath=ancestor::aside[@aria-label='Conversations']")
      .count())
  )
    await openConversationList(page);
  // Keep click targets stable as disclosures open; outermost first.
  const disclosures = control.locator("xpath=ancestor::details");
  for (let i = 0; i < (await disclosures.count()); i++) {
    if (await control.isVisible()) break;
    const section = disclosures.nth(i);
    if ((await section.getAttribute("open")) === null)
      await section.locator(":scope > summary").click();
  }
  await expect(control).toBeVisible();
}

export async function openEvidence(page: import("@playwright/test").Page) {
  const options = page.locator(".message-options");
  if ((await options.getAttribute("open")) === null)
    await options.locator(":scope > summary").click();
  const evidence = page.locator(".chat-evidence");
  if ((await evidence.getAttribute("open")) === null)
    await evidence.locator(":scope > summary").click();
}

export async function openUpdateSettings(
  page: import("@playwright/test").Page,
) {
  const section = page.locator("details.update-settings");
  await expect(section).toBeVisible();
  if ((await section.getAttribute("open")) === null)
    await section.locator(":scope > summary").click();
}

export async function openAISchedules(page: import("@playwright/test").Page) {
  const pane = page.locator(".scheduled-ai-disclosure");
  await expect(pane).toBeAttached();
  if ((await pane.getAttribute("open")) === null)
    await pane.locator(":scope > summary").click();
}

export const exec = promisify(execFile);
export const binary = resolve(
  process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
);
export const fixturePassword = "generated-browser-fixture-password";
export let dir: string;
let child: ChildProcess | undefined;
export let url: string;
export let output = "";
// Only this test's spawned cores are trusted. Keep their earlier ports during
// restart; unrelated loopback and external origins remain disallowed.
export const ownedFixtureOrigins = new Set<string>();

export async function start(
  aiFixture = false,
  listen = "127.0.0.1:0",
  location = "local",
  requirePassword = true,
) {
  output = "";
  child = spawn(binary, [
    "serve",
    ...(aiFixture ? ["--ai-fixture"] : []),
    ...(location === "remote" ? ["--instance-location", "remote"] : []),
    ...(requirePassword && location === "local" ? ["--require-password"] : []),
    "--listen",
    listen,
    "--data-dir",
    dir,
  ]);
  child.stdout!.on("data", (data) => (output += String(data)));
  child.stderr!.on("data", (data) => (output += String(data)));
  try {
    await expect.poll(() => output.includes('"event":"listening"')).toBe(true);
  } catch (error) {
    await writeFile(
      resolve(
        ".evidence/core-browser-fixtures",
        `${basename(dir)}-startup.log`,
      ),
      output,
    );
    throw Error(`Fixture core startup failed:\n${output}\n${String(error)}`);
  }
  url = JSON.parse(output.trim().split("\n")[0]).url;
  ownedFixtureOrigins.add(new URL(url).origin);
}
export async function stop() {
  if (!child || child.exitCode !== null) return;
  const done = once(child, "exit");
  child.kill("SIGTERM");
  await done;
  child = undefined;
}
export async function privateCLI(args: string[], input?: unknown) {
  if (input === undefined)
    return JSON.parse(
      (await exec(binary, [...args, "--data-dir", dir])).stdout,
    );
  const child = spawn(binary, [...args, "--data-dir", dir]);
  let output = "",
    error = "";
  child.stdout!.on("data", (b) => (output += b));
  child.stderr!.on("data", (b) => (error += b));
  child.stdin!.end(JSON.stringify(input));
  const [code] = await once(child, "exit");
  if (code !== 0) throw new Error(error);
  return JSON.parse(output);
}
// Explicit fixture setup; never normalize production/negative request payloads.
export async function fixtureProviderSelection(
  provider: "openai" | "anthropic" | "xai",
) {
  const account = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider,
    name: "Preference fixture",
    key: `fased-test-${provider}-browser-preference-key`,
    passphrase: "disposable-browser-preference-vault",
  });
  const model = account.catalog.models.find(
    (m: { recommended: boolean; provider: string }) =>
      m.recommended && m.provider === provider,
  );
  expect(model).toBeTruthy();
  expect(account.testOnly).toBe(true);
  return { provider, model: model.id, accountRef: account.ref };
}

export async function captureScreenshot(
  page: Page,
  options?: Parameters<Page["screenshot"]>[0],
) {
  if (process.env.FASED_CAPTURE_SCREENSHOTS === "1") {
    await page.screenshot(options);
  }
}

export function installCoreFixture() {
  test.use({ colorScheme: "dark" });
  test.beforeEach(async () => {
    ownedFixtureOrigins.clear();
    const root = resolve(".evidence/core-browser-fixtures");
    await mkdir(root, { recursive: true, mode: 0o700 });
    // Disposable, reconstructible state; keep IPC independent of checkout length.
    dir = await mkdtemp(join(tmpdir(), "fcu-"));
    await start();
  });
  test.afterEach(async ({ page }) => {
    try {
      // Drain intercepted reads before stopping the fixture they forward to.
      await page.unrouteAll({ behavior: "wait" });
    } finally {
      try {
        await stop();
      } finally {
        if (dir) await rm(dir, { recursive: true, force: true });
      }
    }
  });
}
