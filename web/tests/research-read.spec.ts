import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");

test("selected research reads share UI/CLI data, preserve losses and survive restart without writes", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const root = resolve(".evidence/f3-research-read");
  await mkdir(join(root, "fixtures"), { recursive: true });
  const dir = await mkdtemp(join(root, "fixtures/core-"));
  const password = "generated-read-fixture-password";
  let child: ChildProcess | undefined,
    output = "",
    url = "",
    release: (() => void) | undefined;
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
  const command = async (flag: string, body: object, name = "wen-research") => {
    const p = spawn(binary, [name, "--data-dir", dir, flag]);
    let out = "",
      err = "";
    p.stdout!.on("data", (b) => (out += b));
    p.stderr!.on("data", (b) => (err += b));
    p.stdin!.end(JSON.stringify(body));
    expect((await once(p, "exit"))[0], err).toBe(0);
    return JSON.parse(out);
  };
  const read = () => cli(["agents", "--profile", "wen-operator"]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      external.push(r.url());
  });
  const normalize = (r: any) => {
    const clone = structuredClone(r);
    delete clone.preparedAt;
    delete clone.resultSHA256;
    delete clone.usage.elapsedMs;
    for (const v of clone.results) delete v.elapsedMs;
    return clone;
  };
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
    let library = await command("--request-stdin", {
      profile: "wen-operator",
      revision: 0,
      action: "save",
      raw,
    });
    const first = library.versions[0];
    library = await command("--request-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      action: "save",
      raw: raw
        .replace("Fixture session draft", "Fixture alternate")
        .replace('"everyNthRound": 3', '"everyNthRound": 6'),
    });
    const history = JSON.parse(
      (
        await readFile("internal/core/testdata/wen-saved-history.json", "utf8")
      ).replaceAll("a".repeat(64), first.digest),
    );
    history.source = "Owner & <scope> \u2028";
    library = await command("--request-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      action: "history",
      raw: JSON.stringify(history),
    });
    const capture = await command("--review-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      strategyDigest: first.digest,
    });
    let view = await command(
      "--request-stdin",
      {
        action: "wen-research-chat",
        profileId: capture.profileId,
        revision: capture.revision,
        expiresAt: capture.expiresAt,
        text: capture.text,
        source: capture.source,
      },
      "agents",
    );
    const session = view.sessions[0].id;
    const text =
      'Frozen research\n{"schema":"wen-research-chat-context/1","quoteEventAt":"2026-10-22T11:59:58Z","quoteReceivedAt":"2026-10-22T11:59:59Z"}';
    view = await command(
      "--request-stdin",
      {
        action: "message-add",
        profileId: "wen-operator",
        revision: view.revision,
        id: session,
        text,
      },
      "agents",
    );
    await page.goto(url + "/#agents/chat");
    await expect(
      page.getByRole("heading", {
        name: "Research: Kraken BTC/USD",
        exact: true,
      }),
    ).toHaveCount(0);
    await page
      .getByRole("button", { name: "Inspect research", exact: true })
      .click();
    const panel = page.getByRole("region", {
      name: "Research inspection",
      exact: true,
    });
    await expect(
      panel.getByRole("checkbox", { name: "Fixture alternate", exact: true }),
    ).toBeVisible();
    await panel.getByRole("checkbox", { name: /^Frozen research/ }).check();
    await panel
      .getByRole("checkbox", { name: "Fixture session draft", exact: true })
      .check();
    await panel
      .getByRole("checkbox", { name: "Fixture alternate", exact: true })
      .check();
    await panel
      .getByLabel("Imported history", { exact: true })
      .selectOption("0");
    await panel
      .getByLabel("History strategy", { exact: true })
      .selectOption(first.digest);
    const before = await read();
    const response = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/v1/wen/research/read") &&
        r.request().method() === "POST",
    );
    await panel
      .getByRole("button", { name: "Inspect selected records", exact: true })
      .click();
    const http = await response;
    expect(http.status()).toBe(200);
    const ui = await http.json(),
      input = http.request().postDataJSON();
    expect(ui.status).toBe("complete");
    expect(ui.usage.completed).toBe(5);
    const fromCLI = await command("--read-stdin", input);
    expect(normalize(fromCLI)).toEqual(normalize(ui));
    await expect(
      panel.getByRole("region", { name: "Research results", exact: true }),
    ).toContainText("complete · 5/5 completed");
    const summary = ui.results.find(
      (r: any) => r.tool === "wen.history.summary",
    ).data;
    expect(summary.summary.netSOL).toBe("-100001015");
    expect(summary.verified).toBe(false);
    expect(summary.gaps).toHaveLength(2);
    expect(summary.revertedSelectedRows).toBe(1);
    expect(summary.skipped).toBeNull();
    expect(ui.sources[0].observedAt).toBe("2026-10-22T11:59:58Z");
    expect(await read()).toEqual(before);
    expect(await cli(["wen-research", "--profile", "wen-operator"])).toEqual(
      library,
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: join(root, `${info.project.name}.png`),
      fullPage: true,
    });
    // Deliberately delayed read delivery: cancelling must hide late output and
    // writes remain absent even if the deterministic server read already finished.
    let pending = false;
    const gate = new Promise<void>((resolve) => (release = resolve));
    await page.route("**/api/v1/wen/research/read", async (route) => {
      const result = await route.fetch();
      pending = true;
      await gate;
      await route.fulfill({ response: result }).catch(() => {});
    });
    await panel
      .getByRole("button", { name: "Inspect selected records", exact: true })
      .click();
    await expect.poll(() => pending).toBe(true);
    await panel
      .getByRole("button", { name: "Cancel inspection", exact: true })
      .click();
    release();
    release = undefined;
    await expect(
      panel.getByRole("region", { name: "Research results", exact: true }),
    ).toHaveCount(0);
    await page.unroute("**/api/v1/wen/research/read");
    expect(await read()).toEqual(before);
    library = await command("--request-stdin", {
      profile: "wen-operator",
      revision: library.revision,
      action: "save",
      raw: raw.replace("Fixture session draft", "Later version"),
    });
    await panel
      .getByRole("button", { name: "Inspect selected records", exact: true })
      .click();
    await expect(panel.getByRole("alert")).toContainText("source_changed");
    await expect(
      panel.getByRole("region", { name: "Research results", exact: true }),
    ).toHaveCount(0);
    expect(await read()).toEqual(before);
    await panel
      .getByRole("button", { name: "Close inspection", exact: true })
      .click();
    await stop();
    await start();
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await page
      .getByRole("button", { name: "Inspect research", exact: true })
      .click();
    await expect(
      panel.getByRole("checkbox", { name: "Later version", exact: true }),
    ).toBeVisible();
    const refreshed = { ...input, libraryRevision: library.revision };
    const afterRestart = await command("--read-stdin", refreshed);
    expect(afterRestart.status).toBe("complete");
    expect(await read()).toEqual(before);
    expect((await read()).financialExecution).toBe(false);
    expect((await read()).providerCallsEnabled).toBe(false);
    expect((await read()).aiRequests).toEqual([]);
    expect((await read()).tasks).toEqual([]);
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
          tools: ui.results.map((r: any) => r.tool),
          uiCLI: true,
          historyLossesPreserved: true,
          noWrites: true,
          declaredTimesPreserved: true,
          staleSourceRefusal: true,
          cancelLateDelivery: true,
          restart: true,
          fixtureOnly: true,
          externalRequests: external,
        },
        null,
        2,
      ) + "\n",
    );
  } finally {
    release?.();
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
