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
import urllib.error
import urllib.request

P = pathlib.Path
ASSETS = ["fased-app-linux-amd64.tar.gz", "fased-install-linux-amd64", "fased-app-linux-amd64.provenance.sigstore.json", "install.sh", "fased-release.json"]
PROFILES = {"local": ("fased-local", 1100, 17481), "remote": ("fased-remote", 1200, 17482), "legacy": ("fased-legacy", 1300, 17483), "fresh": ("fased-fresh", 1400, 17484), "agent": ("fased-agent", 1500, 17485), "private": ("ubuntu", 1000, 17482)}
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


def await_tailnet_enrollment(status, begin, announce, *, clock=time.monotonic, pause=time.sleep):
    """Keep the existing node/request alive through the human approval gate."""
    deadline = clock() + 1200
    current = status()
    if current.get('BackendState') == 'Running':
        return current
    if current.get('BackendState') not in ('NeedsLogin', 'NeedsMachineAuth', 'Starting'):
        raise RuntimeError('Tailscale enrollment stopped; preserve the same node')
    if current.get('BackendState') == 'NeedsLogin' and not current.get('AuthURL'):
        begin()  # One bounded CLI request; its timeout does not revoke owner input.
        current = status()
    while clock() < deadline:
        if current.get('BackendState') == 'Running':
            return current
        if current.get('BackendState') not in ('NeedsLogin', 'NeedsMachineAuth', 'Starting'):
            raise RuntimeError('Tailscale enrollment stopped; preserve the same node')
        link = current.get('AuthURL', '')
        if current.get('BackendState') == 'NeedsLogin' and not link:
            raise RuntimeError('Tailscale login produced no pending request; preserve the same node')
        if link:
            if not re.fullmatch(r'https://login\.tailscale\.com/a/[A-Za-z0-9]+', link):
                raise RuntimeError('unexpected Tailscale enrollment URL')
            announce(link)
        pause(min(5, max(0, deadline - clock())))
        current = status()
    raise RuntimeError('Tailscale owner approval pending; preserve the SAME node/request')


class PrivateHTTPSNoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def wait_private_https(origin, *, clock=time.monotonic, pause=time.sleep, opener=None):
    """Warm on-demand TLS before the browser timer; this never approves setup."""
    if not re.fullmatch(r'https://[a-z0-9-]+\.[a-z0-9-]+\.ts\.net', origin):
        raise RuntimeError('validated private origin required')
    client = opener or urllib.request.build_opener(urllib.request.ProxyHandler({}), PrivateHTTPSNoRedirect())
    started = clock();deadline = started + 60;attempts = 0;last = ''
    while clock() < deadline:
        attempts += 1
        try:
            with client.open(origin + '/', timeout=min(5, max(0.1, deadline - clock()))) as response:
                if response.status != 200: raise RuntimeError('private HTTPS returned ' + str(response.status))
                return {'result':'PASS','scope':'TLS readiness only; not browser approval',
                        'attempts':attempts,'elapsedSeconds':round(clock()-started,3)}
        except urllib.error.HTTPError as error:
            error.close()
            raise RuntimeError('private HTTPS refused with status ' + str(error.code)) from error
        except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
            if isinstance(getattr(error, 'reason', error), ssl.SSLCertVerificationError):
                raise RuntimeError('private certificate verification failed') from error
            last = redacted(str(error))
            pause(min(1, max(0, deadline-clock())))
    raise RuntimeError('private HTTPS readiness deadline; preserve enrollment: ' + last)


