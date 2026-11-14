import { test, expect } from "@playwright/test";
test("shared Kraken delivery: source parity, stale data, unavailable service and recovery", async ({
  page,
}, info) => {
  test.setTimeout(45000);
  const external: string[] = [],
    writes: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      external.push(r.url());
    if (r.method() !== "GET") writes.push(r.url());
  });
  await page.goto(
    info.project.metadata.host === "wen"
      ? "/#/markets/satcoin"
      : "/#terminals/satcoin",
  );
  const ui = page.getByRole("region", { name: "Strategy research workspace" });
  await ui.getByRole("button", { name: "Research", exact: true }).click();
  const feed = page.getByRole("region", { name: "Shared Kraken research" });
  const wait = page.waitForResponse((r) =>
    r.url().includes("/api/mining/v1/research?lookback=1"),
  );
  await feed
    .getByRole("button", { name: "Connect research", exact: true })
    .click();
  const record = await (await wait).json();
  await expect(feed.getByTestId("shared-quote")).toHaveText("131.00");
  await expect(feed.getByTestId("shared-digest")).toHaveText(
    record.data.inputDigest,
  );
  await expect(feed.getByTestId("shared-cursor")).toHaveText(
    record.sourceCursor,
  );
  await expect(feed).toContainText("0 bps / 0 bps");
  await feed.getByRole("button", { name: "30m", exact: true }).click();
  await expect(feed.getByTestId("shared-quote")).toHaveText("Unavailable");
  await expect(feed).toContainText("disconnected · stale");
  await feed.getByRole("button", { name: "60m", exact: true }).click();
  await expect(feed.getByRole("status")).toHaveText(
    "Research service unavailable",
  );
  await expect(feed.getByTestId("shared-quote")).toHaveCount(0);
  await feed.getByRole("button", { name: "1m", exact: true }).click();
  await expect(feed.getByTestId("shared-quote")).toHaveText("131.00");
  await ui.screenshot({
    path: `${process.env.FASED_DATA_SCREENSHOT_DIR ?? ".evidence/f3-data-delivery"}/${info.project.name}.png`,
  });
  await feed
    .getByRole("button", { name: "Disconnect research", exact: true })
    .click();
  await expect(feed.getByTestId("shared-quote")).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(external).toEqual([]);
  expect(writes).toEqual([]);
});
