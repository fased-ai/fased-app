import type { AgentV2CoreClient } from "./agents-v2";

export type GoalDomain =
  | "mining"
  | "trading"
  | "news"
  | "notifications"
  | "portfolio"
  | "research"
  | "monitoring"
  | "custom";

export type ScheduleKind =
  "continuous" | "interval" | "cron" | "once" | "event";

export type ExecMode = "standalone" | "ai" | "hybrid";
export type ApprovalPolicy = "manual" | "semi_auto" | "auto";
export type TaskStatus =
  "draft" | "active" | "paused" | "completed" | "stopped" | "killed" | "error";
export type EdgeCondition =
  "on_success" | "on_signal" | "on_threshold" | "on_failure" | "always";

export type ScheduleSpec = {
  intervalSec?: number;
  cronExpr?: string;
  runAt?: number;
  eventFilter?: string;
  roundPhase?: string;
};

export type ExecutionBinding = {
  walletHandle?: string;
  venue?: string;
  symbol?: string;
  accountId?: string;
  broker?: string;
  cluster?: string;
  notificationChannels?: string[];
};

export type TaskAttachments = {
  personaId?: string;
  skillIds?: string[];
  strategyId?: string;
  scriptId?: string;
  aiProvider?: string;
  aiModel?: string;
  hybridCadenceSec?: number;
  promptTemplate?: string;
};

export type TaskConstraints = {
  maxRuns?: number;
  expiresAt?: number;
  maxSpendUsd?: number;
  maxSpendLamports?: number;
  dailyLossCapUsd?: number;
  stopOnReversal?: boolean;
  cooldownSec?: number;
};

export type UniversalTask = {
  id: string;
  name: string;
  goalDomain: GoalDomain;
  goalObjective: string;
  scheduleKind: ScheduleKind;
  scheduleSpec: ScheduleSpec;
  execMode: ExecMode;
  approvalPolicy: ApprovalPolicy;
  executionBinding: ExecutionBinding;
  attachments: TaskAttachments;
  constraints: TaskConstraints;
  status: TaskStatus;
  tainted?: boolean;
  parentTaskId?: string;
  swarmGroupId?: string;
  templateId?: string;
  createdVia: string;
  policyHash: string;
  lastRunAt?: number;
  nextRunAt?: number;
  lastHybridAt?: number;
  runCount: number;
  errorCount: number;
  lastError?: string;
  createdAt: number;
  updatedAt: number;
};

export type TaskEdge = {
  id: string;
  parentTaskId: string;
  childTaskId: string;
  condition: EdgeCondition;
  payloadMapJson?: string;
  createdAt: number;
};

export type TaskRun = {
  id: string;
  taskId: string;
  parentRunId?: string;
  triggerSource: string;
  execMode: ExecMode;
  status: string;
  tainted?: boolean;
  startedAt: number;
  finishedAt: number;
  durationMs: number;
  tokensIn?: number;
  tokensOut?: number;
  costMicroUsd?: number;
  pnlMicroUsd?: number;
  satEarnedAtoms?: number;
  signalEmitted?: boolean;
  thresholdHit?: boolean;
  actionTaken?: string;
  decisionSummary?: string;
  resultJson?: string;
  error?: string;
};

export type TaskLog = {
  id: number;
  taskId: string;
  runId?: string;
  seq: number;
  timestamp: number;
  level: string;
  stage: string;
  message: string;
  metaJson?: string;
};

export type TaskPersona = {
  id: string;
  name: string;
  role: string;
  goalDomains: GoalDomain[];
  systemPrompt: string;
  riskProfile: string;
  builtIn: boolean;
  createdAt: number;
};

export type TaskSkill = {
  id: string;
  name: string;
  goalDomain: GoalDomain;
  instructions: string;
  allowedTools: string[];
  source: string;
  sha256: string;
  builtIn: boolean;
  createdAt: number;
};

export type TaskTemplate = {
  id: string;
  name: string;
  description: string;
  goalDomain: GoalDomain;
  execMode: ExecMode;
  defaultTask: UniversalTask;
  builtIn: boolean;
  createdAt: number;
};

export type TaskLibraries = {
  personas: TaskPersona[];
  skills: TaskSkill[];
  templates: TaskTemplate[];
};

export type TaskDetail = {
  task: UniversalTask;
  edges: TaskEdge[];
  runs: TaskRun[];
  logs: TaskLog[];
};

export type SwarmChildSpec = {
  name: string;
  templateId?: string;
  goalDomain?: GoalDomain;
  execMode?: ExecMode;
  personaId?: string;
  skillIds?: string[];
  objective?: string;
  condition?: EdgeCondition;
  dependsOn?: number[];
};

export type SwarmRequest = {
  name: string;
  goalDomain: GoalDomain;
  goalObjective: string;
  scheduleKind?: ScheduleKind;
  scheduleSpec?: ScheduleSpec;
  approvalPolicy?: ApprovalPolicy;
  executionBinding?: ExecutionBinding;
  constraints?: TaskConstraints;
  createdVia?: string;
  children: SwarmChildSpec[];
};

export type SwarmResponse = {
  swarmGroupId: string;
  rootTask: UniversalTask;
  children: UniversalTask[];
  edges: TaskEdge[];
};

export const TASK_GOAL_FILTERS: Array<{ id: "" | GoalDomain; label: string }> =
  [
    { id: "", label: "All" },
    { id: "mining", label: "Mining" },
    { id: "trading", label: "Trading" },
    { id: "news", label: "News" },
    { id: "monitoring", label: "Monitoring" },
    { id: "portfolio", label: "Portfolio" },
    { id: "research", label: "Research" },
  ];

