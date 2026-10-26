#!/usr/bin/env bash
# Publish a release from this machine, without GitHub Actions:
#   scripts/release.sh 0.1.0-beta     a release (tags git v0.1.0-beta, pushes the image, creates the GitHub release)
#   scripts/release.sh edge           the current main as :edge and :sha-<short>
#
# Does the same as .github/workflows/release.yml: a linux/amd64 + linux/arm64 image of
# notsliver/ignition on Docker Hub, stamped with the version and commit.
# Needs: docker with buildx, `docker login` to Docker Hub, and the GitHub CLI (gh) for releases.
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${IGNITION_RELEASE_IMAGE:-notsliver/ignition}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
say() { printf '\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

version="${1:-}"
[ -n "$version" ] || fail "Usage: scripts/release.sh <version|edge>   e.g. scripts/release.sh 0.1.0-beta"
version="${version#v}"

# Releases come from a clean, pushed main.
[ -z "$(git status --porcelain)" ] || fail "The working tree has uncommitted changes. Commit or stash them first."
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || fail "Release from main."
git fetch -q origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main isn't the same as origin/main. Push or pull first."
commit="$(git rev-parse HEAD)"
short="${commit:0:7}"

# Tags, matching the workflow.
tags=()
if [ "$version" = edge ]; then
  tags=("$IMAGE:edge" "$IMAGE:sha-$short")
else
  [[ "$version" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.]+)?$ ]] || fail "Version must look like 1.2.3 or 1.2.3-beta.1"
  git rev-parse -q --verify "refs/tags/v$version" >/dev/null && fail "Tag v$version already exists."
  tags=("$IMAGE:$version")
  if [ -n "${BASH_REMATCH[4]}" ]; then
    tags+=("$IMAGE:beta")
  else
    tags+=("$IMAGE:${BASH_REMATCH[1]}.${BASH_REMATCH[2]}" "$IMAGE:${BASH_REMATCH[1]}" "$IMAGE:latest")
  fi
fi

docker buildx version >/dev/null 2>&1 || fail "docker buildx is required."
gh auth status >/dev/null 2>&1 || [ "$version" = edge ] || fail "Sign in to the GitHub CLI first: gh auth login"

say "Releasing $version ($short) as:"
printf '  %s\n' "${tags[@]}"
if [ "${RELEASE_YES:-}" != 1 ]; then
  read -r -p "Continue? [y/N] " ok
  [ "$ok" = y ] || [ "$ok" = Y ] || fail "Cancelled."
fi

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

say "Tagging v$version and creating the GitHub release…"
git tag -a "v$version" -m "IGNITION $version"
git push -q origin "v$version"
notes="$(awk -v v="$version" '$0 ~ "^## "v" " {on=1; next} on && /^## / {exit} on' CHANGELOG.md)"
prerelease=()
[[ "$version" == *-* ]] && prerelease=(--prerelease)
gh release create "v$version" "${prerelease[@]}" --title "IGNITION $version" --notes "$(printf '%s\n\n---\n\n**Docker image:** `%s:%s` (linux/amd64, linux/arm64)\n\n**Install on a server:**\n```sh\ncurl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | sudo bash\n```\n\n**Update an existing install:** Settings › Updates › Update now.\n' "$notes" "$IMAGE" "$version")"

say "Released $version: https://hub.docker.com/r/$IMAGE/tags"
