#!/usr/bin/env bash
# DNSmarty panel: install, update, and manage the control plane.
#
#   bash <(curl -Ls https://raw.githubusercontent.com/Jav1x/DNSmarty/main/scripts/dnsmarty.sh) @ install
#   dnsmarty
set -euo pipefail

VERSION="1.0.0"
REPO="${DNSMARTY_REPO:-Jav1x/DNSmarty}"
OWNER="$(printf '%s' "${REPO%%/*}" | tr '[:upper:]' '[:lower:]')"
IMAGE_REPO="ghcr.io/${OWNER}/dnsmarty"
# Resolved to a release tag by resolve_image; DNSMARTY_IMAGE pins it explicitly.
IMAGE="${DNSMARTY_IMAGE:-}"
WANT_VERSION=""
PANEL_DOMAIN=""
APP_DIR="${DNSMARTY_DIR:-/opt/dnsmarty}"
BIN_PATH="/usr/local/bin/dnsmarty"
lang="en"
IN_MENU=0
MENU_SEL=0
CHECK_FAIL=0
ENV_CREATED=0

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

have_ss() { command -v ss >/dev/null 2>&1; }

port_in_use() {
  local port=$1 proto=${2:-tcp}
  local flag="t"
  [[ "$proto" == "udp" ]] && flag="u"
  if ! have_ss; then
    return 1
  fi
  [[ -n "$(ss -H -ln"$flag" "sport = :$port" 2>/dev/null || true)" ]]
}

port_owner() {
  local port=$1 proto=${2:-tcp}
  local flag="t"
  [[ "$proto" == "udp" ]] && flag="u"
  ss -H -lnp"$flag" "sport = :$port" 2>/dev/null | head -1 | sed -n 's/.*users:(("\([^"]*\).*/\1/p' || true
}

panel_installed() { [[ -f "$APP_DIR/compose.yml" ]]; }

compose_running() {
  local dir=$1
  [[ -f "$dir/compose.yml" ]] || return 1
  command -v docker >/dev/null 2>&1 || return 1
  local n=0
  n=$(cd "$dir" && docker compose ps -q --status running 2>/dev/null | wc -l | tr -d ' ')
  [[ "${n:-0}" -gt 0 ]]
}

