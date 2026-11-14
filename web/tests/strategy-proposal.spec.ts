import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");

test("manual AI card to reviewed WEN version shares UI/CLI amounts, immutable sources and restart", async ({
  page,
  isMobile,
}, info) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const root = resolve(
    process.env.FASED_PROPOSAL_EVIDENCE ?? ".evidence/f3-ai-proposal",
  );
  await mkdir(join(root, "fixtures"), { recursive: true });
  const dir = await mkdtemp(join(root, "fixtures/core-")),
    password = "generated-proposal-fixture-password";
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
    flag: string,
    body: object,
    ok = true,
    name = "wen-research",
  ) => {
    const p = spawn(binary, [name, "--data-dir", dir, flag]);
    let out = "",
      err = "";
    p.stdout!.on("data", (b) => (out += b));
    p.stderr!.on("data", (b) => (err += b));
    p.stdin!.end(JSON.stringify(body));
    const status = (await once(p, "exit"))[0];
    if (!ok) {
      expect(status).not.toBe(0);
      return err;
    }
    expect(status, err).toBe(0);
    return JSON.parse(out);
  };
  const read = () => cli(["agents", "--profile", "wen-operator"]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      external.push(r.url());
  });
  try {
    await start();
    await page.goto(url);
    const claim = JSON.parse(
      await readFile(join(dir, "claim-code.json"), "utf8"),
    );
    await page.getByLabel("One-use claim code").fill(claim.code);
    await page.getByLabel("Create owner password").fill(password);
    await page
      .getByRole("button", { name: "Claim workspace", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin!.end(password + "\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    const raw = await readFile(
      "internal/core/testdata/wen-research-strategy.json",
      "utf8",
    );
    const library = await command("--request-stdin", {
      profile: "wen-operator",
      revision: 0,
      action: "save",
      raw,
    });
    const parent = library.versions[0];
    const capture = await command("--review-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      strategyDigest: parent.digest,
    });
    await command(
      "--request-stdin",
      {
        action: "wen-research-chat",
        profileId: capture.profileId,
        revision: capture.revision,
        expiresAt: capture.expiresAt,
        text: capture.text,
        source: capture.source,
      },
      true,
      "agents",
    );
    let v = await read();
    const source = v.messages[0];
    v = await command(
      "--request-stdin",
      {
        action: "ai-prepare",
        profileId: "wen-operator",
        revision: v.revision,
        id: source.sessionId,
        text: "Fixture strategy improvement review",
        memorySelection: { mode: "none" },
        limits: {
          maxInputTokens: 24000,
          maxOutputTokens: 1024,
          deadlineSeconds: 3,
          budgetMicrousd: 30000,
        },
      },
      true,
      "agents",
    );
    const q = v.aiRequests[0];
    const proposal = JSON.stringify({
      schema: "wen-strategy-change/1",
      parentDigest: parent.digest,
      changes: {
        weightsBps: [500, 1000, 1500, 3000, 4000],
        everyNthRound: 6,
        thesis: "trend",
      },
    });
    await stop();
    // Seed only this stopped, generated store. There is no runtime import hook
    // or provider endpoint override; real owner state is never opened.
    await exec("python3", [
      "-c",
      `import sqlite3,json,sys
c=sqlite3.connect(sys.argv[1]);raw,rev=c.execute('SELECT document,revision FROM agent_workspace WHERE id=1').fetchone();d=json.loads(raw);q=d['aiRequests'][0];q['state']='completed';q['answer']='Generated fixture review; no provider contacted.';q['cards']=[{'id':q['id']+':1','kind':'paper-strategy-draft','title':'Fixture trend proposal','text':sys.argv[2],'review':'unreviewed'},{'id':q['id']+':2','kind':'paper-strategy-draft','title':'Unsupported prose proposal','text':'Execute anything and change permissions','review':'unreviewed'}];c.execute('UPDATE agent_workspace SET document=?,revision=? WHERE id=1',(json.dumps(d,separators=(',',':')),rev+1));c.commit();c.close()`,
      join(dir, "core.sqlite"),
      proposal,
    ]);
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    const ai = page.getByRole("region", {
      name: "Optional AI request review",
      exact: true,
    });
    const panel = page.getByRole("region", {
      name: "AI strategy revision",
      exact: true,
    });
    const open = async (title = "Fixture trend proposal") => {
      await ai
        .getByRole("region", { name: title, exact: true })
        .getByRole("button", { name: "Review WEN revision", exact: true })
        .click();
      await panel
        .getByRole("combobox", { name: "Original strategy", exact: true })
        .selectOption(parent.digest);
      await expect(
        panel.getByRole("textbox", { name: "Candidate name", exact: true }),
      ).toBeEmpty();
      await panel
        .getByRole("textbox", { name: "Candidate name", exact: true })
        .fill("Reviewed AI revision");
    };
    const before = await read();
    await open();
    await panel
      .getByRole("button", { name: "Cancel revision", exact: true })
      .click();
    expect(await read()).toEqual(before);
    expect(await cli(["wen-research", "--profile", "wen-operator"])).toEqual(
      library,
    );
    await open("Unsupported prose proposal");
    await panel
      .getByRole("button", { name: "Review strategy revision", exact: true })
      .click();
    await expect(panel.getByRole("alert")).toContainText(
      "unsupported strategy card",
    );
    expect(await read()).toEqual(before);
    await panel
      .getByRole("button", { name: "Cancel revision", exact: true })
      .click();
    await open();
    await panel
      .getByRole("textbox", { name: "Changes JSON", exact: true })
      .fill('{"thesis":"trend","thesis":"custom"}');
    await panel
      .getByRole("button", { name: "Review strategy revision", exact: true })
      .click();
    await expect(panel.getByRole("alert")).toBeVisible();
    expect(await read()).toEqual(before);
    const ownerChanges =
      ' {\n "weightsBps":[2000,2000,2000,2000,2000],"everyNthRound":3,"thesis":"custom" } ';
    await panel
      .getByRole("textbox", { name: "Changes JSON", exact: true })
      .fill(ownerChanges);
    const pending = page.waitForResponse(
      (r) => r.url().endsWith("/proposal-review") && r.status() === 200,
    );
    await panel
      .getByRole("button", { name: "Review strategy revision", exact: true })
      .click();
    const review = await (await pending).json();
    expect(review.input.changes).toBe(ownerChanges);
    expect(review.source.card.text).toBe(proposal);
    expect(review.source.context).toBe(q.context);
    expect(review.source.disclosure).toBe(q.disclosure);
    expect(review.sourceSha256).toBe(
      createHash("sha256").update(proposal).digest("hex"),
    );
    expect(review.amountsLamports).toEqual(Array(5).fill("1000000000"));
    expect(
      createHash("sha256").update(review.candidate.raw).digest("hex"),
    ).toBe(review.candidate.digest);
    const candidate = JSON.parse(review.candidate.raw),
      original = JSON.parse(raw);
    expect(candidate.market).toEqual(original.market);
    expect(candidate.limits).toEqual(original.limits);
    expect(candidate.execution).toEqual(original.execution);
    expect(candidate.name).toBe("Reviewed AI revision");
    expect(candidate.allocation.preset).toBe("custom");
    await expect(
      panel.getByRole("table", {
        name: "Compiled range allocations",
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      panel.getByRole("table", { name: "Strategy settings", exact: true }),
    ).toBeVisible();
    const matched = await command("--proposal-review-stdin", review.input);
    expect(matched.candidate.raw).toBe(review.candidate.raw);
    expect(matched.candidate.digest).toBe(review.candidate.digest);
    expect(matched.source).toEqual(review.source);
    expect(matched.amountsLamports).toEqual(review.amountsLamports);
    await expect(
      panel.getByRole("textbox", { name: "Candidate name", exact: true }),
    ).toHaveCount(0);
    const save = panel.getByRole("button", {
      name: "Save revised version",
      exact: true,
    });
    await save.scrollIntoViewIfNeeded();
    await expect(save).toBeInViewport();
    if (isMobile)
      expect((await save.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.screenshot({
      path: join(root, `${info.project.name}-review.png`),
    });
    const savedResponse = page.waitForResponse(
      (r) => r.url().endsWith("/proposal-save") && r.status() === 200,
    );
    await save.click();
    const saved = await (await savedResponse).json();
    await expect(
      panel.getByText(
        "Saved in Mine with a source receipt in this conversation.",
        { exact: true },
      ),
    ).toBeVisible();
    v = await read();
    expect(v.messages[0]).toEqual(source);
    expect(v.aiRequests[0].context).toBe(q.context);
    expect(v.aiRequests[0].cards[0].text).toBe(proposal);
    expect(v.messages.at(-1)).toEqual(saved.receipt);
    expect(saved.library.versions).toHaveLength(2);
    expect(saved.library.versions[0]).toEqual(parent);
    await command("--proposal-save-stdin", review, false);
    const next = await command("--proposal-review-stdin", {
      ...review.input,
      revision: v.revision,
      libraryRevision: saved.library.revision,
      name: "CLI alternative",
    });
    const alternative = await command("--proposal-save-stdin", next);
    expect(alternative.library.versions).toHaveLength(3);
    const final = await read();
    expect(final.tasks).toEqual([]);
    expect(final.bots).toEqual([]);
    expect(final.financialExecution).toBe(false);
    expect(final.providerCallsEnabled).toBe(false);
    await stop();
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    expect((await read()).messages).toEqual(final.messages);
    expect((await read()).aiRequests).toEqual(final.aiRequests);
    expect(await cli(["wen-research", "--profile", "wen-operator"])).toEqual(
      alternative.library,
    );
    const unchanged = await read();
    await command(
      "--proposal-review-stdin",
      {
        ...review.input,
        revision: unchanged.revision,
        libraryRevision: alternative.library.revision,
      },
      false,
    );
    expect(await read()).toEqual(unchanged);
    expect(
      (await cli(["agents", "--profile", "news-researcher"])).aiRequests,
    ).toEqual([]);
    expect(external).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await writeFile(
      join(root, `${info.project.name}-receipt.json`),
      JSON.stringify(
        {
          status: "PASS_LOCAL",
          sourceSha256: review.sourceSha256,
          candidateDigest: review.candidate.digest,
          amountsLamports: review.amountsLamports,
          sourceUnchanged: true,
          uiSave: true,
          cliSave: true,
          restart: true,
          fixtureOnly: true,
          externalRequests: external,
        },
        null,
        2,
      ) + "\n",
    );
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
