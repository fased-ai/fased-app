// Release credentials remain in the owner's desktop keyring, never Git or argv.
import { spawnSync } from "node:child_process";
import {
  unlockLocalSigning,
  privateSigningPassphrase,
} from "./local-release-signing.mjs";

export function desktopCredentials(config, { run = spawnSync } = {}) {
  if (
    !/^[a-f0-9]{64}$/.test(config.identitySHA256) ||
    !["production", "disposable"].includes(config.keyClass)
  )
    throw Error("Bound signing identity required");
  const attributes = [
    "application",
    "fased-release",
    "identity",
    config.identitySHA256,
    "key-class",
    config.keyClass,
  ];
  const call = (args, input) => {
    const result = run("secret-tool", args, {
      input,
      encoding: "buffer",
      timeout: 30000,
      maxBuffer: 4096,
      stdio: [input ? "pipe" : "ignore", "pipe", "pipe"],
    });
    result.stderr?.fill?.(0);
    if (result.error || result.status !== 0) {
      result.stdout?.fill?.(0);
      throw Error(
        "Desktop signing is locked or not set up; run release credentials once",
      );
    }
    return result.stdout;
  };
  return {
    getPassphrase() {
      const raw = call(["lookup", ...attributes]);
      // secret-tool appends one newline; preserve all actual password bytes.
      const end = raw.at(-1) === 10 ? raw.length - 1 : raw.length;
      const pass = Buffer.from(raw.subarray(0, end));
      raw.fill(0);
      if (pass.length < 16 || pass.length > 1024) {
        pass.fill(0);
        throw Error("Desktop signing credential invalid");
      }
      return pass;
    },
    async remember(getPassphrase = privateSigningPassphrase) {
      const pass = await getPassphrase(
        "Existing release password (one-time desktop setup): ",
      );
      let session;
      try {
        session = unlockLocalSigning({ ...config, passphrase: pass });
        session.close();
        session = null;
        const reply = call(
          ["store", "--label=Fased release signing", ...attributes],
          pass,
        );
        reply?.fill?.(0);
      } finally {
        session?.close();
        pass.fill(0);
      }
      return { remembered: true, identitySHA256: config.identitySHA256 };
    },
    forget() {
      call(["clear", ...attributes])?.fill?.(0);
      return { remembered: false };
    },
  };
}