https_port() {
  local p=7443 line=""
  if [[ -f "$APP_DIR/.env" ]]; then
    line=$(grep -E '^PANEL_HTTPS_PORT=' "$APP_DIR/.env" | tail -1 || true)
    line=${line#PANEL_HTTPS_PORT=}
    [[ "$line" =~ ^[0-9]+$ ]] && p=$line
  fi
  printf '%s' "$p"
}

published_https() {
  local line=""
  command -v docker >/dev/null 2>&1 || return 0
  panel_installed || return 0
  line=$(cd "$APP_DIR" && docker compose port caddy 443 2>/dev/null || true)
  line=${line##*:}
  printf '%s' "$line"
}

check_port() {
  local port=$1 proto=$2 label=$3 ours=$4
  local owner=""
  if ! have_ss; then
    row_warn "$(say "ss is missing, skipped ${label}" "нет ss, пропуск ${label}")"
    return
  fi
  if port_in_use "$port" "$proto"; then
    owner=$(port_owner "$port" "$proto")
    if [[ "$ours" == "1" ]]; then
      row_ok "$(say "${label} :${port}/${proto} used by this install" "${label} :${port}/${proto} занят этой установкой")"
    else
      row_bad "$(say "${label} :${port}/${proto} is busy" "${label} :${port}/${proto} занят")${owner:+ ($owner)}"
    fi
  else
    row_ok "$(say "${label} :${port}/${proto} is free" "${label} :${port}/${proto} свободен")"
  fi
}

check_firewall_port() {
  local port=$1
  local st="" listed=""
  if command -v ufw >/dev/null 2>&1; then
    st=$(ufw status 2>/dev/null || true)
    if printf '%s\n' "$st" | grep -q 'Status: active'; then
      if printf '%s\n' "$st" | grep -Eq "(^|[[:space:]])${port}/(tcp|udp)|(^|[[:space:]])${port}[[:space:]]"; then
        row_ok "ufw :${port}"
      else
        row_warn "$(say "ufw is active and :${port} is not allowed" "ufw включён, :${port} не открыт")"
      fi
    fi
  fi
  if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld 2>/dev/null; then
    listed=$(firewall-cmd --list-ports --list-services 2>/dev/null || true)
    if printf '%s\n' "$listed" | grep -q "${port}/"; then
      row_ok "firewalld :${port}"
    else
      row_warn "$(say "firewalld is active and :${port} is not open" "firewalld включён, :${port} не открыт")"
    fi
  fi
}

check_disk() {
  local kb=""
  kb=$(df -Pk "$APP_DIR" 2>/dev/null | awk 'NR==2 {print $4}' || true)
  if [[ -z "$kb" ]]; then
    kb=$(df -Pk / 2>/dev/null | awk 'NR==2 {print $4}' || true)
  fi
  if [[ -z "$kb" ]]; then
    row_warn "$(say "Could not read free disk space." "Не удалось прочитать свободное место.")"
    return
  fi
  if [[ "$kb" -lt 1048576 ]]; then
    row_warn "$(say "Less than 1 GiB free." "Свободно меньше 1 ГиБ.")"
  else
    row_ok "$(say "Disk space is enough." "Места на диске достаточно.")"
  fi
}

check_registry() {
  local code=""
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 8 https://ghcr.io/v2/ 2>/dev/null || true)
  if [[ "$code" == "200" || "$code" == "401" ]]; then
    row_ok "ghcr.io"
  else
    row_warn "$(say "ghcr.io did not answer. Image pull may fail." "ghcr.io не ответил. Скачивание образа может не пройти.")"
  fi
}

audit_tools() {
  local data_ours=0 https_ours=0 hp="" published=""
  CHECK_FAIL=0
  if [[ "$(uname -s)" == "Linux" ]]; then
    row_ok "Linux $(uname -m)"
  else
    row_warn "$(say "This host is not Linux. Compose still expects a Linux Docker engine." "Это не Linux. Compose рассчитан на Linux Docker.")"
  fi
  if command -v curl >/dev/null 2>&1; then
    row_ok "curl"
  else
    row_bad "$(say "curl is missing." "Нет curl.")"
  fi
  if command -v openssl >/dev/null 2>&1; then
    row_ok "openssl"
  else
    row_bad "$(say "openssl is missing." "Нет openssl.")"
  fi
  if [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
    row_ok "/usr/local/bin"
  else
    row_bad "$(say "/usr/local/bin is not writable." "/usr/local/bin недоступен для записи.")"
  fi
  if [[ -d "$APP_DIR" && -w "$APP_DIR" ]] || [[ ! -d "$APP_DIR" && -w "$(dirname "$APP_DIR")" ]]; then
    row_ok "$APP_DIR"
  else
    row_bad "$(say "${APP_DIR} is not writable." "${APP_DIR} недоступен для записи.")"
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
  if panel_installed; then
    if compose_running "$APP_DIR"; then
      data_ours=1
      row_ok "$(say "Panel is already installed and running." "Панель уже установлена и работает.")"
    else
      row_warn "$(say "Panel files exist, containers are stopped." "Файлы панели есть, контейнеры остановлены.")"
    fi
  else
    row_ok "$(say "No panel install in ${APP_DIR}." "В ${APP_DIR} панели ещё нет.")"
  fi
  hp=$(https_port)
  published=$(published_https)
  if [[ "$data_ours" -eq 1 && ( "$published" == "$hp" || "$(port_owner "$hp" tcp)" == "docker-proxy" ) ]]; then
    https_ours=1
  fi
  check_port "$hp" tcp "https" "$https_ours"
  check_port 8080 tcp "panel" "$data_ours"
  check_port 9100 tcp "metrics" "$data_ours"
  check_firewall_port "$hp"
  check_disk
  check_registry
}

show_status() {
  local hp="" state=""
  hp=$(https_port)
  if panel_installed && compose_running "$APP_DIR"; then
    state="$(say "running" "работает")"
    printf '  %s●%s  panel   %s%s%s   %s\n' "$GRN" "$R" "$GRN" "$state" "$R" "$APP_DIR"
  elif panel_installed; then
    state="$(say "stopped" "остановлена")"
    printf '  %s●%s  panel   %s%s%s   %s\n' "$AMB" "$R" "$AMB" "$state" "$R" "$APP_DIR"
  else
    state="$(say "not installed" "не установлена")"
    printf '  %s●%s  panel   %s%s%s\n' "$D" "$R" "$D" "$state" "$R"
  fi
  if port_in_use "$hp" tcp; then
    printf '  %s●%s  :%s    %s\n' "$AMB" "$R" "$hp" "$(say "in use" "занят") $(port_owner "$hp" tcp)"
  else
    printf '  %s●%s  :%s    %s\n' "$GRN" "$R" "$hp" "$(say "free" "свободен")"
  fi
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    printf '  %s●%s  docker  %s\n' "$GRN" "$R" "$(say "ready" "готов")"
  else
    printf '  %s●%s  docker  %s\n' "$RED" "$R" "$(say "not ready" "не готов")"
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

rand() { openssl rand -hex 24; }

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# latest_tag prints the newest GitHub release tag, or nothing when there is none.
latest_tag() {
  curl -fsSL --max-time 10 "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true
}

# resolve_image pins the image to a release tag: --version, then DNSMARTY_IMAGE,
# then the latest release. :latest is only the fallback when no release exists.
resolve_image() {
  local tag=""
  if [[ -n "$WANT_VERSION" ]]; then
    IMAGE="${IMAGE_REPO}:${WANT_VERSION#v}"
    return
  fi
  [[ -n "$IMAGE" ]] && return
  tag=$(latest_tag)
  if [[ -n "$tag" ]]; then
    IMAGE="${IMAGE_REPO}:${tag#v}"
  else
    IMAGE="${IMAGE_REPO}:latest"
    warn "$(say "No release found, using ${IMAGE}." "Релиз не найден, беру ${IMAGE}.")"
  fi
}

env_get() {
  local line=""
  [[ -f "$APP_DIR/.env" ]] || return 0
  line=$(grep -E "^$1=" "$APP_DIR/.env" | tail -1 || true)
  printf '%s' "${line#*=}"
}

# env_set replaces KEY=... in .env or appends it; other lines stay untouched.
env_set() {
  local key=$1 value=$2 file="$APP_DIR/.env" tmp=""
  tmp=$(mktemp)
  if [[ -f "$file" ]]; then
    grep -vE "^${key}=" "$file" >"$tmp" || true
  fi
  printf '%s=%s\n' "$key" "$value" >>"$tmp"
  cat "$tmp" >"$file"
  rm -f "$tmp"
  chmod 600 "$file"
}

env_default() {
  [[ -n "$(env_get "$1")" ]] || env_set "$1" "$2"
}

# fetch_verified downloads a release asset and checks it against the release SHA256SUMS.
# Without a release it falls back to the main branch and says so.
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

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version) WANT_VERSION="${2:-}"; shift ;;
      --version=*) WANT_VERSION="${1#*=}" ;;
      --domain) PANEL_DOMAIN="${2:-}"; shift ;;
      --domain=*) PANEL_DOMAIN="${1#*=}" ;;
      *) die "$(say "Unknown argument: $1" "Неизвестный аргумент: $1")" ;;
    esac
    shift
  done
}

