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

if [[ "${1:-}" == "@" ]]; then
  shift
fi

c_reset=$'\033[0m'
c_amber=$'\033[33m'
c_red=$'\033[31m'
c_green=$'\033[32m'

info() { printf '%s\n' "$*"; }
ok() { printf '%s%s%s\n' "$c_green" "$*" "$c_reset"; }
warn() { printf '%s%s%s\n' "$c_amber" "$*" "$c_reset"; }
die() { printf '%s%s%s\n' "$c_red" "$*" "$c_reset" >&2; exit 1; }

say() {
  if [[ "$lang" == "ru" ]]; then printf '%s\n' "$2"; else printf '%s\n' "$1"; fi
}

need_root() {
  [[ "$(id -u)" -eq 0 ]] || die "$(say "Run as root." "Запустите от root.")"
}

pick_lang() {
  case "${DNSMARTY_LANG:-}${LANG:-}" in
    ru*|Ru*|RU*) lang=ru ;;
  esac
  if [[ -f "$APP_DIR/.lang" ]]; then
    lang="$(cat "$APP_DIR/.lang")"
  fi
  local choice=""
  printf "Language / Язык [en/ru] (%s): " "$lang"
  IFS= read -r choice || true
  case "$choice" in
    en|ru) lang=$choice ;;
    "") ;;
    *) die "Use en or ru. / Укажите en или ru." ;;
  esac
}

save_lang() {
  mkdir -p "$APP_DIR"
  printf '%s\n' "$lang" >"$APP_DIR/.lang"
}

have_docker() {
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1
}

ensure_docker() {
  if have_docker; then
    return
  fi
  say "Docker is not installed." "Docker не установлен."
  local answer=""
  if [[ "$lang" == "ru" ]]; then
    printf "Установить Docker через get.docker.com? [y/N]: "
  else
    printf "Install Docker from get.docker.com? [y/N]: "
  fi
  IFS= read -r answer || true
  [[ "$answer" == "y" || "$answer" == "Y" ]] || die "$(say "Install Docker and run this again." "Установите Docker и запустите снова.")"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
  have_docker || die "docker compose is still missing"
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
  if [[ -f "$APP_DIR/.env" ]]; then
    say "Keeping the existing .env." "Существующий .env сохранён."
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
}

cmd_install() {
  need_root
  pick_lang
  ensure_docker
  mkdir -p "$APP_DIR"
  save_lang
  write_env
  write_compose
  cmd_install_script
  (
    cd "$APP_DIR"
    docker compose pull
    docker compose up -d
  )
  # shellcheck disable=SC1091
  set -a
  # shellcheck disable=SC1090
  source "$APP_DIR/.env"
  set +a
  ok "$(say "Panel is up." "Панель запущена.")"
  info "https://127.0.0.1:${PANEL_HTTPS_PORT:-7443}"
  info "$(say "User: ${PANEL_ADMIN_USER}" "Логин: ${PANEL_ADMIN_USER}")"
  info "$(say "Password: ${PANEL_ADMIN_PASSWORD}" "Пароль: ${PANEL_ADMIN_PASSWORD}")"
  info "$(say "Saved in ${APP_DIR}/.env" "Сохранено в ${APP_DIR}/.env")"
}

cmd_update() {
  need_root
  [[ -f "$APP_DIR/compose.yml" ]] || die "$(say "Panel is not installed." "Панель не установлена.")"
  pick_lang
  save_lang
  write_compose
  (
    cd "$APP_DIR"
    docker compose pull
    docker compose up -d
  )
  ok "$(say "Updated." "Обновлено.")"
}

cmd_compose() {
  need_root
  [[ -d "$APP_DIR" ]] || die "$(say "Panel is not installed." "Панель не установлена.")"
  (cd "$APP_DIR" && docker compose "$@")
}

cmd_edit_env() {
  need_root
  [[ -f "$APP_DIR/.env" ]] || die "$(say "No .env yet." "Файла .env ещё нет.")"
  "${EDITOR:-nano}" "$APP_DIR/.env"
  say "Restart to apply: dnsmarty restart" "Чтобы применить, перезапустите: dnsmarty restart"
}

cmd_uninstall() {
  need_root
  pick_lang
  local answer=""
  if [[ "$lang" == "ru" ]]; then
    printf "Удалить контейнеры панели? Данные Postgres останутся, пока не удалите ${APP_DIR}. [y/N]: "
  else
    printf "Remove the panel containers? Postgres data stays until you delete ${APP_DIR}. [y/N]: "
  fi
  IFS= read -r answer || true
  [[ "$answer" == "y" || "$answer" == "Y" ]] || exit 0
  if [[ -f "$APP_DIR/compose.yml" ]]; then
    (cd "$APP_DIR" && docker compose down) || true
  fi
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
  pick_lang
  while true; do
    echo
    if [[ "$lang" == "ru" ]]; then
      echo "DNSmarty ${VERSION}"
      echo "  1) Установить панель"
      echo "  2) Обновить образы"
      echo "  3) Статус"
      echo "  4) Логи"
      echo "  5) Перезапуск"
      echo "  6) Править .env"
      echo "  7) Остановить и снять контейнеры"
      echo "  0) Выход"
    else
      echo "DNSmarty ${VERSION}"
      echo "  1) Install panel"
      echo "  2) Update images"
      echo "  3) Status"
      echo "  4) Logs"
      echo "  5) Restart"
      echo "  6) Edit .env"
      echo "  7) Stop and remove containers"
      echo "  0) Exit"
    fi
    printf "> "
    local choice=""
    IFS= read -r choice || exit 0
    case "$choice" in
      1) cmd_install ;;
      2) cmd_update ;;
      3) cmd_compose ps ;;
      4) cmd_compose logs --tail 100 ;;
      5) cmd_compose restart ;;
      6) cmd_edit_env ;;
      7) cmd_uninstall ;;
      0) exit 0 ;;
      *) warn "1-7 / 0" ;;
    esac
  done
}

usage() {
  if [[ "$lang" == "ru" ]]; then
    cat <<EOF
dnsmarty install | update | up | down | restart | status | logs | edit-env | uninstall | install-script
Без аргументов открывается меню.
EOF
  else
    cat <<EOF
dnsmarty install | update | up | down | restart | status | logs | edit-env | uninstall | install-script
No arguments opens the menu.
EOF
  fi
}

main() {
  local cmd="${1:-menu}"
  case "$cmd" in
    menu) menu ;;
    install) cmd_install ;;
    update) cmd_update ;;
    up|down|restart) shift; cmd_compose "$cmd" "$@" ;;
    status) cmd_compose ps ;;
    logs) shift; cmd_compose logs "$@" ;;
    edit-env) cmd_edit_env ;;
    uninstall) cmd_uninstall ;;
    install-script) need_root; cmd_install_script ;;
    help|-h|--help) usage ;;
    *) die "$(say "Unknown command: $cmd" "Неизвестная команда: $cmd")" ;;
  esac
}

main "$@"
