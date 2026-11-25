#!/bin/bash
# Release entry template. Publisher tooling pins public trust and verifier bytes.
# Root performs OS preparation only; verifier, wizard and Fased run unprivileged.
set -euo pipefail
umask 077
entry_version='@ENTRY_VERSION@'
installer_url='@INSTALLER_URL@'
installer_sha256='@INSTALLER_SHA256@'
seed_base64='@SEED_BASE64@'
seed_sha256='@SEED_SHA256@'
fixture_ca_base64='@FIXTURE_CA_BASE64@'
development_only='@DEVELOPMENT_ONLY@'

root_file() { [[ -f $1 && ! -L $1 && $(/usr/bin/stat -c '%u:%g:%a:%h' "$1") == "0:0:$2:1" ]]; }
operator_record() { [[ $1 =~ ^(pending:[a-f0-9]{32}|ready:[1-9][0-9]*:[1-9][0-9]*:(external|[a-f0-9]{32}))$ ]]; }
die() { printf 'Fased: %s\n' "$*" >&2; exit 1; }
package_installed() {
  case "$package_family" in
    apt) [[ $(/usr/bin/dpkg-query -W -f='${Status}' "$1" 2>/dev/null || true) == 'install ok installed' ]] ;;
    dnf) /usr/bin/rpm -q --quiet -- "$1" ;;
    *) return 1 ;;
  esac
}
install_packages() {
  local -a elevate=()
  if [[ $(/usr/bin/id -u) != 0 ]]; then elevate=(/usr/bin/sudo --); fi
  case "$package_family" in
    apt)
      "${elevate[@]}" /usr/bin/apt-get update
      "${elevate[@]}" /usr/bin/apt-get install -y --no-remove --no-install-recommends "$@" ;;
    dnf)
      "${elevate[@]}" /usr/bin/dnf install --assumeyes --setopt=install_weak_deps=False "$@" ;;
    *) die 'Unsupported OS package manager.' ;;
  esac
}
darwin_entry() {
  [[ $(/usr/bin/id -u) != 0 ]] || die 'Run Fased on macOS from your ordinary user account, without sudo.'
  [[ -z $operator ]] || die '--operator is only for Linux root bootstrap.'
  [[ $action != refresh-helper && -z $previous_helper ]] || die '--refresh-helper is only supported on Linux.'
  [[ -z $candidate_dir && -z $candidate_packet ]] || die '--candidate-dir is only supported on Linux.'
  [[ -z $wallet_instance && -z $wallet_network && $action != wallets ]] || die 'Wallets enrollment is not yet supported on macOS.'
  [[ $profile != remote ]] || die 'Managed Remote requires a supported Linux host; use --server on macOS.'

  local darwin_cpu target darwin_archive_url='' darwin_archive_sha256=''
  darwin_cpu=$(/usr/bin/uname -m)
  case "$darwin_cpu" in
    arm64|aarch64) target=darwin-arm64 ;;
    x86_64|amd64) target=darwin-amd64 ;;
    *) die 'No verified package for this macOS CPU.' ;;
  esac
  @DARWIN_TARGET_BINDINGS@
  [[ $darwin_archive_sha256 =~ ^[a-f0-9]{64}$ ]] || die 'No authenticated macOS package checksum is pinned.'
  if [[ $development_only == 0 ]]; then
    [[ $darwin_archive_url == "https://github.com/fased-ai/fased-app/releases/download/v${entry_version}/fased-app-${target}.zip" && -z $fixture_ca_base64 ]] || die 'Untrusted macOS installer source.'
  elif [[ $development_only == 1 ]]; then
    [[ $darwin_archive_url =~ ^https://127\.0\.0\.1:[0-9]+/ && -n $fixture_ca_base64 ]] || die 'Invalid controlled macOS fixture.'
    printf 'Fased: unpublished disposable-trust test installer.\n' >&2
  else
    die 'Invalid installer binding.'
  fi

  local app_root="$HOME/Library/Application Support/Fased App/app"
  local server_root="$HOME/Library/Application Support/Fased App/server"
  if [[ $action != install ]]; then
    case "$action" in
      uninstall|status|start|stop|open) ;;
      *) die "The --${action} action is not yet supported on macOS." ;;
    esac
    local installed_bin=''
    if [[ $profile == server ]]; then
      installed_bin="$server_root/fased-app"
      [[ -x $installed_bin ]] || die 'No installed Fased server binary was found.'
      case "$action" in
        uninstall)
          if [[ $purge == 1 ]]; then exec "$installed_bin" server uninstall --purge; fi
          exec "$installed_bin" server uninstall ;;
        status)
          exec "$installed_bin" server status ;;
        *)
          die "Use 'fased-app server status|uninstall' or launchctl for macOS Server." ;;
      esac
    else
      if [[ -x "$app_root/fased-app" ]]; then
        installed_bin="$app_root/fased-app"
      elif [[ -z ${profile} && $action == status && -x "$server_root/fased-app" ]]; then
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

  local has_tty=0
  if { exec 3<>/dev/tty; } 2>/dev/null; then
    has_tty=1
  fi
  if [[ ${profile:-} != server ]]; then
    [[ $has_tty == 1 ]] || die 'Run this command in an interactive terminal.'
  fi
  if [[ -z ${profile} ]]; then
    [[ $has_tty == 1 ]] || die 'Run this command in an interactive terminal.'
    local choice
    printf 'Fased\n  Local\n  Server\n  Connect\nChoose [local/server/connect, default local]: ' >&3
    IFS= read -r choice <&3
    choice=$(printf '%s' "$choice" | /usr/bin/tr '[:upper:]' '[:lower:]')
    case "$choice" in
      ""|local) profile=local ;;
      server|connect) profile=$choice ;;
      *) die 'Choose Local, Server, or Connect.' ;;
    esac
  fi
  if [[ $profile == server ]]; then
    if [[ -z $server_transport ]]; then
      local choice
      printf 'Fased Server\n  ssh        — SSH tunnel (loopback only)\n  tailscale  — Tailscale private HTTPS\n  cloudflare — Cloudflare Tunnel HTTPS\n  domain     — Your own domain (Caddy auto-HTTPS)\nTransport [ssh/tailscale/cloudflare/domain, default ssh]: ' >&3
      IFS= read -r choice <&3
      choice=$(printf '%s' "$choice" | /usr/bin/tr '[:upper:]' '[:lower:]')
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

  local cache_root="$HOME/Library/Caches/fased-install" work actual
  /bin/mkdir -p -m 0700 "$cache_root"
  [[ ! -L $cache_root && $(/usr/bin/stat -f '%u:%Lp' "$cache_root") == "$(/usr/bin/id -u):700" ]] || die 'Installer cache must be private and owned by you.'
  work=$(/usr/bin/mktemp -d "$cache_root/entry.XXXXXXXX")
  trap '/bin/rm -rf -- "$work"' EXIT

  local darwin_curl_args
  darwin_curl_args=(-q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --noproxy '*' --connect-timeout 10 --max-time 180 --max-filesize 268435456 -H 'Accept-Encoding: identity')
  if [[ $development_only == 1 ]]; then
    printf '%s' "$fixture_ca_base64" | /usr/bin/base64 -D > "$work/ca.pem"
    darwin_curl_args+=(--cacert "$work/ca.pem")
  fi
  printf 'Downloading and verifying Fased for macOS…\n' >&2
  /usr/bin/curl "${darwin_curl_args[@]}" --output "$work/archive.zip" "$darwin_archive_url"
  read -r actual _ < <(/usr/bin/shasum -a 256 "$work/archive.zip")
  [[ $actual == "$darwin_archive_sha256" ]] || die 'macOS archive checksum differs; nothing was executed.'
  /bin/mkdir -p -m 0700 "$work/extract"
  /usr/bin/ditto -xk "$work/archive.zip" "$work/extract"

  local bundle_dir=''
  if [[ -x "$work/extract/Fased App.app/Contents/MacOS/fased-app" && -f "$work/extract/Fased App.app/Contents/MacOS/bundle.json" ]]; then
    bundle_dir="$work/extract/Fased App.app/Contents/MacOS"
  elif [[ -x "$work/extract/fased-app-${target}/fased-app" && -f "$work/extract/fased-app-${target}/bundle.json" ]]; then
    bundle_dir="$work/extract/fased-app-${target}"
  else
    die 'Verified macOS archive is missing fased-app or bundle.json.'
  fi
  local bundle_engine="$bundle_dir/fased-app"

  case "$profile" in
    local)
      "$bundle_engine" install --bundle "$bundle_dir" --listen "$listen" --autostart
      local bin_dir="$HOME/.local/bin"
      /bin/mkdir -p "$bin_dir" 2>/dev/null || true
      if [[ -d $bin_dir && ! -e "$bin_dir/fased" && ! -L "$bin_dir/fased" && -x "$app_root/fased-app" ]]; then
        /bin/ln -s "$app_root/fased-app" "$bin_dir/fased" 2>/dev/null || true
      fi
      ;;
    server)
      local srv_args
      srv_args=(server install --transport "$server_transport" --listen "$listen")
      if [[ -n $server_domain ]]; then srv_args+=(--domain "$server_domain"); fi
      "$bundle_engine" "${srv_args[@]}"
      ;;
    connect)
      [[ $has_tty == 1 ]] || die 'Run this command in an interactive terminal.'
      "$bundle_engine" connect <&3
      ;;
  esac
}
profile='' operator='' action=install listen='127.0.0.1:7331'
wallet_instance='' wallet_network=''
server_transport='' server_domain='' purge=0
candidate_dir='' candidate_packet='' package_instance='' refresh_instance='' previous_helper=''
while (($#)); do
  case "$1" in
    --refresh-helper) [[ $action == install ]] || die 'Choose one maintenance action.'; action=refresh-helper; if (( $# >= 2 )) && [[ $2 != -* ]]; then refresh_instance=$2; shift 2; else shift 1; fi ;;
    --previous-helper) (($# >= 2)) || die 'Missing predecessor helper.'; previous_helper=$2; shift 2 ;;
    --local) profile=local; shift ;;
    --remote) profile=remote; shift ;;
    --server) profile=server; shift ;;
    --connect) profile=connect; shift ;;
    --transport) (($# >= 2)) || die 'Missing server transport.'; server_transport=$(printf '%s' "$2" | /usr/bin/tr '[:upper:]' '[:lower:]'); shift 2 ;;
    --domain) (($# >= 2)) || die 'Missing server domain.'; server_domain=$2; shift 2 ;;
    --open|--start|--stop|--status|--recover|--update|--setup|--wallets)
      [[ $action == install ]] || die 'Choose one management action.'
      action=${1#--}; shift ;;
    --uninstall)
      [[ $action == install ]] || die 'Choose one management action.'
      action=uninstall; shift ;;
    --purge) purge=1; shift ;;
    --wallet-instance) (($# >= 2)) || die 'Missing workspace.'; wallet_instance=$2; shift 2 ;;
    --wallet-network) (($# >= 2)) || die 'Missing network.'; wallet_network=$2; shift 2 ;;
    --operator) (($# >= 2)) || die 'Missing operator.'; operator=$2; shift 2 ;;
    --listen) (($# >= 2)) || die 'Missing listener.'; listen=$2; shift 2 ;;
    --candidate-dir) (($# >= 2)) || die 'Missing candidate directory.'; candidate_dir=$2; shift 2 ;;
    --candidate-packet-sha256) (($# >= 2)) || die 'Missing candidate identity.'; candidate_packet=$2; shift 2 ;;
    --help|-h) printf 'Usage: install.sh [--local|--remote|--server|--connect] [--transport tailscale|cloudflare|domain|ssh] [--domain HOST] [--open|--start|--stop|--status|--recover|--update|--setup|--wallets|--uninstall] [--purge] [--wallet-instance ID --wallet-network NETWORK] [--local|--remote --refresh-helper [INSTANCE] [--previous-helper SHA256]] [--operator USER] [--listen 127.0.0.1:PORT]\n'; exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done
[[ $entry_version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9.-]+)?$ && $installer_sha256 =~ ^[a-f0-9]{64}$ && $seed_sha256 =~ ^[a-f0-9]{64}$ ]] || die 'No trusted installer release is published yet.'
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
if [[ $action == wallets ]]; then
  [[ $profile == remote && $wallet_instance =~ ^[A-Za-z0-9_-]{43}$ && ( $wallet_network == devnet || $wallet_network == mainnet-beta ) ]] || die 'Wallets setup requires Remote, the exact workspace and network.'
else
  [[ -z $wallet_instance && -z $wallet_network ]] || die 'Wallet options require --wallets.'
fi
if [[ $action == refresh-helper ]]; then
  [[ ( $profile == local || $profile == remote ) && -z $operator && $(/usr/bin/id -u) != 0 && $development_only == 0 ]] || die 'Use the verified Local or Remote installer as the instance owner.'
  [[ -z $refresh_instance || $refresh_instance =~ ^[a-f0-9]{32}$ ]] || die 'Use one exact 32-character hex instance ID.'
  [[ -z $previous_helper || $previous_helper =~ ^[a-f0-9]{64}$ ]] || die 'Use one exact 64-character hex predecessor helper digest.'
else
  [[ -z $previous_helper ]] || die 'A predecessor helper requires --refresh-helper.'
fi
[[ -z $operator || $operator =~ ^[a-z_][a-z0-9_-]{0,30}$ && $operator != root ]] || die 'Invalid ordinary operator.'
[[ $listen =~ ^127\.0\.0\.1:([0-9]{4,5})$ ]] || die 'Use a fixed loopback port.'
port=${BASH_REMATCH[1]}
[[ $port == "$((10#$port))" ]] && ((10#$port >= 1024 && 10#$port <= 65535)) || die 'Invalid listener port.'
if [[ $(/usr/bin/uname -s) == Darwin ]]; then
  darwin_entry
  exit $?
fi
[[ -r /etc/os-release ]] || die 'OS release metadata is missing.'
# Read only fixed data fields; never evaluate os-release as shell code.
ID='' ID_LIKE='' VERSION_ID='' seen_id_like=0
while IFS='=' read -r key value; do
  case "$key" in ID|ID_LIKE|VERSION_ID)
    value=${value%\"}; value=${value#\"}; value=${value%\'}; value=${value#\'}
    if [[ $key == ID_LIKE ]]; then
      [[ $seen_id_like == 0 && $value =~ ^[a-z0-9._\ -]*$ ]] || die 'Invalid OS family metadata.'
      ID_LIKE=$value; seen_id_like=1
    else
      [[ $value =~ ^[a-z0-9._-]+$ ]] || die 'Invalid OS release metadata.'
      if [[ $key == ID ]]; then [[ -z $ID ]] || die 'Ambiguous OS identity.'; ID=$value
      else [[ -z $VERSION_ID ]] || die 'Ambiguous OS version.'; VERSION_ID=$value; fi
    fi ;;
  esac
done < /etc/os-release
[[ -n $ID && -n $VERSION_ID ]] || die 'OS identity and version are required.'
package_family=''
for family_id in "$ID" $ID_LIKE; do
  next_family=''
  case "$family_id" in
    ubuntu|debian) next_family=apt ;;
    fedora|rhel|centos|rocky|almalinux) next_family=dnf ;;
  esac
  [[ -z $next_family || -z $package_family || $next_family == "$package_family" ]] || die 'Conflicting Linux package families.'
  if [[ -n $next_family ]]; then package_family=$next_family; fi
done
@PLATFORM_BINDINGS@
case $(/usr/bin/uname -m) in x86_64|amd64) target=linux-amd64 ;; aarch64|arm64) target=linux-arm64 ;; *) die 'No verified package for this CPU.' ;; esac
@PLATFORM_TARGET_BINDINGS@
@TARGET_BINDINGS@
if [[ $development_only == 0 ]]; then
  [[ $installer_url == "https://github.com/fased-ai/fased-app/releases/download/v${entry_version}/fased-install-${target}" && -z $fixture_ca_base64 ]] || die 'Untrusted installer source.'
elif [[ $development_only == 1 ]]; then
  [[ $installer_url =~ ^https://127\.0\.0\.1:[0-9]+/fased-install$ && -n $fixture_ca_base64 ]] || die 'Invalid controlled fixture.'
  printf 'Fased: unpublished disposable-trust test installer.\n' >&2
else die 'Invalid installer binding.'; fi
has_tty=0
if { exec 3<>/dev/tty; } 2>/dev/null; then
  has_tty=1
fi
if [[ ${profile:-} != server ]]; then
  [[ $has_tty == 1 ]] || die 'Run this command in an interactive terminal.'
fi
if [[ -z $profile ]]; then
  suggested=local
  if [[ -n ${SSH_CONNECTION:-} || $(/usr/bin/id -u) == 0 && -z ${SUDO_USER:-} ]]; then suggested=remote; fi
  if [[ $action == install ]]; then
    printf 'Fased\n  Local\n  Remote\n  Server\n  Connect\n  Update\nChoose [local/remote/server/connect/update, default %s]: ' "$suggested" >&3
    IFS= read -r choice <&3
    choice=$(printf '%s' "$choice" | /usr/bin/tr '[:upper:]' '[:lower:]')
    case "$choice" in "") profile=$suggested ;; local|remote|server|connect) profile=$choice ;; update) action=update ;; *) die 'Choose Local, Remote, Server, Connect or Update.' ;; esac
  fi
  if [[ -z $profile ]]; then
    printf 'Installation [local/remote, default %s]: ' "$suggested" >&3
    IFS= read -r choice <&3; profile=$(printf '%s' "${choice:-$suggested}" | /usr/bin/tr '[:upper:]' '[:lower:]')
    [[ $profile == local || $profile == remote ]] || die 'Choose Local or Remote.'
  fi
