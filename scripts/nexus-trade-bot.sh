#!/usr/bin/env bash

set -Eeuo pipefail

# Server runner for nexus-trade-bot.
#
# Usage:
#   scripts/nexus-trade-bot.sh start
#   scripts/nexus-trade-bot.sh stop
#   scripts/nexus-trade-bot.sh restart
#   scripts/nexus-trade-bot.sh status
#   scripts/nexus-trade-bot.sh logs
#   scripts/nexus-trade-bot.sh worker
#
# Optional env:
#   NEXUS_TRADE_BOT_DIR=/opt/nexus-trade-bot
#   NEXUS_TRADE_BOT_REPO=https://github.com/haohaoi34/nexus-trade-bot.git
#   NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080
#   NEXUS_TRADE_BOT_CONFIG=/opt/nexus-trade-bot/config.yaml
#   NEXUS_TRADE_BOT_GO_VERSION=1.26.3
#   NEXUS_TRADE_BOT_PUBLIC_IP=1.2.3.4

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

REPO_URL="${NEXUS_TRADE_BOT_REPO:-https://github.com/haohaoi34/nexus-trade-bot.git}"
GO_VERSION="${NEXUS_TRADE_BOT_GO_VERSION:-1.26.3}"
ADDR="${NEXUS_TRADE_BOT_ADDR:-0.0.0.0:8080}"

if [[ -f "${SCRIPT_ROOT}/go.mod" || -x "${SCRIPT_ROOT}/nexus-trade-bot" || -x "${SCRIPT_ROOT}/nexus-trade-bot.exe" ]]; then
  APP_DIR="${NEXUS_TRADE_BOT_DIR:-${SCRIPT_ROOT}}"
else
  APP_DIR="${NEXUS_TRADE_BOT_DIR:-${HOME}/nexus-trade-bot}"
fi

BIN_NAME="nexus-trade-bot"
if [[ "$(uname -s)" =~ MINGW|MSYS|CYGWIN ]]; then
  BIN_NAME="nexus-trade-bot.exe"
fi

BIN_PATH="${APP_DIR}/${BIN_NAME}"
CONFIG_PATH="${NEXUS_TRADE_BOT_CONFIG:-${APP_DIR}/config.yaml}"
LOG_DIR="${APP_DIR}/logs"
WEB_LOG="${LOG_DIR}/web-console.log"
WEB_ERR_LOG="${LOG_DIR}/web-console-error.log"
PID_FILE="${APP_DIR}/nexus-trade-bot.pid"

say() { printf "%s\n" "$*"; }
info() { say "==> $*"; }
ok() { say "OK $*"; }
warn() { say "WARN $*" >&2; }
fail() { say "ERROR $*" >&2; exit 1; }

need_sudo() { [[ "${EUID:-$(id -u)}" -ne 0 ]]; }

sudo_cmd() {
  if need_sudo && command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    "$@"
  fi
}

version_ge() {
  local have="$1" want="$2"
  [[ "$(printf '%s\n' "$want" "$have" | sort -V | head -n1)" == "$want" ]]
}

install_os_deps() {
  if ! command -v apt-get >/dev/null 2>&1; then
    return
  fi
  info "Installing server dependencies"
  sudo_cmd apt-get update -y
  sudo_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y \
    ca-certificates curl wget git tar gzip build-essential lsof procps
}

download() {
  local url="$1" output="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 10 --max-time 300 "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$output" "$url"
  else
    fail "curl or wget is required"
  fi
}

managed_data_paths() {
  printf "%s\n" \
    "$CONFIG_PATH" \
    "${CONFIG_PATH}.auth.json" \
    "${APP_DIR}/web_console_accounts.json" \
    "${APP_DIR}/web_console_robots.json" \
    "${APP_DIR}/web_console_robots"
}

backup_managed_data() {
  local backup_dir="$1" path rel
  mkdir -p "$backup_dir"
  while IFS= read -r path; do
    [[ -e "$path" ]] || continue
    rel="${path#${APP_DIR}/}"
    if [[ "$rel" == "$path" ]]; then
      rel="$(basename "$path")"
    fi
    mkdir -p "${backup_dir}/$(dirname "$rel")"
    cp -a "$path" "${backup_dir}/${rel}"
  done < <(managed_data_paths)
}