# prompt_domain asks for an optional public name. With one, Caddy gets a Let's Encrypt
# certificate; that needs ports 80 and 443 on this host.
prompt_domain() {
  PANEL_DOMAIN="${PANEL_DOMAIN:-${DNSMARTY_DOMAIN:-}}"
  if [[ -z "$PANEL_DOMAIN" && -t 0 ]]; then
    printf '  %s%s%s ' "$AMB" "$(say "Panel domain for Let's Encrypt (Enter: self-signed on :7443):" "Домен панели для Let's Encrypt (Enter — свой сертификат на :7443):")" "$R"
    IFS= read -r PANEL_DOMAIN || true
  fi
  PANEL_DOMAIN=$(printf '%s' "${PANEL_DOMAIN//[[:space:]]/}" | tr '[:upper:]' '[:lower:]')
  [[ -n "$PANEL_DOMAIN" ]] || return 0
  if [[ ! "$PANEL_DOMAIN" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]]; then
    die "$(say "Not a domain name: ${PANEL_DOMAIN}" "Это не доменное имя: ${PANEL_DOMAIN}")"
  fi
  if port_in_use 80 tcp || port_in_use 443 tcp; then
    warn "$(say "Ports 80/443 are busy (a proxy agent?). Let's Encrypt cannot check the domain; using a self-signed certificate on :7443." "Порты 80/443 заняты (агент прокси?). Let's Encrypt не сможет проверить домен; ставлю свой сертификат на :7443.")"
    PANEL_DOMAIN=""
  fi
}

