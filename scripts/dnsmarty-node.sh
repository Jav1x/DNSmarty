#!/usr/bin/env bash
# DNSmarty DNS or proxy agent.
#
#   bash <(curl -Ls https://raw.githubusercontent.com/Jav1x/DNSmarty/main/scripts/dnsmarty-node.sh) @ install
#   dnsmarty-node
set -euo pipefail

VERSION="1.0.0"
REPO="${DNSMARTY_REPO:-Jav1x/DNSmarty}"
OWNER="$(printf '%s' "${REPO%%/*}" | tr '[:upper:]' '[:lower:]')"
IMAGE_REPO="ghcr.io/${OWNER}/dnsmarty"
# Pinned to a release by resolve_image; --image or DNSMARTY_IMAGE set it explicitly.
IMAGE="${DNSMARTY_IMAGE:-}"
WANT_VERSION=""
BIN_PATH="/usr/local/bin/dnsmarty-node"
lang="en"
role=""
key=""
port=""
IN_MENU=0
MENU_SEL=0
CHECK_FAIL=0

if [[ "${1:-}" == "@" ]]; then
  shift
fi

if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  R=$'\033[0m'
  B=$'\033[1m'
  D=$'\033[2m'
  RED=$'\033[1;31m'
  GRN=$'\033[1;32m'
  AMB=$'\033[1;33m'
else
  R="" B="" D="" RED="" GRN="" AMB=""
fi

detect_lang() {
  local src="${DNSMARTY_LANG:-${LANGUAGE:-${LC_ALL:-${LC_MESSAGES:-${LANG:-en}}}}}"
  case "$src" in
    ru*|Ru*|RU*) lang=ru ;;
    *) lang=en ;;
  esac
}

say() {
  if [[ "$lang" == "ru" ]]; then
    printf '%s' "$2"
  else
    printf '%s' "$1"
  fi
}

info() { printf '  %s\n' "$*"; }
ok() { printf '  %s✓%s  %s\n' "$GRN" "$R" "$*"; }
warn() { printf '  %s!%s  %s\n' "$AMB" "$R" "$*"; }
die() { printf '  %s✗%s  %s\n' "$RED" "$R" "$*" >&2; exit 1; }
section() { printf '\n  %s%s%s\n' "$B$AMB" "$*" "$R"; }
row_ok() { printf '  %s✓%s  %s\n' "$GRN" "$R" "$*"; }
row_bad() { printf '  %s✗%s  %s\n' "$RED" "$R" "$*"; CHECK_FAIL=1; }
row_warn() { printf '  %s!%s  %s\n' "$AMB" "$R" "$*"; }

need_root() {
  [[ "$(id -u)" -eq 0 ]] || die "$(say "Run as root." "Запустите от root.")"
}

clear_screen() {
  if [[ -t 1 ]]; then
    clear 2>/dev/null || printf '\033[2J\033[H'
  fi
}

banner() {
  local subtitle=$1
  printf '\n%s╭────────────────────────────────────────────────╮%s\n' "$AMB" "$R"
  printf '%s│%s  %s%-46s%s%s│%s\n' "$AMB" "$R" "$B" "DNSmarty" "$R" "$AMB" "$R"
  printf '%s│%s  %s%-46s%s%s│%s\n' "$AMB" "$R" "$D" "$subtitle" "$R" "$AMB" "$R"
  printf '%s╰────────────────────────────────────────────────╯%s\n' "$AMB" "$R"
}

confirm() {
  local prompt=$1
  local def=${2:-n}
  local hint="[y/N]"
  [[ "$def" == "y" || "$def" == "Y" ]] && hint="[Y/n]"
  printf '  %s%s%s %s%s%s ' "$AMB" "$prompt" "$R" "$D" "$hint" "$R"
  if [[ ! -t 0 ]]; then
    echo
    [[ "$def" == "y" || "$def" == "Y" ]]
    return
  fi
  local answer=""
  IFS= read -r answer || true
  answer=$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')
  if [[ -z "$answer" ]]; then
    [[ "$def" == "y" || "$def" == "Y" ]]
    return
  fi
  [[ "$answer" == "y" || "$answer" == "yes" || "$answer" == "д" || "$answer" == "да" ]]
}

choose() {
  local -a items=("$@")
  local n=${#items[@]}
  local i=0
  local k=""
  if [[ "$n" -eq 0 ]]; then
    MENU_SEL=-1
    return
  fi
  if [[ "$MENU_SEL" -lt 0 || "$MENU_SEL" -ge "$n" ]]; then
    MENU_SEL=0
  fi
  if [[ ! -t 0 || ! -t 1 ]]; then
    for ((i = 0; i < n; i++)); do
      printf '  %s%2d)%s  %s\n' "$GRN" "$((i + 1))" "$R" "${items[$i]}"
    done
    printf '  > '
    IFS= read -r k || exit 0
    if [[ "$k" == "q" || "$k" == "0" ]]; then
      MENU_SEL=-1
      return
    fi
    if [[ "$k" =~ ^[0-9]+$ && "$k" -ge 1 && "$k" -le "$n" ]]; then
      MENU_SEL=$((k - 1))
      return
    fi
    MENU_SEL=-2
    return
  fi
  local drawn=0
  tput civis 2>/dev/null || true
  while true; do
    if [[ "$drawn" -eq 1 ]]; then
      printf '\033[%dA' "$n"
    fi
    drawn=1
    for ((i = 0; i < n; i++)); do
      if [[ "$i" -eq "$MENU_SEL" ]]; then
        printf '\r  %s❯%s %s%2d%s  %s%s%s\033[K\n' "$AMB" "$R" "$AMB" "$((i + 1))" "$R" "$B" "${items[$i]}" "$R"
      else
        printf '\r    %s%2d%s  %s\033[K\n' "$D" "$((i + 1))" "$R" "${items[$i]}"
      fi
    done
    IFS= read -rsn1 k || { tput cnorm 2>/dev/null || true; exit 0; }
    if [[ -z "$k" ]]; then
      break
    fi
    if [[ "$k" == $'\033' ]]; then
      local rest=""
      IFS= read -rsn2 rest || true
      case "$rest" in
        '[A') MENU_SEL=$(((MENU_SEL + n - 1) % n)) ;;
        '[B') MENU_SEL=$(((MENU_SEL + 1) % n)) ;;
      esac
    elif [[ "$k" == "q" || "$k" == "Q" ]]; then
      MENU_SEL=-1
      break
    elif [[ "$k" =~ ^[0-9]$ ]]; then
      if [[ "$k" == "0" ]]; then
        MENU_SEL=-1
        break
      fi
      if [[ "$k" -ge 1 && "$k" -le "$n" ]]; then
        MENU_SEL=$((k - 1))
        break
      fi
    fi
  done
  tput cnorm 2>/dev/null || true
}

pause() {
  [[ "$IN_MENU" -eq 1 ]] || return 0
  printf '\n  %s%s%s ' "$D" "$(say "Press Enter to return" "Enter — вернуться в меню")" "$R"
  IFS= read -r _ || true
  echo
}

run_action() {
  (
    set -euo pipefail
    "$@"
  ) || true
}

app_dir() { printf '/opt/dnsmarty-%s' "$role"; }

role_installed() {
  local r=$1
  [[ -f "/opt/dnsmarty-${r}/compose.yml" ]]
}

have_ss() { command -v ss >/dev/null 2>&1; }

port_in_use() {
  local p=$1 proto=${2:-tcp}
  local flag="t"
  [[ "$proto" == "udp" ]] && flag="u"
  if ! have_ss; then
    return 1
  fi
  [[ -n "$(ss -H -ln"$flag" "sport = :$p" 2>/dev/null || true)" ]]
}

port_owner() {
  local p=$1 proto=${2:-tcp}
  local flag="t"
  [[ "$proto" == "udp" ]] && flag="u"
  ss -H -lnp"$flag" "sport = :$p" 2>/dev/null | head -1 | sed -n 's/.*users:(("\([^"]*\).*/\1/p' || true
}