restore_managed_data() {
  local backup_dir="$1" path rel src
  [[ -d "$backup_dir" ]] || return
  while IFS= read -r path; do
    rel="${path#${APP_DIR}/}"
    if [[ "$rel" == "$path" ]]; then
      rel="$(basename "$path")"
    fi
    src="${backup_dir}/${rel}"
    [[ -e "$src" ]] || continue
    mkdir -p "$(dirname "$path")"
    rm -rf "$path"
    cp -a "$src" "$path"
  done < <(managed_data_paths)
  chmod 600 "$CONFIG_PATH" "${CONFIG_PATH}.auth.json" "${APP_DIR}/web_console_accounts.json" "${APP_DIR}/web_console_robots.json" 2>/dev/null || true
  chmod 700 "${APP_DIR}/web_console_robots" 2>/dev/null || true
}

install_go() {
  local installed=""
  if command -v go >/dev/null 2>&1; then
    installed="$(go version | awk '{print $3}' | sed 's/^go//')"
  fi
  if [[ -n "$installed" ]] && version_ge "$installed" "$GO_VERSION"; then
    return
  fi

  local os arch go_arch tarball tmp
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) go_arch="amd64" ;;
    aarch64|arm64) go_arch="arm64" ;;
    *) fail "Unsupported CPU architecture: ${arch}" ;;
  esac

  tarball="go${GO_VERSION}.${os}-${go_arch}.tar.gz"
  tmp="$(mktemp -d)"
  info "Installing Go ${GO_VERSION}"
  download "https://go.dev/dl/${tarball}" "${tmp}/${tarball}"
  sudo_cmd rm -rf /usr/local/go
  sudo_cmd tar -C /usr/local -xzf "${tmp}/${tarball}"
  rm -rf "$tmp"
  export PATH="/usr/local/go/bin:${PATH}"
  ok "Go installed: $(go version | awk '{print $3}')"
}

prepare_app_dir() {
  if [[ -f "${APP_DIR}/go.mod" || -x "${APP_DIR}/${BIN_NAME}" ]]; then
    return
  fi
  if [[ -e "$APP_DIR" ]]; then
    fail "${APP_DIR} exists but is not a nexus-trade-bot source or release directory"
  fi
  command -v git >/dev/null 2>&1 || fail "git is required to clone ${REPO_URL}"
  info "Cloning ${REPO_URL} -> ${APP_DIR}"
  git clone "$REPO_URL" "$APP_DIR"
}

sync_source() {
  command -v git >/dev/null 2>&1 || fail "git is required to update ${REPO_URL}"
  if [[ -d "${APP_DIR}/.git" ]]; then
    info "Updating source in ${APP_DIR}"
    git -C "$APP_DIR" fetch --prune origin
    git -C "$APP_DIR" reset --hard origin/main
    git -C "$APP_DIR" clean -fd \
      -e config.yaml \
      -e config.yaml.auth.json \
      -e web_console_accounts.json \
      -e web_console_robots.json \
      -e web_console_robots/ \
      -e logs/ \
      -e nexus-trade-bot.pid
    return
  fi

  if [[ -e "$APP_DIR" ]]; then
    fail "${APP_DIR} exists but is not a git checkout; refusing to replace it automatically"
  fi
  info "Cloning ${REPO_URL} -> ${APP_DIR}"
  git clone "$REPO_URL" "$APP_DIR"
}

build_if_source() {
  if [[ ! -f "${APP_DIR}/go.mod" ]]; then
    [[ -x "$BIN_PATH" ]] || fail "binary not found: ${BIN_PATH}"
    return
  fi
  install_go
  cd "$APP_DIR"
  info "Downloading Go modules"
  go mod download
  info "Building ${BIN_NAME}"
  go build -o "$BIN_PATH" .
  chmod +x "$BIN_PATH"
}

