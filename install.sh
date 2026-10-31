#!/usr/bin/env bash
# Install or update Ignition. Safe to re-run: existing secrets and data are kept.
#
# On a server (VPS), as root:
#   curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | bash
# This downloads the compose files to /opt/ignition and runs the published image
# notsliver/ignition from Docker Hub. Nothing is built.
#
# From a git checkout (developing Ignition):  ./install.sh
# This builds the image from your checkout instead.
#
# Options (environment variables):
#   IGNITION_DIR=/opt/ignition   where a server install lives
#   IGNITION_TAG=latest          image tag to install (e.g. 0.1.0-beta); defaults to latest, else beta
#   IGNITION_HOST=203.0.113.7    address or domain people use to reach the server (detected if unset)
#   IGNITION_PORT=8080           dashboard port
#   IGNITION_HTTP_PORT=80        port the domain proxy serves HTTP on
#   IGNITION_HTTPS_PORT=443      port the domain proxy serves HTTPS on
#   IGNITION_SKIP_CHECKS=1       skip the memory, disk and port checks (not recommended)
set -Eeuo pipefail

bold=$'\033[1m' red=$'\033[31m' yellow=$'\033[33m' dim=$'\033[2m' off=$'\033[0m'
[ -t 1 ] || { bold='' red='' yellow='' dim='' off=''; }
say() { printf '%s%s%s\n' "$bold" "$*" "$off"; }
note() { printf '%s%s%s\n' "$yellow" "$*" "$off"; }
fail() {
  printf '\n%sInstallation stopped: %s%s\n' "$red" "$1" "$off" >&2
  shift || true
  for line in "$@"; do printf '  %s\n' "$line" >&2; done
  printf '\n%sNothing you already had was deleted. Fix the problem above and run the installer again.%s\n' "$dim" "$off" >&2
  exit 1
}
# Anything not handled explicitly still ends with a clear message, never a silent exit.
trap 'rc=$?; [ $rc -ne 0 ] && printf "\n%sThe installer failed unexpectedly (line %s, exit %s): %s%s\nPlease report it with this output: https://github.com/notsliver/Ignition/issues\n" "$red" "$LINENO" "$rc" "$BASH_COMMAND" "$off" >&2' ERR
trap 'printf "\n%sCancelled. Re-run the installer to continue; it picks up where it left off.%s\n" "$yellow" "$off" >&2; exit 130' INT

RAW="https://raw.githubusercontent.com/notsliver/Ignition/${IGNITION_REF:-main}"
IMAGE_REPO="notsliver/ignition"

# Developing Ignition when the script sits in a checkout; otherwise a server install from Docker Hub.
here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
if [ -n "$here" ] && [ -f "$here/deploy/docker-compose.yml" ] && [ -d "$here/services" ]; then
  MODE=source
  cd "$here"
else
  MODE=server
fi

# ---- 1. The machine ----
os="$(uname -s)"
arch="$(uname -m)"
if [ "$MODE" = server ]; then
  [ "$os" = Linux ] || fail "Ignition servers run Linux; this is $os." \
    "To try Ignition on a laptop, clone the repository and run ./install.sh from the checkout."
  case "$arch" in
    x86_64|amd64|aarch64|arm64) ;;
    *) fail "This server's CPU architecture ($arch) isn't supported." "Published images are for x86_64 (amd64) and arm64." ;;
  esac
  [ "$(id -u)" = 0 ] || fail "Run the installer as root." \
    "curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | sudo bash"
  command -v curl >/dev/null || fail "curl isn't installed." "Install it first: apt-get install -y curl   (or: dnf install -y curl)"
fi