fi
if [[ $profile == remote ]]; then
@REMOTE_PLATFORMS@
fi
if [[ $(/usr/bin/id -u) == 0 && $profile != remote && $profile != server && -z ${SUDO_USER:-} && -z $operator ]]; then
  die 'Run Local/Connect from your ordinary computer account. For a VPS, choose Remote or Server.'
fi
if [[ $action == uninstall && $profile == server ]]; then
  installed_bin=''
  data_home=${XDG_DATA_HOME:-$HOME/.local/share}
  for candidate in "$data_home/fased-app/fased-app" "$HOME/.local/share/fased-app/fased-app" "$HOME/.local/bin/fased-app" /usr/local/bin/fased-app; do
    if [[ -x $candidate ]]; then installed_bin=$candidate; break; fi
  done
  [[ -n $installed_bin ]] || die 'No installed Fased server binary was found.'
  uninstall_args=(server uninstall)
  if [[ $purge == 1 ]]; then uninstall_args+=(--purge); fi
  exec "$installed_bin" "${uninstall_args[@]}"
fi
if [[ $action == uninstall && $profile == local ]]; then
  installed_bin=''
  for candidate in "$HOME/.local/share/fased-app/app/fased-app" "$HOME/.local/bin/fased"; do
    if [[ -x $candidate ]]; then installed_bin=$candidate; break; fi
  done
  [[ -n $installed_bin ]] || die 'No installed Fased Local binary was found; run fased uninstall.'
  exec "$installed_bin" uninstall
