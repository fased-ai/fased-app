import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");

test("retained strategy/history review shares exact UI/CLI context, revisions and restart", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  const root = resolve(".evidence/f3-saved-chat");
  await mkdir(join(root, "fixtures"), { recursive: true });
  const dir = await mkdtemp(join(root, "fixtures/core-"));
  const password = "generated-saved-context-password";
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
  const command = async (name: string, body: object, review = false) => {
    const p = spawn(binary, [
      name,
      "--data-dir",
      dir,
      review ? "--review-stdin" : "--request-stdin",
    ]);
    let out = "",
      err = "";
    p.stdout!.on("data", (b) => (out += b));
    p.stderr!.on("data", (b) => (err += b));
    p.stdin!.end(JSON.stringify(body));
    expect((await once(p, "exit"))[0], err).toBe(0);
    return JSON.parse(out);
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
    const raw = await readFile(
      "internal/core/testdata/wen-research-strategy.json",
      "utf8",
    );
    await ui
      .getByRole("textbox", { name: "Strategy JSON", exact: true })
      .fill(raw);
    await ui
      .getByRole("button", { name: "Save original version", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await cli(["wen-research", "--profile", "wen-operator"])).revision,
      )
      .toBe(1);
    let library = await cli(["wen-research", "--profile", "wen-operator"]);
    expect(library.versions[0].raw).toBe(raw);
    const digest = library.versions[0].digest;
    await ui.getByRole("button", { name: "Mine", exact: true }).click();
    await ui
      .getByRole("button", {
        name: "Discuss Fixture session draft",
        exact: true,
      })
      .click();
    const panel = page.getByRole("region", { name: "Research Chat review" });
    await expect(panel).toBeVisible();
    await expect(panel).toBeFocused();
    await expect(panel).toContainText("Strategy only · no results included");
    let before = await cli(["agents", "--profile", "wen-operator"]);
    await panel
      .getByRole("button", { name: "Cancel research review", exact: true })
      .click();
    expect((await cli(["agents", "--profile", "wen-operator"])).revision).toBe(
      before.revision,
    );

    const strategyReview = await command(
      "wen-research",
      { profile: "wen-operator", revision: 1, strategyDigest: digest },
      true,
    );
    await command("agents", {
      action: "wen-research-chat",
      profileId: strategyReview.profileId,
      revision: strategyReview.revision,
      text: strategyReview.text,
      source: strategyReview.source,
      expiresAt: strategyReview.expiresAt,
    });
    const strategyText = (await cli(["agents", "--profile", "wen-operator"]))
      .messages[0].text;
    expect(strategyText).toBe(strategyReview.text);
    let historyRaw = await readFile(
      "internal/core/testdata/wen-saved-history.json",
      "utf8",
    );
    historyRaw = historyRaw.replaceAll("a".repeat(64), digest);
    library = await command("wen-research", {
      profile: "wen-operator",
      revision: 1,
      action: "history",
      raw: historyRaw,
    });
    await ui
      .getByRole("button", { name: "Reload library", exact: true })
      .click();
    await ui.getByRole("button", { name: "History", exact: true }).click();
    await ui
      .getByRole("button", { name: "Discuss history", exact: true })
      .click();
    await expect(panel).toContainText("-200001015");
    await expect(panel).toContainText("All recorded strategies");
    await panel
      .getByRole("button", { name: "Cancel research review", exact: true })
      .click();
    await ui
      .getByLabel("History context scope", { exact: true })
      .selectOption(digest);
    await ui
      .getByRole("button", { name: "Discuss history", exact: true })
      .click();
    await expect(panel).toContainText("-100001015");
    await expect(panel).toContainText("participation unverified");
    await expect(panel).toContainText("historical-replay · unverified");
    const frozen = (await panel
      .getByLabel("Research Chat snapshot")
      .textContent())!;
    const projection = JSON.parse(frozen.split("\n")[1]);
    expect(projection.history.gaps).toEqual(JSON.parse(historyRaw).gaps);
    expect(projection.history.summary.satEarned).toBe("11");
    expect(projection.history.summary.btcPending).toBe("7");
    expect(projection.history.summary.overlays.staking).toBe("23");
    const stale = await command("wen-research", {
      profile: "wen-operator",
      revision: library.revision,
      action: "save",
      raw: raw.replace("Fixture session draft", "second retained version"),
    });
    await panel
      .getByRole("button", { name: "Save to Chat", exact: true })
      .click();
    await expect(panel.getByRole("alert")).toBeVisible();
    expect(
      (await cli(["agents", "--profile", "wen-operator"])).messages,
    ).toHaveLength(1);
    await panel
      .getByRole("button", { name: "Cancel research review", exact: true })
      .click();
    await ui.getByRole("button", { name: "Library", exact: true }).click();
    await ui
      .getByRole("button", { name: "Reload library", exact: true })
      .click();
    await ui.getByRole("button", { name: "History", exact: true }).click();
    await ui
      .getByRole("button", { name: "Discuss history", exact: true })
      .click();
    await expect(panel).toBeVisible();
    await expect(panel.getByRole("alert")).toHaveCount(0);
    if (info.project.name === "mobile") {
      for (const button of await panel.getByRole("button").all())
        expect((await button.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    }
    await panel.screenshot({
      path: join(root, `review-${info.project.name}.png`),
    });
    const finalText = (await panel
      .getByLabel("Research Chat snapshot")
      .textContent())!;
    before = await cli(["agents", "--profile", "wen-operator"]);
    await panel
      .getByRole("button", { name: "Save to Chat", exact: true })
      .click();
    await expect(
      page.getByRole("heading", {
        name: "Strategy: Fixture session draft",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.getByText(finalText, { exact: true })).toBeVisible();
    const after = await cli(["agents", "--profile", "wen-operator"]);
    expect(after.messages).toHaveLength(2);
    expect(after.messages[1].text).toBe(finalText);
    expect(after.tasks).toEqual(before.tasks);
    expect(after.runs).toEqual(before.runs);
    expect(after.aiRequests).toEqual([]);
    expect(after.financialExecution).toBe(false);
    expect(after.providerCallsEnabled).toBe(false);
    expect(
      (await cli(["wen-research", "--profile", "wen-operator"])).revision,
    ).toBe(stale.revision);
    expect(
      (await cli(["agents", "--profile", "news-researcher"])).messages,
    ).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await stop();
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByText(finalText, { exact: true })).toBeVisible();
    expect(
      (await cli(["agents", "--profile", "wen-operator"])).messages[1].text,
    ).toBe(finalText);
    expect(remote).toEqual([]);
    await page.screenshot({
      path: join(root, `chat-${info.project.name}.png`),
      fullPage: true,
    });
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
