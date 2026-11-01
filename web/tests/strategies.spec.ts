import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import type { AccountsView } from "../src/accounts";
import type { AgentCommand, AgentView, TaskReview } from "../src/agents";

test.use({ colorScheme: "dark" });

test("versioned paper strategies join reviewed Tasks and CLI through pause edit and restart", async ({
  page,
}, info) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(10000);
  const root = resolve(".evidence/b06-strategies");
  await mkdir(root, { recursive: true, mode: 0o700 });
  const evidence = await mkdtemp(join(root, `${info.project.name}-journey-`));
  // Disposable, reconstructible and nonauthoritative. Keep core.sock short;
  // public inputs/results and stopped failure state are retained in evidence.
  const dir = await mkdtemp(join(tmpdir(), "fased-b06-"));
  // Reuse the binaries compiled by the joined CI journey. Ignored local
  // acceptance artifacts are not available in a clean checkout.
  const binary = resolve(
    process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
  );
  const seed = resolve(
    process.env.FASED_WALLET_BROWSER_BINARY ?? ".evidence/bin/wallet-core.test",
  );
  const password = "disposable-strategy-browser-owner-password";
  const profileId = "trading-analyst";
  const strategyName = "Paper accumulation";
  const firstName = "Accumulation run";
  const freshName = "Fresh paper run";
  let child: ChildProcess | undefined;
  let url = "";
  let startup = "";
  let attempt = 0;
  let passed = false;
  const external: string[] = [];
  const origins = new Set<string>();
  const writes: AgentCommand[] = [];

  async function execute(
    executable: string,
    args: string[],
    input?: unknown,
    env = process.env,
  ) {
    const p = spawn(executable, args, { cwd: resolve("."), env });
    let stdout = "",
      stderr = "";
    p.stdout!.on("data", (b) => {
      stdout += b;
    });
    p.stderr!.on("data", (b) => {
      stderr += b;
    });
    p.stdin!.end(
      input === undefined
        ? undefined
        : typeof input === "string"
          ? input
          : JSON.stringify(input),
    );
    const [code] = await once(p, "exit");
    expect(code, stderr || stdout).toBe(0);
    return { stdout, stderr };
  }
  async function cli<T>(args: string[], input?: unknown): Promise<T> {
    return JSON.parse(
      (await execute(binary, [...args, "--data-dir", dir], input)).stdout,
    );
  }
  const read = () => cli<AgentView>(["agents", "--profile", profileId]);
  const command = async (value: AgentCommand) =>
    cli<AgentView>(["agents", "--request-stdin"], {
      ...value,
      profileId,
      revision: (await read()).revision,
    });
  async function start(listen = "127.0.0.1:0") {
    startup = "";
    attempt++;
    child = spawn(binary, [
      "serve",
      "--ai-fixture",
      "--listen",
      listen,
      "--data-dir",
      dir,
    ]);
    child.stdout!.on("data", (b) => {
      startup += b;
    });
    child.stderr!.on("data", (b) => {
      startup += b;
    });
    const listening = () =>
      startup.split("\n").flatMap((line) => {
        try {
          const event = JSON.parse(line);
          return event.event === "listening" ? [event.url as string] : [];
        } catch {
          return [];
        }
      })[0];
    await expect
      .poll(listening, { message: "The disposable strategy core must start" })
      .toBeTruthy();
    url = listening();
    origins.add(new URL(url).origin);
  }
  async function stop() {
    if (child && child.exitCode === null) {
      const exited = once(child, "exit");
      child.kill("SIGTERM");
      let timer: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          exited,
          new Promise((_, reject) => {
            timer = setTimeout(() => {
              child?.kill("SIGKILL");
              reject(Error("Strategy fixture core did not stop"));
            }, 15000);
          }),
        ]);
      } finally {
        clearTimeout(timer);
      }
    }
    child = undefined;
    await writeFile(join(evidence, `core-${attempt}.log`), startup);
  }
  const strategiesTab = () =>
    page
      .locator(".task-views")
      .getByRole("button", { name: "Strategies", exact: true });
  const tasksTab = () =>
    page
      .locator(".task-views")
      .getByRole("button", { name: "Tasks", exact: true });
  async function selectProfile() {
    const selector = page.getByLabel("Agent", { exact: true });
    await expect(selector).toBeAttached();
    const menus = selector.locator("xpath=ancestor::details");
    for (let i = 0; i < (await menus.count()); i++) {
      if (await selector.isVisible()) break;
      const menu = menus.nth(i);
      if ((await menu.getAttribute("open")) === null)
        await menu.locator(":scope > summary").click();
    }
    await expect(selector).toBeVisible();
    await selector.selectOption(profileId);
    await expect(selector).toHaveValue(profileId);
  }
  const strategyRow = () =>
    page
      .getByRole("table", { name: "Saved strategies", exact: true })
      .locator("tbody tr")
      .filter({ hasText: strategyName });
  const taskRow = (name: string) =>
    page
      .getByRole("table", { name: "Tasks", exact: true })
      .locator("tbody tr")
      .filter({ hasText: name });
  async function reviewSchedule(name: string, runs: string, version: number) {
    let editor = page.getByRole("complementary", {
      name: "Schedule strategy",
      exact: true,
    });
    await expect(editor).toBeVisible();
    await editor.getByLabel("Task name", { exact: true }).fill(name);
    await editor.getByLabel("Interval (minutes)", { exact: true }).fill("1");
    await editor.getByLabel("Runs", { exact: true }).fill(runs);
    const before = await read();
    const response = page.waitForResponse(
      (r) =>
        r.request().method() === "POST" &&
        new URL(r.url()).pathname.endsWith("/agents") &&
        r.request().postDataJSON()?.action === "task-review",
    );
    await editor.getByRole("button", { name: "Review", exact: true }).click();
    const viewed: AgentView = await (await response).json();
    const review = viewed.taskReview!;
    expect(review.strategyPlan?.strategyVersion).toBe(version);
    expect(review.strategyPlan?.strategyId).toBe(before.strategies![0].id);
    expect(review.strategyPlan?.plan.accountId).toBe("a".repeat(32));
    expect(review.strategyPlan?.binding.network).toBe("devnet");
    expect(review.digest).toBeTruthy();
    expect(review.strategyPlan?.digest).toBeTruthy();
    const after = await read();
    expect(after.tasks).toEqual(before.tasks);
    expect(after.sessions).toEqual(before.sessions);
    expect(after.revision).toBe(before.revision);
    editor = page.getByRole("complementary", {
      name: "Schedule strategy",
      exact: true,
    });
    await expect(editor).toContainText(name);
    await expect(editor).toContainText("0.1 SOL");
    await expect(editor).toContainText("30 USDC");
    return review;
  }
  async function createReviewed(review: TaskReview, name: string) {
    const before = await read();
    await page
      .getByRole("complementary", { name: "Schedule strategy", exact: true })
      .getByRole("button", { name: "Create task", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).tasks.length)
      .toBe(before.tasks.length + 1);
    const created = (await read()).tasks.find(
      (t) => !before.tasks.some((old) => old.id === t.id),
    )!;
    expect(created.name).toBe(name);
    expect(created.id).toMatch(/^strategy-task:/);
    expect(created.strategyPlan).toEqual(review.strategyPlan);
    expect(created.ai).toBeUndefined();
    expect(created.news).toBeUndefined();
    expect(created.calendar).toBeUndefined();
    await expect(
      page.getByRole("complementary", { name: "Task details", exact: true }),
    ).toContainText(name);
    const sent = writes
      .filter((w) => w.action === "task-reviewed-create")
      .at(-1)!;
    expect(sent.consentDigest).toBe(review.digest);
    expect(sent.expiresAt).toBe(review.expiresAt);
    expect(sent.strategyVersion).toBe(review.strategyPlan!.strategyVersion);
    return created;
  }
  async function runOnce(taskId: string, name: string) {
    await page
      .getByRole("button", { name: `Run now: ${name}`, exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await read()).runs.filter((r) => r.taskId === taskId).length,
      )
      .toBe(1);
    const v = await read();
    const run = v.runs.find((r) => r.taskId === taskId)!;
    expect(run.outcome).toBe("paper-filled");
    expect(run.paper).toMatchObject({
      quantityAtoms: 100000000,
      priceMicros: 125000000,
      costMicros: 12500000,
      budgetBeforeMicros: 30000000,
      budgetAfterMicros: 17500000,
      market: "SOL/USDC",
      network: "devnet",
      accountId: "a".repeat(32),
    });
    expect(run.paper!.consentDigest).toBe(
      v.tasks.find((t) => t.id === taskId)!.strategyPlan!.digest,
    );
    const details = page.getByRole("complementary", {
      name: "Task details",
      exact: true,
    });
    await expect(details).toContainText("Filled (paper)");
    await expect(details).toContainText("0.1 SOL");
    await expect(details).toContainText("125 USDC");
    await expect(details).toContainText("12.5 USDC");
    await expect(details).toContainText("17.5 USDC");
    return run;
  }

  try {
    const seeded = await execute(
      seed,
      ["-test.run=^TestStrategyBrowserSeed$"],
      undefined,
      { ...process.env, FASED_STRATEGY_SEED_DIR: dir },
    );
    await writeFile(join(evidence, "seed.log"), seeded.stdout + seeded.stderr);
    await start();
    page.on("request", (r) => {
      if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
      if (
        r.method() === "POST" &&
        new URL(r.url()).pathname.endsWith("/agents")
      )
        writes.push(r.postDataJSON());
    });
    await page.route("**/*", (route) =>
      origins.has(new URL(route.request().url()).origin)
        ? route.continue()
        : route.abort(),
    );
    await page.goto(url + "/#agents/tasks");
    const claim = JSON.parse(
      await readFile(join(dir, "claim-code.json"), "utf8"),
    );
    await openOwnerSetup(page, claim.code);
    await page
      .getByLabel("Create owner password", { exact: true })
      .fill(password);
    await page
      .getByRole("button", { name: "Claim workspace", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    await execute(
      binary,
      ["login", "--data-dir", dir, "--password-stdin"],
      password + "\n",
    );

    await command({ action: "session-create", name: "Retained conversation" });
    const session = (await read()).sessions.find(
      (s) => s.name === "Retained conversation",
    )!;
    await command({
      action: "message-add",
      id: session.id,
      text: "Keep this synthetic conversation.",
    });
    await command({
      action: "memory-put",
      text: "Paper operations only.",
      source: "owner",
    });
    await command({
      action: "bot-create",
      name: "Retained paper bot",
      quantity: 1,
      priceCents: 12500,
      budgetCents: 20000,
    });
    const plain = await command({
      action: "task-create",
      name: "Retained reminder",
      text: "Keep this unrelated reminder.",
      intervalSeconds: 86400,
      maxRuns: 1,
    });
    await command({
      action: "task-pause",
      id: plain.tasks.find((t) => t.name === "Retained reminder")!.id,
    });
    const baseline = await read();
    const baselineAccounts = (await cli<AccountsView>(["accounts"])).record;
    expect(baseline.aiRequests).toEqual([]);
    expect(baseline.paperExecutionAvailable).toBe(true);
    await selectProfile();
    await page
      .locator(".tasks-toolbar")
      .getByRole("button", { name: "Refresh", exact: true })
      .click();
    await strategiesTab().click();
    await page
      .getByRole("button", { name: "New strategy", exact: true })
      .click();
    let editor = page.getByRole("complementary", {
      name: "Strategy editor",
      exact: true,
    });
    await editor
      .getByLabel("Strategy name", { exact: true })
      .fill(strategyName);
    await editor
      .getByLabel("Strategy wallet", { exact: true })
      .selectOption("a".repeat(32));
    await editor.getByLabel("Quantity (SOL)", { exact: true }).fill("0.1");
    await editor.getByLabel("Limit (USDC)", { exact: true }).fill("150");
    await editor.getByLabel("Order cap (USDC)", { exact: true }).fill("20");
    await editor.getByLabel("Budget (USDC)", { exact: true }).fill("30");
    await editor
      .getByRole("button", { name: "Save version", exact: true })
      .click();
    await expect.poll(async () => (await read()).strategies?.length).toBe(1);
    let v = await read();
    const strategyId = v.strategies![0].id;
    expect(v.strategies![0].versions[0].plan).toMatchObject({
      accountId: "a".repeat(32),
      network: "devnet",
      quantityAtoms: 100000000,
      limitPriceMicros: 150000000,
      orderBudgetMicros: 20000000,
      totalBudgetMicros: 30000000,
    });
    expect(v.tasks).toEqual(baseline.tasks);
    await strategyRow()
      .getByRole("button", { name: `Schedule: ${strategyName}`, exact: true })
      .click();
    const firstReview = await reviewSchedule(firstName, "3", 1);
    const first = await createReviewed(firstReview, firstName);
    const firstRun = await runOnce(first.id, firstName);
    await page
      .getByRole("button", { name: `Pause: ${firstName}`, exact: true })
      .click();
    await expect
      .poll(
        async () => (await read()).tasks.find((t) => t.id === first.id)?.state,
      )
      .toBe("paused");
    await expect(
      page.getByRole("button", { name: `Resume: ${firstName}`, exact: true }),
    ).toBeEnabled();
    await page
      .getByRole("button", { name: `Resume: ${firstName}`, exact: true })
      .click();
    await expect
      .poll(
        async () => (await read()).tasks.find((t) => t.id === first.id)?.state,
      )
      .toBe("scheduled");

    await strategiesTab().click();
    await strategyRow()
      .getByRole("button", { name: `Edit: ${strategyName}`, exact: true })
      .click();
    editor = page.getByRole("complementary", {
      name: "Strategy editor",
      exact: true,
    });
    await expect(
      editor.getByLabel("Quantity (SOL)", { exact: true }),
    ).toHaveValue("0.1");
    await editor.getByLabel("Limit (USDC)", { exact: true }).fill("160");
    await editor
      .getByRole("button", { name: "Save version", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).strategies![0].versions.length)
      .toBe(2);
    v = await read();
    expect(v.strategies![0].id).toBe(strategyId);
    expect(v.strategies![0].versions[0].plan.limitPriceMicros).toBe(150000000);
    expect(v.strategies![0].versions[0].spentMicros).toBe(12500000);
    expect(v.strategies![0].versions[1].plan.limitPriceMicros).toBe(160000000);
    expect(v.tasks.find((t) => t.id === first.id)?.state).toBe("held");
    await tasksTab().click();
    await expect(
      taskRow(firstName).getByRole("button", { name: "Review", exact: true }),
    ).toBeVisible();
    await expect(
      taskRow(firstName).getByRole("button", {
        name: `Reuse: ${firstName}`,
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      taskRow(firstName).getByRole("button", {
        name: `Resume: ${firstName}`,
        exact: true,
      }),
    ).toHaveCount(0);
    await taskRow(firstName)
      .getByRole("button", { name: "Review", exact: true })
      .click();
    const freshReview = await reviewSchedule(freshName, "2", 2);
    const fresh = await createReviewed(freshReview, freshName);
    expect(fresh.id).not.toBe(first.id);
    const freshRun = await runOnce(fresh.id, freshName);
    expect(freshRun.id).not.toBe(firstRun.id);
    await page
      .getByRole("button", { name: `Pause: ${freshName}`, exact: true })
      .click();
    await expect
      .poll(
        async () => (await read()).tasks.find((t) => t.id === fresh.id)?.state,
      )
      .toBe("paused");

    const saved = await read();
    expect(saved.bots).toEqual(baseline.bots);
    expect(saved.sessions).toEqual(baseline.sessions);
    expect(saved.messages).toEqual(baseline.messages);
    expect(saved.memories).toEqual(baseline.memories);
    expect(saved.tasks.find((t) => t.id === baseline.tasks[0].id)).toEqual(
      baseline.tasks[0],
    );
    expect(saved.strategies![0].versions.map((s) => s.spentMicros)).toEqual([
      12500000, 12500000,
    ]);
    expect(saved.aiRequests).toEqual([]);
    expect(saved.providerCallsEnabled).toBe(false);
    expect(saved.financialExecution).toBe(false);
    expect((await cli<AccountsView>(["accounts"])).record).toEqual(
      baselineAccounts,
    );
    await page
      .getByRole("complementary", { name: "Task details", exact: true })
      .scrollIntoViewIfNeeded();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: join(evidence, `layout-${info.project.name}.png`),
      fullPage: false,
    });
    await writeFile(
      join(evidence, "before-restart.json"),
      JSON.stringify(saved, null, 2),
    );

    const listen = new URL(url).host;
    await stop();
    await start(listen);
    await page.goto(url + "/#agents/tasks");
    const signOut = page.getByRole("button", { name: "Sign out", exact: true });
    const ownerPassword = page.getByLabel("Owner password", { exact: true });
    await expect
      .poll(
        async () =>
          (await signOut.isVisible()) || (await ownerPassword.isVisible()),
      )
      .toBe(true);
    if (!(await signOut.isVisible())) {
      await ownerPassword.fill(password);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
    }
    await expect(signOut).toBeVisible();
    await execute(
      binary,
      ["login", "--data-dir", dir, "--password-stdin"],
      password + "\n",
    );
    await selectProfile();
    const restarted = await read();
    for (const key of [
      "strategies",
      "tasks",
      "runs",
      "bots",
      "memories",
      "sessions",
      "messages",
      "aiRequests",
    ] as const)
      expect(restarted[key]).toEqual(saved[key]);
    expect(new Set(restarted.runs.map((r) => r.id)).size).toBe(2);
    await page
      .getByRole("button", { name: `Result: ${freshName}`, exact: true })
      .click();
    const result = page.getByRole("article", {
      name: `Run ${freshRun.id}`,
      exact: true,
    });
    await expect(result).toContainText("Filled (paper)");
    await expect(result).toContainText("17.5 USDC");
    const foreign = await cli<AgentView>([
      "agents",
      "--profile",
      "news-researcher",
    ]);
    expect(foreign.strategies ?? []).toEqual([]);
    expect(foreign.tasks).toEqual([]);
    expect(foreign.runs).toEqual([]);
    expect(foreign.memories).toEqual([]);
    expect(external).toEqual([]);
    await stop();
    await rm(dir, { recursive: true, force: true });
    await writeFile(
      join(evidence, "receipt.json"),
      JSON.stringify(
        {
          status: "PASS",
          profileId,
          strategyId,
          firstTaskId: first.id,
          freshTaskId: fresh.id,
          runIds: restarted.runs.map((r) => r.id),
          source: "compiled paper fixture:125USDC/SOL",
          external,
          writes,
          gates: {
            providerCallsEnabled: saved.providerCallsEnabled,
            financialExecution: saved.financialExecution,
            aiRequests: saved.aiRequests.length,
          },
          boundaries: [
            "No mining/wallet/update action attempted",
            "Compiled fixture price; no provider/model connection",
          ],
          state:
            "disposable reconstructible fixture; removed after successful capture",
        },
        null,
        2,
      ),
    );
    passed = true;
  } finally {
    await stop();
    if (!passed) {
      const snapshot = join(evidence, "state");
      await writeFile(
        join(evidence, "failure.json"),
        JSON.stringify(
          {
            status: "FAIL",
            state: dir,
            persistentState: snapshot,
            external,
            cleanup:
              "Core stopped; copying exact state before short runtime cleanup",
          },
          null,
          2,
        ),
      );
      // Preserve the short directory if copying fails; never remove the sole
      // diagnostic state. The persistent startup log was written by stop().
      await cp(dir, snapshot, {
        recursive: true,
        force: false,
        errorOnExist: true,
      });
      await rm(dir, { recursive: true, force: true });
      await writeFile(
        join(evidence, "failure.json"),
        JSON.stringify(
          {
            status: "FAIL",
            state: snapshot,
            external,
            cleanup:
              "Core stopped; exact state retained; short runtime removed",
          },
          null,
          2,
        ),
      );
    }
  }
});
