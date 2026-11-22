"use strict";
const token = location.hash.slice(1);
history.replaceState(null, "", "/");
const el = (id) => document.getElementById(id);
const post = (path, value) =>
  fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Fased-Owner": token },
    body: JSON.stringify(value),
    credentials: "omit",
    cache: "no-store",
    redirect: "error",
    referrerPolicy: "no-referrer",
  });
let action = "";
let uploadedRecoveryBase64 = "";
function bytesToBase64(bytes) {
  const chunks = [];
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    chunks.push(String.fromCharCode(...bytes.subarray(i, i + chunkSize)));
  }
  return btoa(chunks.join(""));
}
el("backup-upload").addEventListener("change", async () => {
  const file = el("backup-upload").files[0];
  if (!file || file.size > 36 * 1024 * 1024) {
    uploadedRecoveryBase64 = "";
    el("backup-upload").value = "";
    el("status").textContent = "Choose a valid encrypted backup file.";
    return;
  }
  const bytes = new Uint8Array(await file.arrayBuffer());
  uploadedRecoveryBase64 = bytesToBase64(bytes);
  el("recovery").value = "";
  el("status").textContent = `Loaded ${file.name} (${bytes.length} bytes).`;
});
el("recovery").addEventListener("input", () => {
  if (uploadedRecoveryBase64) {
    uploadedRecoveryBase64 = "";
    el("backup-upload").value = "";
  }
});
async function start() {
  const response = await post("/info", {});
  if (!response.ok) throw new Error();
  const info = await response.json();
  if (!["setup", "import", "recover"].includes(info.action)) throw new Error();
  action = info.action;
  el("title").textContent = {
    setup: "Create wallet",
    import: "Import wallet",
    recover: "Recover wallet",
  }[action];
  el("network").textContent = info.network;
  el("instance").textContent = `Instance ${info.instance}`;
  el("seed-label").hidden = action !== "import";
  el("recovery-fields").hidden = action !== "recover";
}
el("wallet-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!action) return;
  el("submit").disabled = true;
  el("status").textContent = "Working…";
  const input = {
    Unlock: el("unlock").value,
    UnlockConfirm: el("unlock-confirm").value,
    BackupPassword: el("backup").value,
    BackupConfirm: el("backup-confirm").value,
    Seed: el("seed").value,
    Recovery: uploadedRecoveryBase64 ? "" : el("recovery").value,
    RecoveryBase64: uploadedRecoveryBase64,
    Fingerprint: el("fingerprint").value,
  };
  try {
    const response = await post("/enroll", input);
    if (!response.ok) throw new Error();
    const result = await response.json();
    uploadedRecoveryBase64 = "";
    el("backup-upload").value = "";
    el("wallet-form").reset();
    el("wallet-form").hidden = true;
    el("address").textContent = result.address;
    el("backup-file").textContent = result.backupFile;
    el("backup-fingerprint").textContent = result.fingerprint;
    el("result").hidden = false;
    el("status").textContent = result.recoveryHeld
      ? "Recovery is required before use."
      : "Wallet locked. Backup verified.";
  } catch (_) {
    el("status").textContent =
      "Could not finish. Check the inputs or reopen Wallets to resume.";
    el("submit").disabled = false;
  }
});
start().catch(() => {
  el("status").textContent =
    "This Wallets session is unavailable. Reopen it from Fased.";
  el("submit").disabled = true;
});
