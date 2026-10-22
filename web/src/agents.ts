export type OpenAIModelPolicy = {
  id: string;
  model: string;
  reviewedAt: number;
  expiresAt: number;
  inputMicrousdPerMillion: number;
  cachedMicrousdPerMillion: number;
  outputMicrousdPerMillion: number;
  contextTokens: number;
  outputTokens: number;
  modelSource: string;
  tariffSource: string;
  tokenSource: string;
};
export type OpenAIAdmission = {
  state: string;
  wireSha256: string;
  accessAt: number;
  countedAt: number;
  inputTokens: number;
  modelRecordSha256: string;
  countPayloadSha256: string;
  countResponseSha256: string;
  generationAttempted: boolean;
};
export type NewsSource = {
  id: string;
  profileId: string;
  name: string;
  url: string;
  maxAgeSeconds: number;
  disabled: boolean;
};
export type NewsItem = {
  id: string;
  title: string;
  url: string;
  publishedAt: number;
};
export type NewsFetch = {
  fresh: boolean;
  id: string;
  profileId: string;
  sourceId: string;
  taskId?: string;
  url: string;
  maxAgeSeconds: number;
  createdAt: number;
  fetchedAt: number;
  state: string;
  bodySha256: string;
  bytes: number;
  items: NewsItem[];
  failure: string;
};
export type NewsEvidence = {
  sourceId: string;
  fetchId: string;
  url: string;
  maxAgeSeconds: number;
  fetchedAt: number;
  bodySha256: string;
  digest: string;
};
export type TaskNewsInput = {
  sourceId: string;
  url: string;
  maxAgeSeconds: number;
  pendingId: string;
  fetchId: string;
};
import { useCallback, useEffect, useRef, useState } from "react";
import type { useCore } from "./core";
import type { Provider } from "./state";
export type AgentProfile = {
  id: string;
  name: string;
  persona: string;
  provider: Provider;
  model: string;
};
export type AgentSession = {
  id: string;
  profileId: string;
  name: string;
  createdAt: number;
};
export type AgentMessage = {
  id: string;
  profileId: string;
  sessionId: string;
  role: string;
  text: string;
  createdAt: number;
};
export type AgentMemory = {
  id: string;
  profileId: string;
  text: string;
  source: string;
  updatedAt: number;
  expiresAt: number;
  expired: boolean;
  history: { text: string; source: string; at: number; expiresAt: number }[];
};
export type PaperBot = {
  id: string;
  profileId: string;
  name: string;
  strategy: string;
  quantity: number;
  priceCents: number;
  budgetCents: number;
  spentCents: number;
};
export type TaskAIStep = {
  model?: string;
  policy?: OpenAIModelPolicy;
  connectionRef?: string;
  sessionId: string;
  context: string;
  disclosure: string;
  limits: AILimits;
  budgetMicrousd: number;
  createdAt: number;
  expiresAt: number;
  digest: string;
  authorized: boolean;
  pendingId: string;
  bot?: PaperBot;
};
export type AgentTask = {
  news?: TaskNewsInput;
  ai?: TaskAIStep;
  id: string;
  profileId: string;
  name: string;
  text: string;
  botId: string;
  intervalSeconds: number;
  maxRuns: number;
  runs: number;
  nextAt: number;
  state: string;
};
export type AgentRun = {
  news?: NewsEvidence;
  aiRequestId?: string;
  aiDigest?: string;
  id: string;
  profileId: string;
  taskId: string;
  botId: string;
  strategy: string;
  at: number;
  outcome: string;
  quantity: number;
  priceCents: number;
  costCents: number;
  budgetBefore: number;
};
export type AgentNotice = {
  id: string;
  profileId: string;
  text: string;
  runId: string;
  at: number;
  read: boolean;
};
export type AILimits = {
  maxInputTokens: number;
  maxOutputTokens: number;
  deadlineSeconds: number;
  budgetMicrousd: number;
};
export type AgentAIRequest = {
  policy?: OpenAIModelPolicy;
  admission?: OpenAIAdmission;
  cachedInputTokens?: number;
  costBasis?: string;
  news?: NewsEvidence;
  connectionRef?: string;
  taskId?: string;
  taskDigest?: string;
  id: string;
  profileId: string;
  sessionId: string;
  prompt: string;
  model: string;
  context: string;
  disclosure: string;
  digest: string;
  limits: AILimits;
  state: string;
  createdAt: number;
  heldMicrousd: number;
  spentMicrousd: number;
  inputTokens: number;
  outputTokens: number;
  answer: string;
  failure: string;
  cards: {
    id: string;
    kind: string;
    title: string;
    text: string;
    review: string;
  }[];
};
export type AgentView = {
  newsSources: NewsSource[];
  newsFetches: NewsFetch[];
  aiRequests: AgentAIRequest[];
  aiFixtureAvailable: boolean;
  revision: number;
  profileId: string;
  profiles: AgentProfile[];
  sessions: AgentSession[];
  messages: AgentMessage[];
  memories: AgentMemory[];
  bots: PaperBot[];
  tasks: AgentTask[];
  runs: AgentRun[];
  notices: AgentNotice[];
  providerCallsEnabled: false;
  financialExecution: false;
};
export type AgentCommand = {
  newsSourceId?: string;
  newsMaxAgeSeconds?: number;
  connectionRef?: string;
  revision?: number;
  action: string;
  profileId?: string;
  id?: string;
  name?: string;
  text?: string;
  source?: string;
  expiresAt?: number;
  provider?: Provider;
  model?: string;
  botId?: string;
  intervalSeconds?: number;
  maxRuns?: number;
  quantity?: number;
  priceCents?: number;
  budgetCents?: number;
  limits?: AILimits;
  consentDigest?: string;
  totalBudgetMicrousd?: number;
  authorizationSeconds?: number;
};

