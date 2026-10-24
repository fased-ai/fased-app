import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";

test("inert research preparation shares UI/CLI facts, cancellation and restart without dispatch", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const binary = resolve(".evidence/bin/fased-app");
  const root = resolve(".evidence/f3-research-prepare");
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
    await page.getByRole("button", { name: "Sources", exact: true }).click();
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
    await ai.locator(".message-options > summary").click();
    await ai
      .getByRole("combobox", { name: "Memory", exact: true })
      .selectOption("none");
    await ai
      .getByRole("textbox", { name: "Message", exact: true })
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
    const blocked = await command(
      "agents",
      ["--request-stdin"],
      {
        revision: v.revision,
        profileId: "wen-operator",
        action: "ai-start",
        id: q.id,
        consentDigest: q.digest,
      },
      false,
    );
    expect(blocked).toContain("research dispatch is disabled");
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
    expect(viaCLI.turn.rootSHA256).toBe(inspect.turn.rootSHA256);
    expect(viaCLI.turn.facts).toEqual(inspect.turn.facts);
    expect(viaCLI.dispatchEnabled).toBe(false);
    await command(
      "wen-research",
      ["--ai-cancel-stdin"],
      turn(viaCLI.turn.id, viaCLI.revision),
    );
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
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await command("login", ["--password-stdin"], password + "\n");
    await expect(
      page.getByRole("heading", { name: q.prompt, exact: true }).first(),
    ).toBeVisible();
    const restarted = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(restarted.turn).toEqual(inspect.turn);
    expect(restarted.request).toEqual(inspect.request);
    v = await read();
    v = await change(v, {
      action: "message-add",
      id: session,
      text: "A new retained question changes the selected context.",
    });
    const changed = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(changed.sourceState).toBe("source_changed");
    expect(changed.request.digest).toBe(q.digest);
    await page.reload();
    await page
      .getByRole("button", { name: "Cancel request", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).aiRequests[0].state)
      .toBe("cancelled");
    v = await read();
    const cancelled = await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      turn(q.researchTurnId),
    );
    expect(cancelled.turn.state).toBe("cancelled");
    expect(cancelled.request.heldMicrousd).toBe(0);
    await command(
      "wen-research",
      ["--ai-cancel-stdin"],
      turn(q.researchTurnId, v.revision),
      false,
    );
    await command(
      "wen-research",
      ["--ai-inspect-stdin"],
      { ...turn(q.researchTurnId), profile: "news-researcher" },
      false,
    );
    expect((await read()).revision).toBe(v.revision);
    expect(external).toEqual([]);
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