compose_running() {
  local dir=$1
  [[ -f "$dir/compose.yml" ]] || return 1
  command -v docker >/dev/null 2>&1 || return 1
  local n=0
  n=$(cd "$dir" && docker compose ps -q --status running 2>/dev/null | wc -l | tr -d ' ')
  [[ "${n:-0}" -gt 0 ]]
}

default_port() {
  if [[ "$role" == "dns" ]]; then
    printf '9443'
  else
    printf '9444'
  fi
}

saved_port() {
  local dir=$1 line=""
  line=$(grep -E '^AGENT_ADDR=' "$dir/.env" 2>/dev/null | tail -1 || true)
  line=${line#AGENT_ADDR=}
  line=${line#:}
  if [[ "$line" =~ ^[0-9]+$ ]]; then
    printf '%s' "$line"
  fi
}

check_port() {
  local p=$1 proto=$2 label=$3 ours=$4
  local owner="" hint=""
  if ! have_ss; then
    row_warn "$(say "ss is missing, skipped ${label}" "нет ss, пропуск ${label}")"
    return
  fi
  if port_in_use "$p" "$proto"; then
    owner=$(port_owner "$p" "$proto")
    case "$owner" in
      systemd-resolve | systemd-resolved)
        hint="$(say ". systemd-resolved is listening; move its stub off this port." ". Слушает systemd-resolved, уберите stub с этого порта.")"
        ;;
    esac
    if [[ "$ours" == "1" ]]; then
      row_ok "$(say "${label} :${p}/${proto} used by this install" "${label} :${p}/${proto} занят этой установкой")"
    else
      row_bad "$(say "${label} :${p}/${proto} is busy" "${label} :${p}/${proto} занят")${owner:+ ($owner)}${hint}"
    fi
  else
    row_ok "$(say "${label} :${p}/${proto} is free" "${label} :${p}/${proto} свободен")"
  fi
}

check_firewall_port() {
  local p=$1
  local st="" listed=""
  if command -v ufw >/dev/null 2>&1; then
    st=$(ufw status 2>/dev/null || true)
    if printf '%s\n' "$st" | grep -q 'Status: active'; then
      if printf '%s\n' "$st" | grep -Eq "(^|[[:space:]])${p}/(tcp|udp)|(^|[[:space:]])${p}[[:space:]]"; then
        row_ok "ufw :${p}"
      else
        row_warn "$(say "ufw is active and :${p} is not allowed" "ufw включён, :${p} не открыт")"
      fi
    fi
  fi
  if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld 2>/dev/null; then
    listed=$(firewall-cmd --list-ports 2>/dev/null || true)
    if printf '%s\n' "$listed" | grep -q "${p}/"; then
      row_ok "firewalld :${p}"
    else
      row_warn "$(say "firewalld is active and :${p} is not open" "firewalld включён, :${p} не открыт")"
    fi
  fi
}

audit_common() {
  local code=""
  CHECK_FAIL=0
  if [[ "$(uname -s)" == "Linux" ]]; then
    row_ok "Linux $(uname -m)"
  else
    row_bad "$(say "DNS and proxy need Linux host networking." "DNS и прокси нужны на Linux с host network.")"
  fi
  if command -v curl >/dev/null 2>&1; then
    row_ok "curl"
  else
    row_bad "$(say "curl is missing." "Нет curl.")"
  fi
  if command -v openssl >/dev/null 2>&1; then
    row_ok "openssl"
  else
    row_bad "$(say "openssl is missing. DoT and DoH need a certificate." "Нет openssl. Для DoT и DoH нужен сертификат.")"
  fi
  if [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
    row_ok "/usr/local/bin"
  else
    row_bad "$(say "/usr/local/bin is not writable." "/usr/local/bin недоступен для записи.")"
  fi
  if [[ -d /opt && -w /opt ]]; then
    row_ok "/opt"
  else
    row_bad "$(say "/opt is not writable." "/opt недоступен для записи.")"
  fi
  if command -v docker >/dev/null 2>&1; then
    if docker info >/dev/null 2>&1; then
      row_ok "docker $(docker version -f '{{.Server.Version}}' 2>/dev/null || true)"
    else
      row_bad "$(say "Docker is installed, but the daemon is not reachable." "Docker установлен, но служба не отвечает.")"
    fi
    if docker compose version >/dev/null 2>&1; then
      row_ok "docker compose"
    else
      row_bad "$(say "Docker Compose v2 is missing." "Нет Docker Compose v2.")"
    fi
  else
    row_warn "$(say "Docker is not installed yet." "Docker ещё не установлен.")"
  fi
  if role_installed dns; then
    row_warn "$(say "DNS agent files are already in /opt/dnsmarty-dns." "Файлы DNS-агента уже есть в /opt/dnsmarty-dns.")"
  else
    row_ok "$(say "No DNS agent install." "DNS-агент ещё не установлен.")"
  fi
  if role_installed proxy; then
    row_warn "$(say "Proxy agent files are already in /opt/dnsmarty-proxy." "Файлы прокси уже есть в /opt/dnsmarty-proxy.")"
  else
    row_ok "$(say "No proxy agent install." "Прокси ещё не установлен.")"
  fi
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 8 https://ghcr.io/v2/ 2>/dev/null || true)
  if [[ "$code" == "200" || "$code" == "401" ]]; then
    row_ok "ghcr.io"
  else
    row_warn "$(say "ghcr.io did not answer. Image pull may fail." "ghcr.io не ответил. Скачивание образа может не пройти.")"
  fi
}

audit_role() {
  local data_ours=0 agent_ours=0 dir="" agent="" saved=""
  CHECK_FAIL=0
  dir="$(app_dir)"
  agent="${port:-$(default_port)}"
  if role_installed "$role" && compose_running "$dir"; then
    data_ours=1
    saved=$(saved_port "$dir")
    [[ "$agent" == "$saved" ]] && agent_ours=1
    row_ok "$(say "${role} agent is already installed and running." "Агент ${role} уже установлен и работает.")"
  elif role_installed "$role"; then
    row_warn "$(say "${role} files exist, the container is stopped." "Файлы ${role} есть, контейнер остановлен.")"
  else
    row_ok "$(say "No ${role} install in ${dir}." "В ${dir} агента ${role} ещё нет.")"
  fi
  check_port "$agent" tcp "agent" "$agent_ours"
  if [[ "$role" == "dns" ]]; then
    check_port 53 tcp "dns" "$data_ours"
    check_port 53 udp "dns" "$data_ours"
    check_port 853 tcp "dot" "$data_ours"
    check_port 8443 tcp "doh" "$data_ours"
    check_port 9101 tcp "metrics" "$data_ours"
    check_firewall_port "$agent"
    check_firewall_port 53
    check_firewall_port 853
    check_firewall_port 8443
  else
    check_port 80 tcp "http" "$data_ours"
    check_port 443 tcp "https" "$data_ours"
    check_port 9102 tcp "metrics" "$data_ours"
    check_firewall_port "$agent"
    check_firewall_port 80
    check_firewall_port 443
  fi
}

role_line() {
  local r=$1
  local dir="/opt/dnsmarty-${r}" mark="$D" word="" p=""
  if role_installed "$r" && compose_running "$dir"; then
    mark="$GRN"
    word="$(say "running" "работает")"
  elif role_installed "$r"; then
    mark="$AMB"
    word="$(say "stopped" "остановлен")"
  else
    word="$(say "not installed" "не установлен")"
    printf '  %s●%s  %-6s  %s%s%s\n' "$mark" "$R" "$r" "$D" "$word" "$R"
    return
  fi
  p=$(saved_port "$dir")
  printf '  %s●%s  %-6s  %s%s%s   %s  :%s\n' "$mark" "$R" "$r" "$mark" "$word" "$R" "$dir" "${p:-?}"
}

show_status() {
  role_line dns
  role_line proxy
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    printf '  %s●%s  %-6s  %s\n' "$GRN" "$R" "docker" "$(say "ready" "готов")"
  else
    printf '  %s●%s  %-6s  %s\n' "$RED" "$R" "docker" "$(say "not ready" "не готов")"
  fi
  echo
}

have_docker() {
  command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && docker compose version >/dev/null 2>&1
}

ensure_docker() {
  if have_docker; then
    return
  fi
  if command -v docker >/dev/null 2>&1 && ! docker info >/dev/null 2>&1; then
    die "$(say "Docker is installed, but the daemon is not running." "Docker установлен, но служба не запущена.")"
  fi
  if command -v docker >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
    die "$(say "Docker Compose v2 is missing." "Нет Docker Compose v2.")"
  fi
  warn "$(say "Docker is not installed." "Docker не установлен.")"
  if [[ ! -t 0 && "${DNSMARTY_INSTALL_DOCKER:-}" != "1" ]]; then
    die "$(say "Install Docker, or set DNSMARTY_INSTALL_DOCKER=1." "Установите Docker или задайте DNSMARTY_INSTALL_DOCKER=1.")"
  fi
  confirm "$(say "Install Docker from get.docker.com?" "Установить Docker с get.docker.com?")" "n" \
    || die "$(say "Install Docker and run this again." "Установите Docker и запустите снова.")"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
  have_docker || die "$(say "Docker Compose is still missing." "Docker Compose всё ещё недоступен.")"
}

parse_install_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      dns | proxy) role="$1" ;;
      --role) role="${2:-}"; shift ;;
      --role=*) role="${1#*=}" ;;
      --key) key="${2:-}"; shift ;;
      --key=*) key="${1#*=}" ;;
      --port) port="${2:-}"; shift ;;
      --port=*) port="${1#*=}" ;;
      --image) IMAGE="${2:-}"; shift ;;
      --image=*) IMAGE="${1#*=}" ;;
      --version) WANT_VERSION="${2:-}"; shift ;;
      --version=*) WANT_VERSION="${1#*=}" ;;
      *) die "$(say "Unknown argument: $1" "Неизвестный аргумент: $1")" ;;
    esac
    shift
  done
}