export function useAgents(core: ReturnType<typeof useCore>) {
  const [view, setView] = useState<AgentView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const current = useRef<AgentView | null>(null);
  const generation = useRef(0);
  const writing = useRef(false);
  const accept = useCallback((v: AgentView) => {
    if (!current.current || v.revision >= current.current.revision) {
      current.current = v;
      setView(v);
    }
  }, []);
  const refresh = useCallback(
    async (profile?: string) => {
      const epoch = generation.current;
      const v = await core.request<AgentView>(
        `agents?profile=${encodeURIComponent(profile ?? current.current?.profileId ?? "")}`,
      );
      if (epoch === generation.current) accept(v);
    },
    [core.request, accept],
  );
  useEffect(() => {
    generation.current++;
    current.current = null;
    setView(null);
    setError("");
    if (core.mode !== "ready") return;
    let active = true;
    const load = () =>
      refresh().catch((e) => {
        if (active)
          setError(e instanceof Error ? e.message : "Workspace unavailable");
      });
    void load();
    const timer = setInterval(() => void load(), 5000);
    return () => {
      active = false;
      generation.current++;
      clearInterval(timer);
    };
  }, [core.mode, core.instanceId, refresh]);
  async function select(profile: string) {
    generation.current++;
    setError("");
    try {
      await refresh(profile);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Profile unavailable");
    }
  }
  async function change(command: AgentCommand) {
    if (!current.current || writing.current) return false;
    writing.current = true;
    const epoch = generation.current;
    setBusy(true);
    setError("");
    try {
      const v = await core.request<AgentView>("agents", "POST", {
        profileId: current.current.profileId,
        ...command,
        revision: command.revision ?? current.current.revision,
      });
      if (epoch === generation.current) {
        accept(v);
        generation.current++;
      }
      return true;
    } catch (e) {
      if (epoch === generation.current) {
        setError(e instanceof Error ? e.message : "Change failed");
        await refresh().catch(() => {});
      }
      return false;
    } finally {
      writing.current = false;
      setBusy(false);
    }
  }
  return { view, error, busy, refresh, select, change };
}
