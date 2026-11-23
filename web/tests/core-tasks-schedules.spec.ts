import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import {
  browserRequest,
  openExactNoticeResult,
} from "../../scripts/staged-package-browser.mjs";
import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { once } from "node:events";
import { createHash } from "node:crypto";
import {
  installCoreFixture,
  captureScreenshot,
  exactReplyReview,
  choosePasswordSignIn,
  revealControl,
  openAISchedules,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  start,
  stop,
  privateCLI,
  fixtureProviderSelection,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("reviewed recurring tasks preserve named zones bounds CLI results pause and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(75000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
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
  const before = await privateCLI(["agents"]);
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await page.getByLabel("Task name", { exact: true }).fill("Weekday journal");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review saved positions and task results");
  await revealControl(page, page.getByLabel("Schedule", { exact: true }));
  await page.getByLabel("Schedule", { exact: true }).selectOption("cron");
  await page.getByLabel("Recurrence", { exact: true }).selectOption("weekdays");
  await page.getByLabel("Time", { exact: true }).fill("09:30");
  await page
    .getByLabel("Time zone", { exact: true })
    .selectOption("Asia/Seoul");
  await page.getByLabel("Maximum evaluations", { exact: true }).fill("3");
  await captureScreenshot(page, {
    path: `.evidence/cron-editor-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  const review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Asia/Seoul");
  await expect(review).toContainText(
    "Missing times skipped; repeated times run once.",
  );
  expect(await privateCLI(["agents"])).toEqual(before);
  await captureScreenshot(page, {
    path: `.evidence/cron-review-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks.length)
    .toBe(1);
  let v = await privateCLI(["agents"]);
  const task = v.tasks[0];
  expect(task.calendar).toMatchObject({
    version: "cron-v1",
    expression: "30 9 * * 1-5",
    timeZone: "Asia/Seoul",
  });
  expect(task.maxRuns).toBe(3);
  expect(task.nextAt).toBe(task.calendar.at);
  await page
    .getByRole("button", { name: "Run now: Weekday journal", exact: true })
    .first()
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].runs)
    .toBe(1);
  v = await privateCLI(["agents"]);
  expect(v.runs).toHaveLength(1);
  expect(v.tasks[0].nextAt).toBeGreaterThan(task.nextAt);
  await page
    .getByRole("button", { name: "Pause: Weekday journal", exact: true })
    .first()
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].state)
    .toBe("paused");
  v = await privateCLI(["agents"]);
  await captureScreenshot(page, {
    path: `.evidence/cron-result-${test.info().project.name}.png`,
    fullPage: false,
  });
  const address = new URL(url);
  await stop();
  await start(false, address.host);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  expect(await privateCLI(["agents"])).toEqual(v);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.tasks).toHaveLength(0);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  expect(health.capabilities.aiConnections).toBe(true);
  for (const key of ["financialExecution", "walletSigner", "updater"])
    expect(health.capabilities[key]).toBe(false);
  if (isMobile)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      JSON.stringify(
        await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
          offenders: [...document.querySelectorAll("body *")]
            .filter(
              (el) =>
                el.getBoundingClientRect().right > innerWidth + 1 &&
                getComputedStyle(el).display !== "none",
            )
            .slice(0, 6)
            .map((el) => ({
              tag: el.tagName,
              classes: el.className,
              right: el.getBoundingClientRect().right,
            })),
        })),
      ),
    ).toBe(true);
  expect(external).toEqual([]);
});

