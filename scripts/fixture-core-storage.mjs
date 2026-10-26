// Compose an explicitly nonhistorical predecessor for same-schema reader tests.
// Only archived disposable sources are changed, never an installed runtime.
import assert from "node:assert/strict";
import { readFile, writeFile, mkdir, readdir } from "node:fs/promises";
import { createHash } from "node:crypto";
import path from "node:path";
export async function currentStorageFixture(archivedRoot) {
  assert.ok(process.getuid() > 0);
  const storage = {};
  const sha = (b) => createHash("sha256").update(b).digest("hex");
  for (const file of [
    "agents.go",
    "agent_provider.go",
    "account_rpc_storage.go",
    "agent_reminders.go",
    "agent_task_drafts.go",
    "agent_task_review.go",
    "agent_calendar.go",
    "agent_cron.go",
    "agent_history.go",
    "agent_recall.go",
    "agent_chat.go",
    "agent_ai_stream.go",
    "ai_task_access.go",
    "agent_playbooks.go",
    "wen_paper.go",
    "agent_ai.go",
    "agent_evidence.go",
    "agent_memory.go",
    "agent_ai_tasks.go",
    "agent_news.go",
    "ai_admission.go",
    "agent_news_tasks.go",
    "ai_connection.go",
    "update.go",
    "lifecycle.go",
    ...(await readdir("internal/core")).filter(
      (name) =>
        name.startsWith("wen_") &&
        name.endsWith(".go") &&
        !name.endsWith("_test.go") &&
        name !== "wen_paper.go",
    ),
  ]) {
    const bytes = await readFile("internal/core/" + file);
    await writeFile(path.join(archivedRoot, "internal/core", file), bytes);
    storage[file] = sha(bytes);
  }
  // Preserve the new storage field without adding any configuration routes.
  const accounts = path.join(archivedRoot, "internal/core/accounts.go");
  const accountBefore = await readFile(accounts, "utf8");
  assert.equal(accountBefore.split("type Account struct {").length, 2);
  assert.ok(!accountBefore.includes("*AccountRPC"));
  await writeFile(
    accounts,
    accountBefore.replace(
      "type Account struct {",
      'type Account struct {\n\tRPC *AccountRPC `json:"rpc,omitempty"`',
    ),
  );
  storage["accounts.go"] = sha(await readFile(accounts));
  const strategyRoot = path.join(archivedRoot, "internal/wenstrategy");
  await mkdir(path.join(strategyRoot, "testdata"), { recursive: true });
  for (const file of [
    ...(await readdir("internal/wenstrategy")).filter(
      (name) => name.endsWith(".go") && !name.endsWith("_test.go"),
    ),
    ...(await readdir("internal/wenstrategy/testdata")).map(
      (name) => "testdata/" + name,
    ),
  ]) {
    const bytes = await readFile(path.join("internal/wenstrategy", file));
    await writeFile(path.join(strategyRoot, file), bytes);
    storage["wenstrategy/" + file] = sha(bytes);
  }
  // Storage projections compile their private reader helpers, but the archived
  // router/command dispatch remain unchanged. These zero-value fields do not
  // expose a research endpoint or enable fixture execution in the predecessor.
  const http = path.join(archivedRoot, "internal/core/http.go");
  const httpBefore = await readFile(http, "utf8");
  assert.ok(!httpBefore.includes("/api/v1/wen/"));
  assert.equal(httpBefore.split("type HTTPHandler struct {").length, 2);
  await writeFile(
    http,
    httpBefore.replace(
      "type HTTPHandler struct {",
      "type HTTPHandler struct {\n\twenResearchKey []byte\n\twenResearchOrigin string\n\tagentFixture bool",
    ),
  );
  storage["http.go"] = sha(await readFile(http));
  const pm1Root = path.join(archivedRoot, "internal/wenpm1");
  await mkdir(path.join(pm1Root, "testdata"), { recursive: true });
  for (const file of [
    ...(await readdir("internal/wenpm1")).filter(
      (name) => name.endsWith(".go") && !name.endsWith("_test.go"),
    ),
    "testdata/golden.json",
  ]) {
    const bytes = await readFile(path.join("internal/wenpm1", file));
    await writeFile(path.join(pm1Root, file), bytes);
    storage["wenpm1/" + file] = sha(bytes);
  }
  const store = path.join(archivedRoot, "internal/core/store.go");
  const before = await readFile(store, "utf8");
  const marker =
    "\tstore := &Store{db: db, dir: dir, now: time.Now, bootID: update.NewProbe()}";
  assert.equal(before.split(marker).length, 2);
  assert.ok(!before.includes("migrateAgents(ctx, db)"));
  await writeFile(
    store,
    before
      .replace('"sync"', '"sync"\n"sync/atomic"')
      .replace(
        "type Store struct {",
        "type Store struct {\n\tchatEpoch atomic.Uint64\n\tprogressMu sync.Mutex\n\taiProgress map[string]string\n\tsessionFixture bool\n\taiMu sync.Mutex\n\tnewsMu sync.Mutex\n\tprovider providerState\n\tresearchRead func(context.Context, researchFactResult) (json.RawMessage, error)",
      )
      .replace(
        marker,
        "\tif schema <= 3 { if err = migrateAgents(ctx, db); err != nil { return nil, err } }; if schema <= 4 { if err = migrateAgentAI(ctx, db); err != nil { return nil, err } }; if schema <= 5 { if err = migrateTaskAI(ctx, db); err != nil { return nil, err } }; if schema <= 6 { if err = migrateAIConnections(ctx, db); err != nil { return nil, err } }; if schema <= 7 { if err = migrateAgentNews(ctx, db); err != nil { return nil, err } }; if schema <= 8 { if err = migrateAIAdmission(ctx, db); err != nil { return nil, err } }; if schema <= 9 { if err = migrateWENPaper(ctx, db); err != nil { return nil, err } }; if schema <= 10 { if err = migrateWENResearch(ctx, db); err != nil { return nil, err } }; if schema <= 11 { if err = migrateWENResearchAI(ctx, db); err != nil { return nil, err } }; if schema <= 12 { if err = migrateWENResearchLoop(ctx, db); err != nil { return nil, err } }; if schema <= 13 { if err = migrateWENSessions(ctx, db); err != nil { return nil, err } }; if schema <= 14 { if err = migrateWENFixtureRuns(ctx, db); err != nil { return nil, err } }; if schema <= 15 { if err = migratePlaybooks(ctx, db); err != nil { return nil, err } }; if schema <= 16 { if err = migrateTaskCalendar(ctx, db); err != nil { return nil, err } }; if schema <= 17 { if err = migrateTaskCron(ctx, db); err != nil { return nil, err } }; if schema <= 18 { if err = migrateAgentChat(ctx, db); err != nil { return nil, err } }; if schema <= 19 { if err = migrateAccountRPC(ctx, db); err != nil { return nil, err } }; if schema <= 20 { if err = migrateProviderRPC(ctx, db); err != nil { return nil, err } }\n" +
          marker,
      ),
  );
  storage["store.go"] = sha(await readFile(store));
  return { fixtureOnly: true, exactHistoricalBinary: false, storage };
}
