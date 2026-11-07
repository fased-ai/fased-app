/* Isolated native-owner assets. Never load a core page or send secrets to fetch. */
const byId = (id) => document.getElementById(id);
const owner = window.go.main.WalletOwner;
const info = await owner.Info();
byId("network").textContent = info.network === "devnet" ? "Devnet" : "Mainnet";
byId("instance").textContent = info.origin || "This computer";
byId("title").textContent = {
  setup: "Create wallet",
  import: "Import wallet",
  recover: "Restore key",
}[info.action];
byId("remote-user").hidden = !info.origin;
byId("remote-target").hidden = !info.origin;
byId("target").value = info.address || "";
byId("user").value = info.user || "";
byId("seed-field").hidden = info.action !== "import";
byId("restore-field").hidden = info.action !== "recover";
byId("seed").required = info.action === "import";
let busy = false;
const lockTarget = (locked) => {
  for (const id of ["target", "user"]) byId(id).disabled = locked;
};
const showWallet = () => {
  byId("admin-setup").hidden = true;
  byId("instance").textContent = byId("target").value || "This computer";
  byId("connect").hidden = true;
  byId("wallet").hidden = false;
  byId("unlock").focus();
};
const error = (e) => {
  byId("error").textContent = String(e);
};
byId("connect-button").onclick = async () => {
  if (busy) return;
  busy = true;
  lockTarget(true);
  byId("connect-button").disabled = true;
  error("");
  try {
    const ready = await owner.Connect(byId("target").value, byId("user").value);
    if (ready === false) {
      byId("enable-button").hidden = false;
      byId("connect-button").hidden = true;
    } else {
      showWallet();
    }
  } catch (e) {
    error(e);
  } finally {
    busy = false;
    lockTarget(false);
    byId("connect-button").disabled = false;
  }
};
byId("enable-button").onclick = async () => {
  if (busy) return;
  byId("admin-setup").hidden = true;
  busy = true;
  lockTarget(true);
  byId("enable-button").disabled = true;
  byId("connect-button").disabled = true;
  byId("setup-status").hidden = false;
  error("");
  try {
    await owner.Enable(byId("target").value, byId("user").value);
    showWallet();
  } catch (e) {
    error(e);
    const current = await owner.Info();
    if (current.setup) {
      byId("setup-command").value = current.setup;
      byId("instance").textContent = byId("target").value;
      byId("admin-setup").hidden = false;
    }
  } finally {
    busy = false;
    lockTarget(false);
    byId("enable-button").disabled = false;
    byId("connect-button").disabled = false;
    byId("setup-status").hidden = true;
  }
};
byId("wallet").onsubmit = async (event) => {
  event.preventDefault();
  if (busy) return;
  if (byId("confirm").value !== byId("unlock").value) {
    error("Passwords differ");
    return;
  }
  if (byId("backup-confirm").value !== byId("backup").value) {
    error("Backup passwords differ");
    return;
  }
  busy = true;
  byId("submit").disabled = true;
  error("");
  try {
    let recovery = "";
    if (info.action === "recover") {
      const file = byId("restore").files[0];
      if (!file || file.size > 262144)
        throw new Error("Choose an encrypted backup");
      recovery = await file.text();
    }
    const result = await owner.Run(
      byId("unlock").value,
      byId("backup").value,
      byId("seed").value,
      recovery,
      byId("checksum").value,
    );
    byId("address").textContent = result.address;
    byId("backup-file").textContent = result.backupFile;
    byId("fingerprint").textContent = result.fingerprint;
    byId("wallet").hidden = true;
    byId("result").hidden = false;
  } catch (e) {
    error(e);
  } finally {
    for (const id of ["unlock", "confirm", "backup", "backup-confirm", "seed"])
      byId(id).value = "";
    busy = false;
    byId("submit").disabled = false;
  }
};
byId("close").onclick = async () => {
  await owner.Cancel();
  window.runtime.Quit();
};

byId("copy-setup").onclick = async () => {
  const field = byId("setup-command");
  try {
    await navigator.clipboard.writeText(field.value);
  } catch {
    field.focus();
    field.select();
  }
};

for (const id of ["target", "user"])
  byId(id).addEventListener("input", () => {
    byId("admin-setup").hidden = true;
    byId("setup-command").value = "";
  });
