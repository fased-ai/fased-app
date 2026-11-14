import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");

test("saved WEN source uses exact manual UI/CLI disclosure and explicit fixture consent across restart", async ({
  page,
  isMobile,
}, info) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const root = resolve(
    process.env.FASED_WEN_AI_ROOT ?? ".evidence/f3-manual-ai",
  );
  await mkdir(join(root, "fixtures"), { recursive: true });
  const dir = await mkdtemp(join(root, "fixtures/core-"));
  const password = "generated-wen-review-fixture-password";
  let child: ChildProcess | undefined,
    output = "",
    url = "";
  const start = async (fixture = false) => {
    output = "";
    child = spawn(binary, [
      "serve",
      "--listen",
      "127.0.0.1:0",
      "--data-dir",
      dir,
      ...(fixture ? ["--ai-fixture"] : []),
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
    if (!ok) {
      expect(status).not.toBe(0);
      return err;
    }
    expect(status, err).toBe(0);
    return JSON.parse(out);
  };
  const read = () => cli(["agents", "--profile", "wen-operator"]);
  const change = async (body: object, ok = true) =>
    command(
      "agents",
      "--request-stdin",
      { profileId: "wen-operator", revision: (await read()).revision, ...body },
      ok,
    );
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
    let library = await command("wen-research", "--request-stdin", {
      profile: "wen-operator",
      action: "save",
      revision: 0,
      raw,
    });
    const original = library.versions[0];
    const history = (
      await readFile("internal/core/testdata/wen-saved-history.json", "utf8")
    ).replaceAll("a".repeat(64), original.digest);
    library = await command("wen-research", "--request-stdin", {
      profile: "wen-operator",
      action: "history",
      revision: library.revision,
      raw: history,
    });
    const capture = await command("wen-research", "--review-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      strategyDigest: original.digest,
      bundleIndex: 0,
    });
    await change({
      action: "wen-research-chat",
      profileId: capture.profileId,
      revision: capture.revision,
      expiresAt: capture.expiresAt,
      text: capture.text,
      source: capture.source,
    });
    let v = await read();
    const source = v.messages[0];
    expect(source.text).toContain('"netSOL":"-100001015"');
    expect(source.text).toContain('"skipped":null');
    expect(source.text).toContain("participation unverified");
    await change({
      action: "memory-put",
      text: "Do not include this private note",
      source: "owner",
    });
    await change({
      action: "memory-put",
      profileId: "news-researcher",
      text: "Foreign profile note",
      source: "owner",
    });
    const review = page.getByRole("region", {
      name: "Optional AI request review",
      exact: true,
    });
    const open = async (afterRestart = false) => {
      await page.goto(url + "/#agents/chat");
      if (afterRestart) {
        await page.getByLabel("Owner password", { exact: true }).fill(password);
        await page
          .getByRole("button", { name: "Sign in", exact: true })
          .click();
      }
      await page
        .getByRole("button", {
          name: "Strategy: Fixture session draft",
          exact: true,
        })
        .click();
      await expect(review).toBeVisible();
    };
    const card = (prompt: string) =>
      review.locator("article.ai-request").filter({
        has: page.getByRole("heading", { name: prompt, exact: true }),
      });
    const prepare = async (prompt: string) => {
      await review
        .getByRole("combobox", { name: "Memory", exact: true })
        .selectOption("none");
      await review.getByLabel("AI request", { exact: true }).fill(prompt);
      await review
        .getByRole("button", { name: "Prepare disclosure", exact: true })
        .click();
      await expect
        .poll(async () =>
          (await read()).aiRequests.some(
            (q: { prompt: string }) => q.prompt === prompt,
          ),
        )
        .toBe(true);
      const q = (await read()).aiRequests.find(
        (q: { prompt: string }) => q.prompt === prompt,
      );
      await card(prompt)
        .getByText("Exact context disclosure", { exact: true })
        .click();
      await expect(card(prompt).locator("pre").first()).toHaveText(
        q.disclosure,
      );
      expect(q.state).toBe("prepared");
      expect(q.heldMicrousd).toBe(0);
      return q;
    };
    await open();
    const prompt = "Review the selected frozen WEN history";
    const ui = await prepare(prompt);
    const context = JSON.parse(ui.context);
    expect(context.context.messages).toEqual([
      { role: "user", text: source.text },
    ]);
    expect(context.context.memories).toEqual([]);
    expect(ui.context).not.toContain("Do not include this private note");
    expect(ui.context).not.toContain("Foreign profile note");
    const wire = JSON.parse(ui.disclosure);
    expect(wire.input).toBe(ui.context);
    expect(wire.tools).toEqual([]);
    expect(wire.tool_choice).toBe("none");
    expect(wire.store).toBe(false);
    await card(prompt)
      .getByRole("checkbox", {
        name: "I reviewed this context and these fixture limits",
        exact: true,
      })
      .check();
    await expect(
      card(prompt).getByRole("button", {
        name: "Run development fixture",
        exact: true,
      }),
    ).toBeDisabled();
    const viaCLI = await change({
      action: "ai-prepare",
      id: source.sessionId,
      text: prompt,
      memorySelection: { mode: "none" },
      limits: ui.limits,
    });
    expect(viaCLI.aiRequests.at(-1).context).toBe(ui.context);
    expect(viaCLI.aiRequests.at(-1).disclosure).toBe(ui.disclosure);
    const stable = await read();
    await change(
      { action: "ai-start", id: ui.id, consentDigest: ui.digest },
      false,
    );
    expect(await read()).toEqual(stable);
    await change({
      action: "message-add",
      id: source.sessionId,
      text: "New source context requires a fresh disclosure",
    });
    const changed = await read();
    // After fixture activation, consent for the older context must still refuse.
    await stop();
    await start(true);
    await open(true);
    await change(
      { action: "ai-start", id: ui.id, consentDigest: ui.digest },
      false,
    );
    expect(
      (await read()).aiRequests.map((q: { state: string }) => q.state),
    ).toEqual(changed.aiRequests.map((q: { state: string }) => q.state));
    const freshPrompt = "Review updated source without authorizing actions";
    const fresh = await prepare(freshPrompt);
    expect(JSON.parse(fresh.context).context.messages).toEqual([
      { role: "user", text: source.text },
      { role: "user", text: "New source context requires a fresh disclosure" },
    ]);
    await expect(
      card(freshPrompt).getByRole("button", {
        name: "Run development fixture",
        exact: true,
      }),
    ).toBeDisabled();
    await stop();
    await start(true);
    await open(true);
    const retained = (await read()).aiRequests.find(
      (q: { id: string }) => q.id === fresh.id,
    );
    expect(retained.context).toBe(fresh.context);
    expect(retained.disclosure).toBe(fresh.disclosure);
    expect(retained.state).toBe("prepared");
    const run = card(freshPrompt).getByRole("button", {
      name: "Run development fixture",
      exact: true,
    });
    await expect(run).toBeDisabled();
    await card(freshPrompt)
      .getByRole("checkbox", {
        name: "I reviewed this context and these fixture limits",
        exact: true,
      })
      .check();
    await run.scrollIntoViewIfNeeded();
    await expect(run).toBeInViewport();
    if (isMobile)
      expect((await run.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.screenshot({
      path: join(root, `${info.project.name}-consent.png`),
    });
    await run.click();
    await expect
      .poll(
        async () =>
          (await read()).aiRequests.find(
            (q: { id: string }) => q.id === fresh.id,
          ).state,
      )
      .toBe("completed");
    v = await read();
    const result = v.aiRequests.find((q: { id: string }) => q.id === fresh.id);
    expect(result.answer).toContain("Development fixture response");
    expect(result.heldMicrousd).toBe(0);
    expect(v.tasks).toEqual([]);
    expect(v.bots).toEqual([]);
    expect(v.financialExecution).toBe(false);
    expect(v.providerCallsEnabled).toBe(false);
    expect(v.messages[0]).toEqual(source);
    expect(await cli(["wen-research", "--profile", "wen-operator"])).toEqual(
      library,
    );
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
          sourceText: source.text,
          preparedContext: fresh.context,
          disclosure: fresh.disclosure,
          result,
          externalRequests: external,
          fixtureOnly: true,
          libraryUnchanged: true,
          tasksCreated: v.tasks.length,
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
