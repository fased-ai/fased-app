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
    "agent_reminders.go",
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
  ]) {
    const bytes = await readFile("internal/core/" + file);
    await writeFile(path.join(archivedRoot, "internal/core", file), bytes);
    storage[file] = sha(bytes);
  }
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
      .replace(
        "type Store struct {",
        "type Store struct {\n\taiMu sync.Mutex\n\tnewsMu sync.Mutex\n\tprovider providerState",
      )
      .replace(
        marker,
        "\tif schema <= 3 { if err = migrateAgents(ctx, db); err != nil { return nil, err } }; if schema <= 4 { if err = migrateAgentAI(ctx, db); err != nil { return nil, err } }; if schema <= 5 { if err = migrateTaskAI(ctx, db); err != nil { return nil, err } }; if schema <= 6 { if err = migrateAIConnections(ctx, db); err != nil { return nil, err } }; if schema <= 7 { if err = migrateAgentNews(ctx, db); err != nil { return nil, err } }; if schema <= 8 { if err = migrateAIAdmission(ctx, db); err != nil { return nil, err } }; if schema <= 9 { if err = migrateWENPaper(ctx, db); err != nil { return nil, err } }\n" +
          marker,
      ),
  );
  storage["store.go"] = sha(await readFile(store));
  return { fixtureOnly: true, exactHistoricalBinary: false, storage };
}
