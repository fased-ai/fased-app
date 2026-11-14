import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { test, expect } from "@playwright/test";

test("strategy discovery and scoped comparisons preserve original evidence", async ({
  page,
}, info) => {
  const remote: string[] = [],
    writes: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      remote.push(r.url());
    if (!["GET", "HEAD"].includes(r.method())) writes.push(r.url());
  });
  await page.goto(
    info.project.metadata.host === "wen"
      ? "/#/markets/satcoin"
      : "/#terminals/satcoin",
  );
  const ui = page.getByRole("region", { name: "Strategy research workspace" });
  await expect(ui.getByRole("status")).toHaveText("6 of 6 versions");
  for (const name of ["wide", "center", "trend-up", "trend-down"])
    await ui
      .getByRole("button", { name: `Compare ${name}`, exact: true })
      .click();
  await ui.getByRole("button", { name: "Compare tails", exact: true }).click();
  await expect(ui.getByRole("alert")).toHaveText(
    "Compare at most four versions",
  );
  await expect(
    ui.getByRole("button", { name: "Compare (4)", exact: true }),
  ).toBeVisible();
  for (const name of ["wide", "center", "trend-up", "trend-down"])
    await ui
      .getByRole("button", { name: `Compare ${name}`, exact: true })
      .click();
  await ui.getByRole("button", { name: "Inspect wide", exact: true }).click();
  const raw = await ui
    .getByLabel("Strategy JSON", { exact: true })
    .inputValue();
  await ui
    .getByRole("button", { name: "Save original version", exact: true })
    .click();
  const draft = JSON.parse(raw);
  draft.name = "Private trend";
  draft.research.thesis = "trend";
  draft.research.horizonMinutes = 240;
  draft.market.assetMint = "other-fixture-mint";
  const otherRaw = JSON.stringify(draft, null, 2) + "\n";
  await ui.getByLabel("Strategy JSON", { exact: true }).fill(otherRaw);
  await ui
    .getByRole("button", { name: "Save original version", exact: true })
    .click();
  await ui.getByRole("button", { name: "Mine", exact: true }).click();
  await expect(ui.getByRole("status")).toHaveText("2 of 2 versions");
  await expect(ui.getByRole("button", { name: /^Discuss / })).toHaveCount(0);
  await ui
    .getByRole("searchbox", { name: "Search strategies" })
    .fill("private trend");
  await expect(ui.getByRole("status")).toHaveText("1 of 2 versions");
  await ui.getByLabel("Horizon (minutes)", { exact: true }).selectOption("240");
  await ui
    .getByLabel("Asset mint", { exact: true })
    .selectOption("other-fixture-mint");
  await ui.getByLabel("Thesis", { exact: true }).selectOption("trend");
  await ui.getByLabel("History evidence", { exact: true }).selectOption("none");
  await ui
    .getByLabel("Record source", { exact: true })
    .selectOption("owner-import");
  await expect(
    ui.getByRole("heading", { name: "Private trend", exact: true }),
  ).toBeVisible();
  await ui
    .getByRole("searchbox", { name: "Search strategies" })
    .fill("no match <script>");
  await expect(ui).toContainText("No strategies match.");
  await ui.getByRole("button", { name: "Reset filters", exact: true }).click();
  if (info.project.name.endsWith("390")) {
    for (const control of await ui.locator("button, input, select").all()) {
      if (await control.isVisible())
        expect((await control.boundingBox())!.height).toBeGreaterThanOrEqual(
          44,
        );
    }
  }
  const download = page.waitForEvent("download");
  await ui
    .getByRole("button", { name: "Export Private trend", exact: true })
    .click();
  expect(await readFile((await (await download).path())!, "utf8")).toBe(
    otherRaw,
  );
  const id = (raw: string) => createHash("sha256").update(raw).digest("hex");
  const row = (digest: string, index: number) => ({
    id: `event-${index}`,
    cursor: index,
    strategyDigest: digest,
    at: "2026-10-22T00:10:00Z",
    finality: "finalized",
    category: "mining",
    committed: "1000000000",
    returned: "950000000",
    fees: "10000000",
    forfeitures: "40000000",
    userCosts: "1000",
    btcSaleSOL: "0",
    satSaleSOL: "0",
    btcPending: "5",
    btcReceived: "10",
    satEarned: index === 1 ? "100" : "0",
    satClaimed: index === 1 ? "100" : "0",
    satSold: "0",
  });
  const history = {
    schema: "wen-history-import/1",
    source: "generated browser evidence",
    verified: false,
    scope: {
      market: "fixture",
      terms: "fixture",
      evidence: "paper",
      from: "2026-10-22T00:00:00Z",
      to: "2026-10-22T04:00:00Z",
      dataset: "same-inputs",
      valuation: "native",
      costs: "observed",
      coverage: "declared wallet scope",
    },
    initialBankroll: "1000000000",
    skipped: 2,
    pendingExposure: "50000000",
    rows: [row(id(raw), 1), row(id(otherRaw), 2)],
    gaps: [] as string[],
  };
  await ui.getByRole("button", { name: "History", exact: true }).click();
  for (const h of [
    history,
    {
      ...history,
      scope: {
        ...history.scope,
        terms: "different-terms",
        evidence: "actual",
        dataset: "different-inputs",
      },
      rows: [row(id(otherRaw), 2)],
      gaps: ["Missing observation window"],
    },
  ]) {
    await ui
      .getByLabel("History JSON", { exact: true })
      .fill(JSON.stringify(h));
    await ui
      .getByRole("button", { name: "Save history import", exact: true })
      .click();
    await expect(ui).toContainText("UNVERIFIED");
  }
  await ui.getByRole("button", { name: "Library", exact: true }).click();
  await ui.getByRole("button", { name: "Mine", exact: true }).click();
  await ui
    .getByLabel("History evidence", { exact: true })
    .selectOption("actual");
  await expect(ui.getByRole("status")).toHaveText("1 of 2 versions");
  await ui.getByRole("button", { name: "Reset filters", exact: true }).click();
  await ui.screenshot({
    path: `.evidence/f3-library/library-${info.project.name}.png`,
  });
  for (const name of ["wide", "Private trend"])
    await ui
      .getByRole("button", { name: `Compare ${name}`, exact: true })
      .click();
  await ui.getByRole("button", { name: "Compare (2)", exact: true }).click();
  await ui.getByLabel("wide dataset", { exact: true }).selectOption("0");
  await ui
    .getByLabel("Private trend dataset", { exact: true })
    .selectOption("0");
  await expect(ui.getByRole("status")).toContainText(
    "descriptive comparison only",
  );
  const results = ui.getByRole("table", {
    name: "Descriptive recorded results",
  });
  await expect(
    results.getByRole("row", { name: /Net mining cash flow/ }),
  ).toContainText("-0.050001");
  await expect(
    results.getByRole("row", { name: /Fees \(SOL\)/ }),
  ).toContainText("0.01");
  await expect(
    results.getByRole("row", { name: /BTC sale proceeds/ }),
  ).toContainText("0");
  await expect(
    results.getByRole("row", { name: /SAT acquisition cost/ }),
  ).toContainText("Unavailable: no received SAT");
  await expect(
    results.getByRole("row", { name: /Scope pending exposure/ }),
  ).toContainText("0.05");
  await ui
    .getByLabel("Private trend dataset", { exact: true })
    .selectOption("1");
  await expect(ui.getByRole("status")).toContainText(
    "Different comparison scopes: terms, evidence, dataset",
  );
  await expect(results.getByRole("row", { name: /Gaps/ })).toContainText(
    "Missing observation window",
  );
  await expect(
    results.getByRole("row", { name: /Verification/ }),
  ).toContainText("Unverified import");
  await expect(ui).toContainText("Financial replay unavailable");
  const scroll = ui.getByRole("region", {
    name: "Descriptive recorded results scroll area",
  });
  await scroll.focus();
  await expect(scroll).toBeFocused();
  await ui.screenshot({
    path: `.evidence/f3-library/${info.project.name}.png`,
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(remote).toEqual([]);
  expect(writes).toEqual([]);
});
