import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";

test("one consented research fixture shares UI/CLI accounting and survives restart", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const binary = resolve(".evidence/bin/fased-app");
  const root = resolve(
    process.env.FASED_RESEARCH_FIXTURE_EVIDENCE ??
      ".evidence/f3-research-fixture",
  );
  await mkdir(root, { recursive: true });
  // Reconstructible test state is temporary; project-owned inputs/results remain.
  // Keep Unix IPC paths independent of checkout length. The finally block removes it.
  const dir = await mkdtemp(join(tmpdir(), "frq-"));
  const password = "disposable-research-preparation-fixture";
  let child: ChildProcess | undefined,
    output = "",
    url = "";
  const start = async () => {
    output = "";
    child = spawn(binary, [
      "serve",
      "--ai-fixture",
      "--listen",
      "127.0.0.1:0",
      "--data-dir",
      dir,
    ]);
    child.stdout!.on("data", (b) => (output += b));
    child.stderr!.on("data", (b) => (output += b));
    try {
      await expect
        .poll(() => output.includes('"event":"listening"'))
        .toBe(true);
    } catch (error) {
      await writeFile(join(root, `${info.project.name}-startup.log`), output);
      throw Error(`Fixture core startup failed:\n${output}\n${String(error)}`);
    }
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
  const command = async (
    name: string,
    flags: string[],
    input?: unknown,
    success = true,
  ) => {
    const p = spawn(binary, [name, ...flags, "--data-dir", dir]);
    let out = "",
      err = "";
    p.stdout!.on("data", (b) => (out += b));
    p.stderr!.on("data", (b) => (err += b));
    p.stdin!.end(
      input === undefined
        ? undefined
        : typeof input === "string"
          ? input
          : JSON.stringify(input),
    );
    const code = (await once(p, "exit"))[0];
    if (!success) {
      expect(code, out).not.toBe(0);
      return err;
    }
    expect(code, err).toBe(0);
    if (name === "login") return out;
    return JSON.parse(out || "null");
  };
  const read = () => command("agents", ["--profile", "wen-operator"]);
  const change = async (v: any, body: object) =>
    command("agents", ["--request-stdin"], {
      profileId: "wen-operator",
      revision: v.revision,
      ...body,
    });
  const turn = (id: string, revision = 0) => ({
    schema: "wen-research-ai-selection/1",
    profile: "wen-operator",
    id,
    revision,
  });
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
    await command("login", ["--password-stdin"], password + "\n");
    let v = await read();
    v = await change(v, {
      action: "session-create",
      name: "Research preparation",
    });
    const session = v.sessions.at(-1).id;
    v = await change(v, {
      action: "message-add",
      id: session,
      text: 'Frozen research\n{"quoteEventAt":"2026-10-22T11:59:58Z"}',
    });
    const before = await read();
    await page.goto(url + "/#agents/chat");
    await page
      .getByRole("button", { name: "Inspect research", exact: true })
      .click();
    const panel = page.getByRole("region", {
      name: "Research inspection",
      exact: true,
    });
    await panel.getByRole("checkbox", { name: /^Frozen research/ }).check();
    await panel
      .getByRole("button", { name: "Inspect selected records", exact: true })
      .click();
    await panel
      .getByRole("button", { name: "Review research with AI", exact: true })
      .click();
    const ai = page.getByRole("region", {
      name: "Optional AI request review",
      exact: true,
    });
    await expect(
      ai.getByRole("group", { name: "Selected research for AI", exact: true }),
    ).toBeVisible();
    const manifest = JSON.parse(
      await ai
        .getByRole("group", { name: "Selected research for AI", exact: true })
        .locator("pre")
        .innerText(),
    );
    expect((await read()).revision).toBe(before.revision);
    await ai
      .getByRole("combobox", { name: "Memory", exact: true })
      .selectOption("none");
    await ai
      .getByRole("textbox", { name: "AI request", exact: true })
      .fill("Explain this retained source and its limits.");
    await ai
      .getByRole("button", { name: "Prepare research disclosure", exact: true })
      .click();
    await expect(
      ai.getByRole("heading", {
        name: "Explain this retained source and its limits.",
        exact: true,
      }),
    ).toBeVisible();
    v = await read();
    expect(v.revision).toBe(before.revision + 1);
    const q = v.aiRequests[0];
    expect(q.state).toBe("prepared");
    expect(q.heldMicrousd).toBe(0);
    expect(q.spentMicrousd).toBe(0);
    expect(q.researchStep).toBe(1);
    const inspect = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(inspect.sourceState).toBe("unchanged");
    expect(inspect.dispatchEnabled).toBe(false);
    expect(inspect.turn.startedAt).toBe(0);
    expect(inspect.turn.tools).toEqual([]);
    expect(inspect.turn.models[0].attempted).toBe(false);
    expect(inspect.request.context).toBe(q.context);
    await expect(
      ai.getByRole("button", { name: "Run development fixture", exact: true }),
    ).toHaveCount(0);
    await expect(
      ai.getByRole("button", { name: "Use as draft", exact: true }),
    ).toHaveCount(0);
    await ai
      .getByRole("button", { name: "Inspect research turn", exact: true })
      .click();
    const record = ai.getByRole("region", {
      name: "Research turn record",
      exact: true,
    });
    await expect(record.getByRole("status")).toContainText("unchanged");
    expect(JSON.parse(await record.locator("pre").innerText())).toEqual(
      inspect,
    );
    expect((await read()).revision).toBe(v.revision);
    const consent = record.getByRole("checkbox", {
      name: "I reviewed this research wire and consent to one fixture request",
      exact: true,
    });
    const run = record.getByRole("button", {
      name: "Run research fixture",
      exact: true,
    });
    await expect(run).toBeDisabled();
    expect((await read()).aiRequests[0].state).toBe("prepared");
    await consent.check();
    await run.click();
    await expect
      .poll(async () => (await read()).aiRequests[0].state)
      .toBe("completed");
    const completed = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(completed.turn.models).toEqual([
      { id: q.id, step: 1, digest: q.digest, attempted: true },
    ]);
    expect(completed.turn.deadlineAt - completed.turn.startedAt).toBe(90);
    expect(completed.turn.tools).toEqual([]);
    expect(completed.request.disclosure).toBe(q.disclosure);
    expect(completed.request.heldMicrousd).toBe(0);
    expect(completed.request.spentMicrousd).toBeGreaterThan(0);
    v = await read();
    const viaCLI = await command("wen-research", ["--ai-prepare-stdin"], {
      schema: "wen-research-ai-prepare/1",
      selection: { ...manifest, revision: v.revision },
      prompt: q.prompt,
      model: q.model,
      memorySelection: { mode: "none" },
      limits: q.limits,
      bounds: {
        maxModelAttempts: 1,
        maxToolAttempts: 8,
        wallSeconds: 90,
        budgetMicrousd: q.limits.budgetMicrousd,
      },
    });
    expect(viaCLI.turn.rootSHA256).toBe(completed.turn.rootSHA256);
    const startInput = {
      schema: "wen-research-ai-start/1",
      profile: "wen-operator",
      id: viaCLI.turn.id,
      revision: viaCLI.revision,
      requestId: viaCLI.request.id,
      consentDigest: viaCLI.request.digest,
    };
    const rejected = await command(
      "wen-research",
      ["--ai-start-stdin"],
      { ...startInput, consentDigest: "0".repeat(64) },
      false,
    );
    expect(rejected).toContain("exact disclosure digest");
    expect((await read()).revision).toBe(viaCLI.revision);
    const queued = await command(
      "wen-research",
      ["--ai-start-stdin"],
      startInput,
    );
    expect(queued.request.state).toBe("queued");
    expect(queued.request.heldMicrousd).toBeGreaterThan(0);
    await expect
      .poll(async () => (await read()).aiRequests[1].state)
      .toBe("completed");
    const second = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(viaCLI.turn.id),
    );
    expect(second.request.spentMicrousd).toBeGreaterThan(0);
    expect(second.request.heldMicrousd).toBe(0);
    expect(second.turn.models[0].attempted).toBe(true);
    await command(
      "wen-research",
      ["--ai-start-stdin"],
      { ...startInput, revision: second.revision },
      false,
    );
    await page.reload();
    await ai
      .getByRole("button", { name: "Inspect research turn", exact: true })
      .first()
      .click();
    const shown = ai
      .getByRole("region", { name: "Research turn record", exact: true })
      .first();
    const parity = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(JSON.parse(await shown.locator("pre").innerText())).toEqual(parity);
    await page.screenshot({
      path: join(root, `${info.project.name}.png`),
      fullPage: true,
    });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await stop();
    const priorBinary = process.env.FASED_RESEARCH_PREVIOUS_CORE;
    if (priorBinary) {
      const prior = spawn(priorBinary, [
        "serve",
        "--listen",
        "127.0.0.1:0",
        "--data-dir",
        dir,
      ]);
      let priorOutput = "";
      prior.stdout!.on("data", (b) => (priorOutput += b));
      prior.stderr!.on("data", (b) => (priorOutput += b));
      const priorCode = (await once(prior, "exit"))[0];
      expect(priorCode, priorOutput).not.toBe(0);
      expect(priorOutput).toContain("state schema is newer");
      await writeFile(
        join(root, `${info.project.name}-older-reader.json`),
        JSON.stringify({ code: priorCode, output: priorOutput }),
      );
    } else {
      await writeFile(
        join(root, `${info.project.name}-compat-not-requested.json`),
        JSON.stringify({
          status: "N/A",
          reason:
            "No preceding-binary fixture supplied; prior refusal proof is retained separately.",
        }),
      );
    }
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await command("login", ["--password-stdin"], password + "\n");
    const restarted = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(restarted.request).toEqual(completed.request);
    expect(restarted.turn).toEqual(completed.turn);
    v = await read();
    expect(v.aiRequests).toHaveLength(2);
    expect(v.tasks).toEqual(before.tasks);
    expect(v.memories).toEqual(before.memories);
    expect(v.messages).toEqual(before.messages);
    expect(external).toEqual([]);
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
