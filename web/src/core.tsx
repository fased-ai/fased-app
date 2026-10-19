import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { PrototypeState } from "./state";
export type CoreSetup = { revision: number; settings: PrototypeState["setup"] };
type Mode =
  "loading" | "prototype" | "unclaimed" | "locked" | "ready" | "error";
const sessionKey = "fased.core.session.v1";
class CoreError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}
function useCoreState() {
  const [mode, setMode] = useState<Mode>("loading");
  const [setup, setSetup] = useState<CoreSetup | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(0);
  const [instanceId, setInstanceId] = useState("");
  const [version, setVersion] = useState("");
  const record = useRef<CoreSetup | null>(null);
  const csrf = useRef("");
  const token = useRef("");
  const generation = useRef(0);
  const queue = useRef<Promise<void>>(Promise.resolve());
  const request = useCallback(
    async <T,>(route: string, method = "GET", body?: unknown): Promise<T> => {
      const headers: Record<string, string> = {};
      if (token.current) headers.Authorization = `Bearer ${token.current}`;
      if (method !== "GET") {
        headers["Content-Type"] = "application/json";
        if (csrf.current) headers["X-CSRF-Token"] = csrf.current;
      }
      const response = await fetch(`/api/v1/${route}`, {
        method,
        headers,
        credentials: "same-origin",
        cache: "no-store",
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!response.headers.get("content-type")?.includes("application/json"))
        throw new CoreError("Core API is unavailable", 0);
      const result = await response.json();
      if (!response.ok)
        throw new CoreError(
          typeof result.error === "string"
            ? result.error
            : "Core request failed",
          response.status,
        );
      return result as T;
    },
    [],
  );
  const refresh = useCallback(async () => {
    const auth = await request<{
      claimed: boolean;
      authenticated: boolean;
      csrfToken?: string;
    }>("auth/state");
    if (!auth.claimed) {
      setMode("unclaimed");
      return;
    }
    if (!auth.authenticated) {
      token.current = "";
      csrf.current = "";
      try {
        sessionStorage.removeItem(sessionKey);
      } catch {
        /* no credential is placed in local storage */
      }
      setMode("locked");
      return;
    }
    csrf.current = auth.csrfToken ?? "";
    const [current, health] = await Promise.all([
      request<CoreSetup>("setup"),
      request<{ instanceId: string; version: string }>("health"),
    ]);
    record.current = current;
    setSetup(current);
    setInstanceId(health.instanceId);
    setVersion(health.version);
    setMode("ready");
  }, [request]);
  useEffect(() => {
    let active = true;
    try {
      token.current = sessionStorage.getItem(sessionKey) ?? "";
    } catch {
      token.current = "";
    }
    refresh().catch((e) => {
      if (!active) return;
      if (import.meta.env.DEV && e instanceof CoreError && e.status === 0)
        setMode("prototype");
      else {
        setMode("error");
        setError(
          "The private core is unavailable. Restart it or retry the connection.",
        );
      }
    });
    return () => {
      active = false;
    };
  }, [refresh]);
  async function authenticate(password: string, code?: string) {
    setError("");
    setPending((n) => n + 1);
    try {
      const session = await request<{
        csrfToken: string;
        sessionToken: string;
      }>(
        code === undefined ? "auth/login" : "auth/claim",
        "POST",
        code === undefined ? { password } : { password, code },
      );
      csrf.current = session.csrfToken;
      // HTTP loopback cookies would be shared across ports. Use origin-scoped session
      // storage for the bearer on loopback; HTTPS uses the Secure HttpOnly cookie.
      token.current =
        location.protocol === "https:" ? "" : session.sessionToken;
      try {
        if (token.current) sessionStorage.setItem(sessionKey, token.current);
        else sessionStorage.removeItem(sessionKey);
      } catch {
        /* current-tab memory still works */
      }
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Authentication failed");
      throw e;
    } finally {
      setPending((n) => n - 1);
    }
  }
  const updateSetup = useCallback(
    (patch: Partial<PrototypeState["setup"]>) => {
      const epoch = generation.current;
      let saved = false;
      setPending((n) => n + 1);
      queue.current = queue.current
        .catch(() => {})
        .then(async () => {
          if (epoch !== generation.current || !record.current) return;
          const current = record.current;
          const updated = await request<CoreSetup>("setup", "PUT", {
            revision: current.revision,
            settings: { ...current.settings, ...patch },
          });
          record.current = updated;
          setSetup(updated);
          setError("");
          saved = true;
        })
        .catch(async (e) => {
          generation.current++;
          setError(e instanceof Error ? e.message : "Setup could not be saved");
          if (e instanceof CoreError && (e.status === 409 || e.status === 401))
            await refresh().catch(() => {
              setMode("error");
              setError(
                "The private core is unavailable. Retry the connection.",
              );
            });
        })
        .finally(() => setPending((n) => n - 1));
      return queue.current.then(() => saved);
    },
    [refresh, request],
  );
  async function logout() {
    setPending((n) => n + 1);
    try {
      await request("auth/logout", "POST", {});
      generation.current++;
      token.current = "";
      csrf.current = "";
      record.current = null;
      try {
        sessionStorage.removeItem(sessionKey);
      } catch {
        /* already cleared in memory */
      }
      setSetup(null);
      setMode("locked");
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Logout failed");
    } finally {
      setPending((n) => n - 1);
    }
  }
  return {
    mode,
    setup,
    error,
    saving: pending > 0,
    instanceId,
    version,
    authenticate,
    updateSetup,
    logout,
    retry: async () => {
      setError("");
      try {
        await refresh();
      } catch {
        setMode("error");
        setError("The private core is unavailable. Retry the connection.");
      }
    },
  };
}
const CoreContext = createContext<ReturnType<typeof useCoreState> | null>(null);
export function CoreProvider({ children }: { children: ReactNode }) {
  const value = useCoreState();
  return <CoreContext.Provider value={value}>{children}</CoreContext.Provider>;
}
export function useCore() {
  const core = useContext(CoreContext);
  if (!core) throw new Error("Core provider missing");
  return core;
}
