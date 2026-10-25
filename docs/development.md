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