class RemoteEnrollmentRetry:
    """One resend; owner input uses the existing enclosing wizard deadline."""
    prompt = "Connect Tailscale for private Remote access? [Y/n]: "
    failure = "Connect Tailscale and retry. Fased has not been installed."
    setup_prompt = "Set up Tailscale packages, service, browser sign-in and access for this OS user? [y/N]: "

    def __init__(self, ready, clock=time.monotonic, handoff_pending=lambda: False):
        self.ready, self.clock = ready, clock
        self.handoff_pending, self.waiting_checks = handoff_pending, 0
        self.checks, self.next_check, self.used = 0, 0, False
        self.expires_at, self.pending_ended = None, False
        self.setup_retried = False

    def end_pending(self):
        self.pending_ended = True
        raise RuntimeError("Tailscale enrollment is not ready; no retry sent. Finish enrollment before rerunning fresh acceptance.")

    def __call__(self, text):
        count = text.count(self.prompt)
        if count > 2:
            raise RuntimeError("Remote enrollment prompt repeated more than once; no retry sent")
        if self.used:
            setup_count = text.count(self.setup_prompt)
            if setup_count > 2:
                raise RuntimeError('Remote setup consent repeated more than once; no retry sent')
            if setup_count == 2 and not self.setup_retried and text.rfind(self.setup_prompt) > text.rfind(self.prompt):
                if self.ready():
                    self.setup_retried = True
                    return self.setup_prompt
            return None
        if count != 2:
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

    def begin_branch(self, name):
        order = ('ui', 'cli', 'automatic')
        if name not in order: raise RuntimeError('unknown Remote entry branch')
        previous = self.value.get('replacementBranch')
        if previous and previous['name'] == name:
            return previous['phase']
        index = order.index(name)
        if (index and (not previous or previous['name'] != order[index - 1] or previous['phase'] != 'completed') or
                not index and previous):
            raise RuntimeError('preserve the active Remote branch; no predecessor restore')
        self.value['replacementBranch'] = {'name': name, 'phase': 'preparing'}
        write_private(self.path, self.value)
        return 'preparing'

    def activate_branch(self, name):
        if self.value.get('replacementBranch') != {'name': name, 'phase': 'preparing'}:
            raise RuntimeError('Remote branch is not preparing')
        self.value['replacementBranch']['phase'] = 'active'
        write_private(self.path, self.value)

    def finish_branch(self, name, operation_id):
        branch = self.value.get('replacementBranch', {})
        if (branch.get('name') != name or branch.get('phase') not in ('active', 'completed') or
                not re.fullmatch(r'[a-f0-9]{32}', operation_id) or
                branch.get('operationId', operation_id) != operation_id):
            raise RuntimeError('preserve Remote branch operation identity')
        branch.update(phase='completed', operationId=operation_id)
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
    kwargs.setdefault('timeout', 90)
    return subprocess.run(args, check=True, capture_output=True, **kwargs)


def os_tool(tool, *args):
    preparation = {
        ('/usr/bin/apt-get', 'update', '-qq'),
        ('/usr/bin/apt-get', 'install', '-y', 'tailscale'),
        ('/usr/bin/systemctl', 'enable', '--now', 'tailscaled.service'),
        ('/usr/bin/tailscale', 'set', '--operator=ubuntu'),
    }
    if tool not in {"/usr/bin/install", "/usr/sbin/ip", "/usr/sbin/nft", "/usr/sbin/update-ca-certificates"} and (tool, *args) not in preparation:
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
    try:
        os_tool("/usr/sbin/nft", "-f", str(rules))
    except subprocess.CalledProcessError as error:
        detail = redacted((error.stderr or b"").decode(errors="replace"))[-2048:]
        raise RuntimeError("fixture nft rule failed (" + str(error.returncode) + "): " + (detail or "stderr unavailable")) from error
    process = subprocess.Popen([sys.executable, __file__, "serve", "--work", str(work)], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=(work / "server.log").open("ab"), start_new_session=True, env={"PATH": "/usr/bin:/bin", "HOME": str(P.home()), "FASED_DISPOSABLE_VM": "1"})
    wait_delivery(process)
    write_private(work / "server.json", {"pid": process.pid})


