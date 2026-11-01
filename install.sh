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
profile='' operator='' action=install listen='127.0.0.1:7331'
while (($#)); do
  case "$1" in
    --local) profile=local; shift ;;
    --remote) profile=remote; shift ;;
    --connect) profile=connect; shift ;;
    --open|--start|--stop|--status|--recover|--update|--setup)
      [[ $action == install ]] || die 'Choose one management action.'
      action=${1#--}; shift ;;
    --operator) (($# >= 2)) || die 'Missing operator.'; operator=$2; shift 2 ;;
    --listen) (($# >= 2)) || die 'Missing listener.'; listen=$2; shift 2 ;;
    --help|-h) printf 'Usage: install.sh [--local|--remote|--connect] [--open|--start|--stop|--status|--recover|--update|--setup] [--operator USER] [--listen 127.0.0.1:PORT]\n'; exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done
[[ $entry_version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9.-]+)?$ && $installer_sha256 =~ ^[a-f0-9]{64}$ && $seed_sha256 =~ ^[a-f0-9]{64}$ ]] || die 'No trusted installer release is published yet.'
[[ -z $operator || $operator =~ ^[a-z_][a-z0-9_-]{0,30}$ && $operator != root ]] || die 'Invalid ordinary operator.'
[[ $listen =~ ^127\.0\.0\.1:([0-9]{4,5})$ ]] || die 'Use a fixed loopback port.'
port=${BASH_REMATCH[1]}
[[ $port == "$((10#$port))" ]] && ((10#$port >= 1024 && 10#$port <= 65535)) || die 'Invalid listener port.'
[[ -r /etc/os-release ]] || die 'OS release metadata is missing.'
# Read only fixed data fields; never evaluate os-release as shell code.
ID='' VERSION_ID=''
while IFS='=' read -r key value; do
  case "$key" in ID|VERSION_ID)
    value=${value%\"}; value=${value#\"}; value=${value%\'}; value=${value#\'}
    [[ $value =~ ^[a-z0-9.]+$ ]] || die 'Invalid OS release metadata.'
    if [[ $key == ID ]]; then [[ -z $ID ]] || die 'Ambiguous OS identity.'; ID=$value
    else [[ -z $VERSION_ID ]] || die 'Ambiguous OS version.'; VERSION_ID=$value; fi ;;
  esac
done < /etc/os-release
@PLATFORM_BINDINGS@
case $(/usr/bin/uname -m) in x86_64|amd64) target=linux-amd64 ;; aarch64|arm64) target=linux-arm64 ;; *) die 'No verified package for this CPU.' ;; esac
@TARGET_BINDINGS@
if [[ $development_only == 0 ]]; then
  [[ $installer_url == "https://github.com/fased-ai/fased-app/releases/download/v${entry_version}/fased-install-${target}" && -z $fixture_ca_base64 ]] || die 'Untrusted installer source.'
elif [[ $development_only == 1 ]]; then
  [[ $installer_url =~ ^https://127\.0\.0\.1:[0-9]+/fased-install$ && -n $fixture_ca_base64 ]] || die 'Invalid controlled fixture.'
  printf 'Fased: unpublished disposable-trust test installer.\n' >&2
else die 'Invalid installer binding.'; fi
[[ -r /dev/tty && -w /dev/tty ]] || die 'Run this command in an interactive terminal.'
exec 3<>/dev/tty
if [[ -z $profile && $action == install ]]; then
  printf 'Fased\n  Local\n  Remote\n  Connect\n  Update\nChoose [local/remote/connect/update, default local]: ' >&3
  IFS= read -r choice <&3
  case "${choice,,}" in "") profile=local ;; local|remote|connect) profile=${choice,,} ;; update) action=update ;; *) die 'Choose Local, Remote, Connect or Update.' ;; esac
fi
if [[ -z $profile ]]; then
  printf 'Installation [local/remote]: ' >&3
  IFS= read -r profile <&3; profile=${profile,,}
  [[ $profile == local || $profile == remote ]] || die 'Choose Local or Remote.'
fi
if [[ $profile == remote ]]; then
@REMOTE_PLATFORMS@
fi
if [[ $(/usr/bin/id -u) == 0 && $profile != remote && -z ${SUDO_USER:-} && -z $operator ]]; then
  die 'Run Local/Connect from your ordinary computer account. For a VPS, choose Remote.'
fi

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
  if [[ $development_only == 1 ]]; then
    printf '%s' "$fixture_ca_base64" | /usr/bin/base64 --decode > "$work/ca.pem"
    curl_args+=(--cacert "$work/ca.pem")
    verifier_args+=(--development --tls-ca-file "$work/ca.pem")
  fi
  printf 'Downloading and verifying Fased…\n' >&2
  /usr/bin/curl "${curl_args[@]}" --output "$work/fased-install" "$installer_url"
  read -r actual _ < <(/usr/bin/sha256sum "$work/fased-install")
  [[ $actual == "$installer_sha256" ]] || die 'Installer checksum differs; nothing was executed.'
  /usr/bin/chmod 0500 "$work/fased-install"
  "$work/fased-install" "${verifier_args[@]}" <&3
}

