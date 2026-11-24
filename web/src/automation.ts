import type { AgentV2CoreClient } from "./agents-v2";

export type TriggerKind =
  "price" | "indicator" | "news" | "prediction" | "cron";

export type TriggerComparator =
  "above" | "below" | "cross_above" | "cross_below" | "pct_change_gte";

export type TriggerIndicator =
  "rsi_14" | "ema_cross" | "vwap_cross" | "volume_spike" | "cvd_delta";

export type TriggerActionType =
  "notify" | "jev_triage" | "agent_prompt" | "paper_alert";

export type NotificationSeverity = "info" | "warn" | "critical";

export type TriggerCondition = {
  venue?: string;
  symbol?: string;
  interval?: string;
  indicator?: TriggerIndicator | "";
  comparator?: TriggerComparator | "";
  threshold?: number;
  category?: string;
  region?: string;
  ticker?: string;
  query?: string;
  minUrgency?: number;
  sentiment?: string;
  minSpreadPct?: number;
  cronExpr?: string;
};

export type TriggerAction = {
  type: TriggerActionType;
  promptTemplate?: string;
  sendTelegram?: boolean;
  sendWebhook?: boolean;
  severity?: NotificationSeverity;
};

export type EventTrigger = {
  id: string;
  name: string;
  kind: TriggerKind;
  enabled: boolean;
  cooldownSeconds: number;
  condition: TriggerCondition;
  action: TriggerAction;
  lastEvaluatedAt?: number;
  lastFiredAt?: number;
  lastValue?: number;
  fireCount: number;
  createdAt: number;
  updatedAt: number;
};

export type TriggerInput = {
  id: string;
  name: string;
  kind: TriggerKind;
  enabled?: boolean;
  cooldownSeconds?: number;
  condition: TriggerCondition;
  action: TriggerAction;
};

export type NotificationItem = {
  id: string;
  triggerId?: string;
  triggerName?: string;
  kind: string;
  severity: NotificationSeverity | string;
  title: string;
  body: string;
  ticker?: string;
  venue?: string;
  urgency: number;
  read: boolean;
  untrusted: boolean;
  deliveryStatus?: string;
  createdAt: number;
};

export type NotificationList = {
  notifications: NotificationItem[];
  unreadCount: number;
  total: number;
};

export type AutomationControlState = {
  paused: boolean;
  killSwitchActive: boolean;
  killSwitchReason?: string;
  updatedAt: number;
  updatedBy?: string;
};

export type DeliveryReceipt = {
  id: string;
  kind: string;
  chatId: number;
  title: string;
  status: string;
  createdAt: number;
};

export type TelegramConfigView = {
  enabled: boolean;
  hasBotToken: boolean;
  botTokenMasked?: string;
  allowedChatIds: number[];
  minUrgency: number;
  notifyOnApproval: boolean;
  notifyOnKillSwitch: boolean;
  pollingActive: boolean;
  lastPollAt?: number;
  lastError?: string;
  recentDeliveries: DeliveryReceipt[];
};

export type TelegramConfigInput = {
  enabled: boolean;
  botToken?: string;
  clearToken?: boolean;
  allowedChatIds: number[];
  minUrgency: number;
  notifyOnApproval: boolean;
  notifyOnKillSwitch: boolean;
};

export type TelegramCommandReply = {
  authorized: boolean;
  command: string;
  text: string;
};

export type WebhookConfigView = {
  enabled: boolean;
  endpoint: string;
  hasSecret: boolean;
  secretMasked?: string;
  lastStatus?: string;
  lastError?: string;
  lastSentAt?: number;
};

export type WebhookConfigInput = {
  enabled: boolean;
  endpoint: string;
  secret?: string;
  clearSecret?: boolean;
};

export type EvalResult = {
  triggerId: string;
  matched: boolean;
  fired: boolean;
  suppressed?: string;
  metricValue?: number;
  summary: string;
  notification?: NotificationItem;
};

export type SweepResult = {
  evaluated: number;
  fired: number;
  skipped?: string;
  results: EvalResult[];
};

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export function parseChatIdsInput(raw: string): number[] {
  const seen = new Set<number>();
  const out: number[] = [];
  for (const part of raw.split(/[\s,;]+/)) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    if (!/^-?\d+$/.test(trimmed)) continue;
    const id = Number(trimmed);
    if (!Number.isSafeInteger(id) || id === 0 || seen.has(id)) continue;
    seen.add(id);
    out.push(id);
  }
  return out.slice(0, 8);
}

