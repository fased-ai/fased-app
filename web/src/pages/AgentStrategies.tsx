import { useEffect, useRef, useState } from "react";
import type { Account, AccountsView } from "../accounts";
import type {
  AgentCommand,
  AgentStrategy,
  AgentView,
  StrategyPaperResult,
  StrategyPlan,
  TaskReview,
} from "../agents";
import { Badge } from "../components/shared";
import { useCore } from "../core";
import { RefreshCw } from "../icons";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";

export type StrategyDraft = {
  name: string;
  accountId: string;
  quantity: string;
  price: string;
  order: string;
  budget: string;
};
const emptyDraft: StrategyDraft = {
  name: "",
  accountId: "",
  quantity: "",
  price: "",
  order: "",
  budget: "",
};
export function strategyUnits(value: number, decimals: number): string {
  if (!Number.isSafeInteger(value) || value < 0) return "—";
  const digits = String(value).padStart(decimals + 1, "0");
  const fraction = digits.slice(-decimals).replace(/0+$/, "");
  return digits.slice(0, -decimals) + (fraction ? `.${fraction}` : "");
}
export function parseStrategyUnits(
  value: string,
  decimals: number,
  max: number,
) {
  if (!/^\d+(?:\.\d+)?$/.test(value.trim())) return;
  const [whole, fraction = ""] = value.trim().split(".");
  if (whole.length > 16 || fraction.length > decimals) return;
  const amount =
    BigInt(whole) * 10n ** BigInt(decimals) +
    BigInt(fraction.padEnd(decimals, "0"));
  if (amount < 1n || amount > BigInt(max)) return;
  return Number(amount);
}
export function strategyWallets(accounts: Account[]) {
  return accounts.filter(
    (a) =>
      [2, 3].includes(a.version) &&
      ["watch", "fixture-managed"].includes(a.mode) &&
      ["devnet", "mainnet-beta"].includes(a.network),
  );
}
export function strategyPlan(
  draft: StrategyDraft,
  accounts: Account[],
): StrategyPlan | undefined {
  const account = strategyWallets(accounts).find(
    (a) => a.id === draft.accountId,
  );
  const quantityAtoms = parseStrategyUnits(draft.quantity, 9, 1e12);
  const limitPriceMicros = parseStrategyUnits(draft.price, 6, 1e12);
  const orderBudgetMicros = parseStrategyUnits(draft.order, 6, 1e15);
  const totalBudgetMicros = parseStrategyUnits(draft.budget, 6, 1e15);
  if (
    !account ||
    quantityAtoms === undefined ||
    limitPriceMicros === undefined ||
    orderBudgetMicros === undefined ||
    totalBudgetMicros === undefined ||
    orderBudgetMicros > totalBudgetMicros
  )
    return;
  return {
    version: "paper-limit-buy-v1",
    market: "SOL/USDC",
    accountId: account.id,
    network: account.network,
    quantityAtoms,
    limitPriceMicros,
    orderBudgetMicros,
    totalBudgetMicros,
  };
}
export function strategyScheduleCommand(
  strategy: AgentStrategy,
  name: string,
  minutes: string,
  runs: string,
): AgentCommand | undefined {
  const latest = strategy.versions.at(-1);
  const intervalSeconds = Number(minutes) * 60;
  const maxRuns = Number(runs);
  if (
    !latest ||
    !name.trim() ||
    !Number.isInteger(intervalSeconds) ||
    intervalSeconds < 60 ||
    intervalSeconds > 86400 ||
    !Number.isInteger(maxRuns) ||
    maxRuns < 1 ||
    maxRuns > 20 ||
    intervalSeconds * maxRuns > 7 * 86400
  )
    return;
  return {
    action: "task-review",
    name: name.trim(),
    strategyId: strategy.id,
    strategyVersion: latest.version,
    intervalSeconds,
    maxRuns,
  };
}
export function paperOutcome(outcome: string) {
  return (
    (
      {
        "paper-filled": "Filled (paper)",
        "price-skipped": "Price skipped",
        "price-held": "Price unavailable",
        "version-held": "Version changed",
        "order-cap-held": "Order cap reached",
        "budget-held": "Budget held",
        "binding-held": "Wallet changed",
        "expired-held": "Expired",
        "paper-unavailable": "Paper unavailable",
      } as Record<string, string>
    )[outcome] ?? outcome
  );
}

