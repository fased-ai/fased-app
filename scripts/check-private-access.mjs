// Real local OpenSSH and TLS fixtures. This does not enroll/change Tailscale,
// create a VPS, change owner SSH/firewall settings or use wallet keys.
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createServer as tcpServer } from "node:net";
import { createServer as tlsServer } from "node:https";
import { request } from "node:http";
import { randomBytes, createHash } from "node:crypto";
import { userInfo } from "node:os";
import { chromium } from "@playwright/test";
if (
  process.platform !== "linux" ||
  process.env.FASED_LOCAL_ACCEPTANCE !== "1" ||
  process.getuid() === 0
)
  throw new Error(
    "Requires authorized ordinary-user Linux private-access check",
  );
const root = resolve(".evidence/private-access-live");
await mkdir(root, { mode: 0o700 });
await writeFile(
  join(root, "fixture.json"),
  JSON.stringify({ nonauthoritative: true }),
  { mode: 0o600 },
);
const state = join(root, "state"),
  binary = resolve(".evidence/bin/fased-app");
const report = {
  result: "FAIL",
  class: "real local SSH/TLS; SUPPORTING for actual VPS/Tailscale acceptance",
  checks: {},
};
const children = [];
let browser, proxy;
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
let coreOutput = "",
  sshLog = "";
