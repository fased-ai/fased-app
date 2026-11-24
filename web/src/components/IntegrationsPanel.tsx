import { useEffect, useState } from "react";
import {
  deleteExternalMCPClient,
  evaluateDecision,
  formatDecisionAnswerSummary,
  getDecisionConfig,
  listExternalMCPClients,
  parseAllowedToolsInput,
  saveDecisionConfig,
  saveExternalMCPClient,
  syncExternalMCPClient,
  type DecisionEvaluateResponse,
  type DecisionStatus,
  type ExternalMCPServer,
} from "../integrations";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";

export function IntegrationsPanel() {
  const { t } = useUI();
  const { core } = useWorkspace();

  const [jevStatus, setJevStatus] = useState<DecisionStatus | null>(null);
  const [jevModel, setJevModel] = useState("jev-latest");
  const [jevKey, setJevKey] = useState("");
  const [jevBusy, setJevBusy] = useState(false);
  const [jevError, setJevError] = useState("");
  const [jevResult, setJevResult] = useState<DecisionEvaluateResponse | null>(
    null,
  );

  const [clients, setClients] = useState<ExternalMCPServer[]>([]);
  const [mcpId, setMcpId] = useState("");
  const [mcpName, setMcpName] = useState("");
  const [mcpEndpoint, setMcpEndpoint] = useState("");
  const [mcpToken, setMcpToken] = useState("");
  const [mcpAllowed, setMcpAllowed] = useState("");
  const [mcpAllowLocal, setMcpAllowLocal] = useState(false);
  const [mcpReadOnly, setMcpReadOnly] = useState(true);
  const [mcpBusyId, setMcpBusyId] = useState("");
  const [mcpError, setMcpError] = useState("");

  useEffect(() => {
    let cancelled = false;
    if (core.mode !== "ready") return;
    void Promise.all([
      getDecisionConfig(core),
      listExternalMCPClients(core),
    ]).then(
      ([dec, mcp]) => {
        if (cancelled) return;
        setJevStatus(dec);
        if (dec.model) setJevModel(dec.model);
        setClients(mcp.clients ?? []);
      },
      (err) => {
        if (cancelled) return;
        setJevError(err instanceof Error ? err.message : t("Status unknown"));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [core, t]);

  if (core.mode !== "ready") return null;

  async function runJevSave(clearKey: boolean) {
    if (jevBusy) return;
    setJevBusy(true);
    setJevError("");
    try {
      const updated = await saveDecisionConfig(core, {
        model: jevModel.trim() || undefined,
        apiKey: clearKey ? undefined : jevKey.trim() || undefined,
        clearKey,
      });
      setJevStatus(updated);
      setJevKey("");
    } catch (err) {
      setJevError(err instanceof Error ? err.message : "Could not save config");
    } finally {
      setJevBusy(false);
    }
  }

  async function runJevTriage() {
    if (jevBusy) return;
    setJevBusy(true);
    setJevError("");
    try {
      const res = await evaluateDecision(core, {
        state: "Verified spot inflow breakout across BTC and SOL perps",
        model: jevModel.trim() || undefined,
        questions: {
          urgency: { type: "noul", instructions: "Urgent signal?" },
          bias: { type: "choice", choices: ["bullish", "bearish"] },
        },
      });
      setJevResult(res);
    } catch (err) {
      setJevError(err instanceof Error ? err.message : "Evaluation failed");
    } finally {
      setJevBusy(false);
    }
  }

  async function saveMCPClient() {
    if (mcpBusyId) return;
    setMcpBusyId("save");
    setMcpError("");
    try {
      const saved = await saveExternalMCPClient(core, {
        id: mcpId.trim(),
        name: mcpName.trim(),
        endpoint: mcpEndpoint.trim(),
        allowLocalhost: mcpAllowLocal,
        readOnly: mcpReadOnly,
        authToken: mcpToken.trim() || undefined,
        allowedTools: parseAllowedToolsInput(mcpAllowed),
      });
      setClients((prev) => {
        const next = prev.filter((item) => item.id !== saved.id);
        return [...next, saved].sort((a, b) => a.id.localeCompare(b.id));
      });
      setMcpId("");
      setMcpName("");
      setMcpEndpoint("");
      setMcpToken("");
      setMcpAllowed("");
    } catch (err) {
      setMcpError(
        err instanceof Error ? err.message : "Could not save MCP client",
      );
    } finally {
      setMcpBusyId("");
    }
  }

  async function handleSync(id: string) {
    if (mcpBusyId) return;
    setMcpBusyId(id);
    setMcpError("");
    try {
      const updated = await syncExternalMCPClient(core, id);
      setClients((prev) =>
        prev.map((item) => (item.id === updated.id ? updated : item)),
      );
    } catch (err) {
      setMcpError(err instanceof Error ? err.message : "Sync failed");
    } finally {
      setMcpBusyId("");
    }
  }

  async function handleDelete(id: string) {
    if (mcpBusyId) return;
    setMcpBusyId(id);
    setMcpError("");
    try {
      await deleteExternalMCPClient(core, id);
      setClients((prev) => prev.filter((item) => item.id !== id));
    } catch (err) {
      setMcpError(err instanceof Error ? err.message : "Remove failed");
    } finally {
      setMcpBusyId("");
    }
  }

  return (
    <>
      <div className="settings-modal-card" data-testid="jev-card">
        <div className="settings-modal-row">
          <strong>{t("TypeSafe Jev")}</strong>
          <span className="pill">
            {jevStatus?.configured ? t("Configured") : t("Fallback")}
          </span>
        </div>
        <dl className="settings-detail-kv">
          <div>
            <dt>{t("Endpoint")}</dt>
            <dd className="mono">
              {jevStatus?.endpoint ?? "https://api.typesafe.ai/v1/systemone"}
            </dd>
          </div>
          <div>
            <dt>{t("Model")}</dt>
            <dd className="mono">{jevStatus?.model ?? "jev-latest"}</dd>
          </div>
          <div>
            <dt>{t("API key")}</dt>
            <dd>{jevStatus?.configured ? t("Saved") : t("Not set")}</dd>
          </div>
        </dl>
        <form
          className="settings-inline-form"
          aria-label={t("TypeSafe Jev")}
          onSubmit={(e) => {
            e.preventDefault();
            void runJevSave(false);
          }}
        >
          <input
            type="text"
            aria-label={t("Model")}
            placeholder={t("Model")}
            value={jevModel}
            disabled={jevBusy}
            onChange={(e) => setJevModel(e.target.value)}
          />
          <input
            type="password"
            autoComplete="off"
            aria-label={t("API key")}
            placeholder={t("API key")}
            value={jevKey}
            disabled={jevBusy}
            onChange={(e) => setJevKey(e.target.value)}
          />
          <button type="submit" className="primary" disabled={jevBusy}>
            {t("Save")}
          </button>
          {jevStatus?.configured && (
            <button
              type="button"
              className="secondary"
              disabled={jevBusy}
              onClick={() => void runJevSave(true)}
            >
              {t("Clear key")}
            </button>
          )}
          <button
            type="button"
            className="secondary"
            disabled={jevBusy}
            onClick={() => void runJevTriage()}
          >
            {t("Test triage")}
          </button>
        </form>
        {jevResult && (
          <dl className="settings-detail-kv" data-testid="jev-triage-result">
            <div>
              <dt>{jevResult.provider}</dt>
              <dd className="mono">{`${jevResult.latencyMs}ms`}</dd>
            </div>
            {Object.entries(jevResult.answers ?? {}).map(([key, ans]) => (
              <div key={key}>
                <dt>{key}</dt>
                <dd className="mono">{formatDecisionAnswerSummary(ans)}</dd>
              </div>
            ))}
          </dl>
        )}
        {jevError && <p role="alert">{jevError}</p>}
      </div>

      <div className="settings-modal-card" data-testid="external-mcp-card">
        <div className="settings-modal-row">
          <strong>{t("External MCP")}</strong>
          <span className="pill">{clients.length}</span>
        </div>
        <form
          className="settings-mcp-form"
          aria-label={t("External MCP")}
          onSubmit={(e) => {
            e.preventDefault();
            void saveMCPClient();
          }}
        >
          <div className="settings-inline-form">
            <input
              type="text"
              aria-label={t("Server ID")}
              placeholder={t("Server ID")}
              value={mcpId}
              disabled={Boolean(mcpBusyId)}
              onChange={(e) => setMcpId(e.target.value)}
            />
            <input
              type="text"
              aria-label={t("Name")}
              placeholder={t("Name")}
              value={mcpName}
              disabled={Boolean(mcpBusyId)}
              onChange={(e) => setMcpName(e.target.value)}
            />
            <input
              type="url"
              aria-label={t("Endpoint")}
              placeholder="https://..."
              value={mcpEndpoint}
              disabled={Boolean(mcpBusyId)}
              onChange={(e) => setMcpEndpoint(e.target.value)}
            />
            <input
              type="password"
              autoComplete="off"
              aria-label={t("Bearer token")}
              placeholder={t("Bearer token")}
              value={mcpToken}
              disabled={Boolean(mcpBusyId)}
              onChange={(e) => setMcpToken(e.target.value)}
            />
            <input
              type="text"
              aria-label={t("Allowed tools")}
              placeholder={t("Allowed tools")}
              value={mcpAllowed}
              disabled={Boolean(mcpBusyId)}
              onChange={(e) => setMcpAllowed(e.target.value)}
            />
          </div>
          <div className="settings-mcp-form-actions">
            <label className="settings-checkbox-label">
              <input
                type="checkbox"
                checked={mcpAllowLocal}
                disabled={Boolean(mcpBusyId)}
                onChange={(e) => setMcpAllowLocal(e.target.checked)}
              />
              <span>{t("Allow localhost")}</span>
            </label>
            <label className="settings-checkbox-label">
              <input
                type="checkbox"
                checked={mcpReadOnly}
                disabled={Boolean(mcpBusyId)}
                onChange={(e) => setMcpReadOnly(e.target.checked)}
              />
              <span>{t("Read-only")}</span>
            </label>
            <button
              type="submit"
              className="primary"
              disabled={
                Boolean(mcpBusyId) ||
                !mcpId.trim() ||
                !mcpName.trim() ||
                !mcpEndpoint.trim()
              }
            >
              {t("Save client")}
            </button>
          </div>
        </form>
        {clients.length > 0 && (
          <div className="settings-mcp-server-list">
            {clients.map((srv) => (
              <article
                className="settings-mcp-server-row"
                key={srv.id}
                data-testid={`mcp-client-${srv.id}`}
              >
                <div className="settings-modal-row">
                  <div className="settings-mcp-server-meta">
                    <strong>{srv.name}</strong>
                    <code className="mono">{srv.id}</code>
                  </div>
                  <div className="settings-mcp-server-badges">
                    <span className="pill">
                      {srv.readOnly ? t("Read-only") : t("Enabled")}
                    </span>
                    <span className="pill">{`${t("Tools")}: ${srv.toolCount}`}</span>
                    {srv.hasAuthToken && (
                      <span className="pill">{t("Token")}</span>
                    )}
                    <button
                      type="button"
                      className="secondary"
                      disabled={Boolean(mcpBusyId)}
                      onClick={() => void handleSync(srv.id)}
                    >
                      {t("Sync")}
                    </button>
                    <button
                      type="button"
                      className="secondary"
                      disabled={Boolean(mcpBusyId)}
                      onClick={() => void handleDelete(srv.id)}
                    >
                      {t("Remove")}
                    </button>
                  </div>
                </div>
                <div className="mono settings-mcp-endpoint">{srv.endpoint}</div>
                {srv.tools && srv.tools.length > 0 && (
                  <div className="settings-mcp-tools mono">
                    {srv.tools.map((item) => item.name).join(", ")}
                  </div>
                )}
                {srv.lastError && <p role="alert">{srv.lastError}</p>}
              </article>
            ))}
          </div>
        )}
        {mcpError && <p role="alert">{mcpError}</p>}
      </div>
    </>
  );
}
