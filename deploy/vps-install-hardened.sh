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
id -u fased-signer >/dev/null 2>&1 || useradd --system --home /var/lib/fased-signer --shell /usr/sbin/nologin --gid fased-ipc fased-signer
id -u fased-app >/dev/null 2>&1 || useradd --system --home /var/lib/fased-app --shell /usr/sbin/nologin --gid fased-ipc fased-app

install -d -m 0700 -o fased-signer -g fased-ipc /var/lib/fased-signer
install -d -m 0700 -o fased-app -g fased-ipc /var/lib/fased-app
install -d -m 0750 -o root -g root /etc/credstore.encrypted /etc/fased

if [[ ! -f /etc/credstore.encrypted/fased-signer-kek ]]; then
  tmp_kek="$(mktemp)"
  chmod 0400 "${tmp_kek}"
  head -c 32 /dev/urandom > "${tmp_kek}"
  systemd-creds encrypt --name=fased-signer-kek "${tmp_kek}" /etc/credstore.encrypted/fased-signer-kek
  shred -u "${tmp_kek}" || rm -f "${tmp_kek}"
  chmod 0400 /etc/credstore.encrypted/fased-signer-kek
fi

install -m 0644 "${SCRIPT_DIR}/systemd/fased-signer.service" /etc/systemd/system/fased-signer.service
install -m 0644 "${SCRIPT_DIR}/systemd/fased-app.service" /etc/systemd/system/fased-app.service

systemctl daemon-reload
systemctl enable --now fased-signer.service
systemctl enable --now fased-app.service
systemctl status --no-pager fased-signer.service fased-app.service
