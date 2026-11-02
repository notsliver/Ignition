#!/usr/bin/env bash
# Publish a release from this machine, without GitHub Actions:
#   scripts/release.sh 0.1.0-beta              a release (tags git v0.1.0-beta, pushes the image, creates the GitHub release)
#   scripts/release.sh patch|minor|major       the next version after the latest tag (0.1.2-beta -> 0.1.3-beta)
#   scripts/release.sh 0.1.2-beta --replace    rebuild an existing release from the current main: same version,
#                                              its tag moves here and its GitHub release is recreated
#   scripts/release.sh edge                    the current main as :edge and :sha-<short>
# Add --yes (or RELEASE_YES=1) to skip the questions.
#
# Does the same as .github/workflows/release.yml: a linux/amd64 + linux/arm64 image of
# notsliver/ignition on Docker Hub, stamped with the version and commit.
# Needs: docker with buildx, `docker login` to Docker Hub, and the GitHub CLI (gh) for releases.
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${IGNITION_RELEASE_IMAGE:-notsliver/ignition}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
say() { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

usage="Usage: scripts/release.sh <version|patch|minor|major|edge> [--replace] [--yes]   e.g. scripts/release.sh 0.1.3-beta"
version="" replace=0 yes="${RELEASE_YES:-}"
for arg in "$@"; do
  case $arg in
    --replace) replace=1 ;;
    --yes|-y) yes=1 ;;
    -h|--help) printf '%s\n' "$usage"; exit 0 ;;
    -*) fail "Unknown option $arg. $usage" ;;
    *) [ -z "$version" ] || fail "One version at a time. $usage"; version="${arg#v}" ;;
  esac
done
[ -n "$version" ] || fail "$usage"
confirm() { [ "$yes" = 1 ] && return 0; read -r -p "$1 [y/N] " ok; [ "$ok" = y ] || [ "$ok" = Y ]; }

semver='^([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.]+)?$'
latest_tag() { git tag -l 'v*' | sed 's/^v//' | grep -E "$semver" | sort -V | tail -1; }
# bump KIND VERSION: the next patch, minor or major version, keeping a pre-release suffix like -beta.
bump() {
  [[ "$2" =~ $semver ]] || return 1
  local x=${BASH_REMATCH[1]} y=${BASH_REMATCH[2]} z=${BASH_REMATCH[3]} pre=${BASH_REMATCH[4]}
  case $1 in patch) z=$((z + 1)) ;; minor) y=$((y + 1)) z=0 ;; major) x=$((x + 1)) y=0 z=0 ;; esac
  echo "$x.$y.$z$pre"
}

# Releases come from a clean, pushed main.
[ -z "$(git status --porcelain)" ] || fail "The working tree has uncommitted changes. Commit or stash them first."
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || fail "Release from main."
git fetch -q origin main
git fetch -q --tags origin 2>/dev/null || true
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main isn't the same as origin/main. Push or pull first."
commit="$(git rev-parse HEAD)"
short="${commit:0:7}"

case $version in
  patch|minor|major)
    last=$(latest_tag)
    [ -n "$last" ] || fail "There's no earlier release tag to count from. Give the version, e.g. 0.1.0-beta."
    version=$(bump "$version" "$last")
    say "The latest release is $last, so this one is $version." ;;
esac

# Tags, matching the workflow.
tags=()
if [ "$version" = edge ]; then
  [ "$replace" = 0 ] || fail "--replace is for versions; edge is always replaced."
  tags=("$IMAGE:edge" "$IMAGE:sha-$short")