if [ "${IGNITION_SKIP_CHECKS:-}" != 1 ] && [ "$os" = Linux ]; then
  mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
  if [ "$mem_mb" -gt 0 ] && [ "$mem_mb" -lt 900 ]; then
    fail "This server has ${mem_mb} MB of memory; Ignition needs at least 1 GB (2 GB recommended)." \
      "Choose a bigger server, or add swap and run with IGNITION_SKIP_CHECKS=1 at your own risk."
  elif [ "$mem_mb" -gt 0 ] && [ "$mem_mb" -lt 1800 ]; then
    note "This server has ${mem_mb} MB of memory. Ignition runs, but 2 GB or more is recommended once you deploy apps."
  fi
  target="${IGNITION_DIR:-/opt/ignition}"; [ "$MODE" = source ] && target="$here"
  probe="$target"; while [ ! -d "$probe" ]; do probe="$(dirname "$probe")"; done
  free_mb=$(df -Pm "$probe" 2>/dev/null | awk 'NR==2 {print $4}' || echo 0)
  docker_root="/var/lib/docker"; [ -d "$docker_root" ] || docker_root="/var/lib"
  docker_free_mb=$(df -Pm "$docker_root" 2>/dev/null | awk 'NR==2 {print $4}' || echo 0)
  if [ "${docker_free_mb:-0}" -gt 0 ] && [ "$docker_free_mb" -lt 3000 ]; then
    fail "Only ${docker_free_mb} MB of disk is free for Docker; Ignition needs at least 3 GB." \
      "Free space (docker system prune removes unused images), or use a server with a bigger disk."
  fi
  [ "${free_mb:-1}" -gt 50 ] || fail "There's almost no disk space left at $probe."
fi

# ---- 2. Docker ----
if ! command -v docker >/dev/null; then
  if [ "$MODE" = server ]; then
    say "Installing Docker…"
    if ! curl -fsSL https://get.docker.com -o /tmp/get-docker.sh; then
      fail "Couldn't download Docker's installer from get.docker.com." "Check this server's internet access and DNS (try: curl -I https://get.docker.com)."
    fi
    sh /tmp/get-docker.sh || fail "Docker's installer failed (see above)." \
      "Install Docker yourself (https://docs.docker.com/engine/install/), then run this installer again."
    rm -f /tmp/get-docker.sh
    command -v systemctl >/dev/null && systemctl enable --now docker >/dev/null 2>&1 || true
  else
    fail "Docker isn't installed." "Get it from https://docs.docker.com/get-docker/ (Docker Desktop on macOS and Windows)."
  fi
fi
if ! docker info >/dev/null 2>&1; then
  if [ "$MODE" = server ] && command -v systemctl >/dev/null; then
    systemctl start docker >/dev/null 2>&1 || true
    sleep 2
  fi
  if ! docker info >/dev/null 2>&1; then
    err=$(docker info 2>&1 | tail -1 || true)
    case "$err" in
      *permission\ denied*) fail "This user can't use Docker." "Run the installer as root (sudo), or add yourself to the docker group and log in again." ;;
      *) fail "Docker isn't running." "Start it (systemctl start docker, or open Docker Desktop) and run the installer again." "Docker said: $err" ;;
    esac
  fi
fi
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 (the 'docker compose' command) is missing." \
  "Install the plugin: apt-get install -y docker-compose-plugin   (or update Docker)."
docker_major=$(docker version --format '{{.Server.Version}}' 2>/dev/null | cut -d. -f1 || echo 0)
[ "${docker_major:-0}" -ge 24 ] 2>/dev/null || fail "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null) is too old; Ignition needs Docker 24 or newer." \
  "Upgrade Docker: https://docs.docker.com/engine/install/"

# ---- 3. Install folder ----
if [ "$MODE" = server ]; then
  dir="${IGNITION_DIR:-/opt/ignition}"
  mkdir -p "$dir/deploy" 2>/dev/null || fail "Can't create $dir." "Check the path and permissions, or choose another folder with IGNITION_DIR=/path."
  [ -w "$dir" ] || fail "Can't write to $dir."
  cd "$dir"
  say "Downloading Ignition's compose files to $dir…"
  for f in deploy/docker-compose.yml deploy/proxy-bootstrap.json .env.example; do
    code=$(curl -sS -L -w '%{http_code}' --retry 3 --retry-delay 2 --max-time 30 "$RAW/$f" -o "$f.tmp" 2>/tmp/ignition-curl.err || echo 000)
    if [ "$code" != 200 ]; then
      rm -f "$f.tmp"
      case "$code" in
        000) fail "Couldn't reach GitHub to download $f." "Check this server's internet access and DNS (try: curl -I https://raw.githubusercontent.com)." "curl said: $(tail -1 /tmp/ignition-curl.err 2>/dev/null)" ;;
        404) fail "$f wasn't found on GitHub (HTTP 404)." "If you set IGNITION_REF, check that the branch or tag exists." ;;
        *) fail "GitHub answered HTTP $code for $f." "Wait a minute and try again." ;;
      esac
    fi
    mv "$f.tmp" "$f"
  done
  rm -f /tmp/ignition-curl.err
