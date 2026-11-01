import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { test, expect, type Page } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { readFile, writeFile, mkdir, mkdtemp, rm } from "node:fs/promises";
import { createHash } from "node:crypto";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");
const previousCore = process.env.FASED_PREVIOUS_CORE;

for (const variant of ["consent", "unknown"] as const) {
  test(`${variant}: automatic fixture consent, interruption and complete UI/CLI history`, async ({
    page: initialPage,
    context,
    isMobile,
  }) => {
    test.setTimeout(120000);
    let page: Page = initialPage;
    page.setDefaultTimeout(15000);
    const evidence = resolve(".evidence/f4-automatic");
    await mkdir(evidence, { recursive: true });
    // Disposable, reconstructible, nonauthoritative state; retained proof is above.
    const dir = await mkdtemp("/tmp/fased-auto-");
    const password = "generated-automatic-fixture-password";
    let child: ChildProcess | undefined,
      output = "",
      url = "";
    const external: string[] = [];
    let maximumConcurrentFixtureReads = 0;
    let reviewedPauseConflicts = 0;
    let reviewedStopConflicts = 0;
    context.on("request", (r) => {
      if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
        external.push(r.url());
    });
    const stop = async () => {
      if (child && child.exitCode === null) {
        const done = once(child, "exit");
        child.kill("SIGTERM");
        await done;
      }
      child = undefined;
    };
    const start = async (fixture = true) => {
      output = "";
      child = spawn(binary, [
        "serve",
        "--listen",
        "127.0.0.1:0",
        "--data-dir",
        dir,
        ...(fixture ? ["--session-fixture"] : []),
      ]);
      child.stdout!.on("data", (b) => {
        output += b;
      });
      child.stderr!.on("data", (b) => {
        output += b;
      });
      await expect
        .poll(() => output.includes('"event":"listening"'))
        .toBe(true);
      url = JSON.parse(
        output
          .trim()
          .split("\n")
          .find((l) => l.includes('"event":"listening"'))!,
      ).url;
    };
    const cli = async (args: string[]) =>
      JSON.parse(
        (
          await exec(binary, [...args, "--data-dir", dir], {
            maxBuffer: 600 * 1024,
          })
        ).stdout,
      );
    const read = () =>
      cli(["wen-session", "--auto-inspect", "--profile", "wen-operator"]);
    const command = async (
      name: string,
      flag: string,
      body: object,
      ok = true,
      allowPauseConflict = false,
    ) => {
      const p = spawn(binary, [name, "--data-dir", dir, flag]);
      let out = "",
        error = "";
      p.stdout!.on("data", (b) => {
        out += b;
      });
      p.stderr!.on("data", (b) => {
        error += b;
      });
      p.stdin!.end(JSON.stringify(body));
      const status = (await once(p, "exit"))[0];
      if (!ok) {
        expect(status).not.toBe(0);
        return error;
      }
      if (
        allowPauseConflict &&
        status !== 0 &&
        error.includes("core request failed (409)")
      ) {
        expect(name).toBe("wen-session");
        expect(flag).toBe("--auto-control-stdin");
        expect((body as { action: string }).action).toBe("pause");
        return { pauseConflict: error };
      }
      expect(status, error).toBe(0);
      return JSON.parse(out);
    };
    const loginCLI = async () => {
      const p = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
      p.stdin!.end(password + "\n");
      expect((await once(p, "exit"))[0]).toBe(0);
    };
    const loginUI = async () => {
      await page.goto(url);
      await page.getByLabel("Owner password", { exact: true }).fill(password);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      await page.goto(url + "/#terminals/satcoin");
    };
    const panel = () =>
      page.getByRole("region", { name: "Automatic fixture runs", exact: true });
    let priorReader = "N/A: optional actual schema14 reader was not supplied";
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
      await loginCLI();
      expect((await read()).runs).toEqual([]);
      await page.waitForTimeout(1100); // Exercise a real scheduler wake with no consent.
      expect((await read()).runs).toEqual([]);
      const raw = await readFile(
        "internal/core/testdata/wen-research-strategy.json",
        "utf8",
      );
      const saved = await command("wen-research", "--request-stdin", {
        profile: "wen-operator",
        revision: 0,
        action: "save",
        raw,
      });
      await page.goto(url + "/#terminals/satcoin");
      await expect(panel()).toBeVisible();
      await panel()
        .getByLabel("Automatic strategy")
        .selectOption(saved.versions[0].digest);
      await panel()
        .getByLabel("Automatic run name")
        .fill("Auto " + variant);
      if (variant === "unknown")
        await panel()
          .getByLabel("Automatic scenario")
          .selectOption("fixture-4h-unknown");
      await panel()
        .getByRole("button", { name: "Review automatic draft", exact: true })
        .click();
      await expect(
        panel().getByRole("region", {
          name: "Automatic strategy draft",
          exact: true,
        }),
      ).toBeVisible();
      expect((await read()).runs).toEqual([]);
      await panel()
        .getByRole("button", { name: "Save automatic draft", exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await cli(["wen-research", "--profile", "wen-operator"])).versions
              .length,
        )
        .toBe(2);
      const versions = await cli(["wen-research", "--profile", "wen-operator"]);
      expect(versions.versions[0]).toEqual(saved.versions[0]);
      const automatic = JSON.parse(versions.versions[1].raw);
      expect(automatic.execution).toEqual({
        mode: "bounded-auto",
        runner: "fased",
        mandateId: null,
      });
      await panel()
        .getByRole("button", { name: "Review fixture run", exact: true })
        .click();
      await expect(
        panel().getByRole("region", {
          name: "Fixture run review",
          exact: true,
        }),
      ).toBeVisible();
      await panel()
        .getByRole("button", { name: "Cancel run review", exact: true })
        .click();
      expect((await read()).runs).toEqual([]);
      const response = page.waitForResponse(
        (r) =>
          r.url().endsWith("/wen/session-runs/review") &&
          r.request().method() === "POST",
      );
      await panel()
        .getByRole("button", { name: "Review fixture run", exact: true })
        .click();
      const reviewed = await (await response).json();
      expect(reviewed.executable).toBe(false);
      expect(reviewed.runDeadline).toBeNull();
      expect(reviewed.maxWallSeconds).toBe(300);
      expect(reviewed.source.digest).toBe(versions.versions[1].digest);
      await panel()
        .getByRole("button", { name: "Start fixture run", exact: true })
        .click();
      const card = () =>
        panel().getByRole("article", {
          name: "Automatic run Auto " + variant,
          exact: true,
        });
      await expect(card()).toBeVisible();
      await expect
        .poll(async () => (await read()).runs[0].operations.length, {
          intervals: [50],
        })
        .toBeGreaterThan(0);
      let v = await read();
      const id = v.runs[0].record.sessionId,
        originalDeadline = v.runs[0].deadline;
      expect(v.runs[0].record.mandateId).toBeNull();
      expect(v.runs[0].consentHistory).toHaveLength(1);
      if (variant === "consent") {
        const cursor = v.runs[0].cursor;
        await page.close();
        await expect
          .poll(async () => (await read()).runs[0].cursor)
          .toBeGreaterThan(cursor);
        const pause = (observed: any, allowConflict = false) =>
          command(
            "wen-session",
            "--auto-control-stdin",
            {
              schema: "wen-fixture-run-control/1",
              profile: observed.profile,
              revision: observed.revision,
              poolRevision: observed.pools[0].revision,
              runId: id,
              action: "pause",
              decisionId: null,
            },
            true,
            allowConflict,
          );
        // The closed tab's observation is deliberately stale. Refusal is a
        // required result, not permission to reuse that command or its revision.
        const stalePause = await pause(v, true);
        expect(stalePause.pauseConflict).toContain("reload and review");
        reviewedPauseConflicts++;
        let pausedByOwner = false;
        for (let review = 0; review < 3; review++) {
          const observed = await read();
          expect(observed.runs[0].record.sessionId).toBe(id);
          expect(observed.runs[0].source.digest).toBe(reviewed.source.digest);
          expect(observed.runs[0].deadline).toBe(originalDeadline);
          expect(observed.runs[0].consentHistory).toHaveLength(1);
          const result = await pause(observed, true);
          if (!result.pauseConflict) {
            pausedByOwner = true;
            break;
          }
          reviewedPauseConflicts++;
        }
        expect(
          pausedByOwner,
          "owner's bounded fresh pause reviews failed",
        ).toBe(true);
        const paused = await read();
        await new Promise((r) => setTimeout(r, 1100));
        expect(await read()).toEqual(paused);
        page = await context.newPage();
        page.setDefaultTimeout(15000);
        await loginUI();
        await expect(card().getByTestId(`run-status-${id}`)).toContainText(
          "paused",
        );
        if (
          paused.runs[0].operations.some(
            (op: any) => op.status !== "reconciled",
          )
        ) {
          await card()
            .getByRole("button", {
              name: "Reconcile fixture operation",
              exact: true,
            })
            .click();
          await expect
            .poll(async () =>
              (await read()).runs[0].operations.every(
                (op: any) => op.status === "reconciled",
              ),
            )
            .toBe(true);
        }
        let pendingReads = 0,
          delayedReads = 0;
        const reads = "**/api/v1/wen/session-runs?profile=wen-operator";
        await page.route(reads, async (route) => {
          pendingReads++;
          delayedReads++;
          maximumConcurrentFixtureReads = Math.max(
            maximumConcurrentFixtureReads,
            pendingReads,
          );
          try {
            await new Promise((r) => setTimeout(r, 2100));
            await route.continue();
          } finally {
            pendingReads--;
          }
        });
        await expect.poll(() => delayedReads).toBeGreaterThan(0);
        await page.waitForTimeout(2400);
        await page.unrouteAll({ behavior: "wait" });
        await expect.poll(() => pendingReads).toBe(0);
        expect(maximumConcurrentFixtureReads).toBe(1);
        await context.setOffline(true);
        await expect(
          panel().getByText("Disconnected · last-known records", {
            exact: true,
          }),
        ).toBeVisible();
        await context.setOffline(false);
        await panel()
          .getByRole("button", { name: "Reload fixture runs", exact: true })
          .click();
        await expect(
          panel().getByText("Disconnected · last-known records", {
            exact: true,
          }),
        ).toHaveCount(0);
        await card()
          .getByRole("button", { name: "Review resume", exact: true })
          .click();
        await panel()
          .getByRole("button", { name: "Start fixture run", exact: true })
          .click();
        await expect
          .poll(async () => (await read()).runs[0].consentHistory.length)
          .toBe(2);
      } else {
        // Freeze the real wake after acceptance, before uncertainty classification.
        v = await read();
        await command("wen-session", "--auto-control-stdin", {
          schema: "wen-fixture-run-control/1",
          profile: v.profile,
          revision: v.revision,
          poolRevision: v.pools[0].revision,
          runId: id,
          action: "pause",
          decisionId: null,
        });
        v = await read();
        expect(v.runs[0].operations[0].status).toBe("reserved");
        await panel()
          .getByRole("button", { name: "Reload fixture runs", exact: true })
          .click();
        await expect(card().getByTestId(`run-status-${id}`)).toContainText(
          "paused",
        );
        await card()
          .getByRole("button", { name: "Stop fixture run", exact: true })
          .click();
        await expect(card().getByTestId(`run-status-${id}`)).toContainText(
          "held",
        );
        const heldBefore = await read();
        await card()
          .getByRole("button", {
            name: "Inspect fixture operation",
            exact: true,
          })
          .click();
        await expect
          .poll(async () => (await read()).runs[0].operations[0].status)
          .toBe("uncertain");
        expect((await read()).pools).toEqual(heldBefore.pools);
        await expect
          .poll(async () => (await read()).runs[0].record.status)
          .toBe("held");
        v = await read();
        expect(v.runs[0].operations[0].status).toBe("uncertain");
        const held = JSON.stringify(v);
        await page.waitForTimeout(1100);
        expect(JSON.stringify(await read())).toBe(held);
        await stop();
        if (!isMobile && previousCore) {
          const before = createHash("sha256")
            .update(await readFile(join(dir, "core.sqlite")))
            .digest("hex");
          await expect(
            exec(
              previousCore,
              ["serve", "--listen", "127.0.0.1:0", "--data-dir", dir],
              { timeout: 10000 },
            ),
          ).rejects.toMatchObject({
            stderr: expect.stringContaining("newer than this binary"),
          });
          expect(
            createHash("sha256")
              .update(await readFile(join(dir, "core.sqlite")))
              .digest("hex"),
          ).toBe(before);
          priorReader =
            "PASS: actual canonical schema14 reader refused schema15 without changing metadata";
        }
        await start(false);
        await loginCLI();
        await page.close();
        page = await context.newPage();
        page.setDefaultTimeout(15000);
        await loginUI();
        v = await read();
        expect(v.capability.enabled).toBe(false);
        expect(v.runs[0].operations).toEqual(
          JSON.parse(held).runs[0].operations,
        );
        expect(v.runs[0].deadline).toBe(originalDeadline);
        expect(v.runs[0].consentHistory.at(-1).revokedAt).not.toBeNull();
        expect(
          await command(
            "wen-session",
            "--auto-review-stdin",
            reviewed.input,
            false,
          ),
        ).toContain("session-fixture");
        await expect(
          card().getByRole("button", { name: "Review resume", exact: true }),
        ).toBeDisabled();
      }
      if (!(await read()).runs[0].stopRequested) {
        let stoppedByOwner = false;
        for (let review = 0; review < 3; review++) {
          // Each click follows a visible reload and inspection. A conflicting
          // stop is refused, never silently replayed with its old revisions.
          await panel()
            .getByRole("button", { name: "Reload fixture runs", exact: true })
            .click();
          const observed = await read();
          expect(observed.runs[0].record.sessionId).toBe(id);
          expect(observed.runs[0].source.digest).toBe(reviewed.source.digest);
          expect(observed.runs[0].deadline).toBe(originalDeadline);
          const reply = page.waitForResponse(
            (r) =>
              r.url().endsWith("/wen/session-runs/control") &&
              r.request().method() === "POST" &&
              r.request().postDataJSON()?.action === "stop",
          );
          await card()
            .getByRole("button", { name: "Stop fixture run", exact: true })
            .click();
          const response = await reply;
          if (response.ok()) {
            stoppedByOwner = true;
            break;
          }
          expect(response.status()).toBe(409);
          expect((await response.json()).error).toContain("reload and review");
          reviewedStopConflicts++;
        }
        expect(
          stoppedByOwner,
          "owner's bounded fresh stop reviews failed",
        ).toBe(true);
      }
      await expect
        .poll(async () => (await read()).runs[0].stopRequested)
        .toBe(true);
      v = await read();
      const pending = v.runs[0].operations.find(
        (op: any) => op.status !== "reconciled",
      );
      let reconcile: any = null;
      if (pending) {
        reconcile = {
          schema: "wen-fixture-run-reconcile/1",
          profile: v.profile,
          revision: v.revision,
          poolRevision: v.pools[0].revision,
          runId: id,
          eventId: "resolve:" + pending.id,
        };
        await card()
          .getByRole("button", {
            name: "Reconcile fixture operation",
            exact: true,
          })
          .click();
      }
      await expect(card().getByTestId(`run-status-${id}`)).toContainText(
        "complete",
      );
      v = await read();
      const complete = JSON.stringify(v);
      expect(v.runs[0].deadline).toBe(originalDeadline);
      expect(v.runs[0].record.mandateId).toBeNull();
      expect(
        v.runs[0].operations.every((op: any) => op.status === "reconciled"),
      ).toBe(true);
      await page.waitForTimeout(1100);
      expect(JSON.stringify(await read())).toBe(complete);
      if (reconcile) {
        const retried = await command(
          "wen-session",
          "--auto-reconcile-stdin",
          reconcile,
        );
        expect(retried.receipt.id).toBe("reconcile:" + reconcile.eventId);
        expect((await read()).revision).toBe(v.revision);
      }
      expect(
        (await command("wen-session", "--auto-start-stdin", reviewed)).runs,
      ).toEqual(v.runs);
      await panel()
        .getByRole("button", { name: "Reload fixture runs", exact: true })
        .click();
      await expect
        .poll(async () =>
          JSON.parse(
            (await panel().getByTestId("fixture-run-view").textContent())!,
          ),
        )
        .toEqual(await read());
      expect(
        (await cli(["wen-session", "--profile", "wen-operator"])).sessions,
      ).toEqual([]);
      const agents = await cli(["agents", "--profile", "wen-operator"]);
      expect(agents.aiRequests).toEqual([]);
      expect(agents.tasks).toEqual([]);
      expect(agents.notices).toHaveLength(1);
      expect(external).toEqual([]);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      const name = `${variant}-${isMobile ? "mobile" : "desktop"}`;
      await writeFile(
        join(evidence, name + ".json"),
        JSON.stringify(
          {
            result: "PASS",
            view: await read(),
            originalSource: saved.versions[0],
            noExternalRequests: true,
            noTaskOrAI: true,
            priorReader,
            applicationTabClosedWhileCoreAdvanced: variant === "consent",
            maximumConcurrentFixtureReads,
            reviewedPauseConflicts,
            reviewedStopConflicts,
            fixtureOnly: true,
          },
          null,
          2,
        ),
      );
      await card().screenshot({ path: join(evidence, name + ".png") });
    } finally {
      await context.setOffline(false);
      await stop();
      await rm(dir, { recursive: true, force: true });
    }
  });
}
