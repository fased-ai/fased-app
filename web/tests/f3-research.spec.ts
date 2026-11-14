import { readFile } from "node:fs/promises";
import { test, expect } from "@playwright/test";
test("shared private library: exact imports, clones, compare, history and reload", async ({
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
  await expect(ui).toBeVisible();
  await ui.getByRole("button", { name: "Inspect wide", exact: true }).click();
  const raw = await ui
    .getByLabel("Strategy JSON", { exact: true })
    .inputValue();
  await ui.getByRole("button", { name: "Save original version" }).click();
  await ui.getByRole("button", { name: "Mine", exact: true }).click();
  await expect(
    ui.getByRole("heading", { name: "wide", exact: true }),
  ).toBeVisible();
  await ui.getByLabel("Version name").fill("Private wide");
  await ui.getByRole("button", { name: "Save cloned version" }).click();
  await expect(
    ui.getByRole("heading", { name: "Private wide", exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Compare wide", exact: true }).click();
  await ui
    .getByRole("button", { name: "Compare Private wide", exact: true })
    .click();
  await ui.getByRole("button", { name: "Compare (2)", exact: true }).click();
  await expect(ui).toContainText("Performance unavailable");
  await expect(ui.locator("table tbody tr")).toHaveCount(6);
  await ui.getByRole("button", { name: "Library", exact: true }).click();
  await ui.getByRole("button", { name: "Inspect wide", exact: true }).click();
  expect(
    await ui.getByLabel("Strategy JSON", { exact: true }).inputValue(),
  ).toBe(raw);
  const digest = await page.evaluate(
    async (raw) =>
      [
        ...new Uint8Array(
          await crypto.subtle.digest("SHA-256", new TextEncoder().encode(raw)),
        ),
      ]
        .map((x) => x.toString(16).padStart(2, "0"))
        .join(""),
    raw,
  );
  const row = {
    id: "fixture-event",
    cursor: 1,
    strategyDigest: digest,
    at: "2026-10-22T00:10:00Z",
    finality: "finalized",
    category: "mining",
    committed: "1000000000",
    returned: "950000000",
    fees: "0",
    forfeitures: "50000000",
    userCosts: "1000",
    btcSaleSOL: "0",
    satSaleSOL: "0",
    btcPending: "5",
    btcReceived: "10",
    satEarned: "100",
    satClaimed: "100",
    satSold: "0",
  };
  const history = {
    schema: "wen-history-import/1",
    source: "browser fixture",
    verified: false,
    scope: {
      market: "fixture",
      terms: "fixture",
      evidence: "paper",
      from: "2026-10-22T00:00:00Z",
      to: "2026-10-22T04:00:00Z",
      dataset: "browser-fixture",
      valuation: "native",
      costs: "known fixture",
      coverage: "declared",
    },
    initialBankroll: null,
    skipped: null,
    pendingExposure: null,
    rows: [row],
    gaps: [],
  };
  await ui.getByRole("button", { name: "History", exact: true }).click();
  await ui
    .getByLabel("History JSON", { exact: true })
    .fill(JSON.stringify(history));
  await ui.getByRole("button", { name: "Save history import" }).click();
  await expect(ui).toContainText("UNVERIFIED");
  await expect(ui).toContainText("fixture-event");
  await expect(ui).toContainText("0.95");
  await page.reload();
  await ui.getByRole("button", { name: "Mine", exact: true }).click();
  await expect(
    ui.getByRole("heading", { name: "Private wide", exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Following", exact: true }).click();
  await expect(ui).toContainText("not connected");
  await ui.getByRole("button", { name: "Research", exact: true }).click();
  await expect(
    ui.getByRole("heading", { name: "Historical capture", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(remote).toEqual([]);
  expect(writes).toEqual([]);
  const header = await ui.locator(".wr-heading").boundingBox();
  const nav = await ui
    .getByRole("navigation", { name: "Research sections" })
    .boundingBox();
  expect(header!.y + header!.height).toBeLessThanOrEqual(nav!.y);
  await ui.screenshot({
    path: `.evidence/f3-research/${info.project.name}.png`,
    fullPage: true,
  });
});

test("captured BTC research: provenance, gaps and original exports", async ({
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
  await ui.getByRole("button", { name: "Research", exact: true }).click();
  const time = (m: number) =>
    new Date(Date.UTC(2026, 9, 22, 0, m)).toISOString();
  const rows = [
    {
      schema: "wen-research-capture/1",
      source: "generated browser observations",
      evaluatedAt: time(31),
      lookbackMinutes: 30,
    },
    { type: "connect", receivedAt: time(0) },
    ...Array.from({ length: 32 }, (_, i) => ({
      type: "ticker",
      product_id: "BTC-USD",
      sequence: String(i),
      time: time(i),
      receivedAt: time(i),
      best_bid: String(100 + i),
      best_ask: String(100 + i),
    })),
    {
      type: "heartbeat",
      product_id: "BTC-USD",
      sequence: "31",
      time: time(31),
      receivedAt: time(31),
    },
  ];
  const raw = rows.map((r) => JSON.stringify(r)).join("\n") + "\n";
  await ui.getByLabel("Import BTC capture").setInputFiles({
    name: "capture.ndjson",
    mimeType: "application/x-ndjson",
    buffer: Buffer.from(raw),
  });
  await expect(
    ui.getByLabel("BTC capture NDJSON", { exact: true }),
  ).toHaveValue(raw);
  await ui
    .getByRole("button", { name: "Inspect capture", exact: true })
    .click();
  await expect(ui).toContainText(
    "Historical · unverified · execution disabled",
  );
  await expect(ui).toContainText("3000 bps /");
  await expect(
    ui.getByRole("img", { name: "Historical BTC midpoint closes" }),
  ).toBeVisible();
  await ui.screenshot({
    path: `.evidence/f3-capture/${info.project.name}.png`,
  });
  const originalWait = page.waitForEvent("download");
  await ui.getByRole("button", { name: "Export original capture" }).click();
  const original = await originalWait;
  expect(await readFile((await original.path())!, "utf8")).toBe(raw);
  const snapshotWait = page.waitForEvent("download");
  await ui.getByRole("button", { name: "Export research snapshot" }).click();
  const snapshot = JSON.parse(
    await readFile((await (await snapshotWait).path())!, "utf8"),
  );
  expect(snapshot.execution).toBe("disabled");
  expect(snapshot.captureSHA256).toMatch(/^[a-f0-9]{64}$/);
  expect(snapshot.raw).toBeUndefined();
  await ui.getByRole("button", { name: "240m", exact: true }).click();
  await expect(ui).toContainText("Unavailable");
  await expect(
    ui
      .locator("dt")
      .filter({ hasText: "Missing minute closes" })
      .locator("+ dd"),
  ).toHaveText("210");
  const krakenRows = JSON.parse(JSON.stringify(rows)) as any[];
  krakenRows[0].schema = "wen-kraken-capture/1";
  krakenRows[0].source = "generated Kraken observations";
  const krakenRaw =
    [
      krakenRows[0],
      krakenRows[1],
      ...krakenRows
        .slice(2)
        .filter((e) => e.type === "ticker")
        .map((e) => ({
          receivedAt: e.receivedAt,
          raw: JSON.stringify({
            channel: "ticker",
            type: "update",
            data: [
              {
                symbol: "BTC/USD",
                bid: Number(e.best_bid),
                ask: Number(e.best_ask),
                timestamp: e.time,
              },
            ],
          }),
        })),
    ]
      .map((r) => JSON.stringify(r))
      .join("\n") + "\n";
  await ui.getByLabel("BTC capture NDJSON", { exact: true }).fill(krakenRaw);
  await ui
    .getByRole("button", { name: "Inspect capture", exact: true })
    .click();
  await expect(ui).toContainText("Kraken BTC/USD BBO imported capture");
  await expect(ui).toContainText("wen-kraken-research/1");
  await expect(ui).toContainText("3000 bps /");
  const krakenExportWait = page.waitForEvent("download");
  await ui.getByRole("button", { name: "Export original capture" }).click();
  expect(await readFile((await (await krakenExportWait).path())!, "utf8")).toBe(
    krakenRaw,
  );
  await ui.screenshot({
    path: `.evidence/f3-alternative-source/${info.project.name}.png`,
  });
  await ui
    .getByLabel("BTC capture NDJSON", { exact: true })
    .fill("bad capture");
  await expect(
    ui.getByRole("img", { name: "Historical BTC midpoint closes" }),
  ).toHaveCount(0);
  await ui
    .getByRole("button", { name: "Inspect capture", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(remote).toEqual([]);
  expect(writes).toEqual([]);
});