fi
COMPOSE=(docker compose -p ignition --env-file .env -f deploy/docker-compose.yml)

# ---- 4. .env ----
random() {
  local out
  out=$(head -c "$1" /dev/urandom 2>/dev/null | base64 | tr -d '\n') || true
  [ -n "$out" ] || out=$(docker run --rm alpine:3.20 sh -c "head -c $1 /dev/urandom | base64 | tr -d '\n'")
  printf '%s' "$out"
}
setenv() { # setenv KEY VALUE: replace or append in .env
  if grep -q "^$1=" .env; then sed -i.bak "s|^$1=.*|$1=$2|" .env && rm -f .env.bak; else printf '%s=%s\n' "$1" "$2" >> .env; fi
}
getenv() { grep -E "^$1=" .env 2>/dev/null | tail -1 | cut -d= -f2- || true; }

if [ ! -f .env ] && docker volume inspect ignition_db-data >/dev/null 2>&1; then
  fail ".env is missing, but an Ignition database already exists on this server." \
    "That .env holds the database password and the key that encrypts your secrets; a new one can't open the old data." \
    "Restore your backup of .env into $(pwd), then run the installer again." \
    "Or, to start over and DELETE the old data: docker volume rm ignition_db-data ignition_proxy-data"
fi
first=0
if [ ! -f .env ]; then
  first=1
  say "Creating .env with random secrets…"
  cp .env.example .env || fail "Couldn't create .env in $(pwd)."
  chmod 600 .env
  pg=$(random 24 | tr -d '/+='); key=$(random 32)
  [ -n "$pg" ] && [ -n "$key" ] || fail "Couldn't generate random secrets."
  setenv POSTGRES_PASSWORD "$pg"
  setenv IGNITION_SECRET_KEY "$key"
else
  say "Using existing .env"
  [ -n "$(getenv IGNITION_SECRET_KEY)" ] || fail ".env has no IGNITION_SECRET_KEY." "Restore it from your backup; without it stored secrets can't be decrypted."
  [ -n "$(getenv POSTGRES_PASSWORD)" ] || fail ".env has no POSTGRES_PASSWORD." "Restore it from your backup."
