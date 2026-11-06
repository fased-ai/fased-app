#!/usr/bin/env python3
"""Disposable-VM delivery fixture and ordinary-owner staged package journey.

Root executes fixed OS tools only. Original release payloads and Sigstore proofs
are served unchanged. No application endpoint or CA override is introduced.
"""
import argparse
import hashlib
import http.server
import json
import os
import pathlib
import pty
import re
import select
import signal
import ssl
import subprocess
import sys
import threading
import tarfile
import time
import urllib.parse
import urllib.request

P = pathlib.Path
ASSETS = ["fased-app-linux-amd64.tar.gz", "fased-install-linux-amd64", "fased-app-linux-amd64.provenance.sigstore.json", "install.sh", "fased-release.json"]
PROFILES = {"local": ("fased-local", 1100, 17481), "remote": ("fased-remote", 1200, 17482), "legacy": ("fased-legacy", 1300, 17483), "fresh": ("fased-fresh", 1400, 17484), "agent": ("fased-agent", 1500, 17485)}
HOSTS = ["github.com", "api.github.com", "release-assets.githubusercontent.com"]
ADDRESS = "140.82.112.3"
PASSWORD = "disposable-staged-owner-password"
PREPARATION_ANSWERS = {
    "Authorize required Ubuntu preparation with sudo? [y/N]: ": "y",
    "Authorize Ubuntu preparation, isolated service and bounded update maintenance with sudo? [y/N]: ": "y",
}
STAGED_ACCEPTANCE_CONTRACT = json.loads(P(__file__).with_name('staged-acceptance-contract.json').read_text())
LOCAL_REQUIRED_CHECKS = tuple(STAGED_ACCEPTANCE_CONTRACT['local'])


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def unique(pairs):
    value = {}
    for key, child in pairs:
        if key in value:
            raise ValueError("duplicate JSON field")
        value[key] = child
    return value


def document(path):
    return json.loads(P(path).read_text(), object_pairs_hook=unique)


def write_private(path, value):
    target = P(path)
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = target.with_name(target.name + ".pending")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(value, stream, separators=(",", ":"))
        stream.write("\n")
    os.replace(temporary, target)


def redacted(text):
    text = re.sub(r"https://login\.tailscale\.com/a/[^\s]+", "[redacted Tailscale enrollment link]", text, flags=re.IGNORECASE)
    text = re.sub(r"https://[a-z0-9.-]+\.ts\.net/[^\s]*", "[redacted private Tailscale link]", text, flags=re.IGNORECASE)
    return re.sub(r"(#(?:setup|local)=)[^\s]+", r"\1[redacted]", text.replace(PASSWORD, "[redacted]"))


def private_tailnet_origin(status, suffix):
    if (not re.fullmatch(r"[a-z0-9-]+\.ts\.net", suffix or "") or
            status.get("BackendState") != "Running" or
            status.get("Self", {}).get("Online") is not True or
            status.get("MagicDNSSuffix") != suffix):
        raise RuntimeError("Tailscale must be Running and online on the expected tailnet")
    name = status["Self"].get("DNSName", "").rstrip(".")
    if not re.fullmatch(r"[a-z0-9-]+\." + re.escape(suffix), name):
        raise RuntimeError("Tailscale node DNS name does not match the expected tailnet")
    return "https://" + name


