// The development verifier authenticates exact RawMessage payload bytes.
// Return compact wire bytes; pretty-printing the envelope invalidates consent.
import { sign } from "node:crypto";
export function fixtureDevelopmentEnvelope(payload, keyId, privateKey) {
  const bytes = JSON.stringify(payload);
  return JSON.stringify({
    version: 1,
    keyId,
    payload,
    signature: sign(
      null,
      Buffer.from("fased-app-update-development:v1\0" + bytes),
      privateKey,
    ).toString("base64"),
  });
}
