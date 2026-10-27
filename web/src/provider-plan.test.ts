import { describe, expect, it } from "vitest";
import {
  isPlanPolicy,
  planFailureDetails,
  type OpenAIModelPolicy,
} from "./agents";

describe("plan presentation", () => {
  it("keeps retained fixture and API policies separate from plan usage", () => {
    const policy = (wireVersion: string) =>
      ({ wireVersion }) as OpenAIModelPolicy;
    expect(isPlanPolicy(policy("openai-signin-v2"))).toBe(true);
    expect(isPlanPolicy(policy("openai-signin-v1"))).toBe(false);
    expect(isPlanPolicy(policy("openai-responses-v1"))).toBe(false);
    expect(isPlanPolicy(undefined)).toBe(false);
  });

  it.each([
    [429, "", "ChatGPT limit reached"],
    [200, "rate_limit_exceeded", "ChatGPT limit reached"],
    [401, "", "Reconnect required"],
    [403, "", "Plan access unavailable"],
    [200, "insufficient_scope", "Plan access unavailable"],
    [400, "context_length_exceeded", "Input too long"],
    [404, "model_not_found", "Model unavailable"],
    [504, "", "Request timed out"],
    [503, "", "ChatGPT unavailable"],
    [400, "invalid_request_error", "ChatGPT request failed"],
  ])(
    "summarizes %s/%s while retaining inspectable evidence",
    (status, code, summary) => {
      const details = JSON.stringify({
        status,
        code,
        body: "synthetic provider evidence",
        requestId: "fixture-request",
        late: status === 200,
      });
      const result = planFailureDetails(
        "OpenAI plan request stopped. Diagnostics: " + details,
      );
      expect(result).toEqual({ summary, details });
      expect(result?.summary).not.toContain("synthetic provider evidence");
      expect(result?.summary).not.toContain("fixture-request");
    },
  );

  it("does not rewrite local failures or expose malformed diagnostics as status", () => {
    expect(planFailureDetails("Connection locked")).toBeNull();
    expect(
      planFailureDetails("OpenAI plan request stopped. Diagnostics: {broken"),
    ).toEqual({ summary: "ChatGPT request failed", details: "{broken" });
  });
});
