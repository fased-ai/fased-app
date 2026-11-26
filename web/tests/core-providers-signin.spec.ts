import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import {
  installCoreFixture,
  captureScreenshot,
  openProviders,
  exactReplyReview,
  openConversationList,
  revealControl,
  openAISchedules,
  exec,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  output,
  start,
  stop,
  privateCLI,
  fixtureProviderSelection,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("OpenAI test connection shares encrypted storage, bound consent and restart lock across UI and CLI", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/settings");
  await openProviders(page);
  const panel = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  const key = "fased-test-browser-disposable-key";
  const passphrase = "disposable-browser-vault-passphrase";
  await panel.getByRole("button", { name: "API key", exact: true }).click();
  await revealControl(page, panel.getByLabel("API key"), "");
  await panel.getByLabel("API key").fill(key);
  await revealControl(page, panel.getByLabel("Vault passphrase"), "");
  await panel.getByLabel("Vault passphrase").fill(passphrase);
  await panel.getByRole("button", { name: "Save" }).click();
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await panel.getByRole("button", { name: "Edit", exact: true }).click();
  await expect(panel.getByLabel("API key")).toHaveValue("");
  await expect(panel.getByLabel("Vault passphrase")).toHaveValue("");
  await panel
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  let connection = await privateCLI(["ai-connection"]);
  expect(connection.locked).toBe(false);
  expect(connection.liveCallsEnabled).toBe(false);
  const firstRef = connection.ref;
  const ciphertext = await readFile(join(dir, "ai-credentials.json"), "utf8");
  expect(ciphertext).not.toContain(key);
  expect(ciphertext).not.toContain(passphrase);
  expect(connection.credentialMode).toBe("test");
  await expect(
    privateCLI(["ai-connection", "--request-stdin"], {
      action: "save",
      credentialMode: "openai",
      ref: firstRef,
      key: "disposable-api-input-not-issued-by-any-provider",
      passphrase,
    }),
  ).rejects.toThrow();
  expect(await readFile(join(dir, "ai-credentials.json"), "utf8")).toBe(
    ciphertext,
  );

  await page.goto(url + "/#agents/review");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page.getByLabel("Conversation name").fill("Connected fixture review");
  await page.getByRole("button", { name: "Create conversation" }).click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(firstRef);
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Analyze fixed paper inputs using the test connection");
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send" }).click();
  await review.getByText("Details", { exact: true }).click();
  await expect(review).toContainText(firstRef);
  await revealControl(
    page,
    review.getByLabel("I reviewed this context and these limits"),
    "",
  );
  await review.getByLabel("I reviewed this context and these limits").check();
  await review.getByRole("button", { name: "Run preview" }).click();
  await expect(review.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  let agents = await privateCLI(["agents"]);
  expect(agents.aiRequests[0].connectionRef).toBe(firstRef);
  expect(agents.aiRequests[0].heldMicrousd).toBe(0);
  expect(JSON.stringify(agents)).not.toContain(key);
  expect(
    await page.evaluate(() => JSON.stringify([localStorage, sessionStorage])),
  ).not.toContain(key);
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/settings");
  await openProviders(page);
  await expect(panel.getByRole("status")).toContainText("Locked");
  connection = await privateCLI(["ai-connection"]);
  expect(connection.ref).toBe(firstRef);
  expect(connection.locked).toBe(true);
  connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "unlock",
    ref: firstRef,
    passphrase,
  });
  expect(connection.locked).toBe(false);
  await page.reload();
  await openProviders(page);
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await panel.getByText("Edit", { exact: true }).click();
  await panel.getByRole("button", { name: "Lock", exact: true }).click();
  await expect(panel.getByRole("status")).toContainText("Locked");
  expect((await privateCLI(["ai-connection"])).locked).toBe(true);
  connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    ref: firstRef,
    key: key + "-replacement",
    passphrase,
  });
  expect(connection.ref).not.toBe(firstRef);
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "remove",
    ref: connection.ref,
    passphrase,
  });
  await page.reload();
  await openProviders(page);
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  agents = await privateCLI(["agents"]);
  expect(agents.aiRequests[0].state).toBe("completed");
  expect(agents.providerCallsEnabled).toBe(true);
  expect(output).not.toContain(key);
  await captureScreenshot(page, {
    path: `.evidence/p5-openai-connection/${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("optional provider fixture requires scoped disclosure and budgets, preserves review and never creates a task", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/review");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page
    .getByLabel("Conversation name")
    .fill("Provider fixture conversation");
  await page.getByRole("button", { name: "Create conversation" }).click();
  await expect(
    page.getByRole("heading", {
      name: "Provider fixture conversation",
      exact: true,
    }),
  ).toBeVisible();
  const panel = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(page, panel.getByLabel("Message", { exact: true }), "");
  await panel
    .getByLabel("Message", { exact: true })
    .fill("Compare a bounded paper strategy");
  await exactReplyReview(page);
  await panel.getByRole("button", { name: "Send" }).click();
  await expect(panel.getByText("prepared", { exact: true })).toBeVisible();
  const run = panel.getByRole("button", { name: "Run preview" });
  await expect(run).toBeDisabled();
  await panel.getByText("Details", { exact: true }).click();
  await expect(panel.locator("pre")).toContainText('"store":false');
  await revealControl(
    page,
    panel.getByLabel("I reviewed this context and these limits"),
    "",
  );
  await panel.getByLabel("I reviewed this context and these limits").check();
  await run.click();
  await expect(panel.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  const card = panel.getByRole("region", { name: "Review paper journal" });
  await card.getByRole("button", { name: "Keep draft" }).click();
  await expect(card).toContainText("Saved");
  const view = await privateCLI(["agents"]);
  expect(view.aiRequests).toHaveLength(1);
  expect(
    view.aiRequests[0].cards.find(
      (c: { title: string; review: string }) =>
        c.title === "Review paper journal",
    )?.review,
  ).toBe("kept");
  expect(view.tasks).toHaveLength(0);
  expect(view.aiRequests[0].heldMicrousd).toBe(0);
  expect(view.providerCallsEnabled).toBe(false);
  const request = {
    revision: view.revision,
    action: "ai-prepare",
    profileId: view.profileId,
    id: view.sessions[0].id,
    text: "Cancelled preview",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  };
  const next = await privateCLI(["agents", "--request-stdin"], request);
  await privateCLI(["agents", "--request-stdin"], {
    revision: next.revision,
    action: "ai-cancel",
    profileId: view.profileId,
    id: next.aiRequests[1].id,
  });
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/review");
  await expect(page.getByText("completed", { exact: true })).toBeVisible();
  await expect(page.getByText("cancelled", { exact: true })).toBeVisible();
  const saved = await privateCLI(["agents"]);
  expect(saved.aiFixtureAvailable).toBe(false);
  expect(saved.aiRequests[0].cards[0].review).toBe("kept");
  expect(saved.tasks).toHaveLength(0);
  const profileName = saved.profiles.find(
    (p: { id: string }) => p.id === saved.profileId,
  ).name;
  const sessionName = "Provider fixture conversation";
  const question = saved.aiRequests[0].prompt;
  const answer = "No model, market feed or wallet was contacted.";
  const checkSearch = async () => {
    for (const query of [question.toUpperCase(), answer.toUpperCase()]) {
      await revealControl(page, page.getByLabel("Search conversations"), "");
      await page.getByLabel("Search conversations").fill(query);
      await expect(
        page.locator(".agent-session-list").getByRole("button", {
          name: sessionName,
          exact: true,
        }),
      ).toBeVisible();
      const hit = await privateCLI([
        "agents",
        "--profile",
        saved.profileId,
        "--search",
        query,
      ]);
      expect(hit.sessions.map((s: { id: string }) => s.id)).toEqual([
        saved.aiRequests[0].sessionId,
      ]);
      const other = await privateCLI([
        "agents",
        "--profile",
        "news-researcher",
        "--search",
        query,
      ]);
      expect(other.sessions).toHaveLength(0);
      expect(other.aiRequests).toHaveLength(0);
      expect(hit.providerCallsEnabled).toBe(true);
    }
  };
  await checkSearch();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  // Profile loading remounts the conversation panel. Wait for that response
  // before opening its mobile disclosure, rather than opening the old panel.
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(
    "news-researcher",
  );
  await revealControl(page, page.getByLabel("Search conversations"), "");
  await page.getByLabel("Search conversations").fill(answer);
  await expect(page.locator(".agent-session-list button")).toHaveCount(0);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: profileName });
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(
    saved.profileId,
  );
  await page.evaluate(() => {
    window.location.hash = "agents/settings";
  });
  await revealControl(page, page.getByLabel("Provider", { exact: true }), "");
  // Switch the current model preference without changing retained history.
  const beforeProvider = await privateCLI([
    "agents",
    "--profile",
    saved.profileId,
  ]);
  await privateCLI(["agents", "--request-stdin"], {
    action: "profile-update",
    revision: beforeProvider.revision,
    profileId: saved.profileId,
    name: profileName,
    provider: "openai",
    model: "gpt-6.2-sol",
  });
  await page.reload();
  await revealControl(page, page.getByLabel("Provider", { exact: true }), "");
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "openai",
  );
  await revealControl(page, page.getByLabel("Model", { exact: true }), "");
  await expect(page.getByLabel("Model", { exact: true })).toHaveValue(
    "gpt-6.2-sol",
  );
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect
    .poll(async () => {
      const current = await privateCLI([
        "agents",
        "--profile",
        saved.profileId,
      ]);
      return current.profiles.find(
        (p: { id: string }) => p.id === saved.profileId,
      ).provider;
    })
    .toBe("openai");
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await checkSearch();
  await revealControl(page, page.getByLabel("Search conversations"), "");
  await page.getByLabel("Search conversations").fill("no-such-phrase");
  await expect(page.locator(".agent-session-list button")).toHaveCount(0);
  await revealControl(page, page.getByLabel("Search conversations"), "");
  await page.getByLabel("Search conversations").fill("");
  await mkdir(".evidence/p5-ai-search", { recursive: true });
  await captureScreenshot(page, {
    path: `.evidence/p5-ai-search/search-${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("OpenAI model admission binds token and tariff evidence across UI CLI tasks and restart", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    key: "fased-test-model-admission-browser-key",
    passphrase: "disposable-admission-vault-passphrase",
  });
  expect(connection.testOnly).toBe(true);
  expect(connection.liveCallsEnabled).toBe(false);
  const registry = JSON.parse(
    await readFile("internal/core/provider_models.json", "utf8"),
  );
  const currentModel = registry.models.find(
    (m: { id: string }) => m.id === connection.admissionPolicy.model,
  );
  expect(currentModel).toBeTruthy();
  expect(connection.admissionPolicy).toMatchObject({
    inputMicrousdPerMillion: currentModel.input,
    cachedMicrousdPerMillion: currentModel.cached,
    outputMicrousdPerMillion: currentModel.output,
  });
  const price = (n: number) => `$${(n / 1000000).toFixed(2)}`;
  await page.goto(url + "/#agents/review");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page.getByLabel("Conversation name").fill("Admitted model review");
  await page.getByRole("button", { name: "Create conversation" }).click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(connection.ref);
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Model",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Model", exact: true, includeHidden: true })
    .selectOption(connection.admissionPolicy.model);
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Review the admitted model using controlled data");
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send" }).click();
  // An intentionally expired compiled tariff must refuse, including on future CI dates.
  if (Date.now() >= connection.admissionPolicy.expiresAt * 1000) {
    await expect(
      page.getByText(/model, token or tariff policy unavailable/),
    ).toBeVisible();
    expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
    return;
  }
  await revealControl(
    page,
    review.getByText("Model and tariff admission", { exact: true }),
  );
  await review.getByText("Model and tariff admission", { exact: true }).click();
  await expect(review).toContainText(
    `input ${price(currentModel.input)}, cached input ${price(currentModel.cached)}, output ${price(currentModel.output)}`,
  );
  await expect(review).toContainText("Live execution disabled");
  await revealControl(
    page,
    review.getByLabel("I reviewed this context and these limits"),
    "",
  );
  await review.getByLabel("I reviewed this context and these limits").check();
  await review.getByRole("button", { name: "Run preview" }).click();
  await expect(review.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  let view = await privateCLI(["agents"]);
  const manual = view.aiRequests[0];
  expect(manual.model).toBe(connection.admissionPolicy.model);
  expect(manual.admission.inputTokens).toBe(256);
  expect(manual.admission.generationAttempted).toBe(true);
  expect(manual.admission.wireSha256).toMatch(/^[a-f0-9]{64}$/);
  expect(manual.inputTokens).toBe(256);
  expect(manual.outputTokens).toBe(128);
  expect(manual.cachedInputTokens || 0).toBe(0);
  expect(manual.spentMicrousd).toBe(
    Math.ceil((256 * currentModel.input + 128 * currentModel.output) / 1000000),
  );
  expect(manual.heldMicrousd).toBe(0);
  expect(
    (await privateCLI(["agents", "--profile", "news-researcher"])).aiRequests,
  ).toHaveLength(0);
  await page.goto(url + "/#agents/tasks");
  await openAISchedules(page);
  const scheduled = page.getByRole("region", {
    name: "Scheduled AI steps",
    exact: true,
  });
  await revealControl(
    page,
    scheduled.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await scheduled
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(connection.ref);
  await revealControl(
    page,
    scheduled.getByRole("combobox", {
      name: "Model",
      exact: true,
      includeHidden: true,
    }),
  );
  await scheduled
    .getByRole("combobox", { name: "Model", exact: true, includeHidden: true })
    .selectOption(connection.admissionPolicy.model);
  await revealControl(
    page,
    scheduled.getByLabel("AI task title", { exact: true }),
    "",
  );
  await scheduled
    .getByLabel("AI task title", { exact: true })
    .fill("Admitted UI task");
  await revealControl(
    page,
    scheduled.getByLabel("AI analysis prompt", { exact: true }),
    "",
  );
  await scheduled
    .getByLabel("AI analysis prompt", { exact: true })
    .fill("Review the frozen controlled context");
  await revealControl(
    page,
    scheduled.getByLabel("AI run count", { exact: true }),
    "",
  );
  await scheduled.getByLabel("AI run count", { exact: true }).fill("1");
  await scheduled
    .getByRole("button", { name: "Prepare AI task review" })
    .click();
  const task = page.getByRole("article", {
    name: "AI task Admitted UI task",
    exact: true,
  });
  await expect(task).toBeVisible();
  await task.getByText("Review AI task disclosure", { exact: true }).click();
  await expect(task).toContainText(connection.admissionPolicy.model);
  await revealControl(
    page,
    task.getByLabel("Allow this frozen context, schedule and limits"),
    "",
  );
  await task
    .getByLabel("Allow this frozen context, schedule and limits")
    .check();
  await task.getByRole("button", { name: "Authorize test schedule" }).click();
  await expect(
    task.getByRole("button", { name: "Evaluate now" }),
  ).toBeEnabled();
  view = await privateCLI(["agents"]);
  const access = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "task-access-review",
    ref: connection.ref,
    revision: view.revision,
    profileId: view.profileId,
    taskIds: [view.tasks[0].id],
    authorizationSeconds: 600,
  });
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "task-access-allow",
    ref: connection.ref,
    allowance: access.accessReview,
    consentDigest: access.accessReview.id,
    passphrase: "disposable-admission-vault-passphrase",
  });
  await task.getByRole("button", { name: "Evaluate now" }).click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].state)
    .toBe("completed");
  view = await privateCLI(["agents"]);
  expect(view.aiRequests).toHaveLength(2);
  expect(view.aiRequests[1].admission.state).toBe("admitted");
  expect(view.runs[0].aiRequestId).toBe(view.aiRequests[1].id);
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  view = await privateCLI(["agents"]);
  expect(view.aiRequests[0].admission).toEqual(manual.admission);
  expect(view.aiRequests[0].policy).toEqual(manual.policy);
  expect((await privateCLI(["ai-connection"])).locked).toBe(true);
  expect(view.providerCallsEnabled).toBe(true);
  expect(view.financialExecution).toBe(false);
  await page.goto(url + "/#agents/review");
  await revealControl(
    page,
    page.getByText("Model and tariff admission", { exact: true }).first(),
  );
  await page
    .getByText("Model and tariff admission", { exact: true })
    .first()
    .click();
  await expect(
    page.getByRole("region", { name: "Optional AI request review" }),
  ).toContainText("Counted 256 input tokens");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await mkdir(resolve(".evidence/p5-admission"), { recursive: true });
  await captureScreenshot(page, {
    path: resolve(".evidence/p5-admission", test.info().project.name + ".png"),
    fullPage: true,
  });
});

