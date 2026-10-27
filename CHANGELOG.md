# Changelog

## 0.1.1-beta — 2026-10-27

- Automations: an automations engine with its own dashboard, also available per server.
- Projects: imports, database data, external databases and functions.
- Installer: preflight checks (OS, architecture, memory, disk, Docker version, free ports) and clear explanations when something fails, including ports already in use.
- `uninstall.sh` to remove IGNITION, keeping your data unless you ask otherwise.

## 0.1.0-beta — 2026-10-26

First public beta.

- Projects, environments and resources from container images or Git repositories, with Dockerfile and auto-detected builds.
- Managed databases with persistent volumes.
- Deployments with step logs, health-checked rollouts, cancel, retry, redeploy and exact rollbacks.
- Domains with automatic HTTPS, port publishing and private networks.
- Logs, metrics history, terminal, health checks, resource limits, scheduled tasks, deploy hooks and tokens.
- Infrastructure › Servers: status and capability model, host metrics, diagnostics, Docker inventory and safe cleanup, maintenance mode, scheduled server tasks, and remote Docker hosts over mutual TLS (monitoring only).
- Users, roles, invites, audit log, instance settings and update checks.

Known limitations: no deploying to remote servers, no Buildpacks/Nixpacks, no templates, no scheduled database backups. The Dashboard, project Deployments and some instance settings pages still show labelled sample data.
