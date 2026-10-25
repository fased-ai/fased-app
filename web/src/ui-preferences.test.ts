import { describe, expect, it } from "vitest";
import { parsePreferences, defaultPreferences } from "./ui-preferences";
import { copyKeys, languages, resolveLanguage, translate } from "./ui-language";
describe("UI preferences", () => {
  it("refuses corrupt or unsupported browser values without touching application state", () => {
    for (const value of [
      null,
      "invalid",
      "null",
      "[]",
      '{"theme":"bright","language":"toString","rail":"false"}',
    ])
      expect(parsePreferences(value)).toEqual(defaultPreferences);
    expect(
      parsePreferences(
        '{"theme":"light","language":"ko","rail":false,"credential":"ignored"}',
      ),
    ).toEqual({ theme: "light", language: "ko", rail: false });
  });
  it("resolves browser languages and provides complete shipped interface catalogs", () => {
    expect(resolveLanguage("system", ["fr-FR", "zh-TW"])).toBe("zh");
    expect(resolveLanguage("system", ["es-MX"])).toBe("es");
    expect(resolveLanguage("system", ["ja-JP"])).toBe("en");
    expect(resolveLanguage("ru", ["en"])).toBe("ru");
    expect(resolveLanguage("constructor", [])).toBe("en");
    for (const language of Object.keys(languages) as (keyof typeof languages)[])
      for (const key of copyKeys) expect(translate(language, key)).toBeTruthy();
    expect(translate("ko", "Owner-authored saved headline")).toBe(
      "Owner-authored saved headline",
    );
    expect(translate("ru", "constructor")).toBe("constructor");
    expect(translate("es", "toString")).toBe("toString");
  });
});
