#!/bin/bash
# Cross-platform CLI & self-hosted Server installer template (macOS + Linux amd64/arm64).
# Publisher tooling pins exact release version, HTTPS URLs, and SHA-256 archive digests.
set -euo pipefail
umask 077

entry_version='@ENTRY_VERSION@'
development_only='@DEVELOPMENT_ONLY@'
fixture_ca_base64='@FIXTURE_CA_BASE64@'

die() { printf 'Fased: %s\n' "$*" >&2; exit 1; }
usage() {
  printf 'Usage: install-cli.sh [--local|--server|--connect] [--transport tailscale|cloudflare|domain|ssh] [--domain HOST] [--open|--start|--stop|--status|--uninstall] [--purge] [--upgrade] [--no-autostart] [--prepare-host] [--listen 127.0.0.1:PORT]\n'
}

profile='' action=install listen='127.0.0.1:7331'
server_transport='' server_domain='' purge=0 upgrade=0 no_autostart=0 prepare_host=0
while (($#)); do
  case "$1" in
    --local) profile=local; shift ;;
    --remote) die 'Use install.sh --remote on Ubuntu 24.04 for attested Managed Remote setup.' ;;
    --server) profile=server; shift ;;
    --connect) profile=connect; shift ;;
    --transport) (($# >= 2)) || die 'Missing server transport.'; server_transport=$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]'); shift 2 ;;
    --domain) (($# >= 2)) || die 'Missing server domain.'; server_domain=$2; shift 2 ;;
    --open|--start|--stop|--status)
      [[ $action == install ]] || die 'Choose one management action.'
      action=${1#--}; shift ;;
    --uninstall)
      [[ $action == install ]] || die 'Choose one management action.'
      action=uninstall; shift ;;
    --purge) purge=1; shift ;;
    --upgrade) upgrade=1; shift ;;
    --no-autostart) no_autostart=1; shift ;;
    --prepare-host) prepare_host=1; shift ;;
    --listen) (($# >= 2)) || die 'Missing listener.'; listen=$2; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done

[[ $entry_version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9.-]+)?$ && ( $development_only == 0 || $development_only == 1 ) ]] || die 'No trusted installer release is published yet.'
if [[ $profile == connect && $action != install ]]; then
  die 'Connect does not manage an installation.'
fi
if [[ -n $server_transport || -n $server_domain ]]; then
  [[ $profile == server && $action == install ]] || die '--transport and --domain require --server.'
fi
if [[ -n $server_transport ]]; then
  [[ $server_transport == tailscale || $server_transport == cloudflare || $server_transport == domain || $server_transport == ssh ]] || die 'Server transport must be tailscale, cloudflare, domain, or ssh.'
fi
if [[ -n $server_domain ]]; then
  [[ $server_transport != ssh ]] || die 'SSH tunnel transport does not take --domain.'
  [[ ${#server_domain} -le 253 && $server_domain == *.* && $server_domain =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$ ]] || die 'Invalid server HTTPS domain.'
fi
if [[ $purge == 1 ]]; then
  [[ $action == uninstall ]] || die '--purge requires --uninstall.'
fi
if [[ $action == uninstall ]]; then
  [[ $profile == server || $profile == local ]] || die '--uninstall requires --local or --server.'
  [[ $purge == 0 || $profile == server ]] || die '--purge is only supported with --server --uninstall.'
fi
if [[ $no_autostart == 1 ]]; then
  [[ $action == install && $profile != server && $profile != connect ]] || die '--no-autostart is only supported with --local install.'
fi
if [[ $prepare_host == 1 ]]; then
  [[ $action == install && $profile != server && $profile != connect ]] || die '--prepare-host is only supported with --local install.'
fi
if [[ $upgrade == 1 ]]; then
  [[ $action == install && $profile != connect ]] || die '--upgrade is only supported with --local or --server install.'
fi
[[ $listen =~ ^127\.0\.0\.1:([0-9]{4,5})$ ]] || die 'Use a fixed loopback port (127.0.0.1:PORT).'
port=${BASH_REMATCH[1]}
[[ $port == "$((10#$port))" ]] && ((10#$port >= 1024 && 10#$port <= 65535)) || die 'Invalid listener port.'

os_raw=$(uname -s)
case "$os_raw" in
  Darwin) os_name=darwin ;;
  Linux) os_name=linux ;;
  *) die "Unsupported operating system: $os_raw" ;;
esac
arch_raw=$(uname -m)
case "$arch_raw" in
  arm64|aarch64) arch_name=arm64 ;;
  x86_64|amd64) arch_name=amd64 ;;
  *) die "No verified package for CPU architecture: $arch_raw" ;;