fi
if [[ $profile == server && $action == install ]]; then
  if [[ -z $server_transport ]]; then
    printf 'Fased Server\n  ssh        — SSH tunnel (loopback only)\n  tailscale  — Tailscale private HTTPS\n  cloudflare — Cloudflare Tunnel HTTPS\n  domain     — Your own domain (Caddy auto-HTTPS)\nTransport [ssh/tailscale/cloudflare/domain, default ssh]: ' >&3
    IFS= read -r choice <&3
    choice=$(printf '%s' "$choice" | /usr/bin/tr '[:upper:]' '[:lower:]')
    case "$choice" in "") server_transport=ssh ;; ssh|tailscale|cloudflare|domain) server_transport=$choice ;; *) die 'Choose ssh, tailscale, cloudflare, or domain.' ;; esac
  fi
  if [[ ( $server_transport == cloudflare || $server_transport == domain ) && -z $server_domain ]]; then
    printf 'HTTPS domain (e.g. fased.example.com): ' >&3
    IFS= read -r server_domain <&3
    [[ ${#server_domain} -le 253 && $server_domain == *.* && $server_domain =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$ ]] || die 'Invalid server HTTPS domain.'
  fi
fi
if [[ -n $candidate_dir || -n $candidate_packet ]]; then
  [[ $candidate_dir == /* && $candidate_dir != *$'\n'* && $candidate_dir != *$'\r'* && $candidate_packet =~ ^[a-f0-9]{64}$ && ( $profile == local || $profile == remote ) && ( $action == install || $action == refresh-helper ) && -z $operator && $(/usr/bin/id -u) != 0 && $development_only == 0 ]] || die 'Candidate acceptance requires one exact package and ordinary owner.'
fi

owner_maintenance_setup() {
  [[ $profile != connect && $profile != server ]] || return 0
  local acquired=$1 protected="/var/lib/fased-maintenance/images/$installer_sha256/fased-install" stage current recovered=0
  # Administrator consent is for this one authenticated setup. No OS-tool
  # command is added to sudoers; subsequent maintenance uses one fixed helper.
  for current in /var/lib/fased-maintenance /var/lib/fased-maintenance/images "/var/lib/fased-maintenance/images/$installer_sha256"; do
    if [[ ! -e $current && ! -L $current ]]; then
      /usr/bin/sudo -- /usr/bin/install -d -m 0755 -o root -g root -- "$current"
    fi
    [[ -d $current && ! -L $current && $(/usr/bin/stat -c '%u:%g:%a' "$current") == 0:0:755 ]] || die 'Foreign maintenance directory; preserving it.'
  done
  if [[ ! -e $protected && ! -L $protected ]]; then
    stage=$(/usr/bin/sudo -- /usr/bin/mktemp -d /var/lib/fased-maintenance/.bootstrap.XXXXXXXX)
    [[ $stage =~ ^/var/lib/fased-maintenance/\.bootstrap\.[a-zA-Z0-9]+$ ]] || die 'Invalid protected staging directory.'
    /usr/bin/sudo -- /usr/bin/install -m 0555 -o root -g root -- "$acquired" "$stage/fased-install"
    # Hash the protected copy before any project executable receives root.
    read -r actual _ < <(/usr/bin/sudo -- /usr/bin/sha256sum -- "$stage/fased-install")
    [[ $actual == "$installer_sha256" ]] || die 'Protected installer checksum differs; nothing was executed.'
    /usr/bin/sudo -- /usr/bin/mv -T --no-clobber -- "$stage/fased-install" "$protected"
    /usr/bin/sudo -- /usr/bin/rmdir -- "$stage"
  fi
  root_file "$protected" 555 || die 'Foreign protected installer; preserving it.'
  read -r actual _ < <(/usr/bin/sha256sum -- "$protected")
  [[ $actual == "$installer_sha256" ]] || die 'Protected installer checksum differs.'
  /usr/bin/sudo -- /usr/bin/sync -f -- "$protected"
  if [[ $action == refresh-helper ]]; then
    local owner_uid package_parent authority_file
    owner_uid=$(/usr/bin/id -u)
    if [[ -z $refresh_instance ]]; then
      package_parent="/var/lib/fased-app-package/$owner_uid/$profile"
      [[ -d $package_parent && ! -L $package_parent ]] || die 'No installed package instance found for helper refresh.'
      local -a instances=()
      while IFS= read -r -d '' entry; do
        local name=${entry##*/}
        if [[ $name =~ ^[a-f0-9]{32}$ ]]; then instances+=("$name"); fi
      done < <(/usr/bin/find "$package_parent" -mindepth 1 -maxdepth 1 -type d -print0)
      (( ${#instances[@]} == 1 )) || die 'Specify exact --refresh-helper INSTANCE when zero or multiple instances exist.'
      refresh_instance=${instances[0]}
    fi
    if [[ -z $previous_helper ]]; then
      authority_file="/var/lib/fased-maintenance/instances/$owner_uid/$profile/$refresh_instance/authority.json"
      [[ -f $authority_file && ! -L $authority_file ]] || die 'Predecessor helper authority record is unavailable; specify --previous-helper.'
      previous_helper=$(/usr/bin/sed -n 's/.*"imageSha256"[[:space:]]*:[[:space:]]*"\([a-f0-9]\{64\}\)".*/\1/p' "$authority_file" | /usr/bin/head -n1)
      [[ $previous_helper =~ ^[a-f0-9]{64}$ ]] || die 'Cannot read predecessor helper digest; specify --previous-helper.'
    fi
    /usr/bin/sudo -- "$protected" maintenance-package-transition --owner "$owner_uid" --location "$profile" --instance "$refresh_instance" --previous-helper "$previous_helper"
    printf 'Fased maintenance is ready. Use Update in the browser or run fased update.\n'
    return
  fi
  if [[ -n $package_instance ]]; then
    current=$(/usr/bin/sudo -- "$protected" maintenance-package-register --owner "$(/usr/bin/id -u)" --location "$profile" --instance "$package_instance")
    [[ $current == "$protected" ]] || die 'Package maintenance identity differs.'
    FASED_PROTECTED_INSTALLER=$protected
    return
  fi
  current=$(/usr/bin/sudo -- "$protected" maintenance-register --owner "$(/usr/bin/id -u)" --profile "$profile")
  if [[ $action == update && $current != "$protected" ]]; then
    # The protected helper selects only this owner's existing held operation.
    current=$(/usr/bin/sudo -- "$protected" maintenance-register --owner "$(/usr/bin/id -u)" --profile "$profile" --resume)
    [[ $current != "$protected" ]] || recovered=1
  fi
  [[ $current =~ ^/var/lib/fased-maintenance/images/[a-f0-9]{64}/fased-install$ ]] || die 'Invalid registered maintenance entry.'
  /usr/bin/sudo -- "$current" maintenance-bootstrap --owner "$(/usr/bin/id -u)"
  if [[ $recovered == 1 ]]; then
    printf 'Update files were checked; app recovery and future updates are not yet verified.\n' >&2
    exit 1
  fi
  FASED_PROTECTED_INSTALLER=$protected
}

owner_install() {
  [[ $(/usr/bin/id -u) != 0 ]] || die 'Refusing to run the verifier as root.'
  cd "$HOME" || die 'Cannot enter the ordinary owner home.'
  umask 077
  /usr/bin/mkdir -p "$HOME/.cache/fased-install"
  [[ ! -L $HOME/.cache/fased-install && $(/usr/bin/stat -c '%u:%a' "$HOME/.cache/fased-install") == "$(/usr/bin/id -u):700" ]] || die 'Installer cache must be private and owned by you.'
  work=$(/usr/bin/mktemp -d "$HOME/.cache/fased-install/entry.XXXXXXXX")
  owner_cleanup() {
    # Keep small authenticated acquisition records, not duplicate executables.
    # A failed preservation leaves the private cache intact for recovery.
    if [[ -f $work/verification.json ]]; then
      records="$HOME/.local/state/fased-install"
      if ! /usr/bin/mkdir -p "$records" || [[ -L $records || $(/usr/bin/stat -c '%u:%a' "$records") != "$(/usr/bin/id -u):700" ]]; then
        printf 'Fased: verification record retained at %s\n' "$work" >&2; return
      fi
      record=$(/usr/bin/mktemp -d "$records/install.XXXXXXXX") || return
      while IFS= read -r -d '' file; do
        relative=${file#"$work/"}
        /usr/bin/mkdir -p "$record/$(/usr/bin/dirname "$relative")" || return
        /usr/bin/cp -- "$file" "$record/$relative" || return
      done < <(/usr/bin/find "$work" -type f -name '*.json' -size -256k -print0)
    fi
    /usr/bin/rm -rf -- "$work"
  }
  trap owner_cleanup EXIT
  printf '%s' "$seed_base64" | /usr/bin/base64 --decode > "$work/seed.json"
  read -r actual _ < <(/usr/bin/sha256sum "$work/seed.json")
  [[ $actual == "$seed_sha256" ]] || die 'Installer trust binding differs.'
  curl_args=(-q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --noproxy '*' --connect-timeout 10 --max-time 180 --max-filesize 134217728 -H 'Accept-Encoding: identity')
  verifier_args=(--seed-file "$work/seed.json" --seed-sha256 "$seed_sha256" --output "$work/verified" --install --profile "$profile" --listen "$listen" --receipt-file "$work/verification.json" --entry-action "$action")
  if [[ -n $candidate_dir ]]; then
    [[ -d $candidate_dir && ! -L $candidate_dir && $(/usr/bin/stat -c '%u:%a' "$candidate_dir") == "$(/usr/bin/id -u):700" ]] || die 'Candidate directory must be private and owned by you.'
    canonical_candidate=$(/usr/bin/realpath -e -- "$candidate_dir") || die 'Candidate directory is unavailable.'
    [[ $canonical_candidate == "$candidate_dir" ]] || die 'Use the original candidate directory, without links.'
    verifier_args=(--install-candidate "$candidate_dir" --candidate-packet-sha256 "$candidate_packet" --profile "$profile" --listen "$listen")
  fi
  if [[ $action == wallets ]]; then verifier_args+=(--wallet-instance "$wallet_instance" --wallet-network "$wallet_network"); fi
  if [[ $profile == server ]]; then
    verifier_args+=(--server-transport "$server_transport")
    if [[ -n $server_domain ]]; then verifier_args+=(--server-domain "$server_domain"); fi
  fi
  if [[ $development_only == 1 ]]; then
    printf '%s' "$fixture_ca_base64" | /usr/bin/base64 --decode > "$work/ca.pem"
    curl_args+=(--cacert "$work/ca.pem")
    verifier_args+=(--development --tls-ca-file "$work/ca.pem")
  fi
  printf 'Downloading and verifying Fased…\n' >&2
  installer="$work/fased-install"
  if [[ -n $candidate_dir ]]; then
    candidate_installer="$candidate_dir/fased-install-${target}"
    [[ -f $candidate_installer && ! -L $candidate_installer && -f $candidate_dir/fased-release.json && ! -L $candidate_dir/fased-release.json ]] || die 'Candidate inventory is missing.'
    read -r actual _ < <(/usr/bin/sha256sum "$candidate_dir/fased-release.json")
    [[ $actual == "$candidate_packet" ]] || die 'Candidate identity differs.'
    /usr/bin/install -m 0500 -- "$candidate_installer" "$installer"
  elif [[ -n ${FASED_PROTECTED_INSTALLER:-} ]]; then
    [[ $FASED_PROTECTED_INSTALLER == "/var/lib/fased-maintenance/images/$installer_sha256/fased-install" && ! -L $FASED_PROTECTED_INSTALLER && $(/usr/bin/stat -c '%u:%g:%a' "$FASED_PROTECTED_INSTALLER") == 0:0:555 ]] || die 'Protected installer identity differs.'
    installer=$FASED_PROTECTED_INSTALLER
  else
    /usr/bin/curl "${curl_args[@]}" --output "$installer" "$installer_url"
    /usr/bin/chmod 0500 "$installer"
  fi
  read -r actual _ < <(/usr/bin/sha256sum "$installer")
  [[ $actual == "$installer_sha256" ]] || die 'Installer checksum differs; nothing was executed.'
  if [[ $action == refresh-helper ]]; then
    if [[ -n $candidate_dir ]]; then
      candidate_report=$("$installer" --verify-candidate "$candidate_dir") || die 'Candidate verification failed.'
      [[ $candidate_report == *'"linuxBundleVerified":true'* ]] || die 'The attested Linux bundle is required.'
    else
      inspection=$("$installer" --inspect-public-package) || die 'Public release verification failed.'
      read -r kind public_packet public_version public_installer extra <<< "$inspection"
      [[ $kind == native && $public_packet =~ ^[a-f0-9]{64}$ && $public_version == "$entry_version" && $public_installer == "$installer_sha256" && -z ${extra:-} ]] || die 'The public installer identity differs.'
    fi
    owner_maintenance_setup "$installer"
    return
  fi
  if [[ -n $candidate_dir ]]; then
    candidate_report=$("$installer" --verify-candidate "$candidate_dir") || die 'Candidate verification failed.'
    if [[ $candidate_report == *'"linuxPackageDigestsVerified":true'* || $candidate_report == *'"linuxBundleVerified":true'* ]]; then
      "$installer" --package-controller-ready || die 'Native package installation and recovery are not available yet.'
      fixture_instance=$("$installer" --fixture-package-instance) || die 'The selected Local fixture is unavailable.'
      if [[ -n $fixture_instance && $profile == local ]]; then
        [[ $fixture_instance =~ ^[a-f0-9]{32}$ ]] || die 'The Local fixture identity is invalid.'
        package_instance=$fixture_instance
      else
        read -r identity _ < <(printf '%s\0%s\0%s' "$(/usr/bin/id -u)" "$candidate_packet" "$profile" | /usr/bin/sha256sum)
        [[ $identity =~ ^[a-f0-9]{64}$ ]] || die 'Cannot determine the package instance.'
        package_instance=${identity:0:32}
      fi
      verifier_args+=(--package-location "$profile" --package-instance "$package_instance")
    fi
  elif [[ ( $profile == local || $profile == remote ) && $action == install && $development_only == 0 ]]; then
    inspection=$("$installer" --inspect-public-package) || die 'Public release verification failed.'
    read -r kind public_packet public_version public_installer extra <<< "$inspection"
    if [[ $kind == native ]]; then
      [[ $public_packet =~ ^[a-f0-9]{64}$ && $public_version == "$entry_version" && $public_installer == "$installer_sha256" && -z ${extra:-} ]] || die 'Public release identity changed; restart installation.'
      "$installer" --package-controller-ready || die 'Native package installation and recovery are not available yet.'
      candidate_packet=$public_packet
      read -r identity _ < <(printf '%s\0%s\0%s' "$(/usr/bin/id -u)" "$candidate_packet" "$profile" | /usr/bin/sha256sum)
      [[ $identity =~ ^[a-f0-9]{64}$ ]] || die 'Cannot determine the package instance.'
      package_instance=${identity:0:32}
      verifier_args=(--install-public-package --candidate-packet-sha256 "$candidate_packet" --package-location "$profile" --package-instance "$package_instance" --profile "$profile" --listen "$listen")
    elif [[ $kind != legacy || -n ${public_packet:-} ]]; then
      die 'Public release verification returned an invalid result.'
    fi
  fi
  if [[ -n $package_instance ]]; then
    package_parent="/var/lib/fased-app-package/$(/usr/bin/id -u)/$profile"
    if [[ -e $package_parent || -L $package_parent ]]; then
      [[ -d $package_parent && ! -L $package_parent ]] || die 'Existing package state is unavailable; preserving it.'
      while IFS= read -r -d '' existing; do
        name=${existing##*/}
        [[ $name == "$package_instance" || $name == ".$package_instance.pending" ]] || die 'An existing package requires its installed Update command.'
      done < <(/usr/bin/find "$package_parent" -mindepth 1 -maxdepth 1 -print0)
    fi
  fi
  if [[ -z ${FASED_PROTECTED_INSTALLER:-} ]]; then
    owner_maintenance_setup "$installer"
    if [[ -n ${FASED_PROTECTED_INSTALLER:-} ]]; then installer=$FASED_PROTECTED_INSTALLER; fi
  fi
  if ! { : <&3; } 2>/dev/null; then exec 3</dev/null; fi
  "$installer" "${verifier_args[@]}" <&3
}

if [[ $(/usr/bin/id -u) != 0 ]]; then
  [[ -z $operator ]] || die '--operator is only for the root bootstrap.'
  if [[ $profile != connect && $profile != server ]] && [[ ! -x /usr/bin/sudo ]]; then
    die 'This account needs administrator preparation (sudo).'
  fi
  missing=()
  packages=(ca-certificates curl)
  if [[ $profile != connect && $profile != server && $action == install ]]; then
    case "$package_family" in apt) packages+=(polkitd) ;; dnf) packages+=(polkit) ;; esac
  fi
  if [[ $profile != connect && $profile != server && -e /sys/fs/selinux/enforce ]]; then packages+=(policycoreutils-python-utils); fi
  if [[ $profile == remote ]]; then packages+=(nftables openssh-server); fi
  for package in "${packages[@]}"; do
    package_installed "$package" || missing+=("$package")
  done
  if ((${#missing[@]})); then
    [[ -x /usr/bin/sudo ]] || die 'This account needs administrator preparation (sudo).'
    printf 'Preparing required system packages with administrator approval…\n' >&2
    install_packages "${missing[@]}"
  fi

  owner_install
  exit
fi

# Root prepares only the verified, protected maintenance entry. The wizard,
# core, native client and signer run after dropping to their ordinary identities.
printf 'Preparing Linux for Fased…\n' >&2
missing=()
packages=(ca-certificates curl sudo util-linux)
if [[ $profile != connect && $profile != server && $action == install ]]; then
  case "$package_family" in apt) packages+=(polkitd) ;; dnf) packages+=(polkit) ;; esac
fi
if [[ $profile != connect && $profile != server && -e /sys/fs/selinux/enforce ]]; then packages+=(policycoreutils-python-utils); fi
if [[ $profile == remote ]]; then packages+=(nftables openssh-server); fi
for package in "${packages[@]}"; do
  package_installed "$package" || missing+=("$package")
done
if ((${#missing[@]})); then
  install_packages "${missing[@]}"
fi
explicit_operator=$operator
operator=${operator:-${SUDO_USER:-fased-operator}}
[[ $operator =~ ^[a-z_][a-z0-9_-]{0,30}$ && $operator != root ]] || die 'Invalid ordinary operator.'
state=/var/lib/fased-bootstrap
[[ ! -L $state ]] || die 'Foreign bootstrap state.'
if [[ ! -e $state ]]; then /usr/bin/install -d -m 0700 -o root -g root "$state"; fi
[[ -d $state && $(/usr/bin/stat -c '%u:%g:%a' "$state") == 0:0:700 ]] || die 'Foreign bootstrap state ownership.'
lock="$state/lock"
if [[ ! -e $lock && ! -L $lock ]]; then (set -o noclobber; : > "$lock"); fi
root_file "$lock" 600 || die 'Foreign bootstrap lock.'
exec 9<>"$lock"
/usr/bin/flock -n 9 || die 'Another installation is running.'
marker="$state/operator-$operator"
entry=$(/usr/bin/getent passwd "$operator" || true)
[[ $action == install || -n $entry ]] || die 'No installed operator.'
if [[ ! -e $marker && ! -L $marker ]]; then
  if [[ -n $entry && -z $explicit_operator && -z ${SUDO_USER:-} ]]; then die 'The default operator already exists; choose your administrator with --operator.'; fi
  nonce=$(/usr/bin/od -An -N16 -tx1 /dev/urandom | /usr/bin/tr -d ' \n')
  (set -o noclobber; printf 'pending:%s\n' "$nonce" > "$marker")
fi
root_file "$marker" 600 || die 'Foreign operator receipt.'
mapfile -t record < "$marker"
[[ ${#record[@]} == 1 ]] && operator_record "${record[0]}" || die 'Malformed operator receipt; preserving it.'
IFS=: read -r phase first second lineage <<< "${record[0]}"
/usr/bin/sync -f "$marker"
/usr/bin/sync -f "$state"
if [[ -z $entry ]]; then
  [[ $phase == pending ]] || die 'Recorded operator is missing; preserving state.'
  /usr/sbin/useradd --create-home --user-group --shell /bin/bash --password '!' --comment "Fased installer $first" "$operator"
  entry=$(/usr/bin/getent passwd "$operator")
fi
if [[ $phase == pending ]]; then
  if [[ $entry == *":Fased installer $first:"* ]]; then lineage=$first
  elif [[ -n $explicit_operator || -n ${SUDO_USER:-} ]]; then lineage=external
  else die 'Operator differs from interrupted setup.'
  fi
elif [[ $lineage != external && $entry != *":Fased installer $lineage:"* ]]; then
  die 'Created operator lineage changed; preserving state.'
fi
IFS=: read -r _ _ owner_uid owner_gid _ owner_home _ <<< "$entry"
[[ $owner_uid =~ ^[0-9]+$ && $owner_gid =~ ^[0-9]+$ && $owner_uid -ge 1000 && $owner_gid -gt 0 && $owner_home == /* && $owner_home != / && $owner_home != /root && $(/usr/bin/stat -c '%u' "$owner_home") == "$owner_uid" ]] || die 'Invalid ordinary operator identity.'
if [[ $phase == ready ]]; then
  [[ $first == "$owner_uid" && $second == "$owner_gid" ]] || die 'Operator identity changed; preserving state.'
else
  ready_file=$(/usr/bin/mktemp "$state/.operator.XXXXXXXX")
  printf 'ready:%s:%s:%s\n' "$owner_uid" "$owner_gid" "$lineage" > "$ready_file"
  /usr/bin/sync -f "$ready_file"
  /usr/bin/mv -T -- "$ready_file" "$marker"
  /usr/bin/sync -f "$state"
fi
# Keep the existing administrator SSH route. A new operator is not silently
# advertised as SSH-accessible and receives no copied keys or new login rights.
ssh_user=${SUDO_USER:-root}
[[ $ssh_user =~ ^[a-z_][a-z0-9_-]{0,30}$ ]] || die 'Invalid administrator SSH identity.'
policy="/etc/sudoers.d/fased-bootstrap-$owner_uid"
policy_receipt="$state/policy-$owner_uid.sha256"
if [[ -e $policy_receipt || -L $policy_receipt ]]; then
  root_file "$policy_receipt" 600 || die 'Foreign policy receipt.'
  mapfile -t receipt < "$policy_receipt"
  [[ ${#receipt[@]} == 1 && ${receipt[0]} =~ ^[a-f0-9]{64}$ ]] || die 'Malformed policy receipt; preserving it.'
fi
stage=$(/usr/bin/mktemp -d "$state/stage.XXXXXXXX")
bootstrap_cleanup() {
  /usr/bin/rm -rf -- "$stage"
}
trap bootstrap_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
# The owner receives one exact helper command, never general root utilities.
# The release-stamped SHA-256 selects a protected immutable executable before
# any project code is elevated. Existing foreign files and policies are refused.
helper_root=/var/lib/fased-maintenance
for directory in "$helper_root" "$helper_root/images" "$helper_root/images/$installer_sha256"; do
  if [[ ! -e $directory && ! -L $directory ]]; then /usr/bin/install -d -m 0755 -o root -g root "$directory"; fi
  [[ -d $directory && ! -L $directory && $(/usr/bin/stat -c '%u:%g:%a' "$directory") == 0:0:755 ]] || die 'Foreign maintenance directory; preserving it.'
done
protected_installer="$helper_root/images/$installer_sha256/fased-install"
if [[ ! -e $protected_installer && ! -L $protected_installer ]]; then
  root_curl=(-q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --noproxy '*' --connect-timeout 10 --max-time 180 --max-filesize 134217728 -H 'Accept-Encoding: identity')
  if [[ $development_only == 1 ]]; then
    printf '%s' "$fixture_ca_base64" | /usr/bin/base64 --decode > "$stage/ca.pem"
    root_curl+=(--cacert "$stage/ca.pem")
  fi
  /usr/bin/curl "${root_curl[@]}" --output "$stage/fased-install" "$installer_url"
  read -r actual _ < <(/usr/bin/sha256sum "$stage/fased-install")
  [[ $actual == "$installer_sha256" ]] || die 'Installer checksum differs; nothing was elevated.'
  /usr/bin/install -m 0555 -o root -g root "$stage/fased-install" "$stage/protected-installer"
  /usr/bin/sync -f "$stage/protected-installer"
  /usr/bin/mv -T --no-clobber -- "$stage/protected-installer" "$protected_installer"
  /usr/bin/sync -f "$protected_installer"
  /usr/bin/sync -f "${protected_installer%/*}"
fi
[[ -f $protected_installer && ! -L $protected_installer && $(/usr/bin/stat -c '%u:%g:%a:%h' "$protected_installer") == 0:0:555:1 ]] || die 'Foreign protected maintenance image.'
read -r actual _ < <(/usr/bin/sha256sum "$protected_installer")
[[ $actual == "$installer_sha256" ]] || die 'Protected maintenance checksum differs.'
root_package_instance=''
if [[ $profile == remote && $action == install && $development_only == 0 ]]; then
  # Inspect as the ordinary operator before registering the exact package.
  inspection=$(/usr/sbin/runuser -u "$operator" -- /usr/bin/env -i HOME="$owner_home" USER="$operator" LOGNAME="$operator" PATH=/usr/sbin:/usr/bin:/sbin:/bin "$protected_installer" --inspect-public-package) || die 'Public release verification failed.'
  read -r kind public_packet public_version public_installer extra <<< "$inspection"
  if [[ $kind == native ]]; then
    [[ $public_packet =~ ^[a-f0-9]{64}$ && $public_version == "$entry_version" && $public_installer == "$installer_sha256" && -z ${extra:-} ]] || die 'Public release identity changed; restart installation.'
    read -r identity _ < <(printf '%s\0%s\0remote' "$owner_uid" "$public_packet" | /usr/bin/sha256sum)
    [[ $identity =~ ^[a-f0-9]{64}$ ]] || die 'Cannot determine the package instance.'
    root_package_instance=${identity:0:32}
    maintenance_image=$("$protected_installer" maintenance-package-register --owner "$owner_uid" --location remote --instance "$root_package_instance")
    [[ $maintenance_image == "$protected_installer" ]] || die 'Package maintenance identity differs.'
  elif [[ $kind != legacy || -n ${public_packet:-} ]]; then
    die 'Public release verification returned an invalid result.'
  fi
fi
if [[ -z $root_package_instance && $profile != server ]]; then
  if [[ -e $policy || -L $policy ]]; then
    [[ ! -L $policy && -f $policy_receipt ]] || die 'Foreign bootstrap authorization.'
    read -r expected < "$policy_receipt"
    read -r actual _ < <(/usr/bin/sha256sum "$policy")
    [[ $expected == "$actual" && $(/usr/bin/stat -c '%u:%g:%a' "$policy") == 0:0:440 ]] || die 'Changed bootstrap authorization; preserving it.'
    /usr/bin/rm -- "$policy"
  fi
  maintenance_image=$("$protected_installer" maintenance-register --owner "$owner_uid" --profile "$profile")
  recovered=0
  if [[ $action == update && $maintenance_image != "$protected_installer" ]]; then
    maintenance_image=$("$protected_installer" maintenance-register --owner "$owner_uid" --profile "$profile" --resume)
    [[ $maintenance_image != "$protected_installer" ]] || recovered=1
  fi
  [[ $maintenance_image =~ ^/var/lib/fased-maintenance/images/[a-f0-9]{64}/fased-install$ ]] || die 'Invalid registered maintenance entry.'
  "$maintenance_image" maintenance-bootstrap --owner "$owner_uid"
  if [[ $recovered == 1 ]]; then
    printf 'Update files were checked; app recovery and future updates are not yet verified.\n' >&2
    exit 1
  fi
fi
# Keep the bootstrap lock through the ordinary installer; sanitize root's environment.
if [[ $profile == local ]]; then
  runtime="/run/user/$owner_uid"
  [[ -d $runtime && ! -L $runtime && $(/usr/bin/stat -c '%u:%a' "$runtime") == "$owner_uid:700" ]] || die 'Sign in to the ordinary Local desktop account and retry.'
elif [[ $profile == server ]]; then
  runtime="/run/user/$owner_uid"
  [[ -d $runtime && ! -L $runtime && $(/usr/bin/stat -c '%u:%a' "$runtime") == "$owner_uid:700" ]] || runtime=''
else runtime=''; fi
# Offer one explicitly approved bootstrap public key, never a copied key list.
# The unprivileged wizard confirms it and writes only its own recovery account.
recovery_key=''
if [[ $profile == remote && $action == install && $ssh_user == root && -f /root/.ssh/authorized_keys && ! -L /root/.ssh/authorized_keys ]]; then
  mapfile -t recovery_candidates < <(/usr/bin/awk '($1 == "ssh-ed25519" || $1 == "ssh-rsa" || $1 ~ /^ecdsa-sha2-nistp(256|384|521)$/) && NF >= 2 && !seen[$1 " " $2]++ { print $1 " " $2; if (++count >= 16) exit }' /root/.ssh/authorized_keys)
  if (( ${#recovery_candidates[@]} > 0 )); then
    printf -v recovery_key '%s\n' "${recovery_candidates[@]}"
    recovery_key=${recovery_key%$'\n'}
  fi
fi
body="$(declare -f die owner_install); owner_install"
if [[ $has_tty != 1 ]]; then exec 3</dev/null; fi
/usr/sbin/runuser -u "$operator" -- /usr/bin/env -i HOME="$owner_home" USER="$operator" LOGNAME="$operator" PATH=/usr/sbin:/usr/bin:/sbin:/bin SSH_CONNECTION="${SSH_CONNECTION:-}" FASED_INSTALL_SSH_USER="$ssh_user" FASED_PROTECTED_INSTALLER="$protected_installer" FASED_BOOTSTRAP_RECOVERY_KEY="$recovery_key" action="$action" wallet_instance="${wallet_instance:-}" wallet_network="${wallet_network:-}" server_transport="${server_transport:-}" server_domain="${server_domain:-}" entry_version="$entry_version" installer_url="$installer_url" installer_sha256="$installer_sha256" seed_base64="$seed_base64" seed_sha256="$seed_sha256" fixture_ca_base64="$fixture_ca_base64" development_only="$development_only" listen="$listen" profile="$profile" XDG_RUNTIME_DIR="$runtime" DBUS_SESSION_BUS_ADDRESS="${runtime:+unix:path=$runtime/bus}" /bin/bash -c "$body" <&3

# The root-owned command contains only fixed OS identity/drop operations. All
# Fased/user-owned code starts AFTER runuser; updates still use the installed
# unprivileged controller and do not redownload this entry.
if [[ $profile == remote && -z $root_package_instance && ( $action == install || $action == update ) ]]; then
  launcher=$(/usr/sbin/runuser -u "$operator" -- /usr/bin/env --ignore-environment --chdir="$owner_home" HOME="$owner_home" USER="$operator" LOGNAME="$operator" PATH=/usr/sbin:/usr/bin:/sbin:/bin "/var/lib/fased-app-vps-$owner_uid/fased-app" install-command --remote --path)
  [[ $launcher == "$owner_home/.local/bin/fased" || $launcher == "$owner_home/.local/bin/fased-app" ]] || die 'The installed command identity is missing; preserve state and repair.'
  [[ -f $launcher && ! -L $launcher && $(/usr/bin/stat -c '%u:%a' "$launcher") == "$owner_uid:700" ]] || die 'The installed command changed; preserve state and repair.'
  for directory in /usr /usr/local /usr/local/bin; do
    [[ -d $directory && ! -L $directory && $(/usr/bin/stat -c '%u' "$directory") == 0 ]] || die 'Foreign system command directory.'
    mode=$(/usr/bin/stat -c '%a' "$directory")
    (( (8#$mode & 0022) == 0 )) || die 'Writable system command directory.'
  done
  {
    printf '#!/bin/bash\n# Fased App root-safe command v1\nset -euo pipefail\n'
    printf 'operator=%q\nowner_uid=%q\nowner_gid=%q\nowner_home=%q\nlauncher=%q\n' "$operator" "$owner_uid" "$owner_gid" "$owner_home" "$launcher"
    /usr/bin/cat <<'FASED_ROOT_COMMAND'
if [[ $(/usr/bin/id -u "$operator") != "$owner_uid" || $(/usr/bin/id -g "$operator") != "$owner_gid" || ! -d $owner_home || -L $owner_home || $(/usr/bin/stat -c '%u' "$owner_home") != "$owner_uid" ]]; then
  printf 'Fased installation owner changed; preserve the installation and repair.\n' >&2; exit 1
fi
if [[ $(/usr/bin/id -u) == 0 ]]; then
  exec /usr/sbin/runuser -u "$operator" -- /usr/bin/env --ignore-environment --chdir="$owner_home" HOME="$owner_home" USER="$operator" LOGNAME="$operator" PATH=/usr/sbin:/usr/bin:/sbin:/bin "$launcher" "$@"
fi
[[ $(/usr/bin/id -u) == "$owner_uid" ]] || { printf 'Run Fased as its installation owner.\n' >&2; exit 1; }
exec "$launcher" "$@"
FASED_ROOT_COMMAND
  } > "$stage/command"
  /usr/bin/chmod 0555 "$stage/command"
  read -r command_sha _ < <(/usr/bin/sha256sum "$stage/command")
  published_command=''
  for name in fased fased-app; do
    destination="/usr/local/bin/$name"
    if [[ ! -e $destination && ! -L $destination ]]; then
      /usr/bin/mv --no-clobber --no-target-directory -- "$stage/command" "$destination"
      root_file "$destination" 555 || die 'System command publication conflicted; preserving it.'
      read -r actual_sha _ < <(/usr/bin/sha256sum "$destination")
      [[ $actual_sha == "$command_sha" ]] || die 'System command identity changed; preserving it.'
      published_command=$name; break
    elif root_file "$destination" 555 && /usr/bin/cmp -s "$stage/command" "$destination"; then
      published_command=$name; break
    fi
  done
  [[ -n $published_command ]] || die 'Existing system commands preserved; choose an unused command name.'
  printf 'Installed command: %s — open, status, login, update.\n' "$published_command" >&2
fi
