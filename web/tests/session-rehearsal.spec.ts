import { test, expect } from "@playwright/test";
import { spawn, execFile, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
import { readFile, writeFile, mkdir, mkdtemp, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
const exec = promisify(execFile);
const binary = resolve(".evidence/bin/fased-app");
const previousCore = process.env.FASED_PREVIOUS_CORE;

for (const variant of ["review", "notify", "unknown"] as const) {
  test(`${variant}: session fixture has UI/CLI parity, bounded native amounts and recovery`, async ({
    page,
    isMobile,
  }) => {
    test.setTimeout(120000);
    page.setDefaultTimeout(12000);
    const evidence = resolve(".evidence/f4-rehearsal");
    await mkdir(evidence, { recursive: true });
    const dir = await mkdtemp("/tmp/fased-session-rehearsal-"); // Disposable reconstructible state; evidence persists above.
    const password = "generated-session-fixture-password";
    let process: ChildProcess | undefined,
      output = "",
      url = "";
    const stop = async () => {
      if (process && process.exitCode === null) {
        const done = once(process, "exit");
        process.kill("SIGTERM");
        await done;
      }
      process = undefined;
    };
    const start = async () => {
      output = "";
      process = spawn(binary, [
        "serve",
        "--listen",
        "127.0.0.1:0",
        "--data-dir",
        dir,
      ]);
      process.stdout!.on("data", (b) => {
        output += b;
      });
      process.stderr!.on("data", (b) => {
        output += b;
      });
      await expect
        .poll(() => output.includes('"event":"listening"'))
        .toBe(true);
      url = JSON.parse(output.trim().split("\n")[0]).url;
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
      expect(status, error).toBe(0);
      return JSON.parse(out);
    };
    const read = () => cli(["wen-session", "--profile", "wen-operator"]);
    const external: string[] = [];
    page.on("request", (r) => {
      if (!["localhost", "127.0.0.1"].includes(new URL(r.url()).hostname))
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
      const initial = await command("wen-research", "--request-stdin", {
        profile: "wen-operator",
        revision: 0,
        action: "save",
        raw,
      });
      await page.goto(url + "/#terminals/satcoin");
      const panel = page.getByRole("region", {
        name: "Session rehearsal",
        exact: true,
      });
      await expect(panel).toBeVisible();
      await panel
        .getByLabel("Rehearsal strategy")
        .selectOption(initial.versions[0].digest);
      await panel.getByLabel("Rehearsal name").fill("Fixture " + variant);
      if (variant === "notify") {
        await panel.getByLabel("Rehearsal mode").selectOption("notify");
        await panel
          .getByLabel("Rehearsal scenario")
          .selectOption("fixture-12h");
      }
      if (variant === "unknown")
        await panel
          .getByLabel("Rehearsal scenario")
          .selectOption("fixture-4h-unknown");
      await panel
        .getByRole("button", { name: "Review Fased draft", exact: true })
        .click();
      await expect(
        panel.getByRole("region", {
          name: "Fased strategy draft",
          exact: true,
        }),
      ).toBeVisible();
      await panel
        .getByRole("button", { name: "Save Fased draft", exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await cli(["wen-research", "--profile", "wen-operator"])).versions
              .length,
        )
        .toBe(2);
      await panel
        .getByRole("button", { name: "Review session", exact: true })
        .click();
      const reviewPanel = panel.getByRole("region", {
        name: "Session review",
        exact: true,
      });
      await expect(reviewPanel).toBeVisible();
      const noSession = await read();
      expect(noSession.sessions).toEqual([]);
      await reviewPanel
        .getByRole("button", { name: "Cancel session review", exact: true })
        .click();
      expect((await read()).sessions).toEqual([]);
      await panel
        .getByRole("button", { name: "Review session", exact: true })
        .click();
      await panel
        .getByRole("button", { name: "Create rehearsal", exact: true })
        .click();
      const card = panel.getByRole("article", {
        name: "Rehearsal Fixture " + variant,
        exact: true,
      });
      await expect(card).toBeVisible();
      let v = await read();
      const id = v.sessions[0].record.sessionId;
      expect(v.sessions[0].record.status).toBe("waiting");
      expect(v.sessions[0].record.mandateId).toBeNull();
      expect(v.sessions[0].record.runner).toBe("fased");
      expect(v.executable).toBe(false);
      const stateNoTimer = JSON.stringify(v);
      await page.waitForTimeout(300);
      expect(JSON.stringify(await read())).toBe(stateNoTimer);
      await card
        .getByRole("button", { name: "Step fixture", exact: true })
        .click();
      await expect(card.getByTestId(`accepted-${id}`)).toHaveText("0");
      v = await read();
      expect(v.sessions[0].decisions).toHaveLength(1);
      expect(v.pools[0].budget.availableLamports).toBe("20000000000");
      if (variant === "notify") {
        expect(v.sessions[0].decisions[0].record.reason).toBe("notify-only");
        expect(v.sessions[0].pending).toBeNull();
        await expect(
          card.getByRole("button", { name: "Review proposal", exact: true }),
        ).toHaveCount(0);
        // Finish the entire 12h controlled schedule through the same private CLI.
        for (
          let i = 0;
          i < 73 && v.sessions[0].record.status !== "complete";
          i++
        )
          v = await command("wen-session", "--step-stdin", {
            schema: "wen-session-step/1",
            profile: v.profile,
            revision: v.revision,
            poolRevision: v.pools[0].revision,
            sessionId: id,
            eventId: v.nextEvents[id],
          });
        expect(v.sessions[0].record.status).toBe("complete");
        expect(v.sessions[0].decisions).toHaveLength(72);
        expect(v.sessions[0].schedule.acceptedRounds).toBe(0);
        expect(v.pools[0].budget.availableLamports).toBe("20000000000");
        await panel
          .getByRole("button", { name: "Refresh rehearsals", exact: true })
          .click();
        await expect(card.getByText("COMPLETE", { exact: true })).toBeVisible();
      } else {
        await card
          .getByRole("button", { name: "Review proposal", exact: true })
          .click();
        const action = card.getByRole("region", {
          name: "Simulated allocation review",
          exact: true,
        });
        await expect(action).toBeVisible();
        const before = await read();
        const input = {
          schema: "wen-session-action-review/1",
          profile: before.profile,
          revision: before.revision,
          poolRevision: before.pools[0].revision,
          sessionId: id,
          decisionId: before.sessions[0].pending.decisionId,
        };
        const a = await command("wen-session", "--action-review-stdin", input);
        expect(a.decision.record.amountsLamports).toEqual(
          before.sessions[0].decisions[0].record.amountsLamports,
        );
        await action
          .getByRole("button", { name: "Cancel proposal review", exact: true })
          .click();
        expect((await read()).pools[0].revision).toBe(0);
        await card
          .getByRole("button", { name: "Review proposal", exact: true })
          .click();
        await card
          .getByRole("button", { name: "Simulate acceptance", exact: true })
          .click();
        await expect(card.getByTestId(`accepted-${id}`)).toHaveText("1");
        v = await read();
        expect(v.sessions[0].decisions[1].phase).toBe("acceptance");
        expect(v.sessions[0].decisions[1].record.amountsLamports).toEqual(
          a.decision.record.amountsLamports,
        );
        expect(v.pools[0].budget.unsettledLamports).toBe("5000000000");
        const receipts = v.sessions[0].receipts;
        if (variant === "unknown") {
          await card
            .getByRole("button", { name: "Step fixture", exact: true })
            .click();
          await expect(
            card.getByText("fixture-operation-uncertain", { exact: true }),
          ).toBeVisible();
          const originalHolds = (await read()).pools[0].budget;
          await stop();
          await start();
          v = await read();
          expect(v.sessions[0].record.status).toBe("held");
          expect(v.pools[0].budget).toEqual(originalHolds);
          expect(v.sessions[0].receipts.slice(0, receipts.length)).toEqual(
            receipts,
          );
          await page.goto(url + "/#terminals/satcoin");
          // Restart preserves shadow obligations, not the old browser session.
          await page
            .getByLabel("Owner password", { exact: true })
            .fill(password);
          await page
            .getByRole("button", { name: "Sign in", exact: true })
            .click();
          await expect(card.getByText("HELD", { exact: true })).toBeVisible();
          await card
            .getByRole("button", { name: "Stop rehearsal", exact: true })
            .click();
          await expect(
            card.getByText("stop-awaiting-reconciliation", { exact: true }),
          ).toBeVisible();
          await card
            .getByRole("button", { name: "Step fixture", exact: true })
            .click();
          await expect(
            card.getByText("COMPLETE", { exact: true }),
          ).toBeVisible();
          v = await read();
          expect(v.pools[0].budget.availableLamports).toBe("20000000000");
          expect(v.sessions[0].completionReason).toBe("stopped");
        } else {
          await card
            .getByRole("button", { name: "Step fixture", exact: true })
            .click();
          await expect(
            card.getByText("shadow-reconciled; BTC/SAT unavailable", {
              exact: true,
            }),
          ).toBeVisible();
          v = await read();
          expect(v.pools[0].budget.unsettledLamports).toBe("0");
          expect(v.pools[0].budget.actualDeductionsLamports).toBe("250000000");
          await card
            .getByRole("button", { name: "Pause rehearsal", exact: true })
            .click();
          await expect(card.getByText("PAUSED", { exact: true })).toBeVisible();
          await card
            .getByRole("button", { name: "Resume rehearsal", exact: true })
            .click();
          await expect(
            card.getByText("WAITING", { exact: true }),
          ).toBeVisible();
          await card
            .getByRole("button", { name: "Stop rehearsal", exact: true })
            .click();
          await expect(
            card.getByText("COMPLETE", { exact: true }),
          ).toBeVisible();
          v = await read();
        }
      }
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      expect(external).toEqual([]);
      const agents = await cli(["agents", "--profile", "wen-operator"]);
      expect(agents.tasks).toEqual([]);
      expect(agents.aiRequests).toEqual([]);
      expect(agents.notices).toHaveLength(1);
      await writeFile(
        join(evidence, `${variant}-${isMobile ? "mobile" : "desktop"}.json`),
        JSON.stringify(
          {
            view: v,
            source: initial.versions[0],
            noExternalRequests: true,
            noTaskOrAI: true,
          },
          null,
          2,
        ),
      );
      await card.screenshot({
        path: join(
          evidence,
          `${variant}-${isMobile ? "mobile" : "desktop"}.png`,
        ),
      });
      if (variant === "review" && !isMobile && previousCore) {
        await stop();
        const previous = previousCore;
        let rejected = false;
        try {
          await exec(
            previous,
            ["serve", "--listen", "127.0.0.1:0", "--data-dir", dir],
            { timeout: 10000 },
          );
        } catch (e: any) {
          expect(String(e.stderr)).toContain("newer than this binary");
          rejected = true;
        }
        expect(rejected).toBe(true);
        await writeFile(
          join(evidence, "schema13-reader.json"),
          JSON.stringify({
            result: "PASS",
            actualPreviousBinaryRefused: true,
            liveExecution: false,
          }),
        );
      } else if (variant === "review" && !isMobile) {
        await writeFile(
          join(evidence, "schema13-reader.json"),
          JSON.stringify({
            result: "N/A",
            reason:
              "Canonical previous reader not supplied; local proof is separate",
            actualPreviousBinaryRefused: false,
            liveExecution: false,
          }),
        );
      }
    } finally {
      await stop();
      await rm(dir, { recursive: true, force: true });
    }
  });
}
