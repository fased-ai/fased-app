import { useUI } from "../ui-preferences";
import { useEffect, useRef, useState } from "react";
import type { AgentView } from "../agents";
import { useCore } from "../core";
import "./ChatStrategyRevision.css";
import "./ResearchReadPanel.css";

// The selected-read API projection needs identities and labels only. Keep the
// complete received history object for its byte-bound hash; this type does not
// copy or depend on the private terminal implementation.
type Library = {
  revision: number;
  versions: { digest: string; name: string }[];
  bundles: (Record<string, unknown> & { scope: { dataset: string } })[];
};

type Source = {
  refId: string;
  kind: string;
  messageId?: string;
  textSHA256?: string;
  digest?: string;
  bundleIndex?: number;
  storedBundleSHA256?: string;
};
type Call = {
  id: string;
  tool: string;
  sourceRefs: string[];
  strategyRef?: string;
};
export type ResearchSelection = {
  schema: "wen-research-read/1";
  profile: string;
  sessionId: string;
  revision: number;
  libraryRevision: number;
  sources: Source[];
  calls: Call[];
};
type ReadResult = {
  schema: "wen-research-result/1";
  status: string;
  resultSHA256: string;
  results: {
    id: string;
    tool: string;
    status: string;
    data?: unknown;
    error?: string;
  }[];
  usage: {
    attempted: number;
    completed: number;
    failed: number;
    notRun: number;
  };
  sources: unknown[];
};
const hash = async (text: string) =>
  Array.from(
    new Uint8Array(
      await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text)),
    ),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