class RemoteEnrollmentRetry:
    """One resend; owner input uses the existing enclosing wizard deadline."""
    prompt = "Connect Tailscale for private Remote access? [Y/n]: "
    failure = "Connect Tailscale and retry. Fased has not been installed."

    def __init__(self, ready, clock=time.monotonic, handoff_pending=lambda: False):
        self.ready, self.clock = ready, clock
        self.handoff_pending, self.waiting_checks = handoff_pending, 0
        self.checks, self.next_check, self.used = 0, 0, False
        self.expires_at, self.pending_ended = None, False

    def end_pending(self):
        self.pending_ended = True
        raise RuntimeError("Tailscale enrollment is not ready; no retry sent. Finish enrollment before rerunning fresh acceptance.")

    def __call__(self, text):
        count = text.count(self.prompt)
        if count > 2:
            raise RuntimeError("Remote enrollment prompt repeated more than once; no retry sent")
        if count != 2 or self.used:
            return None
        _, remainder = text.split(self.prompt, 1)
        if self.failure not in remainder.split(self.prompt, 1)[0]:
            raise RuntimeError("unrecognized Remote enrollment failure; no retry sent")
        now = self.clock()
        # Human sign-in is a separate gate, not twelve failed network probes.
        # The enclosing terminal's existing 1200s deadline remains unchanged.
        if self.expires_at is None and self.handoff_pending():
            if now < self.next_check:
                return None
            self.waiting_checks += 1
            self.next_check = now + 5
            if self.ready():
                self.used = True
                return self.prompt
            return None
        if self.expires_at is None:
            self.expires_at = now + 60
        if self.pending_ended or self.checks >= 12 or now >= self.expires_at:
            self.end_pending()
        if now < self.next_check:
            return None
        self.checks += 1
        self.next_check = now + 5
        ready = self.ready()
        if self.clock() >= self.expires_at:
            self.end_pending()
        if ready:
            self.used = True
            return self.prompt
        if self.checks >= 12:
            self.end_pending()
        return None


def checked_reply(result, owner, action, failure_file):
    if result.returncode:
        error = redacted((result.stderr or b"").decode(errors="replace"))[-8192:]
        write_private(failure_file, {"owner": owner, "action": action, "returncode": result.returncode, "stderr": error})
        raise RuntimeError("staged " + owner + "/" + action + " failed (" + str(result.returncode) + "): " + error[-1024:])
    text = result.stdout.decode()
    return json.loads(text) if text.strip().startswith("{") else text


def package(directory):
    directory = P(directory).resolve(strict=True)
    raw = (directory / "fased-release.json").read_bytes()
    packet = json.loads(raw, object_pairs_hook=unique)
    if packet.get("version") not in (1, 2) or not re.fullmatch(r"[a-f0-9]{40}", packet.get("source", "")):
        raise ValueError("original attested package required")
    identities = [packet["archive"], packet["installer"], packet["release"]["provenance"], packet["entry"]]
    assets = []
    for name, identity in zip(ASSETS[:4], identities):
        path = directory / name
        if path.is_symlink() or not path.is_file() or identity["file"] != name:
            raise ValueError("regular exact package asset required")
        size = path.stat().st_size
        if not 0 < size <= 128 * 1024 * 1024:
            raise ValueError("bounded package asset required")
        if identity["size"] != size or identity["sha256"] != digest(path.read_bytes()):
            raise ValueError("original package asset changed")
        assets.append({"name": name, "size": size, "sha256": identity["sha256"]})
    assets.append({"name": ASSETS[4], "size": len(raw), "sha256": digest(raw)})
    return {"directory": str(directory), "packet": packet, "assets": assets}


class RemoteJourney:
    """Private resume journal, not authenticated publication evidence.

    Never restore an earlier disk snapshot or revoke enrollment between phases.
    A different package, owner instance or Tailscale node needs explicit review.
    """
    phases = ('new', 'enrolled', 'private-browser', 'installed', 'owner',
              'private-admin', 'console', 'restricted', 'disconnected',
              'rebooted', 'current')

    def __init__(self, path, identity, local_sha):
        self.path = P(path)
        if self.path.exists() or self.path.is_symlink():
            info = self.path.lstat()
            if self.path.is_symlink() or not self.path.is_file() or info.st_uid != os.getuid() or info.st_mode & 0o777 != 0o600 or info.st_size > 65536:
                raise RuntimeError('Remote journal must be a private owned file')
            self.value = document(self.path)
            if self.value.get('schemaVersion') != 1 or self.value.get('binding') != identity:
                raise RuntimeError('Remote journal belongs to a different package')
            if self.value.get('localAcceptanceSha256') != local_sha:
                raise RuntimeError('Remote journal Local receipt differs')
            if self.value.get('phase') not in self.phases:
                raise RuntimeError('Unknown Remote journal phase')
        else:
            self.value = {'schemaVersion': 1, 'binding': identity,
                          'localAcceptanceSha256': local_sha, 'phase': 'new'}
            write_private(self.path, self.value)

    def enrolled(self, status, suffix):
        origin = private_tailnet_origin(status, suffix)
        node = status.get('Self', {}).get('ID', '')
        if not isinstance(node, str) or not node or len(node) > 256:
            raise RuntimeError('Tailscale stable node identity required')
        fingerprint = digest(node.encode())
        if self.value.get('origin', origin) != origin or self.value.get('nodeIdentitySha256', fingerprint) != fingerprint:
            raise RuntimeError('Remote Tailscale identity changed; preserve the journal')
        self.value.update(origin=origin, nodeIdentitySha256=fingerprint)
        if self.value['phase'] == 'new':
            self.advance('enrolled')

    def advance(self, phase, *, instance_id=None):
        if phase not in self.phases:
            raise RuntimeError('Unknown Remote phase')
        current = self.phases.index(self.value['phase'])
        proposed = self.phases.index(phase)
        if proposed > current + 1:
            raise RuntimeError('Remote phase cannot skip required evidence')
        if instance_id is not None:
            if not re.fullmatch(r'[A-Za-z0-9_-]{43}', instance_id):
                raise RuntimeError('Remote owner instance identity required')
            if self.value.get('instanceId', instance_id) != instance_id:
                raise RuntimeError('Remote owner instance changed')
            self.value['instanceId'] = instance_id
        # Rechecking an earlier predicate cannot rewind the recovery checkpoint.
        if proposed >= current:
            self.value['phase'] = phase
        write_private(self.path, self.value)