test("reviewed task credential allowance is scoped, finite and revocable in Providers and CLI", async ({
  page,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/review");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-task-access-passphrase";
  const connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    key: "fased-test-browser-task-access-key",
    passphrase,
  });
  let v = await privateCLI(["agents"]);
  const change = async (c: unknown) => {
    v = await privateCLI(["agents", "--request-stdin"], {
      profileId: v.profileId,
      revision: v.revision,
      ...(c as object),
    });
    return v;
  };
  await change({ action: "session-create", name: "Task access source" });
  const session = v.sessions[0].id;
  await change({
    action: "task-ai-prepare",
    id: session,
    name: "Selected fixture reminder",
    text: "Review the fixed context",
    connectionRef: connection.ref,
    intervalSeconds: 3600,
    maxRuns: 2,
    authorizationSeconds: 7200,
    totalBudgetMicrousd: 60000,
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const task = v.tasks[0];
  await change({
    action: "task-ai-authorize",
    id: task.id,
    consentDigest: task.ai.digest,
  });
  await page.goto(url + "/#agents/settings");
  await page.reload();
  await openProviders(page);
  const provider = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await provider.getByText("Background access", { exact: true }).click();
  await provider
    .getByRole("checkbox", { name: task.name, exact: true })
    .check();
  await provider
    .getByRole("checkbox", { name: "Continue after sign-out", exact: true })
    .check();
  const persistent = process.platform === "linux";
  if (persistent)
    await provider
      .getByRole("checkbox", { name: "Continue after restart", exact: true })
      .check();
  await provider
    .getByRole("button", { name: "Review access", exact: true })
    .click();
  await expect(
    provider.getByText(
      persistent
        ? "Selected tasks continue after restart until expiry. Lock or revoke stops access."
        : "Selected tasks continue after sign-out until expiry. Lock, revoke or restart stops access.",
      { exact: true },
    ),
  ).toBeVisible();
  expect((await privateCLI(["ai-connection"])).taskAccess).toBeUndefined();
  await provider
    .getByLabel("Vault passphrase", { exact: true })
    .first()
    .fill(passphrase);
  await provider
    .getByRole("button", { name: "Allow task access", exact: true })
    .click();
  await expect
    .poll(
      async () => (await privateCLI(["ai-connection"])).taskAccess?.tasks[0].id,
    )
    .toBe(task.id);
  expect(
    (await privateCLI(["ai-connection"])).taskAccess.continueOnSignOut,
  ).toBe(true);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page.getByRole("button", { name: "Use password", exact: true }).click();
  await expect(
    page.getByLabel("Owner password", { exact: true }),
  ).toBeVisible();
  const afterSignOut = await privateCLI(["ai-connection"]);
  expect(afterSignOut.locked).toBe(true);
  expect(afterSignOut.taskAccess.continueOnSignOut).toBe(true);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await openProviders(page);
  await provider.getByText("Background access", { exact: true }).click();
  await change({ action: "task-run", id: task.id });
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].runs, {
      timeout: 12000,
    })
    .toBe(1);
  await expect
    .poll(
      async () =>
        (await privateCLI(["ai-connection"])).taskAccess?.usedRequests,
    )
    .toBe(1);
  if (persistent) {
    const before = (await privateCLI(["ai-connection"])).taskAccess;
    const listen = new URL(url).host;
    await stop();
    await start(true, listen);
    await page.reload();
    const recovered = await privateCLI(["ai-connection"]);
    expect(recovered.locked).toBe(true);
    expect(recovered.taskAccess.id).toBe(before.id);
    expect(recovered.taskAccess.usedRequests).toBe(1);
    expect(recovered.taskAccess.allocatedMicrousd).toBe(
      before.allocatedMicrousd,
    );
    await openProviders(page);
    await provider.getByText("Background access", { exact: true }).click();
    await expect(
      provider.getByText("After restart", { exact: true }),
    ).toBeVisible();
  }
  await provider.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).taskAccess)
    .toBeUndefined();
  v = await privateCLI(["agents"]);
  await change({ action: "task-run", id: task.id });
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].state, {
      timeout: 12000,
    })
    .toBe("held");
  const held = await privateCLI(["agents"]);
  expect(held.tasks[0].runs).toBe(2);
  expect(
    held.runs.filter((r: any) => r.strategy === "ai-step-v1/reminder-v1"),
  ).toHaveLength(1);
  expect(held.runs.at(-1).outcome).toBe("ai-cancelled");
  expect(held.aiRequests.at(-1).spentMicrousd).toBe(0);
  const status = await privateCLI(["ai-connection"]);
  expect(status.liveCallsEnabled).toBe(false);
  await captureScreenshot(page, {
    path: `.evidence/provider-task-access-${test.info().project.name}.png`,
    fullPage: false,
  });
});

test("provider editor follows core credential admission without enabling live calls", async ({
  page,
}) => {
  await stop();
  await start();
  await page.goto(url + "/#overview");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const status = await page.request.get(url + "/api/v1/ai/connection", {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(status.ok()).toBe(true);
  const connection = await status.json();
  expect(connection.liveCallsEnabled).toBe(true);
  expect(connection.credentialModes).toContain("openai");
  let saved: Record<string, unknown> | undefined;
  // UI-only capability injection. POST is intercepted and never reaches a vault,
  // model endpoint or live admission path.
  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "POST") {
      saved = route.request().postDataJSON();
      await route.fulfill({
        json: {
          credentialMode: "openai",
          credentialModes: ["openai"],
          osVaultAvailable: false,
          osProtected: false,
          ref: "",
          model: "gpt-6.1-terra",
          locked: true,
          liveCallsEnabled: true,
        },
      });
      return;
    }
    // Reuse the immutable core status; refreshing the editor can cancel GETs.
    // Do not leave a second upstream fetch racing that UI cancellation.
    await route.fulfill({
      json: {
        ...connection,
        credentialModes: ["openai"],
        osVaultAvailable: false,
        osProtected: false,
      },
    });
  });
  await page.getByRole("button", { name: "Setup", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Instance", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await editor.getByRole("button", { name: "API key", exact: true }).click();
  await revealControl(
    page,
    editor.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await editor
    .getByLabel("API key", { exact: true })
    .fill("disposable-api-input-not-issued-by-any-provider");
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-admission-vault-password");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect.poll(() => saved?.credentialMode).toBe("openai");
  await expect(editor.getByLabel("API key", { exact: true })).toHaveValue("");
  await page.unrouteAll({ behavior: "wait" });
  await page.reload();
  await expect(page).toHaveURL(/#providers$/);
  await expect(editor).toBeVisible();
  await editor.getByRole("button", { name: "API key", exact: true }).click();
  await revealControl(page, editor.getByLabel("API key", { exact: true }), "");
  await expect(editor.getByLabel("API key", { exact: true })).toBeVisible();
  await expect(editor.getByRole("status")).toContainText("No connection saved");
});

test("multiple accounts keep scheduled agent selection and refuse a stale provider editor", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/tasks");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-multi-account-password";
  const a = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Primary",
    key: "fased-test-primary-account-key",
    passphrase,
  });
  const b = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    name: "Research",
    key: "fased-test-research-account-key",
    passphrase,
  });
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  await command({
    action: "profile-update",
    name: "Analyst",
    provider: "openai",
    model: "gpt-6.2-sol",
    accountRef: b.ref,
  });
  await command({ action: "session-create", name: "Account schedule" });
  await page.reload();
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  await openAISchedules(page);
  const perCall = page.getByLabel("Per-call cost cap (micro USD)", {
    exact: true,
  });
  const total = page.getByLabel("AI total cost cap (micro USD)", {
    exact: true,
  });
  await revealControl(page, perCall, "");
  await expect(perCall).toHaveValue("58240");
  await perCall.fill("100");
  await total.fill("200");
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "models-refresh",
    ref: b.ref,
  });
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect(perCall).toHaveValue("100");
  await expect(total).toHaveValue("200");
  await perCall.fill("58240");
  await total.fill("116480");
  await revealControl(page, page.getByLabel("AI task title"), "");
  await page.getByLabel("AI task title").fill("Research account only");
  await page
    .getByLabel("AI analysis prompt")
    .fill("Summarize the saved fixture context");
  await page
    .getByRole("button", { name: "Prepare AI task review", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(1);
  expect((await read()).tasks[0].ai.connectionRef).toBe(b.ref);
  expect((await read()).tasks[0].ai.model).toBe("gpt-6.2-sol");
  const retainedTask = (await read()).tasks[0];
  const refreshed = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "models-refresh",
    ref: b.ref,
  });
  expect(refreshed.catalog.policy).toBe("latest-only");
  expect(
    refreshed.catalog.models
      .filter((m: any) => m.selectable)
      .map((m: any) => m.id),
  ).toEqual(["gpt-6.1-terra", "gpt-6.2-sol", "gpt-6.2-astra"]);
  expect((await read()).tasks[0]).toEqual(retainedTask);
  expect((await read()).tasks[0].state).toBe("review");
  await page.goto(url + "/#agents/settings");
  await page
    .getByRole("region", { name: "Agent settings", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await editor.locator(`[data-account="${b.ref}"]`).click();
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "remove",
    ref: b.ref,
    passphrase,
  });
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect(editor.getByRole("alert")).toContainText(
    "Previous connection unavailable",
  );
  await revealControl(
    page,
    editor.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await expect(
    editor.getByRole("button", { name: "Lock", exact: true }),
  ).toBeDisabled();
  expect((await privateCLI(["ai-connection"])).ref).toBe(a.ref);
  expect((await privateCLI(["ai-connection"])).locked).toBe(false);
  expect((await read()).tasks[0].ai.connectionRef).toBe(b.ref);
});