load_saved() {
  local dir="" line=""
  dir="$(app_dir)"
  [[ -f "$dir/.env" ]] || return 1
  if [[ -z "$key" ]]; then
    line=$(grep -E '^NODE_KEY=' "$dir/.env" | tail -1 || true)
    key=${line#NODE_KEY=}
  fi
  if [[ -z "$port" ]]; then
    port=$(saved_port "$dir")
  fi
}

prompt_role() {
  if [[ "$role" == "dns" || "$role" == "proxy" ]]; then
    return
  fi
  if [[ ! -t 0 ]]; then
    die "$(say "Pass --role dns or --role proxy." "Передайте --role dns или --role proxy.")"
  fi
  section "$(say "Role" "Роль")"
  MENU_SEL=0
  choose \
    "$(say "DNS    ports 53, 853, 8443, agent 9443" "DNS    порты 53, 853, 8443, агент 9443")" \
    "$(say "Proxy  ports 80, 443, agent 9444" "Прокси порты 80, 443, агент 9444")"
  case "$MENU_SEL" in
    0) role=dns ;;
    1) role=proxy ;;
    *) return 1 ;;
  esac
}

prompt_key() {
  local entered=""
  if [[ -n "$key" ]]; then
    key="${key//[[:space:]]/}"
  elif [[ ! -t 0 ]]; then
    die "$(say "Pass --key." "Передайте --key.")"
  else
    printf '  %s%s%s ' "$AMB" "$(say "Node key:" "Ключ узла:")" "$R"
    IFS= read -r entered || true
    key="${entered//[[:space:]]/}"
  fi
  [[ "$key" =~ ^[0-9a-fA-F]{64}$ ]] || die "$(say "The key must be 64 hex characters." "Ключ — 64 hex-символа.")"
}

