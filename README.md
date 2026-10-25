# Ignition

A self-hosted platform for deploying your apps and databases on your own machine. Point it at a Git repository or a container image and Ignition builds it, runs it in Docker, and gives you logs, live stats, deployment history and rollbacks.

Ignition runs locally. There is no cloud service and nothing leaves your machine except the image pulls and Git clones you ask for.

## Quick start

You only need Docker (Docker Desktop on Windows and macOS). Go, Node and PostgreSQL all run inside Docker.

```sh
./install.sh        # macOS, Linux, or Git Bash on Windows
.\install.ps1       # Windows PowerShell
```

It checks Docker, creates `.env` with random secrets the first time, builds and starts everything, waits until it's ready, and prints the URL and a one-time setup code. Open http://localhost:8080, enter the code, and create your owner account. The code only appears on this machine, so only someone with access to it can claim the instance.

Re-run the same command to update after pulling new code; your secrets and data are kept.

**Back up `IGNITION_SECRET_KEY`.** It encrypts every environment variable. If you lose or change it, stored secrets can't be decrypted.

## What works today

| Area | Status |
| --- | --- |
| Sign-in, first-run setup, sessions, roles | Live |
| Projects: create, edit, favourite, archive, delete | Live |
| Resources from a container image or a public Git repo with a Dockerfile | Live |
| Databases: PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Dragonfly, ClickHouse with persistent volumes | Live |
| Deployments: progress, step logs, cancel, retry, redeploy, rollback | Live |
| Start, stop, restart, delete | Live |
| Environment variables, encrypted, secrets hidden by default | Live |
| Container logs and live CPU/memory/network | Live |
| Audit log of sensitive actions | Live (API) |
| Dashboard, Monitor, Metrics history, Terminal, Domains, Networking, Storage, Health Checks, Resource Limits, Rollback History, Scheduled Tasks, Access & Secrets, Advanced Settings, instance settings | Preview with sample data, clearly labelled in the UI |

See [docs/architecture.md](docs/architecture.md) for how it fits together and what's next.

## Repository layout

| Path | What |
| --- | --- |
| [`apps/web`](apps/web) | React dashboard (Vite). API client in `src/api/`. |
| [`services`](services) | Go module: `cmd/api` (HTTP API) and `cmd/worker` (job runner) sharing `internal/`. |
| [`services/internal/database/migrations`](services/internal/database/migrations) | Versioned SQL migrations, applied automatically on start. |
| [`contracts/openapi.yaml`](contracts/openapi.yaml) | The API contract. A test fails if routes and contract drift. |
| [`deploy/docker-compose.yml`](deploy/docker-compose.yml) | PostgreSQL, API and worker. |
| [`e2e`](e2e) | Browser end-to-end test (Playwright). |
| [`docs`](docs) | Architecture, development, configuration and security notes. |

## Development

```sh
make test      # Go unit + integration tests against a throwaway Postgres
make web-dev   # dashboard with hot reload on :5173, proxying /api to :8080
make e2e IGNITION_EMAIL=you@example.com IGNITION_PASSWORD=...   # full browser flow against the running stack
```

More in [docs/development.md](docs/development.md).

## Security

Ignition controls Docker on this machine, which is equivalent to root access. Only give accounts to people you'd trust with that. Read [docs/security.md](docs/security.md) before exposing it beyond localhost.