// GET returns Go's typed field order. Refuse unsafe browser integers, then match
// encoding/json escaping before hashing the stored bundle, never upload bytes.
export function storedBundleJSON(value: unknown): string {
  const check = (v: unknown): void => {
    if (typeof v === "number" && !Number.isSafeInteger(v))
      throw Error(
        "This bundle exceeds browser integer precision; use exact CLI inspection.",
      );
    if (v && typeof v === "object") Object.values(v).forEach(check);
  };
  check(value);
  return JSON.stringify(value).replace(
    /[<>&\u2028\u2029]/g,
    (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
}
export function ResearchReadPanel({
  view,
  sessionId,
  onClose,
  onUseAI,
}: {
  view: AgentView;
  sessionId: string;
  onClose: () => void;
  onUseAI?: (selection: ResearchSelection) => void;
}) {
  const { t: uiText } = useUI();
  const core = useCore();
  const [library, setLibrary] = useState<Library | null>(null);
  const [messages, setMessages] = useState<string[]>([]),
    [versions, setVersions] = useState<string[]>([]),
    [history, setHistory] = useState("");
  const [filter, setFilter] = useState("");
  const [selection, setSelection] = useState<ResearchSelection>();
  const [result, setResult] = useState<ReadResult | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const generation = useRef(0),
    active = useRef<AbortController | null>(null);
  const invalidate = () => {
    generation.current++;
    active.current?.abort();
    active.current = null;
    setBusy(false);
    setResult(null);
    setSelection(undefined);
    setError("");
  };
  useEffect(() => {
    const epoch = ++generation.current;
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    setLibrary(null);
    setMessages([]);
    setVersions([]);
    setHistory("");
    setFilter("");
    setResult(null);
    setSelection(undefined);
    setError("");
    setBusy(false);
    core
      .request<Library>(
        `wen/research?profile=${encodeURIComponent(view.profileId)}`,
        "GET",
        undefined,
        controller.signal,
      )
      .then((v) => {
        if (epoch === generation.current) setLibrary(v);
      })
      .catch((e) => {
        if (epoch === generation.current && !controller.signal.aborted)
          setError((e as Error).message);
      });
    return () => {
      generation.current++;
      controller.abort();
      active.current?.abort();
    };
  }, [view.profileId, view.revision, sessionId, core.request]);
  const toggle = (values: string[], id: string) =>
    values.includes(id) ? values.filter((x) => x !== id) : [...values, id];
  const inspect = async () => {
    if (!library) return;
    invalidate();
    const epoch = generation.current;
    const controller = new AbortController();
    active.current = controller;
    setBusy(true);
    try {
      const sources: Source[] = [],
        calls: Call[] = [];
      for (const id of messages) {
        const m = view.messages.find(
          (m) => m.id === id && m.sessionId === sessionId && m.role === "user",
        );
        if (!m) throw Error("Selected message changed; reopen research.");
        const refId = `m${sources.length}`;
        sources.push({
          refId,
          kind: "research-message",
          messageId: id,
          textSHA256: await hash(m.text),
        });
        calls.push({
          id: `read${calls.length}`,
          tool: "wen.research.snapshot",
          sourceRefs: [refId],
        });
      }
      const refs = new Map<string, string>();
      for (const digest of versions) {
        const refId = `s${sources.length}`;
        sources.push({ refId, kind: "strategy", digest });
        refs.set(digest, refId);
        calls.push({
          id: `read${calls.length}`,
          tool: "wen.strategy.inspect",
          sourceRefs: [refId],
        });
      }
      if (history !== "") {
        const index = Number(history),
          bundle = library.bundles[index];
        if (!bundle) throw Error("Selected history changed; reopen research.");
        const refId = `h${sources.length}`;
        sources.push({
          refId,
          kind: "history",
          bundleIndex: index,
          storedBundleSHA256: await hash(storedBundleJSON(bundle)),
        });
        calls.push({
          id: `read${calls.length}`,
          tool: "wen.history.summary",
          sourceRefs: [refId],
          ...(filter ? { strategyRef: refs.get(filter) } : {}),
        });
      }
      if (versions.length >= 2) {
        if (versions.length > 4) throw Error("Compare at most four versions.");
        calls.push({
          id: `read${calls.length}`,
          tool: "wen.strategy.compare",
          sourceRefs: versions.map((v) => refs.get(v)!),
        });
      }
      if (!calls.length || calls.length > 8 || sources.length > 8)
        throw Error("Select one to eight reads; comparison adds one read.");
      if (filter && !refs.has(filter))
        throw Error("Select the history filter's strategy too.");
      if (epoch !== generation.current || controller.signal.aborted) return;
      const body: ResearchSelection = {
        schema: "wen-research-read/1",
        profile: view.profileId,
        sessionId,
        revision: view.revision,
        libraryRevision: library.revision,
        sources,
        calls,
      };
      if (new TextEncoder().encode(JSON.stringify(body)).length > 65536)
        throw Error("Complete selection exceeds 64 KiB.");
      const output = await core.request<ReadResult>(
        "wen/research/read",
        "POST",
        body,
        controller.signal,
      );
      if (epoch === generation.current && !controller.signal.aborted) {
        setResult(output);
        setSelection(body);
      }
    } catch (e) {
      if (epoch === generation.current && !controller.signal.aborted)
        setError((e as Error).message);
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  };
  return (
    <section
      className="wen-chat-revision wen-research-inspection"
      aria-label={uiText("Research inspection")}
    >
      <h4>{uiText("Research inspection")}</h4>
      <fieldset disabled={!library}>
        <legend>{uiText("Saved messages")}</legend>
        {view.messages
          .filter((m) => m.sessionId === sessionId && m.role === "user")
          .map((m) => (
            <label key={m.id}>
              <input
                type="checkbox"
                checked={messages.includes(m.id)}
                onChange={() => {
                  invalidate();
                  setMessages(toggle(messages, m.id));
                }}
              />
              {m.text.split("\n", 1)[0].slice(0, 90)}
            </label>
          ))}
      </fieldset>
      <fieldset disabled={!library}>
        <legend>{uiText("Saved strategies")}</legend>
        {library?.versions.map((v) => (
          <label key={v.digest}>
            <input
              type="checkbox"
              checked={versions.includes(v.digest)}
              onChange={() => {
                invalidate();
                setVersions(toggle(versions, v.digest));
                setFilter("");
              }}
            />
            {v.name}
          </label>
        ))}
      </fieldset>
      <label>
        {uiText("Imported history")}
        <select
          aria-label={uiText("Imported history")}
          value={history}
          onChange={(e) => {
            invalidate();
            setHistory(e.target.value);
            setFilter("");
          }}
        >
          <option value="">{uiText("None")}</option>
          {library?.bundles.map((b, i) => (
            <option key={i} value={i}>
              {i + 1}: {b.scope.dataset}
              {uiText(" · unverified")}
            </option>
          ))}
        </select>
      </label>
      {history !== "" && (
        <label>
          {uiText("History strategy")}
          <select
            aria-label={uiText("History strategy")}
            value={filter}
            onChange={(e) => {
              invalidate();
              setFilter(e.target.value);
            }}
          >
            <option value="">{uiText("All declared entries")}</option>
            {library?.versions
              .filter((v) => versions.includes(v.digest))
              .map((v) => (
                <option key={v.digest} value={v.digest}>
                  {v.name}
                </option>
              ))}
          </select>
        </label>
      )}
      <div className="wen-research-actions">
        <button disabled={!library || busy} onClick={() => void inspect()}>
          {uiText("Inspect selected records")}
        </button>
        {busy && (
          <button className="secondary" onClick={invalidate}>
            {uiText("Cancel inspection")}
          </button>
        )}
        <button
          className="secondary"
          onClick={() => {
            invalidate();
            onClose();
          }}
        >
          {uiText("Close inspection")}
        </button>
      </div>
      {busy && <p role="status">{uiText("Reading selected records…")}</p>}
      {error && <p role="alert">{error}</p>}
      {result && (
        <div role="region" aria-label={uiText("Research results")}>
          {result.status === "complete" && selection && onUseAI && (
            <button className="secondary" onClick={() => onUseAI(selection)}>
              {uiText("Review research with AI")}
            </button>
          )}
          <p role="status">
            {result.status} · {result.usage.completed}/{result.usage.attempted}{" "}
            {uiText("completed")}
          </p>
          <pre aria-label={uiText("Response SHA-256")}>
            {result.resultSHA256}
          </pre>
          {result.results.map((r) => (
            <details key={r.id} open>
              <summary>
                {r.tool} · {r.status}
                {r.error ? ` · ${r.error}` : ""}
              </summary>
              <pre>{JSON.stringify(r, null, 2)}</pre>
            </details>
          ))}
          <details>
            <summary>{uiText("Source evidence")}</summary>
            <pre>{JSON.stringify(result.sources, null, 2)}</pre>
          </details>
        </div>
      )}
    </section>
  );
}