prompt_port() {
  local def="" entered=""
  def=$(default_port)
  if [[ -z "$port" ]]; then
    if [[ ! -t 0 ]]; then
      port=$def
    else
      printf '  %s%s%s ' "$AMB" "$(say "Management port [${def}]:" "Порт управления [${def}]:")" "$R"
      IFS= read -r entered || true
      entered="${entered//[[:space:]]/}"
      if [[ -z "$entered" ]]; then
        port=$def
      else
        port=$entered
      fi
    fi
  fi
  [[ "$port" =~ ^[0-9]+$ ]] || die "$(say "The port must be a number." "Порт должен быть числом.")"
  if [[ "$port" -lt 1 || "$port" -gt 65535 ]]; then
    die "$(say "Port must be between 1 and 65535." "Порт вне диапазона 1–65535.")"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

latest_tag() {
  curl -fsSL --max-time 10 "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true
}

# resolve_image: --version, then --image / DNSMARTY_IMAGE, then the image already in .env
# (unless updating), then the latest release.
resolve_image() {
  local tag="" saved=""
  if [[ -n "$WANT_VERSION" ]]; then
    IMAGE="${IMAGE_REPO}:${WANT_VERSION#v}"
    return
  fi
  [[ -n "$IMAGE" ]] && return
  tag=$(latest_tag)
  if [[ -n "$tag" ]]; then
    IMAGE="${IMAGE_REPO}:${tag#v}"
    return
  fi
  saved=$(env_get DNSMARTY_IMAGE)
  if [[ -n "$saved" ]]; then
    IMAGE="$saved"
    return
  fi
  IMAGE="${IMAGE_REPO}:latest"
  warn "$(say "No release found, using ${IMAGE}." "Релиз не найден, беру ${IMAGE}.")"
}

env_get() {
  local file="" line=""
  file="$(app_dir)/.env"
  [[ -f "$file" ]] || return 0
  line=$(grep -E "^$1=" "$file" | tail -1 || true)
  printf '%s' "${line#*=}"
}

# env_set replaces KEY=... or appends it. Lines the operator added by hand stay.
env_set() {
  local key=$1 value=$2 file="" tmp=""
  file="$(app_dir)/.env"
  tmp=$(mktemp)
  if [[ -f "$file" ]]; then
    grep -vE "^${key}=" "$file" >"$tmp" || true
  fi
  printf '%s=%s\n' "$key" "$value" >>"$tmp"
  cat "$tmp" >"$file"
  rm -f "$tmp"
  chmod 600 "$file"
}

fetch_verified() {
  local name=$1 dest=$2 tag="" tmp="" sums="" want="" got=""
  tag=$(latest_tag)
  tmp=$(mktemp)
  if [[ -n "$tag" ]]; then
    curl -fsSL "https://github.com/${REPO}/releases/download/${tag}/${name}" -o "$tmp" \
      || { rm -f "$tmp"; die "$(say "Download failed: ${name}" "Не скачался ${name}")"; }
    sums=$(curl -fsSL "https://github.com/${REPO}/releases/download/${tag}/SHA256SUMS") \
      || { rm -f "$tmp"; die "$(say "SHA256SUMS is missing in ${tag}." "В ${tag} нет SHA256SUMS.")"; }
    want=$(printf '%s\n' "$sums" | awk -v n="$name" '$2 == n || $2 == "*" n {print $1}')
    got=$(sha256_of "$tmp")
    if [[ -z "$want" || "$want" != "$got" ]]; then
      rm -f "$tmp"
      die "$(say "Checksum mismatch for ${name}." "Контрольная сумма ${name} не совпала.")"
    fi
    ok "$(say "${name} ${tag}, checksum verified" "${name} ${tag}, контрольная сумма совпала")"
  else
    warn "$(say "No release yet: ${name} comes from main without a checksum." "Релиза нет: ${name} берётся из main без проверки суммы.")"
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/scripts/${name}" -o "$tmp" \
      || { rm -f "$tmp"; die "$(say "Download failed: ${name}" "Не скачался ${name}")"; }
  fi
  mv "$tmp" "$dest"
}

write_unit() {
  local dir="" metrics=9102 svc="proxy"
  dir="$(app_dir)"
  [[ "$role" == "dns" ]] && metrics=9101 && svc="dns"
  mkdir -p "$dir/certs" "$dir/state"
  chmod 700 "$dir" "$dir/state"
  if [[ "$role" == "dns" && ( ! -f "$dir/certs/cert.pem" || ! -f "$dir/certs/key.pem" ) ]]; then
    info "$(say "Creating a self-signed certificate for DoT and DoH. Replace certs/*.pem with your own to stop client warnings." "Создаю самоподписанный сертификат для DoT и DoH. Положите свой в certs/*.pem, чтобы клиенты не предупреждали.")"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -sha256 -days 825 -nodes \
      -keyout "$dir/certs/key.pem" -out "$dir/certs/cert.pem" \
      -subj /CN=dnsmarty-dns -addext "subjectAltName=DNS:dnsmarty-dns" 2>/dev/null
    chmod 600 "$dir/certs/key.pem"
  fi
  # Managed keys are set one by one; anything else in .env survives updates.
  umask 077
  env_set NODE_KEY "$key"
  env_set AGENT_ADDR ":${port}"
  env_set DNSMARTY_IMAGE "$IMAGE"
  env_set METRICS_ADDR "127.0.0.1:${metrics}"
  env_set STATE_DIR /var/lib/dnsmarty
  if [[ "$role" == "dns" ]]; then
    env_set TLS_CERT_FILE /certs/cert.pem
    env_set TLS_KEY_FILE /certs/key.pem
    [[ -n "$(env_get DNS_ADDR)" ]] || env_set DNS_ADDR :53
    [[ -n "$(env_get DOT_ADDR)" ]] || env_set DOT_ADDR :853
    [[ -n "$(env_get DOH_ADDR)" ]] || env_set DOH_ADDR :8443
  else
    [[ -n "$(env_get PROXY_HTTP_ADDR)" ]] || env_set PROXY_HTTP_ADDR :80
    [[ -n "$(env_get PROXY_HTTPS_ADDR)" ]] || env_set PROXY_HTTPS_ADDR :443
  fi
  [[ -n "$(env_get LOG_LEVEL)" ]] || env_set LOG_LEVEL info
  local volumes="      - ./state:/var/lib/dnsmarty" mem="1g" pids=1024
  if [[ "$role" == "dns" ]]; then
    volumes=$'      - ./certs:/certs:ro\n'"$volumes"
    mem="512m"
    pids=512
  fi
  cat >"$dir/compose.yml" <<EOF
name: dnsmarty-${svc}

services:
  ${svc}:
    image: \${DNSMARTY_IMAGE}
    command: ["${svc}"]
    # Binding ports below 1024 needs NET_BIND_SERVICE; Docker grants added capabilities to uid 0 only.
    user: "0:0"
    network_mode: host
    env_file: .env
    read_only: true
    security_opt: ["no-new-privileges:true"]
    cap_drop: [ALL]
    cap_add: [NET_BIND_SERVICE]
    tmpfs: [/tmp]
    volumes:
${volumes}
    healthcheck:
      test: ["CMD", "/dnsmarty", "healthcheck", "--url", "http://127.0.0.1:${metrics}/healthz"]
      interval: 15s
      timeout: 5s
      retries: 3
    mem_limit: ${mem}
    pids_limit: ${pids}
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    restart: unless-stopped
EOF
}

bring_up() {
  local digest=""
  (cd "$(app_dir)" && docker compose pull && docker compose up -d --remove-orphans)
  digest=$(docker image inspect --format '{{join .RepoDigests ", "}}' "$IMAGE" 2>/dev/null || true)
  [[ -z "$digest" ]] || info "$(say "Pulled: ${digest}" "Скачан: ${digest}")"
}

wait_local() {
  local p=$1 i=0
  for ((i = 0; i < 20; i++)); do
    if (echo >/dev/tcp/127.0.0.1/"$p") >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

print_access() {
  echo
  printf '%s╭────────────────────────────────────────────────╮%s\n' "$GRN" "$R"
  printf '%s│%s  %-46s%s│%s\n' "$GRN" "$R" "${role} agent ready" "$GRN" "$R"
  printf '%s╰────────────────────────────────────────────────╯%s\n' "$GRN" "$R"
  info "$(app_dir)"
  info "$(say "Management port ${port}. In the panel, press Next." "Порт управления ${port}. В панели нажмите «Далее».")"
  if wait_local "$port"; then
    ok "$(say "Agent port ${port} accepts connections." "Порт агента ${port} принимает соединения.")"
  else
    warn "$(say "Agent port ${port} is not open yet. Check logs." "Порт агента ${port} ещё не открыт. Смотрите логи.")"
  fi
}

cmd_install() {
  need_root
  local hinted=""
  if [[ "$IN_MENU" -ne 1 ]]; then
    banner "agent  ${VERSION}"
  fi
  parse_install_args "$@"
  section "$(say "Checks" "Проверки")"
  audit_common
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before installing." "Исправьте ошибки перед установкой.")"
  prompt_role || return 0
  hinted="$port"
  if [[ -z "$port" ]] && role_installed "$role"; then
    port=$(saved_port "$(app_dir)")
  fi
  section "$(say "Ports" "Порты")"
  audit_role
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before installing." "Исправьте ошибки перед установкой.")"
  [[ -n "$hinted" ]] || port=""
  section "$(say "Setup" "Настройка")"
  if role_installed "$role"; then
    warn "$(say "The ${role} agent is already installed in $(app_dir)." "Агент ${role} уже стоит в $(app_dir).")"
    if [[ -z "$key" && -z "$hinted" ]]; then
      if confirm "$(say "Update the image and keep the current key and port?" "Обновить образ и оставить текущие ключ и порт?")" "y"; then
        load_saved || die "$(say "Saved .env has no key." "В сохранённом .env нет ключа.")"
        prompt_key
        prompt_port
        finish_install
        return
      fi
      if ! confirm "$(say "Enter a new key and port?" "Ввести новый ключ и порт?")" "n"; then
        info "$(say "Left the current agent as it is." "Текущий агент не изменён.")"
        return
      fi
      key=""
      port=""
    fi
  fi
  prompt_key
  prompt_port
  if [[ "$port" != "${hinted:-$(default_port)}" ]]; then
    section "$(say "Ports" "Порты")"
    audit_role
    echo
    [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before installing." "Исправьте ошибки перед установкой.")"
  fi
  finish_install
}

finish_install() {
  ensure_docker
  resolve_image
  write_unit
  cmd_install_script
  bring_up
  print_access
}

pick_installed_role() {
  if [[ "$role" == "dns" || "$role" == "proxy" ]]; then
    role_installed "$role" || die "$(say "The ${role} agent is not installed." "Агент ${role} не установлен.")"
    return
  fi
  local d=0 p=0
  role_installed dns && d=1
  role_installed proxy && p=1
  if [[ "$d" -eq 0 && "$p" -eq 0 ]]; then
    die "$(say "No agent is installed." "Агент не установлен.")"
  fi
  if [[ "$d" -eq 1 && "$p" -eq 1 ]]; then
    if [[ ! -t 0 ]]; then
      die "$(say "Both agents are installed. Example: dnsmarty-node dns logs" "Установлены оба агента. Пример: dnsmarty-node dns logs")"
    fi
    section "$(say "Which agent?" "Какой агент?")"
    MENU_SEL=0
    choose "DNS" "Proxy"
    case "$MENU_SEL" in
      0) role=dns ;;
      1) role=proxy ;;
      *) return 1 ;;
    esac
    return
  fi
  if [[ "$d" -eq 1 ]]; then
    role=dns
  else
    role=proxy
  fi
}

cmd_update() {
  need_root
  parse_install_args "$@"
  section "$(say "Checks" "Проверки")"
  audit_common
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before continuing." "Исправьте ошибки перед продолжением.")"
  pick_installed_role
  load_saved || die "$(say "Saved .env has no key." "В сохранённом .env нет ключа.")"
  section "$(say "Ports" "Порты")"
  audit_role
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before continuing." "Исправьте ошибки перед продолжением.")"
  section "$(say "Setup" "Настройка")"
  prompt_key
  prompt_port
  finish_install
}

cmd_compose() {
  need_root
  pick_installed_role
  (cd "$(app_dir)" && docker compose "$@")
}

cmd_check() {
  need_root
  section "$(say "Checks" "Проверки")"
  audit_common
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks, then run this again." "Исправьте ошибки и запустите снова.")"
  if [[ "$role" != "dns" && "$role" != "proxy" ]]; then
    if role_installed dns && ! role_installed proxy; then
      role=dns
    elif role_installed proxy && ! role_installed dns; then
      role=proxy
    else
      prompt_role || return 0
    fi
  fi
  if [[ -z "$port" ]] && role_installed "$role"; then
    port=$(saved_port "$(app_dir)")
  fi
  [[ -n "$port" ]] || port=$(default_port)
  section "$(say "Ports" "Порты")"
  audit_role
  echo
  if [[ "$CHECK_FAIL" -eq 0 ]]; then
    ok "$(say "Nothing is blocking install." "Установке ничего не мешает.")"
  else
    die "$(say "Fix the failed checks, then run this again." "Исправьте ошибки и запустите снова.")"
  fi
}

cmd_uninstall() {
  need_root
  local other=0
  pick_installed_role || return 0
  if [[ "$role" == "dns" ]] && role_installed proxy; then
    other=1
  fi
  if [[ "$role" == "proxy" ]] && role_installed dns; then
    other=1
  fi
  confirm "$(say "Stop the ${role} agent? Files in $(app_dir) stay." "Остановить агент ${role}? Файлы в $(app_dir) останутся.")" "n" \
    || return 0
  (cd "$(app_dir)" && docker compose down) || true
  if [[ "$other" -eq 0 ]]; then
    rm -f "$BIN_PATH"
  fi
  ok "$(say "Stopped." "Остановлено.")"
}

cmd_install_script() {
  need_root
  if [[ -f "${BASH_SOURCE[0]}" ]] && cp "${BASH_SOURCE[0]}" "$BIN_PATH" 2>/dev/null; then
    :
  else
    fetch_verified dnsmarty-node.sh "$BIN_PATH"
  fi
  chmod 755 "$BIN_PATH"
  ok "$(say "Command installed: dnsmarty-node" "Команда установлена: dnsmarty-node")"
}

menu() {
  IN_MENU=1
  need_root
  while true; do
    clear_screen
    banner "agent  ${VERSION}"
    echo
    show_status
    printf '  %s%s%s\n\n' "$D" "$(say "Arrows or a number, Enter to open, q to quit." "Стрелки или цифра, Enter открывает, q выходит.")" "$R"
    if role_installed dns || role_installed proxy; then
      MENU_SEL=2
    else
      MENU_SEL=0
    fi
    choose \
      "$(say "Install agent" "Установить агент")" \
      "$(say "Update image" "Обновить образ")" \
      "$(say "Check ports and access" "Проверить порты и доступ")" \
      "$(say "Status" "Статус")" \
      "$(say "Logs" "Логи")" \
      "$(say "Restart" "Перезапуск")" \
      "$(say "Stop" "Остановить")" \
      "$(say "Exit" "Выход")"
    case "$MENU_SEL" in
      -1 | 7) exit 0 ;;
      -2) warn "1-8 / q" ;;
      0) run_action cmd_install; pause ;;
      1) run_action cmd_update; pause ;;
      2) role=""; port=""; run_action cmd_check; pause ;;
      3) role=""; run_action cmd_compose ps; pause ;;
      4) role=""; run_action cmd_compose logs --tail 80; pause ;;
      5) role=""; run_action cmd_compose restart; pause ;;
      6) role=""; run_action cmd_uninstall; pause ;;
    esac
  done
}

usage() {
  banner "agent  ${VERSION}"
  echo
  if [[ "$lang" == "ru" ]]; then
    cat <<EOF
  dnsmarty-node
  dnsmarty-node install --role dns --key KEY --port 9443
  dnsmarty-node dns logs

  Без аргументов открывается меню. Язык берётся из локали системы.
EOF
  else
    cat <<EOF
  dnsmarty-node
  dnsmarty-node install --role dns --key KEY --port 9443
  dnsmarty-node dns logs

  No arguments opens the menu. Language follows the system locale.
EOF
  fi
}

main() {
  detect_lang
  if [[ "${1:-}" == "dns" || "${1:-}" == "proxy" ]]; then
    role=$1
    shift
    if [[ $# -eq 0 ]]; then
      cmd_install
      return
    fi
  fi
  local cmd="${1:-menu}"
  case "$cmd" in
    menu) menu ;;
    install)
      shift
      cmd_install "$@"
      ;;
    update) shift; cmd_update "$@" ;;
    check) shift || true; cmd_check ;;
    up | down | restart) shift; cmd_compose "$cmd" "$@" ;;
    status) cmd_compose ps ;;
    logs) shift; cmd_compose logs "$@" ;;
    uninstall) cmd_uninstall ;;
    install-script) need_root; cmd_install_script ;;
    help | -h | --help) usage ;;
    *) die "$(say "Unknown command: $cmd" "Неизвестная команда: $cmd")" ;;
  esac
}

trap 'tput cnorm 2>/dev/null || true' EXIT INT
main "$@"
