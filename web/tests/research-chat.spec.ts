import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");
const evidence =
  process.env.FASED_RESEARCH_CHAT_EVIDENCE ?? ".evidence/f3-research-chat";

test("source-bound research review joins private Chat, CLI and restart without execution", async ({
  page,
}, info) => {
  test.setTimeout(60000);
  await mkdir(`${evidence}/fixtures`, { recursive: true });
  const dir = await mkdtemp(resolve(`${evidence}/fixtures/core-`));
  const password = "generated-research-chat-password";
  let child: ChildProcess | undefined,
    output = "",
    url = "";
  const start = async () => {
    output = "";
    child = spawn(binary, [
      "serve",
      "--listen",
      "127.0.0.1:0",
      "--data-dir",
      dir,
      "--wen-research-origin",
      "http://127.0.0.1:7352",
    ]);
    child.stdout!.on("data", (b) => (output += b));
    child.stderr!.on("data", (b) => (output += b));
    await expect.poll(() => output.includes('"event":"listening"')).toBe(true);
    url = JSON.parse(output.trim().split("\n")[0]).url;
  };
  const stop = async () => {
    if (child && child.exitCode === null) {
      const done = once(child, "exit");
      child.kill("SIGTERM");
      await done;
    }
    child = undefined;
  };
  const cli = async (args: string[]) =>
    JSON.parse((await exec(binary, [...args, "--data-dir", dir])).stdout);
  const change = async (command: object) => {
    const c = spawn(binary, ["agents", "--data-dir", dir, "--request-stdin"]);
    let error = "";
    c.stderr!.on("data", (b) => (error += b));
    c.stdin!.end(JSON.stringify(command));
    expect((await once(c, "exit"))[0], error).toBe(0);
  };
  const remote: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      remote.push(r.url());
  });
  try {
    await start();
    await page.goto(url);
    const claim = JSON.parse(
      await readFile(join(dir, "claim-code.json"), "utf8"),
    );
    await page.getByLabel("One-use claim code").fill(claim.code);
    await page.getByLabel("Create owner password").fill(password);
    await page.getByRole("button", { name: "Claim workspace" }).click();
    await expect(
      page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
    ).toBeVisible();
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin!.end(password + "\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    await page.goto(url + "/#terminals/satcoin");
    const ui = page.getByRole("region", {
      name: "Strategy research workspace",
    });
    await ui.getByRole("button", { name: "Research", exact: true }).click();
    const feed = ui.getByRole("region", { name: "Shared Kraken research" });
    await feed
      .getByRole("button", { name: "Connect research", exact: true })
      .click();
    await expect(feed.getByTestId("shared-quote")).toHaveText("131.00");
    await expect(
      feed.getByRole("button", { name: "Discuss research", exact: true }),
    ).toBeEnabled();
    const response = await cli([
      "wen-market-data",
      "--lookback",
      "1",
      "--profile",
      "wen-operator",
    ]);
    const review = response.chatContext;
    expect(review.profileId).toBe("wen-operator");
    expect(review.text).toContain(response.data.inputDigest);
    const before = await cli(["agents", "--profile", review.profileId]);
    await feed
      .getByRole("button", { name: "Discuss research", exact: true })
      .click();
    const panel = page.getByRole("region", { name: "Research Chat review" });
    await expect(panel).toBeVisible();
    await expect(panel).toBeFocused();
    await expect(panel).toContainText("Frozen Kraken BTC/USD research");
    await expect(panel).toContainText("$131.00");
    if (info.project.name === "mobile") {
      for (const button of await panel.getByRole("button").all())
        expect((await button.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    }
    const panelBox = (await panel.boundingBox())!;
    const fieldsBox = (await panel.locator("dl").boundingBox())!;
    expect(fieldsBox.x - panelBox.x).toBeGreaterThanOrEqual(16);
    await panel.screenshot({
      path: `${evidence}/review-${info.project.name}.png`,
    });
    await page
      .getByRole("button", { name: "Cancel research review", exact: true })
      .click();
    expect(
      (await cli(["agents", "--profile", review.profileId])).revision,
    ).toBe(before.revision);
    const exportWait = page.waitForEvent("download");
    await feed
      .getByRole("button", { name: "Export shared snapshot", exact: true })
      .click();
    const exported = JSON.parse(
      await readFile((await (await exportWait).path())!, "utf8"),
    );
    expect(exported.chatContext).toBeUndefined();
    await change({
      action: "wen-research-chat",
      profileId: review.profileId,
      revision: review.revision,
      text: review.text,
      source: review.source,
      expiresAt: review.expiresAt,
    });
    const viaCLI = await cli(["agents", "--profile", review.profileId]);
    expect(viaCLI.messages).toHaveLength(1);
    expect(viaCLI.messages[0].text).toBe(review.text);
    await page.reload();
    await ui.getByRole("button", { name: "Research", exact: true }).click();
    await feed
      .getByRole("button", { name: "Connect research", exact: true })
      .click();
    await expect(
      feed.getByRole("button", { name: "Discuss research", exact: true }),
    ).toBeEnabled();
    await feed.getByRole("button", { name: "30m", exact: true }).click();
    await expect(feed.getByTestId("shared-quote")).toHaveText("Unavailable");
    await expect(
      feed.getByRole("button", { name: "Discuss research", exact: true }),
    ).toBeDisabled();
    await feed.getByRole("button", { name: "1m", exact: true }).click();
    await expect(
      feed.getByRole("button", { name: "Discuss research", exact: true }),
    ).toBeEnabled();
    await feed
      .getByRole("button", { name: "Discuss research", exact: true })
      .click();
    const frozen = await panel
      .getByLabel("Research Chat snapshot")
      .textContent();
    await panel
      .getByRole("button", { name: "Save to Chat", exact: true })
      .click();
    await expect(
      page.getByRole("heading", {
        name: "Research: Kraken BTC/USD",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.getByText(frozen!, { exact: true })).toBeVisible();
    const after = await cli(["agents", "--profile", review.profileId]);
    expect(after.messages).toHaveLength(2);
    expect(after.messages[1].text).toBe(frozen);
    expect(after.tasks).toEqual(before.tasks);
    expect(after.aiRequests).toEqual([]);
    expect(after.runs).toEqual([]);
    expect(after.financialExecution).toBe(false);
    expect(after.providerCallsEnabled).toBe(false);
    expect(
      (await cli(["agents", "--profile", "news-researcher"])).messages,
    ).toEqual([]);
    await page.screenshot({
      path: `${evidence}/chat-${info.project.name}.png`,
      fullPage: true,
    });
    await stop();
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByText(frozen!, { exact: true })).toBeVisible();
    expect(remote).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