def terminal(args, answers, timeout=180, password_free=False, observer=None, prompt_retry=None, transcript=None):
    pid, fd = pty.fork()
    if pid == 0:
        environment = {"HOME": str(P.home()), "USER": P.home().name, "LOGNAME": P.home().name, "PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "XDG_RUNTIME_DIR": "/run/user/" + str(os.getuid()), "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/" + str(os.getuid()) + "/bus", "LANG": "C.UTF-8"}
        # Preserve sshd/logind's actual connection provenance. The real network
        # controller validates it; a PTY must not turn private SSH into local IO.
        environment.update({key: os.environ[key] for key in ('SSH_CONNECTION', 'SSH_CLIENT', 'SSH_TTY', 'XDG_SESSION_ID') if key in os.environ})
        os.execvpe(args[0], args, environment)
    output = bytearray()
    matched = set()
    retried = set()
    status = None
    deadline = time.monotonic() + timeout
    def retain_transcript():
        if transcript is None: return
        descriptor = os.open(transcript, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, 'w') as stream:
            stream.write(redacted(output.decode(errors='replace')))
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
                retain_transcript()
                text = output.decode(errors="replace")
                if password_free and re.search(r"(?:password|passphrase)[^\r\n]{0,120}:\s*$", text, re.IGNORECASE):
                    raise RuntimeError("Unexpected password prompt; no credential submitted")
                if observer is not None:
                    observer(text)
                if status is None:
                    for prompt, answer in answers.items():
                        if prompt not in matched and prompt in text:
                            os.write(fd, (answer + "\n").encode())
                            matched.add(prompt)
            if status is None and prompt_retry is not None:
                prompt = prompt_retry(output.decode(errors="replace"))
                if prompt is not None:
                    if prompt not in matched or prompt in retried or output.decode(errors="replace").count(prompt) != 2:
                        raise RuntimeError("invalid repeated terminal prompt; no retry sent")
                    os.write(fd, (answers[prompt] + "\n").encode())
                    retried.add(prompt)
            if status is None:
                done, child_status = os.waitpid(pid, os.WNOHANG)
                if done:
                    # Reaping is not PTY EOF: final bytes can still be queued.
                    # Drain under the original deadline, without sending input
                    # or reaping this child twice after its exit is recorded.
                    status = child_status
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
            retain_transcript()
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


def provider_health(port, version, clock=time.monotonic, pause=time.sleep):
    deadline = clock() + 10
    while True:
        try:
            current = health(port)
        except urllib.error.URLError as error:
            if not isinstance(error.reason, ConnectionRefusedError) or clock() >= deadline:
                raise
            pause(0.05)
            continue
        if current.get("version") != version:
            raise RuntimeError("shipping engine version differs")
        return current


def verified_provider_bundle(candidate, target):
    """Extract only the two exact bound members; never replace an attempt."""
    target = P(target)
    target.mkdir(mode=0o700)
    with tarfile.open(P(candidate["directory"]) / ASSETS[0], "r:gz") as archive:
        for name, maximum in [("bundle.json", 65536), ("fased-app", 64 * 1024 * 1024)]:
            entries = [v for v in archive.getmembers() if P(v.name).name == name]
            if len(entries) != 1 or not entries[0].isfile() or ".." in P(entries[0].name).parts or not 0 < entries[0].size <= maximum:
                raise RuntimeError("one bounded original bundle member required")
            raw = archive.extractfile(entries[0]).read()
            (target / name).write_bytes(raw)
    manifest = (target / "bundle.json").read_bytes()
    if digest(manifest) != candidate["packet"]["bundleSha256"]:
        raise RuntimeError("original bundle manifest differs")
    expected = json.loads(manifest)["files"]["fased-app"]["sha256"]
    if digest((target / "fased-app").read_bytes()) != expected:
        raise RuntimeError("unmodified shipping engine required")
    (target / "fased-app").chmod(0o700)
    return expected, manifest


def completed_remote_application(state, version, automatic):
    """Return the real controller receipt; health/no-op cannot stand in for Apply."""
    operation = state.get('operation', {})
    release, receipt = operation.get('release', {}), operation.get('receipt', {})
    if (state.get('installedVersion') != version or operation.get('phase') != 'completed' or
            not re.fullmatch(r'[a-f0-9]{32}', operation.get('id', '')) or
            (operation.get('automatic') is True) != automatic or
            release.get('version') != version or receipt.get('phase') != 'completed' or
            receipt.get('id') != operation['id'] or
            any(receipt.get(k) != release.get(k) for k in ('manifestSha256', 'engineSha256', 'sequence')) or
            any(not re.fullmatch(r'[a-f0-9]{64}', release.get(k, '')) for k in ('manifestSha256', 'engineSha256')) or
            not re.fullmatch(r'[1-9][0-9]*', str(release.get('sequence', '')))):
        raise RuntimeError('actual matching completed application operation required')
    return operation


def remote_metadata():
    """Read only public installation metadata and an opaque password-record hash."""
    manifest = json.loads(run(['sudo', '-n', '/usr/bin/cat', '/var/lib/fased-app-vps-1000/install.json']).stdout)
    if (manifest.get('ownerUid') != 1000 or manifest.get('appUid', 0) in (0, 1000) or
            manifest.get('data') != '/var/lib/fased-app-vps-1000/app-state'):
        raise RuntimeError('fixed isolated Remote installation required')
    # Drop all root privileges before the read-only SQLite observation. Never
    # emit password bytes, sessions, signing material or database copies.
    script = "import sqlite3,hashlib; c=sqlite3.connect('file:/var/lib/fased-app-vps-1000/app-state/core.sqlite?mode=ro',uri=True); r=c.execute('SELECT password_salt,password_hash FROM instance').fetchone(); print(hashlib.sha256(b''.join(bytes(v) for v in r if v is not None)).hexdigest())"
    args = ['sudo', '-n', '/usr/bin/setpriv', '--reuid', str(manifest['appUid']),
            '--regid', str(manifest['appGid']), '--clear-groups', '--no-new-privs',
            '--inh-caps=-all', '--ambient-caps=-all', '--bounding-set=-all',
            '--', '/usr/bin/python3', '-c', script]
    password_record = run(args).stdout.decode().strip()
    service = run(['/usr/bin/systemctl', 'show', 'fased-vps-1000.service',
                   '--property=User,NoNewPrivileges,ProtectSystem,ActiveState,MainPID']).stdout.decode()
    properties = dict(line.split('=', 1) for line in service.splitlines() if '=' in line)
    if (properties.get('NoNewPrivileges') != 'yes' or properties.get('ProtectSystem') != 'strict' or
            properties.get('ActiveState') != 'active' or properties.get('User') not in (str(manifest['appUid']), manifest['appUser'])):
        raise RuntimeError('installed Remote service sandbox is not active')
    if not re.fullmatch(r'[a-f0-9]{64}', password_record):
        raise RuntimeError('opaque original password record observation missing')
    rules = json.loads(run(['sudo', '-n', '/usr/sbin/nft', '--stateless', '--json', 'list', 'ruleset']).stdout)
    engine = run(['/usr/bin/sha256sum', '/var/lib/fased-app-vps-1000/fased-app']).stdout.decode().split()[0]
    if engine != manifest.get('coreSha256'): raise RuntimeError('installed engine differs from protected manifest')
    return {'manifest': manifest, 'engineSha256': engine, 'passwordRecordSha256': password_record,
            'foreignFirewallSha256': foreign_firewall_digest(rules, manifest['id']),
            'service': properties, 'bootId': P('/proc/sys/kernel/random/boot_id').read_text().strip(),
            'serve': json.loads(run(['tailscale', 'serve', 'status', '--json']).stdout),
            'tailnet': json.loads(run(['tailscale', 'status', '--json']).stdout)}


def foreign_firewall_digest(rules, manifest_id):
    """Ignore only this instance's restriction table and volatile rule handles."""
    if not re.fullmatch(r'[a-f0-9]{32}', manifest_id): raise RuntimeError('instance manifest identity required')
    def clean(value):
        if isinstance(value, dict):
            return {k: (clean({a:b for a,b in v.items() if a not in ('packets','bytes')}) if k == 'counter' and isinstance(v, dict) else clean(v)) for k, v in value.items() if k != 'handle'}
        return [clean(v) for v in value] if isinstance(value, list) else value
    declarations, chains = [], {}
    for item in rules['nftables']:
        if 'metainfo' in item: continue
        kind, value = next(iter(item.items()))
        table = value.get('name') if kind == 'table' else value.get('table', '')
        if re.fullmatch(r'fased_vps_1000_'+manifest_id[:12]+r'_[a-f0-9]{32}', table or ''): continue
        if kind == 'rule':
            key = (value['family'], table, value['chain'])
            chains.setdefault(key, []).append(clean(value))  # rule order matters
        else: declarations.append(clean(item))
    canonical = {'objects': sorted(declarations, key=lambda v: json.dumps(v, sort_keys=True)),
                 'chains': [(key, chains[key]) for key in sorted(chains)]}
    return digest(json.dumps(canonical, sort_keys=True, separators=(',', ':')).encode())


def remote_public_probe(prepare=False):
    """Actual new IPv4/IPv6 SSH connections from a non-tailnet namespace."""
    namespace, host, peer = 'fased_staged_probe', 'fstaged-host', 'fstaged-peer'
    if prepare:
        namespaces = run(['/usr/sbin/ip', '-j', 'netns', 'list']).stdout.strip()
        exists = any(row.get('name') == namespace for row in json.loads(namespaces or b'[]'))
        state_path = P.home() / '.cache/fased-staged/state.json'
        state = document(state_path)
        ownership = {'namespace': namespace, 'host': host, 'peer': peer, 'instanceId': state['instanceId']}
        if exists and state.get('publicProbe') != ownership:
            raise RuntimeError('preserve a foreign probe namespace')
        state['publicProbe'] = ownership
        write_private(state_path, state)
        commands = [
            ['netns', 'add', namespace], ['link', 'add', host, 'type', 'veth', 'peer', 'name', peer],
            ['link', 'set', peer, 'netns', namespace],
            ['address', 'add', '198.18.20.1/30', 'dev', host],
            ['-6', 'address', 'add', 'fd00:fa5e:b020::1/64', 'dev', host, 'nodad'],
            ['link', 'set', host, 'up'], ['netns', 'exec', namespace, '/usr/sbin/ip', 'link', 'set', 'lo', 'up'],
            ['netns', 'exec', namespace, '/usr/sbin/ip', 'address', 'add', '198.18.20.2/30', 'dev', peer],
            ['netns', 'exec', namespace, '/usr/sbin/ip', '-6', 'address', 'add', 'fd00:fa5e:b020::2/64', 'dev', peer, 'nodad'],
            ['netns', 'exec', namespace, '/usr/sbin/ip', 'link', 'set', peer, 'up'],
        ]
        if not exists:
            for args in commands: os_tool('/usr/sbin/ip', *args)
    # A missing namespace, failed sudo or down/unconfigured interface must not
    # look like a successful firewall denial.
    peer_state = json.loads(run(['sudo', '-n', '/usr/sbin/ip', 'netns', 'exec', namespace,
                                 '/usr/sbin/ip', '-j', 'address', 'show', 'dev', peer], timeout=5).stdout)
    host_state = json.loads(run(['/usr/sbin/ip', '-j', 'address', 'show', 'dev', host], timeout=5).stdout)
    for state, expected in [(peer_state, {'198.18.20.2', 'fd00:fa5e:b020::2'}),
                            (host_state, {'198.18.20.1', 'fd00:fa5e:b020::1'})]:
        if len(state) != 1 or 'UP' not in state[0].get('flags', []) or not expected.issubset({v['local'] for v in state[0].get('addr_info', [])}):
            raise RuntimeError('dual-stack public probe interfaces are not ready')
    values = {}
    for family, address in [('ipv4', '198.18.20.1'), ('ipv6', 'fd00:fa5e:b020::1')]:
        reply = subprocess.run(['sudo', '-n', '/usr/sbin/ip', 'netns', 'exec', namespace,
                                '/usr/bin/ssh-keyscan', '-4' if family == 'ipv4' else '-6',
                                '-T', '2', '-t', 'ed25519', address], capture_output=True, timeout=5)
        if reply.returncode != 0 and reply.stderr.strip():
            raise RuntimeError('public probe failed before a verifiable network denial: ' + redacted(reply.stderr.decode(errors='replace'))[-256:])
        values[family] = {'exit': reply.returncode, 'reachable': bool(reply.stdout.strip()),
                          'hostKeySha256': digest(reply.stdout) if reply.stdout else None}
    return values


def bind_remote_observation(value, candidate, local_sha, instance_id):
    expected = {**binding(candidate), 'localReceiptSha256': local_sha, 'instanceId': instance_id}
    if any(key in value and value[key] != field for key, field in expected.items()):
        raise RuntimeError('Remote observation binding differs')
    return {**value, **expected}


def read_remote_observation(path, candidate, local_sha, instance_id):
    value = document(path)
    expected = {**binding(candidate), 'localReceiptSha256': local_sha, 'instanceId': instance_id}
    if any(value.get(key) != field for key, field in expected.items()):
        raise RuntimeError('retained Remote observation binding differs')
    return value


def package_engine_sha(candidate):
    with tarfile.open(P(candidate['directory']) / ASSETS[0], 'r:gz') as archive:
        entries = [entry for entry in archive.getmembers() if P(entry.name).name == 'bundle.json']
        if len(entries) != 1 or not entries[0].isfile() or not 0 < entries[0].size <= 65536:
            raise RuntimeError('one bounded original bundle manifest required')
        raw = archive.extractfile(entries[0]).read()
    if digest(raw) != candidate['packet']['bundleSha256']: raise RuntimeError('original bundle differs')
    expected = json.loads(raw)['files']['fased-app']['sha256']
    if not re.fullmatch(r'[a-f0-9]{64}', expected): raise RuntimeError('original engine digest required')
    return expected


def owner(args):
    user, uid, port = PROFILES[args.profile]
    if os.getuid() != uid or P.home() != P("/home") / user:
        raise RuntimeError("fixed ordinary profile owner required")
    state_dir = P.home() / ".cache/fased-staged"
    state_dir.mkdir(mode=0o700, exist_ok=True)
    state = document(state_dir / "state.json") if (state_dir / "state.json").exists() else {}
    if args.profile == 'private' and args.owner_action == 'adopt':
        current = health(port)
        if current['version'] != args.version or state and state['instanceId'] != current['instanceId']:
            raise RuntimeError('preserve the original private instance')
        state.update(instanceId=current['instanceId'], version=current['version'])
        write_private(state_dir / 'state.json', state)
        return {'result': 'PASS', **state}
    if args.owner_action == 'state':
        current = json.loads(run([command_path(), 'update', 'status']).stdout)
        entry = state_dir / 'apply-entry.json'
        if args.profile == 'private' and entry.exists(): current['fixtureCLIEntry'] = document(entry)
        return current
    if args.profile == 'private' and args.owner_action == 'metadata':
        return remote_metadata()
    if args.profile == 'private' and args.owner_action.startswith('network-'):
        action = args.owner_action.removeprefix('network-')
        answers = {'Have you signed in to this instance through its private HTTPS address? [y/N]: ': 'y',
                   'Have you tested your VPS provider console as an independent recovery path? [y/N]: ': 'y'} if action == 'harden' else {}
        code, output = terminal(['/var/lib/fased-app-vps-1000/fased-app', 'vps', action], answers, password_free=True,
                                transcript=state_dir / (args.owner_action + '.log'))
        if code:
            raise RuntimeError('private network operation refused: ' + redacted(output)[-2048:])
        # The CLI prints one JSON document to stdout after its terminal review.
        decoder = json.JSONDecoder()
        for index, character in enumerate(output):
            if character != '{': continue
            try: value, _ = decoder.raw_decode(output[index:])
            except ValueError: continue
            if isinstance(value, dict) and 'phase' in value:
                return value
        raise RuntimeError('network operation did not return its controller status')
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
        current = provider_health(port, candidate["packet"]["productVersion"])
        state = {"instanceId": current["instanceId"], "version": current["version"], "engineSha256": expected, "bundleManifest": manifest.decode(), "installerOwnedProviderMode": True, "sandboxed": probe["sandboxed"], "root": installed["root"], "data": installed["data"], "initialPid": probe["pid"]}
        write_private(state_dir / "state.json", state)
        return {"result": "PASS", **state}
    if args.profile == "agent" and args.owner_action == "provider-inspect":
        if not args.provider_helper: raise RuntimeError("reviewed inspector required")
        provider_health(port, state["version"])
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
        current = provider_health(port, state["version"])
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
        write_private(state_dir / 'apply-entry.json', {'instanceId': before['instanceId'], 'fromVersion': before['version'],
                                                      'targetVersion': args.version, 'argv': ['fased', 'update']})
        code, output = terminal([command_path(), "update"], prompts, timeout=300, password_free=True,
                                transcript=state_dir / 'apply.log')
        (state_dir / "apply.log").write_text(redacted(output))
        current = health(port)
        if code or "Updated" not in output or current["version"] != args.version or current["instanceId"] != before["instanceId"]:
            raise RuntimeError("ordinary installed CLI did not apply the candidate: " + redacted(output)[-8192:])
        state["version"] = current["version"]
        write_private(state_dir / "state.json", state)
        result = {"result": "PASS", "cliOutcome": "applied", "health": current, "passwordPrompt": "Owner password:" in output}
        if args.profile == 'private':
            after = json.loads(run([command_path(), 'update', 'status']).stdout)
            result['operation'] = completed_remote_application(after, args.version, False)
        return result
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


def prepare_remote_tailnet(work):
    """Prepare an actual older-release baseline using the reviewed OS package."""
    key = P(__file__).with_name('tailscale-noble-key.gpg')
    if digest(key.read_bytes()) != '3e03dacf222698c60b8e2f990b809ca1b3e104de127767864284e6c228f1fb39':
        raise RuntimeError('reviewed Tailscale package key differs')
    if not P('/usr/bin/tailscale').exists():
        listing = work / 'tailscale.list'
        listing.write_text('deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/ubuntu noble main\n')
        os_tool('/usr/bin/install', '-m', '0644', str(key), '/usr/share/keyrings/tailscale-archive-keyring.gpg')
        os_tool('/usr/bin/install', '-m', '0644', str(listing), '/etc/apt/sources.list.d/tailscale.list')
        os_tool('/usr/bin/apt-get', 'update', '-qq')
        os_tool('/usr/bin/apt-get', 'install', '-y', 'tailscale')
    os_tool('/usr/bin/systemctl', 'enable', '--now', 'tailscaled.service')
    os_tool('/usr/bin/tailscale', 'set', '--operator=ubuntu')
    return json.loads(run(['tailscale', 'status', '--json']).stdout)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["setup", "network", "serve", "select", "owner", "public-probe", "public-probe-prepare", "tailnet-prepare"])
    parser.add_argument("--work", type=P)
    parser.add_argument("--candidate", type=P)
    parser.add_argument("--predecessor", type=P)
    parser.add_argument("--legacy", type=P)
    parser.add_argument("--selection", choices=["candidate", "predecessor", "legacy"])
    parser.add_argument("--fail-archive-once", action="store_true")
    parser.add_argument("--provider-helper", type=P)
    parser.add_argument("--profile", choices=PROFILES)
    parser.add_argument("--owner-action", choices=["install", "failed-bootstrap", "bootstrap", "open", "login", "apply", "check", "restart", "provider-install", "provider-inspect", "adopt", "state", "metadata", "network-harden", "network-finish", "network-rollback", "network-network"])
    parser.add_argument("--version")
    parser.add_argument("--released-local-only", action="store_true")
    parser.add_argument("--resume", action="store_true")
    args = parser.parse_args()
    vm_guard()
    if args.action == "owner":
        print(json.dumps(owner(args), separators=(",", ":")))
        return
    if args.action in ('public-probe', 'public-probe-prepare'):
        if os.getuid() != 1000: raise RuntimeError('disposable administrator required')
        print(json.dumps(remote_public_probe(args.action == 'public-probe-prepare')))
        return
    if os.getuid() != 1000 or args.work is None:
        raise RuntimeError("fixed VM fixture administrator required")
    if args.action == "tailnet-prepare":
        print(json.dumps(prepare_remote_tailnet(args.work)))
    elif args.action == "setup":
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