fi
# Ports given to the installer win over .env; otherwise .env, otherwise the defaults.
for kv in "IGNITION_PORT:8080" "IGNITION_HTTP_PORT:80" "IGNITION_HTTPS_PORT:443"; do
  k=${kv%%:*} d=${kv#*:}
  v="${!k:-}"; [ -n "$v" ] || v="$(getenv "$k")"; [ -n "$v" ] || v="$d"
  [[ "$v" =~ ^[0-9]+$ ]] && [ "$v" -ge 1 ] && [ "$v" -le 65535 ] || fail "$k must be a port number (1-65535); it's \"$v\"."
  setenv "$k" "$v"
done
port=$(getenv IGNITION_PORT); http_port=$(getenv IGNITION_HTTP_PORT); https_port=$(getenv IGNITION_HTTPS_PORT)
[ "$port" != "$http_port" ] && [ "$port" != "$https_port" ] && [ "$http_port" != "$https_port" ] || \
  fail "IGNITION_PORT ($port), IGNITION_HTTP_PORT ($http_port) and IGNITION_HTTPS_PORT ($https_port) must all be different."

# ---- 5. Free ports ----
# Who holds a TCP port: a Docker container (other than Ignition's own), or a program on the host.
port_owner() {
  local p=$1 c
  c=$(docker ps --format '{{.Names}}\t{{.Ports}}' 2>/dev/null | grep -E "(0\.0\.0\.0|\[::\]|:::?):$p->" | cut -f1 | grep -v '^ignition-' | head -1 || true)
  if [ -n "$c" ]; then echo "container:$c"; return; fi
  docker ps --format '{{.Names}}\t{{.Ports}}' 2>/dev/null | grep -E "^ignition-.*:$p->" -q && return # ours: replaced on restart
  if command -v ss >/dev/null; then
    c=$(ss -Hltnp "sport = :$p" 2>/dev/null | grep -o 'users:(("[^"]*"' | head -1 | cut -d'"' -f2 || true)
    [ -n "$c" ] && { echo "process:$c"; return; }
    ss -Hltn "sport = :$p" 2>/dev/null | grep -q . && { echo "process:unknown"; return; }
  elif command -v netstat >/dev/null; then
    c=$(netstat -ltnp 2>/dev/null | awk -v p=":$p" '$4 ~ p"$" {print $7}' | head -1 | cut -d/ -f2 || true)
    [ -n "$c" ] && { echo "process:$c"; return; }
  fi
  return 0
}
port_help() { # port_help PORT VAR WHAT
  local p=$1 var=$2 what=$3 owner name
  owner=$(port_owner "$p")
  [ -n "$owner" ] || return 0
  name=${owner#*:}
  local why
  case "$owner" in
    container:*) why="Docker container \"$name\" is using port $p." ;;
    process:unknown) why="Another program is listening on port $p." ;;
    *) why="\"$name\" is listening on port $p." ;;
  esac
  local fixes=("$why" "Ignition needs it for $what." "Fix it in one of these ways:")
  case "$owner" in
    container:*) fixes+=("  • Stop that container:  docker stop $name   (and docker update --restart=no $name to keep it stopped)") ;;
    *)
      case "$name" in
        nginx|apache2|httpd|caddy|traefik|haproxy|lighttpd)
          fixes+=("  • Stop the web server already running here:  systemctl disable --now $name") ;;
        *) [ "$name" != unknown ] && fixes+=("  • Stop $name:  systemctl disable --now $name   (if it's a service)") ;;
      esac ;;
  esac
  fixes+=("  • Or run Ignition on another port:  curl -fsSL $RAW/install.sh | sudo $var=<free port> bash")
  [ "$var" != IGNITION_PORT ] && fixes+=("    (domains then need that port in their URL, and automatic HTTPS needs ports 80 and 443)")
  fail "Port $p is already in use." "${fixes[@]}"
}
if [ "${IGNITION_SKIP_CHECKS:-}" != 1 ]; then
  port_help "$http_port" IGNITION_HTTP_PORT "your apps' domains (HTTP and certificate checks)"
  port_help "$https_port" IGNITION_HTTPS_PORT "your apps' domains over HTTPS"
  port_help "$port" IGNITION_PORT "the dashboard"
fi