for (const location of ["local", "remote"]) {
  test(`provider sign-in ${location} keeps its account route through configured Chat`, async ({
    page,
  }) => {
    test.setTimeout(90000);
    await stop();
    await start(true);
    // Runtime fixtures stay standalone/Local. Simulate only the public SSH
    // Remote descriptor to verify its UI consumer; this is not VPS acceptance.
    if (location === "remote")
      await page.route("**/api/v1/health", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.connection = { location: "remote", transport: "loopback" };
        await route.fulfill({ response, json: body });
      });
    const external: string[] = [];
    const signInStarts: { remote?: boolean }[] = [];
    page.on("request", (r) => {
      const host = new URL(r.url()).hostname;
      if (host !== "127.0.0.1" && host !== "localhost") external.push(r.url());
      if (
        r.method() === "POST" &&
        new URL(r.url()).pathname === "/api/v1/ai/connection"
      ) {
        const body = r.postDataJSON();
        if (body.action === "sign-in-start") signInStarts.push(body);
      }
    });
    await page.goto(url + "/#agents/settings");
    const claim = JSON.parse(
      await readFile(join(dir, "claim-code.json"), "utf8"),
    );
    await openOwnerSetup(page, claim.code);
    await page.getByLabel("Create owner password").fill(fixturePassword);
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
    login.stdin!.end(fixturePassword + "\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    const passphrase = "disposable-sign-in-ui-vault";
    const original = await privateCLI(["ai-connection", "--request-stdin"], {
      action: "save",
      key: "fased-test-sign-in-ui-primary",
      passphrase,
    });
    const before = await privateCLI(["agents"]);
    await page.goto(url + "/#agents/review");
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    const creation = page.getByRole("dialog", {
      name: "New agent",
      exact: true,
    });
    await creation.getByLabel("Name", { exact: true }).fill("Plan researcher");
    await creation
      .getByRole("button", { name: "Connect provider", exact: true })
      .click();
    const editor = page.getByRole("region", { name: "Providers", exact: true });
    await editor
      .getByRole("button", { name: "Add provider", exact: true })
      .click();
    await editor.getByRole("button", { name: "Sign in", exact: true }).click();
    await editor
      .getByRole("button", { name: "Continue with ChatGPT", exact: true })
      .click();
    if (location === "remote") expect(signInStarts.at(-1)?.remote).toBe(true);
    const link = editor.getByRole("link", {
      name: "Open sign-in",
      exact: true,
    });
    await expect(link).toBeVisible();
    if (location === "remote") {
      await editor
        .getByLabel("Redirect URL", { exact: true })
        .fill((await link.getAttribute("href"))!);
    } else {
      const opened = page.waitForEvent("popup");
      await link.click();
      const popup = await opened;
      await popup.waitForLoadState("domcontentloaded");
      await popup.close();
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    }
    await editor
      .getByLabel("Vault passphrase", { exact: true })
      .fill(passphrase);
    await expect(
      editor.getByRole("button", { name: "Finish", exact: true }),
    ).toBeEnabled();
    await editor.getByRole("button", { name: "Finish", exact: true }).click();
    await expect
      .poll(
        async () =>
          ((await privateCLI(["ai-connection"])).connections ?? []).length,
      )
      .toBe(2);
    const accounts = await privateCLI(["ai-connection"]);
    expect(accounts.ref).toBe(original.ref);
    const account = accounts.connections.find(
      (a: any) => a.authMethod === "sign-in",
    );
    expect(account).toMatchObject({
      provider: "openai",
      authMethod: "sign-in",
      planUse: true,
      liveCallsEnabled: false,
    });
    await expect(
      editor.locator(`[data-account="${account.ref}"] small`),
    ).toHaveText("Sign in");
    expect((await privateCLI(["agents"])).revision).toBe(before.revision);
    await page.getByRole("button", { name: "Back", exact: true }).click();
    await expect(creation).toBeVisible();
    await expect(creation.getByLabel("Name", { exact: true })).toHaveValue(
      "Plan researcher",
    );
    await creation
      .getByLabel("Account", { exact: true })
      .selectOption(account.ref);
    await creation
      .getByLabel("Model", { exact: true })
      .selectOption("gpt-6.1-terra");
    await creation
      .getByRole("button", { name: "Create agent", exact: true })
      .click();
    await expect(creation).not.toBeVisible();
    const profile = (await privateCLI(["agents"])).profiles.find(
      (p: any) => p.name === "Plan researcher",
    );
    expect(profile.connectionRef).toBe(account.ref);
    const read = () => privateCLI(["agents", "--profile", profile.id]);
    // Model a completed worker journal arriving before the browser's next
    // snapshot. CLI completion alone must not make a stale UI ready to write.
    let delayCompletedView = location === "local";
    await page.route("**/api/v1/agents?**", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      if (
        delayCompletedView &&
        body.profileId === profile.id &&
        body.aiRequests?.some((q: { state: string }) => q.state === "completed")
      )
        await new Promise((resolve) => setTimeout(resolve, 1000));
      await route.fulfill({ response, json: body });
    });
    await revealControl(
      page,
      page.getByLabel("Conversation name", { exact: true }),
      "Conversation name",
    );
    await page
      .getByLabel("Conversation name", { exact: true })
      .fill("New conversation");
    await page
      .getByRole("button", { name: "Create conversation", exact: true })
      .click();
    await page
      .getByLabel("Message", { exact: true })
      .fill("Summarize this synthetic note");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const replyReview = page.getByRole("dialog", {
      name: "Allow replies",
      exact: true,
    });
    await expect(
      replyReview
        .locator(".review-list > div")
        .filter({ has: page.getByText("Provider", { exact: true }) }),
    ).toContainText("OpenAI");
    await expect(
      replyReview
        .locator(".review-list > div")
        .filter({ has: page.getByText("Usage", { exact: true }) }),
    ).toContainText("OpenAI");
    await expect(
      replyReview.getByText("App limits", { exact: true }),
    ).toBeVisible();
    await expect(
      replyReview.getByText("Maximum cost", { exact: true }),
    ).toHaveCount(0);
    await replyReview
      .getByRole("button", { name: "Allow and send", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).aiRequests.at(-1)?.state)
      .toBe("completed");
    const reply = (await read()).aiRequests.at(-1);
    // The backend may finish between browser polls. Await the UI's completed
    // view before creating a task; never rebase/retry a stale mutation.
    await expect(
      page.getByRole("button", { name: "Stop", exact: true }),
    ).toHaveCount(0);
    await expect(page.locator(".ai-answer").last()).toHaveText(reply.answer);
    delayCompletedView = false;
    expect(reply).toMatchObject({
      connectionRef: account.ref,
      costBasis: "chatgpt-plan-reported-usage",
      spentMicrousd: 0,
      heldMicrousd: 0,
      limits: { budgetMicrousd: 0 },
      policy: { wireVersion: "openai-signin-v2" },
    });
    expect(reply.inputTokens).toBeGreaterThan(0);
    expect(reply.outputTokens).toBeGreaterThan(0);
    if (location === "local") {
      await page.goto(url + "/#agents/tasks");
      await openAISchedules(page);
      const schedule = page.getByRole("region", {
        name: "Scheduled AI steps",
        exact: true,
      });
      await revealControl(page, schedule.getByLabel("AI task title"), "");
      await schedule.getByLabel("AI task title").fill("Plan summary");
      await schedule
        .getByLabel("AI context conversation")
        .selectOption((await read()).sessions[0].id);
      await schedule
        .getByLabel("AI analysis prompt")
        .fill("Summarize one fictional event");
      await schedule.getByLabel("AI run count").fill("1");
      await expect(
        schedule.getByLabel("Per-call cost cap (micro USD)"),
      ).toHaveCount(0);
      await schedule
        .getByRole("button", { name: "Prepare AI task review", exact: true })
        .click();
      const task = schedule.getByRole("article", {
        name: "AI task Plan summary",
        exact: true,
      });
      await expect(task).toBeVisible();
      const savedTask = (await read()).tasks.at(-1);
      expect(savedTask.ai).toMatchObject({
        connectionRef: account.ref,
        budgetMicrousd: 0,
        limits: { budgetMicrousd: 0 },
        policy: { wireVersion: "openai-signin-v2" },
      });
      await task
        .getByText("Review AI task disclosure", { exact: true })
        .click();
      await task
        .getByLabel("Allow this frozen context, schedule and limits")
        .check();
      await task
        .getByRole("button", { name: "Authorize task", exact: true })
        .click();
      await expect(task.getByText("scheduled", { exact: true })).toBeVisible();
      await page.goto(url + "/#agents/settings");
      await page
        .getByRole("region", { name: "Agent settings", exact: true })
        .getByRole("button", { name: "Providers", exact: true })
        .click();
      const accessEditor = page.getByRole("region", {
        name: "Providers",
        exact: true,
      });
      await accessEditor.locator(`[data-account="${account.ref}"]`).click();
      await accessEditor
        .getByRole("button", { name: "Background access", exact: true })
        .click();
      const background = accessEditor.getByRole("dialog", {
        name: "Background access",
        exact: true,
      });
      await background.getByLabel("Plan summary", { exact: true }).check();
      await background
        .getByRole("button", { name: "Review access", exact: true })
        .click();
      await background
        .getByLabel("Vault passphrase", { exact: true })
        .fill(passphrase);
      await background
        .getByRole("button", { name: "Allow task access", exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await privateCLI(["ai-connection"])).connections?.find(
              (c: any) => c.ref === account.ref,
            )?.taskAccess?.budgetMicrousd,
        )
        .toBe(0);
      await page.getByRole("button", { name: "Back", exact: true }).click();
      await page.goto(url + "/#agents/tasks");
      await openAISchedules(page);
      await schedule
        .getByRole("article", { name: "AI task Plan summary", exact: true })
        .getByRole("button", { name: "Evaluate now", exact: true })
        .click();
      await expect
        .poll(async () => (await read()).tasks.at(-1)?.state)
        .toBe("completed");
      const result = (await read()).aiRequests.at(-1);
      expect(result).toMatchObject({
        taskId: savedTask.id,
        connectionRef: account.ref,
        costBasis: "chatgpt-plan-reported-usage",
        spentMicrousd: 0,
        heldMicrousd: 0,
      });
      await page
        .getByRole("navigation", { name: "Task views", exact: true })
        .getByRole("button", { name: "Results", exact: true })
        .click();
      const runCard = page.getByRole("region", {
        name: "Run results",
        exact: true,
      });
      await expect(
        runCard.getByRole("button", { name: "Discuss", exact: true }),
      ).toHaveCount(0);
      await runCard
        .getByRole("button", { name: "Open thread", exact: true })
        .click();
      await expect(page).toHaveURL(/#agents\/chat$/);
      const reply = page.locator(".v2-message-assistant").last();
      await expect(reply).toBeVisible();
      await reply
        .getByRole("button", { name: "Remember this", exact: true })
        .click();
      const memDialog = page.getByRole("dialog", {
        name: "Memory",
        exact: true,
      });
      await memDialog
        .getByLabel(/^(?:Fact|Note)$/, { exact: true })
        .fill("Keep fictional event summaries brief");
      await memDialog
        .getByRole("button", { name: "Save memory", exact: true })
        .click();
      await expect(memDialog).toHaveCount(0);
      const readV2Memories = async (profile: string) => {
        const sessionToken = await page.evaluate(() =>
          sessionStorage.getItem("fased.core.session.v1"),
        );
        const res = await page.request.get(
          `${url}/api/v1/agents/v2/memories?profileId=${encodeURIComponent(profile)}`,
          {
            headers: sessionToken
              ? { Authorization: `Bearer ${sessionToken}` }
              : undefined,
          },
        );
        expect(res.ok()).toBe(true);
        return (await res.json()).memories;
      };
      await expect
        .poll(async () => (await readV2Memories(profile.id)).length)
        .toBe(1);
      expect(await readV2Memories("news-researcher")).toHaveLength(0);
    }
    expect(external).toEqual([]);
  });
}

