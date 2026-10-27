export type WalletMode = "watch" | "external" | "automation" | "skip";
export type Host = "local" | "vps";
export type Provider = "none" | "openai" | "anthropic" | "xai";
export type Profile = {
  id: string;
  name: string;
  role: string;
  initials: string;
  color: string;
  provider: Provider;
};
export type Memory = {
  id: string;
  profileId: string;
  text: string;
  source: string;
  verified: string;
};
export type Notice = {
  id: string;
  title: string;
  detail: string;
  read: boolean;
  tone: "info" | "success";
};
export type PrototypeState = {
  version: 1;
  setup: {
    step: number;
    complete: boolean;
    host: Host;
    walletMode: WalletMode;
    provider: Provider;
  };
  profiles: Profile[];
  selectedProfile: string;
  memories: Memory[];
  paused: boolean;
  notices: Notice[];
};
export const STORAGE_KEY = "fased.prototype.v1";
export const providerNames: Record<Provider, string> = {
  none: "No AI connection",
  openai: "OpenAI",
  anthropic: "Claude",
  xai: "Grok / xAI",
};
export const walletNames: Record<WalletMode, string> = {
  watch: "Watch-only",
  external: "External wallet",
  automation: "Automation wallet",
  skip: "Set up later",
};
export function initialState(): PrototypeState {
  return {
    version: 1,
    setup: {
      step: 0,
      complete: false,
      host: "local",
      walletMode: "skip",
      provider: "none",
    },
    profiles: [
      {
        id: "wen",
        name: "WEN Operator",
        role: "Mining research & allocation",
        initials: "WO",
        color: "lime",
        provider: "none",
      },
      {
        id: "analyst",
        name: "Trading Analyst",
        role: "Strategies & market structure",
        initials: "TA",
        color: "blue",
        provider: "none",
      },
      {
        id: "research",
        name: "News Researcher",
        role: "Sources, context & sentiment",
        initials: "NR",
        color: "peach",
        provider: "none",
      },
    ],
    selectedProfile: "wen",
    memories: [
      {
        id: "m1",
        profileId: "wen",
        text: "Keep mining allocation reviews separate from claim and staking permissions.",
        source: "Example owner preference",
        verified: "Illustrative · not verified",
      },
      {
        id: "m2",
        profileId: "analyst",
        text: "Compare strategy versions after fees and include drawdown in the review.",
        source: "Example research workflow",
        verified: "Illustrative · not verified",
      },
      {
        id: "m3",
        profileId: "research",
        text: "Keep publication time, source URL and retrieval time with each news signal.",
        source: "Example research workflow",
        verified: "Illustrative · not verified",
      },
    ],
    paused: true,
    notices: [
      {
        id: "welcome",
        title: "Your workspace, before the wiring",
        detail:
          "This is an interactive design prototype. Accounts, conversations and terminal data are illustrative.",
        read: false,
        tone: "info",
      },
    ],
  };
}
const hostValues = ["local", "vps"];
const modeValues = ["watch", "external", "automation", "skip"];
const providerValues = ["none", "openai", "anthropic", "xai"];
export function restoreState(raw: string | null): PrototypeState {
  const fallback = initialState();
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw) as PrototypeState;
    if (
      parsed.version !== 1 ||
      !parsed.setup ||
      !hostValues.includes(parsed.setup.host) ||
      !modeValues.includes(parsed.setup.walletMode) ||
      !providerValues.includes(parsed.setup.provider) ||
      !Number.isInteger(parsed.setup.step) ||
      parsed.setup.step < 0 ||
      parsed.setup.step > 3 ||
      typeof parsed.setup.complete !== "boolean" ||
      typeof parsed.paused !== "boolean" ||
      !Array.isArray(parsed.profiles) ||
      parsed.profiles.length === 0 ||
      parsed.profiles.length > 30 ||
      !parsed.profiles.every(
        (p) =>
          typeof p.id === "string" &&
          typeof p.name === "string" &&
          p.name.length <= 48 &&
          typeof p.role === "string" &&
          typeof p.initials === "string" &&
          ["lime", "blue", "peach"].includes(p.color) &&
          providerValues.includes(p.provider),
      ) ||
      !parsed.profiles.some((p) => p.id === parsed.selectedProfile) ||
      !Array.isArray(parsed.memories) ||
      !parsed.memories.every(
        (m) =>
          typeof m.id === "string" &&
          typeof m.profileId === "string" &&
          typeof m.text === "string" &&
          m.text.length <= 500 &&
          typeof m.source === "string" &&
          typeof m.verified === "string",
      ) ||
      !Array.isArray(parsed.notices) ||
      !parsed.notices.every(
        (n) =>
          typeof n.id === "string" &&
          typeof n.title === "string" &&
          typeof n.detail === "string" &&
          typeof n.read === "boolean" &&
          ["info", "success"].includes(n.tone),
      )
    )
      return fallback;
    return {
      ...parsed,
      notices: parsed.notices.slice(0, 30),
      memories: parsed.memories.slice(0, 100),
    };
  } catch {
    return fallback;
  }
}
export type Action =
  | { type: "setup"; patch: Partial<PrototypeState["setup"]> }
  | { type: "profile"; id: string }
  | { type: "add-profile"; name: string; role: string }
  | { type: "provider"; provider: Provider }
  | { type: "pause"; paused: boolean }
  | { type: "memory-add"; text: string; id: string }
  | { type: "memory-delete"; id: string }
  | { type: "notice"; notice: Notice }
  | { type: "read-notices" }
  | { type: "reset" };
export function reducer(state: PrototypeState, action: Action): PrototypeState {
  switch (action.type) {
    case "setup":
      return { ...state, setup: { ...state.setup, ...action.patch } };
    case "profile":
      return state.profiles.some((p) => p.id === action.id)
        ? { ...state, selectedProfile: action.id }
        : state;
    case "add-profile": {
      const name = action.name.trim().slice(0, 48);
      if (!name || state.profiles.length >= 30) return state;
      const id = `profile-${state.profiles.length}-${name.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`;
      return {
        ...state,
        selectedProfile: id,
        profiles: [
          ...state.profiles,
          {
            id,
            name,
            role: action.role.trim().slice(0, 100) || "Custom research profile",
            initials: name
              .split(/\s+/)
              .slice(0, 2)
              .map((w) => w[0])
              .join("")
              .toUpperCase(),
            color: "blue",
            provider: "none",
          },
        ],
      };
    }
    case "provider":
      return {
        ...state,
        profiles: state.profiles.map((p) =>
          p.id === state.selectedProfile
            ? { ...p, provider: action.provider }
            : p,
        ),
      };
    case "pause":
      return { ...state, paused: action.paused };
    case "memory-add": {
      const text = action.text.trim().slice(0, 500);
      if (!text || state.memories.length >= 100) return state;
      return {
        ...state,
        memories: [
          ...state.memories,
          {
            id: action.id,
            profileId: state.selectedProfile,
            text,
            source: "Saved in prototype",
            verified: "Owner draft · not verified",
          },
        ],
      };
    }
    case "memory-delete":
      return {
        ...state,
        memories: state.memories.filter(
          (m) => m.id !== action.id || m.profileId !== state.selectedProfile,
        ),
      };
    case "notice":
      return {
        ...state,
        notices: [action.notice, ...state.notices].slice(0, 30),
      };
    case "read-notices":
      return {
        ...state,
        notices: state.notices.map((n) => ({ ...n, read: true })),
      };
    case "reset":
      return initialState();
  }
}
