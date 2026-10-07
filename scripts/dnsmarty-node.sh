#!/usr/bin/env bash
# DNSmarty DNS or proxy agent.
#
#   bash <(curl -Ls https://raw.githubusercontent.com/Jav1x/DNSmarty/main/scripts/dnsmarty-node.sh) @ install
#   dnsmarty-node
set -euo pipefail

VERSION="1.0.0"
REPO="${DNSMARTY_REPO:-Jav1x/DNSmarty}"
IMAGE="${DNSMARTY_IMAGE:-ghcr.io/jav1x/dnsmarty:latest}"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/main/scripts/dnsmarty-node.sh"
lang="en"
role=""
key=""
port=""

if [[ "${1:-}" == "@" ]]; then
  shift
fi

c_reset=$'\033[0m'
c_green=$'\033[32m'
c_red=$'\033[31m'
ok() { printf '%s%s%s\n' "$c_green" "$*" "$c_reset"; }
die() { printf '%s%s%s\n' "$c_red" "$*" "$c_reset" >&2; exit 1; }
say() { if [[ "$lang" == "ru" ]]; then printf '%s\n' "$2"; else printf '%s\n' "$1"; fi; }

need_root() { [[ "$(id -u)" -eq 0 ]] || die "$(say "Run as root." "Запустите от root.")"; }

pick_lang() {
  case "${DNSMARTY_LANG:-}${LANG:-}" in
    ru*|Ru*|RU*) lang=ru ;;
  esac
  local choice=""
  printf "Language / Язык [en/ru] (%s): " "$lang"
  IFS= read -r choice || true
  case "$choice" in
    en|ru) lang=$choice ;;
    "") ;;
    *) die "Use en or ru. / Укажите en или ru." ;;
  esac
}

ensure_docker() {
  if command -v docker >/dev/null 2>&1; then
    return
  fi
  say "Docker is not installed." "Docker не установлен."
  local answer=""
  printf "Install Docker from get.docker.com? [y/N] / Установить Docker? [y/N]: "
  IFS= read -r answer || true
  [[ "$answer" == "y" || "$answer" == "Y" ]] || die "$(say "Install Docker and run this again." "Установите Docker и запустите снова.")"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
}

app_dir() { printf '/opt/dnsmarty-%s' "$role"; }

parse_install_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      dns|proxy) role="$1" ;;
      --role) role="${2:-}"; shift ;;
      --role=*) role="${1#*=}" ;;
      --key) key="${2:-}"; shift ;;
      --key=*) key="${1#*=}" ;;
      --port) port="${2:-}"; shift ;;
      --port=*) port="${1#*=}" ;;
      --image) IMAGE="${2:-}"; shift ;;
      --image=*) IMAGE="${1#*=}" ;;
      *) die "$(say "Unknown argument: $1" "Неизвестный аргумент: $1")" ;;
    esac
    shift
  done
}

prompt_install() {
  if [[ "$role" != "dns" && "$role" != "proxy" ]]; then
    if [[ "$lang" == "ru" ]]; then printf "Роль [dns/proxy]: "; else printf "Role [dns/proxy]: "; fi
    IFS= read -r role
  fi
  [[ "$role" == "dns" || "$role" == "proxy" ]] || die "$(say "Role must be dns or proxy." "Роль должна быть dns или proxy.")"
  if [[ -z "$key" ]]; then
    if [[ "$lang" == "ru" ]]; then printf "Ключ ноды: "; else printf "Node key: "; fi
    IFS= read -r key
  fi
  if [[ -z "$port" ]]; then
    if [[ "$role" == "dns" ]]; then port=9443; else port=9444; fi
    if [[ "$lang" == "ru" ]]; then printf "Порт управления (%s): " "$port"; else printf "Management port (%s): " "$port"; fi
    local entered=""
    IFS= read -r entered || true
    [[ -n "$entered" ]] && port="$entered"
  fi
  case "$port" in
    ''|*[!0-9]*) die "$(say "The port must be a number." "Порт должен быть числом.")" ;;
  esac
  if [[ "$port" -lt 1 || "$port" -gt 65535 ]]; then
    die "$(say "Port must be between 1 and 65535." "Порт вне диапазона 1–65535.")"
  fi
}

