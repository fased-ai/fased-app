import { test, expect } from "@playwright/test";
import { spawn, execFile } from "node:child_process";
import { once } from "node:events";
import { promisify } from "node:util";
const exec = promisify(execFile);
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
test("private core delivers shared research without forwarding owner credentials", async ({
  page,
}, info) => {
  test.setTimeout(45000);
  await mkdir(".evidence/f3-data-delivery/fixtures", { recursive: true });
  const dir = await mkdtemp(
    resolve(".evidence/f3-data-delivery/fixtures/core-"),
  );
  const child = spawn(resolve(".evidence/bin/fased-app"), [
    "serve",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    dir,
    "--wen-research-origin",
    "http://127.0.0.1:7352",
  ]);
  let output = "";
  child.stdout.on("data", (b) => (output += b));
  child.stderr.on("data", (b) => (output += b));
  try {
    await expect.poll(() => output.includes('"event":"listening"')).toBe(true);
    const url = JSON.parse(output.trim().split("\n")[0]).url;
    await page.goto(url);
    const claim = JSON.parse(
      await readFile(join(dir, "claim-code.json"), "utf8"),
    );
    await page.getByLabel("One-use claim code").fill(claim.code);
    await page
      .getByLabel("Create owner password")
      .fill("generated-research-fixture-password");
    await page.getByRole("button", { name: "Claim workspace" }).click();
    await expect(
      page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
    ).toBeVisible();
    await page.goto(url + "/#terminals/satcoin");
    const ui = page.getByRole("region", {
      name: "Strategy research workspace",
    });
    await ui.getByRole("button", { name: "Research", exact: true }).click();
    const feed = page.getByRole("region", { name: "Shared Kraken research" });
    await feed
      .getByRole("button", { name: "Connect research", exact: true })
      .click();
    await expect(feed.getByTestId("shared-quote")).toHaveText("131.00");
    await expect(feed).toContainText("execution disabled");
    const login = spawn(resolve(".evidence/bin/fased-app"), [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin!.end("generated-research-fixture-password\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    const cli = JSON.parse(
      (
        await exec(resolve(".evidence/bin/fased-app"), [
          "wen-market-data",
          "--data-dir",
          dir,
          "--lookback",
          "1",
        ])
      ).stdout,
    );
    expect(cli.data.quote).toBe(131);
    expect(cli.method).toBe("wen-kraken-research/1");
    expect(cli.execution).toBe("disabled");
    await ui.screenshot({
      path: `.evidence/f3-data-delivery/core-${info.project.name}.png`,
    });
  } finally {
    if (child.exitCode === null) {
      const done = once(child, "exit");
      child.kill("SIGTERM");
      await done;
    }
    await rm(dir, { recursive: true, force: true });
  }
});
