import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, readFile, writeFile, rm, cp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { AgentView } from "../src/agents";
import type { SwapProposal } from "../src/onchain";

test.use({ colorScheme: "dark" });

test("Onchain quote review protected approval submit and original result share UI and CLI", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  const credentialMode = process.env.FASED_CREDENTIAL_ONCHAIN_BROWSER === "1";
  const root = resolve(
    credentialMode
      ? ".evidence/b06-venue-credentials/browser"
      : ".evidence/b06-onchain/browser",
  );
  await mkdir(root, { recursive: true });
  const evidence = await mkdtemp(join(root, info.project.name + "-"));
  const dir = await mkdtemp(join(tmpdir(), "fased-swap-"));
  const driver = resolve(
    process.env.FASED_WALLET_BROWSER_BINARY ?? ".evidence/bin/wallet-core.test",
  );
  const binary = resolve(
    process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
  );
  const password = "disposable-swap-browser-owner-password";
  let child: ChildProcess | undefined;
  let log = "";
  let passed = false;
  let url = "";
  const outside: string[] = [];
  const errors: string[] = [];
  const events = () =>
    log.split("\n").flatMap((line) => {
      try {
        return [JSON.parse(line)];
      } catch {
        return [];
      }
    });
  const execute = async (args: string[], input?: unknown) => {
    const p = spawn(binary, [...args, "--data-dir", dir]);
    let out = "",
      err = "";
    p.stdout.on("data", (b) => (out += b));
    p.stderr.on("data", (b) => (err += b));
    p.stdin.end(
      typeof input === "string"
        ? input
        : input === undefined
          ? undefined
          : JSON.stringify(input),
    );
    const [code] = await once(p, "exit");
    expect(code, err).toBe(0);
    return out.trim().startsWith("{") ? JSON.parse(out) : out;
  };
  const read = () =>
    execute(["agents", "--profile", "trading-analyst"]) as Promise<AgentView>;
  async function command(action: string, swap: unknown) {
    return execute(["agents", "--request-stdin"], {
      action,
      profileId: "trading-analyst",
      revision: (await read()).revision,
      swap,
    }) as Promise<AgentView>;
  }
  try {
    child = spawn(driver, ["-test.run=^TestOnchainBrowserServer$"], {
      env: { ...process.env, FASED_ONCHAIN_BROWSER_DIR: dir },
    });
    child.stdout!.on("data", (b) => (log += b));
    child.stderr!.on("data", (b) => (log += b));
    await expect
      .poll(() => events().find((x) => x.event === "listening"), {
        timeout: 15000,
      })
      .toBeTruthy();
    const started = events().find((x) => x.event === "listening");
    url = started.url;
    await page.addInitScript(
      (id: string) =>
        localStorage.setItem(`fased.agent-profile.v1:${id}`, "trading-analyst"),
      started.instanceId,
    );
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/*", (route) => {
      const target = route.request().url();
      if (target.startsWith(url) || target.startsWith("blob:"))
        return route.continue();
      outside.push(target);
      return route.abort();
    });
    await page.goto(url + "/#terminals/jupiter");
    await page
      .getByLabel("One-use claim code", { exact: true })
      .fill(started.claim);
    await page
      .getByLabel("Create owner password", { exact: true })
      .fill(password);
    await page
      .getByRole("button", { name: "Claim workspace", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    await execute(["login", "--password-stdin"], password + "\n");
    if (credentialMode) {
      await page.goto(url + "/#wallets/accounts");
      const row = page.getByRole("article", {
        name: "Account Wallet",
        exact: true,
      });
      await row.getByRole("button", { name: "RPC", exact: true }).click();
      const rpc = page.getByRole("dialog", {
        name: "RPC · Wallet",
        exact: true,
      });
      const rpcKeys = rpc
        .locator("details")
        .filter({ has: page.locator("summary", { hasText: /^API key$/ }) })
        .first();
      await rpcKeys.locator("summary").first().click();
      await rpcKeys
        .getByLabel("RPC API key", { exact: true })
        .fill("r".repeat(32));
      await rpcKeys
        .getByLabel("Vault password", { exact: true })
        .fill("disposable venue vault password 123");
      await rpcKeys
        .getByRole("button", { name: "Save key", exact: true })
        .click();
      await expect(
        rpcKeys.getByRole("button", { name: "Remove key", exact: true }),
      ).toBeVisible();
      const jupiter = rpc
        .locator("details")
        .filter({ has: page.locator("summary", { hasText: /^Jupiter$/ }) })
        .first();
      await jupiter.locator("summary").first().click();
      await jupiter
        .getByLabel("Jupiter API key", { exact: true })
        .fill("fased-test-onchain");
      await jupiter
        .getByLabel("Connection password", { exact: true })
        .fill("disposable venue vault password 123");
      await expect(
        jupiter.getByRole("button", { name: "Save key", exact: true }),
      ).toBeEnabled();
      await jupiter
        .getByRole("button", { name: "Save key", exact: true })
        .click();
      await expect(jupiter.getByText("Locked", { exact: true })).toBeVisible();
      await expect(
        jupiter.getByLabel("Jupiter API key", { exact: true }),
      ).toHaveValue("");
      // Explicit retirement preserves RPC readiness and allows reconnecting.
      await jupiter
        .getByLabel("Connection password", { exact: true })
        .fill("disposable venue vault password 123");
      await jupiter
        .getByRole("button", { name: "Remove key", exact: true })
        .click();
      await expect(
        jupiter.getByRole("button", { name: "Unlock keys", exact: true }),
      ).toHaveCount(0);
      await jupiter
        .getByLabel("Jupiter API key", { exact: true })
        .fill("fased-test-onchain");
      await jupiter
        .getByLabel("Connection password", { exact: true })
        .fill("disposable venue vault password 123");
      await expect(
        jupiter.getByRole("button", { name: "Save key", exact: true }),
      ).toBeEnabled();
      await jupiter
        .getByRole("button", { name: "Save key", exact: true })
        .click();
      await expect(jupiter.getByText("Locked", { exact: true })).toBeVisible();
      await jupiter
        .getByLabel("Connection password", { exact: true })
        .fill("disposable venue vault password 123");
      await jupiter
        .getByRole("button", { name: "Unlock keys", exact: true })
        .click();
      await expect(
        jupiter.getByText("Unlocked", { exact: true }),
      ).toBeVisible();
      const record = await execute(["accounts"]);
      const wallet = record.record.accounts.find(
        (a: any) => a.name === "Wallet",
      );
      const secret = await execute(["accounts", "--venue-credential-stdin"], {
        accountId: wallet.id,
        revision: record.record.revision,
        ref: "",
        action: "status",
      });
      expect(secret.saved).toBe(true);
      expect(secret.locked).toBe(false);
      expect(secret.liveExecution).toBe(false);
      for (const file of [
        `rpc-credential-${wallet.id}.json`,
        `venue-credential-${wallet.id}.json`,
      ]) {
        const cipher = await readFile(join(dir, file), "utf8");
        expect(cipher).not.toContain("r".repeat(32));
        expect(cipher).not.toContain("fased-test-onchain");
      }
      await page.screenshot({
        path: join(evidence, "encrypted-credentials.png"),
      });
      await page.goto(url + "/#terminals/jupiter");
    }
    // Existing records/drafts must survive the new swap journey.
    await execute(["agents", "--request-stdin"], {
      action: "session-create",
      profileId: "trading-analyst",
      revision: (await read()).revision,
      name: "Retained swap conversation",
    });
    const retained = (await read()).sessions;
    const editor = page.getByRole("complementary", {
      name: "Swap",
      exact: true,
    });
    await expect(editor).toBeVisible();
    await editor
      .getByLabel("Wallet", { exact: true })
      .selectOption("c".repeat(32));
    await editor.getByLabel("Amount", { exact: true }).fill("12.5");
    await page.reload();
    await expect(editor.getByLabel("Amount", { exact: true })).toHaveValue(
      "12.5",
    );
    const before = await read();
    await editor.getByRole("button", { name: "Quote", exact: true }).click();
    const review = page.getByLabel("Swap review", { exact: true });
    await expect(review).toBeVisible();
    await expect(review).toContainText("0.1 wSOL");
    await expect(review).toContainText("0.0995 wSOL");
    await expect(
      review.getByRole("button", { name: "Submit", exact: true }),
    ).toBeDisabled();
    expect((await read()).tasks).toEqual(before.tasks);
    await review.getByText("Owner approval", { exact: true }).click();
    const downloadEvent = page.waitForEvent("download");
    await review
      .getByRole("button", { name: "Download approval", exact: true })
      .click();
    const download = await downloadEvent;
    const publicFile = join(evidence, "approval.json");
    await download.saveAs(publicFile);
    const proposal: SwapProposal = JSON.parse(
      await readFile(publicFile, "utf8"),
    );
    child.stdin!.write(JSON.stringify(proposal) + "\n");
    await expect
      .poll(() =>
        events().find(
          (x) => x.event === "approval" && x.digest === proposal.digest,
        ),
      )
      .toBeTruthy();
    await review
      .getByRole("button", { name: "Check approval", exact: true })
      .click();
    await expect(
      review.getByRole("button", { name: "Submit", exact: true }),
    ).toBeEnabled();
    await review.getByRole("button", { name: "Submit", exact: true }).click();
    await expect
      .poll(async () => (await read()).onchain?.runs.at(-1)?.swap?.state, {
        timeout: 12000,
      })
      .toBe("confirmed");
    const result = page.getByRole("article", {
      name: "Swap result",
      exact: true,
    });
    await expect(result).toContainText("Confirmed");
    await expect(result).toContainText("0.1 wSOL");
    const done = await read();
    expect(done.onchain?.runs).toHaveLength(1);
    expect(done.sessions).toEqual(retained);
    expect(done.onchain?.liveExecution).toBe(false);
    const signature = done.onchain!.runs[0].swap!.signature;
    const reconciled = await command("swap-reconcile", {
      id: done.onchain!.tasks[0].id,
    });
    expect(reconciled.onchain?.runs[0].swap?.signature).toBe(signature);
    // Generic Tasks shows the exact swap receipt, without another scheduler.
    await page.goto(url + "/#agents/tasks");
    await expect(
      page.getByRole("heading", { name: "Tasks", exact: true, level: 1 }),
    ).toBeVisible();
    expect((await read()).onchain?.runs).toHaveLength(1);
    await page.goto(url + "/#terminals/jupiter");
    await expect(
      page.getByRole("article", { name: "Swap result", exact: true }),
    ).toContainText("Confirmed");
    await page.screenshot({
      path: join(evidence, "confirmed.png"),
      fullPage: true,
    });
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth + 1,
    );
    expect(overflow).toBe(false);
    expect(outside).toEqual([]);
    expect(errors).toEqual([]);
    await writeFile(
      join(evidence, "result.json"),
      JSON.stringify(
        {
          result: "PASS",
          fixtureOnly: true,
          externalRequests: outside,
          errors,
          view: done.onchain,
          retainedSessions: retained.map((x) => x.id),
        },
        null,
        2,
      ),
    );
    passed = true;
  } finally {
    if (child && child.exitCode === null) {
      const stopped = once(child, "exit");
      child.kill("SIGTERM");
      await stopped;
    }
    await writeFile(join(evidence, "driver.log"), log);
    try {
      await cp(join(dir, "transport.json"), join(evidence, "transport.json"));
      const transport = JSON.parse(
        await readFile(join(dir, "transport.json"), "utf8"),
      );
      expect(transport.submissions).toBe(1);
      expect(transport.externalRequests).toBe(0);
    } catch (e) {
      if (passed) throw e;
    }
    if (passed) await rm(dir, { recursive: true, force: true });
    else await writeFile(join(evidence, "retained-fixture.txt"), dir + "\n");
  }
});