else
  [[ "$version" =~ $semver ]] || fail "Version must look like 1.2.3 or 1.2.3-beta.1"
  exists=0
  if git rev-parse -q --verify "refs/tags/v$version" >/dev/null || [ -n "$(git ls-remote --tags origin "refs/tags/v$version" 2>/dev/null)" ]; then
    exists=1
  fi
  if [ "$exists" = 1 ] && [ "$replace" = 0 ]; then
    fail "v$version is already released. Either:
  • Release the fix as the next version (recommended: existing installs are offered it as an update):
      scripts/release.sh $(bump patch "$version")      (or: scripts/release.sh patch)
  • Or rebuild v$version from the current main, replacing it:
      scripts/release.sh $version --replace
    Installs already on $version won't be offered the new build, because the version doesn't change."
  fi
  [ "$exists" = 1 ] || [ "$replace" = 0 ] || fail "v$version hasn't been released, so there's nothing to replace. Drop --replace."
  [[ "$version" =~ $semver ]]
  tags=("$IMAGE:$version")
  if [ -n "${BASH_REMATCH[4]}" ]; then
    tags+=("$IMAGE:beta")
  else
    tags+=("$IMAGE:${BASH_REMATCH[1]}.${BASH_REMATCH[2]}" "$IMAGE:${BASH_REMATCH[1]}" "$IMAGE:latest")
  fi
fi

docker buildx version >/dev/null 2>&1 || fail "docker buildx is required."
gh auth status >/dev/null 2>&1 || [ "$version" = edge ] || fail "Sign in to the GitHub CLI first: gh auth login"

# The GitHub release's notes come from the version's CHANGELOG.md section.
notes=""
if [ "$version" != edge ]; then
  notes="$(awk -v v="$version" '$0 ~ "^## "v" " {on=1; next} on && /^## / {exit} on' CHANGELOG.md)"
  if [ -z "$(tr -d '[:space:]' <<<"$notes")" ]; then
    warn "CHANGELOG.md has no \"## $version\" section, so the GitHub release would have no notes."
    confirm "Release without notes?" || fail "Cancelled. Add a \"## $version — $(date +%Y-%m-%d)\" section to CHANGELOG.md, commit, push and run again."
  fi
fi

say "Releasing $version ($short) as:"
printf '  %s\n' "${tags[@]}"
if [ "$replace" = 1 ]; then
  old=$(git rev-list -n1 "v$version" 2>/dev/null || true)
  warn "This replaces the existing v$version${old:+ (${old:0:7})}: its image is overwritten, its tag moves to $short and its GitHub release is recreated."
fi
confirm "Continue?" || fail "Cancelled."

# Multi-platform builds need a buildx builder that can push manifest lists.
builder=ignition-release
docker buildx inspect "$builder" >/dev/null 2>&1 || docker buildx create --name "$builder" --driver docker-container >/dev/null
args=()
for t in "${tags[@]}"; do args+=(--tag "$t"); done
say "Building and pushing (the first multi-platform build takes a while)…"
docker buildx build --builder "$builder" --platform "$PLATFORMS" --push \
  --build-arg IGNITION_VERSION="$version" --build-arg IGNITION_COMMIT="$commit" \
  --build-arg IGNITION_BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --build-arg IGNITION_DIRTY=false \
  --label org.opencontainers.image.version="$version" --label org.opencontainers.image.revision="$commit" \
  --label org.opencontainers.image.source=https://github.com/notsliver/Ignition --label org.opencontainers.image.licenses=Apache-2.0 \
  --provenance=true --sbom=true "${args[@]}" . \
  || fail "The build or push failed. If it's a permission error, run: docker login -u notsliver"

[ "$version" = edge ] && { say "Pushed ${tags[*]}"; exit 0; }

if [ "$replace" = 1 ]; then
  # Only now that the new image is pushed: had the build failed, the old release would still be whole.
  say "Removing the old v$version tag and GitHub release…"
  gh release delete "v$version" --yes >/dev/null 2>&1 || true
  git push -q origin ":refs/tags/v$version" 2>/dev/null || true
  git tag -d "v$version" >/dev/null 2>&1 || true
fi
say "Tagging v$version and creating the GitHub release…"
git tag -a "v$version" -m "IGNITION $version"
git push -q origin "v$version"
prerelease=()
[[ "$version" == *-* ]] && prerelease=(--prerelease)
gh release create "v$version" "${prerelease[@]}" --title "$version" --notes "$(printf '%s\n\n---\n\n**Docker image:** `%s:%s` (linux/amd64, linux/arm64)\n\n**Install on a server:**\n```sh\ncurl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | sudo bash\n```\n\n**Update an existing install:** Instance administration › Updates › Update now.\n' "$notes" "$IMAGE" "$version")"

say "Released $version: https://hub.docker.com/r/$IMAGE/tags"
