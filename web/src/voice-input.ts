import { useCallback, useEffect, useRef, useState } from "react";

export type SpeechRecognitionAlternativeLike = {
  transcript?: string;
};

export type SpeechRecognitionResultLike = {
  isFinal?: boolean;
  length: number;
  [index: number]: SpeechRecognitionAlternativeLike | undefined;
};

export type SpeechRecognitionEventLike = {
  resultIndex?: number;
  results: {
    length: number;
    [index: number]: SpeechRecognitionResultLike | undefined;
  };
};

export type SpeechRecognitionErrorEventLike = {
  error?: string;
};

export type SpeechRecognitionLike = {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((event: SpeechRecognitionEventLike) => void) | null;
  onerror: ((event: SpeechRecognitionErrorEventLike) => void) | null;
  onend: (() => void) | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
};

export type SpeechRecognitionCtorLike = new () => SpeechRecognitionLike;

export function speechRecognitionLang(uiLang: string): string {
  const code = (uiLang || "").trim().toLowerCase();
  if (code.startsWith("ru")) return "ru-RU";
  if (code.startsWith("es")) return "es-ES";
  if (code.startsWith("zh")) return "zh-CN";
  if (code.startsWith("ko")) return "ko-KR";
  return "en-US";
}

export function mergeVoiceTranscript(
  baseDraft: string,
  committed: string,
  interim: string,
): string {
  return [baseDraft.trim(), committed.trim(), interim.trim()]
    .filter(Boolean)
    .join(" ");
}

export function getSpeechRecognitionCtor(
  win?: Window | null,
): SpeechRecognitionCtorLike | null {
  const target =
    win === undefined ? (typeof window !== "undefined" ? window : null) : win;
  if (!target) return null;
  const candidate = target as unknown as {
    SpeechRecognition?: unknown;
    webkitSpeechRecognition?: unknown;
  };
  const ctor =
    candidate.SpeechRecognition ?? candidate.webkitSpeechRecognition ?? null;
  return typeof ctor === "function"
    ? (ctor as SpeechRecognitionCtorLike)
    : null;
}

export function useVoiceInput({
  language,
  onTranscript,
  onError,
}: {
  language: string;
  onTranscript: (text: string) => void;
  onError?: (errorKey: string) => void;
}) {
  const supported = getSpeechRecognitionCtor() !== null;
  const [listening, setListening] = useState(false);

  const recognitionRef = useRef<SpeechRecognitionLike | null>(null);
  const baseDraftRef = useRef("");
  const committedRef = useRef("");
  const onTranscriptRef = useRef(onTranscript);
  const onErrorRef = useRef(onError);
  onTranscriptRef.current = onTranscript;
  onErrorRef.current = onError;

  const stop = useCallback(() => {
    const rec = recognitionRef.current;
    recognitionRef.current = null;
    setListening(false);
    if (!rec) return;
    try {
      rec.stop();
    } catch {
      try {
        rec.abort();
      } catch {
        // Ignore cleanup errors from stopped instances.
      }
    }
  }, []);

  const start = useCallback(
    (initialDraft = ""): boolean => {
      const Ctor = getSpeechRecognitionCtor();
      if (!Ctor) {
        onErrorRef.current?.("Voice unavailable");
        return false;
      }
      if (recognitionRef.current) {
        stop();
      }

      const rec = new Ctor();
      rec.lang = speechRecognitionLang(language);
      rec.continuous = true;
      rec.interimResults = true;
      baseDraftRef.current = initialDraft;
      committedRef.current = "";

      rec.onresult = (ev) => {
        let finalText = "";
        let interimText = "";
        const len = ev.results?.length ?? 0;
        for (let i = 0; i < len; i++) {
          const item = ev.results[i];
          const text = item?.[0]?.transcript?.trim() ?? "";
          if (!text) continue;
          if (item?.isFinal) {
            finalText = finalText ? `${finalText} ${text}` : text;
          } else {
            interimText = interimText ? `${interimText} ${text}` : text;
          }
        }
        committedRef.current = finalText;
        onTranscriptRef.current(
          mergeVoiceTranscript(
            baseDraftRef.current,
            committedRef.current,
            interimText,
          ),
        );
      };

      rec.onerror = (ev) => {
        const code = (ev.error || "").trim();
        if (code === "aborted") {
          return;
        }
        if (recognitionRef.current === rec) {
          recognitionRef.current = null;
        }
        setListening(false);
        const key =
          code === "not-allowed" || code === "service-not-allowed"
            ? "Microphone blocked"
            : "Voice unavailable";
        onErrorRef.current?.(key);
      };

      rec.onend = () => {
        if (recognitionRef.current === rec) {
          recognitionRef.current = null;
          setListening(false);
        }
      };

      recognitionRef.current = rec;
      try {
        rec.start();
        setListening(true);
        return true;
      } catch {
        if (recognitionRef.current === rec) {
          recognitionRef.current = null;
        }
        setListening(false);
        onErrorRef.current?.("Voice unavailable");
        return false;
      }
    },
    [language, stop],
  );

  const toggle = useCallback(
    (initialDraft = "") => {
      if (listening) {
        stop();
        return false;
      }
      return start(initialDraft);
    },
    [listening, start, stop],
  );

  useEffect(() => {
    return () => {
      const rec = recognitionRef.current;
      recognitionRef.current = null;
      if (rec) {
        try {
          rec.abort();
        } catch {
          // Ignore abort failures on unmount.
        }
      }
    };
  }, []);

  return {
    supported,
    listening,
    start,
    stop,
    toggle,
  };
}
