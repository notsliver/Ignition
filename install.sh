#!/usr/bin/env bash
# Install or update IGNITION. Safe to re-run: existing secrets and data are kept.
#
# On a server (VPS), as root:
#   curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | bash
# This downloads the compose files to /opt/ignition and runs the published image
# notsliver/ignition from Docker Hub. Nothing is built.
#
# From a git checkout (developing IGNITION):  ./install.sh
# This builds the image from your checkout instead.
#
# Options (environment variables):
#   IGNITION_DIR=/opt/ignition   where a server install lives
#   IGNITION_TAG=latest          image tag to install (e.g. 0.1.0-beta); defaults to latest, else beta
#   IGNITION_HOST=203.0.113.7    address or domain people use to reach the server (detected if unset)
set -euo pipefail

say() { printf '\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }
RAW="https://raw.githubusercontent.com/notsliver/Ignition/${IGNITION_REF:-main}"
IMAGE_REPO="notsliver/ignition"

# Developing IGNITION when the script sits in a checkout; otherwise a server install from Docker Hub.
here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
if [ -n "$here" ] && [ -f "$here/deploy/docker-compose.yml" ] && [ -d "$here/services" ]; then
  MODE=source
  cd "$here"
else
  MODE=server
fi

# 1. Docker. On a Linux server we can install it; elsewhere we explain.
if ! command -v docker >/dev/null; then
  if [ "$MODE" = server ] && [ "$(uname -s)" = Linux ]; then
    [ "$(id -u)" = 0 ] || fail "Docker isn't installed. Re-run as root (sudo) so the installer can install it."
    say "Installing Docker…"
    curl -fsSL https://get.docker.com | sh
  else
    fail "Docker isn't installed. Get it from https://docs.docker.com/get-docker/"
  fi
fi
docker info >/dev/null 2>&1 || fail "Docker isn't running, or this user can't use it. Start Docker, or run as root (sudo)."
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is required (the 'docker compose' command)."

# 2. Server installs: fetch the compose files into the install folder.
if [ "$MODE" = server ]; then
  dir="${IGNITION_DIR:-/opt/ignition}"
  mkdir -p "$dir/deploy" || fail "Can't create $dir. Run as root (sudo), or set IGNITION_DIR."
  cd "$dir"
  say "Downloading IGNITION's compose files to $dir…"
  for f in deploy/docker-compose.yml deploy/proxy-bootstrap.json .env.example; do
    curl -fsSL "$RAW/$f" -o "$f.tmp" && mv "$f.tmp" "$f" || fail "Couldn't download $f from GitHub."
  done
fi
COMPOSE=(docker compose --env-file .env -f deploy/docker-compose.yml)

# 3. .env with random secrets the first time.
random() { docker run --rm alpine:3.20 sh -c "head -c $1 /dev/urandom | base64 | tr -d '\n'"; }
setenv() { # setenv KEY VALUE: replace or append in .env
  if grep -q "^$1=" .env; then sed -i.bak "s|^$1=.*|$1=$2|" .env && rm -f .env.bak; else printf '%s=%s\n' "$1" "$2" >> .env; fi
}
if [ ! -f .env ] && docker volume inspect ignition_db-data >/dev/null 2>&1; then
  fail ".env is missing but an IGNITION database already exists. Restore your .env (it holds the database password and secret key), or remove the old data with: docker compose -f deploy/docker-compose.yml down -v"
fi
first=0
if [ ! -f .env ]; then
  first=1
  say "Creating .env with random secrets…"
  cp .env.example .env
  setenv POSTGRES_PASSWORD "$(random 24 | tr -d '/+=')"
  setenv IGNITION_SECRET_KEY "$(random 32)"
else
  say "Using existing .env"
fi
port=$(grep -E '^IGNITION_PORT=' .env | cut -d= -f2); port=${port:-8080}

if [ "$MODE" = server ]; then
  # Where people reach this server: the given host, else the public IP.
  if [ "$first" = 1 ]; then
    host="${IGNITION_HOST:-$(curl -fsS4 --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}')}"
    host="${host:-localhost}"
    setenv IGNITION_PUBLIC_URL "http://${host}:${port}"
    setenv IGNITION_PUBLISH_HOST "$host"
  fi
  # The image to run: an explicit tag, the one already installed, latest, or (during the beta) beta.
  tag="${IGNITION_TAG:-}"
  current=$(grep -E '^IGNITION_IMAGE=' .env | cut -d= -f2 || true)
  if [ -z "$tag" ] && [ -n "$current" ] && [ "$first" = 0 ]; then
    image="$current"
  else
    if [ -z "$tag" ]; then
      tag=latest
      docker manifest inspect "$IMAGE_REPO:latest" >/dev/null 2>&1 || tag=beta
    fi
    image="$IMAGE_REPO:$tag"
  fi
  setenv IGNITION_IMAGE "$image"
  setenv IGNITION_LOCAL_DEVELOPMENT false
  export IGNITION_SOURCE_DIR="$(pwd)"
  setenv IGNITION_SOURCE_DIR "$(pwd)"
else
  # Building from a checkout: stamp the build with the commit it comes from.
  if git rev-parse HEAD >/dev/null 2>&1; then
    export IGNITION_COMMIT="$(git rev-parse HEAD)"
    export IGNITION_VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
    if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then export IGNITION_DIRTY=true; else export IGNITION_DIRTY=false; fi
  fi
  export IGNITION_BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  export IGNITION_SOURCE_DIR="$(pwd)"
fi

# 4. Start. Deployed apps share the "ignition" network with the domain proxy.
docker network inspect ignition >/dev/null 2>&1 || docker network create ignition >/dev/null
if [ "$MODE" = server ]; then
  say "Pulling $image…"
  "${COMPOSE[@]}" pull api worker db proxy
  say "Starting IGNITION…"
  "${COMPOSE[@]}" up -d --no-build
else
  say "Building and starting IGNITION from this checkout (the first build takes a few minutes)…"
  "${COMPOSE[@]}" up -d --build
fi

# 5. Wait until the API is healthy.
printf 'Waiting for IGNITION to be ready'
for _ in $(seq 1 90); do
  if [ "$(docker inspect -f '{{.State.Health.Status}}' ignition-api-1 2>/dev/null)" = "healthy" ]; then ready=1; break; fi
  printf '.'; sleep 2
done
echo
[ "${ready:-}" = 1 ] || fail "IGNITION didn't become healthy. Check: cd $(pwd) && ${COMPOSE[*]} logs api"

# 6. What to do next.
url=$(grep -E '^IGNITION_PUBLIC_URL=' .env | cut -d= -f2); url=${url:-http://localhost:${port}}
code=$("${COMPOSE[@]}" logs api 2>/dev/null | grep -o '"setup_code":"[A-F0-9]*"' | tail -1 | cut -d'"' -f4 || true)
echo
say "IGNITION is running at $url"
if [ -n "$code" ]; then
  say "Setup code: ${code}"
  echo "Open the URL, enter the code, and create your owner account."
else
  echo "Sign in with your existing account."
fi
echo "Installed in $(pwd). Back up .env: IGNITION_SECRET_KEY can't be recovered."
echo "Update from Settings › Updates, or re-run this installer."
