import { previewEnglish } from "./ui-language-preview";

export const languages = {
  en: "English",
  ru: "Русский",
  es: "Español",
  zh: "简体中文",
  ko: "한국어",
} as const;
export type Language = keyof typeof languages;
export type LocaleRow = readonly [string, string, string, string];
export type LocaleCatalog = Record<string, LocaleRow>;

const localeIndex = { ru: 0, es: 1, zh: 2, ko: 3 } as const;
let loadedCatalog: LocaleCatalog | null = null;
let loadingPromise: Promise<void> | null = null;

export function registerCatalog(rows: LocaleCatalog): void {
  loadedCatalog = rows;
}

export function isLanguageLoaded(language: Language): boolean {
  return language === "en" || loadedCatalog !== null;
}

export function ensureLanguageLoaded(language: Language): Promise<void> {
  if (language === "en" || loadedCatalog !== null) return Promise.resolve();
  if (!loadingPromise) {
    loadingPromise = import("./ui-language-catalog")
      .then(({ catalog }) => {
        loadedCatalog = catalog;
      })
      .finally(() => {
        loadingPromise = null;
      });
  }
  return loadingPromise;
}

// Interface copy only. Never translate user records, model output or server receipts.
export function translate(
  language: Language,
  key: string,
  values: Record<string, string | number> = {},
): string {
  const normalized = key.trim();
  let translated: string | undefined;
  if (language === "en") {
    if (Object.hasOwn(previewEnglish, normalized)) {
      translated = previewEnglish[normalized];
    }
  } else if (loadedCatalog && Object.hasOwn(loadedCatalog, normalized)) {
    translated = loadedCatalog[normalized][localeIndex[language]];
  } else if (Object.hasOwn(previewEnglish, normalized)) {
    translated = previewEnglish[normalized];
  }
  const result =
    translated === undefined
      ? key
      : key.slice(0, key.indexOf(normalized)) +
        translated +
        key.slice(key.indexOf(normalized) + normalized.length);
  return result.replace(/\{(\w+)\}/g, (match, name: string) =>
    Object.hasOwn(values, name) ? String(values[name]) : match,
  );
}

export function resolveLanguage(
  value: string,
  browser: readonly string[],
): Language {
  if (Object.hasOwn(languages, value)) return value as Language;
  if (value === "system")
    for (const locale of browser) {
      const code = locale.toLowerCase().split(/[-_]/)[0];
      if (Object.hasOwn(languages, code)) return code as Language;
    }
  return "en";
}