test("provider sign-in remote restores pending attempt on reload and accepts live iss callback", async ({
  page,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
  await page.route("**/api/v1/health", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.connection = { location: "remote", transport: "loopback" };
    await route.fulfill({ response, json: body });
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);

  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(editor).toBeVisible();
  const passphrase = "disposable-remote-reload-iss-vault";

  await editor.getByRole("button", { name: "Sign in", exact: true }).click();
  await editor
    .getByRole("button", { name: "Continue with ChatGPT", exact: true })
    .click();
  const link = editor.getByRole("link", { name: "Open sign-in", exact: true });
  await expect(link).toBeVisible();
  const redirectInput = editor.getByLabel("Redirect URL", { exact: true });
  await expect(redirectInput).toHaveAttribute(
    "placeholder",
    "http://127.0.0.1:.../auth/callback?...",
  );

  // Reloading #providers while pending must restore the active Sign-in UI, not trap on disabled Sign in.
  await page.reload();
  await expect(link).toBeVisible();
  await expect(redirectInput).toBeVisible();

  // Submitting a bad issuer preserves the pending sign-in UI while showing the error alert so the user can retry or cancel.
  const badUrl = new URL((await link.getAttribute("href"))!);
  badUrl.searchParams.set("iss", "https://evil.invalid");
  await redirectInput.fill(badUrl.toString());
  await editor.getByLabel("Vault passphrase", { exact: true }).fill(passphrase);
  await editor.getByRole("button", { name: "Finish", exact: true }).click();
  await expect(editor.getByRole("alert")).toContainText("issuer mismatch");
  await expect(link).toBeVisible();
  await expect(redirectInput).toBeVisible();
  await editor.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(
    editor.getByRole("button", { name: "Continue with ChatGPT", exact: true }),
  ).toBeVisible();

  // Start again, reload #providers, paste callback containing OpenAI's RFC 9207 iss + whitespace.
  await editor
    .getByRole("button", { name: "Continue with ChatGPT", exact: true })
    .click();
  await expect(link).toBeVisible();
  await page.reload();
  await expect(link).toBeVisible();
  const goodUrl = new URL((await link.getAttribute("href"))!);
  goodUrl.searchParams.set("iss", "https://auth.openai.com");
  goodUrl.searchParams.set(
    "scope",
    "chatgpt.tokens.use.direct email offline_access openid profile resource.invoke",
  );
  await redirectInput.fill(`  ${goodUrl.toString()}  `);
  await editor.getByLabel("Vault passphrase", { exact: true }).fill(passphrase);
  await expect(
    editor.getByRole("button", { name: "Finish", exact: true }),
  ).toBeEnabled();
  await editor.getByRole("button", { name: "Finish", exact: true }).click();

  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(1);
  const accounts = await privateCLI(["ai-connection"]);
  expect(accounts.connections[0]).toMatchObject({
    provider: "openai",
    authMethod: "sign-in",
    planUse: true,
  });
  await expect(
    editor.locator(`[data-account="${accounts.connections[0].ref}"]`),
  ).toBeVisible();
  await page.reload();
  await expect(
    editor.locator(`[data-account="${accounts.connections[0].ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
});

test("shared Add Provider binds native accounts and latest models across Chat and tasks", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/settings");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-native-provider-password";
  const primary = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Primary",
    key: "fased-test-primary-preserved",
    passphrase,
  });
  const refs: Record<string, string> = {};
  await page
    .getByRole("region", { name: "Agent settings", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  for (const provider of ["anthropic", "xai"]) {
    await editor
      .getByRole("button", { name: "Add provider", exact: true })
      .click();
    await editor.locator(`[data-provider=${provider}] > button`).click();
    await expect(editor.getByLabel("API key", { exact: true })).toBeVisible();
    await expect(
      editor.getByRole("button", { name: "Unlock", exact: true }),
    ).toHaveCount(0);
    await expect(
      editor.getByRole("button", { name: "Lock", exact: true }),
    ).toHaveCount(0);
    await expect(
      editor.getByRole("button", { name: "Remove", exact: true }),
    ).toHaveCount(0);

    await editor
      .getByLabel("API key", { exact: true })
      .fill(`fased-test-${provider}-disposable-ui-key`);
    await editor
      .getByLabel("Vault passphrase", { exact: true })
      .fill(passphrase);
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await privateCLI(["ai-connection"])).connections?.find(
            (a: any) => a.provider === provider,
          )?.locked,
      )
      .toBe(false);
    const account = (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.provider === provider,
    );
    refs[provider] = account.ref;
    const refreshButton = editor.getByRole("button", {
      name: "Refresh models",
      exact: true,
    });
    const refreshed = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/v1/ai/connection") &&
        r.request().method() === "POST",
    );
    await refreshButton.click();
    expect((await refreshed).status()).toBe(200);
    await expect(refreshButton).toBeEnabled();
    expect(account.liveCallsEnabled).toBe(false);
    expect(
      account.catalog.models
        .filter((m: any) => m.selectable)
        .map((m: any) => m.id),
    ).toEqual(
      provider === "anthropic"
        ? [
            "claude-haiku-5-5",
            "claude-sonnet-5-5",
            "claude-opus-5-5",
            "claude-fable-5-2",
          ]
        : ["grok-4.7"],
    );
  }
  expect((await privateCLI(["ai-connection"])).ref).toBe(primary.ref);
  await captureScreenshot(page, {
    path: `.evidence/provider-adapters/add-provider-${isMobile ? "mobile" : "desktop"}.png`,
  });
  await page.getByRole("button", { name: "Back", exact: true }).click();
  const profileId = "trading-analyst",
    read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      revision: (await read()).revision,
      profileId,
      ...c,
    });
  await command({ action: "session-create", name: "Native providers" });
  const retained: any[] = [];
  for (const provider of ["anthropic", "xai"]) {
    await page.goto(url + "/#agents");
    await page
      .locator(".agent-header-actions")
      .getByRole("combobox", { name: "Agent", exact: true })
      .selectOption(profileId);
    const providerSelect = page.getByLabel("Provider", { exact: true });
    await revealControl(page, providerSelect);
    await providerSelect.selectOption(provider);
    await expect(page.getByLabel("Account", { exact: true })).toHaveCount(0);
    const model = provider === "anthropic" ? "claude-sonnet-5-5" : "grok-4.7";
    await page.getByLabel("Model", { exact: true }).selectOption(model);
    await page.getByRole("button", { name: "Save agent", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await read()).profiles.find((p: any) => p.id === profileId).model,
      )
      .toBe(model);
    expect(
      (await read()).profiles.find((p: any) => p.id === profileId)
        .connectionRef,
    ).toBe(refs[provider]);
    await page.goto(url + "/#agents/review");
    await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
    await page
      .getByLabel("Message", { exact: true })
      .fill("Summarize my disposable fixture note");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const review = page.getByRole("dialog", {
      name: "Allow replies",
      exact: true,
    });
    await expect(
      review.getByText(provider === "anthropic" ? "Anthropic" : "xAI", {
        exact: true,
      }),
    ).toBeVisible();
    if (provider === "xai") {
      await expect(
        review.getByText("Output reserve", { exact: true }),
      ).toBeVisible();
      await expect(
        review.getByTitle("Includes reasoning tokens", { exact: true }),
      ).toContainText("500,000");
    }
    await review
      .getByRole("button", { name: "Allow and send", exact: true })
      .click();
    await expect
      .poll(async () => {
        const requests = (await read()).aiRequests;
        return { count: requests.length, state: requests.at(-1)?.state };
      })
      .toEqual({ count: retained.length + 1, state: "completed" });
    const result = (await read()).aiRequests.at(-1);
    expect(result.model).toBe(model);
    expect(result.connectionRef).toBe(refs[provider]);
    expect(result.policy.provider).toBe(provider);
    if (provider === "xai")
      expect(result.policy.billableOutputTokens).toBe(500000);
    expect(result.answer).toContain("Development fixture response");
    retained.push(result);
    await page.goto(url + "/#agents/tasks");
    await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
    await openAISchedules(page);
    const title = page.getByLabel("AI task title");
    await revealControl(page, title, "");
    await title.fill(provider + " fixture task");
    await page
      .getByLabel("AI analysis prompt")
      .fill("Keep this account and model");
    await page
      .getByRole("button", { name: "Prepare AI task review", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).tasks.length)
      .toBe(retained.length);
    const task = (await read()).tasks.at(-1);
    expect(task.ai.model).toBe(model);
    expect(task.ai.connectionRef).toBe(refs[provider]);
  }
  const final = await read();
  expect(final.aiRequests).toEqual(retained);
  expect(final.tasks[0].ai.model).toBe("claude-sonnet-5-5");
  expect(final.tasks[1].ai.model).toBe("grok-4.7");
  await captureScreenshot(page, {
    path: `.evidence/provider-adapters/native-task-${isMobile ? "mobile" : "desktop"}.png`,
  });
});

test("scoped pilot controls keep review allow and revoke exact while ordinary core stays closed (UI fixture)", async ({
  page,
  isMobile,
}) => {
  await stop();
  await start(true);
  await page.goto(url + "/#agents/settings");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-pilot-browser-password";
  const account = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Pilot fixture",
    key: "fased-test-pilot-browser-key",
    passphrase,
  });
  const before = await readFile(join(dir, "ai-credentials.json"));
  await page
    .getByRole("region", { name: "Agent settings", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(editor.getByText("Pilot", { exact: true })).toHaveCount(0);
  let permit: any;
  let state = "";
  const commands: any[] = [];
  await page.route("**/api/v1/ai/connection", async (route) => {
    const decorate = (value: any): any => ({
      ...value,
      ...(value.ref === account.ref
        ? {
            pilotAvailable: true,
            pilotEnrollment: true,
            pilot: state ? permit : undefined,
            pilotState: state || undefined,
          }
        : {}),
      ...(value.connections
        ? { connections: value.connections.map(decorate) }
        : {}),
    });
    if (route.request().method() === "GET") {
      const response = await route.fetch();
      return route.fulfill({ response, json: decorate(await response.json()) });
    }
    const command = route.request().postDataJSON();
    if (!command.action.startsWith("pilot-")) return route.continue();
    commands.push(command);
    if (command.action === "pilot-review") {
      const now = Math.floor(Date.now() / 1000);
      permit = {
        id: "fixture-reviewed-pilot",
        instanceId: "fixture-instance",
        bootId: "fixture-boot",
        connectionRef: account.ref,
        connectionEpoch: 1,
        accessEpoch: 0,
        profileId: command.profileId,
        model: "gpt-6.1-terra",
        policyId: "fixture-current-policy",
        revision: command.revision,
        createdAt: now,
        expiresAt: now + 900,
        maxRequests: 4,
        budgetMicrousd: 500000,
        laneBudgetMicrousd: 250000,
        limits: {
          maxInputTokens: 2000,
          maxOutputTokens: 256,
          deadlineSeconds: 5,
          budgetMicrousd: 100000,
        },
        usedRequests: 0,
        allocatedMicrousd: 0,
      };
      return route.fulfill({
        json: { ...decorate(account), pilotReview: permit },
      });
    }
    if (command.action === "pilot-allow") {
      expect(command.pilot).toEqual(permit);
      expect(command.consentDigest).toBe(permit.id);
      expect(command.passphrase).toBe(passphrase);
      state = "active";
    } else {
      expect(command).toEqual({ action: "pilot-revoke", ref: account.ref });
      state = "revoked";
    }
    return route.fulfill({ json: decorate(account) });
  });
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect(
    editor.getByRole("button", { name: "Add provider", exact: true }),
  ).toBeDisabled();
  await editor.getByRole("button", { name: "Edit", exact: true }).click();
  const credentials = editor.getByRole("dialog", { name: "Edit", exact: true });
  await credentials
    .getByLabel("API key", { exact: true })
    .fill("fased-test-pilot-replacement-refused");
  await credentials
    .getByLabel("Vault passphrase", { exact: true })
    .fill(passphrase);
  await expect(
    credentials.getByRole("button", { name: "Replace", exact: true }),
  ).toBeDisabled();
  await credentials.getByLabel("API key", { exact: true }).fill("");
  await credentials.getByLabel("Vault passphrase", { exact: true }).fill("");
  await credentials
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  await editor.getByRole("button", { name: "Pilot", exact: true }).click();
  const pilot = editor.getByRole("dialog", { name: "Pilot", exact: true });
  await pilot.getByRole("button", { name: "Review", exact: true }).click();
  await expect(pilot).toContainText("$0.50");
  await expect(pilot).toContainText("4 attempts");
  await pilot.getByLabel("Vault passphrase", { exact: true }).fill(passphrase);
  await pilot.getByRole("button", { name: "Allow pilot", exact: true }).click();
  await expect(pilot.getByRole("status")).toHaveText("Active");
  await expect(
    pilot.getByLabel("Vault passphrase", { exact: true }),
  ).toHaveCount(0);
  await captureScreenshot(page, {
    path: `.evidence/openai-pilot-gate/controls-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await pilot.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(pilot.getByRole("status")).toHaveText("Stopped");
  expect(commands.map((c) => c.action)).toEqual([
    "pilot-review",
    "pilot-allow",
    "pilot-revoke",
  ]);
  expect(await readFile(join(dir, "ai-credentials.json"))).toEqual(before);
  const ordinary = await privateCLI(["ai-connection"]);
  expect(ordinary.pilot).toBeUndefined();
  expect(ordinary.liveCallsEnabled).toBe(false);
});

test("local provider connects in UI and shares configured agent Chat without API billing", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/review");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await fixtureProviderSelection("openai");
  if (isMobile) await page.goto(url + "/#agents/settings");
  await page
    .getByRole("button", { name: "Providers", exact: true })
    .first()
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await editor
    .locator('[data-provider="local"] > .provider-brand-button')
    .click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:11434/v1",
  );
  await expect(
    editor.getByLabel("API key (optional)", { exact: true }),
  ).toHaveValue("");
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-local-browser-vault");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["ai-connection"])).connections?.find(
          (a: any) => a.provider === "local",
        )?.locked,
    )
    .toBe(false);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "local",
  );
  expect(account.authMethod).toBe("local");
  expect(
    account.catalog.models.find((m: any) => m.id === "gemma4:e2b")?.selectable,
  ).toBe(true);
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Local browser analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "gemma4:e2b",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const view = await privateCLI(["agents"]);
  const created = view.profiles.find(
    (p: any) => p.name === "Local browser analyst",
  );
  const profile = created.id;
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(profile);
  expect(created).toMatchObject({
    provider: "local",
    connectionRef: account.ref,
    model: "gemma4:e2b",
  });
  if (isMobile)
    await page.getByRole("button", { name: "More", exact: true }).click();
  await page
    .getByRole("button", { name: "Providers", exact: true })
    .first()
    .click();
  await page.reload();
  await expect(
    editor.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    editor.getByRole("heading", { name: "Local server", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await expect(page.getByLabel("Message", { exact: true })).toBeVisible();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Explain this synthetic local note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("gemma4:e2b");
  await expect(review).not.toContainText("$");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile])).aiRequests.at(-1)
          ?.state,
    )
    .toBe("completed");
  const result = (
    await privateCLI(["agents", "--profile", profile])
  ).aiRequests.at(-1);
  expect(result.limits.deadlineSeconds).toBe(60);
  expect(result.connectionRef).toBe(account.ref);
  expect(result.costBasis).toBe("local-reported-usage");
  expect(result.spentMicrousd).toBe(0);
});