ensure_config() {
  if [[ -f "$CONFIG_PATH" ]]; then
    chmod 600 "$CONFIG_PATH" 2>/dev/null || true
    return
  fi
  [[ -f "${APP_DIR}/config.example.yaml" ]] || fail "config.example.yaml not found in ${APP_DIR}"
  cp "${APP_DIR}/config.example.yaml" "$CONFIG_PATH"
  chmod 600 "$CONFIG_PATH" 2>/dev/null || true
  warn "Created ${CONFIG_PATH}; fill API keys before live trading"
}

pid_running() {
  [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null
}

process_cmdline() {
  local pid="$1"
  if [[ -r "/proc/${pid}/cmdline" ]]; then
    tr '\0' ' ' <"/proc/${pid}/cmdline"
  elif command -v ps >/dev/null 2>&1; then
    ps -p "$pid" -o command= 2>/dev/null || true
  fi
}

is_nexus_pid() {
  local pid="$1" cmd
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  cmd="$(process_cmdline "$pid")"
  [[ "$cmd" == *"nexus-trade-bot"* ]]
}

kill_pid() {
  local pid="$1" label="${2:-process}"
  [[ -n "$pid" ]] || return
  kill -0 "$pid" 2>/dev/null || return
  info "Stopping ${label} pid ${pid}"
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 30); do
    if ! kill -0 "$pid" 2>/dev/null; then
      return
    fi
    sleep 0.2
  done
  warn "Force stopping ${label} pid ${pid}"
  kill -9 "$pid" 2>/dev/null || true
}

kill_port_listeners() {
  local port="$1" pids pid
  command -v lsof >/dev/null 2>&1 || return 0
  pids="$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)"
  [[ -n "$pids" ]] || return 0
  for pid in $pids; do
    if is_nexus_pid "$pid"; then
      kill_pid "$pid" "old nexus-trade-bot port listener"
    else
      warn "TCP port ${port} is used by another process (pid ${pid}); leaving it untouched"
    fi
  done
}

port_from_addr() {
  printf "%s" "$ADDR" | awk -F: '{print $NF}'
}

local_url() {
  printf "http://127.0.0.1:%s" "$(port_from_addr)"
}

is_ip_address() {
  local value="$1"
  [[ "$value" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ || "$value" =~ ^[0-9A-Fa-f:]+$ ]]
}

fetch_url_text() {
  local url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 2 --max-time 4 "$url" 2>/dev/null | tr -d '[:space:]'
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- --timeout=4 "$url" 2>/dev/null | tr -d '[:space:]'
  fi
}

detect_public_ip() {
  local ip service
  if [[ -n "${NEXUS_TRADE_BOT_PUBLIC_IP:-}" ]]; then
    printf "%s" "$NEXUS_TRADE_BOT_PUBLIC_IP"
    return
  fi

  for service in \
    "https://api.ipify.org" \
    "https://ifconfig.me/ip" \
    "https://icanhazip.com"; do
    ip="$(fetch_url_text "$service" || true)"
    if [[ -n "$ip" ]] && is_ip_address "$ip"; then
      printf "%s" "$ip"
      return
    fi
  done
}

detect_lan_ip() {
  local ip
  if command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    if [[ -n "$ip" ]] && is_ip_address "$ip"; then
      printf "%s" "$ip"
      return
    fi
  fi
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="src") {print $(i+1); exit}}')"
    if [[ -n "$ip" ]] && is_ip_address "$ip"; then
      printf "%s" "$ip"
      return
    fi
  fi
}

server_host() {
  local ip host
  ip="$(detect_public_ip || true)"
  if [[ -n "$ip" ]]; then
    printf "%s" "$ip"
    return
  fi
  ip="$(detect_lan_ip || true)"
  if [[ -n "$ip" ]]; then
    printf "%s" "$ip"
    return
  fi
  host="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
  printf "%s" "${host:-127.0.0.1}"
}

server_host_for_url() {
  local host="$1"
  if [[ "$host" == *:* && "$host" != \[*\] ]]; then
    printf "[%s]" "$host"
  else
    printf "%s" "$host"
  fi
}

server_url() {
  local host
  host="$(server_host)"
  printf "http://%s:%s" "$(server_host_for_url "$host")" "$(port_from_addr)"
}

