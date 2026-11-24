type WailsRuntimeWindow = Window & {
  runtime?: {
    BrowserOpenURL?: (url: string) => void;
  };
};

export function isSafeExternalWebURL(
  href: string,
  currentHref: string,
): string | null {
  const trimmed = href.trim();
  if (!trimmed) return null;
  let parsed: URL;
  let current: URL;
  try {
    current = new URL(currentHref);
    parsed = new URL(trimmed, current);
  } catch {
    return null;
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    return null;
  }
  if (parsed.origin === current.origin) {
    return null;
  }
  return parsed.toString();
}

export type EditableTargetLike = {
  tagName?: string;
  isContentEditable?: boolean;
  closest?: (selector: string) => unknown;
};

export function isEditableTarget(
  target: EventTarget | EditableTargetLike | null,
): boolean {
  if (!target || typeof target !== "object") return false;
  const el = target as EditableTargetLike;
  const tag = (el.tagName || "").toUpperCase();
  if (tag === "INPUT" || tag === "TEXTAREA") return true;
  if (el.isContentEditable) return true;
  if (typeof el.closest === "function") {
    return Boolean(
      el.closest(
        'input, textarea, [contenteditable="true"], [contenteditable=""]',
      ),
    );
  }
  return false;
}

export function shouldAllowNativeContextMenu(
  target: EventTarget | EditableTargetLike | null,
  selectionText: string,
): boolean {
  return isEditableTarget(target) || selectionText.trim().length > 0;
}

export function resolveMcpDisplayOrigin(locationHref: string): string {
  try {
    const parsed = new URL(locationHref);
    if (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      parsed.hostname !== "wails.localhost"
    ) {
      return parsed.origin;
    }
  } catch {
    // Fall through to default local core loopback origin.
  }
  return "http://127.0.0.1:7331";
}

let desktopRuntimeInitialized = false;

export function initDesktopRuntime(): void {
  if (desktopRuntimeInitialized || typeof window === "undefined") return;
  const openExternal = (window as WailsRuntimeWindow).runtime?.BrowserOpenURL;
  if (typeof openExternal !== "function") return;
  desktopRuntimeInitialized = true;

  document.addEventListener(
    "click",
    (event) => {
      if (event.defaultPrevented || event.button !== 0) return;
      const target = event.target as Element | null;
      const anchor = target?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!anchor) return;
      const href = anchor.getAttribute("href") || "";
      const externalURL = isSafeExternalWebURL(href, window.location.href);
      if (!externalURL) return;
      if (anchor.target === "_blank" || externalURL) {
        event.preventDefault();
        openExternal(externalURL);
      }
    },
    true,
  );

  document.addEventListener("contextmenu", (event) => {
    const selectionText = window.getSelection?.()?.toString() ?? "";
    if (!shouldAllowNativeContextMenu(event.target, selectionText)) {
      event.preventDefault();
    }
  });
}