esac
target="${os_name}-${arch_name}"

archive_url='' archive_sha256='' archive_ext=''
@CLI_TARGET_BINDINGS@
[[ $archive_sha256 =~ ^[a-f0-9]{64}$ ]] || die 'No authenticated CLI package checksum is pinned.'
if [[ $development_only == 0 ]]; then
  [[ $archive_url == "https://github.com/fased-ai/fased-app/releases/download/v${entry_version}/fased-app-${target}.${archive_ext}" && -z $fixture_ca_base64 ]] || die 'Untrusted CLI installer source.'
elif [[ $development_only == 1 ]]; then
  [[ $archive_url =~ ^https://127\.0\.0\.1:[0-9]+/ && -n $fixture_ca_base64 ]] || die 'Invalid controlled CLI fixture.'
  printf 'Fased: unpublished disposable-trust test installer.\n' >&2
else
  die 'Invalid installer binding.'
fi

if [[ $(id -u) == 0 ]]; then
  die 'Run Fased CLI setup from your ordinary user account, without sudo.'
fi

if [[ $os_name == darwin ]]; then
  app_root="$HOME/Library/Application Support/Fased App/app"
  server_root="$HOME/Library/Application Support/Fased App/server"
  cache_root="$HOME/Library/Caches/fased-install"
else
  data_base="${XDG_DATA_HOME:-$HOME/.local/share}/fased-app"
  app_root="$data_base"
  server_root="$data_base"
  cache_root="${XDG_CACHE_HOME:-$HOME/.cache}/fased-install"
fi

if [[ $action != install ]]; then
  installed_bin=''
  if [[ $profile == server ]]; then
    installed_bin="$server_root/fased-app"
    [[ -x $installed_bin ]] || die 'No installed Fased server binary was found.'
    case "$action" in
      uninstall)
        if [[ $purge == 1 ]]; then exec "$installed_bin" server uninstall --purge; fi
        exec "$installed_bin" server uninstall ;;
      status) exec "$installed_bin" server status ;;
      *) die "Use 'fased-app server status|uninstall' or the host service manager for Server lifecycle." ;;
    esac
  else
    if [[ -x "$app_root/fased-app" ]]; then
      installed_bin="$app_root/fased-app"
    elif [[ -z $profile && $action == status && -x "$server_root/fased-app" ]]; then
      exec "$server_root/fased-app" server status
    fi
    [[ -n $installed_bin ]] || die 'No installed Fased Local binary was found.'
    case "$action" in
      uninstall) exec "$installed_bin" uninstall ;;
      open) exec "$installed_bin" open ;;
      start|stop|status) exec "$installed_bin" service "$action" ;;
    esac
  fi
fi

has_tty=0
if { exec 3<>/dev/tty; } 2>/dev/null; then
  has_tty=1
fi
needs_tty=0
if [[ -z $profile || $profile == connect ]]; then
  needs_tty=1
elif [[ $profile == server ]]; then
  if [[ -z $server_transport || ( ( $server_transport == cloudflare || $server_transport == domain ) && -z $server_domain ) ]]; then
    needs_tty=1
  fi
fi
if [[ $needs_tty == 1 ]]; then
  [[ $has_tty == 1 ]] || die 'Run this command in an interactive terminal.'
fi
if [[ -z $profile ]]; then
  choice=''
  printf 'Fased\n  Local\n  Server\n  Connect\nChoose [local/server/connect, default local]: ' >&3
  IFS= read -r choice <&3
  choice=$(printf '%s' "$choice" | tr '[:upper:]' '[:lower:]')
  case "$choice" in
    ""|local) profile=local ;;
    server|connect) profile=$choice ;;
    *) die 'Choose Local, Server, or Connect.' ;;
  esac