test("Providers page preserves agent drafts and shares account models without generating during setup", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/settings");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  // An empty provider draft may return to No AI without resurrecting the
  // previous selection while account/model fields reset in the same event.
  await expect(page.getByLabel("Provider", { exact: true })).toBeVisible();
  await page.getByLabel("Provider", { exact: true }).selectOption("xai");
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue("xai");
  await page.getByLabel("Provider", { exact: true }).selectOption("none");
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "none",
  );
  await expect(page.getByLabel("Provider", { exact: true })).toBeVisible();
  await page
    .getByRole("textbox", { name: "Instructions", exact: true })
    .fill("Preserve this unsaved agent draft");
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Research account draft");
  let providerUnavailable = true;
  const providerReads = "**/api/v1/ai/connection";
  await page.route(providerReads, async (route) => {
    if (providerUnavailable && route.request().method() === "GET")
      await route.fulfill({ status: 503, json: { error: "unavailable" } });
    else await route.continue();
  });
  await creation
    .getByRole("button", { name: "Connect provider", exact: true })
    .click();
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(page).toHaveURL(/#providers$/);
  await expect(
    page.getByRole("dialog", { name: "Providers", exact: true }),
  ).toHaveCount(0);
  await expect(
    providers.getByRole("heading", {
      name: "Providers",
      level: 1,
      exact: true,
    }),
  ).toBeVisible();
  const connectionError = providers.locator(".providers-list [role=alert]");
  await expect(connectionError).toContainText("Connection unavailable");
  await expect(connectionError).not.toContainText("preserve the vault");
  providerUnavailable = false;
  await connectionError
    .getByRole("button", { name: "Reload", exact: true })
    .click();
  await expect(
    providers.locator('[data-provider="anthropic"] > button'),
  ).toBeVisible();
  await page.unroute(providerReads);
  await expect(providers.locator(".provider-editor")).toHaveCSS(
    "background-color",
    "rgb(15, 18, 24)",
  );
  await expect(providers.locator(".provider-editor")).toHaveCSS(
    "border-radius",
    "12px",
  );

  await providers.locator('[data-provider="anthropic"] > button').click();
  await expect(
    providers.getByRole("heading", { name: "Anthropic", exact: true }),
  ).toBeVisible();
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-anthropic-page-disposable");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-full-page-vault-password");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(1);
  const account = (await privateCLI(["ai-connection"])).connections[0];
  await expect(
    providers.getByRole("region", { name: "Models", exact: true }),
  ).toContainText(account.catalog.models.find((m: any) => m.selectable).name);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  if (isMobile)
    expect(
      await providers
        .locator(".provider-brand-button")
        .first()
        .evaluate((e) => e.getBoundingClientRect().height),
    ).toBeGreaterThanOrEqual(44);
  await page.evaluate(() => scrollTo(0, 0));
  await captureScreenshot(page, {
    path: `.evidence/b02-agent-use/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await expect(creation).toBeVisible();
  await expect(creation.getByLabel("Name", { exact: true })).toHaveValue(
    "Research account draft",
  );
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
  );
  await creation
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Preserve this unsaved agent draft");
  const selectedAgent = page.getByLabel("Agent", { exact: true });
  const originalAgent = await selectedAgent.inputValue();
  const otherAgent =
    originalAgent === "news-researcher" ? "trading-analyst" : "news-researcher";
  const draftName = "Unsaved account research agent";
  await page
    .getByRole("textbox", { name: "Name", exact: true })
    .fill(draftName);
  await expect(page.getByLabel("Provider", { exact: true })).toBeVisible();
  await page.getByLabel("Provider", { exact: true }).selectOption("anthropic");
  await page
    .getByLabel("Model", { exact: true })
    .selectOption(
      account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
    );
  const draftModel = await page
    .getByLabel("Model", { exact: true })
    .inputValue();
  await selectedAgent.selectOption(otherAgent);
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).not.toHaveValue(draftName);
  await page
    .getByRole("textbox", { name: "Instructions", exact: true })
    .fill("Separate unsaved news instructions");
  await selectedAgent.selectOption(originalAgent);
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue(draftName);
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Preserve this unsaved agent draft");
  await expect(page.getByLabel("Provider", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "anthropic",
  );
  await expect(page.getByLabel("Account", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Model", { exact: true })).toHaveValue(
    draftModel,
  );
  await selectedAgent.selectOption(otherAgent);
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Separate unsaved news instructions");
  await selectedAgent.selectOption(originalAgent);

  const readOriginal = () => privateCLI(["agents", "--profile", originalAgent]);
  const beforeConcurrentEdit = await readOriginal();
  const savedOriginal = beforeConcurrentEdit.profiles.find(
    (p: any) => p.id === originalAgent,
  );
  expect(savedOriginal.name).not.toBe(draftName);
  await privateCLI(["agents", "--request-stdin"], {
    action: "profile-update",
    profileId: originalAgent,
    revision: beforeConcurrentEdit.revision,
    name: "Saved in another window",
    text: savedOriginal.persona,
    provider: savedOriginal.provider,
    model: savedOriginal.model,
    accountRef: savedOriginal.connectionRef || "",
  });
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect(page.locator(".storage-warning[role=alert]")).toContainText(
    /changed|reload|review/i,
  );
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue(draftName);
  expect(
    (await readOriginal()).profiles.find((p: any) => p.id === originalAgent)
      .name,
  ).toBe("Saved in another window");
  await page
    .getByRole("button", { name: "Refresh records", exact: true })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue("Saved in another window");
  await page
    .getByRole("textbox", { name: "Name", exact: true })
    .fill("Saved research agent");
  await expect(page.getByLabel("Provider", { exact: true })).toBeVisible();
  await page.getByLabel("Provider", { exact: true }).selectOption("anthropic");
  await page
    .getByLabel("Model", { exact: true })
    .selectOption(
      account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
    );
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await readOriginal()).profiles.find((p: any) => p.id === originalAgent)
          .connectionRef,
    )
    .toBe(account.ref);
  expect(
    (await readOriginal()).profiles.find((p: any) => p.id === originalAgent)
      .model,
  ).toBe(
    account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
  );
  await selectedAgent.selectOption(otherAgent);
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Separate unsaved news instructions");
  await selectedAgent.selectOption(originalAgent);
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue("Saved research agent");
  await captureScreenshot(page, {
    path: `.evidence/b02-agent-use/agent-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Saved research conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Keep this unsent research question with its agent.");
  await page.evaluate(() => {
    window.location.hash = "providers";
  });
  await expect(
    page.getByRole("region", { name: "Providers", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("region", { name: "Providers", exact: true })
    .getByRole("button", { name: "Back", exact: true })
    .click();
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("Keep this unsent research question with its agent.");
  expect((await readOriginal()).aiRequests).toHaveLength(0);

  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});

test("New cloud accounts use the Providers page and shared agent Chat without generation at setup", async ({
  page,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  const accounts: any[] = [];
  for (const provider of [
    "moonshot",
    "google",
    "openrouter",
    "qwen",
    "opencode",
  ]) {
    await providers.locator(`[data-provider="${provider}"] > button`).click();
    await providers
      .getByLabel("API key", { exact: true })
      .fill(`fased-test-${provider}-disposable-ui-key`);
    await providers
      .getByLabel("Vault passphrase", { exact: true })
      .fill("disposable-five-provider-vault");
    await providers.getByRole("button", { name: "Save", exact: true }).click();
    await expect
      .poll(
        async () => (await privateCLI(["ai-connection"])).connections?.length,
      )
      .toBe(accounts.length + 1);
    const account = (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.provider === provider,
    );
    accounts.push(account);
    const models = providers.getByRole("region", {
      name: "Models",
      exact: true,
    });
    await expect(models).toContainText(account.catalog.models[0].name);
    if (provider === "moonshot") {
      await expect(models).toContainText("Pricing unavailable");
      expect(account.catalog.models[0].selectable).toBe(false);
    } else expect(account.catalog.models[0].selectable).toBe(true);
    expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  }
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Google browser analyst");
  const account = accounts.find((a) => a.provider === "google");
  for (const a of accounts.filter((a) => a.provider !== "moonshot")) {
    await creation.getByLabel("Account", { exact: true }).selectOption(a.ref);
    await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
      a.catalog.models[0].id,
    );
    await expect(
      creation.getByRole("button", { name: "Create agent", exact: true }),
    ).toBeEnabled();
  }
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Google browser analyst",
  );
  expect(profile).toMatchObject({
    provider: "google",
    connectionRef: account.ref,
    model: account.catalog.models[0].id,
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Google conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Explain this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText(account.catalog.models[0].id);
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.connectionRef).toBe(account.ref);
  expect(request.heldMicrousd).toBe(0);
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled();
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
});

test("OpenRouter Remote sign-in accepts a code and returns an ordinary encrypted API account", async ({
  page,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  // Only the public Remote descriptor is simulated; fixture runtime stays Local.
  await page.route("**/api/v1/health", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.connection = { location: "remote", transport: "loopback" };
    await route.fulfill({ response, json: body });
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers.locator('[data-provider="openrouter"] > button').click();
  await providers
    .getByRole("button", { name: "Sign in", exact: true })
    .first()
    .click();
  await providers
    .getByRole("button", { name: "Continue", exact: true })
    .click();
  await expect(providers.getByLabel("Code", { exact: true })).toBeVisible();
  await providers
    .getByLabel("Code", { exact: true })
    .fill("fixture-router-code");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-router-browser-vault");
  await providers.getByRole("button", { name: "Finish", exact: true }).click();
  await expect(
    providers.getByRole("region", { name: "Models", exact: true }),
  ).toContainText("Gemini 3.8 Flash");
  const account = (await privateCLI(["ai-connection"])).connections[0];
  expect(account).toMatchObject({
    provider: "openrouter",
    authMethod: "api-key",
    credentialMode: "test",
    liveCallsEnabled: false,
    locked: false,
  });
  expect(account.planUse).not.toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
});

test("Named local servers share setup models and agent Chat without retargeting locked accounts", async ({
  page,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await editor
    .getByLabel("Search providers", { exact: true })
    .fill("LM Studio");
  await editor
    .getByRole("button", { name: "Connect LM Studio", exact: true })
    .click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:1234/v1",
  );
  await editor
    .getByLabel("Endpoint", { exact: true })
    .fill("http://127.0.0.1:9999/v1");
  await editor
    .getByRole("button", { name: "Connect LM Studio", exact: true })
    .click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:1234/v1",
  );
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await editor.getByLabel("Search providers", { exact: true }).clear();
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-local-preset-vault");
  await editor
    .getByRole("button", { name: "Connect vLLM", exact: true })
    .click();
  await expect(
    editor.getByLabel("Vault passphrase", { exact: true }),
  ).toHaveValue("disposable-local-preset-vault");
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:8000/v1",
  );
  const accounts: any[] = [];
  for (const [name, port] of [
    ["Ollama", 11434],
    ["LM Studio", 1234],
    ["vLLM", 8000],
  ] as const) {
    await editor
      .locator('[data-provider="local"] > .provider-brand-button')
      .click();
    await editor
      .getByRole("button", { name: `Connect ${name}`, exact: true })
      .click();
    await expect(
      editor.getByRole("button", { name: `Connect ${name}`, exact: true }),
    ).toHaveAttribute("aria-pressed", "true");
    await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
      `http://127.0.0.1:${port}/v1`,
    );
    await editor
      .getByLabel("Vault passphrase", { exact: true })
      .fill("disposable-local-preset-vault");
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect
      .poll(
        async () => (await privateCLI(["ai-connection"])).connections?.length,
      )
      .toBe(accounts.length + 1);
    const account = (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.name === name,
    );
    accounts.push(account);
    expect(account).toMatchObject({
      provider: "local",
      name,
      endpoint: `http://127.0.0.1:${port}/v1`,
      authMethod: "local",
      locked: false,
      liveCallsEnabled: false,
    });
  }
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  const account = accounts[1],
    model = "google/gemma-4-e2b";
  await editor.locator(`[data-account="${account.ref}"]`).click();
  await editor.getByText("Edit", { exact: true }).click();
  await editor.getByRole("button", { name: "Lock", exact: true }).click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue("");
  await expect(
    editor.getByRole("button", { name: "Replace", exact: true }),
  ).toBeDisabled();
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-local-preset-vault");
  await editor.getByRole("button", { name: "Unlock", exact: true }).click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:1234/v1",
  );
  await editor
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("LM Studio analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await creation.getByLabel("Model", { exact: true }).selectOption(model);
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "LM Studio analyst",
  );
  expect(profile).toMatchObject({
    provider: "local",
    connectionRef: account.ref,
    model,
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Named local conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic local note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText(model);
  await expect(review).not.toContainText("$");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request).toMatchObject({
    model,
    connectionRef: account.ref,
    costBasis: "local-reported-usage",
    spentMicrousd: 0,
    heldMicrousd: 0,
  });
  await captureScreenshot(page, {
    path: `.evidence/b03-local-servers/local-${test.info().project.name}.png`,
    fullPage: true,
  });
  expect(external).toEqual([]);
});

test("Qwen API keeps its region and model through Providers, agent and Chat", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="qwen"] > .provider-brand-button')
    .click();
  await expect(
    providers.getByText("ModelStudio · Singapore", { exact: true }),
  ).toBeVisible();
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-qwen-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-qwen-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(1);
  const account = (await privateCLI(["ai-connection"])).connections[0];
  expect(account.catalog.source).toBe(
    "https://dashscope-intl.aliyuncs.com/api/v1/models?page_no=1&page_size=100&providers=qwen",
  );
  expect(
    account.catalog.models.find((m: any) => m.id === "qwen3.8-flash")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await expect(
    providers.getByText("ModelStudio · Singapore", { exact: true }),
  ).toBeVisible();
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Qwen regional analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "qwen3.8-flash",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Qwen regional analyst",
  );
  expect(profile).toMatchObject({
    provider: "qwen",
    connectionRef: account.ref,
    model: "qwen3.8-flash",
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Qwen note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("qwen3.8-flash");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request).toMatchObject({
    model: "qwen3.8-flash",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    providers.getByText("ModelStudio · Singapore", { exact: true }),
  ).toBeVisible();
  await captureScreenshot(page, {
    path: `.evidence/b03-qwen-api/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Kimi regional USD account shares agent Chat and preserves mainland records", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "moonshot",
    name: "Existing CNY account",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(false);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="moonshot"] > .provider-brand-button')
    .click();
  await providers
    .getByRole("button", { name: "International · USD", exact: true })
    .click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "International · USD",
  );
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "moonshot-intl",
  );
  expect(account.catalog.source).toBe("https://api.moonshot.ai/v1/models");
  expect(
    account.catalog.models.find(
      (m: any) => m.id === "moonshot-intl/kimi-k3.1-pro",
    ).selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "International · USD",
  );
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Kimi international analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "moonshot-intl/kimi-k3.1-pro",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Kimi international analyst",
  );
  expect(profile).toMatchObject({
    provider: "moonshot-intl",
    connectionRef: account.ref,
    model: "moonshot-intl/kimi-k3.1-pro",
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("moonshot-intl/kimi-k3.1-pro");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request).toMatchObject({
    model: "moonshot-intl/kimi-k3.1-pro",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".provider-route")).toBeVisible();
  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("moonshot");
  await captureScreenshot(page, {
    path: `.evidence/b03-kimi/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Google API sign-in uses project models in agent Chat and preserves its API key account", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const old = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "google",
    name: "Preserved API account",
    key: "fased-test-google-original-browser",
    passphrase: "disposable-google-original-vault",
  });
  await page.reload();
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="google"] > .provider-brand-button')
    .click();
  await providers.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(providers.getByLabel("API key", { exact: true })).toHaveCount(0);
  await providers
    .getByLabel("Project", { exact: true })
    .fill("fased-fixture-project");
  await providers
    .getByRole("button", { name: "Continue with Google", exact: true })
    .click();
  const link = providers.getByRole("link", {
    name: "Open sign-in",
    exact: true,
  });
  await expect(link).toBeVisible();
  const callback = await link.getAttribute("href");
  expect(callback).toMatch(/^http:\/\/127\.0\.0\.1:/);
  expect((await page.request.get(callback!)).status()).toBe(200);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(providers.getByText("Ready", { exact: true })).toBeVisible();
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-google-oauth-vault");
  await providers.getByRole("button", { name: "Finish", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(2);
  const all = (await privateCLI(["ai-connection"])).connections;
  const account = all.find((a: any) => a.authMethod === "oauth");
  expect(account).toMatchObject({
    provider: "google",
    googleProject: "fased-fixture-project",
    planUse: false,
    liveCallsEnabled: false,
  });
  expect(all.find((a: any) => a.ref === old.ref).authMethod).toBe("api-key");
  expect(
    account.catalog.models.find((m: any) => m.id === "gemini-3.8-flash"),
  ).toMatchObject({ method: "oauth", selectable: true });
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await expect(providers.locator(".provider-route")).toHaveText(
    "fased-fixture-project",
  );
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Google OAuth analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "gemini-3.8-flash",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Google OAuth analyst",
  );
  expect(profile).toMatchObject({
    provider: "google",
    connectionRef: account.ref,
    model: "gemini-3.8-flash",
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Google OAuth note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("gemini-3.8-flash");
  await expect(review).not.toContainText("ChatGPT");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const result = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(result).toMatchObject({
    connectionRef: account.ref,
    model: "gemini-3.8-flash",
    heldMicrousd: 0,
  });
  expect(result.spentMicrousd).toBeGreaterThan(0);
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".provider-route")).toHaveText(
    "fased-fixture-project",
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await captureScreenshot(page, {
    path: `.evidence/b03-google-oauth/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Kimi Code membership connects models agent and normal Chat without task authority", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "moonshot",
    name: "Existing CNY account",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(false);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="moonshot"] > .provider-brand-button')
    .click();
  await providers
    .getByRole("button", { name: "Code · Interactive", exact: true })
    .click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "Code · Interactive",
  );
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "kimi-code",
  );
  expect(account.catalog.source).toBe("https://api.kimi.ai/coding/v1/models");
  expect(
    account.catalog.models.find((m: any) => m.id === "kimi-code/k3.1-pro")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "Code · Interactive",
  );
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Personal Kimi analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "kimi-code/k3.1-pro",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Personal Kimi analyst",
  );
  expect(profile).toMatchObject({
    provider: "kimi-code",
    connectionRef: account.ref,
    model: "kimi-code/k3.1-pro",
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("kimi-code/k3.1-pro");
  await expect(review).toContainText("Kimi Code");
  await expect(review).not.toContainText("Maximum cost");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.spentMicrousd).toBe(0);
  expect(request.costBasis).toBe("kimi-code-reported-usage");
  expect(request).toMatchObject({
    model: "kimi-code/k3.1-pro",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".provider-route")).toBeVisible();
  await expect(
    providers.getByText("Background access", { exact: true }),
  ).toHaveCount(0);
  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("moonshot");
  await captureScreenshot(page, {
    path: `.evidence/b03-kimi-code/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Anthropic workspace account joins models agent Chat and preserves existing keys", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "anthropic",
    name: "Existing workspace-scoped key",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(true);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="anthropic"] > .provider-brand-button')
    .click();
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers
    .getByLabel("Workspace (optional)", { exact: true })
    .fill("wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "anthropic" && a.ref !== mainland.ref,
  );
  expect(account.workspace).toBe("wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ");
  expect(account.catalog.source).toBe("https://api.anthropic.com/v1/models");
  expect(
    account.catalog.models.find((m: any) => m.id === "claude-haiku-5-5")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("Workspace analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "claude-haiku-5-5",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Workspace analyst",
  );
  expect(profile).toMatchObject({
    provider: "anthropic",
    connectionRef: account.ref,
    model: "claude-haiku-5-5",
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("claude-haiku-5-5");
  await expect(review).toContainText("Anthropic");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.spentMicrousd).toBeGreaterThan(0);
  expect(request).toMatchObject({
    model: "claude-haiku-5-5",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");

  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("anthropic");
  await captureScreenshot(page, {
    path: `.evidence/b03-methods/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Ollama Cloud key joins models agent Chat and task access without an invented tariff", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "moonshot",
    name: "Existing CNY account",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(false);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="ollama-cloud"] > .provider-brand-button')
    .click();
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        ((await privateCLI(["ai-connection"])).connections ?? []).length,
    )
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "ollama-cloud",
  );
  expect(account.catalog.source).toBe("https://ollama.com/v1/models");
  expect(
    account.catalog.models.find((m: any) => m.id === "ollama-cloud/gemma4:31b")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("Cloud analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "ollama-cloud/gemma4:31b",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Cloud analyst",
  );
  expect(profile).toMatchObject({
    provider: "ollama-cloud",
    connectionRef: account.ref,
    model: "ollama-cloud/gemma4:31b",
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("ollama-cloud/gemma4:31b");
  await expect(review).toContainText("Ollama Cloud");
  await expect(review).not.toContainText("Maximum cost");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.spentMicrousd).toBe(0);
  expect(request.costBasis).toBe("ollama-cloud-reported-usage");
  expect(request).toMatchObject({
    model: "ollama-cloud/gemma4:31b",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });

  const relayAccount = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "local",
    server: "ollama",
    name: "Owned Ollama server",
    passphrase: "disposable-ollama-server-vault",
  });
  expect(
    relayAccount.catalog.models.find((m: any) => m.id === "gemma4:cloud")
      .selectable,
  ).toBe(true);
  await page.goto(url + "/#providers");
  {
    await providers.getByRole("button", { name: "Back", exact: true }).click();
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    const creation = page.getByRole("dialog", {
      name: "New agent",
      exact: true,
    });
    await creation
      .getByLabel("Name", { exact: true })
      .fill("Ollama server cloud analyst");
    await creation
      .getByLabel("Account", { exact: true })
      .selectOption(relayAccount.ref);
    await creation
      .getByLabel("Model", { exact: true })
      .selectOption("gemma4:cloud");
    await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
      "gemma4:cloud",
    );
    await creation
      .getByRole("button", { name: "Create agent", exact: true })
      .click();
    await expect(creation).toBeHidden();
    const profile = (await privateCLI(["agents"])).profiles.find(
      (p: any) => p.name === "Ollama server cloud analyst",
    );
    expect(profile).toMatchObject({
      provider: "local",
      connectionRef: relayAccount.ref,
      model: "gemma4:cloud",
    });
    await page.evaluate(() => {
      window.location.hash = "agents/review";
    });
    await openConversationList(page);
    await page
      .getByRole("button", { name: "New conversation", exact: true })
      .click();
    await page
      .getByLabel("Conversation name", { exact: true })
      .fill("Synthetic Kimi note");
    await page
      .getByRole("button", { name: "Create conversation", exact: true })
      .click();
    await page
      .getByLabel("Message", { exact: true })
      .fill("Summarize this synthetic note.");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const review = page.getByRole("dialog", {
      name: "Allow replies",
      exact: true,
    });
    await expect(review).toContainText("gemma4:cloud");
    await expect(review).toContainText("Ollama Cloud");
    await expect(review).not.toContainText("Maximum cost");
    await review
      .getByRole("button", { name: "Allow and send", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
            -1,
          )?.state,
      )
      .toBe("completed");
    const request = (
      await privateCLI(["agents", "--profile", profile.id])
    ).aiRequests.at(-1);
    expect(request.spentMicrousd).toBe(0);
    expect(request.costBasis).toBe("ollama-cloud-reported-usage");
    expect(request).toMatchObject({
      model: "gemma4:cloud",
      connectionRef: relayAccount.ref,
      heldMicrousd: 0,
    });
  }

  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${relayAccount.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    providers.getByText("Background access", { exact: true }),
  ).toHaveCount(1);
  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("moonshot");
  await captureScreenshot(page, {
    path: `.evidence/b03-methods/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("ordinary mobile Providers scrolls all brands and its saved lower account without overlays", async ({
  page,
  isMobile,
}) => {
  test.skip(!isMobile, "narrow provider pane regression");
  await stop();
  await start(true);
  const link = (
    await exec(binary, ["setup-link", "--data-dir", dir])
  ).stdout.trim();
  await page.goto(link);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const saved = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "opencode",
    name: "Saved lower account",
    key: "fased-test-opencode-scroll-disposable",
    passphrase: "disposable-provider-scroll-vault",
  });
  expect(saved.liveCallsEnabled).toBe(false);
  const listen = new URL(url).host;
  await stop();
  await start(false, listen);
  await page.goto(url + "/#providers");
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  expect((await privateCLI(["agents"])).aiFixtureAvailable).toBeFalsy();
  const before = await privateCLI(["ai-connection"]);
  expect(before.connections.find((a: any) => a.ref === saved.ref).locked).toBe(
    true,
  );
  const writes: string[] = [],
    external: string[] = [];
  page.on("request", (request) => {
    if (!ownedFixtureOrigins.has(new URL(request.url()).origin))
      external.push(request.url());
    if (request.method() !== "GET" && request.url().includes("/api/v1/"))
      writes.push(request.url());
  });
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  const list = providers.locator(".providers-list");
  await expect(list).toHaveCSS("overflow-y", "auto");
  expect(await list.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(
    true,
  );
  const brands = await providers
    .locator(".provider-brand")
    .evaluateAll((elements) =>
      elements.map((el) => el.getAttribute("data-provider")!),
    );
  expect(brands).toContain("local");
  expect(brands).toContain("opencode");
  for (const id of brands) {
    const button = providers.locator(
      `[data-provider="${id}"] > .provider-brand-button`,
    );
    await button.click();
    await expect(button).toHaveAttribute("aria-pressed", "true");
    await expect(providers.locator(".provider-editor")).toBeVisible();
  }
  const lower = providers.locator(
    `.provider-account[data-account="${saved.ref}"]`,
  );
  await lower.click();
  await expect(lower).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".connection-status")).toContainText("Locked");
  expect(await list.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
  const bounds = await providers.evaluate((el) => {
    const list = el.querySelector(".providers-list")!.getBoundingClientRect();
    const editor = el
      .querySelector(".provider-editor")!
      .getBoundingClientRect();
    return {
      listBottom: list.bottom,
      editorTop: editor.top,
      editorRight: editor.right,
      width: innerWidth,
      scroll: document.documentElement.scrollWidth,
    };
  });
  expect(bounds.editorTop).toBeGreaterThanOrEqual(bounds.listBottom);
  expect(bounds.editorRight).toBeLessThanOrEqual(bounds.width);
  expect(bounds.scroll).toBeLessThanOrEqual(bounds.width);
  expect((await privateCLI(["ai-connection"])).connections).toEqual(
    before.connections,
  );
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  expect(writes).toEqual([]);
  expect(external).toEqual([]);
});

test("passwordless OS-protected providers and task access work without a vault passphrase", async ({
  page,
}) => {
  await page.goto(url + "/#overview");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const baseConnection = await (
    await page.request.get(url + "/api/v1/ai/connection", {
      headers: { Authorization: `Bearer ${token}` },
    })
  ).json();

  let savedAccount: Record<string, any> | undefined;
  let signIn: Record<string, any> | undefined;
  let accessReview: Record<string, any> | undefined;
  let taskAccess: Record<string, any> | undefined;
  const postedActions: Record<string, unknown>[] = [];
  const makeAccount = (catalogState: string) => ({
    ...baseConnection,
    ref: "acct-os-1",
    name: "OS Protected OpenAI",
    provider: "openai",
    authMethod: savedAccount?.authMethod ?? "api-key",
    credentialMode: "openai",
    credentialModes: ["openai", "openrouter", "local"],
    osVaultAvailable: true,
    osProtected: true,
    locked: false,
    testOnly: false,
    liveCallsEnabled: true,
    persistentTaskAccessAvailable: true,
    model: "gpt-6.2-sol",
    catalog: {
      ...baseConnection.catalog,
      state: catalogState,
      models: [
        {
          id: "gpt-6.2-sol",
          name: "GPT-6.2 Sol",
          provider: "openai",
          method: savedAccount?.authMethod ?? "api-key",
          connectionRef: "acct-os-1",
          recommended: true,
          selectable: true,
          availability: "ready",
        },
      ],
    },
    ...(taskAccess ? { taskAccess } : {}),
  });
  const rootPayload = () => ({
    ...baseConnection,
    ...(savedAccount ?? {}),
    credentialModes: ["openai", "openrouter", "local"],
    osVaultAvailable: true,
    signInAvailable: true,
    liveCallsEnabled: true,
    persistentTaskAccessAvailable: true,
    connections: savedAccount ? [savedAccount] : [],
    ...(signIn ? { signIn } : {}),
    ...(accessReview ? { accessReview } : {}),
  });

  await page.route("**/api/v1/agents**", async (route) => {
    const res = await route.fetch();
    const json = await res.json();
    if (route.request().method() === "GET") {
      json.tasks = [
        ...(json.tasks ?? []),
        {
          id: "task-os-1",
          name: "Passwordless scheduled summary",
          state: "scheduled",
          ai: {
            authorized: true,
            connectionRef: "acct-os-1",
            digest: "digest-os-1",
          },
        },
      ];
    }
    await route.fulfill({ response: res, json });
  });
  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ json: rootPayload() });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, any>;
    postedActions.push(body);
    expect(body).not.toHaveProperty("passphrase");
    const now = Math.floor(Date.now() / 1000);
    if (body.action === "sign-in-start") {
      signIn = {
        id: "si-os-1",
        provider: "openai",
        state: "waiting",
        url: "https://auth.openai.com/oauth/authorize?state=mock-os-1",
        expiresAt: now + 300,
        remote: false,
      };
    } else if (body.action === "sign-in-complete") {
      signIn = undefined;
      savedAccount = makeAccount("ready");
      savedAccount.authMethod = "sign-in";
    } else if (body.action === "add") {
      savedAccount = makeAccount("missing");
    } else if (body.action === "models-refresh") {
      savedAccount = makeAccount("ready");
    } else if (body.action === "task-access-review") {
      accessReview = {
        id: "allow-os-1",
        connectionRef: "acct-os-1",
        profileId: body.profileId || "trading-analyst",
        model: "gpt-6.2-sol",
        tasks: [{ id: "task-os-1", digest: "digest-os-1" }],
        createdAt: now,
        expiresAt: now + 3600,
        maxRequests: 5,
        budgetMicrousd: 100000,
        usedRequests: 0,
        allocatedMicrousd: 0,
        continueOnSignOut: true,
        continueAfterRestart: true,
      };
    } else if (body.action === "task-access-allow") {
      taskAccess = accessReview;
      accessReview = undefined;
      if (savedAccount) savedAccount = { ...savedAccount, taskAccess };
    } else if (body.action === "remove") {
      savedAccount = undefined;
      taskAccess = undefined;
      accessReview = undefined;
    }
    await route.fulfill({ json: rootPayload() });
  });

  await openProviders(page);
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(editor).toBeVisible();
  await expect(editor.getByLabel("Vault passphrase")).toHaveCount(0);

  // Local passwordless sign-in auto-completes once callback-ready without extra Finish clicks.
  await editor.getByRole("button", { name: "Sign in", exact: true }).click();
  await editor
    .getByRole("button", { name: "Continue with ChatGPT", exact: true })
    .click();
  const openSignIn = editor.getByRole("link", {
    name: "Open sign-in",
    exact: true,
  });
  await expect(openSignIn).toHaveClass(/\bprimary\b/);
  await expect(
    editor.getByRole("button", { name: "Finish", exact: true }),
  ).toHaveCount(0);
  signIn = { ...signIn, state: "callback-ready" };
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect
    .poll(() => postedActions.some((a) => a.action === "sign-in-complete"))
    .toBe(true);

  // Switch to adding a passwordless API key account with catalog refresh in Models header and 1-click Remove.
  savedAccount = undefined;
  await editor
    .getByRole("button", { name: "Add provider", exact: true })
    .click();
  await editor.getByRole("button", { name: "API key", exact: true }).click();
  await expect(editor.getByLabel("Vault passphrase")).toHaveCount(0);
  await editor
    .getByLabel("API key", { exact: true })
    .fill("disposable-os-vault-input-not-issued");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect(editor.locator(".provider-models")).toBeVisible();
  const refreshModels = editor
    .locator(".provider-models header")
    .getByRole("button", { name: "Refresh", exact: true });
  await expect(refreshModels).toBeVisible();
  await refreshModels.click();
  await expect
    .poll(() => postedActions.some((a) => a.action === "models-refresh"))
    .toBe(true);
  await expect(refreshModels).toBeVisible();
  await expect(editor.locator(".provider-models")).toContainText(
    /Recommended|Ready/,
  );

  // Background task access allowance works with zero Vault passphrase prompt.
  await editor.getByText("Background access", { exact: true }).click();
  await editor
    .getByRole("checkbox", {
      name: "Passwordless scheduled summary",
      exact: true,
    })
    .check();
  await editor
    .getByRole("button", { name: "Review access", exact: true })
    .click();
  await expect(editor.getByLabel("Vault passphrase")).toHaveCount(0);
  await editor
    .getByRole("button", { name: "Allow task access", exact: true })
    .click();
  await expect
    .poll(() => postedActions.some((a) => a.action === "task-access-allow"))
    .toBe(true);

  // 1-click Remove requires no passphrase and Lock/Unlock are hidden for OS-protected vaults.
  await editor.getByText("Edit", { exact: true }).click();
  await expect(editor.getByLabel("Vault passphrase")).toHaveCount(0);
  await expect(
    editor.getByRole("button", { name: "Lock", exact: true }),
  ).toHaveCount(0);
  await expect(
    editor.getByRole("button", { name: "Unlock", exact: true }),
  ).toHaveCount(0);
  await editor.getByRole("button", { name: "Remove", exact: true }).click();
  await expect
    .poll(() => postedActions.some((a) => a.action === "remove"))
    .toBe(true);
});

test("remote passwordless sign-in auto-completes from guided modal without details clutter", async ({
  page,
}) => {
  await page.goto(url + "/#overview");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const baseConnection = await (
    await page.request.get(url + "/api/v1/ai/connection", {
      headers: { Authorization: `Bearer ${token}` },
    })
  ).json();

  let savedAccount: Record<string, any> | undefined;
  let signIn: Record<string, any> | undefined;
  const postedActions: Record<string, any>[] = [];
  const rootPayload = () => ({
    ...baseConnection,
    ...(savedAccount ?? {}),
    credentialModes: ["openai", "openrouter", "local"],
    osVaultAvailable: true,
    signInAvailable: true,
    liveCallsEnabled: true,
    persistentTaskAccessAvailable: true,
    connections: savedAccount ? [savedAccount] : [],
    ...(signIn ? { signIn } : {}),
  });

  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ json: rootPayload() });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, any>;
    postedActions.push(body);
    const now = Math.floor(Date.now() / 1000);
    if (body.action === "sign-in-start") {
      signIn = {
        id: "si-remote-os-1",
        provider: "openai",
        state: "waiting",
        url: "https://auth.openai.com/api/accounts/authorize?state=mock-remote-1",
        expiresAt: now + 300,
        remote: true,
      };
    } else if (body.action === "sign-in-complete") {
      signIn = undefined;
      savedAccount = {
        ...baseConnection,
        ref: "acct-remote-os-1",
        name: "Remote OpenAI",
        provider: "openai",
        authMethod: "sign-in",
        credentialMode: "openai",
        credentialModes: ["openai", "openrouter", "local"],
        osVaultAvailable: true,
        osProtected: true,
        locked: false,
        testOnly: false,
        liveCallsEnabled: true,
        persistentTaskAccessAvailable: true,
        model: "gpt-6.2-sol",
        catalog: {
          ...baseConnection.catalog,
          state: "ready",
          models: [
            {
              id: "gpt-6.2-sol",
              name: "GPT-6.2 Sol",
              provider: "openai",
              method: "sign-in",
              connectionRef: "acct-remote-os-1",
              recommended: true,
              selectable: true,
              availability: "ready",
            },
          ],
        },
      };
    }
    await route.fulfill({ json: rootPayload() });
  });

  await openProviders(page);
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(editor).toBeVisible();
  await editor.getByRole("button", { name: "Sign in", exact: true }).click();
  await editor
    .getByRole("button", { name: "Continue with ChatGPT", exact: true })
    .click();

  const modal = editor.getByRole("dialog", { name: "OpenAI", exact: true });
  await expect(modal).toBeVisible();
  await expect(
    modal.getByRole("link", { name: "Open sign-in", exact: true }),
  ).toBeVisible();
  await expect(modal).toContainText("127.0.0.1");
  await expect(
    modal.getByRole("button", { name: "Paste", exact: true }),
  ).toBeVisible();

  await modal
    .getByLabel("Redirect URL", { exact: true })
    .fill(
      "  http://127.0.0.1:48123/auth/callback?code=cb_12345&state=mock-remote-1  ",
    );
  await expect
    .poll(
      () =>
        postedActions.find((a) => a.action === "sign-in-complete")?.callback,
    )
    .toBe(
      "http://127.0.0.1:48123/auth/callback?code=cb_12345&state=mock-remote-1",
    );
  await expect(modal).toHaveCount(0);
  await expect(editor.locator(".provider-editor details")).toHaveCount(0);
  await expect(editor.locator("details")).toHaveCount(0);
  await expect(
    editor.getByRole("button", { name: "Edit", exact: true }),
  ).toBeVisible();
  await expect(
    editor.getByRole("button", { name: "Background access", exact: true }),
  ).toBeVisible();
});

test("Host CLI subscription detection, connect, and device-code auth modal", async ({
  page,
}) => {
  await page.goto(url + "/#overview");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const baseConnection = await (
    await page.request.get(url + "/api/v1/ai/connection", {
      headers: { Authorization: `Bearer ${token}` },
    })
  ).json();

  let savedAccount: Record<string, any> | undefined;
  let cliAuth: Record<string, any> | null = null;
  const postedActions: Record<string, any>[] = [];
  const hostClis = [
    {
      provider: "openai",
      binary: "codex",
      label: "Codex CLI",
      installed: true,
      authenticated: true,
      account: "Logged in using ChatGPT",
      supportsDevice: true,
      loginCommand: "codex login --device-auth",
      installHint: "npm install -g @openai/codex",
      models: ["gpt-5.4"],
    },
    {
      provider: "anthropic",
      binary: "claude",
      label: "Claude Code",
      installed: true,
      authenticated: false,
      supportsDevice: false,
      loginCommand: "claude auth login",
      installHint: "npm install -g @anthropic-ai/claude-code",
      models: ["claude-sonnet-4-6"],
    },
    {
      provider: "google",
      binary: "antigravity",
      label: "Antigravity ACP",
      transport: "acp",
      version: "1.5.13",
      binaryPath: "/home/user/.t3/tools/antigravity-acp/antigravity",
      installed: true,
      authenticated: false,
      supportsDevice: false,
      loginCommand: "antigravity acp --login",
      installHint: "Install Antigravity desktop or run antigravity acp",
      models: ["gemini-4-neon", "gemini-4-carbon", "gemini-3.8-flash"],
    },
    {
      provider: "google",
      binary: "gemini",
      label: "Gemini CLI",
      transport: "cli",
      installed: false,
      authenticated: false,
      supportsDevice: false,
      loginCommand: "gemini",
      installHint: "npm install -g @google/gemini-cli",
      models: ["gemini-2.5-pro"],
    },
  ];

  const rootPayload = () => ({
    ...baseConnection,
    ...(savedAccount ?? {}),
    credentialModes: ["openai", "anthropic", "google", "local"],
    osVaultAvailable: true,
    signInAvailable: true,
    liveCallsEnabled: true,
    persistentTaskAccessAvailable: true,
    hostClis,
    ...(cliAuth ? { cliAuth } : {}),
    connections: savedAccount ? [savedAccount] : [],
  });

  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ json: rootPayload() });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, any>;
    postedActions.push(body);
    const now = Math.floor(Date.now() / 1000);
    if (body.action === "cli-connect") {
      cliAuth = null;
      savedAccount = {
        ...baseConnection,
        ref: "acct-cli-openai-1",
        name: body.name || "Codex CLI",
        provider: body.provider || "openai",
        authMethod: "cli",
        credentialMode: "cli",
        osVaultAvailable: true,
        osProtected: true,
        locked: false,
        testOnly: false,
        liveCallsEnabled: true,
        model: "gpt-5.4",
        catalog: {
          ...baseConnection.catalog,
          state: "ready",
          models: [
            {
              id: "gpt-5.4",
              name: "GPT-5.4",
              provider: "openai",
              method: "cli",
              connectionRef: "acct-cli-openai-1",
              recommended: true,
              selectable: true,
              availability: "available",
            },
          ],
        },
      };
    } else if (body.action === "cli-auth-start") {
      cliAuth = {
        id: "cli-auth-1",
        provider: body.provider || "anthropic",
        binary: body.provider === "anthropic" ? "claude" : "codex",
        state: "awaiting-code",
        verificationUrl: "https://auth.example.com/device",
        userCode: "ABCD-1234",
        expiresAt: now + 300,
      };
    } else if (body.action === "cli-auth-cancel") {
      cliAuth = null;
    } else if (body.action === "set-enabled" && savedAccount) {
      savedAccount = {
        ...savedAccount,
        disabled: !!body.disabled,
      };
    }
    await route.fulfill({ json: rootPayload() });
  });

  await openProviders(page);
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(editor).toBeVisible();

  await editor.getByRole("button", { name: "CLI", exact: true }).click();
  await expect(editor.locator(".cli-status-card")).toContainText("Codex CLI");
  await expect(editor.locator(".cli-status-card")).toContainText("Connected");
  await expect(editor.locator(".cli-status-card")).toContainText(
    "Logged in using ChatGPT",
  );
  await editor
    .getByRole("button", { name: "Connect CLI", exact: true })
    .click();

  await expect
    .poll(() => postedActions.find((a) => a.action === "cli-connect"))
    .toEqual({ action: "cli-connect", provider: "openai" });
  await expect(editor.getByRole("status")).toContainText("Unlocked");

  await editor
    .getByRole("button", { name: "Add provider", exact: true })
    .click();
  await page.getByRole("button", { name: /^Anthropic$/ }).click();
  await editor.getByRole("button", { name: "CLI", exact: true }).click();
  await expect(editor.locator(".cli-status-card")).toContainText("Claude Code");
  await expect(editor.locator(".cli-status-card")).toContainText(
    "Sign in required",
  );
  await editor
    .getByRole("button", { name: "Sign in via CLI", exact: true })
    .click();

  const authDialog = editor.getByRole("dialog", {
    name: "Claude Code",
    exact: true,
  });
  await expect(authDialog).toBeVisible();
  await expect(
    authDialog.getByRole("link", { name: "Open sign-in", exact: true }),
  ).toHaveAttribute("href", "https://auth.example.com/device");
  await expect(authDialog.getByLabel("Device code")).toContainText("ABCD-1234");
  await authDialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect
    .poll(() => postedActions.some((a) => a.action === "cli-auth-cancel"))
    .toBe(true);
  await expect(authDialog).toHaveCount(0);

  await page.getByRole("button", { name: /^Google$/ }).click();
  await editor.getByRole("button", { name: "CLI", exact: true }).click();
  await expect(editor.locator(".cli-status-card")).toContainText(
    "Antigravity ACP",
  );
  await expect(editor.locator(".cli-status-card")).toContainText("v1.5.13");
  await expect(editor.locator(".cli-status-card")).toContainText(
    "Sign in required",
  );
  await editor.getByRole("button", { name: "Gemini CLI", exact: true }).click();
  await expect(editor.locator(".cli-status-card")).toContainText("Gemini CLI");
  await expect(editor.locator(".cli-status-card")).toContainText(
    "Not installed",
  );
  await editor
    .getByRole("button", { name: "Check again", exact: true })
    .click();
  await expect
    .poll(() => postedActions.some((a) => a.action === "cli-probe"))
    .toBe(true);

  await editor.locator('[data-account="acct-cli-openai-1"]').click();
  const enabledSwitch = editor.getByRole("checkbox", { name: "Enabled" });
  await expect(enabledSwitch).toBeChecked();
  await enabledSwitch.uncheck();
  await expect
    .poll(() => postedActions.find((a) => a.action === "set-enabled"))
    .toEqual({
      action: "set-enabled",
      ref: "acct-cli-openai-1",
      disabled: true,
    });
  await expect(editor.getByRole("status")).toContainText("Disabled");
});