export function StrategyFacts({
  plan,
  version,
  wallet,
}: {
  plan: StrategyPlan;
  version: number;
  wallet?: string;
}) {
  const { t } = useUI();
  return (
    <dl className="task-facts">
      <dt>{t("Market")}</dt>
      <dd>{plan.market}</dd>
      <dt>{t("Version")}</dt>
      <dd>{version}</dd>
      {wallet && (
        <>
          <dt>{t("Wallet")}</dt>
          <dd>{wallet}</dd>
        </>
      )}
      <dt>{t("Network")}</dt>
      <dd>{t(plan.network === "devnet" ? "Devnet" : "Mainnet")}</dd>
      <dt>{t("Quantity")}</dt>
      <dd>{strategyUnits(plan.quantityAtoms, 9)} SOL</dd>
      <dt>{t("Limit price")}</dt>
      <dd>{strategyUnits(plan.limitPriceMicros, 6)} USDC</dd>
      <dt>{t("Order cap")}</dt>
      <dd>{strategyUnits(plan.orderBudgetMicros, 6)} USDC</dd>
      <dt>{t("Budget")}</dt>
      <dd>{strategyUnits(plan.totalBudgetMicros, 6)} USDC</dd>
    </dl>
  );
}
export function PaperResultFacts({ result }: { result: StrategyPaperResult }) {
  const { t } = useUI();
  return (
    <dl className="task-facts">
      <dt>{t("Market")}</dt>
      <dd>{result.market}</dd>
      <dt>{t("Version")}</dt>
      <dd>{result.strategyVersion}</dd>
      <dt>{t("Quantity")}</dt>
      <dd>{strategyUnits(result.quantityAtoms, 9)} SOL</dd>
      <dt>{t("Price")}</dt>
      <dd>{strategyUnits(result.priceMicros, 6)} USDC</dd>
      <dt>{t("Cost")}</dt>
      <dd>{strategyUnits(result.costMicros, 6)} USDC</dd>
      <dt>{t("Budget before")}</dt>
      <dd>{strategyUnits(result.budgetBeforeMicros, 6)} USDC</dd>
      <dt>{t("Budget left")}</dt>
      <dd>{strategyUnits(result.budgetAfterMicros, 6)} USDC</dd>
    </dl>
  );
}

