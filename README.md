<div align="center">

# Ignition

**Deploy apps, databases and game servers on your own server.**

Point Ignition at a Git repository or a container image. It builds it, runs it in Docker, routes your domains with automatic HTTPS, and gives you logs, metrics, backups and one-click rollbacks.

[Discord](https://discord.gg/rnW3eEN8Yw) · [Changelog](CHANGELOG.md) · [Docker Hub](https://hub.docker.com/r/notsliver/ignition) · [Security](SECURITY.md)

</div>

> [!NOTE]
> **Ignition is in beta.** It's usable day to day, but expect rough edges and breaking changes before 1.0. Back up your data and your `IGNITION_SECRET_KEY` before upgrading.

There's no cloud service. Nothing leaves your server except the image pulls and Git clones you ask for.

## Quick start

On a Linux server (2 GB of RAM or more; Ubuntu 22.04+ or Debian 12 recommended), as root:

```sh
curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/install.sh | sudo bash
```

Open the address it prints (`http://<your-server-ip>:8080`), enter the one-time setup code, and follow the setup. The installer adds Docker if it's missing, pulls the published image and creates `/opt/ignition/.env` with random secrets.

> [!IMPORTANT]
> **Back up `/opt/ignition/.env`.** Its `IGNITION_SECRET_KEY` encrypts your environment variables and credentials. If it's lost, they can't be recovered.

**Already running Coolify, Easypanel or another panel?** The installer leaves it alone. If it holds ports 80, 443 or 8080, Ignition runs alongside it on free ports. Move your apps over, then switch the ports when you're ready; the installer prints how.

### Before you put apps on it

- **Open ports** 80 and 443 for your apps' domains, and 8080 for the dashboard.
- **Protect the dashboard.** It's served over plain HTTP on port 8080. Limit that port to your own IP, or put it behind HTTPS and set `IGNITION_PUBLIC_URL=https://…` and `IGNITION_SECURE_COOKIES=true` in `.env`.
- **Point your domains' DNS** at the server, then add them to an app under **Domains**.

## Features

| | |
| --- | --- |
| **Deploy** | From a container image or a Git repository (public or private), with a Dockerfile or auto-detected Node.js, Bun and Python builds. Import whole projects from Git or Docker Compose. |
| **Databases** | PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Dragonfly and ClickHouse, with persistent volumes and data imports. |
| **Functions** | One handler file in Node.js, Bun or Python, with no Dockerfile. Edit it in the browser and test it from the dashboard. |
| **Game servers** | Minecraft (Paper, Fabric and more) with a console, mods, worlds and IP:port allocations. |
| **Deployments** | Live logs, health-checked rollouts, cancel, retry, and exact rollbacks of image and variables. |
| **Domains & networking** | Automatic HTTPS, traffic routing, DNS, published ports, firewall and private networks between apps. |
| **Operate** | Logs, metrics, web terminal, file manager, backups, health checks, resource limits, scheduled tasks and automations. |
| **Teams** | Team workspaces with roles, invitations and per-project access. Instance administration has quotas, maintenance mode and an audit log. |
| **Security** | Two-factor authentication, single sign-on, granular permissions and encrypted secrets. |

Not built yet: deploying to remote servers, Buildpacks/Nixpacks and templates. See the [changelog](CHANGELOG.md) for what's new.

## Updating

Go to **Instance administration › Updates** and click **Update now**. Ignition pulls the new image and restarts in about a minute; your apps keep running. Re-running the install command works too.

<details>
<summary>Release channels and pinning a version</summary>

Set `IGNITION_TAG` when installing (for example `IGNITION_TAG=0.1.2-beta`), or pick a channel in Instance administration › Updates:

| Tag | What |
| --- | --- |
| `0.1.2-beta`, `1.2.3` | A specific release |
| `latest` | The newest stable release |
| `beta` | The newest pre-release |
| `edge` | Built from `main` on every change; may break |

</details>

<details>
<summary>Installer options</summary>

Set these before `bash`, e.g. `curl -fsSL … | sudo IGNITION_HOST=ignition.example.com bash`:

| Variable | Default | What |
| --- | --- | --- |
| `IGNITION_DIR` | `/opt/ignition` | Install folder |
| `IGNITION_TAG` | `latest`, or `beta` until 1.0 | Image tag to install |
| `IGNITION_HOST` | the server's public IP | Address or domain used in links |
| `IGNITION_PORT` | `8080` | Dashboard port |
| `IGNITION_HTTP_PORT`, `IGNITION_HTTPS_PORT` | `80`, `443` | Ports your apps' domains are served on |

</details>

<details>
<summary>Running commands yourself</summary>

```sh
cd /opt/ignition
docker compose --env-file .env -f deploy/docker-compose.yml logs -f api worker   # logs
docker compose --env-file .env -f deploy/docker-compose.yml down                  # stop (data is kept)
docker compose --env-file .env -f deploy/docker-compose.yml up -d                 # start
```

</details>

<details>
<summary>Uninstalling</summary>

```sh
curl -fsSL https://raw.githubusercontent.com/notsliver/Ignition/main/uninstall.sh | sudo bash
```

This removes Ignition's own containers and keeps your data (database, certificates, `.env`) and the apps you deployed, so you can reinstall later. It asks before changing anything. Options go after `bash -s --`:

| Option | What |
| --- | --- |
| `--remove-apps` | Also stop and remove the apps Ignition deployed |
| `--purge` | Also delete Ignition's database, certificates, app data volumes, networks, images and `/opt/ignition`. Can't be undone |
| `--yes` | Don't ask (for scripts) |

Docker itself is left installed.

</details>

## Community

Questions, ideas or something broken? Join us on **[Discord](https://discord.gg/rnW3eEN8Yw)** or [open an issue](https://github.com/notsliver/Ignition/issues).

## Developing Ignition

Clone the repository and run the installer from the checkout. It builds the image from your code instead of pulling it (Docker Desktop works on macOS and Windows):

```sh
git clone https://github.com/notsliver/Ignition.git && cd Ignition
./install.sh        # macOS, Linux, or Git Bash on Windows
.\install.ps1       # Windows PowerShell
```

Then open http://localhost:8080.

| | |
| --- | --- |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to contribute |
| [docs/development.md](docs/development.md) | Running locally, tests, adding routes and migrations |
| [docs/architecture.md](docs/architecture.md) | How the API, worker, job queue and deploy pipeline fit together |
| [docs/security.md](docs/security.md) | Trust model and what's enforced. Read before exposing Ignition beyond localhost |
| [contracts/openapi.yaml](contracts/openapi.yaml) | The HTTP API |

<details>
<summary>Repository layout</summary>

| Path | What |
| --- | --- |
| [`apps/web`](apps/web) | React dashboard (Vite) |
| [`services`](services) | Go module: `cmd/api` (HTTP API) and `cmd/worker` (job runner) |
| [`services/internal/database/migrations`](services/internal/database/migrations) | SQL migrations, applied on start |
| [`deploy`](deploy) | Docker Compose stack: PostgreSQL, API, worker, proxy |
| [`e2e`](e2e) | Browser end-to-end tests (Playwright) |

</details>

## Security

Ignition controls Docker on its host, which is equivalent to root access. Only give accounts to people you'd trust with that, and don't leave the dashboard open to the internet over plain HTTP. To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)