test("unified Settings Providers inline toggle and 3-step Add provider wizard work end-to-end", async ({
  page,
  isMobile,
}) => {
  await page.goto(url + "/#overview");
  await openOwnerSetup(
    page,
    JSON.parse(await readFile(join(dir, "claim-code.json"), "utf8")).code,
  );
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  const postedActions: Record<string, any>[] = [];
  let savedAccount: Record<string, any> | null = null;
  const baseConnection = await privateCLI(["ai-connection"]);
  const hostClis = [
    {
      provider: "google",
      binary: "antigravity",
      label: "Antigravity ACP",
      transport: "acp",
      version: "1.5.13",
      binaryPath: "/home/user/.t3/tools/antigravity-acp/antigravity",
      installed: true,
      authenticated: true,
      account: "Google AI Ultra",
      supportsDevice: false,
      loginCommand: "antigravity acp --login",
      installHint: "Install Antigravity desktop or run antigravity acp",
      models: ["gemini-4-neon", "gemini-3.8-flash"],
    },
  ];
  const rootPayload = () => ({
    ...baseConnection,
    ...(savedAccount ?? {}),
    osVaultAvailable: true,
    signInAvailable: true,
    liveCallsEnabled: true,
    hostClis,
    connections: savedAccount ? [savedAccount] : [],
  });

  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ json: rootPayload() });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, any>;
    postedActions.push(body);
    if (body.action === "cli-connect") {
      savedAccount = {
        ...baseConnection,
        ref: "acct-wiz-1",
        name: body.name || "Antigravity ACP",
        provider: body.provider || "google",
        authMethod: "cli",
        credentialMode: "cli",
        binary: body.binary || "antigravity",
        binaryPath: body.binaryPath || "",
        configDir: body.configDir || "",
        accentColor: body.accentColor || "",
        cliVersion: "1.5.13",
        disabled: false,
        osVaultAvailable: true,
        osProtected: true,
        locked: false,
        testOnly: false,
        liveCallsEnabled: true,
        model: "gemini-3.8-flash",
        catalog: {
          ...baseConnection.catalog,
          state: "ready",
          models: [
            {
              id: "gemini-3.8-flash",
              name: "Gemini 3.8 Flash",
              provider: "google",
              method: "cli",
              connectionRef: "acct-wiz-1",
              recommended: true,
              selectable: true,
              availability: "available",
            },
          ],
        },
      };
    } else if (body.action === "set-enabled" && savedAccount) {
      savedAccount = { ...savedAccount, disabled: !!body.disabled };
    }
    await route.fulfill({ json: rootPayload() });
  });

  if (isMobile) {
    await page
      .getByRole("button", { name: "Preferences", exact: true })
      .click();
  } else {
    await page
      .locator("#shell-rail")
      .getByRole("button", { name: "Settings", exact: true })
      .click();
  }

  const settingsDialog = page.getByRole("dialog", { name: /Settings \// });
  await settingsDialog
    .locator(".settings-modal-tabs")
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  await settingsDialog
    .getByRole("button", { name: "Add provider", exact: true })
    .click();

  const wizard = page.getByRole("dialog", {
    name: "Add provider",
    exact: true,
  });
  await expect(wizard).toBeVisible();
  await wizard
    .getByRole("group", { name: "Provider", exact: true })
    .getByRole("button", { name: /Google/ })
    .click();
  await wizard.getByRole("button", { name: "Next", exact: true }).click();

  await wizard.getByLabel("Label", { exact: true }).fill("Ultra ACP");
  await wizard
    .getByRole("group", { name: "Accent color", exact: true })
    .getByRole("button", { name: "#27c5da", exact: true })
    .click();
  await wizard.getByRole("button", { name: "Next", exact: true }).click();

  await expect(wizard.locator(".cli-status-card")).toContainText("v1.5.13");
  await expect(wizard.locator(".cli-status-card")).toContainText(
    "Google AI Ultra",
  );
  await wizard.getByLabel("Binary path", { exact: true }).fill("/opt/ag/acp");
  await wizard
    .getByLabel("Config directory", { exact: true })
    .fill("~/.config/ag-ultra");
  await wizard
    .getByRole("button", { name: "Add instance", exact: true })
    .click();

  await expect
    .poll(() => postedActions.find((a) => a.action === "cli-connect"))
    .toEqual({
      action: "cli-connect",
      provider: "google",
      binary: "antigravity",
      name: "Ultra ACP",
      binaryPath: "/opt/ag/acp",
      configDir: "~/.config/ag-ultra",
      accentColor: "#27c5da",
    });

  await expect(
    settingsDialog.locator(".settings-providers-list"),
  ).toContainText("Ultra ACP");
  await expect(
    settingsDialog.locator(".settings-providers-detail"),
  ).toContainText("/opt/ag/acp");

  const inlineSwitch = settingsDialog
    .locator(".settings-provider-row")
    .getByRole("checkbox", { name: "Enabled Ultra ACP", exact: true });
  await expect(inlineSwitch).toBeChecked();
  await inlineSwitch.uncheck();
  await expect
    .poll(() => postedActions.find((a) => a.action === "set-enabled"))
    .toEqual({ action: "set-enabled", ref: "acct-wiz-1", disabled: true });
  await expect(
    settingsDialog.locator(".settings-providers-detail"),
  ).toContainText("Disabled");
});