export function AgentStrategies({
  view,
  requested,
  handled,
  created,
}: {
  view: AgentView;
  requested?: string;
  handled: () => void;
  created: () => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const { agents } = useWorkspace();
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [editing, setEditing] = useState<string>();
  const [draft, setDraft] = useState<StrategyDraft>(emptyDraft);
  const [scheduling, setScheduling] = useState<string>();
  const [timing, setTiming] = useState({ name: "", minutes: "60", runs: "1" });
  const [review, setReview] = useState<{
    command: AgentCommand;
    value: TaskReview;
  }>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const strategies = (view.strategies ?? []).filter(
    (s) => s.profileId === view.profileId,
  );
  const wallets = strategyWallets(accounts);
  const strategy = strategies.find((s) => s.id === scheduling);
  const latest = strategy?.versions.at(-1);
  const busy = agents.busy || loading;
  const plan = strategyPlan(draft, accounts);
  const stale =
    !!review &&
    (review.value.revision !== view.revision ||
      review.value.expiresAt * 1000 <= Date.now());
  async function refreshAccounts() {
    const attempt = generation.current;
    try {
      const v = await core.request<AccountsView>("accounts");
      if (attempt === generation.current) setAccounts(v.record.accounts);
    } catch (e) {
      if (attempt === generation.current)
        setError(e instanceof Error ? e.message : t("Wallet unavailable"));
    }
  }
  useEffect(() => {
    void refreshAccounts();
    return () => {
      generation.current++;
    };
  }, [core.request, core.instanceId]);
  function schedule(item: AgentStrategy) {
    setScheduling(item.id);
    setEditing(undefined);
    setReview(undefined);
    setTiming({ name: item.name, minutes: "60", runs: "1" });
    setError("");
  }
  useEffect(() => {
    const item = strategies.find((s) => s.id === requested);
    if (item) {
      schedule(item);
      handled();
    }
  }, [requested, view.strategies, handled]);
  function close() {
    setEditing(undefined);
    setScheduling(undefined);
    setReview(undefined);
    setError("");
  }
  async function prepare() {
    if (!strategy || busy) return;
    const command = strategyScheduleCommand(
      strategy,
      timing.name,
      timing.minutes,
      timing.runs,
    );
    if (!command) return;
    const attempt = generation.current;
    setLoading(true);
    setError("");
    setReview(undefined);
    try {
      const result = await core.request<AgentView>("agents", "POST", {
        ...command,
        profileId: view.profileId,
        revision: view.revision,
      });
      if (
        !result.taskReview?.strategyPlan ||
        result.taskReview.profileId !== view.profileId ||
        result.taskReview.strategyPlan.strategyId !== command.strategyId ||
        result.taskReview.strategyPlan.strategyVersion !==
          command.strategyVersion
      )
        throw Error(t("Task review unavailable"));
      if (attempt === generation.current)
        setReview({ command, value: result.taskReview });
    } catch (e) {
      if (attempt === generation.current)
        setError(e instanceof Error ? e.message : t("Task review unavailable"));
    } finally {
      if (attempt === generation.current) setLoading(false);
    }
  }
  const reviewPolicy = review?.value.strategyPlan;
  return (
    <section className="panel task-board" aria-label={t("Strategies")}>
      <div className="panel-heading">
        <h2>{t("Strategies")}</h2>
        <div className="row-actions">
          <Badge>{t("Paper")}</Badge>
          <button
            className="secondary"
            disabled={busy}
            onClick={() => {
              setEditing("");
              setDraft(emptyDraft);
              setScheduling(undefined);
              setReview(undefined);
              setError("");
              void refreshAccounts();
            }}
          >
            {t("New strategy")}
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      <div
        className={`task-workspace ${editing !== undefined || scheduling ? "with-detail" : ""}`}
      >
        <div className="task-list">
          {!strategies.length ? (
            <p className="empty-row">{t("No strategies")}</p>
          ) : (
            <table className="task-table" aria-label={t("Saved strategies")}>
              <thead>
                <tr>
                  <th>{t("Name")}</th>
                  <th>{t("Version")}</th>
                  <th>{t("Spent")}</th>
                  <th>{t("Actions")}</th>
                </tr>
              </thead>
              <tbody>
                {strategies.map((item) => {
                  const version = item.versions.at(-1);
                  return version ? (
                    <tr key={item.id}>
                      <td>{item.name}</td>
                      <td>{version.version}</td>
                      <td>
                        {strategyUnits(version.spentMicros, 6)} /{" "}
                        {strategyUnits(version.plan.totalBudgetMicros, 6)} USDC
                      </td>
                      <td>
                        <div className="row-actions">
                          <button
                            className="text-button"
                            disabled={busy}
                            aria-label={`${t("Edit")}: ${item.name}`}
                            onClick={() => {
                              setEditing(item.id);
                              setScheduling(undefined);
                              setReview(undefined);
                              setError("");
                              setDraft({
                                name: version.name,
                                accountId: version.plan.accountId,
                                quantity: strategyUnits(
                                  version.plan.quantityAtoms,
                                  9,
                                ),
                                price: strategyUnits(
                                  version.plan.limitPriceMicros,
                                  6,
                                ),
                                order: strategyUnits(
                                  version.plan.orderBudgetMicros,
                                  6,
                                ),
                                budget: strategyUnits(
                                  version.plan.totalBudgetMicros,
                                  6,
                                ),
                              });
                              void refreshAccounts();
                            }}
                          >
                            {t("Edit")}
                          </button>
                          <button
                            className="text-button"
                            disabled={busy}
                            aria-label={`${t("Schedule")}: ${item.name}`}
                            onClick={() => schedule(item)}
                          >
                            {t("Schedule")}
                          </button>
                        </div>
                      </td>
                    </tr>
                  ) : null;
                })}
              </tbody>
            </table>
          )}
        </div>
        {editing !== undefined && (
          <aside className="task-composer" aria-label={t("Strategy editor")}>
            <div className="panel-heading">
              <h3>{t(editing ? "Edit strategy" : "New strategy")}</h3>
              <button type="button" className="text-button" onClick={close}>
                {t("Close")}
              </button>
            </div>
            <form
              className="task-detail-body"
              onSubmit={async (e) => {
                e.preventDefault();
                if (!plan || busy || !draft.name.trim()) return;
                const attempt = generation.current;
                if (
                  (await agents.change({
                    action: "strategy-save",
                    ...(editing ? { id: editing } : {}),
                    name: draft.name.trim(),
                    strategy: plan,
                  })) &&
                  attempt === generation.current
                )
                  close();
              }}
            >
              <fieldset disabled={busy}>
                <label>
                  {t("Name")}
                  <input
                    aria-label={t("Strategy name")}
                    value={draft.name}
                    maxLength={80}
                    required
                    disabled={busy}
                    onChange={(e) =>
                      setDraft({ ...draft, name: e.target.value })
                    }
                  />
                </label>
                <label>
                  {t("Wallet")}
                  <div className="row-actions">
                    <select
                      aria-label={t("Strategy wallet")}
                      style={{ flex: 1, width: 0 }}
                      required
                      disabled={busy}
                      value={draft.accountId}
                      onChange={(e) =>
                        setDraft({ ...draft, accountId: e.target.value })
                      }
                    >
                      <option value="">
                        {t(wallets.length ? "Choose wallet" : "No wallets")}
                      </option>
                      {draft.accountId &&
                        !wallets.some((a) => a.id === draft.accountId) && (
                          <option value={draft.accountId} disabled>
                            {t("Wallet unavailable")}
                          </option>
                        )}
                      {wallets.map((a) => (
                        <option key={a.id} value={a.id}>
                          {a.name} ·{" "}
                          {t(a.network === "devnet" ? "Devnet" : "Mainnet")}
                        </option>
                      ))}
                    </select>
                    <button
                      type="button"
                      className="icon-button"
                      disabled={busy}
                      aria-label={t("Refresh wallets")}
                      onClick={() => void refreshAccounts()}
                    >
                      <RefreshCw size={14} />
                    </button>
                  </div>
                </label>
                <div className="task-facts">
                  <span>{t("Market")}</span>
                  <span>SOL/USDC</span>
                </div>
                {(
                  [
                    ["quantity", "Quantity (SOL)"],
                    ["price", "Limit (USDC)"],
                    ["order", "Order cap (USDC)"],
                    ["budget", "Budget (USDC)"],
                  ] as const
                ).map(([field, label]) => (
                  <label key={field}>
                    {t(label)}
                    <input
                      inputMode="decimal"
                      aria-label={t(label)}
                      value={draft[field]}
                      maxLength={24}
                      required
                      disabled={busy}
                      onChange={(e) =>
                        setDraft({ ...draft, [field]: e.target.value })
                      }
                    />
                  </label>
                ))}
                <button
                  className="primary"
                  disabled={busy || !plan || !draft.name.trim()}
                >
                  {t("Save version")}
                </button>
              </fieldset>
            </form>
          </aside>
        )}
        {strategy && latest && (
          <aside className="task-composer" aria-label={t("Schedule strategy")}>
            <div className="panel-heading">
              <h3>{t(review ? "Review task" : "Schedule strategy")}</h3>
              <button type="button" className="text-button" onClick={close}>
                {t("Close")}
              </button>
            </div>
            <div className="task-detail-body">
              {review && reviewPolicy ? (
                <>
                  <h3>{review.value.name}</h3>
                  <StrategyFacts
                    plan={reviewPolicy.plan}
                    version={reviewPolicy.strategyVersion}
                    wallet={
                      accounts.find(
                        (a) => a.id === reviewPolicy.binding.accountId,
                      )?.name ?? t("Wallet unavailable")
                    }
                  />
                  <dl className="task-facts">
                    <dt>{t("Interval (minutes)")}</dt>
                    <dd>{review.value.intervalSeconds / 60}</dd>
                    <dt>{t("Runs")}</dt>
                    <dd>{review.value.maxRuns}</dd>
                    <dt>{t("Expires")}</dt>
                    <dd>
                      {new Date(reviewPolicy.expiresAt * 1000).toLocaleString()}
                    </dd>
                  </dl>
                  {!view.paperExecutionAvailable && (
                    <Badge>{t("Paper unavailable")}</Badge>
                  )}
                  <div className="row-actions">
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => setReview(undefined)}
                    >
                      {t("Back")}
                    </button>
                    <button
                      className="primary"
                      disabled={busy || stale || !view.paperExecutionAvailable}
                      onClick={async () => {
                        const attempt = generation.current;
                        if (
                          (await agents.change({
                            ...review.command,
                            action: "task-reviewed-create",
                            revision: review.value.revision,
                            expiresAt: review.value.expiresAt,
                            consentDigest: review.value.digest,
                          })) &&
                          attempt === generation.current
                        ) {
                          close();
                          created();
                        }
                      }}
                    >
                      {t("Create task")}
                    </button>
                  </div>
                </>
              ) : (
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    void prepare();
                  }}
                >
                  <StrategyFacts
                    plan={latest.plan}
                    version={latest.version}
                    wallet={
                      accounts.find((a) => a.id === latest.binding.accountId)
                        ?.name ?? t("Wallet unavailable")
                    }
                  />
                  <fieldset disabled={busy}>
                    <label>
                      {t("Name")}
                      <input
                        aria-label={t("Task name")}
                        value={timing.name}
                        maxLength={80}
                        required
                        disabled={busy}
                        onChange={(e) =>
                          setTiming({ ...timing, name: e.target.value })
                        }
                      />
                    </label>
                    <label>
                      {t("Interval (minutes)")}
                      <input
                        aria-label={t("Interval (minutes)")}
                        type="number"
                        min="1"
                        max="1440"
                        step="1"
                        value={timing.minutes}
                        required
                        disabled={busy}
                        onChange={(e) =>
                          setTiming({ ...timing, minutes: e.target.value })
                        }
                      />
                    </label>
                    <label>
                      {t("Runs")}
                      <input
                        aria-label={t("Runs")}
                        type="number"
                        min="1"
                        max="20"
                        value={timing.runs}
                        required
                        disabled={busy}
                        onChange={(e) =>
                          setTiming({ ...timing, runs: e.target.value })
                        }
                      />
                    </label>
                    <button
                      className="primary"
                      disabled={
                        busy ||
                        !strategyScheduleCommand(
                          strategy,
                          timing.name,
                          timing.minutes,
                          timing.runs,
                        )
                      }
                    >
                      {t("Review")}
                    </button>
                  </fieldset>
                </form>
              )}
            </div>
          </aside>
        )}
      </div>
    </section>
  );
}
