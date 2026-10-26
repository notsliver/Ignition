# IGNITION

A self-hosted platform for deploying apps and databases on your own server. Point it at a Git repository or a container image and IGNITION builds it, runs it in Docker, routes your domains with automatic HTTPS, and gives you logs, metrics, deployment history and rollbacks.

There's no cloud service. Nothing leaves your server except the image pulls and Git clones you ask for.

> **Beta.** IGNITION is usable day to day, but expect rough edges and breaking changes before 1.0. Back up your data and your `IGNITION_SECRET_KEY` before upgrading.

## Quick start

You need a Linux server (a VPS with 2 GB of RAM or more; Ubuntu 22.04+ or Debian 12 recommended) and root access. Docker is installed for you if it's missing.

```sh
curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | sudo bash
```

The installer:

1. installs Docker if needed;
2. downloads IGNITION's compose files to `/opt/ignition` (nothing is built);
3. creates `/opt/ignition/.env` with random secrets;
4. pulls the published image [`notsliver/ignition`](https://hub.docker.com/r/notsliver/ignition) and starts it;
5. prints the address and a one-time setup code.

Open `http://<your-server-ip>:8080`, enter the setup code, and create your owner account. The code is only printed on the server, so only someone with access to it can claim the instance.

**Back up `/opt/ignition/.env`.** `IGNITION_SECRET_KEY` encrypts environment variables and server credentials; if it's lost, they can't be recovered.

### Before you put apps on it

- **Open the ports you need:** 80 and 443 for your apps' domains (HTTPS certificates are issued automatically), and 8080 for the dashboard.
- **Protect the dashboard.** It's served over plain HTTP on port 8080. Limit port 8080 to your own IP in your firewall, or put it behind a reverse proxy with HTTPS and then set `IGNITION_PUBLIC_URL=https://…` and `IGNITION_SECURE_COOKIES=true` in `.env`.
- Point your domains' DNS records at the server, then add them to an app under **Domains**.

### Updating

Go to **Settings › Updates** and click **Update now**. IGNITION pulls the new image and restarts itself in about a minute; your apps keep running. You can also re-run the install command.

To pin a version, set `IGNITION_TAG` when installing (for example `IGNITION_TAG=0.1.0-beta`), or pick a release channel in Settings › Updates:

| Tag | What |
| --- | --- |
| `0.1.0-beta`, `1.2.3` | A specific release |
| `latest` | The newest stable release |
| `beta` | The newest pre-release |
| `edge` | Built from `main` on every change; may break |

### Installer options

Set these before `bash`, e.g. `curl -fsSL … | sudo IGNITION_HOST=ignition.example.com bash`:

| Variable | Default | What |
| --- | --- | --- |
| `IGNITION_DIR` | `/opt/ignition` | Install folder |
| `IGNITION_TAG` | `latest`, or `beta` until 1.0 | Image tag to install |
| `IGNITION_HOST` | the server's public IP | Address or domain used in links |

### Running commands yourself

```sh
cd /opt/ignition
docker compose --env-file .env -f deploy/docker-compose.yml logs -f api worker   # logs
docker compose --env-file .env -f deploy/docker-compose.yml down                  # stop (data is kept)
docker compose --env-file .env -f deploy/docker-compose.yml up -d                 # start
```

### Uninstalling

```sh
curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/uninstall.sh | sudo bash
```

This removes IGNITION's own containers and keeps your data (database, certificates, `.env`) and the apps you deployed, so you can reinstall later. It asks before changing anything. Options go after `bash -s --`:

| Option | What |
| --- | --- |
| `--remove-apps` | Also stop and remove the apps IGNITION deployed |
| `--purge` | Also delete IGNITION's database, certificates, app data volumes, networks, images and `/opt/ignition`. Can't be undone |
| `--yes` | Don't ask (for scripts) |

For example: `curl -fsSL …/uninstall.sh | sudo bash -s -- --purge`. Docker itself is left installed.

## Features

- **Deploy** from a container image or a Git repository (public or private via deploy keys and registry credentials), with Dockerfile or auto-detected Node and Python builds.
- **Databases**: PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Dragonfly and ClickHouse with persistent volumes.
- **Deployments** with live step logs, cancel, retry, redeploy, health-checked rollouts and exact rollbacks (image and variables).
- **Domains** with automatic HTTPS, published ports, private networks between resources.
- **Operate**: container logs, metrics history, web terminal, health checks, resource limits, scheduled tasks, deploy hooks and scoped tokens.
- **Servers**: health, host metrics, diagnostics, Docker inventory and safe cleanup, maintenance mode and scheduled server tasks for this machine, plus monitoring of remote Docker hosts over mutual TLS.
- **Team**: roles (owner, admin, developer, viewer), invites, audit log, encrypted secrets.

Not supported yet: deploying to remote servers, Buildpacks/Nixpacks, templates, and scheduled database backups. Pages that still show sample data are labelled “Preview” in the UI. See [CHANGELOG.md](CHANGELOG.md).

## Developing IGNITION

To work on IGNITION itself, clone the repository and run the installer from the checkout. It builds the image from your code instead of pulling it (Docker Desktop works on macOS and Windows):

```sh
git clone https://github.com/notsliver/Ignition.git && cd Ignition
./install.sh        # macOS, Linux, or Git Bash on Windows
.\install.ps1       # Windows PowerShell
```

Then open http://localhost:8080. See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/development.md](docs/development.md).

## Documentation

| | |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | How the API, worker, job queue and deploy pipeline fit together |
| [docs/development.md](docs/development.md) | Running locally, tests, adding routes and migrations |
| [docs/security.md](docs/security.md) | Trust model and what's enforced. Read before exposing IGNITION beyond localhost |
| [contracts/openapi.yaml](contracts/openapi.yaml) | The HTTP API |

## Repository layout

| Path | What |
| --- | --- |
| [`apps/web`](apps/web) | React dashboard (Vite) |
| [`services`](services) | Go module: `cmd/api` (HTTP API) and `cmd/worker` (job runner) |
| [`services/internal/database/migrations`](services/internal/database/migrations) | SQL migrations, applied on start |
| [`deploy`](deploy) | Docker Compose stack: PostgreSQL, API, worker, proxy |
| [`e2e`](e2e) | Browser end-to-end tests (Playwright) |

## Security

IGNITION controls Docker on its host, which is equivalent to root access. Only give accounts to people you'd trust with that, and don't leave the dashboard open to the internet over plain HTTP. To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Contributing

Bug reports, ideas and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[Apache License 2.0](LICENSE)