# ---- 6. Image to run ----
if [ "$MODE" = server ]; then
  # Detect the address on first install, when asked, or if an earlier attempt stopped before doing it.
  if [ "$first" = 1 ] || [ -n "${IGNITION_HOST:-}" ] || [ "$(getenv IGNITION_PUBLISH_HOST)" = localhost ]; then
    host="${IGNITION_HOST:-}"
    [ -n "$host" ] || host=$(curl -fsS4 --max-time 5 https://api.ipify.org 2>/dev/null || true)
    [ -n "$host" ] || host=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
    [ -n "$host" ] || { host=localhost; note "Couldn't detect this server's public address; links will use localhost. Set IGNITION_HOST=your.domain to change it."; }
    [[ "$host" =~ ^[A-Za-z0-9.:-]+$ ]] || fail "IGNITION_HOST must be an address or domain, like 203.0.113.7 or ignition.example.com."
    setenv IGNITION_PUBLIC_URL "http://${host}:${port}"
    setenv IGNITION_PUBLISH_HOST "$host"
  fi
  tag="${IGNITION_TAG:-}"
  current=$(getenv IGNITION_IMAGE)
  if [ -z "$tag" ] && [ -n "$current" ] && [ "$first" = 0 ]; then
    image="$current"
  else
    if [ -z "$tag" ]; then
      tag=latest
      docker manifest inspect "$IMAGE_REPO:latest" >/dev/null 2>&1 || tag=beta
    fi
    [[ "$tag" =~ ^[A-Za-z0-9_.-]+$ ]] || fail "IGNITION_TAG must be an image tag like 0.1.0-beta, beta or latest."
    image="$IMAGE_REPO:$tag"
  fi
  export IGNITION_IMAGE="$image" # saved to .env only once it has started, so a bad tag isn't remembered
  setenv IGNITION_LOCAL_DEVELOPMENT false
  setenv IGNITION_SOURCE_DIR "$(pwd)"
  export IGNITION_SOURCE_DIR="$(pwd)"
else
  if git rev-parse HEAD >/dev/null 2>&1; then
    export IGNITION_COMMIT="$(git rev-parse HEAD)"
    export IGNITION_VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
    if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then export IGNITION_DIRTY=true; else export IGNITION_DIRTY=false; fi
  fi
  export IGNITION_BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  export IGNITION_SOURCE_DIR="$(pwd)"
fi
"${COMPOSE[@]}" config -q 2>/tmp/ignition-config.err || fail "The compose configuration is invalid." "$(tail -3 /tmp/ignition-config.err)"

# ---- 7. Start ----
log=/tmp/ignition-install.log
# Turn Docker's errors into what to do about them.
explain() {
  local out="$1" what="$2"
  if grep -q "port is already allocated\|address already in use" <<<"$out"; then
    local p; p=$(grep -oE '(0\.0\.0\.0|\[::\]|127\.0\.0\.1)?:[0-9]+ failed' <<<"$out" | grep -oE '[0-9]+ failed' | head -1 | cut -d' ' -f1)
    [ -n "$p" ] || p=$(grep -oE 'Bind for [^ ]+' <<<"$out" | grep -oE '[0-9]+$' | head -1)
    case "$p" in
      "$http_port") port_help "$p" IGNITION_HTTP_PORT "your apps' domains" ;;
      "$https_port") port_help "$p" IGNITION_HTTPS_PORT "your apps' domains over HTTPS" ;;
      "$port") port_help "$p" IGNITION_PORT "the dashboard" ;;
    esac
    fail "Port ${p:-?} is already in use, so $what couldn't start." \
      "Find what's using it:  ss -ltnp 'sport = :${p:-PORT}'   or   docker ps" \
      "Stop it, or choose another port with IGNITION_PORT, IGNITION_HTTP_PORT or IGNITION_HTTPS_PORT."
  fi
  grep -qi "toomanyrequests\|rate limit" <<<"$out" && fail "Docker Hub's download limit was reached." \
    "Wait a few hours, or sign in to raise the limit: docker login, then run the installer again."
  grep -qi "manifest unknown\|not found: manifest\|manifest for .* not found" <<<"$out" && fail "The image ${image:-} doesn't exist on Docker Hub." \
    "Check IGNITION_TAG. Published versions: https://hub.docker.com/r/$IMAGE_REPO/tags"
  grep -qi "no matching manifest" <<<"$out" && fail "There's no image for this server's architecture ($arch)."
  grep -qi "no space left on device" <<<"$out" && fail "The disk is full." "Free space (docker system prune) and run the installer again."
  grep -qi "dial tcp\|i/o timeout\|TLS handshake timeout\|no such host\|connection refused\|network is unreachable" <<<"$out" && \
    fail "Couldn't download from the internet while starting $what." "Check this server's internet access, DNS and any proxy, then run the installer again." "$(grep -iE 'dial tcp|timeout|no such host|refused|unreachable' <<<"$out" | tail -1)"
  grep -qi "is already in use by container\|Conflict. The container name" <<<"$out" && fail "A container named like Ignition's already exists but belongs to another setup." \
    "Remove it (docker rm -f <name> from the message below) or run the uninstaller first." "$(grep -i conflict <<<"$out" | tail -1)"
  grep -qi "permission denied" <<<"$out" && fail "Docker was denied permission while starting $what." "Run the installer as root (sudo)." "$(grep -i 'permission denied' <<<"$out" | tail -1)"
  fail "Starting $what failed." "Docker said:" "$(tail -8 <<<"$out")" "Full log: $log"
}
run() { # run "what" cmd...: show progress, capture output, explain failures
  local what="$1"; shift
  if ! "$@" >"$log" 2>&1; then explain "$(cat "$log")" "$what"; fi
}

