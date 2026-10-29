// Disposable installer-observation helpers only; no product/runtime imports.
import { readFile, rm } from "node:fs/promises";

export async function readEnrollmentObservation(
  path,
  { durable = false } = {},
) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch (error) {
    if (!durable && (error.code === "ENOENT" || error instanceof SyntaxError))
      return undefined;
    throw error;
  }
}

export async function removeEnrollmentUnitLink(path) {
  try {
    await rm(path);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
}
