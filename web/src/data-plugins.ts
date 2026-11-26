import type { AgentV2CoreClient } from "./agents-v2";

type CoreRequestOnly = Pick<AgentV2CoreClient, "request">;

export type PluginCategory = "exchanges" | "news" | "predictions" | "rpcs";

export interface DataPluginView {
  id: string;
  name: string;
  category: PluginCategory | string;
  kind?: string;
  region?: string;
  cluster?: string;
  endpoint?: string;
  enabled: boolean;
  dataFeedEnabled: boolean;
  tradingEnabled: boolean;
  supportsFeed: boolean;
  supportsTrading: boolean;
  defaultChart?: boolean;
  builtIn: boolean;
  custom?: boolean;
  allowLocalhost?: boolean;
  hasApiKey: boolean;
  hasApiSecret: boolean;
  maskedApiKey?: string;
  lastCheckedAt?: number;
  lastStatus?: string;
  lastLatencyMs?: number;
  lastError?: string;
}

export interface DataPluginsSummary {
  activeTotal: number;
  activeExchanges: number;
  activeNews: number;
  activePredictions: number;
  activeRpcs: number;
}

export interface DataPluginsResponse {
  plugins: DataPluginView[];
  summary: DataPluginsSummary;
}

export interface DataPluginPatch {
  enabled?: boolean;
  dataFeedEnabled?: boolean;
  tradingEnabled?: boolean;
  defaultChart?: boolean;
  endpoint?: string;
  apiKey?: string;
  apiSecret?: string;
  allowLocalhost?: boolean;
}

export interface CustomDataPluginInput {
  name: string;
  category: PluginCategory;
  kind?: string;
  region?: string;
  cluster?: string;
  endpoint: string;
  enabled?: boolean;
  allowLocalhost?: boolean;
  apiKey?: string;
}

export const DATA_PLUGINS_CHANGED_EVENT = "fased-data-plugins-changed";

export function notifyDataPluginsChanged() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(DATA_PLUGINS_CHANGED_EVENT));
  }
}

export async function fetchDataPlugins(
  core: CoreRequestOnly,
): Promise<DataPluginsResponse> {
  return core.request<DataPluginsResponse>("data-plugins");
}

export async function updateDataPlugin(
  core: CoreRequestOnly,
  id: string,
  patch: DataPluginPatch,
): Promise<DataPluginView> {
  const updated = await core.request<DataPluginView>(
    `data-plugins/${encodeURIComponent(id)}`,
    "PUT",
    patch,
  );
  notifyDataPluginsChanged();
  return updated;
}

export async function addCustomDataPlugin(
  core: CoreRequestOnly,
  input: CustomDataPluginInput,
): Promise<DataPluginView> {
  const created = await core.request<DataPluginView>(
    "data-plugins/custom",
    "POST",
    input,
  );
  notifyDataPluginsChanged();
  return created;
}

export async function deleteCustomDataPlugin(
  core: CoreRequestOnly,
  id: string,
): Promise<void> {
  await core.request(`data-plugins/custom/${encodeURIComponent(id)}`, "DELETE");
  notifyDataPluginsChanged();
}

export async function testDataPlugin(
  core: CoreRequestOnly,
  id: string,
): Promise<DataPluginView> {
  return core.request<DataPluginView>(
    `data-plugins/${encodeURIComponent(id)}/test`,
    "POST",
    {},
  );
}
