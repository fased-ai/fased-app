import {
  createContext,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useState,
  type ReactNode,
} from "react";
import { Monitor, Moon, Sun } from "./icons";
import {
  ensureLanguageLoaded,
  isLanguageLoaded,
  languages,
  resolveLanguage,
  translate,
  type Language,
} from "./ui-language";

export type Theme = "system" | "light" | "dark";
export type UIPreferences = {
  theme: Theme;
  language: Language | "system";
  rail: boolean;
};
export const UI_PREFERENCES_KEY = "fased.ui-preferences.v1";
export const defaultPreferences: UIPreferences = {
  theme: "system",
  language: "system",
  rail: true,
};
export function parsePreferences(raw: string | null): UIPreferences {
  try {
    const v = JSON.parse(raw || "null");
    return {
      theme: ["system", "light", "dark"].includes(v?.theme)
        ? v.theme
        : "system",
      language:
        v?.language === "system" || Object.hasOwn(languages, v?.language)
          ? v.language
          : "system",
      rail: typeof v?.rail === "boolean" ? v.rail : true,
    };
  } catch {
    return { ...defaultPreferences };
  }
}
function load() {
  try {
    return parsePreferences(localStorage.getItem(UI_PREFERENCES_KEY));
  } catch {
    return { ...defaultPreferences };
  }
}
function usePreferenceState() {
  const [preferences, setPreferences] = useState(load);
  const [storageError, setStorageError] = useState(false);
  const [catalogTick, setCatalogTick] = useState(0);
  const language = resolveLanguage(preferences.language, navigator.languages);
  useLayoutEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      document.documentElement.dataset.theme =
        preferences.theme === "system"
          ? media.matches
            ? "dark"
            : "light"
          : preferences.theme;
    };
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [preferences.theme]);
  useEffect(() => {
    document.documentElement.lang = language === "zh" ? "zh-Hans" : language;
  }, [language]);
  useEffect(() => {
    if (isLanguageLoaded(language)) return;
    let active = true;
    void ensureLanguageLoaded(language).then(() => {
      if (active) setCatalogTick((n) => n + 1);
    });
    return () => {
      active = false;
    };
  }, [language]);
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === UI_PREFERENCES_KEY || e.key === null)
        setPreferences(load());
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);
  function update(patch: Partial<UIPreferences>) {
    const next = { ...preferences, ...patch };
    try {
      localStorage.setItem(UI_PREFERENCES_KEY, JSON.stringify(next));
      setStorageError(false);
    } catch {
      setStorageError(true);
    }
    if (patch.language !== undefined) {
      const nextLanguage = resolveLanguage(next.language, navigator.languages);
      if (!isLanguageLoaded(nextLanguage)) {
        void ensureLanguageLoaded(nextLanguage).then(() => {
          setCatalogTick((n) => n + 1);
        });
      }
    }
    setPreferences(next);
  }
  return {
    preferences,
    update,
    language,
    catalogTick,
    storageError,
    t: (key: string, values?: Record<string, string | number>) =>
      translate(language, key, values),
  };
}
const UIContext = createContext<ReturnType<typeof usePreferenceState> | null>(
  null,
);
export function UIProvider({ children }: { children: ReactNode }) {
  const value = usePreferenceState();
  return <UIContext.Provider value={value}>{children}</UIContext.Provider>;
}
export function useUI() {
  const value = useContext(UIContext);
  if (!value) throw Error("UIProvider required");
  return value;
}
export function AppearanceSettings() {
  const { preferences, update, t, storageError } = useUI();
  const appearanceLabelId = useId();
  return (
    <div className="appearance-settings">
      <div className="appearance-field">
        <span className="appearance-field-label" id={appearanceLabelId}>
          {t("Appearance")}
        </span>
        <div
          className="segmented-control appearance-segmented"
          role="group"
          aria-labelledby={appearanceLabelId}
        >
          {(
            [
              ["system", "System", Monitor],
              ["light", "Light", Sun],
              ["dark", "Dark", Moon],
            ] as const
          ).map(([v, label, Icon]) => (
            <button
              key={v}
              type="button"
              aria-pressed={preferences.theme === v}
              onClick={() => update({ theme: v })}
            >
              <Icon size={13} aria-hidden="true" />
              {t(label)}
            </button>
          ))}
        </div>
      </div>
      <label>
        {t("Language")}
        <select
          aria-label={t("Language")}
          value={preferences.language}
          onChange={(e) =>
            update({ language: e.target.value as UIPreferences["language"] })
          }
        >
          <option value="system">{t("System")}</option>
          {Object.entries(languages).map(([code, name]) => (
            <option key={code} value={code} lang={code}>
              {name}
            </option>
          ))}
        </select>
      </label>
      {storageError && (
        <p role="status">{t("Preferences cannot be saved in this browser.")}</p>
      )}
    </div>
  );
}
