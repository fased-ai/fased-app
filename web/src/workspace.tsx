import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type ReactNode,
  type FormEvent,
} from "react";
import { initialState, reducer, restoreState, STORAGE_KEY } from "./state";
import { useUI } from "./ui-preferences";
import { useCore } from "./core";
import { AIConnectionProvider } from "./ai-connection";
import { useAgents } from "./agents";
import type { Action, Provider } from "./state";
import type { AccountsView } from "./accounts";
export type ProvidersPreset = {
  provider?: string;
  method?: "api-key" | "sign-in" | "local";
};
export type PendingChatPrompt = {
  id: string;
  profileId: string;
  title?: string;
  prompt: string;
};
export type PendingChatThread = {
  id: string;
  profileId: string;
  threadId: string;
};
export type AgentSettingsDraft = {
  values: {
    name: string;
    persona: string;
    provider: Provider;
    model: string;
    accountRef: string;
  };
  revision: number;
  baseline: string;
};
export type ChatModelDraft = { connectionRef: string; model: string };
export type FocusedRun = { instanceId: string; profileId: string; id: string };
export type TaskInputDraft = {
  name: string;
  text: string;
  prompt: string;
  bot: string;
  news: string;
  age: number;
  contains: string;
  seconds: number;
  unit: number;
  runs: number;
  calendarMode: boolean;
  cronMode: boolean;
  zone: string;
  cron: import("./pages/TaskCronFields").CronSettings;
  at: string;
  analyze: boolean;
  failure: "required" | "optional";
  overridden: boolean;
  connectionRef: string;
  model: string;
  session: string;
  input: number;
  output: number;
  deadline?: number;
  perCall?: number;
  budget?: number;
  minutes: number;
};
export type TaskComposerDraft = {
  open: boolean;
  watchMode: boolean;
  seedId?: string;
  kind?: "reminder" | "analysis";
  inputs?: Record<"reminder" | "analysis" | "watch", TaskInputDraft>;
};
function loadInitialState() {
  try {
    return restoreState(localStorage.getItem(STORAGE_KEY));
  } catch {
    return initialState();
  }
}
function defaultLandingRoute() {
  if (import.meta.env.DEV) {
    try {
      if (sessionStorage.getItem("fased.test.prototype") === "1")
        return "overview";
    } catch {}
  }
  return "overview";
}
function getRoute() {
  const raw = window.location.hash.slice(1);
  if (!raw) return defaultLandingRoute();
  const aliases: Record<string, string> = {
    dashboard: "overview",
    chat: "agents/chat",
    tasks: "agents/tasks",
    agents: "agents/settings",
    news: "agents/news",
    memory: "agents/memory",
    "agents/bots": "agents/tasks",
    terminals: "terminals/jupiter",
    mining: "terminals/satcoin",
    onchain: "terminals/jupiter",
    hyperliquid: "terminals/hyperliquid",
  };
  const route = aliases[raw] ?? raw;
  if (route === "terminals/asset" || route === "terminals/loss")
    return "terminals/satcoin";
  return /^(overview|activity|settings|providers|terminals(\/(satcoin|hyperliquid|jupiter))?|agents\/(chat|review|bots|tasks|news|memory|settings)|wallets\/(overview|accounts|permissions|security|activity))$/.test(
    route,
  )
    ? route
    : defaultLandingRoute();
}
function useWorkspaceState() {
  const { t } = useUI();
  const [prototypeState, localDispatch] = useReducer(
    reducer,
    undefined,
    loadInitialState,
  );
  const core = useCore();
  const agents = useAgents(core);
  const activeAgents = core.mode === "ready" ? agents.view : null;
  const state = useMemo(
    () =>
      core.mode === "ready" && core.setup
        ? {
            ...prototypeState,
            setup: core.setup.settings,
            ...(activeAgents
              ? {
                  profiles: activeAgents.profiles.map((p) => ({
                    id: p.id,
                    name: p.name,
                    role: p.persona,
                    initials: p.name
                      .split(" ")
                      .map((x) => x[0])
                      .join("")
                      .slice(0, 2),
                    color: "lime",
                    provider: p.provider,
                  })),
                  selectedProfile: activeAgents.profileId,
                  memories: activeAgents.memories.map((m) => ({
                    id: m.id,
                    profileId: m.profileId,
                    text: m.text,
                    source: m.source,
                    verified: new Date(m.updatedAt * 1000).toLocaleDateString(),
                  })),
                  notices: activeAgents.notices.map((n) => ({
                    id: n.id,
                    title: n.text,
                    detail:
                      "Saved by the local task engine. No financial execution.",
                    read: n.read,
                    tone: "info" as const,
                  })),
                }
              : {}),
          }
        : prototypeState,
    [core.mode, core.setup, activeAgents, prototypeState],
  );
  const agentsActionRef = useRef({
    select: agents.select,
    change: agents.change,
    notices: agents.view?.notices,
  });
  agentsActionRef.current = {
    select: agents.select,
    change: agents.change,
    notices: agents.view?.notices,
  };
  const dispatch = useCallback(
    (action: Action) => {
      if (core.mode === "ready" && action.type === "profile") {
        void agentsActionRef.current.select(action.id);
        return;
      }
      if (core.mode === "ready" && action.type === "read-notices") {
        void (async () => {
          for (const n of agentsActionRef.current.notices ?? []) {
            if (
              !n.read &&
              !(await agentsActionRef.current.change({
                action: "notice-read",
                id: n.id,
              }))
            )
              break;
          }
        })();
        return;
      }
      if (action.type === "setup" && core.mode === "ready") {
        void core.updateSetup(action.patch);
        return;
      }
      localDispatch(action);
    },
    [core.mode, core.updateSetup],
  );
  const [route, setRoute] = useState(getRoute);
  const routeRef = useRef(route);
  routeRef.current = route;
  const [walletSnapshot, setWalletSnapshot] = useState<{
    instance: string;
    view: AccountsView | null;
    error: string;
    loading: boolean;
  }>();
  const [walletRefresh, setWalletRefresh] = useState(0);
  useEffect(() => {
    let active = true;
    if (core.mode !== "ready") {
      setWalletSnapshot(undefined);
      return;
    }
    if (route !== "overview") return;
    const instance = core.instanceId;
    setWalletSnapshot({ instance, view: null, error: "", loading: true });
    void core.request<AccountsView>("accounts").then(
      (view) => {
        if (active)
          setWalletSnapshot({ instance, view, error: "", loading: false });
      },
      (error: unknown) => {
        if (active)
          setWalletSnapshot({
            instance,
            view: null,
            error:
              error instanceof Error ? error.message : "Wallets unavailable",
            loading: false,
          });
      },
    );
    return () => {
      active = false;
    };
  }, [core.mode, core.instanceId, core.request, route, walletRefresh]);
  const refreshWallets = useCallback(
    () => setWalletRefresh((revision) => revision + 1),
    [],
  );
  const wallets = useMemo(
    () => ({
      ...(core.mode === "ready" && walletSnapshot?.instance === core.instanceId
        ? walletSnapshot
        : { view: null, error: "", loading: false }),
      refresh: refreshWallets,
    }),
    [core.mode, core.instanceId, walletSnapshot, refreshWallets],
  );
  const [focusedTask, setFocusedTask] = useState("");
  const [focusedRun, setFocusedRun] = useState<FocusedRun | null>(null);
  const [chatDrafts, setChatDrafts] = useState(new Map<string, string>());
  const [chatModelDrafts, setChatModelDrafts] = useState(
    new Map<string, ChatModelDraft>(),
  );
  const [agentSettingsDrafts, setAgentSettingsDrafts] = useState(
    new Map<string, AgentSettingsDraft>(),
  );
  const [taskComposerDrafts, setTaskComposerDrafts] = useState(
    new Map<string, TaskComposerDraft>(),
  );
  const [setupOpen, setSetupOpen] = useState(false);
  const providersOpen = route === "providers";
  const [providersPreset, setProvidersPreset] =
    useState<ProvidersPreset | null>(null);
  const [providerReturn, setProviderReturn] = useState("agents/settings");
  const navigate = useCallback((path: string) => {
    window.location.hash = path;
    if (path === routeRef.current) {
      setQuery("");
    }
  }, []);
  const setProvidersOpen = useCallback(
    (open: boolean) => {
      if (open) {
        if (routeRef.current !== "providers")
          setProviderReturn(routeRef.current);
        navigate("providers");
      } else navigate(providerReturn);
    },
    [navigate, providerReturn],
  );
  const openProvidersWith = useCallback(
    (preset?: ProvidersPreset) => {
      setProvidersPreset(preset ?? null);
      setProvidersOpen(true);
    },
    [setProvidersOpen],
  );
  const clearProvidersPreset = useCallback(() => {
    setProvidersPreset(null);
  }, []);
  const [noticesOpen, setNoticesOpen] = useState(false);
  const [actionOpen, setActionOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [contextOpen, setContextOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [pendingChatPrompt, setPendingChatPrompt] =
    useState<PendingChatPrompt | null>(null);
  const [pendingChatThread, setPendingChatThread] =
    useState<PendingChatThread | null>(null);
  const [query, setQuery] = useState("");
  const [storageError, setStorageError] = useState(false);
  const [toast, setToast] = useState("");
  const [messages, setMessages] = useState<{ role: string; text: string }[]>(
    [],
  );
  const [message, setMessage] = useState("");
  const [memoryText, setMemoryText] = useState("");
  const [profileName, setProfileName] = useState("");
  const profile = state.profiles.find((p) => p.id === state.selectedProfile)!;
  const [section, tab = "overview"] = route.split("/");
  const unread = state.notices.filter((n) => !n.read).length;
  useEffect(() => {
    const fn = () => {
      const next = getRoute();
      if (window.location.hash === "#terminals")
        window.history.replaceState(window.history.state, "", `#${next}`);
      if (window.location.hash === "#agents/bots") {
        setFocusedTask("strategies");
        window.history.replaceState(window.history.state, "", `#${next}`);
      }
      if (next === "terminals/satcoin" && window.location.hash !== `#${next}`)
        window.history.replaceState(window.history.state, "", `#${next}`);
      setRoute(next);
      setQuery("");
    };
    fn();
    window.addEventListener("hashchange", fn);
    return () => window.removeEventListener("hashchange", fn);
  }, []);
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(prototypeState));
      setStorageError(false);
    } catch {
      setStorageError(true);
    }
  }, [prototypeState]);
  useEffect(() => {
    if (!toast) return;
    const id = setTimeout(() => setToast(""), 4500);
    return () => clearTimeout(id);
  }, [toast]);
  useEffect(() => {
    setMessages([]);
    setMessage("");
  }, [state.selectedProfile]);
  const launchChatPrompt = useCallback(
    (input: { profileId: string; title?: string; prompt: string }) => {
      setPendingChatPrompt({
        id: crypto.randomUUID(),
        profileId: input.profileId,
        title: input.title,
        prompt: input.prompt,
      });
      navigate("agents/chat");
    },
    [navigate],
  );
  const clearChatPrompt = useCallback((id: string) => {
    setPendingChatPrompt((cur) => (cur?.id === id ? null : cur));
  }, []);
  const openChatThread = useCallback(
    (input: { profileId: string; threadId: string }) => {
      setPendingChatThread({
        id: crypto.randomUUID(),
        profileId: input.profileId,
        threadId: input.threadId,
      });
      navigate("agents/chat");
    },
    [navigate],
  );
  const clearChatThread = useCallback((id: string) => {
    setPendingChatThread((cur) => (cur?.id === id ? null : cur));
  }, []);
  const notify = useCallback(
    (
      title: string,
      detail = "Recorded in this prototype only. No external action was performed.",
    ) => {
      dispatch({
        type: "notice",
        notice: {
          id: crypto.randomUUID(),
          title,
          detail,
          read: false,
          tone: "success",
        },
      });
      setToast(title);
    },
    [dispatch],
  );
  const sendChat = useCallback(
    (event: FormEvent) => {
      event.preventDefault();
      const text = message.trim();
      if (!text) return;
      setMessages((old) => [
        ...old,
        { role: "you", text },
        {
          role: "agent",
          text: "Example response: I can help turn this into a reviewable task or strategy. In the working app I would use your selected sources and provider, then show the inputs, limits and proposed action. This prototype has not contacted an AI provider.",
        },
      ]);
      setMessage("");
    },
    [message],
  );
  const searchItems = useMemo(
    () =>
      [
        ...[
          ["Dashboard", "overview"],
          ["Chat", "agents/chat"],
          ["Tasks", "agents/tasks"],
          ["Agents", "agents/settings"],
          ["News", "agents/news"],
          ["Wallets", "wallets/overview"],
          ["Settings", "settings"],
          ["Activity", "activity"],
        ].map(([title, path]) => ({ title, path })),
        { title: "Mining", path: "terminals/satcoin" },
        { title: "Onchain", path: "terminals/jupiter" },
        { title: "Hyperliquid", path: "terminals/hyperliquid" },
        { title: "Memory", path: "agents/memory" },
        { title: "Permissions", path: "wallets/permissions" },
        { title: "Providers", path: "providers" },
      ].filter(
        (item) =>
          item.title &&
          t(item.title).toLowerCase().includes(query.toLowerCase()),
      ),
    [query, t],
  );

  return useMemo(
    () => ({
      core,
      agents,
      wallets,
      focusedTask,
      setFocusedTask,
      focusedRun,
      setFocusedRun,
      chatDrafts,
      setChatDrafts,
      chatModelDrafts,
      setChatModelDrafts,
      agentSettingsDrafts,
      setAgentSettingsDrafts,
      taskComposerDrafts,
      setTaskComposerDrafts,
      state,
      dispatch,
      route,
      setupOpen,
      setSetupOpen,
      providersOpen,
      setProvidersOpen,
      providersPreset,
      openProvidersWith,
      clearProvidersPreset,
      noticesOpen,
      setNoticesOpen,
      actionOpen,
      setActionOpen,
      profileOpen,
      setProfileOpen,
      contextOpen,
      setContextOpen,
      resetOpen,
      setResetOpen,
      query,
      setQuery,
      storageError,
      toast,
      setToast,
      messages,
      setMessages,
      message,
      setMessage,
      memoryText,
      setMemoryText,
      profileName,
      setProfileName,
      profile,
      section,
      tab,
      unread,
      navigate,
      notify,
      sendChat,
      searchItems,
      pendingChatPrompt,
      launchChatPrompt,
      clearChatPrompt,
      pendingChatThread,
      openChatThread,
      clearChatThread,
    }),
    [
      core,
      agents,
      wallets,
      focusedTask,
      focusedRun,
      chatDrafts,
      chatModelDrafts,
      agentSettingsDrafts,
      taskComposerDrafts,
      state,
      dispatch,
      route,
      setupOpen,
      providersOpen,
      setProvidersOpen,
      providersPreset,
      openProvidersWith,
      clearProvidersPreset,
      noticesOpen,
      actionOpen,
      profileOpen,
      contextOpen,
      resetOpen,
      query,
      storageError,
      toast,
      messages,
      message,
      memoryText,
      profileName,
      profile,
      section,
      tab,
      unread,
      navigate,
      notify,
      sendChat,
      searchItems,
      pendingChatPrompt,
      launchChatPrompt,
      clearChatPrompt,
      pendingChatThread,
      openChatThread,
      clearChatThread,
    ],
  );
}
const WorkspaceContext = createContext<ReturnType<
  typeof useWorkspaceState
> | null>(null);
export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const value = useWorkspaceState();
  return (
    <AIConnectionProvider>
      <WorkspaceContext.Provider value={value}>
        {children}
      </WorkspaceContext.Provider>
    </AIConnectionProvider>
  );
}
export function useWorkspace() {
  const value = useContext(WorkspaceContext);
  if (!value) throw new Error("Workspace provider is missing");
  return value;
}