print_access_block() {
  local title="$1"
  local pid="${2:-}"
  local port access_url
  port="$(port_from_addr)"
  access_url="$(server_url)"

  say
  say "======================================================================"
  say " ${title}"
  say "======================================================================"
  [[ -n "$pid" ]] && say " PID:        ${pid}"
  say " Local URL:  $(local_url)"
  say " Server URL: ${access_url}"
  say " Bind Addr:  ${ADDR}"
  say " Log:        ${WEB_LOG}"
  say " Stop:       ${APP_DIR}/scripts/nexus-trade-bot.sh stop"
  say "----------------------------------------------------------------------"
  say " Open this in your browser: ${access_url}"
  say " If it cannot open, allow TCP port ${port} in your server firewall."
  say "======================================================================"
  say
}

port_is_busy() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
  else
    return 1
  fi
}

start_web() {
  install_os_deps
  prepare_app_dir
  build_if_source
  ensure_config
  mkdir -p "$LOG_DIR"

  if pid_running; then
    warn "Existing nexus-trade-bot is running; restarting it with the current build"
    stop_web
  fi

  local port
  port="$(port_from_addr)"
  if port_is_busy "$port"; then
    kill_port_listeners "$port"
  fi
  if port_is_busy "$port"; then
    fail "TCP port ${port} is still in use by a non-nexus process. Set NEXUS_TRADE_BOT_ADDR=0.0.0.0:8081 or free the port."
  fi

  : > "$WEB_LOG"
  : > "$WEB_ERR_LOG"
  cd "$APP_DIR"
  info "Starting web console on ${ADDR}"
  nohup env NEXUS_TRADE_BOT_ADDR="$ADDR" "$BIN_PATH" "$CONFIG_PATH" </dev/null >>"$WEB_LOG" 2>>"$WEB_ERR_LOG" &
  local pid=$!
  disown "$pid" 2>/dev/null || true
  printf "%s" "$pid" > "$PID_FILE"
  sleep 1

  if ! kill -0 "$pid" 2>/dev/null; then
    tail -n 120 "$WEB_LOG" >&2 || true
    tail -n 120 "$WEB_ERR_LOG" >&2 || true
    rm -f "$PID_FILE"
    fail "web console failed to start"
  fi

  ok "nexus-trade-bot started"
  print_access_block "NEXUS TRADE BOT STARTED SUCCESSFULLY" "$pid"
}

stop_web() {
  local stopped=0 pid port
  if pid_running; then
    pid="$(cat "$PID_FILE")"
    kill_pid "$pid" "nexus-trade-bot"
    stopped=1
  fi
  port="$(port_from_addr)"
  kill_port_listeners "$port"
  rm -f "$PID_FILE"
  if [[ "$stopped" -eq 1 ]]; then
    ok "Stopped"
  else
    say "Not running"
  fi
}

status_web() {
  if pid_running; then
    ok "Running"
    print_access_block "NEXUS TRADE BOT STATUS" "$(cat "$PID_FILE")"
  else
    say "Not running"
  fi
}

run_worker() {
  install_os_deps
  prepare_app_dir
  build_if_source
  ensure_config
  cd "$APP_DIR"
  exec "$BIN_PATH" worker "$CONFIG_PATH"
}

update_source() {
  local backup_dir
  backup_dir="$(mktemp -d)"
  backup_managed_data "$backup_dir"
  stop_web
  sync_source
  restore_managed_data "$backup_dir"
  rm -rf "$backup_dir"
  build_if_source
  ensure_config
  ok "Updated in ${APP_DIR}"
}

case "${1:-start}" in
  install)
    install_os_deps
    update_source
    ok "Installed in ${APP_DIR}"
    ;;
  start)
    start_web
    ;;
  stop)
    stop_web
    ;;
  restart)
    stop_web
    start_web
    ;;
  status)
    status_web
    ;;
  logs)
    mkdir -p "$LOG_DIR"
    touch "$WEB_LOG" "$WEB_ERR_LOG"
    tail -n 80 -f "$WEB_LOG" "$WEB_ERR_LOG"
    ;;
  worker)
    run_worker
    ;;
  update)
    update_source
    ;;
  *)
    say "Usage: $0 {install|start|stop|restart|status|logs|worker|update}"
    exit 2
    ;;
esac
