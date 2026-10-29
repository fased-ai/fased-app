import { isProvider } from "./state";
type AgentDefaults = { provider: string; model: string };
type Access = {
  ref?: string;
  provider?: string;
  locked: boolean;
  model?: string;
} | null;
// Empty preferences mean explicit Demo mode. Unavailable provider preferences
// never become Demo or a different model. Permission remains server-enforced.
export function agentReplyDefaults(agent: AgentDefaults, access: Access) {
  if (
    agent.provider === "none" &&
    (!agent.model || agent.model === "fixture-openai-v1")
  )
    return { connectionRef: "", model: "", ready: true };
  return {
    connectionRef: agent.provider !== "none" ? access?.ref || "" : "",
    model: agent.model,
    ready:
      isProvider(agent.provider) &&
      agent.provider !== "none" &&
      (access?.provider || "openai") === agent.provider &&
      !!agent.model &&
      !!access?.ref &&
      !access.locked &&
      access.model === agent.model,
  };
}

// A deliberate provider change starts with that provider's admitted model.
// Never retain a model belonging to the previous provider.
export function modelForProviderChange(
  provider: string,
  admittedModel?: string,
) {
  if (provider === "local" && admittedModel?.startsWith("google/gemma-"))
    return admittedModel;
  const prefix =
    provider === "openai"
      ? "gpt-"
      : provider === "anthropic"
        ? "claude-"
        : provider === "xai"
          ? "grok-"
          : provider === "ollama-cloud"
            ? "ollama-cloud/"
            : provider === "kimi-code"
              ? "kimi-code/"
              : provider === "moonshot-intl"
                ? "moonshot-intl/kimi-"
                : provider === "moonshot"
                  ? "kimi-"
                  : provider === "google"
                    ? "gemini-"
                    : provider === "openrouter"
                      ? "google/"
                      : provider === "qwen"
                        ? "qwen"
                        : provider === "opencode"
                          ? "opencode/"
                          : provider === "local"
                            ? "gemma"
                            : "";
  return prefix && admittedModel?.startsWith(prefix) ? admittedModel : "";
}
