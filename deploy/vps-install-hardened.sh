#!/usr/bin/env bash
set -euo pipefail

# Hardened two-process Linux systemd bootstrap for Fased App + Isolated Custody Signer.
# Run on target VPS as root after copying /usr/local/bin/fased-app.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ "${EUID}" -ne 0 ]]; then
  echo "error: run as root" >&2
  exit 1
fi

groupadd --system fased-ipc || true
groupadd --system fased-signer || true
groupadd --system fased-app || true
id -u fased-signer >/dev/null 2>&1 || useradd --system --home /var/lib/fased-signer --shell /usr/sbin/nologin --gid fased-signer --groups fased-ipc fased-signer
id -u fased-app >/dev/null 2>&1 || useradd --system --home /var/lib/fased-app --shell /usr/sbin/nologin --gid fased-app --groups fased-ipc fased-app
usermod --gid fased-signer --append --groups fased-ipc fased-signer
usermod --gid fased-app --append --groups fased-ipc fased-app

install -d -m 0700 -o fased-signer -g fased-signer /var/lib/fased-signer
install -d -m 0700 -o fased-app -g fased-app /var/lib/fased-app
install -d -m 0750 -o root -g root /etc/credstore.encrypted /etc/fased

provision_sealed_cred() {
  local name="$1"
  local dest="/etc/credstore.encrypted/${name}"
  if [[ ! -f "${dest}" ]]; then
    umask 077
    head -c 32 /dev/urandom | systemd-creds encrypt --with-key=host --name="${name}" - "${dest}"
    chmod 0400 "${dest}"
  fi
}

provision_sealed_cred fased-signer-kek
provision_sealed_cred fased-provider-task-key

install -m 0644 "${SCRIPT_DIR}/systemd/fased-signer.service" /etc/systemd/system/fased-signer.service
install -m 0644 "${SCRIPT_DIR}/systemd/fased-app.service" /etc/systemd/system/fased-app.service

systemctl daemon-reload
systemctl enable --now fased-signer.service
systemctl enable --now fased-app.service
systemctl status --no-pager fased-signer.service fased-app.service