test.describe("calendar reminders", () => {
  test.use({ timezoneId: "America/Chicago" });
  test("reviewed calendar reminder shares UI CLI date and survives paused restart", async ({
    page,
  }) => {
    test.setTimeout(75000);
    const external: string[] = [];
    page.on("request", (r) => {
      if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
        external.push(r.url());
    });
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
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin!.end(fixturePassword + "\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    await page.goto(url + "/#agents/tasks");
    const newTask = page.getByRole("button", { name: "New task", exact: true });
    await newTask.hover();
    const contrast = await newTask.evaluate((el) => {
      const s = getComputedStyle(el);
      const luminance = (color: string) => {
        const channels = color
          .match(/[\d.]+/g)!
          .slice(0, 3)
          .map(Number)
          .map((x) => {
            const v = x / 255;
            return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
          });
        return (
          channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
        );
      };
      const a = luminance(s.color),
        b = luminance(s.backgroundColor);
      return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
    });
    expect(contrast).toBeGreaterThanOrEqual(4.5);
    await newTask.click();
    await page
      .getByLabel("Task name", { exact: true })
      .fill("Calendar journal");
    await page
      .getByLabel("Reminder text", { exact: true })
      .fill("Review the saved fixture journal.");
    await revealControl(page, page.getByLabel("Schedule", { exact: true }));
    await page
      .getByRole("combobox", { name: "Schedule", exact: true })
      .selectOption("calendar");
    await expect(page.getByLabel("Time zone", { exact: true })).toHaveValue(
      "America/Chicago",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      JSON.stringify(
        await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
          offenders: [...document.querySelectorAll("body *")]
            .filter(
              (el) =>
                el.getBoundingClientRect().right > innerWidth + 1 &&
                getComputedStyle(el).display !== "none",
            )
            .slice(0, 6)
            .map((el) => ({
              tag: el.tagName,
              classes: el.className,
              right: el.getBoundingClientRect().right,
            })),
        })),
      ),
    ).toBe(true);
    await page.evaluate(() => scrollTo(0, 0));
    await captureScreenshot(page, {
      path: `.evidence/calendar-editor-${test.info().project.name}.png`,
      fullPage: false,
    });
    await page
      .getByLabel("Date & time", { exact: true })
      .fill("2027-03-14T02:30");
    await page
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    await expect(page.getByRole("alert")).toContainText("does not exist");
    await page
      .getByLabel("Date & time", { exact: true })
      .fill("2026-11-01T01:30");
    await page
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    await expect(page.getByRole("alert")).toContainText("occurs twice");
    const before = await privateCLI(["agents"]);
    expect(before.tasks).toHaveLength(0);
    await page.getByLabel("Time zone", { exact: true }).selectOption("UTC");
    const at = Math.floor((Date.now() / 1000 + 3600) / 60) * 60;
    await page
      .getByLabel("Date & time", { exact: true })
      .fill(new Date(at * 1000).toISOString().slice(0, 16));
    await page
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    const review = page.getByRole("complementary", {
      name: "Review task",
      exact: true,
    });
    await expect(review).toContainText("UTC");
    await expect(
      review.getByText("No AI or wallet access", { exact: true }),
    ).toBeVisible();
    expect((await privateCLI(["agents"])).revision).toBe(before.revision);
    await page.evaluate(() => scrollTo(0, 0));
    await captureScreenshot(page, {
      path: `.evidence/calendar-review-${test.info().project.name}.png`,
      fullPage: false,
    });
    await review
      .getByRole("button", { name: "Create task", exact: true })
      .click();
    const table = page.getByRole("table", { name: "Tasks", exact: true });
    await expect(table).toContainText("Calendar journal");
    let v = await privateCLI(["agents"]);
    const id = v.tasks[0].id;
    expect(v.tasks[0]).toMatchObject({
      calendar: { version: "once-at-v1", at, timeZone: "UTC" },
      nextAt: at,
      intervalSeconds: 0,
      maxRuns: 1,
      runs: 0,
    });
    await page
      .getByRole("button", { name: "Pause: Calendar journal", exact: true })
      .click();
    await expect(table).toContainText("Paused");
    await stop();
    await start();
    await page.goto(url + "/#agents/tasks");
    await choosePasswordSignIn(page);
    await page.getByLabel("Owner password").fill(fixturePassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(table).toContainText("Paused");
    await page
      .getByRole("button", { name: "Resume: Calendar journal", exact: true })
      .click();
    await expect(table).toContainText("Scheduled");
    v = await privateCLI(["agents"]);
    expect(v.tasks[0].nextAt).toBe(at);
    expect(v.tasks[0].calendar.at).toBe(at);
    expect(v.runs).toHaveLength(0);
    await page
      .getByRole("button", { name: "Run now: Calendar journal", exact: true })
      .click();
    await expect(table).toContainText("Completed");
    await page
      .getByRole("button", { name: "Result: Calendar journal", exact: true })
      .click();
    await expect(page.getByLabel("Run results", { exact: true })).toContainText(
      "Calendar journal: Review the saved fixture journal.",
    );
    v = await privateCLI(["agents"]);
    expect(v.tasks[0]).toMatchObject({
      id,
      nextAt: at,
      runs: 1,
      state: "completed",
    });
    expect(v.runs).toHaveLength(1);
    expect(v.providerCallsEnabled).toBe(true);
    expect(v.financialExecution).toBe(false);
    const foreign = await privateCLI([
      "agents",
      "--profile",
      "news-researcher",
    ]);
    expect(foreign.tasks).toHaveLength(0);
    expect(foreign.runs).toHaveLength(0);
    expect(external).toEqual([]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      JSON.stringify(
        await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
          offenders: [...document.querySelectorAll("body *")]
            .filter(
              (el) =>
                el.getBoundingClientRect().right > innerWidth + 1 &&
                getComputedStyle(el).display !== "none",
            )
            .slice(0, 6)
            .map((el) => ({
              tag: el.tagName,
              classes: el.className,
              right: el.getBoundingClientRect().right,
            })),
        })),
      ),
    ).toBe(true);
    await page.evaluate(() => scrollTo(0, 0));
    await captureScreenshot(page, {
      path: `.evidence/calendar-result-${test.info().project.name}.png`,
      fullPage: false,
    });
  });
});