export function formatTaskSchedule(
  task: Pick<UniversalTask, "scheduleKind" | "scheduleSpec">,
): string {
  switch (task.scheduleKind) {
    case "continuous":
      return task.scheduleSpec?.intervalSec
        ? `Stream · ${task.scheduleSpec.intervalSec}s`
        : "Stream";
    case "interval": {
      const s = task.scheduleSpec?.intervalSec || 60;
      if (s >= 3600 && s % 3600 === 0) return `Every ${s / 3600}h`;
      if (s >= 60 && s % 60 === 0) return `Every ${s / 60}m`;
      return `Every ${s}s`;
    }
    case "cron":
      return task.scheduleSpec?.cronExpr || "Cron";
    case "event":
      return task.scheduleSpec?.eventFilter || "Event";
    case "once":
      return "Once";
    default:
      return task.scheduleKind;
  }
}

export function statusPillTone(
  status: TaskStatus | string,
): "is-ok" | "is-warn" | "is-danger" | "" {
  if (status === "active" || status === "completed") return "is-ok";
  if (status === "paused" || status === "draft") return "is-warn";
  if (status === "killed" || status === "error" || status === "stopped")
    return "is-danger";
  return "";
}

export function needsStepUpToActivate(
  task: Pick<UniversalTask, "approvalPolicy" | "executionBinding" | "tainted">,
): boolean {
  return Boolean(
    task.tainted ||
    (task.approvalPolicy === "auto" &&
      (task.executionBinding?.walletHandle || task.executionBinding?.venue)),
  );
}

export async function listUniversalTasks(
  core: AgentV2CoreClient,
  filter: { goal?: string; status?: string; q?: string; limit?: number } = {},
): Promise<UniversalTask[]> {
  const params = new URLSearchParams();
  if (filter.goal) params.set("goal", filter.goal);
  if (filter.status) params.set("status", filter.status);
  if (filter.q) params.set("q", filter.q);
  if (filter.limit) params.set("limit", String(filter.limit));
  const suffix = params.toString() ? `?${params.toString()}` : "";
  const res = await core.request<{ tasks: UniversalTask[] }>(`tasks${suffix}`);
  return res.tasks ?? [];
}

export async function getTaskDetail(
  core: AgentV2CoreClient,
  id: string,
): Promise<TaskDetail> {
  return core.request<TaskDetail>(`tasks/${encodeURIComponent(id)}`);
}

export async function listTaskLibraries(
  core: AgentV2CoreClient,
): Promise<TaskLibraries> {
  return core.request<TaskLibraries>("tasks/libraries");
}

export async function createUniversalTask(
  core: AgentV2CoreClient,
  input: Partial<UniversalTask>,
  stepUpToken?: string,
): Promise<UniversalTask> {
  const res = await core.request<{ task: UniversalTask }>(
    "tasks",
    "POST",
    input,
    undefined,
    stepUpToken ? { "X-Fased-Step-Up": stepUpToken } : undefined,
  );
  return res.task;
}

export async function instantiateTaskTemplate(
  core: AgentV2CoreClient,
  templateId: string,
  overrides: Partial<UniversalTask> = {},
  stepUpToken?: string,
): Promise<UniversalTask> {
  const res = await core.request<{ task: UniversalTask }>(
    "tasks/from-template",
    "POST",
    { templateId, overrides },
    undefined,
    stepUpToken ? { "X-Fased-Step-Up": stepUpToken } : undefined,
  );
  return res.task;
}

export async function createTaskSwarm(
  core: AgentV2CoreClient,
  req: SwarmRequest,
): Promise<SwarmResponse> {
  return core.request<SwarmResponse>("tasks/swarm", "POST", req);
}

export async function activateUniversalTask(
  core: AgentV2CoreClient,
  id: string,
  stepUpToken?: string,
): Promise<UniversalTask> {
  const res = await core.request<{ task: UniversalTask }>(
    `tasks/${encodeURIComponent(id)}/activate`,
    "POST",
    {},
    undefined,
    stepUpToken ? { "X-Fased-Step-Up": stepUpToken } : undefined,
  );
  return res.task;
}

export async function pauseUniversalTask(
  core: AgentV2CoreClient,
  id: string,
  reason = "",
): Promise<UniversalTask> {
  const res = await core.request<{ task: UniversalTask }>(
    `tasks/${encodeURIComponent(id)}/pause`,
    "POST",
    reason ? { reason } : {},
  );
  return res.task;
}

export async function stopUniversalTask(
  core: AgentV2CoreClient,
  id: string,
): Promise<UniversalTask> {
  const res = await core.request<{ task: UniversalTask }>(
    `tasks/${encodeURIComponent(id)}/stop`,
    "POST",
    {},
  );
  return res.task;
}

export async function deleteUniversalTask(
  core: AgentV2CoreClient,
  id: string,
): Promise<void> {
  await core.request(`tasks/${encodeURIComponent(id)}`, "DELETE");
}

export async function runUniversalTaskNow(
  core: AgentV2CoreClient,
  id: string,
): Promise<TaskRun> {
  const res = await core.request<{ run: TaskRun }>(
    `tasks/${encodeURIComponent(id)}/run`,
    "POST",
    {},
  );
  return res.run;
}

export async function killAllUniversalTasks(
  core: AgentV2CoreClient,
  reason = "Emergency stop from Tasks",
): Promise<string[]> {
  const res = await core.request<{ killedIds: string[] }>(
    "tasks/kill-all",
    "POST",
    { reason },
  );
  return res.killedIds ?? [];
}

export async function requestTaskStepUpToken(
  core: AgentV2CoreClient,
  password: string,
  targetId: string,
): Promise<string> {
  const res = await core.request<{ stepUpToken: string }>(
    "auth/step-up",
    "POST",
    { password, action: "task-activate", walletId: targetId },
  );
  return res.stepUpToken;
}