docker network inspect ignition >/dev/null 2>&1 || run "the apps network" docker network create ignition
if [ "$MODE" = server ]; then
  say "Downloading $image (this can take a few minutes)…"
  run "Ignition" "${COMPOSE[@]}" pull --quiet api worker db proxy
  say "Starting Ignition…"
  run "Ignition" "${COMPOSE[@]}" up -d --no-build --remove-orphans
  setenv IGNITION_IMAGE "$image"
else
  say "Building and starting Ignition from this checkout (the first build takes a few minutes)…"
  run "Ignition" "${COMPOSE[@]}" up -d --build --remove-orphans
fi

# ---- 8. Wait until ready ----
printf 'Waiting for Ignition to be ready'
ready=0
for _ in $(seq 1 90); do
  status=$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' ignition-api-1 2>/dev/null || echo "missing")
  case "$status" in
    "running healthy") ready=1; break ;;
    exited*|dead*|missing) break ;;
  esac
  printf '.'; sleep 2
done
echo
if [ "$ready" != 1 ]; then
  logs=$("${COMPOSE[@]}" logs --tail 40 api 2>&1 || true)
  dbstate=$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' ignition-db-1 2>/dev/null || echo missing)
  if grep -qi "password authentication failed" <<<"$logs"; then
    fail "Ignition couldn't sign in to its database." \
      "The database was created with a different POSTGRES_PASSWORD than the one in $(pwd)/.env." \
      "Restore the .env that belongs to this data, or delete the old data (docker volume rm ignition_db-data) to start fresh."
  elif grep -qi "secret key\|IGNITION_SECRET_KEY" <<<"$logs"; then
    fail "IGNITION_SECRET_KEY in .env isn't valid." "It must be 32 random bytes, base64 encoded (openssl rand -base64 32)." "$(grep -i secret <<<"$logs" | tail -1)"
  elif [[ "$dbstate" != running* ]]; then
    fail "The database didn't start ($dbstate)." "$("${COMPOSE[@]}" logs --tail 15 db 2>&1 | tail -15)"
  fi
  fail "Ignition didn't become ready in 3 minutes (api is ${status})." "Last lines of its log:" "$(tail -15 <<<"$logs")" \
    "More: cd $(pwd) && ${COMPOSE[*]} logs api"
fi

# ---- 9. Done ----
url=$(getenv IGNITION_PUBLIC_URL); url=${url:-http://localhost:${port}}
code=$("${COMPOSE[@]}" logs api 2>/dev/null | grep -o '"setup_code":"[A-F0-9]*"' | tail -1 | cut -d'"' -f4 || true)
echo
say "Ignition is running at $url"
if [ -n "$code" ]; then
  say "Setup code: ${code}"
  echo "Open the URL, enter the code, and create your owner account."
else
  echo "Sign in with your existing account."
fi
if [ "$MODE" = server ] && command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  for p in "$port" "$http_port" "$https_port"; do
    ufw status 2>/dev/null | grep -qE "^$p(/tcp)?\s" || note "ufw is active and doesn't allow port $p yet: ufw allow $p/tcp"
  done
fi
echo "Installed in $(pwd). Back up .env: IGNITION_SECRET_KEY can't be recovered."
echo "Update from Settings › Updates, or re-run this installer."
