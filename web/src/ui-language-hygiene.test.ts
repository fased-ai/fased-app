import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { catalog } from "./ui-language-catalog";
import { previewEnglish } from "./ui-language-preview";

const sourceFiles = [
  "ui-language-pages.ts",
  "ui-language-onchain.ts",
  "ui-language-priority.ts",
  "ui-language-catalog.ts",
] as const;

function extractPlaceholders(text: string): string {
  return [...text.matchAll(/\{(\w+)\}/g)]
    .map((m) => m[1])
    .sort()
    .join(",");
}

function parseTopLevelKeys(file: string): { key: string; line: number }[] {
  const lines = readFileSync(new URL(file, import.meta.url), "utf8").split(
    "\n",
  );
  const found: { key: string; line: number }[] = [];
  for (let i = 0; i < lines.length; i++) {
    const m =
      /^  (?:"((?:[^"\\]|\\.)+)"|([A-Za-z_][A-Za-z0-9_]*)):(?:\s|$)/.exec(
        lines[i],
      );
    if (!m) continue;
    const key = m[1] !== undefined ? (JSON.parse(`"${m[1]}"`) as string) : m[2];
    found.push({ key, line: i + 1 });
  }
  return found;
}

describe("UI translation catalog hygiene", () => {
  it("contains zero duplicate keys across translation modules", () => {
    const seen = new Map<string, string>();
    const duplicates: string[] = [];
    for (const file of sourceFiles) {
      for (const { key, line } of parseTopLevelKeys(file)) {
        const prev = seen.get(key);
        if (prev) {
          duplicates.push(`${JSON.stringify(key)}: ${prev} & ${file}:${line}`);
        } else {
          seen.set(key, `${file}:${line}`);
        }
      }
    }
    expect(duplicates).toEqual([]);
  });

  it("provides four non-empty translations with matching placeholders for every key", () => {
    const badTuples: string[] = [];
    const placeholderMismatches: string[] = [];
    for (const [key, row] of Object.entries(catalog)) {
      if (!Array.isArray(row) || row.length !== 4) {
        badTuples.push(`${key}: expected length 4`);
        continue;
      }
      const expected = extractPlaceholders(key);
      for (let i = 0; i < 4; i++) {
        if (typeof row[i] !== "string" || row[i].trim().length === 0) {
          badTuples.push(`${key}[${i}]: empty translation`);
        }
        const actual = extractPlaceholders(row[i]);
        if (actual !== expected) {
          placeholderMismatches.push(
            `${key}[${i}]: expected {${expected}} got {${actual}}`,
          );
        }
      }
    }
    expect(badTuples).toEqual([]);
    expect(placeholderMismatches).toEqual([]);
  });

  it("keeps previewEnglish aligned with catalog entries", () => {
    const missing = Object.keys(previewEnglish).filter(
      (key) => !Object.hasOwn(catalog, key),
    );
    expect(missing).toEqual([]);
  });

  it("has zero missing translation keys across active Core UI components", () => {
    const activeCoreFiles = [
      "App.tsx",
      "components/AddProviderWizard.tsx",
      "components/AgentChatV2.tsx",
      "components/AgentRulesMemoryPanel.tsx",
      "components/AgentUsagePanel.tsx",
      "components/ApplicationUpdatesPanel.tsx",
      "components/CoreGate.tsx",
      "components/ErrorBoundary.tsx",
      "components/InstanceConnection.tsx",
      "components/RemoteDevices.tsx",
      "components/SettingsModal.tsx",
      "components/ShellNavigation.tsx",
      "components/UpdateStatusPanel.tsx",
      "pages/AgentMemorySection.tsx",
      "pages/AgentTasks.tsx",
      "pages/AgentTaskDrafts.tsx",
      "pages/NewsInputs.tsx",
      "pages/Overview.tsx",
      "pages/Settings.tsx",
    ];
    const missing: string[] = [];
    for (const rel of activeCoreFiles) {
      const txt = readFileSync(new URL(rel, import.meta.url), "utf8");
      const re = /\b(?:t|uiText)\(\s*"((?:[^"\\]|\\.)+)"/g;
      let m: RegExpExecArray | null;
      while ((m = re.exec(txt)) !== null) {
        const key = JSON.parse(`"${m[1]}"`) as string;
        if (!Object.hasOwn(catalog, key)) {
          missing.push(`${rel}: ${JSON.stringify(key)}`);
        }
      }
    }
    expect(missing).toEqual([]);
  });
});
