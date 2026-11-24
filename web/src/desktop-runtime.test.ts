import { describe, expect, it } from "vitest";
import {
  isSafeExternalWebURL,
  shouldAllowNativeContextMenu,
} from "./desktop-runtime";

describe("desktop-runtime helpers", () => {
  it("admits only external http/https URLs for BrowserOpenURL", () => {
    expect(
      isSafeExternalWebURL(
        "https://platform.openai.com/api-keys",
        "wails://wails/#settings",
      ),
    ).toBe("https://platform.openai.com/api-keys");
    expect(
      isSafeExternalWebURL(
        "http://127.0.0.1:1455/auth/callback",
        "http://wails.localhost/",
      ),
    ).toBe("http://127.0.0.1:1455/auth/callback");
    expect(
      isSafeExternalWebURL("javascript:alert(1)", "wails://wails/"),
    ).toBeNull();
    expect(
      isSafeExternalWebURL("file:///etc/passwd", "wails://wails/"),
    ).toBeNull();
    expect(
      isSafeExternalWebURL("data:text/html,hi", "wails://wails/"),
    ).toBeNull();
    expect(
      isSafeExternalWebURL("#agents/chat", "http://wails.localhost/#overview"),
    ).toBeNull();
    expect(
      isSafeExternalWebURL(
        "http://wails.localhost/#wallets",
        "http://wails.localhost/",
      ),
    ).toBeNull();
  });

  it("allows native context menu only for editable targets or selected text", () => {
    expect(shouldAllowNativeContextMenu({ tagName: "INPUT" }, "")).toBe(true);
    expect(shouldAllowNativeContextMenu({ tagName: "TEXTAREA" }, "")).toBe(
      true,
    );
    expect(
      shouldAllowNativeContextMenu(
        { tagName: "DIV", isContentEditable: true },
        "",
      ),
    ).toBe(true);
    expect(
      shouldAllowNativeContextMenu({ tagName: "P" }, "selected reply"),
    ).toBe(true);
    expect(shouldAllowNativeContextMenu({ tagName: "BUTTON" }, "   ")).toBe(
      false,
    );
    expect(shouldAllowNativeContextMenu(null, "")).toBe(false);
  });
});
