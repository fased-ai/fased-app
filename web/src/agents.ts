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
export type ChatMemorySource = "message" | "answer";
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
export type WENPaperInput = {
  budgetSOL: string;
  selection: string;
  validForSeconds: number;
};
export type WENPaperPlan = WENPaperInput & {
  version: string;
  fixtureSource: string;
  fixtureSha256: string;
  expiresAt: number;
  digest: string;
  approved: boolean;
};
export type WENPaperResult = {
  planDigest: string;
  fixtureSource: string;
  fixtureSha256: string;
  commitment: string;
  instructions: {
    operation: string;
    requestSha256: string;
    templateSha256: string;
  }[];
  accountSha256: string;
  receiptSha256: string;
  receipt: {
    amount: string;
    nonce: string;
    slot: string;
    settled: boolean;
    outcome: number | null;
    principal: string;
    sat: string;
    miner_input: string;
    selection: number | null;
  };
};
export type AgentTask = {
  wen?: WENPaperPlan;
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
  wen?: WENPaperResult;
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
  researchTurnId?: string;
  researchStep?: number;
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
export type ResearchAIReview = {
  schema: "wen-research-ai-review/1" | "wen-research-ai-review/2";
  revision: number;
  sourceState: string;
  dispatchEnabled: false;
  request: AgentAIRequest;
  turn: {
    schema: "wen-research-turn/1" | "wen-research-turn/2";
    id: string;
    state: string;
    currentChildId: string;
    currentStep: number;
    models: { id: string; step: number; frameSHA256?: string }[];
  };
};
export type ReminderReview = {
  version: string;
  profileId: string;
  cardId: string;
  requestId: string;
  sessionId: string;
  taskId: string;
  sourceTitle: string;
  sourceText: string;
  name: string;
  text: string;
  intervalSeconds: number;
  maxRuns: number;
  revision: number;
  expiresAt: number;
  digest: string;
};
export type AgentView = {
  reminderReview?: ReminderReview;
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
export type AgentMemoryEvidence = {
  memoryId: string;
  status:
    | "matched"
    | "free-form"
    | "invalid"
    | "unavailable"
    | "ambiguous"
    | "changed";
  kind?: "message" | "answer";
  sessionId?: string;
  id?: string;
  sha256?: string;
  text?: string;
};
export type AgentMemoryView = {
  revision: number;
  profileId: string;
  memories: AgentMemory[];
  source?: AgentMemoryEvidence;
};
export type AIMemorySelection = {
  mode: "all" | "none" | "selected";
  ids?: string[];
};
export type AIEvidenceSelection = {
  kind: AgentEvidenceKind;
  id: string;
  itemId?: string;
  sha256: string;
};
export type AgentAIRequestDraft = {
  revision: number;
  profileId: string;
  sessionId: string;
  prompt: string;
  model: string;
  connectionRef?: string;
  limits: AILimits;
  newsSourceId?: string;
  newsMaxAgeSeconds?: number;
  memorySelection?: AIMemorySelection;
  evidenceSelection?: AIEvidenceSelection;
};
export type AgentEvidenceKind = "memory" | "headline" | "reminder-run";
export type AgentEvidenceView = {
  version: "agent-evidence-v1";
  revision: number;
  profileId: string;
  id: string;
  itemId?: string;
  sha256: string;
  observedAt: number;
  recordedAt: number;
  historical: boolean;
  untrusted: true;
  freshness: "active" | "expired" | "fresh" | "stale" | "disabled" | "recorded";
} & (
  | {
      kind: "memory";
      data: {
        id: string;
        text: string;
        source: string;
        updatedAt: number;
        expiresAt: number;
        provenance: AgentMemoryEvidence;
      };
    }
  | {
      kind: "headline";
      data: {
        sourceName: string;
        sourceDisabled: boolean;
        headline: NewsItem;
        evidence: NewsEvidence;
      };
    }
  | {
      kind: "reminder-run";
      data: {
        task: { id: string; name: string; text: string };
        run: { id: string; at: number; strategy: string; outcome: string };
      };
    }
);
export type AgentCommand = {
  memorySelection?: AIMemorySelection;
  evidenceSelection?: AIEvidenceSelection;
  wen?: WENPaperInput;
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
  const [unavailable, setUnavailable] = useState(false);
  const disconnected = useRef(false);
  const readSequence = useRef(0);
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
      const sequence = ++readSequence.current;
      try {
        const v = await core.request<AgentView>(
          `agents?profile=${encodeURIComponent(profile ?? current.current?.profileId ?? "")}`,
        );
        if (epoch === generation.current) {
          accept(v);
          if (sequence === readSequence.current) {
            if (disconnected.current) setError("");
            disconnected.current = false;
            setUnavailable(false);
          }
        }
      } catch (e) {
        if (epoch === generation.current && sequence === readSequence.current) {
          disconnected.current = true;
          setUnavailable(true);
          setError(e instanceof Error ? e.message : "Workspace unavailable");
        }
        throw e;
      }
    },
    [core.request, accept],
  );
  useEffect(() => {
    generation.current++;
    current.current = null;
    setView(null);
    setError("");
    disconnected.current = false;
    setUnavailable(false);
    if (core.mode !== "ready") return;
    const load = () => refresh().catch(() => {});
    void load();
    const timer = setInterval(() => void load(), 5000);
    return () => {
      generation.current++;
      clearInterval(timer);
    };
  }, [core.mode, core.instanceId, refresh]);
  async function select(profile: string) {
    generation.current++;
    setError("");
    await refresh(profile).catch(() => {});
  }
  async function change(command: AgentCommand) {
    if (!current.current || writing.current || disconnected.current)
      return false;
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
  return { view, error, busy: busy || unavailable, refresh, select, change };
}
