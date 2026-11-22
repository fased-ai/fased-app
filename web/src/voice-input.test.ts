import { describe, expect, it } from "vitest";
import {
  getSpeechRecognitionCtor,
  mergeVoiceTranscript,
  speechRecognitionLang,
} from "./voice-input";

describe("voice-input helpers", () => {
  it("maps workspace UI languages to BCP-47 speech recognition locales", () => {
    expect(speechRecognitionLang("en")).toBe("en-US");
    expect(speechRecognitionLang("ru")).toBe("ru-RU");
    expect(speechRecognitionLang("es")).toBe("es-ES");
    expect(speechRecognitionLang("zh")).toBe("zh-CN");
    expect(speechRecognitionLang("zh-Hans")).toBe("zh-CN");
    expect(speechRecognitionLang("ko")).toBe("ko-KR");
    expect(speechRecognitionLang("unknown")).toBe("en-US");
  });

  it("merges existing draft with committed and interim voice segments cleanly", () => {
    expect(mergeVoiceTranscript("", "check fees", "on solana")).toBe(
      "check fees on solana",
    );
    expect(mergeVoiceTranscript("  Please  ", "check fees", "")).toBe(
      "Please check fees",
    );
    expect(mergeVoiceTranscript("", "", "")).toBe("");
  });

  it("resolves standard and webkit SpeechRecognition constructors safely", () => {
    class FakeStandard {}
    class FakeWebkit {}

    expect(
      getSpeechRecognitionCtor({ SpeechRecognition: FakeStandard } as never),
    ).toBe(FakeStandard);
    expect(
      getSpeechRecognitionCtor({
        webkitSpeechRecognition: FakeWebkit,
      } as never),
    ).toBe(FakeWebkit);
    expect(getSpeechRecognitionCtor({} as never)).toBeNull();
    expect(getSpeechRecognitionCtor(null)).toBeNull();
  });
});
