import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");

test("Chat source to inert revision keeps original, comparison and UI/CLI receipts across restart", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  const root = resolve(
    process.env.FASED_STRATEGY_REVISION_ROOT ?? ".evidence/f3-chat-revision",
  );
  await mkdir(join(root, "fixtures"), { recursive: true });
  const dir = await mkdtemp(join(root, "fixtures/core-")),
    password = "generated-strategy-revision-password";
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
  const command = async (
    name: string,
    flag: string,
    body: object,
    ok = true,
  ) => {
    const p = spawn(binary, [name, "--data-dir", dir, flag]);
    let out = "",
      err = "";
    p.stdout!.on("data", (b) => (out += b));
    p.stderr!.on("data", (b) => (err += b));
    p.stdin!.end(JSON.stringify(body));
    const status = (await once(p, "exit"))[0];
    if (ok) expect(status, err).toBe(0);
    else {
      expect(status).not.toBe(0);
      return err;
    }
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
    const originalRaw = await readFile(
      "internal/core/testdata/wen-research-strategy.json",
      "utf8",
    );
    let library = await command("wen-research", "--request-stdin", {
      profile: "wen-operator",
      revision: 0,
      action: "save",
      raw: originalRaw,
    });
    const original = library.versions[0];
    const context = await command("wen-research", "--review-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      strategyDigest: original.digest,
    });
    await command("agents", "--request-stdin", {
      action: "wen-research-chat",
      profileId: context.profileId,
      revision: context.revision,
      expiresAt: context.expiresAt,
      text: context.text,
      source: context.source,
    });
    let before = await cli(["agents", "--profile", "wen-operator"]);
    const source = before.messages[0];
    await page.goto(url + "/#agents/chat");
    await expect(page.getByText(source.text, { exact: true })).toBeVisible();
    const open = async () => {
      await page
        .getByRole("button", { name: "Revise strategy", exact: true })
        .click();
      const panel = page.getByRole("region", {
        name: "Chat strategy revision",
      });
      await expect(panel).toBeVisible();
      await panel
        .getByRole("combobox", { name: "Original strategy", exact: true })
        .selectOption(original.digest);
      return panel;
    };
    let panel = await open();
    await panel
      .getByRole("button", { name: "Cancel revision", exact: true })
      .click();
    expect((await cli(["agents", "--profile", "wen-operator"])).revision).toBe(
      before.revision,
    );
    expect(
      (await cli(["wen-research", "--profile", "wen-operator"])).revision,
    ).toBe(library.revision);
    panel = await open();
    await panel
      .getByRole("textbox", { name: "Candidate strategy JSON", exact: true })
      .fill("{}");
    await panel
      .getByRole("button", { name: "Review strategy revision", exact: true })
      .click();
    await expect(panel.getByRole("alert")).toBeVisible();
    const candidate = JSON.parse(originalRaw);
    candidate.name = "UI trend revision";
    candidate.research.thesis = "trend";
    candidate.allocation.preset = "custom";
    candidate.allocation.weightsBps = [500, 1000, 1500, 3000, 4000];
    candidate.schedule.everyNthRound = 6;
    const candidateRaw = JSON.stringify(candidate, null, 2) + "\n";
    await panel
      .getByRole("textbox", { name: "Candidate strategy JSON", exact: true })
      .fill(candidateRaw);
    const response = page.waitForResponse(
      (r) =>
        r.url().endsWith("/wen/research/revision-review") && r.status() === 200,
    );
    await panel
      .getByRole("button", { name: "Review strategy revision", exact: true })
      .click();
    const uiReview = await (await response).json();
    await expect(
      panel.getByRole("table", { name: "Strategy settings", exact: true }),
    ).toBeVisible();
    expect(uiReview.source.text).toBe(source.text);
    expect(uiReview.parent.raw).toBe(originalRaw);
    expect(uiReview.candidate.raw).toBe(candidateRaw);
    expect(uiReview.candidate.digest).not.toBe(original.digest);
    const cliReview = await command("wen-research", "--revision-review-stdin", {
      profile: "wen-operator",
      revision: before.revision,
      libraryRevision: library.revision,
      sourceMessageId: source.id,
      parentDigest: original.digest,
      raw: candidateRaw,
    });
    expect(cliReview.sourceSha256).toBe(uiReview.sourceSha256);
    expect(cliReview.parent).toEqual(uiReview.parent);
    expect(cliReview.candidate.digest).toBe(uiReview.candidate.digest);
    expect(cliReview.candidate.raw).toBe(candidateRaw);
    expect(
      (await cli(["wen-research", "--profile", "wen-operator"])).versions,
    ).toHaveLength(1);
    library = await command("wen-research", "--request-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      action: "save",
      raw: originalRaw.replace(
        "Fixture session draft",
        "independent saved version",
      ),
    });
    await panel
      .getByRole("button", { name: "Save revised version", exact: true })
      .click();
    await expect(panel.getByRole("alert")).toBeVisible();
    expect(
      (await cli(["agents", "--profile", "wen-operator"])).messages,
    ).toHaveLength(1);
    await panel
      .getByRole("button", { name: "Cancel revision", exact: true })
      .click();
    panel = await open();
    await panel
      .getByRole("textbox", { name: "Candidate strategy JSON", exact: true })
      .fill(candidateRaw);
    await panel
      .getByRole("button", { name: "Review strategy revision", exact: true })
      .click();
    await expect(
      panel.getByRole("button", { name: "Save revised version", exact: true }),
    ).toBeVisible();
    await expect(panel.getByRole("alert")).toHaveCount(0);
    expect(
      await panel.evaluate((el) => el.closest(".local-messages") === null),
    ).toBe(true);
    const bounds = (await panel.boundingBox())!;
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(
      page.viewportSize()!.width,
    );
    if (info.project.name === "mobile") {
      for (const b of await panel.getByRole("button").all())
        expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    }
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await panel
      .getByRole("heading", { name: "Strategy revision", exact: true })
      .scrollIntoViewIfNeeded();
    await page.screenshot({
      path: join(root, `review-top-${info.project.name}.png`),
    });
    await panel
      .getByRole("button", { name: "Save revised version", exact: true })
      .scrollIntoViewIfNeeded();
    await expect(
      panel.getByRole("button", { name: "Save revised version", exact: true }),
    ).toBeInViewport();
    await page.screenshot({
      path: join(root, `review-controls-${info.project.name}.png`),
    });
    await panel
      .getByRole("button", { name: "Save revised version", exact: true })
      .click();
    await expect(
      panel.getByText(
        "Saved in Mine with a source receipt in this conversation.",
        { exact: true },
      ),
    ).toBeVisible();
    library = await cli(["wen-research", "--profile", "wen-operator"]);
    expect(library.versions).toHaveLength(3);
    expect(library.versions[0]).toEqual(original);
    const savedUI = library.versions.find(
      (v: any) => v.name === candidate.name,
    );
    expect(savedUI.raw).toBe(candidateRaw);
    let after = await cli(["agents", "--profile", "wen-operator"]);
    expect(after.messages).toHaveLength(2);
    expect(after.messages[0]).toEqual(source);
    const uiReceipt = after.messages[1];
    expect(savedUI.origin).toBe("wen-chat-revision-v1:" + uiReceipt.id);
    await panel
      .getByRole("button", { name: "Close revision", exact: true })
      .click();
    const cliRaw = candidateRaw.replace(
      "UI trend revision",
      "CLI trend revision",
    );
    const next = await command("wen-research", "--revision-review-stdin", {
      profile: "wen-operator",
      revision: after.revision,
      libraryRevision: library.revision,
      sourceMessageId: source.id,
      parentDigest: original.digest,
      raw: cliRaw,
    });
    const result = await command("wen-research", "--revision-save-stdin", next);
    expect(result.library.versions).toHaveLength(4);
    expect(result.library.versions.at(-1).raw).toBe(cliRaw);
    await command("wen-research", "--revision-save-stdin", next, false);
    after = await cli(["agents", "--profile", "wen-operator"]);
    expect(after.messages).toHaveLength(3);
    expect(after.messages[2].text).toBe(result.receipt.text);
    expect(after.tasks).toEqual(before.tasks);
    expect(after.runs).toEqual(before.runs);
    expect(after.aiRequests).toEqual([]);
    expect(after.financialExecution).toBe(false);
    expect(after.providerCallsEnabled).toBe(false);
    expect(
      (await cli(["wen-research", "--profile", "news-researcher"])).versions,
    ).toEqual([]);
    expect(
      (await cli(["agents", "--profile", "news-researcher"])).messages,
    ).toEqual([]);
    await stop();
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByText(uiReceipt.text, { exact: true })).toBeVisible();
    await expect(
      page.getByText(result.receipt.text, { exact: true }),
    ).toBeVisible();
    await page.goto(url + "/#terminals/satcoin");
    const workspace = page.getByRole("region", {
      name: "Strategy research workspace",
    });
    await workspace.getByRole("button", { name: "Mine", exact: true }).click();
    await expect(workspace.getByRole("status")).toHaveText("4 of 4 versions");
    for (const name of [original.name, candidate.name])
      await workspace
        .getByRole("button", { name: `Compare ${name}`, exact: true })
        .click();
    await workspace
      .getByRole("button", { name: "Compare (2)", exact: true })
      .click();
    await expect(
      workspace.getByRole("table", { name: "Strategy settings", exact: true }),
    ).toBeVisible();
    expect(
      (await cli(["wen-research", "--profile", "wen-operator"])).versions.find(
        (v: any) => v.digest === savedUI.digest,
      ),
    ).toEqual(savedUI);
    expect(remote).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: join(root, `comparison-${info.project.name}.png`),
      fullPage: true,
    });
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
