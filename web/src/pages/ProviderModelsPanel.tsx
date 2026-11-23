import { useUI } from "../ui-preferences";
import type { Connection } from "../ai-connection";
import { RefreshCw } from "../icons";

export function ProviderModelsPanel({
  view,
  provider,
  busy,
  onRefresh,
}: {
  view: Connection;
  provider: string;
  busy: boolean;
  onRefresh: () => void;
}) {
  const { t: uiText } = useUI();
  const models = (view.catalog?.models ?? []).filter(
    (m) => m.connectionRef === view.ref && m.provider === provider,
  );
  return (
    <section className="provider-models" aria-label={uiText("Models")}>
      <header>
        <h3>{uiText("Models")}</h3>
        {!view.locked && (
          <button
            type="button"
            className="icon-button"
            aria-label={uiText("Refresh")}
            title={uiText("Refresh")}
            disabled={busy}
            onClick={onRefresh}
          >
            <RefreshCw size={15} className={busy ? "spin" : ""} />
          </button>
        )}
      </header>
      <ul>
        {models.map((m) => {
          const state = m.selectable
            ? m.recommended
              ? "recommended"
              : "ready"
            : m.availability === "stale"
              ? "stale"
              : "unavailable";
          return (
            <li key={m.id}>
              <span>{m.name}</span>
              <span className="provider-model-pill" data-state={state}>
                {uiText(
                  m.selectable
                    ? m.recommended
                      ? "Recommended"
                      : "Ready"
                    : m.availability === "stale"
                      ? "Stale"
                      : m.availability === "currency-unsupported"
                        ? "Pricing unavailable"
                        : "Unavailable",
                )}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
