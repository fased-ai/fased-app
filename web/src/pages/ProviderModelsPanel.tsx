import { useUI } from "../ui-preferences";
import type { Connection } from "../ai-connection";

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
  const needsRefresh =
    !view.locked &&
    (view.catalog?.state !== "ready" ||
      models.some((m) => m.availability === "stale"));
  return (
    <section className="provider-models" aria-label={uiText("Models")}>
      <header>
        <h3>{uiText("Models")}</h3>
        {needsRefresh && (
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={onRefresh}
          >
            {uiText("Refresh")}
          </button>
        )}
      </header>
      <ul>
        {models.map((m) => (
          <li key={m.id}>
            <span>{m.name}</span>
            <small>
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
            </small>
          </li>
        ))}
      </ul>
    </section>
  );
}
