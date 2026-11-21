import { expect, it } from "vitest";
import { ownerHandoff, validOwnerHandoff } from "./owner-handoff";

it("keeps legacy Remote and ordinary Local handoffs distinct", () => {
  expect(ownerHandoff({ location: "local" })).toBeUndefined();
  expect(ownerHandoff({ location: "remote", ownerUid: 1000 })?.claim).toBe(
    "/var/lib/fased-app-vps-1000/fased-app vps claim-code",
  );
  expect(validOwnerHandoff({ location: "local", ownerUid: 1000 })).toBe(false);
});
it("uses clean non-dev handoff commands for Local and Remote", () => {
  expect(ownerHandoff({ location: "local", ownerUid: 1000, ownerKind: "custody" })).toEqual({
    claim: "fased open",
    recovery: ["fased open"],
  });
  expect(ownerHandoff({ location: "remote", ownerUid: 1000, ownerKind: "custody" })).toEqual({
    claim: "fased vps access",
    recovery: ["fased vps access"],
  });
  for (const location of ["local", "remote"]) {
    const c = { location, ownerUid: 1000, ownerKind: "custody" as const };
    expect(validOwnerHandoff(c)).toBe(true);
    const commands = ownerHandoff(c)!;
    expect(commands.claim + " " + commands.recovery.join(" ")).not.toMatch(
      /disposable-fixture|prepare-host|fased-app-custody|custody recover|fased-app-vps|backup-file|seed-file/,
    );
  }
});
it("rejects foreign or nonnumeric owner handoffs without shell interpolation", () => {
  for (const uid of [-1, 1.5, NaN, Infinity, 4294967295, "1000;id"]) {
    expect(
      ownerHandoff({
        location: "local",
        ownerKind: "custody",
        ownerUid: uid as number,
      }),
    ).toBeUndefined();
  }
  expect(
    ownerHandoff({ location: "local", ownerKind: "custody" }),
  ).toBeUndefined();
  expect(
    validOwnerHandoff({
      location: "remote",
      ownerUid: 1000,
      ownerKind: "foreign" as "custody",
    }),
  ).toBe(false);
});