test("Add provider wizard Step 3 renders Codex shadowHome and launchArgs and shows them in Settings detail", async ({
  page,
  isMobile,
}) => {
  await page.goto(url + "/#overview");
  await openOwnerSetup(
    page,
    JSON.parse(await readFile(join(dir, "claim-code.json"), "utf8")).code,
  );
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  const postedActions: Record<string, any>[] = [];
  let savedAccount: Record<string, any> | null = null;
  const baseConnection = await privateCLI(["ai-connection"]);
  const hostClis = [
    {
      provider: "openai",
      binary: "codex",
      label: "Codex CLI",
      transport: "cli",
      version: "0.171.0",
      binaryPath: "/usr/local/bin/codex",
      installed: true,
      authenticated: true,
      account: "ChatGPT Pro",
      supportsDevice: true,
      loginCommand: "codex login --device-auth",
      installHint: "npm i -g @openai/codex",
      models: ["gpt-5.4", "gpt-5.4-mini"],
    },
    {
      provider: "anthropic",
      binary: "claude",
      label: "Claude Code CLI",
      transport: "cli",
      version: "2.1.329",
      binaryPath: "/usr/local/bin/claude",
      installed: true,
      authenticated: true,
      account: "Claude Max",
      supportsDevice: false,
      loginCommand: "claude auth login",
      installHint: "npm i -g @anthropic-ai/claude-code",
      models: ["claude-sonnet-4-6"],
    },
  ];
  const rootPayload = () => ({
    ...baseConnection,
    ...(savedAccount ?? {}),
    osVaultAvailable: true,
    signInAvailable: true,
    liveCallsEnabled: true,
    hostClis,
    connections: savedAccount ? [savedAccount] : [],
  });

  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ json: rootPayload() });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, any>;
    postedActions.push(body);
    if (body.action === "cli-connect") {
      savedAccount = {
        ...baseConnection,
        ref: "acct-codex-1",
        name: body.name || "Codex CLI",
        provider: body.provider || "openai",
        authMethod: "cli",
        credentialMode: "cli",
        binary: body.binary || "codex",
        binaryPath: body.binaryPath || "",
        configDir: body.configDir || "",
        shadowHome: body.shadowHome || "",
        launchArgs: body.launchArgs || "",
        accentColor: body.accentColor || "",
        cliVersion: "0.171.0",
        disabled: false,
        osVaultAvailable: true,
        osProtected: true,
        locked: false,
        testOnly: false,
        liveCallsEnabled: true,
        model: "gpt-5.4",
      };
    }
    await route.fulfill({ json: rootPayload() });
  });

  if (isMobile) {
    await page
      .getByRole("button", { name: "Preferences", exact: true })
      .click();
  } else {
    await page
      .locator("#shell-rail")
      .getByRole("button", { name: "Settings", exact: true })
      .click();
  }

  const settingsDialog = page.getByRole("dialog", { name: /Settings \// });
  await settingsDialog
    .locator(".settings-modal-tabs")
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  await settingsDialog
    .getByRole("button", { name: "Add provider", exact: true })
    .click();

  const wizard = page.getByRole("dialog", {
    name: "Add provider",
    exact: true,
  });
  await expect(wizard).toBeVisible();

  // Verify non-Codex CLI shows Launch arguments but hides Shadow home path.
  await wizard
    .getByRole("group", { name: "Provider", exact: true })
    .getByRole("button", { name: /Anthropic/ })
    .click();
  await wizard.getByRole("button", { name: "Next", exact: true }).click();
  await wizard.getByRole("button", { name: "Next", exact: true }).click();
  await expect(
    wizard.getByLabel("Launch arguments", { exact: true }),
  ).toBeVisible();
  await expect(
    wizard.getByLabel("Shadow home path", { exact: true }),
  ).toHaveCount(0);

  // Switch back to Codex and configure Shadow home path + Launch arguments.
  await wizard.getByRole("button", { name: "Back", exact: true }).click();
  await wizard.getByRole("button", { name: "Back", exact: true }).click();
  await wizard
    .getByRole("group", { name: "Provider", exact: true })
    .getByRole("button", { name: /OpenAI/ })
    .click();
  await wizard
    .getByRole("button", { name: "Configure manually", exact: true })
    .click();
  await wizard.getByLabel("Label", { exact: true }).fill("Codex Work");
  await wizard.getByRole("button", { name: "Next", exact: true }).click();

  await wizard.getByLabel("Config directory", { exact: true }).fill("~/.codex");
  await wizard
    .getByLabel("Shadow home path", { exact: true })
    .fill("~/.codex-fased/work");
  await wizard
    .getByLabel("Launch arguments", { exact: true })
    .fill("--enable web_search_request");
  await wizard
    .getByRole("button", { name: "Add instance", exact: true })
    .click();

  await expect
    .poll(() => postedActions.find((a) => a.action === "cli-connect"))
    .toEqual({
      action: "cli-connect",
      provider: "openai",
      binary: "codex",
      name: "Codex Work",
      configDir: "~/.codex",
      shadowHome: "~/.codex-fased/work",
      launchArgs: "--enable web_search_request",
    });

  const detail = settingsDialog.locator(".settings-providers-detail");
  await expect(detail).toContainText("~/.codex-fased/work");
  await expect(detail).toContainText("--enable web_search_request");
});

