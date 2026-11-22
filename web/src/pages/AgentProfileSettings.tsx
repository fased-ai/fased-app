import type { FormEvent } from "react";
import { Plug } from "../icons";
import { AgentRulesMemoryPanel } from "../components/AgentRulesMemoryPanel";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace, type AgentSettingsDraft } from "../workspace";
import { modelForProviderChange } from "../agent-defaults";
import { AIModelPicker, useAIConnection } from "./AIConnection";
import { providerNames, type Provider } from "../state";
import type { AgentView } from "../agents";

export function AgentProfileSettings({ view: v }: { view: AgentView }) {
  const { t: uiText } = useUI();
  const t = uiText;
  const core = useCore();
  const {
    agents,
    setProvidersOpen,
    openProvidersWith,
    agentSettingsDrafts,
    setAgentSettingsDrafts,
  } = useWorkspace();
  const { view: connection } = useAIConnection();

  const profile = v.profiles.find((p) => p.id === v.profileId)!;
  const profileDraftKey = `${core.instanceId}:${v.profileId}`;
  const initialValues = {
    name: profile.name,
    persona: profile.persona,
    provider: profile.provider,
    model: profile.model,
    accountRef: profile.connectionRef || "",
  };
  const profileDraft = agentSettingsDrafts.get(profileDraftKey) ?? {
    values: initialValues,
    revision: v.revision,
    baseline: JSON.stringify(initialValues),
  };
  const { name, persona, provider, model, accountRef } = profileDraft.values;

  function updateProfileDraft(patch: Partial<AgentSettingsDraft["values"]>) {
    setAgentSettingsDrafts((drafts) => {
      const draft = drafts.get(profileDraftKey) ?? profileDraft;
      const values = { ...draft.values, ...patch };
      const next = new Map(drafts);
      if (JSON.stringify(values) === draft.baseline)
        next.delete(profileDraftKey);
      else next.set(profileDraftKey, { ...draft, values });
      return next;
    });
  }

  function reloadProfile(expected?: AgentSettingsDraft) {
    setAgentSettingsDrafts((drafts) => {
      if (
        !drafts.has(profileDraftKey) ||
        (expected && drafts.get(profileDraftKey) !== expected)
      )
        return drafts;
      const next = new Map(drafts);
      next.delete(profileDraftKey);
      return next;
    });
  }

  function submit(fn: () => Promise<void>) {
    return (e: FormEvent) => {
      e.preventDefault();
      void fn();
    };
  }

  const hasAccount = !!(
    connection?.connections?.some((a) => a.provider === provider) ||
    (connection?.provider === provider && !!connection?.ref)
  );
  const showModel = provider === "none" || hasAccount || !!model;

  return (
    <div className="agent-settings-workspace">
      <nav className="panel agent-profile-list" aria-label={uiText("Agents")}>
        {v.profiles.map((item) => (
          <button
            key={item.id}
            type="button"
            aria-pressed={item.id === v.profileId}
            onClick={() => void agents.select(item.id)}
            disabled={agents.busy}
          >
            <span className="avatar small" aria-hidden="true">
              {item.name.slice(0, 2).toUpperCase()}
            </span>
            <span>{item.name}</span>
          </button>
        ))}
      </nav>
      <div className="agent-settings-stack">
        <section className="panel" aria-label={uiText("Agent settings")}>
          <div className="panel-heading">
            <h2>{uiText("Agent")}</h2>
            <button
              type="button"
              className="secondary"
              onClick={() => setProvidersOpen(true)}
            >
              <Plug size={16} />
              {t("Providers")}
            </button>
          </div>
          <form
            className="agent-record-body"
            onSubmit={submit(async () => {
              if (
                await agents.change({
                  action: "profile-update",
                  revision: profileDraft.revision,
                  name,
                  text: persona,
                  provider,
                  accountRef,
                  model,
                })
              ) {
                reloadProfile(profileDraft);
              }
            })}
          >
            <label>
              {uiText("Name")}
              <input
                disabled={agents.busy}
                required
                maxLength={80}
                value={name}
                onChange={(e) => updateProfileDraft({ name: e.target.value })}
              />
            </label>
            <label>
              {uiText("Instructions")}
              <textarea
                disabled={agents.busy}
                maxLength={4000}
                value={persona}
                onChange={(e) =>
                  updateProfileDraft({ persona: e.target.value })
                }
              />
            </label>
            <fieldset className="profile-defaults">
              <legend className="sr-only">{uiText("Model")}</legend>
              <label>
                {uiText("Provider")}
                <select
                  disabled={agents.busy}
                  value={provider}
                  aria-label={uiText("Provider")}
                  onChange={(e) => {
                    const nextProvider = e.target.value as Provider;
                    const a =
                      connection?.connections?.find(
                        (acc) => acc.provider === e.target.value,
                      ) ??
                      (connection?.provider === e.target.value
                        ? connection
                        : null);
                    updateProfileDraft({
                      provider: nextProvider,
                      accountRef: a?.ref || "",
                      model: modelForProviderChange(
                        nextProvider,
                        a?.catalog?.models.find((m) => m.recommended)?.id ||
                          connection?.catalog?.models.find(
                            (m) => m.provider === nextProvider && m.recommended,
                          )?.id,
                      ),
                    });
                  }}
                >
                  <option value="none">{uiText("No AI")}</option>
                  {(() => {
                    const items = connection?.catalog?.providers ?? [];
                    const isConnected = (id: string) =>
                      connection?.connections?.some((a) => a.provider === id) ||
                      (!!connection?.ref && connection?.provider === id);
                    const connected = items.filter((item) =>
                      isConnected(item.id),
                    );
                    const other = items.filter((item) => !isConnected(item.id));
                    return (
                      <>
                        {connected.length > 0 && (
                          <optgroup label={uiText("Connected")}>
                            {connected.map((item) => (
                              <option key={item.id} value={item.id}>
                                {item.name}
                              </option>
                            ))}
                          </optgroup>
                        )}
                        {other.length > 0 && (
                          <optgroup label={uiText("Other")}>
                            {other.map((item) => (
                              <option key={item.id} value={item.id}>
                                {item.name}
                              </option>
                            ))}
                          </optgroup>
                        )}
                      </>
                    );
                  })()}
                  {provider !== "none" &&
                    !connection?.catalog?.providers.some(
                      (item) => item.id === provider,
                    ) && (
                      <option value={provider} disabled>
                        {providerNames[provider]}
                      </option>
                    )}
                </select>
              </label>
              {provider !== "none" &&
                ((connection?.connections?.filter(
                  (a) => a.provider === provider,
                ).length ?? 0) >= 2 ||
                  (!!accountRef &&
                    !connection?.connections?.some(
                      (a) => a.ref === accountRef,
                    ))) && (
                  <label>
                    {uiText("Account")}
                    <select
                      aria-label={uiText("Account")}
                      value={accountRef}
                      disabled={agents.busy}
                      onChange={(e) =>
                        updateProfileDraft({ accountRef: e.target.value })
                      }
                    >
                      <option value="">
                        {uiText(provider === "openai" ? "Default" : "Select")}
                      </option>
                      {connection?.connections
                        ?.filter((a) => a.provider === provider)
                        .map((a) => (
                          <option key={a.ref} value={a.ref}>
                            {a.name ||
                              connection?.catalog?.providers.find(
                                (p) => p.id === a.provider,
                              )?.name ||
                              a.provider ||
                              uiText("Provider")}
                          </option>
                        ))}
                      {accountRef &&
                        !connection?.connections?.some(
                          (a) => a.ref === accountRef,
                        ) && (
                          <option value={accountRef} disabled>
                            {uiText("Unavailable")}
                          </option>
                        )}
                    </select>
                  </label>
                )}
              {showModel && (
                <div hidden={provider === "none"}>
                  <AIModelPicker
                    value={model}
                    onChange={(nextModel) =>
                      updateProfileDraft({ model: nextModel })
                    }
                    connectionRef={
                      provider !== "none"
                        ? accountRef ||
                          (connection?.provider === provider
                            ? connection.ref
                            : "") ||
                          ""
                        : ""
                    }
                    allowDemo={provider === "none"}
                    disabled={agents.busy}
                  />
                </div>
              )}
              {provider !== "none" && !hasAccount && (
                <button
                  type="button"
                  className="secondary"
                  onClick={() => openProvidersWith({ provider })}
                >
                  {`${uiText("Connect")} ${providerNames[provider] || provider}`}
                </button>
              )}
            </fieldset>
            <div className="row-actions">
              <button className="primary" disabled={agents.busy}>
                {uiText("Save agent")}
              </button>
            </div>
          </form>
        </section>
        <AgentRulesMemoryPanel profileId={v.profileId} />
      </div>
    </div>
  );
}