# apply_tls_mode writes the Caddy mode to .env: ACME with a domain, tls internal without.
apply_tls_mode() {
  if [[ -n "$PANEL_DOMAIN" ]]; then
    env_set CADDYFILE Caddyfile.acme
    env_set PANEL_DOMAIN "$PANEL_DOMAIN"
    env_set PANEL_HTTPS_PORT 443
    env_set PANEL_HTTP_PORT 80
  else
    env_default CADDYFILE Caddyfile
    env_default PANEL_HTTPS_PORT 7443
    env_default PANEL_HTTP_PORT 127.0.0.1:7080
  fi
}

# ensure_env brings an .env from an older version up to date without touching existing values.
ensure_env() {
  env_default CADDYFILE Caddyfile
  env_default PANEL_HTTP_PORT 127.0.0.1:7080
  env_default PANEL_COOKIE_SECURE true
  env_default LOG_LEVEL info
  if [[ -n "$WANT_VERSION" || -z "$(env_get DNSMARTY_IMAGE)" ]]; then
    env_set DNSMARTY_IMAGE "$IMAGE"
  fi
  IMAGE=$(env_get DNSMARTY_IMAGE)
}

write_compose() {
  cat >"$APP_DIR/compose.yml" <<'EOF'
name: dnsmarty

x-logging: &logging
  logging:
    driver: json-file
    options:
      max-size: "10m"
      max-file: "3"

x-hardened: &hardened
  <<: *logging
  read_only: true
  security_opt: ["no-new-privileges:true"]
  cap_drop: [ALL]
  tmpfs: [/tmp]

services:
  postgres:
    image: postgres:16@sha256:ca0bd484cb98bf4b24eb1010e73fb3fcbd6714d240fbc1a10eea5b7dbecb641d
    <<: *logging
    security_opt: ["no-new-privileges:true"]
    environment:
      POSTGRES_USER: dnsmarty
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: dnsmarty
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U dnsmarty -d dnsmarty"]
      interval: 5s
      timeout: 3s
      retries: 20
    shm_size: 128m
    mem_limit: 1g
    networks: [control]
    restart: unless-stopped

  migrate:
    image: ${DNSMARTY_IMAGE}
    <<: *hardened
    command: ["migrate"]
    environment:
      DATABASE_URL: postgres://dnsmarty:${POSTGRES_PASSWORD}@postgres:5432/dnsmarty?sslmode=disable
    depends_on:
      postgres:
        condition: service_healthy
    networks: [control]
    restart: "no"

  panel:
    image: ${DNSMARTY_IMAGE}
    <<: *hardened
    command: ["panel"]
    environment:
      DATABASE_URL: postgres://dnsmarty:${POSTGRES_PASSWORD}@postgres:5432/dnsmarty?sslmode=disable
      PANEL_ADMIN_USER: ${PANEL_ADMIN_USER}
      PANEL_ADMIN_PASSWORD: ${PANEL_ADMIN_PASSWORD}
      SESSION_SECRET: ${SESSION_SECRET}
      PANEL_MASTER_KEY: ${PANEL_MASTER_KEY}
      PANEL_LISTEN: ":8080"
      PANEL_COOKIE_SECURE: ${PANEL_COOKIE_SECURE:-true}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      METRICS_ADDR: ":9100"
    ports:
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:9100:9100"
    healthcheck:
      test: ["CMD", "/dnsmarty", "healthcheck", "--url", "http://127.0.0.1:8080/healthz"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 10s
    depends_on:
      migrate:
        condition: service_completed_successfully
    mem_limit: 512m
    pids_limit: 256
    networks: [control]
    restart: unless-stopped

  caddy:
    image: caddy:2@sha256:f2a1290d0463aad60660d4ec134943f183ee2a5f6c3eb7bf32dd984f2f020772
    <<: *hardened
    cap_add: [NET_BIND_SERVICE]
    environment:
      PANEL_DOMAIN: ${PANEL_DOMAIN:-}
    ports:
      - "${PANEL_HTTPS_PORT:-7443}:443"
      - "${PANEL_HTTP_PORT:-127.0.0.1:7080}:80"
    volumes:
      - ./${CADDYFILE:-Caddyfile}:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      panel:
        condition: service_healthy
    mem_limit: 256m
    networks: [control]
    restart: unless-stopped

networks:
  control: {}

volumes:
  postgres_data:
  caddy_data:
  caddy_config:
EOF
  cat >"$APP_DIR/Caddyfile" <<'EOF'
{
	admin off
	auto_https disable_redirects
}

:443 {
	tls internal
	encode zstd gzip
	reverse_proxy panel:8080
}
EOF
  cat >"$APP_DIR/Caddyfile.acme" <<'EOF'
{
	admin off
}

{$PANEL_DOMAIN} {
	encode zstd gzip
	reverse_proxy panel:8080
}
EOF
}

write_env() {
  ENV_CREATED=0
  if [[ -f "$APP_DIR/.env" ]]; then
    ok "$(say "Keeping the existing .env." "Существующий .env сохранён.")"
    return
  fi
  umask 077
  cat >"$APP_DIR/.env" <<EOF
POSTGRES_PASSWORD=$(rand)
PANEL_ADMIN_USER=admin
PANEL_ADMIN_PASSWORD=$(rand)
SESSION_SECRET=$(rand)$(rand)
PANEL_MASTER_KEY=$(rand)$(rand)
DNSMARTY_IMAGE=${IMAGE}
PANEL_COOKIE_SECURE=true
LOG_LEVEL=info
EOF
  chmod 600 "$APP_DIR/.env"
  ENV_CREATED=1
}

wait_local() {
  local port=$1 i=0
  for ((i = 0; i < 40; i++)); do
    if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

print_access() {
  local hp=""
  # shellcheck disable=SC1090
  set -a
  # shellcheck disable=SC1091
  source "$APP_DIR/.env"
  set +a
  hp="${PANEL_HTTPS_PORT:-7443}"
  echo
  printf '%s╭────────────────────────────────────────────────╮%s\n' "$GRN" "$R"
  printf '%s│%s  %-46s%s│%s\n' "$GRN" "$R" "panel ready" "$GRN" "$R"
  printf '%s╰────────────────────────────────────────────────╯%s\n' "$GRN" "$R"
  if [[ -n "${PANEL_DOMAIN:-}" ]]; then
    info "https://${PANEL_DOMAIN}"
  else
    info "https://127.0.0.1:${hp}  $(say "(self-signed; the browser will warn)" "(свой сертификат, браузер предупредит)")"
  fi
  info "$(say "Image: ${DNSMARTY_IMAGE:-}" "Образ: ${DNSMARTY_IMAGE:-}")"
  info "$(say "User: ${PANEL_ADMIN_USER}" "Логин: ${PANEL_ADMIN_USER}")"
  if [[ "$ENV_CREATED" -eq 1 ]]; then
    info "$(say "Password: ${PANEL_ADMIN_PASSWORD}" "Пароль: ${PANEL_ADMIN_PASSWORD}")"
  else
    info "$(say "Password is already in ${APP_DIR}/.env" "Пароль уже лежит в ${APP_DIR}/.env")"
  fi
  info "$(say "Change the password in the panel (Account). PANEL_ADMIN_PASSWORD in .env is used only for the first start." "Смените пароль в панели («Аккаунт»). PANEL_ADMIN_PASSWORD из .env нужен только при первом запуске.")"
  if wait_local 8080; then
    ok "$(say "Panel port 8080 accepts connections." "Порт панели 8080 принимает соединения.")"
  else
    warn "$(say "Panel port 8080 is not open yet. Check logs." "Порт панели 8080 ещё не открыт. Смотрите логи.")"
  fi
}

bring_up() {
  local digest=""
  (
    cd "$APP_DIR"
    docker compose pull
    docker compose up -d --remove-orphans
  )
  digest=$(docker image inspect --format '{{join .RepoDigests ", "}}' "$IMAGE" 2>/dev/null || true)
  [[ -z "$digest" ]] || info "$(say "Pulled: ${digest}" "Скачан: ${digest}")"
}

cmd_check() {
  need_root
  section "$(say "Checks" "Проверки")"
  audit_tools
  echo
  if [[ "$CHECK_FAIL" -eq 0 ]]; then
    ok "$(say "Nothing is blocking install." "Установке ничего не мешает.")"
  else
    die "$(say "Fix the failed checks, then run this again." "Исправьте ошибки и запустите снова.")"
  fi
}

cmd_install() {
  need_root
  parse_args "$@"
  if [[ "$IN_MENU" -ne 1 ]]; then
    banner "panel  ${VERSION}"
  fi
  section "$(say "Checks" "Проверки")"
  audit_tools
  echo
  if [[ "$CHECK_FAIL" -ne 0 ]]; then
    die "$(say "Fix the failed checks before installing." "Исправьте ошибки перед установкой.")"
  fi
  section "$(say "Setup" "Настройка")"
  if panel_installed; then
    warn "$(say "A panel is already installed in ${APP_DIR}." "Панель уже стоит в ${APP_DIR}.")"
    if confirm "$(say "Update images and restart, keeping .env and Postgres?" "Обновить образы и перезапустить, сохранив .env и Postgres?")" "y"; then
      ensure_docker
      resolve_image
      ensure_env
      write_compose
      bring_up
      print_access
      return
    fi
    info "$(say "Left the current install as it is." "Текущая установка не изменена.")"
    return
  fi
  ensure_docker
  prompt_domain
  resolve_image
  mkdir -p "$APP_DIR"
  chmod 700 "$APP_DIR"
  write_env
  apply_tls_mode
  ensure_env
  write_compose
  cmd_install_script
  bring_up
  print_access
}

cmd_update() {
  need_root
  parse_args "$@"
  panel_installed || die "$(say "Panel is not installed." "Панель не установлена.")"
  section "$(say "Checks" "Проверки")"
  audit_tools
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before continuing." "Исправьте ошибки перед продолжением.")"
  section "$(say "Setup" "Настройка")"
  ensure_docker
  # An explicit --version or DNSMARTY_IMAGE wins; otherwise move to the latest release.
  if [[ -z "$WANT_VERSION" && -z "$IMAGE" ]]; then
    IMAGE=$(env_get DNSMARTY_IMAGE)
    local tag=""
    tag=$(latest_tag)
    [[ -z "$tag" ]] || IMAGE="${IMAGE_REPO}:${tag#v}"
    [[ -n "$IMAGE" ]] || resolve_image
    env_set DNSMARTY_IMAGE "$IMAGE"
  else
    resolve_image
    env_set DNSMARTY_IMAGE "$IMAGE"
  fi
  [[ -z "$PANEL_DOMAIN" ]] || apply_tls_mode
  ensure_env
  write_compose
  bring_up
  ok "$(say "Updated to ${IMAGE}." "Обновлено до ${IMAGE}.")"
  print_access
}

cmd_compose() {
  need_root
  panel_installed || die "$(say "Panel is not installed." "Панель не установлена.")"
  (cd "$APP_DIR" && docker compose "$@")
}

cmd_edit_env() {
  need_root
  [[ -f "$APP_DIR/.env" ]] || die "$(say "No .env yet." "Файла .env ещё нет.")"
  "${EDITOR:-nano}" "$APP_DIR/.env"
  info "$(say "Restart to apply: dnsmarty restart" "Чтобы применить: dnsmarty restart")"
}

cmd_uninstall() {
  need_root
  local answer_default="n"
  if ! panel_installed; then
    die "$(say "Panel is not installed." "Панель не установлена.")"
  fi
  confirm "$(say "Stop panel containers? Files in ${APP_DIR} stay, including Postgres data." "Остановить контейнеры панели? Файлы в ${APP_DIR}, включая данные Postgres, останутся.")" "$answer_default" \
    || return 0
  (cd "$APP_DIR" && docker compose down) || true
  rm -f "$BIN_PATH"
  ok "$(say "Containers stopped. ${APP_DIR} was kept." "Контейнеры остановлены. ${APP_DIR} оставлен.")"
}

cmd_install_script() {
  need_root
  local src=""
  src="${BASH_SOURCE[0]}"
  if [[ -f "$src" ]] && cp "$src" "$BIN_PATH" 2>/dev/null; then
    :
  else
    fetch_verified dnsmarty.sh "$BIN_PATH"
  fi
  chmod 755 "$BIN_PATH"
  ok "$(say "Command installed: dnsmarty" "Команда установлена: dnsmarty")"
}

menu() {
  IN_MENU=1
  need_root
  while true; do
    clear_screen
    banner "panel  ${VERSION}"
    echo
    show_status
    printf '  %s%s%s\n\n' "$D" "$(say "Arrows or a number, Enter to open, q to quit." "Стрелки или цифра, Enter открывает, q выходит.")" "$R"
    if panel_installed; then
      MENU_SEL=3
    else
      MENU_SEL=0
    fi
    choose \
      "$(say "Install panel" "Установить панель")" \
      "$(say "Update images" "Обновить образы")" \
      "$(say "Check ports and access" "Проверить порты и доступ")" \
      "$(say "Status" "Статус")" \
      "$(say "Logs" "Логи")" \
      "$(say "Restart" "Перезапуск")" \
      "$(say "Edit .env" "Править .env")" \
      "$(say "Stop and remove containers" "Остановить и снять контейнеры")" \
      "$(say "Exit" "Выход")"
    case "$MENU_SEL" in
      -1 | 8) exit 0 ;;
      -2) warn "1-9 / q" ;;
      0) run_action cmd_install; pause ;;
      1) run_action cmd_update; pause ;;
      2) run_action cmd_check; pause ;;
      3) run_action cmd_compose ps; pause ;;
      4) run_action cmd_compose logs --tail 80; pause ;;
      5) run_action cmd_compose restart; pause ;;
      6) run_action cmd_edit_env; pause ;;
      7) run_action cmd_uninstall; pause ;;
    esac
  done
}

