#!/usr/bin/env bash
# DNSmarty panel: install, update, and manage the control plane.
#
#   bash <(curl -Ls https://raw.githubusercontent.com/Jav1x/DNSmarty/main/scripts/dnsmarty.sh) @ install
#   dnsmarty
set -euo pipefail

VERSION="1.0.0"
REPO="${DNSMARTY_REPO:-Jav1x/DNSmarty}"
IMAGE="${DNSMARTY_IMAGE:-ghcr.io/jav1x/dnsmarty:latest}"
APP_DIR="${DNSMARTY_DIR:-/opt/dnsmarty}"
BIN_PATH="/usr/local/bin/dnsmarty"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/main/scripts/dnsmarty.sh"
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

write_compose() {
  cat >"$APP_DIR/compose.yml" <<EOF
name: dnsmarty

services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: dnsmarty
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD}
      POSTGRES_DB: dnsmarty
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U dnsmarty -d dnsmarty"]
      interval: 2s
      timeout: 3s
      retries: 20
    networks: [control]
    restart: unless-stopped

  migrate:
    image: ${IMAGE}
    command: ["migrate"]
    environment:
      DATABASE_URL: postgres://dnsmarty:\${POSTGRES_PASSWORD}@postgres:5432/dnsmarty?sslmode=disable
    depends_on:
      postgres:
        condition: service_healthy
    networks: [control]
    restart: "no"

  panel:
    image: ${IMAGE}
    command: ["panel"]
    environment:
      DATABASE_URL: postgres://dnsmarty:\${POSTGRES_PASSWORD}@postgres:5432/dnsmarty?sslmode=disable
      PANEL_ADMIN_USER: \${PANEL_ADMIN_USER}
      PANEL_ADMIN_PASSWORD: \${PANEL_ADMIN_PASSWORD}
      SESSION_SECRET: \${SESSION_SECRET}
      PANEL_MASTER_KEY: \${PANEL_MASTER_KEY}
      PANEL_LISTEN: ":8080"
      METRICS_ADDR: ":9100"
    ports:
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:9100:9100"
    depends_on:
      migrate:
        condition: service_completed_successfully
    networks: [control]
    restart: unless-stopped

  caddy:
    image: caddy:2
    ports:
      - "\${PANEL_HTTPS_PORT:-7443}:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
    depends_on:
      - panel
    networks: [control]
    restart: unless-stopped

networks:
  control:
    name: dnsmarty_control

volumes:
  postgres_data:
EOF
  cat >"$APP_DIR/Caddyfile" <<'EOF'
{
	admin off
	auto_https disable_redirects
}

:443 {
	tls internal
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
  cat >"$APP_DIR/.env" <<EOF
POSTGRES_PASSWORD=$(rand)
PANEL_ADMIN_USER=admin
PANEL_ADMIN_PASSWORD=$(rand)
SESSION_SECRET=$(rand)$(rand)
PANEL_MASTER_KEY=$(rand)$(rand)
PANEL_HTTPS_PORT=7443
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
  info "https://127.0.0.1:${hp}"
  info "$(say "User: ${PANEL_ADMIN_USER}" "Логин: ${PANEL_ADMIN_USER}")"
  if [[ "$ENV_CREATED" -eq 1 ]]; then
    info "$(say "Password: ${PANEL_ADMIN_PASSWORD}" "Пароль: ${PANEL_ADMIN_PASSWORD}")"
  else
    info "$(say "Password is already in ${APP_DIR}/.env" "Пароль уже лежит в ${APP_DIR}/.env")"
  fi
  if wait_local 8080; then
    ok "$(say "Panel port 8080 accepts connections." "Порт панели 8080 принимает соединения.")"
  else
    warn "$(say "Panel port 8080 is not open yet. Check logs." "Порт панели 8080 ещё не открыт. Смотрите логи.")"
  fi
}

bring_up() {
  (
    cd "$APP_DIR"
    docker compose pull
    docker compose up -d
  )
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
      write_compose
      bring_up
      print_access
      return
    fi
    info "$(say "Left the current install as it is." "Текущая установка не изменена.")"
    return
  fi
  ensure_docker
  mkdir -p "$APP_DIR"
  write_env
  write_compose
  cmd_install_script
  bring_up
  print_access
}

cmd_update() {
  need_root
  panel_installed || die "$(say "Panel is not installed." "Панель не установлена.")"
  section "$(say "Checks" "Проверки")"
  audit_tools
  echo
  [[ "$CHECK_FAIL" -eq 0 ]] || die "$(say "Fix the failed checks before continuing." "Исправьте ошибки перед продолжением.")"
  section "$(say "Setup" "Настройка")"
  ensure_docker
  write_compose
  bring_up
  ok "$(say "Updated." "Обновлено.")"
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
    curl -fsSL "$RAW_BASE" -o "$BIN_PATH"
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
  dnsmarty install | update | check | up | down | restart | status | logs | edit-env | uninstall

  Без аргументов открывается меню. Язык берётся из локали системы.
EOF
  else
    cat <<EOF
  dnsmarty
  dnsmarty install | update | check | up | down | restart | status | logs | edit-env | uninstall

  No arguments opens the menu. Language follows the system locale.
EOF
  fi
}

main() {
  detect_lang
  local cmd="${1:-menu}"
  case "$cmd" in
    menu) menu ;;
    install) cmd_install ;;
    update) cmd_update ;;
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
