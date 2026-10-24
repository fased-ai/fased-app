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
  const [pending, setPending] = useState({ generation: 0, count: 0 });
  const [instanceId, setInstanceId] = useState("");
  const [version, setVersion] = useState("");
  const record = useRef<CoreSetup | null>(null);
  const csrf = useRef("");
  const token = useRef("");
  const generation = useRef(0);
  const authGeneration = useRef(0);
  const queue = useRef<Promise<void>>(Promise.resolve());
  const changePending = useCallback((epoch: number, delta: number) => {
    setPending((current) => {
      if (epoch !== authGeneration.current) return current;
      return {
        generation: epoch,
        count: Math.max(
          0,
          (current.generation === epoch ? current.count : 0) + delta,
        ),
      };
    });
  }, []);
  const lock = useCallback((reason = "") => {
    authGeneration.current++;
    generation.current++;
    token.current = "";
    csrf.current = "";
    record.current = null;
    setSetup(null);
    setInstanceId("");
    setVersion("");
    setError(reason);
    setMode("locked");
    try {
      sessionStorage.removeItem(sessionKey);
    } catch {
      /* in-memory credentials have already been cleared */
    }
  }, []);
  const request = useCallback(
    async <T,>(
      route: string,
      method = "GET",
      body?: unknown,
      signal?: AbortSignal,
    ): Promise<T> => {
      const epoch = authGeneration.current;
      const headers: Record<string, string> = {};
      if (token.current) headers.Authorization = `Bearer ${token.current}`;
      if (method !== "GET") {
        headers["Content-Type"] = "application/json";
        if (csrf.current) headers["X-CSRF-Token"] = csrf.current;
      }
      const response = await fetch(`/api/v1/${route}`, {
        method,
        signal,
        headers,
        credentials: "same-origin",
        cache: "no-store",
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (
        response.status === 401 &&
        route !== "auth/login" &&
        route !== "auth/claim"
      ) {
        const message = "Your session ended. Sign in again.";
        if (epoch === authGeneration.current) lock(message);
        throw new CoreError(message, 401);
      }
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
    [lock],
  );
  const refresh = useCallback(async () => {
    const epoch = authGeneration.current;
    try {
      const auth = await request<{
        claimed: boolean;
        authenticated: boolean;
        csrfToken?: string;
      }>("auth/state");
      if (epoch !== authGeneration.current) return;
      if (!auth.claimed) {
        setMode("unclaimed");
        return;
      }
      if (!auth.authenticated) {
        lock();
        return;
      }
      csrf.current = auth.csrfToken ?? "";
      const [current, health] = await Promise.all([
        request<CoreSetup>("setup"),
        request<{ instanceId: string; version: string }>("health"),
      ]);
      if (epoch !== authGeneration.current) return;
      record.current = current;
      setSetup(current);
      setInstanceId(health.instanceId);
      setVersion(health.version);
      setMode("ready");
    } catch (e) {
      if (epoch === authGeneration.current) throw e;
    }
  }, [request, lock]);
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
          "The Fased instance is unavailable. Restart it or retry the connection.",
        );
      }
    });
    return () => {
      active = false;
    };
  }, [refresh]);
  async function authenticate(password: string, code?: string) {
    const epoch = ++authGeneration.current;
    setError("");
    changePending(epoch, 1);
    try {
      const session = await request<{
        csrfToken: string;
        sessionToken: string;
        sessionMode?: "bearer" | "cookie";
      }>(
        code === undefined ? "auth/login" : "auth/claim",
        "POST",
        code === undefined ? { password } : { password, code },
      );
      if (epoch !== authGeneration.current) return;
      csrf.current = session.csrfToken;
      // HTTP loopback cookies would be shared across ports. Use origin-scoped session
      // storage for loopback/native bearer sessions; private HTTPS uses its cookie.
      const cookieMode = session.sessionMode
        ? session.sessionMode === "cookie"
        : location.protocol === "https:";
      token.current = cookieMode ? "" : session.sessionToken;
      try {
        if (token.current) sessionStorage.setItem(sessionKey, token.current);
        else sessionStorage.removeItem(sessionKey);
      } catch {
        /* current-tab memory still works */
      }
      await refresh();
    } catch (e) {
      if (epoch === authGeneration.current) {
        setError(e instanceof Error ? e.message : "Authentication failed");
        throw e;
      }
    } finally {
      changePending(epoch, -1);
    }
  }
  const updateSetup = useCallback(
    (patch: Partial<PrototypeState["setup"]>) => {
      const epoch = generation.current;
      const authEpoch = authGeneration.current;
      let saved = false;
      changePending(authEpoch, 1);
      queue.current = queue.current
        .catch(() => {})
        .then(async () => {
          if (epoch !== generation.current || !record.current) return;
          const current = record.current;
          const updated = await request<CoreSetup>("setup", "PUT", {
            revision: current.revision,
            settings: { ...current.settings, ...patch },
          });
          if (epoch !== generation.current) return;
          record.current = updated;
          setSetup(updated);
          setError("");
          saved = true;
        })
        .catch(async (e) => {
          if (epoch !== generation.current) return;
          generation.current++;
          setError(e instanceof Error ? e.message : "Setup could not be saved");
          if (e instanceof CoreError && e.status === 409)
            await refresh().catch(() => {
              setMode("error");
              setError(
                "The Fased instance is unavailable. Retry the connection.",
              );
            });
        })
        .finally(() => changePending(authEpoch, -1));
      return queue.current.then(() => saved);
    },
    [refresh, request, changePending],
  );
  async function logout() {
    const epoch = authGeneration.current;
    changePending(epoch, 1);
    try {
      await request("auth/logout", "POST", {});
      if (epoch === authGeneration.current) lock();
    } catch (e) {
      if (epoch === authGeneration.current)
        setError(e instanceof Error ? e.message : "Logout failed");
    } finally {
      changePending(epoch, -1);
    }
  }
  return {
    mode,
    request,
    setup,
    error,
    saving: pending.generation === authGeneration.current && pending.count > 0,
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
        setError("The Fased instance is unavailable. Retry the connection.");
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
