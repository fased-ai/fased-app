import { useState } from "react";
import { Badge } from "../components/shared";
import type { AgentView } from "../agents";
import { useWorkspace } from "../workspace";

const at = (n: number) =>
  n ? new Date(n * 1000).toLocaleString() : "Not retrieved";
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
  return (
    <div className="agent-form-grid">
      <label>
        News input
        <select
          aria-label="News input"
          value={value}
          onChange={(e) => {
            onChange(e.target.value);
            const s = view.newsSources.find((s) => s.id === e.target.value);
            if (s) onAge(s.maxAgeSeconds);
          }}
        >
          <option value="">None</option>
          {value && !view.newsSources.some((s) => s.id === value) && (
            <option value={value} disabled>
              Unavailable news source: {value}
            </option>
          )}
          {view.newsSources.map((s) => (
            <option key={s.id} value={s.id} disabled={s.disabled}>
              {s.name}
              {s.disabled ? " · disabled" : ""}
            </option>
          ))}
        </select>
      </label>
      {value && (
        <label>
          Maximum news age (seconds)
          <input
            type="number"
            min={60}
            max={86400}
            required
            value={age}
            onChange={(e) => onAge(e.target.valueAsNumber)}
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
  const { agents } = useWorkspace();
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [age, setAge] = useState(3600);
  return (
    <section className="panel news-inputs" aria-label="News inputs">
      <div className="panel-heading">
        <h2>News inputs</h2>
        <Badge>PUBLIC RSS · UNTRUSTED</Badge>
      </div>
      <div className="agent-split news-workspace">
        <div className="agent-record-body news-library">
          {!v.newsSources.length && (
            <p className="subtle">No news sources saved.</p>
          )}

          <button
            className="secondary"
            disabled={agents.busy}
            onClick={() => void agents.refresh().catch(() => {})}
          >
            Refresh news records
          </button>
          {v.newsSources.map((s) => {
            const fetches = v.newsFetches.filter((f) => f.sourceId === s.id);
            const latest = fetches.at(-1);
            return (
              <article
                className="ai-request"
                key={s.id}
                aria-label={`News source ${s.name}`}
              >
                <h3>{s.name}</h3>
                <p>
                  {s.disabled ? "Disabled" : "Active"} · freshness{" "}
                  {s.maxAgeSeconds}s
                </p>
                <p className="mono">{s.url}</p>
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
                  Fetch headlines
                </button>
                <button
                  className="secondary"
                  disabled={agents.busy || s.disabled}
                  onClick={() =>
                    void agents.change({
                      action: "news-source-disable",
                      id: s.id,
                    })
                  }
                >
                  Disable source
                </button>
                {latest && (
                  <p>
                    Latest attempt: <strong>{latest.state}</strong> ·{" "}
                    {latest.fresh ? "Fresh" : "Not usable as fresh input"} ·{" "}
                    {at(latest.fetchedAt)}
                  </p>
                )}
                {fetches.map((f) => (
                  <details key={f.id} open={f.id === latest?.id}>
                    <summary>
                      {f.state} · {f.id}
                    </summary>
                    <p>
                      Requested {at(f.createdAt)} · retrieved {at(f.fetchedAt)}{" "}
                      · {f.bytes} bytes
                    </p>
                    <p>
                      Task: {f.taskId || "Owner fetch"} · source: {f.url}
                    </p>
                    {f.bodySha256 && (
                      <p className="mono">SHA-256: {f.bodySha256}</p>
                    )}
                    {f.failure && <p>{f.failure}</p>}
                    {f.items.map((item) => (
                      <div className="memory-record" key={item.id}>
                        <strong>{item.title}</strong>
                        <p>{item.url}</p>
                        <p>
                          Published {at(item.publishedAt)} · item {item.id}
                        </p>
                        {f.state === "completed" && (
                          <button
                            className="secondary"
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
                            Discuss headline
                          </button>
                        )}
                      </div>
                    ))}
                    {(f.state === "queued" || f.state === "fetching") && (
                      <button
                        className="secondary"
                        disabled={agents.busy}
                        onClick={() =>
                          void agents.change({
                            action: "news-cancel",
                            id: f.id,
                          })
                        }
                      >
                        Cancel news fetch
                      </button>
                    )}
                  </details>
                ))}
              </article>
            );
          })}
        </div>
        <aside
          className="agent-record-body news-composer"
          aria-label="Add news source"
        >
          <h3>Add a source</h3>
          <form
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
                  }
                });
            }}
          >
            <label>
              News source name
              <input
                required
                maxLength={80}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <label>
              Public RSS URL
              <input
                type="url"
                required
                maxLength={300}
                value={url}
                onChange={(e) => setURL(e.target.value)}
              />
            </label>
            <label>
              Source freshness limit (seconds)
              <input
                type="number"
                min={60}
                max={86400}
                required
                value={age}
                onChange={(e) => setAge(e.target.valueAsNumber)}
              />
            </label>
            <button className="primary" disabled={agents.busy}>
              Add news source
            </button>
          </form>{" "}
          <details className="agent-disclosure">
            <summary>Source rules and provenance</summary>

            <p>
              Choose a public HTTPS RSS 2.0 feed without credentials or query
              parameters. Each fetch has a three-second deadline, 64 KiB body
              limit and at most five fresh headlines. No article crawling, AI
              call or trade is performed.
            </p>
            <p>
              Publication times are publisher claims. Retrieval time and content
              hashes record what this core observed; they do not prove a
              headline is true. Feed text is untrusted data.
            </p>
            <p>
              To automate, select a source in Tasks. Each run fetches new
              evidence before its reminder, paper evaluation or optional fixture
              AI step. Unavailable/stale news holds dependent work. Other tasks
              and recovery continue. Retained snapshots are bounded and are not
              silently deleted.
            </p>
          </details>
        </aside>
      </div>
    </section>
  );
}
