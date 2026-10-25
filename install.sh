#!/usr/bin/env bash
# One command to install or update IGNITION:  ./install.sh
# Safe to re-run: existing secrets and data are kept.
set -euo pipefail
cd "$(dirname "$0")"

COMPOSE=(docker compose --env-file .env -f deploy/docker-compose.yml)
say() { printf '\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

# 1. Docker must be installed and running.
command -v docker >/dev/null || fail "Docker isn't installed. Get it from https://docs.docker.com/get-docker/"
docker info >/dev/null 2>&1 || fail "Docker isn't running. Start Docker Desktop (or the docker service) and try again."
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is required (the 'docker compose' command)."

# 2. Create .env with random secrets the first time. Uses Docker, so openssl isn't needed.
random() { docker run --rm alpine:3.20 sh -c "head -c $1 /dev/urandom | base64 | tr -d '\n'"; }
if [ ! -f .env ] && docker volume inspect ignition_db-data >/dev/null 2>&1; then
  fail ".env is missing but an IGNITION database already exists. Restore your .env (it holds the database password and secret key), or remove the old data with: docker compose -f deploy/docker-compose.yml down -v"
fi
if [ ! -f .env ]; then
  say "Creating .env with random secrets…"
  cp .env.example .env
  pg=$(random 24 | tr -d '/+=')
  key=$(random 32)
  sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pg}|; s|^IGNITION_SECRET_KEY=.*|IGNITION_SECRET_KEY=${key}|" .env && rm -f .env.bak
  say "Back up IGNITION_SECRET_KEY in .env. Without it, stored secrets can't be decrypted."
else
  say "Using existing .env"
fi
port=$(grep -E '^IGNITION_PORT=' .env | cut -d= -f2); port=${port:-8080}

# 3. Build and start. Deployed apps share the "ignition" network with the domain proxy.
docker network inspect ignition >/dev/null 2>&1 || docker network create ignition >/dev/null
say "Building and starting IGNITION (the first build takes a few minutes)…"
"${COMPOSE[@]}" up -d --build

# 4. Wait until the API is healthy.
printf 'Waiting for IGNITION to be ready'
for _ in $(seq 1 60); do
  if [ "$(docker inspect -f '{{.State.Health.Status}}' ignition-api-1 2>/dev/null)" = "healthy" ]; then ready=1; break; fi
  printf '.'; sleep 2
done
echo
[ "${ready:-}" = 1 ] || fail "IGNITION didn't become healthy. Check: ${COMPOSE[*]} logs api"

# 5. Tell the user what to do next.
code=$("${COMPOSE[@]}" logs api 2>/dev/null | grep -o '"setup_code":"[A-F0-9]*"' | tail -1 | cut -d'"' -f4 || true)
echo
say "IGNITION is running at http://localhost:${port}"
if [ -n "$code" ]; then
  say "Setup code: ${code}"
  echo "Open the URL, enter the code, and create your owner account."
else
  echo "Sign in with your existing account."
fi
echo "Stop it with:  ${COMPOSE[*]} down   (your data is kept)"
