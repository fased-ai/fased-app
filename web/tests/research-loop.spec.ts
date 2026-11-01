import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";

test("reviewed v2 continuations share UI/CLI consent, transcript and restart", async ({
  page,
}, info) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(10000);
  const binary = resolve(".evidence/bin/fased-app");
  const root = resolve(".evidence/f3-research-loop");
  await mkdir(join(root, "fixtures"), { recursive: true });
  const dir = await mkdtemp("/tmp/fased-research-v2-"); // Disposable, reconstructible fixture; persistent results below.
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
    await writeFile(join(root, `${info.project.name}-startup.log`), output);
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
  const select = (id: string, revision = 0) => ({
    schema: "wen-research-ai-selection/2",
    profile: "wen-operator",
    id,
    revision,
  });
  const inspect = (id: string) =>
    command("wen-research", ["--ai-inspect-stdin"], select(id));
  const external: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      external.push(r.url());
  });
  const records: unknown[] = [];
  const ai = page.getByRole("region", {
    name: "Optional AI request review",
    exact: true,
  });
  const consentName =
    "I reviewed this wire and its allowed local reads and consent to one fixture request";
  const show = async (id: string) => {
    await page.reload();
    const q = (await inspect(id)).request;
    const article = ai.locator("article.ai-request").filter({
      has: page.getByRole("heading", { name: q.prompt, exact: true }),
    });
    await article
      .getByRole("button", { name: "Inspect research turn", exact: true })
      .click();
    const region = article.getByRole("region", {
      name: "Research turn record",
      exact: true,
    });
    await expect(region.locator("pre")).toBeVisible();
    const record = await inspect(id);
    expect(JSON.parse(await region.locator("pre").innerText())).toEqual(record);
    return { region, record };
  };
  const startInput = (r: any) => ({
    schema: "wen-research-ai-start/2",
    profile: "wen-operator",
    id: r.turn.id,
    revision: r.revision,
    requestId: r.request.id,
    consentDigest: r.request.digest,
    step: r.request.researchStep,
  });
  const nextInput = (r: any) => ({
    schema: "wen-research-ai-next/1",
    profile: "wen-operator",
    id: r.turn.id,
    revision: r.revision,
    fromRequestId: r.request.id,
    fromFrameSHA256: r.turn.models.at(-1).frameSHA256,
  });
  try {
    await start();
    await page.goto(url);
    const claim = JSON.parse(
      await readFile(join(dir, "claim-code.json"), "utf8"),
    );
    await openOwnerSetup(page, claim.code);
    await page.getByLabel("Create owner password").fill(password);
    await page
      .getByRole("button", { name: "Claim workspace", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    await command("login", ["--password-stdin"], password + "\n");
    let v = await read();
    v = await change(v, { action: "session-create", name: "Continuation" });
    const session = v.sessions.at(-1).id;
    v = await change(v, {
      action: "message-add",
      id: session,
      text: "Frozen.",
    });
    const before = await read();
    await page.goto(url + "/#agents/chat");
    await page.getByRole("button", { name: "Sources", exact: true }).click();
    const panel = page.getByRole("region", {
      name: "Research inspection",
      exact: true,
    });
    await panel.getByRole("checkbox", { name: /^Frozen\./ }).check();
    await panel
      .getByRole("button", { name: "Inspect selected records", exact: true })
      .click();
    await panel
      .getByRole("button", { name: "Review research with AI", exact: true })
      .click();
    const group = ai.getByRole("group", {
      name: "Selected research for AI",
      exact: true,
    });
    const manifest = JSON.parse(await group.locator("pre").innerText());
    await group
      .getByRole("combobox", { name: "Research mode", exact: true })
      .selectOption("loop");
    await ai.locator(".message-options > summary").click();
    await ai
      .getByRole("combobox", { name: "Memory", exact: true })
      .selectOption("none");
    await ai
      .getByRole("textbox", { name: "Message", exact: true })
      .fill("Two-step review.");
    await ai
      .getByRole("spinbutton", { name: "Model attempts", exact: true })
      .fill("2");
    await ai
      .getByRole("button", { name: "Prepare research disclosure", exact: true })
      .click();
    await expect(
      ai.getByRole("heading", { name: "Two-step review.", exact: true }),
    ).toBeVisible();
    v = await read();
    const id = v.aiRequests[0].researchTurnId;
    let { region, record } = await show(id);
    expect(record.schema).toBe("wen-research-ai-review/2");
    expect(record.turn.state).toBe("prepared");
    expect(record.request.heldMicrousd).toBe(0);
    const run = region.getByRole("button", {
      name: "Run research fixture",
      exact: true,
    });
    await expect(run).toBeDisabled();
    await region
      .getByRole("checkbox", { name: consentName, exact: true })
      .check();
    await run.click();
    await expect
      .poll(async () => (await inspect(id)).turn.state)
      .toBe("awaiting_review");
    ({ region, record } = await show(id));
    expect(record.turn.models).toHaveLength(1);
    expect(record.turn.tools).toHaveLength(1);
    expect(record.turn.tools[0].result.status).toBe("ok");
    expect(record.request.spentMicrousd).toBeGreaterThan(0);
    expect(record.request.heldMicrousd).toBe(0);
    const deadline = record.turn.deadlineAt;
    const snapshot = JSON.stringify(record);
    await expect
      .poll(async () => JSON.stringify(await inspect(id)))
      .toBe(snapshot);
    await region
      .getByRole("button", {
        name: "Prepare next research request",
        exact: true,
      })
      .click();
    await expect.poll(async () => (await inspect(id)).turn.currentStep).toBe(2);
    ({ region, record } = await show(id));
    expect(record.turn.state).toBe("prepared");
    expect(record.turn.deadlineAt).toBe(deadline);
    const context = JSON.parse(record.request.context);
    expect(context.research.priorSteps).toHaveLength(1);
    expect(context.research.priorSteps[0].reads).toHaveLength(1);
    expect(record.request.heldMicrousd).toBe(0);
    await expect(
      region.getByRole("checkbox", { name: consentName, exact: true }),
    ).not.toBeChecked();
    await expect(
      region.getByRole("button", { name: "Run research fixture", exact: true }),
    ).toBeDisabled();
    await command(
      "wen-research",
      ["--ai-start-stdin"],
      { ...startInput(record), step: 1 },
      false,
    );
    expect((await inspect(id)).revision).toBe(record.revision);
    await region
      .getByRole("checkbox", { name: consentName, exact: true })
      .check();
    await region
      .getByRole("button", { name: "Run research fixture", exact: true })
      .click();
    await expect
      .poll(async () => (await inspect(id)).turn.state)
      .toBe("completed");
    const complete = await inspect(id);
    expect(complete.turn.models).toHaveLength(2);
    records.push(complete);
    // Three model calls use three independent exact consents, never an automatic continuation.
    v = await read();
    let three = await command("wen-research", ["--ai-prepare-stdin"], {
      schema: "wen-research-ai-prepare/2",
      selection: {
        ...manifest,
        revision: v.revision,
        sources: [{ ...manifest.sources[0], refId: "s" }],
        calls: [
          { ...manifest.calls[0], id: "read-1", sourceRefs: ["s"] },
          { ...manifest.calls[0], id: "read-2", sourceRefs: ["s"] },
        ],
      },
      prompt: "Three-step review.",
      model: complete.request.model,
      memorySelection: { mode: "none" },
      limits: {
        ...complete.request.limits,
        maxInputTokens: 32000,
        maxOutputTokens: 128,
        budgetMicrousd: 33000,
      },
      bounds: {
        maxModelAttempts: 3,
        maxToolAttempts: 8,
        wallSeconds: 90,
        budgetMicrousd: 99000,
      },
    });
    for (let step = 1; step <= 3; step++) {
      ({ record: three } = await show(three.turn.id));
      expect(three.turn.state).toBe("prepared");
      expect(three.request.researchStep).toBe(step);
      await command("wen-research", ["--ai-start-stdin"], startInput(three));
      await expect
        .poll(async () => (await inspect(three.turn.id)).turn.state)
        .toBe(step === 3 ? "completed" : "awaiting_review");
      three = await inspect(three.turn.id);
      if (step < 3)
        three = await command(
          "wen-research",
          ["--ai-prepare-next-stdin"],
          nextInput(three),
        );
    }
    expect(three.turn.models).toHaveLength(3);
    expect(three.turn.tools).toHaveLength(2);
    await command(
      "wen-research",
      ["--ai-prepare-next-stdin"],
      nextInput(three),
      false,
    );
    expect((await inspect(three.turn.id)).revision).toBe(three.revision);
    records.push(three);
    ({ region } = await show(three.turn.id));
    await expect(
      region.getByRole("button", {
        name: "Prepare next research request",
        exact: true,
      }),
    ).toHaveCount(0);
    await page.screenshot({
      path: join(root, `${info.project.name}.png`),
      fullPage: true,
    });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    // Expiry and owner cancellation share the same retained child and known usage.
    v = await read();
    const makeTurn = async (prompt: string, models: number, seconds: number) =>
      command("wen-research", ["--ai-prepare-stdin"], {
        schema: "wen-research-ai-prepare/2",
        selection: { ...manifest, revision: (await read()).revision },
        prompt,
        model: complete.request.model,
        memorySelection: { mode: "none" },
        limits: complete.request.limits,
        bounds: {
          maxModelAttempts: models,
          maxToolAttempts: 8,
          wallSeconds: seconds,
          budgetMicrousd: models * complete.request.limits.budgetMicrousd,
        },
      });
    let expired = await makeTurn("Expired review.", 2, 2);
    await command("wen-research", ["--ai-start-stdin"], startInput(expired));
    await expect
      .poll(async () => (await inspect(expired.turn.id)).turn.state)
      .toBe("awaiting_review");
    await expect
      .poll(async () => (await inspect(expired.turn.id)).sourceState)
      .toBe("deadline_expired");
    ({ region, record: expired } = await show(expired.turn.id));
    await expect(
      region.getByRole("button", {
        name: "Prepare next research request",
        exact: true,
      }),
    ).toBeDisabled();
    await region
      .getByRole("button", { name: "Cancel research turn", exact: true })
      .click();
    await expect
      .poll(async () => (await inspect(expired.turn.id)).turn.state)
      .toBe("cancelled");
    expired = await inspect(expired.turn.id);
    expect(expired.request.state).toBe("completed");
    expect(expired.request.spentMicrousd).toBeGreaterThan(0);
    records.push(expired);
    const inert = await makeTurn("Prepared restart.", 1, 90);
    expect(inert.request.heldMicrousd).toBe(0);
    await stop();
    const priorBinary = process.env.FASED_RESEARCH_PREVIOUS_CORE;
    if (priorBinary) {
      const p = spawn(priorBinary, [
        "serve",
        "--listen",
        "127.0.0.1:0",
        "--data-dir",
        dir,
      ]);
      let old = "";
      p.stdout!.on("data", (b) => (old += b));
      p.stderr!.on("data", (b) => (old += b));
      const code = (await once(p, "exit"))[0];
      expect(code, old).not.toBe(0);
      expect(old).toContain("schema is newer");
      await writeFile(
        join(root, `${info.project.name}-schema12-refusal.json`),
        JSON.stringify({ code, output: old }),
      );
    }
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await command("login", ["--password-stdin"], password + "\n");
    const restarted = await inspect(three.turn.id);
    expect(restarted.turn).toEqual(three.turn);
    expect(restarted.request).toEqual(three.request);
    v = await read();
    expect(v.aiRequests).toHaveLength(7);
    const preparedAgain = await inspect(inert.turn.id);
    expect(preparedAgain.turn.state).toBe("prepared");
    expect(preparedAgain.request).toEqual(inert.request);
    await command(
      "wen-research",
      ["--ai-start-stdin"],
      startInput(preparedAgain),
    );
    await expect
      .poll(async () => (await inspect(inert.turn.id)).turn.state)
      .toBe("completed");
    const afterOne = await inspect(inert.turn.id);
    expect(afterOne.turn.models).toHaveLength(1);
    expect(afterOne.turn.tools).toEqual([]);
    expect(afterOne.turn.models[0].frame.decision).toBe("complete");
    records.push(afterOne);
    expect(v.tasks).toEqual(before.tasks);
    expect(v.messages).toEqual(before.messages);
    expect(v.memories).toEqual(before.memories);
    expect(external).toEqual([]);
    await writeFile(
      join(root, `${info.project.name}-records.json`),
      JSON.stringify(
        { result: "PASS", fixtureOnly: true, records, external },
        null,
        2,
      ),
    );
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