def binding(candidate):
    packet = candidate["packet"]
    assets = json.dumps(candidate["assets"], separators=(",", ":"))
    return {"schemaVersion": 1, "source": packet["source"], "sourceTree": packet["release"]["sourceTree"], "productVersion": packet["productVersion"], "runId": packet["release"]["runId"], "runAttempt": packet["release"]["runAttempt"], "platform": "ubuntu-24.04-amd64", "artifactSetSha256": digest(assets.encode())}


def require_local_acceptance(candidate, path):
    receipt = document(path)
    if (any(receipt.get(key) != value for key, value in binding(candidate).items()) or
        receipt.get("result") != "PASS" or receipt.get("localOnly") is not True or
        receipt.get("upgradeApplied") is not True or
        any(receipt.get(name) != "applied" for name in ("uiOutcome", "cliOutcome", "automaticOutcome")) or
        receipt.get("automaticPolicyRestored") != "manual" or
        any(receipt.get(name) is not False for name in ("mining", "liveFeatures", "automaticUpdates")) or
        any(receipt.get("checks", {}).get(name) != "PASS" for name in LOCAL_REQUIRED_CHECKS)):
        raise RuntimeError("matching completed Local acceptance required before fresh Remote")
    return digest(P(path).read_bytes())


def vm_guard():
    if os.geteuid() == 0 or os.environ.get("FASED_DISPOSABLE_VM") != "1":
        raise RuntimeError("ordinary-user disposable VM authority required")
    product = P("/sys/class/dmi/id/product_name").read_text().strip()
    if product not in ("Standard PC (Q35 + ICH9, 2009)", "Standard PC (i440FX + PIIX, 1996)"):
        raise RuntimeError("guarded disposable QEMU guest required")


def run(args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, timeout=90, **kwargs)


def os_tool(tool, *args):
    if tool not in {"/usr/bin/install", "/usr/sbin/ip", "/usr/sbin/nft", "/usr/sbin/update-ca-certificates"}:
        raise RuntimeError("fixed guest OS tool required")
    return run(["/usr/bin/sudo", "-n", "--", tool, *args])