if [[ $(/usr/bin/id -u) != 0 ]]; then
  [[ -z $operator ]] || die '--operator is only for the root bootstrap.'
  [[ -x /usr/bin/sudo ]] || die 'This account needs administrator preparation (sudo).'
  missing=()
  for package in ca-certificates curl; do
    [[ $(/usr/bin/dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true) == 'install ok installed' ]] || missing+=("$package")
  done
  if ((${#missing[@]})); then
    printf 'Preparing required download tools with administrator approval…\n' >&2
    /usr/bin/sudo -- /usr/bin/apt-get update
    /usr/bin/sudo -- /usr/bin/apt-get install -y --no-remove --no-install-recommends "${missing[@]}"
  fi

  owner_install
  exit
fi

# Only fixed Ubuntu utilities run here. Never execute downloaded/project Go as root.
printf 'Preparing Ubuntu for Fased…\n' >&2
missing=()
for package in ca-certificates curl sudo util-linux; do
  [[ $(/usr/bin/dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true) == 'install ok installed' ]] || missing+=("$package")
done
if ((${#missing[@]})); then
  /usr/bin/apt-get update
  /usr/bin/apt-get install -y --no-remove --no-install-recommends "${missing[@]}"
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
if [[ -e $policy || -L $policy ]]; then
  [[ ! -L $policy && -f $policy_receipt ]] || die 'Foreign bootstrap authorization.'
  read -r expected < "$policy_receipt"
  read -r actual _ < <(/usr/bin/sha256sum "$policy")
  [[ $expected == "$actual" && $(/usr/bin/stat -c '%u:%g:%a' "$policy") == 0:0:440 ]] || die 'Changed bootstrap authorization; preserving it.'
  /usr/bin/rm -- "$policy"
fi
stage=$(/usr/bin/mktemp -d "$state/stage.XXXXXXXX")
bootstrap_cleanup() {
  if [[ -f $policy ]] && /usr/bin/cmp -s "$policy" "$stage/authorization"; then /usr/bin/rm -- "$policy"; fi
  /usr/bin/rm -rf -- "$stage"
}
trap bootstrap_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
expires=$(/usr/bin/date -u -d '+45 minutes' '+%Y%m%d%H%M%SZ')
printf '# Fased temporary bootstrap OS administration; no app runtime rights.\nDefaults:%s verifypw=never\n' "$operator" > "$stage/authorization"
for command in /usr/bin/true /usr/bin/apt-get /usr/bin/cat /usr/bin/cmp /usr/bin/find /usr/bin/flock /usr/bin/getent /usr/bin/id /usr/bin/install /usr/bin/ln /usr/bin/mkdir /usr/bin/mv /usr/bin/passwd /usr/bin/pkcheck /usr/bin/rm /usr/bin/setpriv /usr/bin/stat /usr/bin/sync /usr/bin/systemctl /usr/bin/tailscale /usr/sbin/apparmor_parser /usr/sbin/useradd; do
  printf '%s ALL=(root) NOTAFTER=%s NOPASSWD: %s\n' "$operator" "$expires" "$command" >> "$stage/authorization"
done
/usr/bin/chmod 0440 "$stage/authorization"
/usr/sbin/visudo -c -f "$stage/authorization" >/dev/null
read -r policy_sha _ < <(/usr/bin/sha256sum "$stage/authorization")
printf '%s\n' "$policy_sha" > "$stage/policy.sha256"
/usr/bin/mv -T -- "$stage/policy.sha256" "$policy_receipt"
/usr/bin/ln "$stage/authorization" "$policy"
# Keep the bootstrap lock through the ordinary installer; sanitize root's environment.
if [[ $profile == local ]]; then
  runtime="/run/user/$owner_uid"
  [[ -d $runtime && ! -L $runtime && $(/usr/bin/stat -c '%u:%a' "$runtime") == "$owner_uid:700" ]] || die 'Sign in to the ordinary Local desktop account and retry.'
else runtime=''; fi
body="$(declare -f die owner_install); owner_install"
/usr/sbin/runuser -u "$operator" -- /usr/bin/env -i HOME="$owner_home" USER="$operator" LOGNAME="$operator" PATH=/usr/sbin:/usr/bin:/sbin:/bin SSH_CONNECTION="${SSH_CONNECTION:-}" FASED_INSTALL_SSH_USER="$ssh_user" FASED_INSTALL_ROOT_ENTRY=1 action="$action" entry_version="$entry_version" installer_url="$installer_url" installer_sha256="$installer_sha256" seed_base64="$seed_base64" seed_sha256="$seed_sha256" fixture_ca_base64="$fixture_ca_base64" development_only="$development_only" listen="$listen" profile="$profile" XDG_RUNTIME_DIR="$runtime" DBUS_SESSION_BUS_ADDRESS="${runtime:+unix:path=$runtime/bus}" /bin/bash -c "$body" <&3

# The root-owned command contains only fixed OS identity/drop operations. All
# Fased/user-owned code starts AFTER runuser; updates still use the installed
# unprivileged controller and do not redownload this entry.
if [[ $profile == remote ]]; then
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
