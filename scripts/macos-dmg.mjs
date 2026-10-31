import { execFileSync } from "node:child_process";
import { setTimeout } from "node:timers/promises";

// hdiutil may report an OS resource as busy while creating an image. Keep the
// original failure after a finite budget; never change host policy or retry
// other failures, and do not extend the native CI job timeout.
export async function createMacDiskImage(
  args,
  {
    run = execFileSync,
    wait = setTimeout,
    log = (text) => process.stderr.write(text),
  } = {},
) {
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const result = run("hdiutil", args, {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      });
      if (result) log(result);
      return;
    } catch (err) {
      if (err.stdout) log(String(err.stdout));
      const stderr = String(err.stderr ?? "");
      if (stderr) log(stderr);
      if (
        attempt === 2 ||
        err.status !== 1 ||
        err.signal ||
        !/(?:^|\n)hdiutil: create failed - Resource busy(?:\r?\n|$)/.test(
          stderr,
        )
      )
        throw err;
      log(`Retrying busy disk-image creation (${attempt + 2}/3).\n`);
      await wait(1000 * (attempt + 1));
    }
  }
}
