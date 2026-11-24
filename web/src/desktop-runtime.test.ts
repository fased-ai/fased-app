import { describe, expect, it } from "vitest";
import {
  isSafeExternalWebURL,
  resolveMcpDisplayOrigin,
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

  it("maps Wails desktop origins to local core HTTP origin for MCP display", () => {
    expect(resolveMcpDisplayOrigin("wails://wails/#settings")).toBe(
      "http://127.0.0.1:7331",
    );
    expect(resolveMcpDisplayOrigin("http://wails.localhost/#overview")).toBe(
      "http://127.0.0.1:7331",
    );
    expect(resolveMcpDisplayOrigin("http://127.0.0.1:7341/#overview")).toBe(
      "http://127.0.0.1:7341",
    );
    expect(
      resolveMcpDisplayOrigin("https://fased.tail7bbde2.ts.net/#overview"),
    ).toBe("https://fased.tail7bbde2.ts.net");
  });
});
