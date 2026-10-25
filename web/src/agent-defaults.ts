type AgentDefaults = { provider: string; model: string };
type Access = { ref?: string; locked: boolean; model?: string } | null;
// Empty preferences mean explicit Demo mode. Unavailable provider preferences
// never become Demo or a different model. Permission remains server-enforced.
export function agentReplyDefaults(agent: AgentDefaults, access: Access) {
  if (
    agent.provider === "none" &&
    (!agent.model || agent.model === "fixture-openai-v1")
  )
    return { connectionRef: "", model: "", ready: true };
  return {
    connectionRef: agent.provider === "openai" ? access?.ref || "" : "",
    model: agent.model,
    ready:
      agent.provider === "openai" &&
      !!agent.model &&
      !!access?.ref &&
      !access.locked &&
      access.model === agent.model,
  };
}
