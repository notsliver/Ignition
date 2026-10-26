# Architecture

Ignition is three processes on one machine, started by Docker Compose:

```
browser ──HTTP──▶ api ──SQL──▶ PostgreSQL ◀──SQL── worker ──Docker API──▶ your containers
                   │                                  │
                   └──── Docker API (logs, stats) ────┘
```

- **api** (`services/cmd/api`) serves the dashboard and the JSON API. It validates requests, enforces roles, writes state to PostgreSQL and enqueues jobs. It never builds or deploys inline; it returns a job or deployment ID immediately.
- **worker** (`services/cmd/worker`) claims jobs from PostgreSQL and performs them against Docker: clone, build, pull, create, start, stop, remove. It writes progress and step logs back to the database, which the dashboard polls.
- **PostgreSQL** is the source of truth for everything: users, sessions, projects, resources, encrypted variables, deployments, jobs, job logs and audit events.

## Job queue

`services/internal/jobs` is a durable queue in the `jobs` table.

- **Claiming**: `UPDATE … WHERE id = (SELECT … FOR UPDATE SKIP LOCKED LIMIT 1)`, so any number of workers can poll without double-claiming.
- **Leases**: a claimed job has `lease_until`. The worker extends it with heartbeats every lease/3. If a worker crashes, the lease expires and another worker reclaims the job as a new attempt.
- **Retries**: failures retry with exponential backoff (10 s, 20 s, 40 s …, capped). Errors marked `ErrPermanent` (bad image, missing Dockerfile, failed health check) fail immediately; retrying wouldn't help.
- **Cancellation**: pending jobs cancel at once; running jobs see `cancel_requested` on their next heartbeat and stop.
- **Idempotency**: container names include the deployment number, and a retry removes any half-created container with that name first. Removal of an already-deleted resource succeeds.

The integration tests in `services/internal/jobs/jobs_test.go` cover concurrent claiming, lease recovery, retries, permanent failures and cancellation against a real PostgreSQL.

## Deploy pipeline

1. **Source** — image resources pull the image. Git resources do a shallow `git clone` of a public https repository (git is invoked with an argument list, never a shell; URLs and branches are validated so they can't be read as options).
2. **Build** — the repository is tarred (skipping `.git` and symlinks that escape it) and sent to Docker's build API with the configured Dockerfile, tagged `ignition/<resource>:<commit>`.
3. **Rollout** — a new container starts alongside the old one on the `ignition` network, with a stable DNS alias (the resource name), memory/CPU limits, decrypted environment variables, and a named volume if the resource has a data path. Ports publish on a free host port.
4. **Health verification** — the container must stay running for 5 seconds. If it exits, its last log lines are saved, it's removed, and the deployment fails while the previous release keeps serving.
5. **Switch** — the resource points at the new container, the deployment becomes current, and the old container is stopped and removed.

Redeploys and rollbacks reuse the image built earlier, so they skip steps 1–2.

A status sync loop in the worker inspects running containers every 10 seconds so the dashboard reflects crashes or changes made outside Ignition.

## Data model

See `services/internal/database/migrations`. Highlights:

- `resources` keeps **desired state** (set by users) separately from **observed status** (written by the worker from Docker).
- `deployments` are numbered per resource; rollbacks create new rows rather than rewriting history.
- `env_vars.value_enc` is AES-256-GCM ciphertext bound to `resource_id:key`, so a value copied to another row fails to decrypt.
- `sessions.token_hash` stores SHA-256 of the cookie token, never the token.

## Servers

Infrastructure › Servers (`services/internal/servers`) manages the Docker hosts IGNITION knows about. The local server (this machine, reached through the mounted socket) is created by migration 0015 and is where every resource runs. Remote servers are Docker Engines reached over mutual TLS: the CA pins the engine's identity, the engine's certificate fingerprint is pinned at enrollment, and the client key is sealed with `IGNITION_SECRET_KEY`. Remote servers are monitored, inspected, diagnosed and cleaned up, but the deploy engine doesn't target them.

- **Status** has three independent dimensions: connection (pending, online, degraded, offline; reported as unknown when the last check is older than 60 s), runtime (ready, not installed, unavailable, incompatible) and operator flags (deployments enabled, maintenance, removing). The worker checks every server every 15 s; see the transition rules in `servers/model.go`.
- **Deployment policy** is enforced in `store.CreateDeployment`, the single path for every kind of deploy, so maintenance or disabled deployments refuse manual deploys, hooks, tokens and rollbacks alike.
- **Host metrics** come from `/proc` for the local server (host-wide figures) and from the Engine API only for remote servers; unmeasurable values are stored as NULL, never zero.
- **Server work** (refresh, diagnostics, cleanup, scheduled server tasks) runs as `server` jobs on the same durable queue. Cleanups are previewed first, re-planned at execution, never force, and never touch volumes.

## What's next

These pages exist in the dashboard but still show sample data, labelled "Preview with sample data": Dashboard, Monitor, project-level Deployments, Metrics history, Terminal, Domains, Networking, Storage, Health Checks, Resource Limits, Rollback History, Scheduled Tasks, Access & Secrets, Advanced Settings, and the instance settings pages. Suggested order: resource limits (the API already accepts `memoryMb`/`cpus`), domains with a reverse proxy, scheduled tasks, metrics history, users and API tokens, terminal over a websocket.

Not supported yet, and the UI says so where it comes up: Buildpacks/Nixpacks builds, private repositories and registries, templates, file uploads, database import and backups, deploying to remote servers.