test("compact Tasks reviews without writes and preserves bounded runs across UI CLI restart", async ({
  page,
}) => {
  test.setTimeout(75000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
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
  await page.goto(url + "/#agents/tasks");
  await expect(page.getByText("No tasks", { exact: true })).toBeVisible();
  await page.evaluate(() => scrollTo(0, 0));
  await captureScreenshot(page, {
    path: `.evidence/tasks-empty-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await page.getByLabel("Task name", { exact: true }).fill("Journal review");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the saved fixture results.");
  await revealControl(page, page.getByLabel("Schedule", { exact: true }));
  await page
    .getByRole("combobox", { name: "Schedule", exact: true })
    .selectOption("repeat");
  await page.getByLabel("Every", { exact: true }).fill("15");
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  const review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Every 15 minutes · 3 runs");
  const before = await privateCLI(["agents"]);
  expect(before.tasks).toHaveLength(0);
  expect(before.runs).toHaveLength(0);
  await page.evaluate(() => scrollTo(0, 0));
  await captureScreenshot(page, {
    path: `.evidence/tasks-review-${test.info().project.name}.png`,
    fullPage: false,
  });
  await review.getByRole("button", { name: "Edit task", exact: true }).click();
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the corrected fixture results.");
  await expect(
    page.getByRole("button", { name: "Create task", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await expect(review).toContainText("Review the corrected fixture results.");
  await privateCLI(["agents", "--request-stdin"], {
    action: "session-create",
    profileId: before.profileId,
    revision: before.revision,
    name: "Concurrent fixture conversation",
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(review.getByRole("alert")).toContainText("Records changed");
  await review
    .getByRole("button", { name: "Review again", exact: true })
    .click();
  await expect(review.getByRole("alert")).toHaveCount(0);
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  const table = page.getByRole("table", { name: "Tasks", exact: true });
  await expect(table).toContainText("Journal review");
  let v = await privateCLI(["agents"]);
  const id = v.tasks[0].id;
  expect(v.tasks[0]).toMatchObject({
    text: "Review the corrected fixture results.",
    intervalSeconds: 900,
    maxRuns: 3,
    runs: 0,
    state: "scheduled",
  });
  expect(v.revision).toBe(before.revision + 2);
  await page
    .getByRole("button", { name: "Run now: Journal review", exact: true })
    .click();
  await expect(table).toContainText("1 / 3");
  const details = page.getByRole("complementary", {
    name: "Task details",
    exact: true,
  });
  await expect(details.locator(".task-run")).toContainText(
    "Journal review: Review the corrected fixture results.",
  );
  await page
    .getByRole("button", { name: "Pause: Journal review", exact: true })
    .click();
  await expect(table).toContainText("Paused");
  await expect(
    page.getByRole("button", { name: "Run now: Journal review", exact: true }),
  ).toBeDisabled();
  const paused = await privateCLI(["agents"]);
  expect(paused.runs).toHaveLength(1);
  await page.evaluate(() => scrollTo(0, 0));
  await captureScreenshot(page, {
    path: `.evidence/tasks-paused-${test.info().project.name}.png`,
    fullPage: false,
  });
  await stop();
  await start();
  await page.goto(url + "/#agents/tasks");
  await choosePasswordSignIn(page);
  await page.getByLabel("Owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(table).toContainText("Paused");
  expect((await privateCLI(["agents"])).runs).toEqual(paused.runs);
  await page
    .getByRole("button", { name: "Resume: Journal review", exact: true })
    .click();
  await expect(table).toContainText("Scheduled");
  v = await privateCLI(["agents"]);
  expect(v.tasks[0].nextAt).toBeGreaterThan(Date.now() / 1000 + 895);
  await page
    .getByRole("button", { name: "Result: Journal review", exact: true })
    .click();
  await expect(page.getByLabel("Run results", { exact: true })).toContainText(
    "Journal review: Review the corrected fixture results.",
  );
  await expect(
    page
      .getByLabel("Run results", { exact: true })
      .getByText("Run details", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Run results", { exact: true }).locator("details"),
  ).not.toHaveAttribute("open");
  const foreign = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(foreign.tasks).toHaveLength(0);
  expect(foreign.runs).toHaveLength(0);
  expect(v.tasks[0].id).toBe(id);
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.evaluate(() => scrollTo(0, 0));
  await captureScreenshot(page, {
    path: `.evidence/tasks-result-${test.info().project.name}.png`,
    fullPage: false,
  });
});

test("task inbox opens the exact named run without writes across profiles and reload", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
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
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  for (const name of ["Earlier saved reminder", "Later saved reminder"]) {
    const v = await command({
      action: "task-create",
      name,
      text: `Review ${name}`,
      intervalSeconds: 3600,
      maxRuns: 1,
    });
    await command({ action: "task-run", id: v.tasks.at(-1).id });
  }
  let baseline = await read();
  await page.goto(url + "/#agents/tasks");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  const runCard = (id: string) =>
    page.locator(".memory-card").filter({ hasText: `Run ${id}` });
  for (const i of [1, 0]) {
    const run = baseline.runs[i];
    const task = baseline.tasks.find((t: any) => t.id === run.taskId);
    await page.getByRole("button", { name: "Results", exact: true }).click();
    await expect(
      runCard(run.id).getByText(task.name, { exact: true }),
    ).toBeVisible();
    const notice = page.getByRole("article", {
      name: `Task notification ${run.id}`,
      exact: true,
    });
    await page.getByRole("button", { name: "Inbox", exact: true }).click();
    await notice
      .getByRole("button", { name: "View result", exact: true })
      .click();
    await expect(runCard(run.id)).toBeFocused();
    expect(await read()).toEqual(baseline);
  }
  const firstRun = baseline.runs[0];
  const firstNotice = page.getByRole("article", {
    name: `Task notification ${firstRun.id}`,
    exact: true,
  });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "Mark read", exact: true })
    .click();
  await expect.poll(async () => (await read()).notices[0].read).toBe(true);
  const afterRead = await read();
  expect(afterRead.runs).toEqual(baseline.runs);
  expect(afterRead.tasks).toEqual(baseline.tasks);
  baseline = afterRead;
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "View result", exact: true })
    .click();
  expect(await read()).toEqual(baseline);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(
    page.getByText("No task notifications.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "View result", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("Earlier saved reminder", { exact: true }),
  ).toHaveCount(0);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(firstNotice).toBeVisible();
  let fault: "missing" | "foreign" = "missing";
  const routePattern = /\/api\/v1\/agents\?/;
  await page.route(routePattern, async (route) => {
    const response = await route.fetch();
    const v = await response.json();
    if (v.profileId === profileId) {
      if (fault === "missing") v.notices[0].runId = "unavailable-run";
      else v.runs[0].profileId = "news-researcher";
    }
    await route.fulfill({ response, json: v });
  });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", {
      name: "Result unavailable",
      exact: true,
    }),
  ).toBeDisabled();
  fault = "foreign";
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Results", exact: true }).click();
  await expect(
    runCard(firstRun.id).getByText("Task unavailable", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", {
      name: "Result unavailable",
      exact: true,
    }),
  ).toBeDisabled();
  expect(await read()).toEqual(baseline);
  await page.unroute(routePattern);
  await page.reload();
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", { name: "View result", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "View result", exact: true })
    .click();
  await expect(runCard(firstRun.id)).toBeFocused();
  expect(await read()).toEqual(baseline);
  expect(external).toEqual([]);
  await captureScreenshot(page, {
    path: `.evidence/task-inbox-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("reminder task draft shares inert inspection edited review and bounded creation across UI CLI and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const origins = new Set([new URL(url).origin]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = async () => {
    const child = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    child.stdin!.end(fixturePassword + "\n");
    expect((await once(child, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({
    action: "task-create",
    name: "Saved daily review",
    text: "Read retained results",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  v = await command({ action: "task-run", id: v.tasks[0].id });
  const original = v.tasks[0];
  const snapshot = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--reminder-draft",
    original.id,
  ]);
  expect(snapshot.source).toEqual(original);
  expect(snapshot.sourceSha256).toBe(
    createHash("sha256").update(JSON.stringify(original)).digest("hex"),
  );
  expect(await read()).toEqual(v);
  await page.goto(url + "/#agents/tasks");
  const profile = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  const reuse = page.getByRole("button", {
    name: "Reuse: Saved daily review",
    exact: true,
  });
  await reuse.click();
  const panel = page.getByRole("region", {
    name: "Reuse reminder",
    exact: true,
  });
  await expect(
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
  ).toHaveValue(original.name);
  await panel.getByText("Saved reminder source", { exact: true }).click();
  await expect(panel.locator("pre")).toContainText(snapshot.sourceSha256);
  await panel.getByRole("button", { name: "Discard reminder draft" }).click();
  expect(await read()).toEqual(v);
  await reuse.click();
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder name", exact: true })
    .fill("Reused saved review");
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder text", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder text", exact: true })
    .fill("Review the saved journal again");
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", {
      name: "Create task",
      exact: true,
    }),
  ).toBeVisible();
  expect(await read()).toEqual(v);
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder text", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder text", exact: true })
    .fill("Edited after the first review");
  await expect(panel.getByRole("button", { name: "Create task" })).toHaveCount(
    0,
  );
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", { name: "Create task" }),
  ).toBeVisible();
  v = await command({
    action: "profile-update",
    name: "Trading Analyst",
    ...(await fixtureProviderSelection("xai")),
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel.getByRole("alert")).toHaveText(
    "Records changed. Reload the reminder to review again.",
  );
  await expect(panel.getByRole("button", { name: "Create task" })).toHaveCount(
    0,
  );
  await panel
    .getByRole("button", { name: "Reload reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
  ).toHaveValue(original.name);
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder name", exact: true })
    .fill("Reused saved review");
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder text", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder text", exact: true })
    .fill("Review the saved journal again");
  await revealControl(
    page,
    panel.getByRole("spinbutton", { name: "Interval in seconds", exact: true }),
  );
  await panel
    .getByRole("spinbutton", { name: "Interval in seconds", exact: true })
    .fill("120");
  await revealControl(
    page,
    panel.getByRole("spinbutton", { name: "Maximum evaluations", exact: true }),
  );
  await panel
    .getByRole("spinbutton", { name: "Maximum evaluations", exact: true })
    .fill("1");
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", { name: "Create task" }),
  ).toBeVisible();
  expect(await read()).toEqual(v);
  await panel.getByRole("button", { name: "Create task", exact: true }).click();
  await expect(panel).toHaveCount(0);
  v = await read();
  expect(v.tasks).toHaveLength(2);
  expect(v.tasks[0]).toEqual(original);
  const copy = v.tasks[1];
  expect(copy).toMatchObject({
    name: "Reused saved review",
    text: "Review the saved journal again",
    intervalSeconds: 120,
    maxRuns: 1,
    runs: 0,
    state: "scheduled",
    botId: "",
  });
  expect(copy.ai).toBeUndefined();
  expect(copy.news).toBeUndefined();
  expect(copy.wen).toBeUndefined();
  expect(v.runs).toHaveLength(1);
  await page
    .getByRole("button", { name: "Run now: Reused saved review", exact: true })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(2);
  v = await read();
  expect(v.tasks[0]).toEqual(original);
  expect(v.tasks[1]).toMatchObject({ state: "completed", runs: 1, maxRuns: 1 });
  const foreign = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("News Researcher");
  await expect(
    page.getByRole("button", { name: "Reuse: Saved daily review" }),
  ).toHaveCount(0);
  await expect(
    privateCLI([
      "agents",
      "--profile",
      "news-researcher",
      "--reminder-draft",
      original.id,
    ]),
  ).rejects.toThrow();
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await page.goto(url + "/#agents/tasks");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await login();
  const reopened = await read();
  for (const field of ["tasks", "runs", "notices", "aiRequests"])
    expect(reopened[field]).toEqual(v[field]);
  expect(reopened.aiRequests).toHaveLength(0);
  expect(reopened.providerCallsEnabled).toBe(true);
  expect(reopened.financialExecution).toBe(false);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await page
    .getByRole("button", { name: "Reuse: Reused saved review", exact: true })
    .click();
  await expect(
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
  ).toHaveValue(copy.name);
  await expect(panel.getByRole("button", { name: "Create task" })).toHaveCount(
    0,
  );
  await panel.getByText("Saved reminder source", { exact: true }).click();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await captureScreenshot(page, {
    path: `.evidence/reminder-task-draft-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await panel.getByRole("button", { name: "Discard reminder draft" }).click();
  expect(external).toEqual([]);
  expect((await read()).tasks).toEqual(v.tasks);
});

test("bounded AI schedules share consent and execution with CLI, survive restart and keep paper inputs fixed", async ({
  page,
}) => {
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
  await page.getByLabel("Conversation name").fill("Scheduled context");
  await page.getByRole("button", { name: "Create conversation" }).click();
  await expect(
    page.getByRole("heading", { name: "Scheduled context", exact: true }),
  ).toBeVisible();
  let v = await privateCLI(["agents"]);
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    profileId: v.profileId,
    action: "bot-create",
    name: "Fixed task paper",
    quantity: 2,
    priceCents: 5,
    budgetCents: 20,
  });
  const bot = v.bots[0].id;
  await page.goto(url + "/#agents/tasks");
  await openAISchedules(page);
  // Hash navigation preserves the UI's cache. Reload after this external CLI
  // write so the task review uses the same current records.
  await page.reload();
  await openAISchedules(page);
  await openAISchedules(page);
  const region = page.getByRole("region", {
    name: "Scheduled AI steps",
    exact: true,
  });
  await expect(
    region.getByLabel("Step after validated AI response").getByRole("option", {
      name: "Fixed paper evaluation · Fixed task paper",
    }),
  ).toHaveCount(1);
  await revealControl(page, region.getByLabel("AI task title"), "");
  await region.getByLabel("AI task title").fill("Two bounded AI evaluations");
  await revealControl(page, region.getByLabel("AI analysis prompt"), "");
  await region
    .getByLabel("AI analysis prompt")
    .fill("Compare fixed paper inputs");
  await revealControl(
    page,
    region.getByLabel("Step after validated AI response"),
    "",
  );
  await region.getByLabel("Step after validated AI response").selectOption(bot);
  await region.getByRole("button", { name: "Prepare AI task review" }).click();
  const task = region.getByRole("article", {
    name: "AI task Two bounded AI evaluations",
  });
  await expect(task.getByText("review", { exact: true })).toBeVisible();
  const authorize = task.getByRole("button", {
    name: "Authorize test schedule",
  });
  await expect(authorize).toBeDisabled();
  await task.getByText("Review AI task disclosure").click();
  await expect(task.locator("pre")).toContainText('"store":false');
  await revealControl(
    page,
    task.getByLabel("Allow this frozen context, schedule and limits"),
    "",
  );
  await task
    .getByLabel("Allow this frozen context, schedule and limits")
    .check();
  await authorize.click();
  await expect(task.getByText("scheduled", { exact: true })).toBeVisible();
  await task.getByRole("button", { name: "Evaluate now" }).click();
  await page.goto(url + "/#overview");
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.tasks[0].runs;
    })
    .toBe(1);
  expect(v.bots[0].spentCents).toBe(10);
  expect(v.aiRequests).toHaveLength(1);
  const taskId = v.tasks[0].id;
  await stop();
  await start(true);
  v = await privateCLI(["agents"]);
  await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    profileId: v.profileId,
    action: "task-run",
    id: taskId,
  });
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.tasks[0].state;
    })
    .toBe("completed");
  expect(v.bots[0].spentCents).toBe(20);
  expect(v.runs).toHaveLength(2);
  expect(v.aiRequests).toHaveLength(2);
  expect(v.aiRequests[0].id).not.toBe(v.aiRequests[1].id);
  expect(v.tasks).toHaveLength(1);
  expect(v.providerCallsEnabled).toBe(false);
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
  await page.goto(url + "/#agents/tasks");
  await openAISchedules(page);
  await expect(task.getByText("completed", { exact: true })).toBeVisible();
  await expect(task).toContainText("2/2 evaluations");
  await expect(task).toContainText(
    "No model, market feed or wallet was contacted.",
  );
  await revealControl(page, region.getByLabel("AI task title"), "");
  await region.getByLabel("AI task title").fill("Cancelled schedule");
  await revealControl(page, region.getByLabel("AI analysis prompt"), "");
  await region.getByLabel("AI analysis prompt").fill("No call needed");
  await region.getByRole("button", { name: "Prepare AI task review" }).click();
  const cancelled = region.getByRole("article", {
    name: "AI task Cancelled schedule",
  });
  await cancelled.getByRole("button", { name: "Cancel AI schedule" }).click();
  await expect(cancelled.getByText("cancelled", { exact: true })).toBeVisible();
  await mkdir(".evidence/p5-ai-tasks", { recursive: true });
  await captureScreenshot(page, {
    path: `.evidence/p5-ai-tasks/tasks-${test.info().project.name}.png`,
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});

test("chat reminder drafts require shared UI and CLI review before scheduling and survive restart without AI", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
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
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) => {
    const v = await read();
    return privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: v.revision,
      ...c,
    });
  };
  let v = await command({
    action: "session-create",
    name: "Reminder planning",
  });
  const sessionId = v.sessions[0].id;
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/review");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const ai = page.getByRole("region", { name: "Optional AI request review" });
  await revealControl(page, ai.getByLabel("Message", { exact: true }), "");
  await ai
    .getByLabel("Message", { exact: true })
    .fill("Draft a reminder to review saved research");
  await exactReplyReview(page);
  await ai.getByRole("button", { name: /Send|Prepare/ }).click();
  const request = ai.locator(".ai-request").last();
  await revealControl(
    page,
    request.getByLabel("I reviewed this context and these limits"),
    "",
  );
  await request.getByLabel("I reviewed this context and these limits").check();
  await request
    .getByRole("button", { name: "Run preview", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).aiRequests[0]?.state)
    .toBe("completed");
  const card = request.locator(".ai-action-card");
  await card.getByRole("button", { name: "Keep draft", exact: true }).click();
  expect((await read()).tasks).toHaveLength(0);
  await card.getByRole("button", { name: "Review task", exact: true }).click();
  const review = card.getByRole("region", { name: /Reminder task review:/ });
  await revealControl(
    page,
    review.getByLabel("Reminder task name", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reminder task name", { exact: true })
    .fill("Review my research");
  await revealControl(
    page,
    review.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reminder text", { exact: true })
    .fill("Read the saved headlines and my notes");
  await review.getByLabel("Unit", { exact: true }).selectOption("3600");
  await review.getByLabel("First run in", { exact: true }).fill("1");
  await revealControl(page, review.getByLabel("Maximum reminder runs"), "");
  await review.getByLabel("Maximum reminder runs").fill("2");
  await review
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  const create = review.getByRole("button", {
    name: "Create task",
    exact: true,
  });
  await expect(create).toBeDisabled();
  await expect(review).toContainText("No task has been created");
  expect((await read()).tasks).toHaveLength(0);
  await review
    .getByRole("button", { name: "Cancel reminder review", exact: true })
    .click();
  expect((await read()).tasks).toHaveLength(0);
  await card.getByRole("button", { name: "Review task", exact: true }).click();
  await review
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  await expect(review).toContainText("No task has been created");
  const beforeNote = (await read()).revision;
  // Idle refresh runs every five seconds, equal to the assertion budget.
  // Observe the real updated workspace response before checking its render;
  // do not race an arbitrary phase of the timer or lengthen the assertion.
  const workspaceRefreshed = page.waitForResponse(async (response) => {
    if (
      response.request().method() !== "GET" ||
      new URL(response.url()).pathname !== "/api/v1/agents" ||
      response.status() !== 200
    )
      return false;
    const value = await response.json();
    return value.profileId === profileId && value.revision > beforeNote;
  });
  await command({
    action: "memory-put",
    text: "An unrelated owner note",
    source: "owner",
  });
  await workspaceRefreshed;
  await expect(review).toContainText("Workspace changed or review expired");
  await expect(create).toBeDisabled();
  await review
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  await revealControl(
    page,
    review.getByLabel("I reviewed this reminder text and schedule"),
    "",
  );
  await review.getByLabel("I reviewed this reminder text and schedule").check();
  await revealControl(
    page,
    review.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reminder text", { exact: true })
    .fill("Edited: read saved research, no external actions");
  await expect(create).toHaveCount(0);
  await review
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  await revealControl(
    page,
    review.getByLabel("I reviewed this reminder text and schedule"),
    "",
  );
  await review.getByLabel("I reviewed this reminder text and schedule").check();
  await create.click();
  await expect(card.getByRole("status")).toContainText(
    "Reminder task created: Review my research",
  );
  v = await read();
  expect(v.tasks).toHaveLength(1);
  expect(v.runs).toHaveLength(0);
  const first = v.tasks[0];
  expect(first.text).toBe("Edited: read saved research, no external actions");
  expect(first.intervalSeconds).toBe(3600);
  expect(first.maxRuns).toBe(2);
  expect(first.botId).toBe("");
  expect(first.ai).toBeUndefined();
  expect(first.news).toBeUndefined();
  expect(first.wen).toBeUndefined();
  v = await command({ action: "task-pause", id: first.id });
  expect(
    v.tasks.find((task: { id: string }) => task.id === first.id).state,
  ).toBe("paused");
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(
    page.getByRole("button", {
      name: "Resume: Review my research",
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Resume: Review my research", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Run now: Review my research", exact: true })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(1);
  v = await command({ action: "task-pause", id: first.id });
  expect(v.notices).toHaveLength(1);
  expect(v.aiRequests).toHaveLength(1);
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Draft another reminder",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const secondRequest = v.aiRequests.at(-1);
  await command({
    action: "ai-start",
    id: secondRequest.id,
    consentDigest: secondRequest.digest,
  });
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  v = await read();
  const source = v.aiRequests.at(-1).cards[0];
  const stale = v.revision;
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Scoped research",
    ...(await fixtureProviderSelection("xai")),
  });
  await stop();
  await start(false);
  await page.goto(url + "/#agents/review");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const proposal = {
    action: "ai-reminder-review",
    id: source.id,
    name: "CLI reviewed reminder",
    text: "Follow up on saved notes",
    intervalSeconds: 600,
    maxRuns: 1,
  };
  await expect(
    privateCLI(["agents", "--request-stdin"], {
      ...proposal,
      profileId,
      revision: stale,
    }),
  ).rejects.toThrow();
  await expect(
    command({ ...proposal, profileId: "news-researcher" }),
  ).rejects.toThrow();
  const before = await read();
  v = await command(proposal);
  expect(v.revision).toBe(before.revision);
  expect(v.tasks).toHaveLength(1);
  const reviewed = v.reminderReview;
  const creation = {
    ...proposal,
    action: "ai-reminder-create",
    expiresAt: reviewed.expiresAt,
    consentDigest: reviewed.digest,
  };
  await expect(
    command({ ...creation, text: "unreviewed edit" }),
  ).rejects.toThrow();
  v = await command(creation);
  expect(v.tasks).toHaveLength(2);
  await expect(command(creation)).rejects.toThrow(/already created/);
  expect(v.tasks[1].state).toBe("scheduled");
  expect(v.tasks[1].text).toBe(proposal.text);
  // Synchronize the external CLI write through the existing profile refresh.
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByText("Reminder task created: CLI reviewed reminder.", {
      exact: true,
    }),
  ).toBeVisible();
  await command({ action: "task-run", id: v.tasks[1].id });
  await expect(
    command({ action: "task-run", id: v.tasks[1].id }),
  ).rejects.toThrow();
  v = await read();
  expect(v.runs).toHaveLength(2);
  expect(v.notices).toHaveLength(2);
  expect(v.aiRequests).toHaveLength(2);
  expect(v.aiFixtureAvailable).toBe(false);
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  expect(v.bots).toHaveLength(0);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.tasks).toHaveLength(0);
  expect(other.aiRequests).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await mkdir(".evidence/chat-reminders", { recursive: true });
  await captureScreenshot(page, {
    path: `.evidence/chat-reminders/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("task run chat shares historical UI and CLI capture, explicit fixture explanation and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
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
  const login = async () => {
    const c = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
    c.stdin!.end(fixturePassword + "\n");
    expect((await once(c, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({
    action: "task-create",
    name: "Journal review",
    text: "Compare retained sources",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  v = await command({ action: "task-run", id: v.tasks[0].id });
  const plain = v.runs[0];
  const source = {
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  };
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Results", exact: true }).click();
  const historicalRunCard = page
    .locator(".memory-card")
    .filter({ hasText: `Run ${plain.id}` });
  await expect(
    historicalRunCard.getByRole("button", { name: "Discuss", exact: true }),
  ).toHaveCount(0);
  await historicalRunCard
    .getByRole("button", { name: "Open thread", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  await expect(page.locator(".v2-message-assistant").last()).toContainText(
    "Journal review",
  );
  await expect(page.locator(".v2-message-assistant").last()).toContainText(
    "Compare retained sources",
  );
  await expect(page.locator(".v2-message-assistant").last()).toContainText(
    plain.id,
  );
  v = await read();
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(source);
  v = await command({ action: "task-run-explain", id: plain.id });
  const snapshot = v.messages.at(-1);
  const [body, hash] = snapshot.text.split("\nSnapshot SHA-256: ");
  expect(hash).toBe(createHash("sha256").update(body).digest("hex"));
  expect(snapshot.text).toContain(plain.id);
  expect(snapshot.text).toContain("Compare retained sources");
  expect(v.aiRequests).toHaveLength(0);
  const stale = {
    action: "task-run-explain",
    profileId,
    revision: v.revision - 1,
    id: plain.id,
  };
  await expect(
    privateCLI(["agents", "--request-stdin"], stale),
  ).rejects.toThrow();
  await expect(
    command({
      action: "task-run-explain",
      profileId: "news-researcher",
      id: plain.id,
    }),
  ).rejects.toThrow();
  expect((await read()).messages).toHaveLength(1);
  // Saving a V2 memory from the chat message persists without altering core tasks/runs.
  await page
    .locator(".v2-message-assistant")
    .last()
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const memoryDialog = page.getByRole("dialog", {
    name: "Memory",
    exact: true,
  });
  await memoryDialog
    .getByLabel("Note", { exact: true })
    .fill("Review saved reminder outcomes before planning");
  await memoryDialog
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memoryDialog).toHaveCount(0);
  await expect
    .poll(async () => {
      const sessionToken = await page.evaluate(() =>
        sessionStorage.getItem("fased.core.session.v1"),
      );
      const res = await page.request.get(
        `${url}/api/v1/agents/v2/memories?profileId=${encodeURIComponent(profileId)}`,
        {
          headers: sessionToken
            ? { Authorization: `Bearer ${sessionToken}` }
            : undefined,
        },
      );
      expect(res.ok()).toBe(true);
      return (await res.json()).memories.length;
    })
    .toBe(1);
  // Prepare disclosure is inert; consent is a separate command.
  v = await command({
    action: "ai-prepare",
    id: snapshot.sessionId,
    text: "Explain this historical run",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const explanation = v.aiRequests.at(-1);
  expect(explanation.state).toBe("prepared");
  expect(explanation.context).toContain("Historical reminder run");
  await command({
    action: "ai-start",
    id: explanation.id,
    consentDigest: explanation.digest,
  });
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  // A completed fixture-AI reminder also has a discussable saved result.
  v = await command({
    action: "task-ai-prepare",
    id: snapshot.sessionId,
    name: "Fixture reminder",
    text: "Review the recorded journal",
    intervalSeconds: 60,
    maxRuns: 1,
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
    totalBudgetMicrousd: 30000,
    authorizationSeconds: 3600,
  });
  const task = v.tasks.at(-1);
  await command({
    action: "task-ai-authorize",
    id: task.id,
    consentDigest: task.ai.digest,
  });
  await command({ action: "task-run", id: task.id });
  await expect.poll(async () => (await read()).runs.length).toBe(2);
  v = await read();
  const run = v.runs.at(-1);
  const retained = {
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  };
  v = await command({ action: "task-run-explain", id: run.id });
  const captured = v.messages.at(-1);
  expect(captured.text).toContain(run.aiRequestId);
  expect(captured.text).toContain("Limits");
  expect(captured.text).not.toContain("ConnectionRef");
  expect(captured.text).not.toContain("Disclosure");
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(retained);
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Changed persona",
    ...(await fixtureProviderSelection("xai")),
  });
  await stop();
  await start();
  await login();
  await page.goto(url + "/#agents/review");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(page.locator(".local-messages")).toContainText(captured.text);
  v = await read();
  expect(v.messages.at(-1)).toEqual(captured);
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(retained);
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.messages).toHaveLength(0);
  expect(other.sessions).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await captureScreenshot(page, {
    path: `.evidence/discuss-run/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("activity and notifications open the exact task run", async ({
  page,
  isMobile,
}) => {
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
  const profileId = "trading-analyst";
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (input: Record<string, unknown>) =>
    browserRequest(page, "agents", "POST", {
      profileId,
      revision: (await read()).revision,
      ...input,
    });
  const unrelated = (
    await command({
      action: "task-create",
      name: "Unrelated reminder",
      text: "A separate synthetic note",
      intervalSeconds: 3600,
      maxRuns: 1,
    })
  ).tasks.at(-1);
  await command({ action: "task-run", id: unrelated.id });
  const task = (
    await command({
      action: "task-create",
      name: "Two-run reminder",
      text: "Review the saved risk limits",
      intervalSeconds: 3600,
      maxRuns: 2,
    })
  ).tasks.at(-1);
  await command({ action: "task-run", id: task.id });
  await command({ action: "task-run", id: task.id });
  const saved = await read();
  const runs = saved.runs.filter((r: any) => r.taskId === task.id);
  const notices = saved.notices.filter((n: any) =>
    runs.some((r: any) => r.id === n.runId),
  );
  expect(runs).toHaveLength(2);
  expect(notices).toHaveLength(2);
  expect(notices[0].runId).toBe(runs[0].id);
  expect(notices[1].runId).toBe(runs[1].id);
  await page.goto(url + "/#activity");
  const activity = page.getByRole("list", { name: "Activity events" });
  const taskEvents = activity.locator("li").filter({ hasText: task.name });
  await expect(taskEvents).toHaveCount(2);
  await taskEvents
    .first()
    .getByRole("button", { name: "Result", exact: true })
    .click();
  const results = page.getByRole("region", {
    name: "Run results",
    exact: true,
  });
  await expect(results).toBeVisible();
  await expect(
    results.getByRole("article", {
      name: `Run ${runs[0].id}`,
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    results.getByRole("article", {
      name: `Run ${runs[1].id}`,
      exact: true,
    }),
  ).toHaveCount(0);
  await openExactNoticeResult(page, expect, {
    index: 0,
    runId: runs[0].id,
    otherRunId: runs[1].id,
    noticeText: task.name,
    isMobile,
  });
  await openExactNoticeResult(page, expect, {
    index: 1,
    runId: runs[1].id,
    otherRunId: runs[0].id,
    noticeText: task.name,
    isMobile,
  });
});

test("normal Tasks joins optional News analysis and granted analysis-only results across UI CLI restart and memory", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(12000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
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
  const profileId = "trading-analyst";
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  const selected = await fixtureProviderSelection("openai");
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    provider: selected.provider,
    model: selected.model,
    accountRef: selected.accountRef,
  });
  await command({
    action: "news-source-add",
    name: "Paper feed",
    source: "https://news.fased.invalid/headlines.xml",
    newsMaxAgeSeconds: 600,
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.getByRole("button", { name: "New task", exact: true }).click();
  let editor = page.getByRole("complementary", {
    name: "Create a task",
    exact: true,
  });
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Keep reminder draft");
  await editor
    .getByLabel("Reminder text", { exact: true })
    .fill("Preserve this unfinished reminder");
  // Leaving Tasks must preserve input without retaining a reviewed action.
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await expect(editor).toHaveCount(0);
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  await expect(editor).toBeVisible();
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Keep reminder draft",
  );
  await expect(editor.getByLabel("Reminder text", { exact: true })).toHaveValue(
    "Preserve this unfinished reminder",
  );
  await editor.getByLabel("Type", { exact: true }).selectOption("analysis");
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Keep analysis draft");
  await editor
    .getByLabel("Prompt", { exact: true })
    .fill("Summarize synthetic evidence");
  const agentPicker = page.getByLabel("Agent", { exact: true });
  await agentPicker.selectOption("news-researcher");
  await expect(editor).toHaveCount(0);
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Other agent draft");
  await editor
    .getByLabel("Reminder text", { exact: true })
    .fill("Separate profile input");
  await agentPicker.selectOption(profileId);
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Keep analysis draft",
  );
  await expect(editor.getByLabel("Prompt", { exact: true })).toHaveValue(
    "Summarize synthetic evidence",
  );
  await agentPicker.selectOption("news-researcher");
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Other agent draft",
  );
  await editor
    .getByRole("button", { name: "Cancel task", exact: true })
    .click();
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue("");
  await editor
    .getByRole("button", { name: "Cancel task", exact: true })
    .click();
  await agentPicker.selectOption(profileId);
  await editor.getByLabel("Type", { exact: true }).selectOption("reminder");
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Keep reminder draft",
  );
  await expect(editor.getByLabel("Reminder text", { exact: true })).toHaveValue(
    "Preserve this unfinished reminder",
  );
  await page.getByRole("button", { name: "Watch news", exact: true }).click();
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Optional paper analysis");
  await editor
    .getByLabel("News source", { exact: true })
    .selectOption({ label: "Paper feed" });
  await editor.getByLabel("Contains", { exact: true }).fill("paper");
  await editor.getByRole("checkbox", { name: "Analyze", exact: true }).check();
  await editor
    .getByLabel("Prompt", { exact: true })
    .fill("Summarize the selected synthetic headlines");
  await editor
    .getByLabel("If analysis fails", { exact: true })
    .selectOption("optional");
  const before = await read();
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  let review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Keep news result");
  let v = await read();
  expect(v.tasks).toEqual(before.tasks);
  expect(v.sessions).toEqual(before.sessions);
  // Completed reviews cannot survive a page or agent round trip.
  await page.evaluate(() => {
    window.location.hash = "providers";
  });
  await expect(
    page.getByRole("region", { name: "Providers", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  await expect(editor).toBeVisible();
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Optional paper analysis",
  );
  await expect(editor.getByLabel("Contains", { exact: true })).toHaveValue(
    "paper",
  );
  await expect(review).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Create task", exact: true }),
  ).toHaveCount(0);
  await expect(
    editor.getByRole("checkbox", { name: "Analyze", exact: true }),
  ).toBeChecked();
  expect((await read()).tasks).toEqual(before.tasks);
  expect((await read()).aiRequests).toHaveLength(0);
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  await expect(review).toBeVisible();
  await agentPicker.selectOption("news-researcher");
  await expect(review).toHaveCount(0);
  await agentPicker.selectOption(profileId);
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Optional paper analysis",
  );
  await expect(review).toHaveCount(0);
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  await expect(review).toBeVisible();
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(1);
  await page
    .getByRole("button", {
      name: "Run now: Optional paper analysis",
      exact: true,
    })
    .click();
  await expect
    .poll(async () => (await read()).tasks[0].state)
    .toBe("completed");
  v = await read();
  expect(v.aiRequests).toHaveLength(0);
  expect(v.runs[0].analysis.state).toBe("skipped");
  expect(v.runs[0].watch.newIds).toHaveLength(1);
  let result = page.getByRole("article", {
    name: `Run ${v.runs[0].id}`,
    exact: true,
  });
  await expect(result).toContainText("Analysis skipped");
  await expect(result).toContainText("New matches: 1");
  await captureScreenshot(page, {
    path: `.evidence/b04-task-analysis/optional-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await result
    .getByRole("button", { name: "Open thread", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  let message = page
    .locator(".v2-message-assistant")
    .filter({ hasText: "Optional paper analysis" })
    .last();
  await expect(message).toContainText("Analysis skipped");
  await message
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  let memory = page.getByRole("dialog", { name: "Memory", exact: true });
  await memory
    .getByLabel("Note", { exact: true })
    .fill(
      "Paper watch keeps its result when optional analysis is unavailable.",
    );
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memory).toHaveCount(0);
  const readV2Memories = async () => {
    const sessionToken = await page.evaluate(() =>
      sessionStorage.getItem("fased.core.session.v1"),
    );
    const res = await page.request.get(
      `${url}/api/v1/agents/v2/memories?profileId=trading-analyst`,
      {
        headers: sessionToken
          ? { Authorization: `Bearer ${sessionToken}` }
          : undefined,
      },
    );
    expect(res.ok()).toBe(true);
    return (await res.json()).memories;
  };
  await expect.poll(async () => (await readV2Memories()).length).toBe(1);
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "New task", exact: true }).click();
  editor = page.getByRole("complementary", {
    name: "Create a task",
    exact: true,
  });
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue("");
  await editor.getByLabel("Type", { exact: true }).selectOption("analysis");
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Routine analysis");
  await editor
    .getByLabel("Prompt", { exact: true })
    .fill("Summarize the saved synthetic evidence");
  await expect(
    editor.getByLabel("Step after validated AI response", { exact: true }),
  ).not.toBeVisible();
  const beforeAnalysis = await read();
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Routine analysis");
  v = await read();
  expect(v.tasks).toEqual(beforeAnalysis.tasks);
  expect(v.sessions).toEqual(beforeAnalysis.sessions);
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(2);
  const run = page.getByRole("button", {
    name: "Run now: Routine analysis",
    exact: true,
  });
  await expect(run).toBeDisabled();
  const detail = page.locator(".task-detail");
  await detail.getByText("Background access", { exact: true }).click();
  await detail
    .getByRole("checkbox", { name: "Routine analysis", exact: true })
    .check();
  await detail
    .getByRole("button", { name: "Review access", exact: true })
    .click();
  await detail
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-browser-preference-vault");
  await detail
    .getByRole("button", { name: "Allow task access", exact: true })
    .click();
  await expect(run).toBeEnabled();
  await run.click();
  await expect
    .poll(async () => (await read()).tasks[1].state)
    .toBe("completed");
  v = await read();
  expect(v.aiRequests).toHaveLength(1);
  expect(v.aiRequests[0].connectionRef).toBe(selected.accountRef);
  expect(v.aiRequests[0].model).toBe(selected.model);
  expect(v.runs[1].strategy).toBe("analysis-v1");
  expect(v.runs[1].analysis.state).toBe("completed");
  result = page.getByRole("article", {
    name: `Run ${v.runs[1].id}`,
    exact: true,
  });
  await expect(result).toContainText(v.aiRequests[0].answer);
  await result
    .getByRole("button", { name: "Open thread", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  message = page
    .locator(".v2-message-assistant")
    .filter({ hasText: "Routine analysis" })
    .last();
  await expect(message).toContainText(v.aiRequests[0].answer);
  await message
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  memory = page.getByRole("dialog", { name: "Memory", exact: true });
  await memory
    .getByLabel("Note", { exact: true })
    .fill(
      "Synthetic analysis is a saved result, not a reminder or trading authority.",
    );
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memory).toHaveCount(0);
  await expect.poll(async () => (await readV2Memories()).length).toBe(2);
  const saved = await read();
  expect(saved.tasks).toEqual(v.tasks);
  expect(saved.runs).toEqual(v.runs);
  expect(saved.providerCallsEnabled).toBe(false);
  expect(saved.financialExecution).toBe(false);
  await captureScreenshot(page, {
    path: `.evidence/b04-task-analysis/analysis-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await stop();
  await start(true);
  await page.goto(url + "/#agents/tasks");
  await choosePasswordSignIn(page);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const relogin = spawn(binary, [
    "login",
    "--data-dir",
    dir,
    "--password-stdin",
  ]);
  relogin.stdin!.end(fixturePassword + "\n");
  expect((await once(relogin, "exit"))[0]).toBe(0);
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  const restarted = await read();
  expect(restarted.tasks).toEqual(saved.tasks);
  expect(restarted.runs).toEqual(saved.runs);
  expect(restarted.memories).toEqual(saved.memories);
  expect(restarted.aiRequests).toEqual(saved.aiRequests);
  const foreign = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(foreign.tasks).toHaveLength(0);
  expect(foreign.messages).toHaveLength(0);
  expect(foreign.memories).toHaveLength(0);
  expect(external).toEqual([]);
});
