# Development

## Requirements

Docker and `make`. Go 1.23 and Node 22 run in containers, so local versions don't matter.

## Running locally

```sh
make env && make up          # API + worker + Postgres on :8080 (serves the built dashboard)
make web-dev                 # optional: dashboard with hot reload on :5173, proxying /api to :8080
make logs                    # follow API and worker logs (JSON)
make setup-code              # one-time code for first-run setup
```

## Tests

```sh
make test   # gofmt check, go vet, unit tests and integration tests against a throwaway Postgres
make e2e IGNITION_EMAIL=… IGNITION_PASSWORD=…   # Playwright browser test against the running stack
```

What's covered:

- `internal/auth` — Argon2id hashing and verification, password policy, session tokens.
- `internal/secrets` — encryption round-trip, tamper detection, context binding, wrong key.
- `internal/worker` — Git URL/branch and Dockerfile path validation, including injection attempts.
- `internal/api` — resource validation, image references, rate limiter, and a contract test that keeps routes and `contracts/openapi.yaml` in sync.
- `internal/jobs` (needs Postgres) — concurrent claiming, lease expiry and recovery, retry/backoff, permanent failures, cancellation, idempotent migrations.
- `e2e/full-flow.mjs` — signs in, creates a project, deploys PostgreSQL and a web app for real, checks Overview, Deployments, Logs and Environment Variables (including that secrets never reach the browser), stops and deletes the app, and confirms a project with resources can't be deleted.

The e2e test deploys real containers. It leaves the test project and its database behind so you can inspect them; delete them from the dashboard afterwards.

## Changing the API

1. Add the route in `services/internal/api/server.go` and the handler.
2. Document it in `contracts/openapi.yaml` — `make test` fails until you do.
3. Add the call to `apps/web/src/api/client.js`.

## Adding a migration

Create `services/internal/database/migrations/NNNN_description.sql`. Migrations run in order on API and worker start, each in a transaction, guarded by an advisory lock. Never edit an applied migration; add a new one.

## Team-aware tests

Set `IGNITION_TEAM_ID` when an E2E account has multiple memberships. API E2E helpers authenticate globally and scope workload requests to that fixture team. The targeted `e2e/teams.mjs` browser test takes disposable fixtures through `IGNITION_TEST_TEAMS_FIXTURE`; it must run without a worker because creation queues jobs.

`IGNITION_TEST_MIGRATION_DATABASE_URL` points at a separate, empty disposable database for the 0024-to-0025 upgrade fixture. Never use an installation database for integration or migration tests. The fixture preserves resource/environment IDs and ciphertext and checks migration replay.


### Profile pictures and team icons

The API stores normalized 256×256 PNG files in the `ignition_uploads` Docker named
volume, mounted at `/var/lib/ignition/uploads`. Only generated filenames are stored
in PostgreSQL (`users.avatar_file` and `teams.icon_file`). Uploads accept PNG, JPEG,
and WebP up to 2 MiB; the server checks decoded dimensions, crops the centre square,
and strips metadata by re-encoding. Replacing or removing an image deletes its old file.

Compose provisions and mounts the volume on the API service. Container replacement
and ordinary `docker compose down` preserve it. Back up and restore this volume
alongside PostgreSQL to retain images; PostgreSQL backups alone contain only filenames.
For a locally run API, `IGNITION_UPLOADS_DIR` overrides the default `./data/uploads`
(relative to the API working directory). Every API replica must share the same uploads
storage. Account and team image URLs require an active signed-in session. Avatar reads allow
the owner, shared teammates and instance administrators; team icon reads allow team
members and instance administrators. Team owners/admins can update their own team
icon, and users can update only their own avatar. Resource tokens cannot use image
endpoints. Responses use `Cache-Control: private, no-store` and
`Cross-Origin-Resource-Policy: same-origin`; authorization is checked before GET, HEAD,
conditional and range responses. Removing membership immediately denies subsequent
image requests, including previously issued URLs.