async function port() {
  const s = tcpServer();
  await new Promise((r) => s.listen(0, "127.0.0.1", r));
  const n = s.address().port;
  await new Promise((r) => s.close(r));
  return n;
}
function child(command, args) {
  const p = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"] });
  p.on("error", (e) => {
    sshLog += String(e);
  });
  children.push(p);
  return p;
}
try {
  const corePort = await port(),
    sshPort = await port(),
    tlsPort = await port();
  const url = `http://127.0.0.1:${corePort}`,
    origin = `https://localhost:${tlsPort}`;
  for (const name of ["host", "client"])
    execFileSync("ssh-keygen", [
      "-q",
      "-t",
      "ed25519",
      "-N",
      "",
      "-f",
      join(root, name),
    ]);
  await writeFile(
    join(root, "authorized_keys"),
    await readFile(join(root, "client.pub")),
    { mode: 0o600 },
  );
  const hostKey = (await readFile(join(root, "host.pub"), "utf8"))
    .trim()
    .split(" ")
    .slice(0, 2)
    .join(" ");
  await writeFile(
    join(root, "known_hosts"),
    `[127.0.0.1]:${sshPort} ${hostKey}\n`,
    { mode: 0o600 },
  );
  const config = `Port ${sshPort}\nListenAddress 127.0.0.1\nHostKey ${join(root, "host")}\nPidFile ${join(root, "sshd.pid")}\nAuthorizedKeysFile ${join(root, "authorized_keys")}\nStrictModes yes\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nAuthenticationMethods publickey\nPermitRootLogin no\nAllowUsers ${userInfo().username}\nAllowTcpForwarding local\nPermitOpen 127.0.0.1:${corePort}\nGatewayPorts no\nX11Forwarding no\nLogLevel ERROR\n`;
  await writeFile(join(root, "sshd_config"), config, { mode: 0o600 });
  const daemon = child("/usr/sbin/sshd", [
    "-D",
    "-e",
    "-f",
    join(root, "sshd_config"),
  ]);
  daemon.stderr.on("data", (b) => (sshLog += String(b)));
  const core = child(binary, [
    "serve",
    "--data-dir",
    state,
    "--listen",
    `127.0.0.1:${corePort}`,
    "--public-origin",
    origin,
  ]);
  core.stdout.on("data", (b) => (coreOutput += String(b)));
  for (let i = 0; i < 100 && !coreOutput.includes('"event":"listening"'); i++)
    await pause(100);
  assert.match(coreOutput, /"event":"listening"/);
  const tunnel = child("ssh", [
    "-N",
    "-F",
    "/dev/null",
    "-o",
    "StrictHostKeyChecking=yes",
    "-o",
    `UserKnownHostsFile=${join(root, "known_hosts")}`,
    "-o",
    "IdentitiesOnly=yes",
    "-o",
    "ExitOnForwardFailure=yes",
    "-i",
    join(root, "client"),
    "-L",
    `[::1]:${corePort}:127.0.0.1:${corePort}`,
    "-p",
    String(sshPort),
    `${userInfo().username}@127.0.0.1`,
  ]);
  tunnel.stderr.on("data", (b) => (sshLog += String(b)));
  await pause(600);
  assert.equal(tunnel.exitCode, null, sshLog);
  browser = await chromium.launch({
    headless: true,
    executablePath: process.env.FASED_TEST_BROWSER || undefined,
    args: ["--host-resolver-rules=MAP localhost [::1]"],
  });
  const page = await browser.newPage();
  const response = await page.goto(`http://localhost:${corePort}`);
  const address = await response.serverAddr();
  assert.ok(
    ["::1", "[::1]"].includes(address.ipAddress),
    JSON.stringify(address),
  );
  const password = "FixtureOnly-" + randomBytes(24).toString("base64url");
  const code = JSON.parse(
    await readFile(join(state, "claim-code.json"), "utf8"),
  ).code;
  await page.getByLabel("One-use claim code").fill(code);
  await page.getByLabel("Create owner password").fill(password);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await page.getByRole("button", { name: "Sign out", exact: true }).waitFor();
  report.checks.sshTunnelBrowserClaim =
    "PASS: pinned generated host key and IPv6 local tunnel; IPv4-only core";
  await browser.close();
  browser = undefined;
  assert.equal((await fetch(url + "/api/v1/health")).status, 200);
  report.checks.browserCloseKeepsCore = "PASS";
  execFileSync(
    "openssl",
    [
      "req",
      "-x509",
      "-newkey",
      "rsa:2048",
      "-nodes",
      "-keyout",
      join(root, "tls.key"),
      "-out",
      join(root, "tls.crt"),
      "-days",
      "1",
      "-subj",
      "/CN=localhost",
      "-addext",
      "subjectAltName=DNS:localhost",
    ],
    { stdio: "ignore" },
  );
  proxy = tlsServer(
    {
      key: await readFile(join(root, "tls.key")),
      cert: await readFile(join(root, "tls.crt")),
    },
    (req, res) => {
      const upstream = request(
        url + req.url,
        { method: req.method, headers: req.headers },
        (r) => {
          res.writeHead(r.statusCode, r.headers);
          r.pipe(res);
        },
      );
      upstream.on("error", () => {
        res.writeHead(502);
        res.end();
      });
      req.pipe(upstream);
    },
  );
  await new Promise((r) => proxy.listen(tlsPort, "127.0.0.1", r));
  browser = await chromium.launch({
    headless: true,
    executablePath: process.env.FASED_TEST_BROWSER || undefined,
  });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const secure = await context.newPage();
  await secure.goto(origin);
  await secure.getByLabel("Owner password", { exact: true }).fill(password);
  await secure.getByRole("button", { name: "Sign in", exact: true }).click();
  await secure.getByRole("button", { name: "Sign out", exact: true }).waitFor();
  const cookies = await context.cookies();
  const cookie = cookies.find(
    (c) => c.httpOnly && c.secure && c.sameSite === "Strict",
  );
  assert.ok(
    cookie,
    "private HTTPS must use Secure HttpOnly SameSiteStrict session",
  );
  assert.equal(
    await secure.evaluate(() =>
      sessionStorage.getItem("fased.core.session.v1"),
    ),
    null,
  );
  const result = await secure.evaluate(async () => {
    const auth = await (await fetch("/api/v1/auth/state")).json();
    const current = await (await fetch("/api/v1/setup")).json();
    const response = await fetch("/api/v1/setup", {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": auth.csrfToken,
      },
      body: JSON.stringify({
        revision: current.revision,
        settings: { ...current.settings, host: "vps" },
      }),
    });
    return {
      status: response.status,
      settings: (await response.json()).settings,
    };
  });
  assert.equal(result.status, 200);
  assert.equal(result.settings.host, "vps");
  report.checks.privateHTTPSCookieAndCSRF =
    "PASS: real TLS fixture with explicit test-only certificate trust; private cookie and authenticated setup";
  const forbidden = await context.request.post(origin + "/api/v1/auth/login", {
    headers: { Origin: "https://foreign.example" },
    data: { password },
  });
  assert.equal(forbidden.status(), 403);
  report.checks.foreignOriginRejected = "PASS";
  report.binarySHA256 = createHash("sha256")
    .update(await readFile(binary))
    .digest("hex");
  report.result = "PASS";
} catch (error) {
  report.error = String(error);
  report.sshDiagnostic = sshLog.slice(-2000);
  throw error;
} finally {
  if (browser) await browser.close();
  if (proxy) await new Promise((r) => proxy.close(r));
  for (const p of children.reverse()) {
    if (p.exitCode === null) {
      p.kill("SIGTERM");
      await Promise.race([new Promise((r) => p.once("exit", r)), pause(2000)]);
      if (p.exitCode === null) p.kill("SIGKILL");
    }
  }
  await writeFile(
    ".evidence/private-access-live-result.json",
    JSON.stringify(report, null, 2) + "\n",
  );
  await rm(root, { recursive: true });
}
