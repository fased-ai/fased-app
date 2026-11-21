import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { binary, captureScreenshot, exec } from "./helpers/core-fixture.ts";

test("Remote SSH pairing flow, device management, revocation, and logout stay clean", async ({
  page,
  browser,
  isMobile,
}) => {
  const remoteDir = await mkdtemp(join(tmpdir(), "fased-pair-e2e-"));
  let daemonOut = "";
  const daemon = spawn(binary, [
    "serve",
    "--instance-location",
    "remote",
    "--remote-transport",
    "ssh",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    remoteDir,
  ]);
  daemon.stdout.on("data", (d) => (daemonOut += String(d)));
  daemon.stderr.on("data", (d) => (daemonOut += String(d)));

  try {
    await expect
      .poll(() => daemonOut.includes('"event":"listening"'))
      .toBe(true);
    const remoteURL = JSON.parse(
      daemonOut
        .trim()
        .split("\n")
        .find((line) => line.includes('"event":"listening"'))!,
    ).url as string;

    // 1. Unclaimed Remote SSH gate shows Pair this device cleanly without password fields.
    await page.goto(remoteURL);
    await expect(
      page.getByRole("heading", { name: "Pair this device", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByLabel("Pairing code", { exact: true }),
    ).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await expect(page.getByText("Remote", { exact: true })).toBeVisible();

    // 2. CLI `pair --json` produces one-time #pair= URL that auto-enters #agents/chat.
    const { stdout: pairRaw } = await exec(binary, [
      "pair",
      "--data-dir",
      remoteDir,
      "--origin",
      remoteURL,
      "--label",
      "PlaywrightPrimary",
      "--json",
    ]);
    const issued = JSON.parse(pairRaw) as {
      id: string;
      code: string;
      url: string;
      sshCommand?: string;
    };
    expect(issued.code).toHaveLength(43);
    expect(issued.url).toContain("#pair=");
    expect(issued.sshCommand).toContain("ssh -N -L");

    await page.goto(issued.url);
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Chat", exact: true }),
    ).toBeVisible();
    expect(page.url()).not.toContain("#pair=");

    // 3. Settings shows Devices panel and issues a link for a second device.
    await page.goto(remoteURL + "/#settings");
    await expect(
      page.getByRole("heading", { name: "Devices", exact: true }),
    ).toBeVisible();
    await expect(page.getByText(/PlaywrightPrimary/)).toBeVisible();
    await expect(page.getByText("This device", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Add device", exact: true }).click();
    const inviteInput = page.getByLabel("Open on your other device", {
      exact: true,
    });
    await expect(inviteInput).toBeVisible();
    const secondURL = await inviteInput.inputValue();
    expect(secondURL).toContain("#pair=");

    // 4. Second browser context pairs cleanly and can then be revoked from device 1.
    const context2 = await browser.newContext();
    try {
      const page2 = await context2.newPage();
      await page2.goto(secondURL);
      await expect(
        page2.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();

      await page.reload();
      await page.goto(remoteURL + "/#settings");
      const removeButton = page.getByRole("button", {
        name: "Remove",
        exact: true,
      });
      await expect(removeButton).toHaveCount(1);
      await captureScreenshot(page, {
        path: `.evidence/remote-pair-devices-${isMobile ? "mobile" : "desktop"}.png`,
        fullPage: true,
      });
      await removeButton.click();
      await expect(removeButton).toHaveCount(0);

      // Revoked second context immediately loses access on reload.
      await page2.reload();
      await expect(
        page2.getByRole("heading", { name: "Pair this device", exact: true }),
      ).toBeVisible();
    } finally {
      await context2.close();
    }

    // 5. Explicit Sign out persists across reload and rejects already-used code.
    await page.getByRole("button", { name: "Sign out", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Pair this device", exact: true }),
    ).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "Pair this device", exact: true }),
    ).toBeVisible();

    await page.getByLabel("Pairing code", { exact: true }).fill(issued.code);
    await page.getByRole("button", { name: "Pair", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText(
      /invalid|expired|used/i,
    );

    // 6. Fresh manual code entry pairs into the workspace.
    const { stdout: pair2Raw } = await exec(binary, [
      "pair",
      "--data-dir",
      remoteDir,
      "--origin",
      remoteURL,
      "--json",
    ]);
    const issued2 = JSON.parse(pair2Raw) as { code: string };
    await page.getByLabel("Pairing code", { exact: true }).fill(issued2.code);
    await page.getByRole("button", { name: "Pair", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
  } finally {
    if (daemon.exitCode === null) {
      const exited = once(daemon, "exit");
      daemon.kill("SIGTERM");
      await exited;
    }
    await rm(remoteDir, { recursive: true, force: true });
  }
});
