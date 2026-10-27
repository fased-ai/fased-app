import { describe, expect, it } from "vitest";
import { initialState, reducer, restoreState } from "./state";
describe("prototype state boundaries", () => {
  it("resumes an interrupted setup without implying completion", () => {
    const state = reducer(initialState(), {
      type: "setup",
      patch: { host: "vps", step: 2, walletMode: "automation" },
    });
    const restored = restoreState(JSON.stringify(state));
    expect(restored.setup).toEqual({
      host: "vps",
      step: 2,
      complete: false,
      walletMode: "automation",
      provider: "none",
    });
  });
  it("rejects corrupt, outdated and invalid stored setup", () => {
    for (const raw of [
      "{",
      "{}",
      JSON.stringify({ ...initialState(), version: 2 }),
      JSON.stringify({
        ...initialState(),
        setup: { ...initialState().setup, step: 999 },
      }),
    ])
      expect(restoreState(raw)).toEqual(initialState());
  });
  it("keeps memory scoped when deleting from a different profile", () => {
    const state = reducer(initialState(), { type: "memory-delete", id: "m2" });
    expect(state.memories.some((m) => m.id === "m2")).toBe(true);
    expect(
      reducer(state, { type: "memory-delete", id: "m1" }).memories.some(
        (m) => m.id === "m1",
      ),
    ).toBe(false);
  });
  it("changes the profile model preference without changing wallet setup", () => {
    const state = reducer(initialState(), {
      type: "provider",
      provider: "openai",
    });
    expect(state.profiles[0].provider).toBe("openai");
    expect(state.profiles[1].provider).toBe("none");
    expect(state.setup).toEqual(initialState().setup);
  });
  it("does not create a wallet or permission when adding a persona", () => {
    const state = reducer(initialState(), {
      type: "add-profile",
      name: "  Research desk  ",
      role: "News only",
    });
    expect(state.profiles.at(-1)?.name).toBe("Research desk");
    expect(state.profiles.at(-1)?.provider).toBe("none");
    expect(state.setup).toEqual(initialState().setup);
    expect(reducer(state, { type: "add-profile", name: "  ", role: "" })).toBe(
      state,
    );
  });
});
