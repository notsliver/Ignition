# Contributing to IGNITION

Thanks for helping. Issues and pull requests are both welcome.

## Reporting bugs

Open an issue with what you did, what you expected, what happened, and your IGNITION version (Settings › Updates) and OS. API and worker logs help: `make logs`. Never paste your `.env`, secrets or private keys.

Security problems go to [SECURITY.md](SECURITY.md), not public issues.

## Development

Everything runs in Docker; you need Docker and `make`. See [docs/development.md](docs/development.md).

```sh
make env && make up   # stack on :8080
make web-dev          # dashboard with hot reload on :5173
make test             # gofmt, go vet, unit and integration tests
```

## Pull requests

- Keep changes focused; one feature or fix per PR.
- `make test` must pass. Add tests for new behaviour where it's practical.
- New API routes go in `services/internal/api/server.go` **and** `contracts/openapi.yaml` (a test enforces this).
- Schema changes are new files in `services/internal/database/migrations`. Never edit an applied migration.
- Match the existing UI: use the components in `apps/web/src/ui`, Heroicons for icons, and provide reduced-motion alternatives for animation. Don't show sample data as if it were real.
- Write user-facing text plainly: say what happened and what to do next.

By contributing you agree your work is licensed under the [Apache License 2.0](LICENSE).

## Releasing (maintainers)

Releases are published from a maintainer's machine; GitHub Actions isn't required.

```sh
docker login -u notsliver          # once, with a Docker Hub access token
make release VERSION=0.2.0-beta    # or VERSION=edge for the current main
```

`scripts/release.sh` checks that `main` is clean and pushed, builds `notsliver/ignition` for linux/amd64 and linux/arm64, pushes the version tags (`0.2.0-beta` + `beta`, or `0.2.0` + `0.2` + `0` + `latest`), tags git, and creates the GitHub release with the notes from `CHANGELOG.md`. Add the version's section to `CHANGELOG.md` first. `.github/workflows/release.yml` does the same on a pushed `v*` tag when Actions are available.

