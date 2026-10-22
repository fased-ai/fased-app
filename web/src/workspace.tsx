import {
  createContext,
  useContext,
  useEffect,
  useReducer,
  useState,
  type ReactNode,
  type FormEvent,
} from "react";
import { initialState, reducer, restoreState, STORAGE_KEY } from "./state";
import { navigation, terminalData } from "./fixtures";
import { useCore } from "./core";
import { useAgents } from "./agents";
import type { Action } from "./state";
function loadInitialState() {
  try {
    return restoreState(localStorage.getItem(STORAGE_KEY));
  } catch {
    return initialState();
  }
}
function getRoute() {
  const route = window.location.hash.slice(1);
  return /^(overview|activity|settings|terminals(\/(asset|loss|hyperliquid|jupiter))?|agents\/(chat|bots|tasks|news|memory|settings)|wallets\/(overview|accounts|permissions|security|activity))$/.test(
    route,
  )
    ? route
    : "overview";
}
function useWorkspaceState() {
  const [prototypeState, localDispatch] = useReducer(
    reducer,
    undefined,
    loadInitialState,
  );
  const core = useCore();
  const agents = useAgents(core);
  const activeAgents = core.mode === "ready" ? agents.view : null;
  const state =
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
      : prototypeState;
  function dispatch(action: Action) {
    if (core.mode === "ready" && action.type === "profile") {
      void agents.select(action.id);
      return;
    }
    if (core.mode === "ready" && action.type === "read-notices") {
      void (async () => {
        for (const n of agents.view?.notices ?? []) {
          if (
            !n.read &&
            !(await agents.change({ action: "notice-read", id: n.id }))
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
  }
  const [route, setRoute] = useState(getRoute);
  const [setupOpen, setSetupOpen] = useState(false);
  const [noticesOpen, setNoticesOpen] = useState(false);
  const [actionOpen, setActionOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [contextOpen, setContextOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
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
      setRoute(getRoute());
      setQuery("");
      setSidebarOpen(false);
    };
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
  function navigate(path: string) {
    window.location.hash = path;
    if (path === route) {
      setSidebarOpen(false);
      setQuery("");
    }
  }
  function notify(
    title: string,
    detail = "Recorded in this prototype only. No external action was performed.",
  ) {
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
  }
  function sendChat(event: FormEvent) {
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
  }
  const searchItems = [
    ...navigation.map((n) => ({ title: n.title, path: n.path })),
    ...terminalData.map((t) => ({ title: t.name, path: `terminals/${t.id}` })),
    { title: "Memory", path: "agents/memory" },
    { title: "Permissions", path: "wallets/permissions" },
    { title: "AI connections", path: "settings" },
  ].filter((item) => item.title.toLowerCase().includes(query.toLowerCase()));

  return {
    core,
    agents,
    state,
    dispatch,
    route,
    setupOpen,
    setSetupOpen,
    noticesOpen,
    setNoticesOpen,
    actionOpen,
    setActionOpen,
    profileOpen,
    setProfileOpen,
    contextOpen,
    setContextOpen,
    sidebarOpen,
    setSidebarOpen,
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
  };
}
const WorkspaceContext = createContext<ReturnType<
  typeof useWorkspaceState
> | null>(null);
export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const value = useWorkspaceState();
  return (
    <WorkspaceContext.Provider value={value}>
      {children}
    </WorkspaceContext.Provider>
  );
}
export function useWorkspace() {
  const value = useContext(WorkspaceContext);
  if (!value) throw new Error("Workspace provider is missing");
  return value;
}