fi
if [[ $profile == server ]]; then
  if [[ -z $server_transport ]]; then
    choice=''
    printf 'Fased Server\n  ssh        — SSH tunnel (loopback only)\n  tailscale  — Tailscale private HTTPS\n  cloudflare — Cloudflare Tunnel HTTPS\n  domain     — Your own domain (Caddy auto-HTTPS)\nTransport [ssh/tailscale/cloudflare/domain, default ssh]: ' >&3
    IFS= read -r choice <&3
    choice=$(printf '%s' "$choice" | tr '[:upper:]' '[:lower:]')
    case "$choice" in
      ""|ssh) server_transport=ssh ;;
      tailscale|cloudflare|domain) server_transport=$choice ;;
      *) die 'Choose ssh, tailscale, cloudflare, or domain.' ;;
    esac
  fi
  if [[ ( $server_transport == cloudflare || $server_transport == domain ) && -z $server_domain ]]; then
    printf 'HTTPS domain (e.g. fased.example.com): ' >&3
    IFS= read -r server_domain <&3
    [[ ${#server_domain} -le 253 && $server_domain == *.* && $server_domain =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$ ]] || die 'Invalid server HTTPS domain.'
  fi
fi

mkdir -p -m 0700 "$cache_root"
work=$(mktemp -d "$cache_root/entry.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT

curl_args=(-q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --noproxy '*' --connect-timeout 10 --max-time 180 --max-filesize 268435456 -H 'Accept-Encoding: identity')
if [[ $development_only == 1 ]]; then
  if [[ $os_name == darwin ]]; then
    printf '%s' "$fixture_ca_base64" | base64 -D > "$work/ca.pem"
  else
    printf '%s' "$fixture_ca_base64" | base64 --decode > "$work/ca.pem"
  fi
  curl_args+=(--cacert "$work/ca.pem")
fi

archive_path="$work/archive.${archive_ext}"
printf 'Downloading and verifying Fased (%s)…\n' "$target" >&2
curl "${curl_args[@]}" --output "$archive_path" "$archive_url"
if [[ $os_name == darwin ]]; then
  read -r actual _ < <(shasum -a 256 "$archive_path")
else
  read -r actual _ < <(sha256sum "$archive_path")
fi
[[ $actual == "$archive_sha256" ]] || die 'Archive checksum differs; nothing was executed.'
if [[ $development_only == 0 && ${FASED_REQUIRE_ATTESTATION:-0} == 1 ]]; then
  command -v gh >/dev/null 2>&1 || die 'FASED_REQUIRE_ATTESTATION=1 requires GitHub CLI (gh).'
  gh attestation verify "$archive_path" --repo fased-ai/fased-app >/dev/null || die 'GitHub build provenance attestation verification failed.'
fi

mkdir -p -m 0700 "$work/extract"
if [[ $archive_ext == zip ]]; then
  ditto -xk "$archive_path" "$work/extract"
else
  tar -xzf "$archive_path" -C "$work/extract"
fi

bundle_dir=''
if [[ -x "$work/extract/Fased App.app/Contents/MacOS/fased-app" && -f "$work/extract/Fased App.app/Contents/MacOS/bundle.json" ]]; then
  bundle_dir="$work/extract/Fased App.app/Contents/MacOS"
elif [[ -x "$work/extract/fased-app-${target}/fased-app" && -f "$work/extract/fased-app-${target}/bundle.json" ]]; then
  bundle_dir="$work/extract/fased-app-${target}"
else
  die 'Verified archive is missing fased-app or bundle.json.'
fi
bundle_engine="$bundle_dir/fased-app"

case "$profile" in
  local)
    local_args=(install --bundle "$bundle_dir" --listen "$listen")
    if [[ $no_autostart == 0 ]]; then
      local_args+=(--autostart)
    fi
    if [[ $prepare_host == 1 ]]; then
      local_args+=(--prepare-host)
    fi
    if [[ $upgrade == 1 || -f "$app_root/install.json" ]]; then
      local_args+=(--upgrade)
    fi
    "$bundle_engine" "${local_args[@]}"
    bin_dir="$HOME/.local/bin"
    mkdir -p "$bin_dir" 2>/dev/null || true
    if [[ -d $bin_dir && ! -e "$bin_dir/fased" && ! -L "$bin_dir/fased" && -x "$app_root/fased-app" ]]; then
      ln -s "$app_root/fased-app" "$bin_dir/fased" 2>/dev/null || true
    fi
    ;;
  server)
    srv_args=(server install --transport "$server_transport" --listen "$listen")
    if [[ -n $server_domain ]]; then srv_args+=(--domain "$server_domain"); fi
    "$bundle_engine" "${srv_args[@]}"
    ;;
  connect)
    [[ $has_tty == 1 ]] || die 'Run this command in an interactive terminal.'
    "$bundle_engine" connect <&3
    ;;
esac