class Fixture:
    def __init__(self, work):
        self.work = P(work)
        config = document(self.work / "fixture.json")
        self.packages = {key: package(config[key]) for key in ("candidate", "predecessor", "legacy")}
        self.lock = threading.Lock()

    def selected(self):
        return document(self.work / "selection.json")["release"]

    def release(self, key):
        value = self.packages[key]
        packet = value["packet"]
        release_id = {"candidate": 2002, "predecessor": 1001, "legacy": 3003}[key]
        return {"id": release_id, "tag_name": "v" + packet["productVersion"], "draft": False, "prerelease": False, "immutable": True, "assets": [{"id": release_id * 10 + index, "name": item["name"], "size": item["size"], "state": "uploaded", "digest": "sha256:" + item["sha256"]} for index, item in enumerate(value["assets"])]}

    def response(self, path):
        path = urllib.parse.urlsplit(path).path
        if path == "/repos/fased-ai/fased-app":
            return 200, {"id": 1426211857, "full_name": "fased-ai/fased-app", "owner": {"id": 279582504}}
        if path == "/repos/fased-ai/fased-app/releases/latest":
            return 200, self.release(self.selected())
        for key, value in self.packages.items():
            packet = value["packet"]
            release = self.release(key)
            tag = "v" + packet["productVersion"]
            tag_object = packet["release"]["tagObject"] or hashlib.sha256(("staged-" + key).encode()).hexdigest()[:40]
            if path in ("/repos/fased-ai/fased-app/releases/tags/" + tag, "/repos/fased-ai/fased-app/releases/" + str(release["id"])):
                return 200, release
            if path == "/repos/fased-ai/fased-app/git/ref/tags/" + tag:
                return 200, {"ref": "refs/tags/" + tag, "object": {"type": "tag", "sha": tag_object}}
            if path == "/repos/fased-ai/fased-app/git/tags/" + tag_object:
                return 200, {"sha": tag_object, "tag": tag, "object": {"type": "commit", "sha": packet["source"]}}
            for asset in release["assets"]:
                if path == "/repos/fased-ai/fased-app/releases/assets/" + str(asset["id"]):
                    return 302, "https://release-assets.githubusercontent.com/github-production-release-asset/1426211857/" + str(asset["id"]) + "/" + asset["name"]
                public_path = "/fased-ai/fased-app/releases/download/" + tag + "/" + asset["name"]
                cdn_path = "/github-production-release-asset/1426211857/" + str(asset["id"]) + "/" + asset["name"]
                latest = "/fased-ai/fased-app/releases/latest/download/" + asset["name"]
                if path in (public_path, cdn_path) or path == latest and key == self.selected():
                    with self.lock:
                        control = document(self.work / "selection.json")
                        if key == "candidate" and asset["name"].endswith(".tar.gz") and control.get("failArchiveOnce"):
                            control["failArchiveOnce"] = False
                            control["archiveFailureObserved"] = True
                            write_private(self.work / "selection.json", control)
                            return 503, {"fixture": "one explicit interrupted acquisition"}
                    return 200, (P(value["directory"]) / asset["name"]).read_bytes()
        return 404, {"message": "unconfigured fixture request"}

    def serve(self):
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                status, body = fixture.response(self.path)
                self.send_response(status)
                if status == 302:
                    self.send_header("Location", body)
                    self.end_headers()
                    return
                raw = body if isinstance(body, bytes) else json.dumps(body, separators=(",", ":")).encode()
                self.send_header("Content-Type", "application/octet-stream" if isinstance(body, bytes) else "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def log_message(self, *_):
                pass

        server = http.server.ThreadingHTTPServer(("0.0.0.0", 8443), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(self.work / "tls.crt", self.work / "tls.key")
        server.socket = context.wrap_socket(server.socket, server_side=True)
        server.serve_forever()


def wait_delivery(process, timeout=15):
    # Starting the child does not mean its TLS listener is ready. Reboot and
    # snapshot restore must prove the actual acquisition route before Update.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("disposable delivery server exited; inspect server.log")
        try:
            with opener.open("https://api.github.com/repos/fased-ai/fased-app", timeout=1) as response:
                value = json.load(response)
            if value.get("id") == 1426211857 and value.get("full_name") == "fased-ai/fased-app":
                return
        except (OSError, ValueError):
            pass
        time.sleep(0.1)
    raise RuntimeError("disposable HTTPS acquisition route did not become ready")


def network(work):
    work = P(work)
    cert = work / "tls.crt"
    if not cert.exists():
        run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2", "-subj", "/CN=Fased disposable delivery fixture", "-addext", "subjectAltName=" + ",".join("DNS:" + host for host in HOSTS), "-keyout", str(work / "tls.key"), "-out", str(cert)])
        (work / "tls.key").chmod(0o600)
    original = P("/etc/hosts").read_text()
    suffix = "\n" + ADDRESS + " " + " ".join(HOSTS) + " # Fased disposable delivery\n"
    hosts = work / "hosts"
    hosts.write_text(original if "# Fased disposable delivery" in original else original + suffix)
    os_tool("/usr/bin/install", "-m", "0644", str(hosts), "/etc/hosts")
    os_tool("/usr/bin/install", "-m", "0644", str(cert), "/usr/local/share/ca-certificates/fased-staged-fixture.crt")
    os_tool("/usr/sbin/update-ca-certificates")
    addresses = json.loads(run(["ip", "-j", "address", "show", "dev", "lo"]).stdout)
    if not any(row.get("local") == ADDRESS for interface in addresses for row in interface.get("addr_info", [])):
        os_tool("/usr/sbin/ip", "address", "add", ADDRESS + "/32", "dev", "lo")
    existing = subprocess.run(["sudo", "-n", "/usr/sbin/nft", "list", "table", "ip", "fased_staged_delivery"], capture_output=True, timeout=10)
    if existing.returncode == 0:
        os_tool("/usr/sbin/nft", "delete", "table", "ip", "fased_staged_delivery")
    rules = work / "delivery.nft"
    rules.write_text("table ip fased_staged_delivery {\n chain output { type nat hook output priority -100; policy accept; ip daddr " + ADDRESS + " tcp dport 443 redirect to :8443; }\n}\n")
    os_tool("/usr/sbin/nft", "-f", str(rules))
    process = subprocess.Popen([sys.executable, __file__, "serve", "--work", str(work)], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=(work / "server.log").open("ab"), start_new_session=True, env={"PATH": "/usr/bin:/bin", "HOME": str(P.home()), "FASED_DISPOSABLE_VM": "1"})
    wait_delivery(process)
    write_private(work / "server.json", {"pid": process.pid})


def terminal(args, answers, timeout=180, password_free=False, observer=None, prompt_retry=None, transcript=None):
    pid, fd = pty.fork()
    if pid == 0:
        os.execvpe(args[0], args, {"HOME": str(P.home()), "USER": P.home().name, "LOGNAME": P.home().name, "PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "XDG_RUNTIME_DIR": "/run/user/" + str(os.getuid()), "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/" + str(os.getuid()) + "/bus", "LANG": "C.UTF-8"})
    output = bytearray()
    matched = set()
    retried = set()
    status = None
    deadline = time.monotonic() + timeout
    try:
        while time.monotonic() < deadline:
            if select.select([fd], [], [], 0.2)[0]:
                try:
                    chunk = os.read(fd, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if len(output) > 2 * 1024 * 1024:
                    raise RuntimeError("bounded terminal output exceeded")
                text = output.decode(errors="replace")
                if password_free and re.search(r"(?:password|passphrase)[^\r\n]{0,120}:\s*$", text, re.IGNORECASE):
                    raise RuntimeError("Unexpected password prompt; no credential submitted")
                if observer is not None:
                    observer(text)
                for prompt, answer in answers.items():
                    if prompt not in matched and prompt in text:
                        os.write(fd, (answer + "\n").encode())
                        matched.add(prompt)
            if prompt_retry is not None:
                prompt = prompt_retry(output.decode(errors="replace"))
                if prompt is not None:
                    if prompt not in matched or prompt in retried or output.decode(errors="replace").count(prompt) != 2:
                        raise RuntimeError("invalid repeated terminal prompt; no retry sent")
                    os.write(fd, (answers[prompt] + "\n").encode())
                    retried.add(prompt)
            done, child_status = os.waitpid(pid, os.WNOHANG)
            if done:
                status = child_status
                break
        else:
            raise TimeoutError("packaged terminal journey deadline: " + redacted(output.decode(errors="replace"))[-8192:])
    finally:
        try:
            os.close(fd)
            if status is None:
                done, status = os.waitpid(pid, os.WNOHANG)
                if not done:
                    os.kill(pid, signal.SIGKILL)
                    _, status = os.waitpid(pid, 0)
        finally:
            if transcript is not None:
                descriptor = os.open(transcript, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
                os.fchmod(descriptor, 0o600)
                with os.fdopen(descriptor, "w") as stream:
                    stream.write(redacted(output.decode(errors="replace")))
    text = output.decode(errors="replace")
    return os.waitstatus_to_exitcode(status), text


def command_path():
    for name in ("fased", "fased-app"):
        path = P.home() / ".local/bin" / name
        if path.is_file() and not path.is_symlink():
            return str(path)
    raise RuntimeError("installed ordinary command missing")


def browser_link(output):
    links = list(dict.fromkeys(re.findall(r"https?://[^\s]+", output)))
    setup = [link for link in links if urllib.parse.urlsplit(link).fragment.startswith("setup=")]
    identified = [link for link in links if "instance" in urllib.parse.parse_qs(urllib.parse.urlsplit(link).query)]
    preferred = setup or identified or links
    if len(preferred) != 1:
        raise RuntimeError("ambiguous or missing private browser handoff")
    return preferred[0], len(links)


def health(port):
    with urllib.request.urlopen("http://127.0.0.1:" + str(port) + "/api/v1/health", timeout=5) as response:
        result = json.load(response)
    capabilities = result.get("capabilities", {})
    if any(capabilities.get(name) is not False for name in ("aiConnections", "financialExecution", "walletSigner")):
        raise RuntimeError("held live capabilities enabled")
    return result


def verified_provider_bundle(candidate, target):
    """Extract only the two exact bound members; never replace an attempt."""
    target = P(target)
    target.mkdir(mode=0o700)
    with tarfile.open(P(candidate["directory"]) / ASSETS[0], "r:gz") as archive:
        for name, maximum in [("manifest.json", 65536), ("fased-app", 64 * 1024 * 1024)]:
            entries = [v for v in archive.getmembers() if P(v.name).name == name]
            if len(entries) != 1 or not entries[0].isfile() or ".." in P(entries[0].name).parts or not 0 < entries[0].size <= maximum:
                raise RuntimeError("one bounded original bundle member required")
            raw = archive.extractfile(entries[0]).read()
            (target / name).write_bytes(raw)
    manifest = (target / "manifest.json").read_bytes()
    if digest(manifest) != candidate["packet"]["bundleSha256"]:
        raise RuntimeError("original bundle manifest differs")
    expected = json.loads(manifest)["files"]["fased-app"]["sha256"]
    if digest((target / "fased-app").read_bytes()) != expected:
        raise RuntimeError("unmodified shipping engine required")
    (target / "fased-app").chmod(0o700)
    return expected, manifest


def owner(args):
    user, uid, port = PROFILES[args.profile]
    if os.getuid() != uid or P.home() != P("/home") / user:
        raise RuntimeError("fixed ordinary profile owner required")
    state_dir = P.home() / ".cache/fased-staged"
    state_dir.mkdir(mode=0o700, exist_ok=True)
    state = document(state_dir / "state.json") if (state_dir / "state.json").exists() else {}
    prompts = {"Private access [": "ssh", "Use a dedicated app account": "y", "Keep Fased running": "y", "Start Fased at login?": "y", "Owner password: ": PASSWORD, **PREPARATION_ANSWERS}
    if args.version and args.owner_action in ("apply", "bootstrap", "failed-bootstrap"):
        prompts["Update to " + args.version + "? [y/N]: "] = "y"
    if args.owner_action == "provider-install":
        if args.profile != "agent" or not args.provider_helper or not args.candidate:
            raise RuntimeError("dedicated agent profile and reviewed helper required")
        candidate = package(args.candidate)
        target = state_dir / "verified-provider-package"
        expected, manifest = verified_provider_bundle(candidate, target)
        env = {**os.environ, "XDG_RUNTIME_DIR": "/run/user/1500", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1500/bus"}
        # Native desktop protection, in this disposable profile only. No store
        # substitute or plaintext wrapping-key fallback enters the application.
        run(["/usr/bin/gnome-keyring-daemon", "--unlock"], input=b"disposable-installed-native-keyring\n", env=env)
        installed = run([str(args.provider_helper), "installed-provider", "--disposable-fixture", "--engine", str(target / "fased-app"), "--engine-sha256", expected], env=env)
        installed = json.loads(installed.stdout)
        if installed.get("providerFixture") is not True or installed["files"]["fased-app"] != expected:
            raise RuntimeError("installer-owned mode/hash missing")
        probe = json.loads(run([str(args.provider_helper), "installed-provider", "--inspect"], env=env).stdout)
        if probe.get("sandboxed") is not True or probe["engineSha256"] != expected: raise RuntimeError("installed sandbox/engine observation missing")
        current = health(port)
        if current["version"] != candidate["packet"]["productVersion"]:
            raise RuntimeError("shipping engine version differs")
        state = {"instanceId": current["instanceId"], "version": current["version"], "engineSha256": expected, "bundleManifest": manifest.decode(), "installerOwnedProviderMode": True, "sandboxed": probe["sandboxed"], "root": installed["root"], "data": installed["data"], "initialPid": probe["pid"]}
        write_private(state_dir / "state.json", state)
        return {"result": "PASS", **state}
    if args.profile == "agent" and args.owner_action == "provider-inspect":
        if not args.provider_helper: raise RuntimeError("reviewed inspector required")
        probe=json.loads(run([str(args.provider_helper),"installed-provider","--inspect"]).stdout)
        if probe.get("sandboxed") is not True or probe["engineSha256"] != state["engineSha256"]:
            raise RuntimeError("installed sandbox or engine changed")
        return {"result":"PASS",**probe}
    if args.profile == "agent" and args.owner_action in ("open", "restart"):
        engine = str(P(state["root"]) / "fased-app")
        scope = ["--prefix", state["root"], "--data-dir", state["data"]]
        if args.owner_action == "open":
            code, output = terminal([engine, "open", *scope, "--print"], {})
            if code: raise RuntimeError("installed Local owner open refused: " + redacted(output)[-1024:])
            url, count = browser_link(output)
            return {"result":"PASS","url":url,"urlCount":count}
        before = health(port)
        for action in ("stop", "start"):
            code, _ = terminal([engine, "service", action, *scope], {})
            if code: raise RuntimeError("installed Local service restart refused")
        current = health(port)
        if current["instanceId"] != before["instanceId"]: raise RuntimeError("restart changed owner identity")
        return {"result":"PASS","instanceId":current["instanceId"]}
    if args.owner_action == "open":
        code, output = terminal([command_path(), "open", *(["--print"] if args.profile in ("local", "fresh", "agent") else [])], {})
        if code:
            raise RuntimeError("ordinary private browser handoff missing: " + redacted(output)[-8192:])
        url, count = browser_link(output)
        return {"result": "PASS", "url": url, "urlCount": count}
    if args.owner_action in ("install", "bootstrap", "failed-bootstrap"):
        version = args.version
        install_profile = "local" if args.profile in ("local", "fresh", "agent") else "remote"
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version or ""):
            raise RuntimeError("fixed verified release version required")
        url = "https://github.com/fased-ai/fased-app/releases/download/v" + version + "/install.sh"
        shell = 'bash <(curl -fsSL "$1") --' + install_profile + ' --listen 127.0.0.1:' + str(port)
        if args.owner_action != "install":
            shell += " --update"
        result, output = terminal(["bash", "-c", shell, "staged-entry", url], prompts)
        scrubbed = redacted(output)
        (state_dir / (args.owner_action + ".log")).write_text(scrubbed)
        if args.owner_action == "failed-bootstrap":
            if result == 0:
                raise RuntimeError("injected acquisition failure did not fail: " + scrubbed[-8192:])
            current = health(port)
            if current["instanceId"] != state["instanceId"] or current["version"] != state["version"]:
                raise RuntimeError("failed acquisition changed the running instance")
            return {"result": "PASS", "recovery": "failed acquisition preserved the original running version", "returncode": result, "failure": scrubbed[-8192:]}
        if result != 0:
            raise RuntimeError("packaged " + args.owner_action + " failed: " + redacted(output)[-8192:])
        current = health(port)
        if args.owner_action == "install":
            state = {"instanceId": current["instanceId"], "version": current["version"]}
        elif current["instanceId"] != state["instanceId"] or current["version"] != version:
            raise RuntimeError("bootstrap did not preserve the instance and install candidate")
        state["version"] = current["version"]
        write_private(state_dir / "state.json", state)
        code, opened = terminal([command_path(), "open", *(["--print"] if args.profile in ("local", "fresh", "agent") else [])], {})
        if code:
            raise RuntimeError("normal private browser handoff missing: " + redacted(opened)[-8192:])
        url, count = browser_link(opened)
        return {"result": "PASS", "url": url, "urlCount": count, "health": current}
    if args.owner_action == "login":
        code, output = terminal([command_path(), "login"], {"Owner password: ": PASSWORD})
        confirmation = "CLI authenticated" if args.profile in ("local", "fresh", "agent") else "Signed in"
        if code or confirmation not in output:
            raise RuntimeError("ordinary CLI owner sign-in failed: " + redacted(output)[-8192:])
        return {"result": "PASS"}
    if args.owner_action == "apply":
        before = health(port)
        if before["version"] == args.version:
            raise RuntimeError("apply requires an older running reader")
        code, output = terminal([command_path(), "update"], prompts, timeout=300, password_free=True)
        (state_dir / "apply.log").write_text(redacted(output))
        current = health(port)
        if code or "Updated" not in output or current["version"] != args.version or current["instanceId"] != before["instanceId"]:
            raise RuntimeError("ordinary installed CLI did not apply the candidate: " + redacted(output)[-8192:])
        state["version"] = current["version"]
        write_private(state_dir / "state.json", state)
        return {"result": "PASS", "cliOutcome": "applied", "health": current, "passwordPrompt": "Owner password:" in output}
    if args.owner_action == "check":
        code, output = terminal([command_path(), "update"], prompts, password_free=True)
        if code or "Already current" not in output:
            raise RuntimeError("ordinary installed CLI Update/no-op failed")
        return {"result": "PASS", "cliOutcome": "alreadyCurrent", "health": health(port), "passwordPrompt": "Owner password:" in output}
    if args.owner_action == "restart":
        previous = health(port)
        for action in ("stop", "start"):
            code, _ = terminal([command_path(), action], {})
            if code:
                raise RuntimeError("installed service " + action + " failed")
        current = health(port)
        if current["instanceId"] != previous["instanceId"] or current["version"] != previous["version"]:
            raise RuntimeError("installed restart changed identity/version")
        return {"result": "PASS", "instanceId": current["instanceId"]}
    raise RuntimeError("unknown owner journey action")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["setup", "network", "serve", "select", "owner"])
    parser.add_argument("--work", type=P)
    parser.add_argument("--candidate", type=P)
    parser.add_argument("--predecessor", type=P)
    parser.add_argument("--legacy", type=P)
    parser.add_argument("--selection", choices=["candidate", "predecessor", "legacy"])
    parser.add_argument("--fail-archive-once", action="store_true")
    parser.add_argument("--provider-helper", type=P)
    parser.add_argument("--profile", choices=PROFILES)
    parser.add_argument("--owner-action", choices=["install", "failed-bootstrap", "bootstrap", "open", "login", "apply", "check", "restart", "provider-install", "provider-inspect"])
    parser.add_argument("--version")
    parser.add_argument("--released-local-only", action="store_true")
    parser.add_argument("--resume", action="store_true")
    args = parser.parse_args()
    vm_guard()
    if args.action == "owner":
        print(json.dumps(owner(args), separators=(",", ":")))
        return
    if os.getuid() != 1000 or args.work is None:
        raise RuntimeError("fixed VM fixture administrator required")
    if args.action == "setup":
        candidate, predecessor, legacy = package(args.candidate), package(args.predecessor), package(args.legacy)
        if candidate["packet"]["version"] != 2 or predecessor["packet"]["version"] != 2 or (not args.released_local_only and predecessor["packet"]["productVersion"] != "0.1.4") or (not args.released_local_only and legacy["packet"]["version"] != 1):
            raise RuntimeError("target, attested compatibility reader and original legacy bridge inputs required")
        identity = {"candidate": str(args.candidate), "predecessor": str(args.predecessor), "legacy": str(args.legacy)}
        if args.resume:
            if document(args.work / "fixture.json") != identity:
                raise RuntimeError("retained delivery belongs to different package paths")
        else:
            args.work.mkdir(mode=0o700, parents=True)
            write_private(args.work / "fixture.json", identity)
            write_private(args.work / "selection.json", {"release": "predecessor"})
        network(args.work)
    elif args.action == "network":
        network(args.work)
    elif args.action == "serve":
        Fixture(args.work).serve()
    elif args.action == "select":
        write_private(args.work / "selection.json", {"release": args.selection, "failArchiveOnce": args.fail_archive_once})


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("Staged package acceptance blocked: " + str(error), file=sys.stderr)
        raise SystemExit(1)