write_unit() {
  local dir
  dir="$(app_dir)"
  mkdir -p "$dir/certs"
  if [[ "$role" == "dns" && ( ! -f "$dir/certs/cert.pem" || ! -f "$dir/certs/key.pem" ) ]]; then
    say "Creating a self-signed certificate for DoT and DoH." "Создаю самоподписанный сертификат для DoT и DoH."
    openssl req -x509 -newkey rsa:2048 -keyout "$dir/certs/key.pem" -out "$dir/certs/cert.pem" -days 365 -nodes -subj /CN=dns.local
  fi
  cat >"$dir/.env" <<EOF
NODE_KEY=${key}
AGENT_ADDR=:${port}
TLS_CERT_FILE=/certs/cert.pem
TLS_KEY_FILE=/certs/key.pem
DNS_ADDR=:53
DOT_ADDR=:853
DOH_ADDR=:8443
PROXY_HTTP_ADDR=:80
PROXY_HTTPS_ADDR=:443
METRICS_ADDR=:$([[ "$role" == "dns" ]] && echo 9101 || echo 9102)
EOF
  chmod 600 "$dir/.env"
  if [[ "$role" == "dns" ]]; then
    cat >"$dir/compose.yml" <<EOF
services:
  dns:
    image: ${IMAGE}
    user: "0:0"
    command: ["dns"]
    network_mode: host
    env_file: .env
    volumes:
      - ./certs:/certs:ro
    restart: unless-stopped
EOF
  else
    cat >"$dir/compose.yml" <<EOF
services:
  proxy:
    image: ${IMAGE}
    user: "0:0"
    command: ["proxy"]
    network_mode: host
    env_file: .env
    restart: unless-stopped
EOF
  fi
  printf '%s\n' "$lang" >"$dir/.lang"
}

cmd_install() {
  need_root
  pick_lang
  parse_install_args "$@"
  prompt_install
  ensure_docker
  write_unit
  cmd_install_script
  (cd "$(app_dir)" && docker compose pull && docker compose up -d)
  ok "$(say "Agent ${role} is listening on port ${port}. Press Next in the panel." "Агент ${role} слушает порт ${port}. В панели нажмите «Далее».")"
}

load_role_dir() {
  if [[ -d /opt/dnsmarty-dns && -f /opt/dnsmarty-dns/compose.yml ]]; then
    role=dns
  elif [[ -d /opt/dnsmarty-proxy && -f /opt/dnsmarty-proxy/compose.yml ]]; then
    role=proxy
  else
    die "$(say "No agent is installed." "Агент не установлен.")"
  fi
  if [[ -f "$(app_dir)/.lang" ]]; then
    lang="$(cat "$(app_dir)/.lang")"
  fi
}

cmd_update() {
  need_root
  load_role_dir
  (cd "$(app_dir)" && docker compose pull && docker compose up -d)
  ok "$(say "Updated." "Обновлено.")"
}

cmd_compose() {
  need_root
  load_role_dir
  (cd "$(app_dir)" && docker compose "$@")
}

cmd_uninstall() {
  need_root
  load_role_dir
  local answer=""
  printf "Remove agent containers? Config in %s stays. [y/N] / Удалить контейнеры агента? [y/N]: " "$(app_dir)"
  IFS= read -r answer || true
  [[ "$answer" == "y" || "$answer" == "Y" ]] || exit 0
  (cd "$(app_dir)" && docker compose down) || true
  rm -f /usr/local/bin/dnsmarty-node
  ok "$(say "Stopped." "Остановлено.")"
}

cmd_install_script() {
  need_root
  if [[ -f "${BASH_SOURCE[0]}" ]] && cp "${BASH_SOURCE[0]}" /usr/local/bin/dnsmarty-node 2>/dev/null; then
    :
  else
    curl -fsSL "$RAW_BASE" -o /usr/local/bin/dnsmarty-node
  fi
  chmod 755 /usr/local/bin/dnsmarty-node
  ok "$(say "Command installed: dnsmarty-node" "Команда установлена: dnsmarty-node")"
}

menu() {
  pick_lang
  while true; do
    echo
    if [[ "$lang" == "ru" ]]; then
      echo "DNSmarty node ${VERSION}"
      echo "  1) Установить агент"
      echo "  2) Обновить образ"
      echo "  3) Статус"
      echo "  4) Логи"
      echo "  5) Перезапуск"
      echo "  6) Остановить"
      echo "  0) Выход"
    else
      echo "DNSmarty node ${VERSION}"
      echo "  1) Install agent"
      echo "  2) Update image"
      echo "  3) Status"
      echo "  4) Logs"
      echo "  5) Restart"
      echo "  6) Stop"
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
      6) cmd_uninstall ;;
      0) exit 0 ;;
    esac
  done
}

main() {
  local cmd="${1:-menu}"
  case "$cmd" in
    menu) menu ;;
    dns|proxy)
      role="$1"
      shift
      cmd_install "$@"
      ;;
    install)
      shift
      cmd_install "$@"
      ;;
    update) cmd_update ;;
    up|down|restart) shift; cmd_compose "$cmd" "$@" ;;
    status) cmd_compose ps ;;
    logs) shift; cmd_compose logs "$@" ;;
    uninstall) cmd_uninstall ;;
    install-script) need_root; cmd_install_script ;;
    help|-h|--help)
      say "dnsmarty-node install --role dns --key KEY --port 9443" "dnsmarty-node install --role dns --key КЛЮЧ --port 9443"
      ;;
    *) die "$(say "Unknown command: $cmd" "Неизвестная команда: $cmd")" ;;
  esac
}

main "$@"