test("AddProviderWizard sends remote:true and accentColor on Remote OAuth sign-in-start", async ({
  page,
  isMobile,
}) => {
  await page.route("**/api/v1/health", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.connection = { location: "remote", transport: "loopback" };
    await route.fulfill({ response, json: body });
  });
  await page.goto(url + "/#overview");
  await openOwnerSetup(
    page,
    JSON.parse(await readFile(join(dir, "claim-code.json"), "utf8")).code,
  );
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  const postedActions: Record<string, any>[] = [];
  const baseConnection = await privateCLI(["ai-connection"]);
  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        json: {
          ...baseConnection,
          osVaultAvailable: true,
          signInAvailable: true,
          liveCallsEnabled: true,
          hostClis: [],
          connections: [],
        },
      });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, any>;
    postedActions.push(body);
    await route.fulfill({
      json: {
        ...baseConnection,
        osVaultAvailable: true,
        signInAvailable: true,
        liveCallsEnabled: true,
        hostClis: [],
        connections: [],
        signIn: {
          id: "si-wizard-remote-1",
          provider: body.provider || "openai",
          state: "waiting",
          url: "https://auth.openai.com/api/accounts/authorize?state=wiz-remote-1",
          expiresAt: Math.floor(Date.now() / 1000) + 600,
          remote: !!body.remote,
        },
      },
    });
  });

  if (isMobile) {
    await page
      .getByRole("button", { name: "Preferences", exact: true })
      .click();
  } else {
    await page
      .locator("#shell-rail")
      .getByRole("button", { name: "Settings", exact: true })
      .click();
  }

  const settingsDialog = page.getByRole("dialog", { name: /Settings \// });
  await settingsDialog
    .locator(".settings-modal-tabs")
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  await settingsDialog
    .getByRole("button", { name: "Add provider", exact: true })
    .click();

  const wizard = page.getByRole("dialog", {
    name: "Add provider",
    exact: true,
  });
  await expect(wizard).toBeVisible();
  await wizard
    .getByRole("group", { name: "Provider", exact: true })
    .getByRole("button", { name: /OpenAI/ })
    .click();
  await wizard
    .getByRole("button", { name: "Configure manually", exact: true })
    .click();

  await wizard.getByLabel("Label", { exact: true }).fill("Remote ChatGPT");
  await wizard
    .getByRole("group", { name: "Accent color", exact: true })
    .getByRole("button", { name: "#27c5da", exact: true })
    .click();
  await wizard.getByRole("button", { name: "Next", exact: true }).click();

  await wizard
    .getByRole("button", { name: "Continue with ChatGPT", exact: true })
    .click();

  await expect
    .poll(() => postedActions.find((a) => a.action === "sign-in-start"))
    .toEqual({
      action: "sign-in-start",
      provider: "openai",
      remote: true,
      name: "Remote ChatGPT",
      accentColor: "#27c5da",
    });
});
