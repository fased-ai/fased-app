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
import time
import urllib.parse
import urllib.request

P = pathlib.Path
ASSETS = ["fased-app-linux-amd64.tar.gz", "fased-install-linux-amd64", "fased-app-linux-amd64.provenance.sigstore.json", "install.sh", "fased-release.json"]
PROFILES = {"local": ("fased-local", 1100, 17481), "remote": ("fased-remote", 1200, 17482), "legacy": ("fased-legacy", 1300, 17483)}
HOSTS = ["github.com", "api.github.com", "release-assets.githubusercontent.com"]
ADDRESS = "140.82.112.3"
PASSWORD = "disposable-staged-owner-password"


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


def binding(candidate):
    packet = candidate["packet"]
    assets = json.dumps(candidate["assets"], separators=(",", ":"))
    return {"schemaVersion": 1, "source": packet["source"], "sourceTree": packet["release"]["sourceTree"], "productVersion": packet["productVersion"], "runId": packet["release"]["runId"], "runAttempt": packet["release"]["runAttempt"], "platform": "ubuntu-24.04-amd64", "artifactSetSha256": digest(assets.encode())}


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
    write_private(work / "server.json", {"pid": process.pid})


def terminal(args, answers, timeout=180):
    pid, fd = pty.fork()
    if pid == 0:
        os.execvpe(args[0], args, {"HOME": str(P.home()), "USER": P.home().name, "LOGNAME": P.home().name, "PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "XDG_RUNTIME_DIR": "/run/user/" + str(os.getuid()), "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/" + str(os.getuid()) + "/bus", "LANG": "C.UTF-8"})
    output = bytearray()
    matched = set()
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
                for prompt, answer in answers.items():
                    if prompt not in matched and prompt in text:
                        os.write(fd, (answer + "\n").encode())
                        matched.add(prompt)
            done, child_status = os.waitpid(pid, os.WNOHANG)
            if done:
                status = child_status
                break
        else:
            raise TimeoutError("packaged terminal journey deadline")
    finally:
        os.close(fd)
        if status is None:
            done, status = os.waitpid(pid, os.WNOHANG)
            if not done:
                os.kill(pid, signal.SIGKILL)
                _, status = os.waitpid(pid, 0)
    text = output.decode(errors="replace")
    return os.waitstatus_to_exitcode(status), text


def command_path():
    for name in ("fased", "fased-app"):
        path = P.home() / ".local/bin" / name
        if path.is_file() and not path.is_symlink():
            return str(path)
    raise RuntimeError("installed ordinary command missing")


def health(port):
    with urllib.request.urlopen("http://127.0.0.1:" + str(port) + "/api/v1/health", timeout=5) as response:
        result = json.load(response)
    capabilities = result.get("capabilities", {})
    if any(capabilities.get(name) is not False for name in ("aiConnections", "financialExecution", "walletSigner")):
        raise RuntimeError("held live capabilities enabled")
    return result


def owner(args):
    user, uid, port = PROFILES[args.profile]
    if os.getuid() != uid or P.home() != P("/home") / user:
        raise RuntimeError("fixed ordinary profile owner required")
    state_dir = P.home() / ".cache/fased-staged"
    state_dir.mkdir(mode=0o700, exist_ok=True)
    state = document(state_dir / "state.json") if (state_dir / "state.json").exists() else {}
    prompts = {"Private access [": "ssh", "Use a dedicated app account": "y", "Keep Fased running": "y", "Start Fased at login?": "y", "Prepare Ubuntu": "y", "Owner password: ": PASSWORD}
    if args.owner_action == "open":
        code, output = terminal([command_path(), "open", "--print"], {})
        links = re.findall(r"https?://[^\s]+", output)
        if code or not links:
            raise RuntimeError("ordinary private browser handoff missing")
        return {"result": "PASS", "url": links[-1]}
    if args.owner_action in ("install", "bootstrap", "failed-bootstrap"):
        version = args.version
        install_profile = "remote" if args.profile == "legacy" else args.profile
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version or ""):
            raise RuntimeError("fixed verified release version required")
        url = "https://github.com/fased-ai/fased-app/releases/download/v" + version + "/install.sh"
        shell = 'bash <(curl -fsSL "$1") --' + install_profile + ' --listen 127.0.0.1:' + str(port)
        if args.owner_action != "install":
            shell += " --update"
        result, output = terminal(["bash", "-c", shell, "staged-entry", url], prompts)
        scrubbed = re.sub(r"#setup=[^\s]+", "#setup=[redacted]", output).replace(PASSWORD, "[redacted]")
        (state_dir / (args.owner_action + ".log")).write_text(scrubbed)
        if args.owner_action == "failed-bootstrap":
            if result == 0:
                raise RuntimeError("injected acquisition failure did not fail")
            current = health(port)
            if current["instanceId"] != state["instanceId"] or current["version"] != state["version"]:
                raise RuntimeError("failed acquisition changed the running instance")
            return {"result": "PASS", "recovery": "failed acquisition preserved the original running version"}
        if result != 0:
            raise RuntimeError("packaged " + args.owner_action + " failed; private terminal log retained")
        current = health(port)
        if args.owner_action == "install":
            state = {"instanceId": current["instanceId"], "version": current["version"]}
        elif current["instanceId"] != state["instanceId"] or current["version"] != version:
            raise RuntimeError("bootstrap did not preserve the instance and install candidate")
        state["version"] = current["version"]
        write_private(state_dir / "state.json", state)
        code, opened = terminal([command_path(), "open", "--print"], {})
        links = re.findall(r"https?://[^\s]+", opened)
        if code or not links:
            raise RuntimeError("normal private browser handoff missing")
        return {"result": "PASS", "url": links[-1], "health": current}
    if args.owner_action == "login":
        code, output = terminal([command_path(), "login"], {"Owner password: ": PASSWORD})
        if code or "CLI authenticated" not in output:
            raise RuntimeError("ordinary CLI owner sign-in failed")
        return {"result": "PASS"}
    if args.owner_action == "apply":
        before = health(port)
        if before["version"] == args.version:
            raise RuntimeError("apply requires an older running reader")
        code, output = terminal([command_path(), "update"], prompts, timeout=300)
        (state_dir / "apply.log").write_text(output.replace(PASSWORD, "[redacted]"))
        current = health(port)
        if code or "Updated" not in output or current["version"] != args.version or current["instanceId"] != before["instanceId"]:
            raise RuntimeError("ordinary installed CLI did not apply the candidate")
        state["version"] = current["version"]
        write_private(state_dir / "state.json", state)
        return {"result": "PASS", "cliOutcome": "applied", "health": current}
    if args.owner_action == "check":
        code, output = terminal([command_path(), "update"], prompts)
        if code or "Already current" not in output:
            raise RuntimeError("ordinary installed CLI Update/no-op failed")
        return {"result": "PASS", "cliOutcome": "alreadyCurrent", "health": health(port)}
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
    parser.add_argument("--profile", choices=PROFILES)
    parser.add_argument("--owner-action", choices=["install", "failed-bootstrap", "bootstrap", "open", "login", "apply", "check", "restart"])
    parser.add_argument("--version")
    args = parser.parse_args()
    vm_guard()
    if args.action == "owner":
        print(json.dumps(owner(args), separators=(",", ":")))
        return
    if os.getuid() != 1000 or args.work is None:
        raise RuntimeError("fixed VM fixture administrator required")
    if args.action == "setup":
        candidate, predecessor, legacy = package(args.candidate), package(args.predecessor), package(args.legacy)
        if candidate["packet"]["version"] != 2 or predecessor["packet"]["version"] != 2 or predecessor["packet"]["productVersion"] != "0.1.4" or legacy["packet"]["version"] != 1:
            raise RuntimeError("target, attested compatibility reader and original legacy bridge inputs required")
        args.work.mkdir(mode=0o700, parents=True)
        write_private(args.work / "fixture.json", {"candidate": str(args.candidate), "predecessor": str(args.predecessor), "legacy": str(args.legacy)})
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
