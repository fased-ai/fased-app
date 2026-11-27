import { validOwnerHandoff } from "./owner-handoff";
import { parseSSEChunkBuffer, type SSEFrame } from "./agents-v2";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { PrototypeState } from "./state";
export type CoreSetup = { revision: number; settings: PrototypeState["setup"] };
export type InstanceConnection = {
  instanceId: string;
  location: "local" | "remote";
  transport: "loopback" | "private-https";
  remoteTransport?: string;
  ownerUid?: number;
  ownerKind?: "custody";
};
type Health = {
  status: string;
  apiVersion: string;
  instanceId: string;
  version: string;
  capabilities?: {
    aiConnections?: boolean;
    walletSigner?: boolean;
    financialExecution?: boolean;
    updater?: boolean;
  };
  connection: {
    location: "local" | "remote";
    transport: "loopback" | "private-https";
    remoteTransport?: string;
    ownerUid?: number;
    ownerKind?: "custody";
  };
};
type Mode =
  "loading" | "prototype" | "unclaimed" | "locked" | "ready" | "error";
const sessionKey = "fased.core.session.v1";
export class CoreError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
    readonly bindingDigest?: string,
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
  const [capabilities, setCapabilities] = useState<
    Health["capabilities"] | null
  >(null);
  const [connection, setConnection] = useState<InstanceConnection | null>(null);
  const [remoteAccess, setRemoteAccess] = useState(false);
  const [remoteEnrolled, setRemoteEnrolled] = useState(false);
  const [remoteSignedOut, setRemoteSignedOut] = useState(false);
  const [pairingAvailable, setPairingAvailable] = useState(false);
  const [remoteTransport, setRemoteTransport] = useState("");
  const [localAccess, setLocalAccess] = useState(false);
  const [localAutoEnter, setLocalAutoEnter] = useState(false);
  const [localSignedOut, setLocalSignedOut] = useState(() => {
    try {
      return sessionStorage.getItem("fased.local.signed-out") === "1";
    } catch {
      return false;
    }
  });
  const [passwordAvailable, setPasswordAvailable] = useState(false);
  const connectedID = useRef(
    new URL(location.href).searchParams.get("instance") ?? "",
  );
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
    setCapabilities(null);
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
      extraHeaders?: Record<string, string>,
    ): Promise<T> => {
      const epoch = authGeneration.current;
      const headers: Record<string, string> = { ...extraHeaders };
      if (token.current && route !== "health")
        headers.Authorization = `Bearer ${token.current}`;
      if (method !== "GET") {
        headers["Content-Type"] = "application/json";
        if (csrf.current) headers["X-CSRF-Token"] = csrf.current;
      }
      const response = await fetch(`/api/v1/${route}`, {
        method,
        signal,
        headers,
        credentials: route === "health" ? "omit" : "same-origin",
        cache: "no-store",
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (
        response.status === 401 &&
        route !== "auth/login" &&
        route !== "auth/claim" &&
        route !== "auth/local" &&
        route !== "auth/remote" &&
        route !== "auth/pair" &&
        route !== "installation/local-owner"
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
          typeof result.code === "string" ? result.code : undefined,
          typeof result.bindingDigest === "string"
            ? result.bindingDigest
            : undefined,
        );
      return result as T;
    },
    [lock],
  );
  const consumeSSEStream = useCallback(
    async (
      route: string,
      method: "GET" | "POST",
      options: {
        body?: unknown;
        signal?: AbortSignal;
        onEvent: (frame: SSEFrame) => void;
      },
    ): Promise<void> => {
      const epoch = authGeneration.current;
      const headers: Record<string, string> = {
        Accept: "text/event-stream",
      };
      if (token.current) headers.Authorization = `Bearer ${token.current}`;
      if (method !== "GET") {
        headers["Content-Type"] = "application/json";
        if (csrf.current) headers["X-CSRF-Token"] = csrf.current;
      }
      const response = await fetch(`/api/v1/${route}`, {
        method,
        signal: options.signal,
        headers,
        credentials: "same-origin",
        cache: "no-store",
        body:
          options.body === undefined ? undefined : JSON.stringify(options.body),
      });
      if (response.status === 401) {
        const message = "Your session ended. Sign in again.";
        if (epoch === authGeneration.current) lock(message);
        throw new CoreError(message, 401);
      }
      if (!response.ok) {
        let message = "Stream request failed";
        try {
          if (
            response.headers.get("content-type")?.includes("application/json")
          ) {
            const parsed = await response.json();
            if (typeof parsed?.error === "string") message = parsed.error;
          }
        } catch {
          /* ignore parse errors on error bodies */
        }
        throw new CoreError(message, response.status);
      }
      if (!response.body) {
        throw new CoreError("Streaming body unavailable", response.status);
      }
      const reader = response.body.getReader();
      const decoder = new TextDecoder("utf-8");
      let pendingBuffer = "";
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          pendingBuffer += decoder.decode(value, { stream: true });
          const { frames, rest } = parseSSEChunkBuffer(pendingBuffer);
          pendingBuffer = rest;
          for (const frame of frames) options.onEvent(frame);
        }
        pendingBuffer += decoder.decode();
        if (pendingBuffer.trim()) {
          const { frames } = parseSSEChunkBuffer(`${pendingBuffer}\n\n`);
          for (const frame of frames) options.onEvent(frame);
        }
      } finally {
        reader.releaseLock();
      }
    },
    [lock],
  );
  const stream = useCallback(
    async (
      route: string,
      body: unknown,
      options: {
        signal?: AbortSignal;
        onEvent: (frame: SSEFrame) => void;
      },
    ): Promise<void> =>
      consumeSSEStream(route, "POST", {
        body,
        signal: options.signal,
        onEvent: options.onEvent,
      }),
    [consumeSSEStream],
  );
  const streamGet = useCallback(
    async (
      route: string,
      options: {
        signal?: AbortSignal;
        onEvent: (frame: SSEFrame) => void;
      },
    ): Promise<void> => consumeSSEStream(route, "GET", options),
    [consumeSSEStream],
  );
  const inspect = useCallback(async () => {
    const health = await request<Health>("health");
    if (
      health.status !== "ok" ||
      health.apiVersion !== "1" ||
      !/^[A-Za-z0-9_-]{16,128}$/.test(health.instanceId) ||
      !health.connection ||
      !["local", "remote"].includes(health.connection.location) ||
      !["loopback", "private-https"].includes(health.connection.transport) ||
      !validOwnerHandoff(health.connection)
    )
      throw new Error("This address is not a compatible Fased instance.");
    if (connectedID.current && connectedID.current !== health.instanceId)
      throw new Error(
        "Instance identity changed. Reopen Fased to choose this instance.",
      );
    connectedID.current = health.instanceId;
    return health;
  }, [request]);
  const refresh = useCallback(async () => {
    const epoch = authGeneration.current;
    try {
      const health = await inspect();
      const verifiedConnection: InstanceConnection = {
        instanceId: health.instanceId,
        location: health.connection.location,
        transport: health.connection.transport,
        ...(health.connection.remoteTransport === undefined
          ? {}
          : { remoteTransport: health.connection.remoteTransport }),
        ...(health.connection.ownerUid === undefined
          ? {}
          : { ownerUid: health.connection.ownerUid }),
        ...(health.connection.ownerKind === undefined
          ? {}
          : { ownerKind: health.connection.ownerKind }),
      };
      const auth = await request<{
        claimed: boolean;
        authenticated: boolean;
        csrfToken?: string;
        localAccess?: boolean;
        localAutoEnter?: boolean;
        remoteAccess?: boolean;
        remoteEnrolled?: boolean;
        remoteSignedOut?: boolean;
        pairingAvailable?: boolean;
        remoteTransport?: string;
        legacyAuthenticated?: boolean;
        passwordAvailable?: boolean;
      }>("auth/state");
      if (epoch !== authGeneration.current) return;
      setLocalAccess(auth.localAccess === true);
      setLocalAutoEnter(auth.localAutoEnter === true);
      setRemoteAccess(auth.remoteAccess === true);
      setRemoteEnrolled(auth.remoteEnrolled === true);
      setRemoteSignedOut(auth.remoteSignedOut === true);
      setPairingAvailable(auth.pairingAvailable === true);
      setRemoteTransport(
        auth.remoteTransport ?? health.connection.remoteTransport ?? "",
      );
      if (auth.legacyAuthenticated) csrf.current = auth.csrfToken ?? "";
      setPasswordAvailable(auth.passwordAvailable === true);
      if (
        auth.remoteAccess &&
        !auth.remoteEnrolled &&
        auth.legacyAuthenticated
      ) {
        const session = await request<{ csrfToken: string }>(
          "auth/remote",
          "POST",
          {},
        );
        if (epoch !== authGeneration.current) return;
        csrf.current = session.csrfToken;
        auth.authenticated = true;
        auth.claimed = true;
        setRemoteEnrolled(true);
      }
      if (!auth.claimed) {
        setConnection(verifiedConnection);
        setMode("unclaimed");
        return;
      }
      if (!auth.authenticated) {
        lock();
        setConnection(verifiedConnection);
        return;
      }
      if (!auth.legacyAuthenticated || auth.remoteEnrolled)
        csrf.current = auth.csrfToken ?? "";
      const current = await request<CoreSetup>("setup");
      if (epoch !== authGeneration.current) return;
      record.current = current;
      setConnection(verifiedConnection);
      setSetup(current);
      setInstanceId(health.instanceId);
      setVersion(health.version);
      setCapabilities(health.capabilities ?? null);
      setMode("ready");
    } catch (e) {
      if (epoch === authGeneration.current) throw e;
    }
  }, [request, lock, inspect]);
  useEffect(() => {
    let active = true;
    try {
      token.current = sessionStorage.getItem(sessionKey) ?? "";
    } catch {
      token.current = "";
    }
    refresh().catch((e) => {
      if (!active) return;
      let fixturePreview = false;
      if (import.meta.env.DEV) {
        try {
          fixturePreview =
            sessionStorage.getItem("fased.test.prototype") === "1";
        } catch {
          // An unavailable browser store is not permission to show sample data.
        }
      }
      if (fixturePreview && e instanceof CoreError && e.status === 0)
        setMode("prototype");
      else {
        setLocalAutoEnter(false);
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
  const authenticate = useCallback(
    async (
      password: string,
      code?: string,
      local = false,
      remote = false,
      confirm = false,
    ) => {
      const epoch = ++authGeneration.current;
      setError("");
      changePending(epoch, 1);
      try {
        const nativeLocal =
          local &&
          code === undefined &&
          ["wails", "wails.localhost"].includes(location.hostname);
        // The protected native handler independently checks the current instance
        // on this empty owner request. Reuse only a previously verified Local
        // connection here; every other sign-in still performs its own inspection.
        if (
          !nativeLocal ||
          !connection ||
          connection.instanceId !== connectedID.current ||
          connection.location !== "local" ||
          connection.transport !== "loopback"
        )
          await inspect();
        const browserLocal = local && code === undefined && !nativeLocal;
        const session = await request<{
          csrfToken: string;
          sessionToken?: string;
          sessionMode?: "bearer" | "cookie";
        }>(
          remote
            ? "auth/remote"
            : nativeLocal
              ? "installation/local-owner"
              : local
                ? "auth/local"
                : code === undefined
                  ? "auth/login"
                  : "auth/claim",
          "POST",
          remote
            ? confirm
              ? { code, confirm: true }
              : { code }
            : nativeLocal
              ? {}
              : browserLocal
                ? { action: "browser" }
                : local
                  ? { action: "exchange", code }
                  : code === undefined
                    ? { password }
                    : { password, code },
          undefined,
          browserLocal ? { "X-Fased-Local": "1" } : undefined,
        );
        if (epoch !== authGeneration.current) return;
        if (remote) {
          try {
            sessionStorage.removeItem("fased.remote.signed-out");
          } catch {}
        }
        if (local) {
          try {
            sessionStorage.removeItem("fased.local.signed-out");
          } catch {}
        }
        csrf.current = session.csrfToken;
        // HTTP loopback cookies would be shared across ports. Use origin-scoped session
        // storage for loopback/native bearer sessions; private HTTPS uses its cookie.
        const cookieMode = session.sessionMode
          ? session.sessionMode === "cookie"
          : location.protocol === "https:";
        token.current = cookieMode ? "" : (session.sessionToken ?? "");
        try {
          if (token.current) sessionStorage.setItem(sessionKey, token.current);
          else sessionStorage.removeItem(sessionKey);
        } catch {
          /* current-tab memory still works */
        }
        await refresh();
        if (epoch === authGeneration.current && remote)
          setRemoteSignedOut(false);
        if (epoch === authGeneration.current && local) setLocalSignedOut(false);
      } catch (e) {
        if (epoch === authGeneration.current) {
          setError(e instanceof Error ? e.message : "Authentication failed");
          throw e;
        }
      } finally {
        changePending(epoch, -1);
      }
    },
    [request, inspect, refresh, connection, changePending],
  );
  const pair = useCallback(
    async (pairCode: string, label?: string) => {
      const epoch = ++authGeneration.current;
      setError("");
      changePending(epoch, 1);
      try {
        await inspect();
        const session = await request<{
          csrfToken: string;
          sessionToken?: string;
          sessionMode?: "bearer" | "cookie";
        }>(
          "auth/pair",
          "POST",
          label ? { code: pairCode.trim(), label } : { code: pairCode.trim() },
          undefined,
          { "X-Fased-Pair": "1" },
        );
        if (epoch !== authGeneration.current) return;
        try {
          sessionStorage.removeItem("fased.remote.signed-out");
        } catch {}
        csrf.current = session.csrfToken;
        const cookieMode = session.sessionMode
          ? session.sessionMode === "cookie"
          : location.protocol === "https:";
        token.current = cookieMode ? "" : (session.sessionToken ?? "");
        try {
          if (token.current) sessionStorage.setItem(sessionKey, token.current);
          else sessionStorage.removeItem(sessionKey);
        } catch {
          /* current-tab memory still works */
        }
        await refresh();
        if (epoch === authGeneration.current) setRemoteSignedOut(false);
      } catch (e) {
        if (epoch === authGeneration.current) {
          setError(e instanceof Error ? e.message : "Pairing failed");
          throw e;
        }
      } finally {
        changePending(epoch, -1);
      }
    },
    [request, inspect, refresh, changePending],
  );
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
  const logout = useCallback(async () => {
    const epoch = authGeneration.current;
    changePending(epoch, 1);
    try {
      await request("auth/logout", "POST", {});
      if (epoch === authGeneration.current) {
        if (remoteAccess || pairingAvailable) {
          setRemoteSignedOut(true);
          try {
            sessionStorage.setItem("fased.remote.signed-out", "1");
          } catch {}
        }
        if (localAccess) {
          setLocalSignedOut(true);
          try {
            sessionStorage.setItem("fased.local.signed-out", "1");
          } catch {}
        }
        lock();
      }
    } catch (e) {
      if (epoch === authGeneration.current)
        setError(e instanceof Error ? e.message : "Logout failed");
    } finally {
      changePending(epoch, -1);
    }
  }, [
    request,
    lock,
    remoteAccess,
    pairingAvailable,
    localAccess,
    changePending,
  ]);
  const retry = useCallback(async () => {
    setError("");
    try {
      await refresh();
    } catch {
      setLocalAutoEnter(false);
      setMode("error");
      setError("The Fased instance is unavailable. Retry the connection.");
    }
  }, [refresh]);
  const saving =
    pending.generation === authGeneration.current && pending.count > 0;
  return useMemo(
    () => ({
      remoteAccess,
      remoteEnrolled,
      remoteSignedOut,
      pairingAvailable,
      remoteTransport,
      localAccess,
      localAutoEnter,
      localSignedOut,
      passwordAvailable,
      mode,
      request,
      stream,
      streamGet,
      setup,
      error,
      saving,
      instanceId,
      version,
      capabilities,
      connection,
      authenticate,
      pair,
      updateSetup,
      logout,
      retry,
    }),
    [
      remoteAccess,
      remoteEnrolled,
      remoteSignedOut,
      pairingAvailable,
      remoteTransport,
      localAccess,
      localAutoEnter,
      localSignedOut,
      passwordAvailable,
      mode,
      request,
      stream,
      streamGet,
      setup,
      error,
      saving,
      instanceId,
      version,
      capabilities,
      connection,
      authenticate,
      pair,
      updateSetup,
      logout,
      retry,
    ],
  );
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
