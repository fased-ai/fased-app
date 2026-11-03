import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { copyKeys, languages, translate, type Language } from "./ui-language";
import { WENPaperJourney } from "./pages/WENPaperJourney";
import { WENSessionAutomatic } from "./pages/WENSessionAutomatic";
import { WENSessionRehearsal } from "./pages/WENSessionRehearsal";
import { previewIdentity, previewModel } from "./ui-language-preview";

const display = vi.hoisted(() => ({ language: "en" as Language }));
vi.mock("./ui-preferences", () => ({
  useUI: () => ({ t: (key: string) => translate(display.language, key) }),
}));
vi.mock("./core", () => ({ useCore: () => ({ mode: "disconnected" }) }));
vi.mock("./workspace", () => ({
  useWorkspace: () => ({
    core: { mode: "disconnected" },
    state: { selectedProfile: "owner-profile" },
    agents: {},
  }),
}));

describe("Preview display copy", () => {
  it("preserves provider model names even when they match interface copy", () => {
    for (const language of Object.keys(languages) as Language[]) {
      const text = (key: string) => translate(language, key);
      for (const value of [
        "Current",
        "Model",
        "Fixture account",
        "fixture-openai-v2",
        "Owner fixture research",
      ])
        expect(previewModel(value, text)).toBe(value);
      for (const value of ["fixture-openai-v1", "OpenAI fixture"])
        expect(previewModel(value, text)).not.toMatch(/fixture/i);
    }
  });

  it("uses customer wording for every shipped interface label", () => {
    for (const language of Object.keys(languages) as Language[]) {
      for (const key of copyKeys.filter((key) => /fixture/i.test(key))) {
        expect(translate(language, key), `${language}: ${key}`).not.toMatch(
          /fixture/i,
        );
      }
      expect(translate(language, "Fixture")).not.toMatch(/fixture/i);
      expect(translate(language, "fixture-openai-v1")).not.toMatch(/fixture/i);
    }
    expect(translate("en", "Fixture account")).toBe("Test account");
    expect(translate("en", "fixture units")).toBe("test units");
  });

  it("keeps owner content, exact commands and interpolated identities intact", () => {
    for (const language of Object.keys(languages) as Language[]) {
      for (const value of [
        "Owner note: fixture research",
        "fased-app custody status --disposable-fixture",
        "fixture-wallet:alpha",
      ])
        expect(translate(language, value)).toBe(value);
      expect(
        translate(language, "{count} replies · {minutes} minutes", {
          count: "owner-fixture-value",
          minutes: 15,
        }),
      ).toContain("owner-fixture-value");
    }
    expect(translate("en", " Fixture account ")).toBe(" Test account ");
  });

  it("renders advanced paper headings and badges in all interface languages", () => {
    for (const language of Object.keys(languages) as Language[]) {
      display.language = language;
      for (const component of [
        WENPaperJourney,
        WENSessionAutomatic,
        WENSessionRehearsal,
      ]) {
        const html = renderToStaticMarkup(createElement(component));
        expect(html).not.toMatch(/fixture/i);
        expect(html).not.toContain("<button");
      }
    }
  });

  it("labels known test choices without changing their stored identities", () => {
    const choices = [
      "fixture-4h",
      "fixture-12h",
      "fixture-4h-unknown",
      "fixture-4h-stale",
      "fixture-4h-cutoff",
      "fixture-wallet:alpha",
      "fixture-wallet:beta",
    ];
    const original = JSON.stringify(choices);
    for (const language of Object.keys(languages) as Language[])
      for (const value of choices)
        expect(translate(language, previewIdentity(value))).not.toMatch(
          /fixture/i,
        );
    expect(JSON.stringify(choices)).toBe(original);
    expect(previewIdentity("Owner fixture research")).toBe(
      "Owner fixture research",
    );
  });
});
