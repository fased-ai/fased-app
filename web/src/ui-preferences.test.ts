import { describe, expect, it } from "vitest";
import { parsePreferences, defaultPreferences } from "./ui-preferences";
import { copyKeys, languages, resolveLanguage, translate } from "./ui-language";
describe("UI preferences", () => {
  it("translates reachable setup and agent controls while preserving owner content", () => {
    expect(translate("ru", "Refresh records")).toBe("Обновить записи");
    expect(translate("es", "Connection settings")).toBe("Ajustes de conexión");
    expect(translate("zh", "Updates and recovery")).toBe("更新与恢复");
    expect(translate("ko", "No expiry")).toBe("만료 없음");
    expect(translate("ru", "Owner fact: 250 SOL")).toBe("Owner fact: 250 SOL");
    expect(translate("es", " Open Chat ")).toBe(" Abrir Chat ");
    expect(
      translate("zh", "Once · {schedule}", { schedule: "09:00 UTC" }),
    ).toBe("单次 · 09:00 UTC");
    expect(translate("ko", "Daily at {time}")).toBe("매일 {time}");
  });
  it("translates finite Chat and credential permissions without changing values", () => {
    for (const language of ["ru", "es", "zh", "ko"] as const) {
      expect(translate(language, "Allow and send")).not.toBe("Allow and send");
      const limits = translate(
        language,
        "{count} replies · {minutes} minutes",
        { count: 20, minutes: 15 },
      );
      expect(limits).toContain("20");
      expect(limits).toContain("15");
      expect(limits).not.toContain("{count}");
      expect(translate(language, "Unattended tasks")).not.toBe(
        "Unattended tasks",
      );
    }
  });
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
