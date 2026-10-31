// Compose an explicitly nonhistorical predecessor for same-schema reader tests.
// Only archived disposable sources are changed, never an installed runtime.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFile, writeFile, mkdir, readdir } from "node:fs/promises";
import { createHash } from "node:crypto";
import path from "node:path";
export async function currentStorageFixture(archivedRoot) {
  assert.ok(process.getuid() > 0);
  const storage = {};
  const sha = (b) => createHash("sha256").update(b).digest("hex");
  for (const file of [
    "agents.go",
    "agent_strategies.go",
    "onchain.go",
    "onchain_storage.go",
    "venue_credentials.go",
    "rpc_credentials.go",
    "rpc_recovery.go",
    "rpc_check.go",
    "rpc_authenticated.go",
    "rpc_public.go",
    "rpc_providers.go",
    "agent_provider.go",
    "provider_models.go",
    "provider_native.go",
    "provider_compat.go",
    "provider_kimi_code.go",
    "provider_ollama_cloud.go",
    "provider_ollama_cloud.json",
    "provider_anthropic_workspace.go",
    "provider_kimi_code.json",
    "provider_moonshot.json",
    "provider_moonshot_intl.json",
    "provider_google.json",
    "provider_openrouter.json",
    "provider_qwen.json",
    "provider_opencode.json",
    "provider_local.go",
    "provider_local.json",
    "provider_anthropic.json",
    "provider_xai.json",
    "provider_models.json",
    "openai_transport.go",
    "openai_pilot.go",
    "openai_pilot_activation.go",
    "openai_pilot_target.go",
    "provider_admission.go",
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
    "agent_news_watch.go",
    "agent_task_analysis.go",
    "ai_connection.go",
    ...(await readdir("internal/core")).filter(
      (name) =>
        name.startsWith("provider_signin") &&
        name.endsWith(".go") &&
        !name.endsWith("_test.go"),
    ),
    "update_preferences.go",
    "backup_linux.go",
    ...(await readdir("internal/core")).filter(
      (name) =>
        name.startsWith("application_") &&
        name.endsWith(".go") &&
        !name.endsWith("_test.go"),
    ),
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
    // Exact current sources are composed without copying their routers or
    // constructors. rpcAdmission remains its false zero value.
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
      'type Account struct {\n\tRPC *AccountRPC \`json:"rpc,omitempty"`',
    ),
  );
  // This is an explicitly nonhistorical storage reader, not a vault/owner
  // backport. Copy only the public v2 structural predicates needed by strategies.
  const accountSource = await readFile(accounts, "utf8");
  if (!accountSource.includes("func managedNetworkAccount(")) {
    await writeFile(
      accounts,
      accountSource +
        `
func managedNetworkAccount(a Account) bool {
 return a.Version == 3 && a.Mode == "fixture-managed" && a.Signer != nil && signer.ValidManagedAccountRef(*a.Signer) && a.Signer.Network == a.Network && a.Signer.PublicKey == a.PublicKey
}
`,
    );
  }
  const signerPublic = path.join(
    archivedRoot,
    "internal/signer/strategy_public_fixture.go",
  );
  await writeFile(
    signerPublic,
    `package signer
import "encoding/hex"
func managedRefValid(r AccountRef) bool { return ValidManagedAccountRef(r) }
func ValidManagedAccountRef(r AccountRef) bool {
 b, err := hex.DecodeString(r.PublicKey)
 return r.Version == 2 && (r.Network == "devnet" || r.Network == "mainnet-beta") && validID(r.SignerID) && validID(r.WalletID) && validID(r.Epoch) && err == nil && len(b) == 32 && hex.EncodeToString(b) == r.PublicKey
}
`,
  );
  storage["signer/strategy_public_fixture.go"] = sha(
    await readFile(signerPublic),
  );
  storage["accounts.go"] = sha(await readFile(accounts));
  // Read-only current swap-history backport. Preserve old protocol, lifecycle
  // and dispatch: the composed helper gains no observer or signing commands.
  const signerRoot = path.join(archivedRoot, "internal/signer");
  await mkdir(path.join(signerRoot, "testdata"), { recursive: true });
  for (const name of [
    "onchain_state.go",
    "receipt_rpc_state.go",
    "onchain_state_test.go",
    "testdata/onchain-reader.json",
    "journal.go",
  ]) {
    const b = await readFile("internal/signer/" + name);
    await writeFile(path.join(signerRoot, name), b);
    storage["signer/" + name] = sha(b);
  }
  const amend = async (file, changes) => {
    const target = path.join(signerRoot, file);
    let b = await readFile(target, "utf8");
    for (const [before, after] of changes) {
      assert.ok(b.includes(before), file + ": missing reader marker " + before);
      b = b.replace(before, after);
    }
    await writeFile(target, b);
    storage["signer/" + file] = sha(Buffer.from(b));
  };
  await amend("policy.go", [
    [
      "type ledger struct {",
      "type ledger struct {\n managedFresh bool\n receiptRPC *receiptRPCEnvelope\n swaps map[string]SwapReceipt",
    ],
    ["return &ledger{", "return &ledger{swaps:map[string]SwapReceipt{},"],
    [
      "next := *l",
      "next := *l\n next.receiptRPC=clone(l.receiptRPC)\n next.swaps=clone(l.swaps)",
    ],
    [
      "switch r.Kind {",
      'switch r.Kind {\n case "onchain-rpc-credential": if e:=l.applyReceiptRPC(r);e!=nil{return e}\n case "onchain-approve", "onchain-sign", "onchain-settled", "onchain-cancel": if e:=l.applySwap(r);e!=nil{return e}',
    ],
    [
      'case "rebind":',
      'case "rebind":\n for _,s:=range l.swaps {if s.State=="signed"{return ErrHeld}}',
    ],
    [
      "l.ref = *r.Account\n\t\tl.policy = nil",
      'for id,s:=range l.swaps{if s.State=="approved"{s.State="cancelled";l.swaps[id]=s}}\n l.ref = *r.Account\n\t\tl.policy = nil',
    ],
  ]);
  await amend("update.go", [
    ['"approvals-v1",', '"approvals-v1", "onchain-swap-fixture-v1",'],
  ]);
  await amend("lifecycle.go", [
    [
      "Features: append([]string{}, contract.Features...)",
      "Features: func() []string { features:=[]string{};for _,feature:=range l.swapStateFeatures(contract.Features) {if feature!=receiptRPCCredentialFeature || l.receiptRPC!=nil {features=append(features,feature)}};return features}()",
    ],
    [
      "return s, nil",
      "pending,unknown:=l.swapPending(v.now().Unix());s.Pending+=pending;s.Unknown+=unknown\n return s,nil",
    ],
  ]);

  for (const [file, marker, field] of [
    [
      "internal/signer/vault.go",
      "type Status struct {",
      'SwapReady bool `json:"swapReady,omitempty"`\nReceiptRPC *ReceiptRPCStatus `json:"receiptRPC,omitempty"`',
    ],
    [
      "internal/signer/rpc.go",
      "type Config struct {",
      'Network string `json:"network,omitempty"`',
    ],
    [
      "internal/signer/rpc.go",
      "type Response struct {",
      'Swap *SwapReceipt `json:"swap,omitempty"`',
    ],
  ]) {
    const target = path.join(archivedRoot, file);
    const b = await readFile(target, "utf8");
    await writeFile(target, b.replace(marker, marker + "\n" + field + "\n"));
    storage[file] = sha(await readFile(target));
  }
  for (const name of ["go.mod", "go.sum"]) {
    const b = await readFile(name);
    await writeFile(path.join(archivedRoot, name), b);
    storage[name] = sha(b);
  }
  const localRoot = path.join(archivedRoot, "internal/localprovider");
  await mkdir(localRoot, { recursive: true });
  for (const file of (await readdir("internal/localprovider")).filter(
    (name) => name.endsWith(".go") && !name.endsWith("_test.go"),
  )) {
    const bytes = await readFile(path.join("internal/localprovider", file));
    await writeFile(path.join(localRoot, file), bytes);
    storage["localprovider/" + file] = sha(bytes);
  }
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
  // State feature lists now exceed 32 entries. Protocol capabilities retain
  // their original bound; backport only the bounded state-list reader.
  for (const file of ["compatibility.go", "pause.go"]) {
    const target = path.join(archivedRoot, "internal/update", file);
    let b = await readFile(target, "utf8");
    if (file === "compatibility.go")
      b = b
        .replace(
          "func listOK(a []string) bool {\n\tif len(a) > 32 {",
          "func listOK(a []string) bool { return boundedListOK(a,32) }\nfunc stateFeaturesOK(a []string) bool { return boundedListOK(a,64) }\nfunc boundedListOK(a []string,max int) bool {\n\tif len(a) > max {",
        )
        .replaceAll("!listOK(s.Features)", "!stateFeaturesOK(s.Features)");
    else
      b = b.replaceAll(
        "listOK(r.Expected.State.Features)",
        "stateFeaturesOK(r.Expected.State.Features)",
      );
    await writeFile(target, b);
    storage["update/" + file] = sha(Buffer.from(b));
  }
  const updateManifest = path.join(archivedRoot, "internal/update/manifest.go");
  let updateSource = await readFile(updateManifest, "utf8");
  if (!updateSource.includes("func strictJSONBounded(")) {
    const signature = "func StrictJSON(b []byte, v any) error {";
    assert.equal(updateSource.split(signature).length, 2);
    const start = updateSource.indexOf(signature);
    const tail = updateSource.slice(start);
    assert.ok(tail.includes("len(b) > MaxDocument"));
    updateSource =
      updateSource.slice(0, start) +
      tail
        .replace(
          signature,
          signature +
            " return strictJSONBounded(b, v, MaxDocument) }\n\nfunc strictJSONBounded(b []byte, v any, limit int) error {",
        )
        .replace("len(b) > MaxDocument", "len(b) > limit");
  }
  if (!updateSource.includes("func StrictJSONBounded(")) {
    assert.ok(updateSource.includes("func strictJSONBounded("));
    await writeFile(
      updateManifest,
      updateSource +
        "\nfunc StrictJSONBounded(b []byte, v any, limit int) error { return strictJSONBounded(b, v, limit) }\n",
    );
  }
  storage["update/manifest.go"] = sha(await readFile(updateManifest));
  for (const dir of ["internal/onchain"]) {
    await mkdir(path.join(archivedRoot, dir), { recursive: true });
    for (const name of await readdir(dir)) {
      if (!name.endsWith(".go") || name.endsWith("_test.go")) continue;
      const b = await readFile(path.join(dir, name));
      await writeFile(path.join(archivedRoot, dir, name), b);
      storage[dir + "/" + name] = sha(b);
    }
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
        "func (s *Store) Close() error { return s.db.Close() }",
        "func (s *Store) Close() error { s.lockVenueCredentials(); return s.db.Close() }",
      )
      .replace(
        "type Store struct {",
        "type Store struct {\n\tappUpdates applicationUpdates\n\tvenueCredentials venueCredentials\n\trpcAdmission bool\n\trpcCheckFixture func(context.Context, RPCCheckRequest) (RPCCheckResult, error)\n\tcustodyNetwork string\n\tonchain *onchainRuntime\n\tproviderFixture bool\n\tchatEpoch atomic.Uint64\n\tprogressMu sync.Mutex\n\taiProgress map[string]string\n\tsessionFixture bool\n\taiMu sync.Mutex\n\tnewsMu sync.Mutex\n\tprovider providerState\n\tpilot openAIPilotState\n\tresearchRead func(context.Context, researchFactResult) (json.RawMessage, error)",
      )
      .replace(
        marker,
        "\tif schema <= 3 { if err = migrateAgents(ctx, db); err != nil { return nil, err } }; if schema <= 4 { if err = migrateAgentAI(ctx, db); err != nil { return nil, err } }; if schema <= 5 { if err = migrateTaskAI(ctx, db); err != nil { return nil, err } }; if schema <= 6 { if err = migrateAIConnections(ctx, db); err != nil { return nil, err } }; if schema <= 7 { if err = migrateAgentNews(ctx, db); err != nil { return nil, err } }; if schema <= 8 { if err = migrateAIAdmission(ctx, db); err != nil { return nil, err } }; if schema <= 9 { if err = migrateWENPaper(ctx, db); err != nil { return nil, err } }; if schema <= 10 { if err = migrateWENResearch(ctx, db); err != nil { return nil, err } }; if schema <= 11 { if err = migrateWENResearchAI(ctx, db); err != nil { return nil, err } }; if schema <= 12 { if err = migrateWENResearchLoop(ctx, db); err != nil { return nil, err } }; if schema <= 13 { if err = migrateWENSessions(ctx, db); err != nil { return nil, err } }; if schema <= 14 { if err = migrateWENFixtureRuns(ctx, db); err != nil { return nil, err } }; if schema <= 15 { if err = migratePlaybooks(ctx, db); err != nil { return nil, err } }; if schema <= 16 { if err = migrateTaskCalendar(ctx, db); err != nil { return nil, err } }; if schema <= 17 { if err = migrateTaskCron(ctx, db); err != nil { return nil, err } }; if schema <= 18 { if err = migrateAgentChat(ctx, db); err != nil { return nil, err } }; if schema <= 19 { if err = migrateAccountRPC(ctx, db); err != nil { return nil, err } }; if schema <= 20 { if err = migrateProviderRPC(ctx, db); err != nil { return nil, err } }; if schema <= 21 { if err = migrateManagedKeys(ctx, db); err != nil { return nil, err } }; if schema <= 22 { if err = migrateProviderAccounts(ctx, db, dir); err != nil { return nil, err } }; if schema <= 23 { if err = migrateProviderModels(ctx, db); err != nil { return nil, err } }; if schema <= 24 { if err = migrateNativeProviders(ctx, db); err != nil { return nil, err } }; if schema <= 25 { if err = migrateSignInPlan(ctx, db); err != nil { return nil, err } }; if schema <= 26 { if err = migrateLocalProviders(ctx, db); err != nil { return nil, err } }; if schema <= 27 { if err = migrateNewsWatch(ctx, db); err != nil { return nil, err } }; if schema <= 28 { if err = migrateTaskAnalysis(ctx, db); err != nil { return nil, err } }; if schema <= 29 { if err = migrateStrategies(ctx, db); err != nil { return nil, err } }; if schema <= 30 { if err = migrateOnchain(ctx, db); err != nil { return nil, err } }; if schema <= 31 { if err = migrateManagedSwaps(ctx, db); err != nil { return nil, err } }; if schema <= 32 { if err = migrateVenueCredentials(ctx, db); err != nil { return nil, err } }; if schema <= 34 { if err = migrateApplicationUpdates(ctx, db); err != nil { return nil, err } }\n" +
          marker,
      ),
  );
  storage["store.go"] = sha(await readFile(store));
  const readerLog = execFileSync(
    "go",
    ["test", "./internal/signer", "-run", "^TestSwapStateReader$"],
    {
      cwd: archivedRoot,
      env: { ...process.env, FASED_COMPOSED_SWAP_READER: "1" },
      encoding: "utf8",
    },
  );
  storage["signer/reader-test-output"] = sha(Buffer.from(readerLog));

  return { fixtureOnly: true, exactHistoricalBinary: false, storage };
}
