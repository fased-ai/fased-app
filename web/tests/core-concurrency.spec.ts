import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import {
  choosePasswordSignIn,
  dir,
  fixturePassword,
  installCoreFixture,
  revealControl,
  start,
  stop,
  url,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("concurrent V2 thread creation and single-flight provider sign-in lock stay consistent", async ({
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

  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  expect(token).toBeTruthy();

  const authHeaders = {
    Authorization: `Bearer ${token}`,
    Origin: url,
  };
  const stateResp = await page.request.get(url + "/api/v1/auth/state", {
    headers: authHeaders,
  });
  expect(stateResp.status()).toBe(200);
  const { csrfToken } = await stateResp.json();
  expect(csrfToken).toBeTruthy();

  const writeHeaders = {
    ...authHeaders,
    "X-CSRF-Token": csrfToken,
  };

  // 1. Concurrent thread creation under SQLite backend lock must yield distinct IDs with zero 500s.
  const createResponses = await Promise.all(
    Array.from({ length: 5 }, (_, i) =>
      page.request.post(url + "/api/v1/agents/v2/threads", {
        headers: writeHeaders,
        data: { profileId: "trading-analyst", title: `Parallel Thread ${i}` },
      }),
    ),
  );
  const createdIds = new Set<string>();
  for (const resp of createResponses) {
    expect(resp.status()).toBe(201);
    const body = await resp.json();
    expect(body.thread?.id).toBeTruthy();
    createdIds.add(body.thread.id);
  }
  expect(createdIds.size).toBe(5);

  const listResp = await page.request.get(url + "/api/v1/agents/v2/threads", {
    headers: authHeaders,
  });
  expect(listResp.status()).toBe(200);
  const listData = await listResp.json();
  for (const id of createdIds) {
    expect(listData.threads.some((t: { id: string }) => t.id === id)).toBe(
      true,
    );
  }

  // 2. Concurrent provider sign-in-start must admit at most one pending ceremony and cleanly conflict the rest.
  const signInAttempts = await Promise.all(
    Array.from({ length: 4 }, (_, i) =>
      page.request.post(url + "/api/v1/ai/connection", {
        headers: writeHeaders,
        data: {
          action: "sign-in-start",
          provider: "openai",
          remote: true,
          name: `Race ${i}`,
        },
      }),
    ),
  );
  const okCount = signInAttempts.filter((r) => r.status() === 200).length;
  const rejectedCount = signInAttempts.filter(
    (r) => r.status() >= 400 && r.status() < 500,
  ).length;
  expect(okCount).toBe(1);
  expect(rejectedCount).toBe(3);

  const connState = await page.request.get(url + "/api/v1/ai/connection", {
    headers: authHeaders,
  });
  expect(connState.status()).toBe(200);
  const connBody = await connState.json();
  expect(connBody.signIn?.provider).toBe("openai");
});

test("multi-tab sign-out revokes that tab session and blocks stale CSRF mutations", async ({
  context,
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

  const tabAToken = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const tabAState = await page.request.get(url + "/api/v1/auth/state", {
    headers: { Authorization: `Bearer ${tabAToken}`, Origin: url },
  });
  expect(tabAState.status()).toBe(200);
  const { csrfToken: tabACSRF } = await tabAState.json();

  // Tab B opens in the same browser context: isolated sessionStorage requires independent sign-in.
  const tabB = await context.newPage();
  await tabB.goto(url);
  await choosePasswordSignIn(tabB);
  const tabBPassword = tabB.getByLabel("Owner password", { exact: true });
  await revealControl(tabB, tabBPassword);
  await tabBPassword.fill(fixturePassword);
  await tabB.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    tabB.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  // Revoke Tab A explicitly from UI and confirm old Tab A bearer + CSRF is permanently rejected.
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Open Fased", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Use password", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => sessionStorage.getItem("fased.core.session.v1")),
  ).toBeNull();

  const staleMutation = await page.request.post(
    url + "/api/v1/agents/v2/threads",
    {
      headers: {
        Authorization: `Bearer ${tabAToken}`,
        Origin: url,
        "X-CSRF-Token": tabACSRF,
      },
      data: { profileId: "trading-analyst", title: "Stale Tab A" },
    },
  );
  expect(staleMutation.status()).toBe(401);

  // Tab B's independent session remains functional.
  await tabB.reload();
  await expect(
    tabB.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await tabB.close();
});
