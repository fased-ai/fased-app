import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import type { OpenAIModelPolicy } from "./agents";
import { useCore } from "./core";
import type { Provider } from "./state";
import type { PilotAllowance } from "./pages/OpenAIPilot";
import type { TaskAccessAllowance } from "./pages/TaskAccess";

export type CredentialMode =
  Exclude<Provider, "none"> | "test" | "openai-signin" | "google-oauth" | "cli";

export type HostCLIStatus = {
  provider: string;
  binary: string;
  label: string;
  transport?: "cli" | "acp";
  version?: string;
  binaryPath?: string;
  installed: boolean;
  authenticated: boolean;
  account?: string;
  supportsDevice: boolean;
  loginCommand: string;
  installHint: string;
  models?: string[];
};

export type CLIAuthView = {
  id: string;
  provider: string;
  binary: string;
  state: "starting" | "awaiting-code" | "authenticated" | "failed";
  verificationUrl?: string;
  userCode?: string;
  message?: string;
  expiresAt: number;
};

export type Connection = {
  authMethod?: "api-key" | "sign-in" | "local" | "oauth" | "cli";
  endpoint?: string;
  googleProject?: string;
  workspace?: string;
  googleSignInAvailable?: boolean;
  disabled?: boolean;
  binary?: string;
  binaryPath?: string;
  configDir?: string;
  accentColor?: string;
  cliVersion?: string;
  planUse?: boolean;
  planPaused?: boolean;
  planFailure?: string;
  signInAvailable?: boolean;
  signIn?: {
    id: string;
    ref?: string;
    provider: string;
    state: "waiting" | "callback-ready";
    url: string;
    expiresAt: number;
    remote: boolean;
  };
  hostClis?: HostCLIStatus[];
  cliAuth?: CLIAuthView | null;
  pilotEnrollment?: boolean;
  pilotAvailable?: boolean;
  pilot?: PilotAllowance;
  pilotReview?: PilotAllowance;
  pilotState?: string;
  provider?: string;
  name?: string;
  connections?: Connection[];
  catalog?: {
    version: number;
    state?: string;
    policy?: string;
    providers: {
      id: string;
      name: string;
      methods: { id: string; name: string }[];
      route?: string;
      family?: string;
      servers?: { id: string; name: string; endpoint: string }[];
    }[];
    models: {
      id: string;
      name: string;
      tier?: string;
      policy?: OpenAIModelPolicy;
      provider: string;
      method: string;
      connectionRef: string;
      recommended: boolean;
      selectable: boolean;
      availability: string;
    }[];
  };
  credentialMode?: CredentialMode;
  credentialModes?: CredentialMode[];
  taskAccess?: TaskAccessAllowance;
  persistentTaskAccessAvailable?: boolean;
  osVaultAvailable?: boolean;
  osProtected?: boolean;
  accessReview?: TaskAccessAllowance;
  admissionPolicy: OpenAIModelPolicy;
  ref: string;
  model: string;
  locked: boolean;
  unlockExpires: number;
  testOnly: boolean;
  liveCallsEnabled: boolean;
};

export type AIConnectionContextValue = {
  view: Connection | null;
  setView: Dispatch<SetStateAction<Connection | null>>;
  error: string;
  setError: Dispatch<SetStateAction<string>>;
  refresh: () => Promise<Connection | null>;
  core: ReturnType<typeof useCore>;
};

const AIConnectionContext = createContext<AIConnectionContextValue | null>(
  null,
);

function useAIConnectionState(enabled: boolean): AIConnectionContextValue {
  const core = useCore();
  const [view, setView] = useState<Connection | null>(null);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const epoch = ++generation.current;
    try {
      const next = await core.request<Connection>("ai/connection");
      if (epoch === generation.current) {
        setView(next);
        setError("");
      }
      return next;
    } catch {
      if (epoch === generation.current) {
        setView(null);
        setError("Connection unavailable");
      }
      return null;
    }
  }, [core.request]);

  useEffect(() => {
    if (!enabled) return;
    setView(null);
    setError("");
    if (core.mode !== "ready") return;
    const load = () => {
      void refresh();
    };
    load();
    window.addEventListener("ai-connection-changed", load);
    window.addEventListener("focus", load);
    const timer = window.setInterval(load, 15000);
    return () => {
      generation.current++;
      clearInterval(timer);
      window.removeEventListener("ai-connection-changed", load);
      window.removeEventListener("focus", load);
    };
  }, [enabled, refresh, core.mode, core.instanceId]);

  return { view, setView, error, setError, refresh, core };
}

export function AIConnectionProvider({ children }: { children: ReactNode }) {
  const value = useAIConnectionState(true);
  return (
    <AIConnectionContext.Provider value={value}>
      {children}
    </AIConnectionContext.Provider>
  );
}

export function useAIConnection(): AIConnectionContextValue {
  const shared = useContext(AIConnectionContext);
  const fallback = useAIConnectionState(shared === null);
  return shared ?? fallback;
}
