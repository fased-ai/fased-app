import { describe, expect, it } from "vitest";
import { isCompleteSignInCallback } from "./provider-signin-ui";

describe("isCompleteSignInCallback", () => {
  it("accepts complete OpenAI and Google loopback callback URLs", () => {
    expect(
      isCompleteSignInCallback(
        "openai",
        "  http://127.0.0.1:48123/auth/callback?code=abc123&state=xyz789&client_id=oaiapp_1  ",
      ),
    ).toBe(true);
    expect(
      isCompleteSignInCallback(
        "google",
        "http://localhost:8080/auth/callback?code=4/0A&state=st-1",
      ),
    ).toBe(true);
  });

  it("rejects incomplete, malformed, or non-loopback callback URLs", () => {
    expect(isCompleteSignInCallback("openai", "")).toBe(false);
    expect(isCompleteSignInCallback("openai", "   ")).toBe(false);
    expect(
      isCompleteSignInCallback(
        "openai",
        "http://127.0.0.1:48123/auth/callback?code=abc",
      ),
    ).toBe(false);
    expect(
      isCompleteSignInCallback(
        "openai",
        "http://127.0.0.1:48123/auth/callback?state=xyz",
      ),
    ).toBe(false);
    expect(
      isCompleteSignInCallback(
        "openai",
        "http://127.0.0.1:48123/wrong?code=abc&state=xyz",
      ),
    ).toBe(false);
    expect(
      isCompleteSignInCallback(
        "openai",
        "https://example.com/auth/callback?code=abc&state=xyz",
      ),
    ).toBe(false);
    expect(
      isCompleteSignInCallback(
        "openai",
        "https://auth.openai.com/api/accounts/authorize?state=xyz",
      ),
    ).toBe(false);
  });

  it("accepts OpenRouter pasted codes and callback URLs", () => {
    expect(isCompleteSignInCallback("openrouter", "or_code_123456")).toBe(true);
    expect(
      isCompleteSignInCallback(
        "openrouter",
        "http://127.0.0.1:9000/auth/callback?code=or_1234",
      ),
    ).toBe(true);
    expect(isCompleteSignInCallback("openrouter", "short")).toBe(false);
    expect(isCompleteSignInCallback("openrouter", "has spaces inside")).toBe(
      false,
    );
  });
});
