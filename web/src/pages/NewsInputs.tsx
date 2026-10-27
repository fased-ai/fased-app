import { useUI } from "../ui-preferences";
import { useState } from "react";
import { Dialog } from "../components/shared";
import { RefreshCw, Settings2 } from "../icons";
import type { AgentView } from "../agents";
import { useWorkspace } from "../workspace";

const at = (n: number) =>
  n
    ? new Date(n * 1000).toLocaleString(
        typeof document === "undefined" ? "en" : document.documentElement.lang,
      )
    : "Not retrieved";
export function NewsSourcePicker({
  view,
  value,
  onChange,
  age,
  onAge,
}: {
  view: AgentView;
  value: string;
  onChange: (id: string) => void;
  age: number;
  onAge: (age: number) => void;
}) {
  const { t: uiText } = useUI();
  return (
    <div className="agent-form-grid">
      <label>
        {uiText("News source")}
        <select
          aria-label={uiText("News source")}
          value={value}
          onChange={(e) => {
            onChange(e.target.value);
            const s = view.newsSources.find((s) => s.id === e.target.value);
            if (s) onAge(s.maxAgeSeconds);
          }}
        >
          <option value="">{uiText("None")}</option>
          {value && !view.newsSources.some((s) => s.id === value) && (
            <option value={value} disabled>
              {uiText("Unavailable news source: ")}
              {value}
            </option>
          )}
          {view.newsSources.map((s) => (
            <option key={s.id} value={s.id} disabled={s.disabled}>
              {s.name}
              {s.disabled ? uiText(" · disabled") : ""}
            </option>
          ))}
        </select>
      </label>
      {value && (
        <label>
          {uiText("Freshness (minutes)")}
          <input
            type="number"
            min={1}
            max={1440}
            step="any"
            required
            value={age / 60}
            onChange={(e) => onAge(Math.round(e.target.valueAsNumber * 60))}
          />
        </label>
      )}
    </div>
  );
}
export function NewsInputs({
  view: v,
  onDiscuss,
}: {
  view: AgentView;
  onDiscuss: () => void;
}) {
  const { t } = useUI();
  const { agents } = useWorkspace();
  const [settings, setSettings] = useState(false);
  const [adding, setAdding] = useState(false);
  const [selected, setSelected] = useState("");
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [age, setAge] = useState(3600);
  const time = (n: number) => (n ? at(n) : t("Not retrieved"));
  const seen = new Set<string>();
  // Saved headlines remain inspectable after failure; they never become a fresh
  // task input through this presentation path. Execution still checks the core.
  const headlines = v.newsFetches
    .slice()
    .reverse()
    .filter(
      (f) => f.state === "completed" && (!selected || f.sourceId === selected),
    )
    .flatMap((fetch) => fetch.items.map((item) => ({ fetch, item })))
    .filter(({ fetch, item }) => {
      const key = `${fetch.sourceId}:${item.url}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  const state = (value: string) =>
    t(
      (
        {
          queued: "Queued",
          fetching: "Loading",
          completed: "Completed",
          failed: "Failed",
          cancelled: "Cancelled",
        } as Record<string, string>
      )[value] || value,
    );
  return (
    <section className="panel news-inputs news-feed" aria-label={t("News")}>
      <div className="panel-heading news-toolbar">
        <select
          aria-label={t("News source")}
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
        >
          <option value="">{t("All sources")}</option>
          {v.newsSources.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <button
          className="icon-button"
          aria-label={t("Refresh")}
          title={t("Refresh")}
          disabled={agents.busy}
          onClick={() => void agents.refresh().catch(() => {})}
        >
          <RefreshCw size={16} />
        </button>
        <button className="secondary" onClick={() => setSettings(true)}>
          <Settings2 size={16} />
          {t("Sources")}
        </button>
      </div>
      <div className="news-headlines">
        {v.newsSources
          .filter((s) => !selected || s.id === selected)
          .map((s) => {
            const latest = v.newsFetches
              .filter((f) => f.sourceId === s.id)
              .at(-1);
            if (!latest || latest.state === "completed") return null;
            return (
              <div className="news-status" role="status" key={s.id}>
                <span>
                  {s.name} · {state(latest.state)}
                </span>
                {latest.failure && (
                  <details>
                    <summary>{t("Details")}</summary>
                    <p>{latest.failure}</p>
                  </details>
                )}
                {(latest.state === "queued" || latest.state === "fetching") && (
                  <button
                    className="text-button"
                    disabled={agents.busy}
                    onClick={() =>
                      void agents.change({
                        action: "news-cancel",
                        id: latest.id,
                      })
                    }
                  >
                    {t("Cancel")}
                  </button>
                )}
              </div>
            );
          })}
        {!headlines.length && (
          <div className="news-empty">{t("No headlines")}</div>
        )}
        {headlines.map(({ fetch: f, item }) => {
          const source = v.newsSources.find((s) => s.id === f.sourceId);
          const latest = v.newsFetches
            .filter((x) => x.sourceId === f.sourceId)
            .at(-1);
          const freshness =
            source?.disabled || latest?.id !== f.id
              ? "Saved"
              : f.fresh
                ? "Fresh"
                : "Stale";
          return (
            <article className="news-headline" key={`${f.id}:${item.id}`}>
              <div className="news-headline-meta">
                <span>{source?.name || f.url}</span>
                <time
                  dateTime={new Date(item.publishedAt * 1000).toISOString()}
                >
                  {time(item.publishedAt)}
                </time>
                <span className={freshness === "Fresh" ? "news-fresh" : ""}>
                  {t(freshness)}
                </span>
              </div>
              <a
                className="news-title"
                href={item.url}
                target="_blank"
                rel="noopener noreferrer"
                referrerPolicy="no-referrer"
              >
                {item.title}
              </a>
              <div className="news-headline-actions">
                <button
                  className="text-button"
                  disabled={agents.busy}
                  onClick={async () => {
                    if (
                      await agents.change({
                        action: "news-item-explain",
                        id: f.id,
                        source: item.id,
                      })
                    )
                      onDiscuss();
                  }}
                >
                  {t("Discuss")}
                </button>
                <details className="news-details">
                  <summary>{t("Details")}</summary>
                  <p>
                    {t("Retrieved")} · {time(f.fetchedAt)}
                  </p>
                  <p>{item.url}</p>
                  <p>
                    {t("Source")} · {f.url}
                  </p>
                  <p className="mono">{f.bodySha256}</p>
                  <p>{t("Saved news is untrusted data.")}</p>
                </details>
              </div>
            </article>
          );
        })}
      </div>
      {settings && (
        <Dialog title={t("Sources")} onClose={() => setSettings(false)} wide>
          <div className="news-source-settings">
            {agents.error && <p role="alert">{agents.error}</p>}
            <div className="news-source-toolbar">
              <button className="secondary" onClick={() => setAdding(true)}>
                {t("Add source")}
              </button>
            </div>
            {adding && (
              <form
                className="news-source-form"
                onSubmit={(e) => {
                  e.preventDefault();
                  void agents
                    .change({
                      action: "news-source-add",
                      name,
                      source: url,
                      newsMaxAgeSeconds: age,
                    })
                    .then((ok) => {
                      if (ok) {
                        setName("");
                        setURL("");
                        setAdding(false);
                      }
                    });
                }}
              >
                <label>
                  {t("Name")}
                  <input
                    autoFocus
                    required
                    maxLength={80}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </label>
                <label>
                  {t("Feed URL")}
                  <input
                    type="url"
                    required
                    maxLength={300}
                    value={url}
                    onChange={(e) => setURL(e.target.value)}
                  />
                </label>
                <label>
                  {t("Freshness (minutes)")}
                  <input
                    type="number"
                    min={1}
                    max={1440}
                    step="any"
                    required
                    value={age / 60}
                    onChange={(e) =>
                      setAge(Math.round(e.target.valueAsNumber * 60))
                    }
                  />
                </label>
                <div className="agent-inline">
                  <button className="primary" disabled={agents.busy}>
                    {t("Add")}
                  </button>
                  <button
                    type="button"
                    className="text-button"
                    onClick={() => setAdding(false)}
                  >
                    {t("Cancel")}
                  </button>
                </div>
              </form>
            )}
            {!v.newsSources.length && <p>{t("No sources")}</p>}
            {v.newsSources.map((s) => {
              const fetches = v.newsFetches.filter((f) => f.sourceId === s.id);
              const latest = fetches.at(-1);
              return (
                <section
                  className="news-source"
                  role="group"
                  aria-label={t("Source {name}", { name: s.name })}
                  key={s.id}
                >
                  <div className="news-source-heading">
                    <strong>{s.name}</strong>
                    <span>{t(s.disabled ? "Disabled" : "Active")}</span>
                  </div>
                  <p className="news-source-url">{s.url}</p>
                  <p>
                    {t("Freshness (minutes)")} ·{" "}
                    {new Intl.NumberFormat(
                      document.documentElement.lang,
                    ).format(s.maxAgeSeconds / 60)}
                  </p>
                  <div className="agent-inline">
                    <button
                      className="secondary"
                      disabled={
                        agents.busy ||
                        s.disabled ||
                        latest?.state === "queued" ||
                        latest?.state === "fetching"
                      }
                      onClick={() =>
                        void agents.change({ action: "news-fetch", id: s.id })
                      }
                    >
                      {t("Fetch")}
                    </button>
                    <button
                      className="text-button"
                      disabled={agents.busy || s.disabled}
                      onClick={() =>
                        void agents.change({
                          action: "news-source-disable",
                          id: s.id,
                        })
                      }
                    >
                      {t("Disable")}
                    </button>
                  </div>
                  {fetches
                    .slice()
                    .reverse()
                    .map((f) => (
                      <details className="news-fetch" key={f.id}>
                        <summary>
                          {state(f.state)} · {time(f.createdAt)}
                        </summary>
                        <p>
                          {t("Retrieved")} · {time(f.fetchedAt)}
                        </p>
                        <p>{f.failure}</p>
                        <dl>
                          <dt>{t("Fetch")}</dt>
                          <dd>{f.id}</dd>
                          <dt>{t("Task")}</dt>
                          <dd>{f.taskId || t("None")}</dd>
                          <dt>{t("Source")}</dt>
                          <dd>{f.url}</dd>
                        </dl>
                        <p className="mono">{f.bodySha256}</p>
                        <p>
                          {f.bytes} {t("bytes")}
                        </p>
                        {f.items.map((item) => (
                          <p key={item.id}>
                            {item.title} · {time(item.publishedAt)}
                          </p>
                        ))}
                        {(f.state === "queued" || f.state === "fetching") && (
                          <button
                            className="text-button"
                            disabled={agents.busy}
                            onClick={() =>
                              void agents.change({
                                action: "news-cancel",
                                id: f.id,
                              })
                            }
                          >
                            {t("Cancel")}
                          </button>
                        )}
                      </details>
                    ))}
                </section>
              );
            })}
            <details className="source-rules">
              <summary>{t("Details")}</summary>
              <p>{t("Saved news is untrusted data.")}</p>
              <p>
                {t(
                  "Choose a public HTTPS RSS 2.0 feed without credentials or query parameters. Each fetch has a three-second deadline, 64 KiB body limit and at most five fresh headlines. No article crawling, AI call or trade is performed.",
                )}
              </p>
              <p>
                {t(
                  "To automate, select a source in Tasks. Each run fetches new evidence before its reminder, paper evaluation or optional fixture AI step. Unavailable/stale news holds dependent work. Other tasks and recovery continue. Retained snapshots are bounded and are not silently deleted.",
                )}
              </p>
            </details>
          </div>
        </Dialog>
      )}
    </section>
  );
}
