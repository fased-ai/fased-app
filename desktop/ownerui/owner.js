/* Isolated native-owner assets. Never load a core page or send secrets to fetch. */
const byId = (id) => document.getElementById(id);
const owner = window.go.main.WalletOwner;
const info = await owner.Info();
const trade = info.action === "approve-swap";
byId("network").textContent = info.network === "devnet" ? "Devnet" : "Mainnet";
byId("instance").textContent = info.origin || "This computer";
byId("title").textContent = {
  "approve-swap": "Approve swap",
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
if (trade) {
  for (const id of ["confirm", "backup", "backup-confirm"]) {
    byId(id).required = false;
    byId(id).closest("label").hidden = true;
  }
  byId("unlock").autocomplete = "current-password";
  byId("submit").textContent = "Approve";
  const p = JSON.parse(info.proposal),
    q = p.quote;
  const unit = (n, decimals) => {
    const s = String(n).padStart(decimals + 1, "0");
    return (
      s.slice(0, -decimals) +
      (s.slice(-decimals).replace(/0+$/, "")
        ? "." + s.slice(-decimals).replace(/0+$/, "")
        : "")
    );
  };
  const asset = (mint) =>
    mint === "So11111111111111111111111111111111111111112"
      ? [9, "wSOL"]
      : [6, "USDC"];
  const amount = (n, mint) => {
    const [d, s] = asset(mint);
    return unit(n, d) + " " + s;
  };
  for (const [label, value] of [
    ["Wallet", q.owner],
    ["Pay", amount(q.amount, q.inputMint)],
    ["Receive", amount(q.output, q.outputMint)],
    ["Minimum", amount(q.minimumOutput, q.outputMint)],
    ["Fee limit", unit(q.maxFeeLamports, 9) + " SOL"],
    ["Rent", unit(q.rentLamports, 9) + " SOL"],
    ["Expires", new Date(q.expiresAt * 1000).toLocaleString()],
    ["Review", p.digest],
  ]) {
    for (const [tag, text] of [
      ["dt", label],
      ["dd", value],
    ]) {
      const el = document.createElement(tag);
      el.textContent = text;
      byId("trade-facts").append(el);
    }
  }
  byId("trade-review").hidden = false;
}
let busy = false;
const lockTarget = (locked) => {
  for (const id of ["target", "user"]) byId(id).disabled = locked;
};
const showWallet = async () => {
  if (trade) {
    const current = await owner.Info();
    byId("unlock").required = current.recorded !== "true";
    byId("unlock").closest("label").hidden = current.recorded === "true";
    const expired =
      JSON.parse(info.proposal).quote.expiresAt * 1000 <= Date.now();
    byId("submit").disabled = current.recorded !== "true" && expired;
    byId("submit").textContent =
      current.recorded === "true"
        ? "View result"
        : expired
          ? "Expired"
          : "Approve";
  }
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
      await showWallet();
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
    await showWallet();
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
  if (!trade && byId("confirm").value !== byId("unlock").value) {
    error("Passwords differ");
    return;
  }
  if (!trade && byId("backup-confirm").value !== byId("backup").value) {
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
      if (!file || file.size > 2 * 18 * 1024 * 1024 + 65536)
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
    if (trade) {
      byId("result-title").textContent = result.trade;
      byId("wallet-result").hidden = true;
      byId("trade-result").hidden = false;
      byId("close").textContent = "Done";
    }
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