usage() {
  banner "panel  ${VERSION}"
  echo
  if [[ "$lang" == "ru" ]]; then
    cat <<EOF
  dnsmarty
  dnsmarty install [--domain DOMAIN] [--version vX.Y.Z]
  dnsmarty update [--version vX.Y.Z]
  dnsmarty check | up | down | restart | status | logs | edit-env | uninstall

  Без аргументов открывается меню. Язык берётся из локали системы.
  --domain включает Let's Encrypt (нужны свободные 80 и 443), без него — свой сертификат на :7443.
EOF
  else
    cat <<EOF
  dnsmarty
  dnsmarty install [--domain DOMAIN] [--version vX.Y.Z]
  dnsmarty update [--version vX.Y.Z]
  dnsmarty check | up | down | restart | status | logs | edit-env | uninstall

  No arguments opens the menu. Language follows the system locale.
  --domain turns on Let's Encrypt (ports 80 and 443 must be free); without it, a self-signed certificate on :7443.
EOF
  fi
}

main() {
  detect_lang
  local cmd="${1:-menu}"
  case "$cmd" in
    menu) menu ;;
    install) shift; cmd_install "$@" ;;
    update) shift; cmd_update "$@" ;;
    check) cmd_check ;;
    up | down | restart) shift; cmd_compose "$cmd" "$@" ;;
    status) cmd_compose ps ;;
    logs) shift; cmd_compose logs "$@" ;;
    edit-env) cmd_edit_env ;;
    uninstall) cmd_uninstall ;;
    install-script) need_root; cmd_install_script ;;
    help | -h | --help) usage ;;
    *) die "$(say "Unknown command: $cmd" "Неизвестная команда: $cmd")" ;;
  esac
}

trap 'tput cnorm 2>/dev/null || true' EXIT INT
main "$@"
