#!/usr/bin/env bash
# Remove IGNITION from this server.
#
#   curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/uninstall.sh | sudo bash
#
# By default this stops and removes IGNITION itself (dashboard, worker, database container, domain
# proxy) and keeps your data: the database volume, HTTPS certificates, .env, and every app you
# deployed. Nothing is deleted without asking. Options:
#   --remove-apps   also stop and remove the containers of apps IGNITION deployed
#   --purge         also delete IGNITION's data: database, certificates, app volumes, networks, images
#                   and the install folder (this can't be undone)
#   --yes           don't ask for confirmation (for scripts)
#   IGNITION_DIR=/opt/ignition   where IGNITION is installed
set -euo pipefail

say() { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

remove_apps=0 purge=0 yes=0
for arg in "$@"; do
  case "$arg" in
    --remove-apps) remove_apps=1 ;;
    --purge) purge=1; remove_apps=1 ;;
    --yes|-y) yes=1 ;;
    -h|--help) sed -n '2,15p' "$0" 2>/dev/null || true; exit 0 ;;
    *) fail "Unknown option $arg. Use --remove-apps, --purge or --yes." ;;
  esac
done

# Prompts read from the terminal, so this works when piped from curl too.
ask() {
  [ "$yes" = 1 ] && return 0
  [ -r /dev/tty ] || fail "Can't ask for confirmation without a terminal. Re-run with --yes if you're sure."
  local reply
  read -r -p "$1 [y/N] " reply </dev/tty
  [ "$reply" = y ] || [ "$reply" = Y ]
}

command -v docker >/dev/null || fail "Docker isn't installed, so there's nothing of IGNITION's left to remove."
docker info >/dev/null 2>&1 || fail "Docker isn't running, or this user can't use it. Run as root (sudo)."

# Find the install: the given folder, the default server folder, or the folder this script is in.
here="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
dir="${IGNITION_DIR:-}"
for d in "$dir" /opt/ignition "$here"; do
  if [ -n "$d" ] && [ -f "$d/deploy/docker-compose.yml" ]; then dir="$d"; break; fi
done
project=ignition
compose=()
if [ -n "$dir" ] && [ -f "$dir/deploy/docker-compose.yml" ]; then
  env_file=()
  [ -f "$dir/.env" ] && env_file=(--env-file "$dir/.env")
  compose=(docker compose -p "$project" "${env_file[@]}" -f "$dir/deploy/docker-compose.yml")
fi

apps=$(docker ps -aq --filter label=ignition.resource 2>/dev/null | wc -l | tr -d ' ')
core=$(docker ps -aq --filter "label=com.docker.compose.project=$project" | wc -l | tr -d ' ')
[ "$core" -gt 0 ] || [ "$apps" -gt 0 ] || [ -n "$dir" ] || fail "No IGNITION install was found on this server."

say "This will remove IGNITION from this server."
echo "  • Stop and remove IGNITION's containers ($core): dashboard/API, worker, database, domain proxy"
if [ "$remove_apps" = 1 ]; then
  echo "  • Stop and remove the $apps container(s) of apps IGNITION deployed"
else
  echo "  • Keep the $apps container(s) of apps IGNITION deployed (they keep running; add --remove-apps to remove them)"
fi
if [ "$purge" = 1 ]; then
  warn "  • DELETE IGNITION's database, HTTPS certificates, app data volumes, networks, images${dir:+ and $dir}"
  warn "    This can't be undone. Back up anything you need first."
else
  echo "  • Keep the database, certificates, app data volumes${dir:+ and $dir (with .env)}, so you can reinstall later"
fi
echo
ask "Continue?" || fail "Cancelled. Nothing was changed."
if [ "$purge" = 1 ]; then
  ask "Really delete all IGNITION data? Type y only if you have backups." || fail "Cancelled. Nothing was changed."
fi

# 1. IGNITION itself.
say "Stopping IGNITION…"
if [ ${#compose[@]} -gt 0 ]; then
  if [ "$purge" = 1 ]; then "${compose[@]}" down --volumes --remove-orphans || true
  else "${compose[@]}" down --remove-orphans || true; fi
fi
# Anything left of the compose project (e.g. the install folder is gone), plus updater/helper containers.
leftover=$(docker ps -aq --filter "label=com.docker.compose.project=$project"; docker ps -aq --filter label=ignition.updater; docker ps -aq --filter label=ignition.probe; docker ps -aq --filter label=ignition.hostprobe; docker ps -aq --filter label=ignition.backup)
[ -n "$leftover" ] && docker rm -f $leftover >/dev/null 2>&1 || true

# 2. Deployed apps.
if [ "$remove_apps" = 1 ]; then
  ids=$(docker ps -aq --filter label=ignition.resource)
  if [ -n "$ids" ]; then
    say "Removing $(echo "$ids" | wc -l | tr -d ' ') app container(s)…"
    docker rm -f $ids >/dev/null
  fi
fi

# 3. Data, networks and images.
if [ "$purge" = 1 ]; then
  say "Deleting data…"
  vols=$( (docker volume ls -q --filter "label=com.docker.compose.project=$project"; docker volume ls -q --filter label=ignition.resource; docker volume ls -q | grep -E '^ign-[0-9a-f]{8}-' || true) | sort -u)
  [ -n "$vols" ] && docker volume rm $vols >/dev/null 2>&1 || true
  nets=$( (docker network ls -q --filter label=ignition.managed=true; docker network ls -q --filter name='^ignition$') | sort -u || true)
  [ -n "$nets" ] && docker network rm $nets >/dev/null 2>&1 || true
  imgs=$(docker images -q --filter reference='notsliver/ignition' --filter reference='ignition/*' | sort -u)
  [ -n "$imgs" ] && docker rmi -f $imgs >/dev/null 2>&1 || true
  if [ -n "$dir" ] && [ "$dir" != "/" ] && [ -f "$dir/deploy/docker-compose.yml" ] && [ ! -d "$dir/.git" ]; then
    rm -rf "$dir"
  elif [ -d "$dir/.git" ]; then
    warn "Kept $dir: it's a git checkout. Delete it yourself if you don't need it."
  fi
fi

echo
say "IGNITION was removed."
if [ "$purge" = 0 ]; then
  echo "Your data was kept.${dir:+ To reinstall with it, run the installer again (it reuses $dir/.env).}"
  [ "$remove_apps" = 0 ] && [ "$apps" -gt 0 ] && echo "Deployed apps are still running, but nothing manages their domains or restarts them now."
fi
echo "Docker itself was left installed."