export function formatChatIdsInput(ids?: number[]): string {
  return (ids ?? []).join(", ");
}

export function automationStateLabel(
  state?: AutomationControlState | null,
): "Kill switch" | "Paused" | "Active" {
  if (state?.killSwitchActive) return "Kill switch";
  if (state?.paused) return "Paused";
  return "Active";
}

export function summarizeTriggerCondition(tr: EventTrigger): string {
  const c = tr.condition ?? {};
  switch (tr.kind) {
    case "price":
      return `${c.venue ?? "hyperliquid"} ${c.symbol ?? "BTC"} ${c.comparator ?? "above"} ${c.threshold ?? 0}`;
    case "indicator":
      return `${c.symbol ?? "BTC"} ${c.indicator ?? "rsi_14"} ${c.comparator ?? "above"} ${c.threshold ?? 0}`;
    case "news":
      return `${c.category || "all"} · ${c.region || "global"} · urgency >= ${Math.round((c.minUrgency ?? 0) * 100)}%`;
    case "prediction":
      return `Spread >= ${(c.minSpreadPct ?? 5).toFixed(1)}%`;
    case "cron":
      return `Cron ${c.cronExpr || "*/15 * * * *"}`;
    default:
      return tr.kind;
  }
}

export function slugifyTriggerId(name: string): string {
  const slug = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48);
  return slug || `trigger-${Date.now()}`;
}

export function getAutomationState(core: CoreRequestOnly) {
  return core.request<AutomationControlState>("automation/state");
}

export function setAutomationState(
  core: CoreRequestOnly,
  action: "pause" | "resume" | "kill" | "unkill",
  reason?: string,
) {
  return core.request<AutomationControlState>("automation/state", "POST", {
    action,
    reason,
  });
}

export function listEventTriggers(core: CoreRequestOnly) {
  return core.request<{ triggers: EventTrigger[]; count: number }>(
    "automation/triggers",
  );
}

export function saveEventTrigger(core: CoreRequestOnly, input: TriggerInput) {
  return core.request<EventTrigger>("automation/triggers", "PUT", input);
}

export function deleteEventTrigger(core: CoreRequestOnly, id: string) {
  return core.request<{ deleted: boolean; id: string }>(
    `automation/triggers/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export function testEventTrigger(
  core: CoreRequestOnly,
  id: string,
  dryRun = false,
) {
  return core.request<EvalResult>(
    `automation/triggers/${encodeURIComponent(id)}/test`,
    "POST",
    { dryRun },
  );
}

export function evaluateAutomationNow(core: CoreRequestOnly) {
  return core.request<SweepResult>("automation/evaluate", "POST");
}

export function listNotifications(
  core: CoreRequestOnly,
  params: { unreadOnly?: boolean; severity?: string; limit?: number } = {},
) {
  const q = new URLSearchParams();
  if (params.unreadOnly) q.set("unreadOnly", "1");
  if (params.severity) q.set("severity", params.severity);
  q.set("limit", String(params.limit ?? 50));
  return core.request<NotificationList>(
    `automation/notifications?${q.toString()}`,
  );
}

export function markNotificationsRead(
  core: CoreRequestOnly,
  payload: { id?: string; all?: boolean },
) {
  return core.request<{ updated: number }>(
    "automation/notifications/read",
    "POST",
    payload,
  );
}

export function getWebhookConfig(core: CoreRequestOnly) {
  return core.request<WebhookConfigView>("automation/webhook");
}

export function saveWebhookConfig(
  core: CoreRequestOnly,
  input: WebhookConfigInput,
) {
  return core.request<WebhookConfigView>("automation/webhook", "PUT", input);
}

export function getTelegramConfig(core: CoreRequestOnly) {
  return core.request<TelegramConfigView>("automation/telegram");
}

export function saveTelegramConfig(
  core: CoreRequestOnly,
  input: TelegramConfigInput,
) {
  return core.request<TelegramConfigView>("automation/telegram", "PUT", input);
}

export function sendTelegramTest(core: CoreRequestOnly) {
  return core.request<TelegramConfigView>("automation/telegram/test", "POST");
}

export function simulateTelegramCommand(
  core: CoreRequestOnly,
  chatId: number,
  text: string,
) {
  return core.request<TelegramCommandReply>(
    "automation/telegram/simulate",
    "POST",
    { chatId, text },
  );
}
