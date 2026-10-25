# Security model

## Trust boundary

The api and worker mount `/var/run/docker.sock`. Control of the Docker socket is equivalent to root on this machine: anyone who can deploy an arbitrary image can mount the host filesystem. So:

- **Every Ignition account with the developer role or above should be someone you'd trust with root on this machine.**
- Don't expose port 8080 to the internet. If you need remote access, put it behind a VPN or an authenticating reverse proxy, serve it over HTTPS, and set `IGNITION_SECURE_COOKIES=true` and `IGNITION_PUBLIC_URL`.
- User-supplied commands (a resource's start command) run inside that resource's own container via `sh -c`, never on the host.

## What's enforced

| Control | Implementation |
| --- | --- |
| First-run setup | Only possible while no users exist, and only with a random code printed to the API logs. Creation is atomic, so two people can't both claim the instance. |
| Passwords | Argon2id (64 MiB, 3 passes), minimum 12 characters. |
| Sessions | 32 random bytes in an HttpOnly, SameSite=Lax cookie; only its SHA-256 is stored. 7-day expiry; deleted on sign-out. |
| CSRF | State-changing requests must carry an `Origin` (or `Referer`) matching the dashboard host or `IGNITION_PUBLIC_URL`. |
| Brute force | 10 sign-in attempts per minute per IP; an account locks for 15 minutes after 5 failed passwords. Unknown emails take as long as wrong passwords. |
| Authorization | Every route declares a minimum role (viewer < developer < admin < owner), checked server-side. |
| Secrets at rest | AES-256-GCM with `IGNITION_SECRET_KEY`, bound to the resource and key name. |
| Secrets in transit to the UI | Secret values are omitted from API responses. Revealing one needs admin and is audited. |
| Audit log | Sign-ins, failed sign-ins, setup, project and resource changes, deploys, cancels, variable changes (names only) and reveals. |
| Input validation | Names, image references, Git URLs, branches, Dockerfile paths, data paths and ports are validated by allowlist patterns; JSON bodies reject unknown fields and are capped at 1 MB. |
| Command injection | Docker is driven through its HTTP API with JSON bodies; git is executed with an argument list after validation. No shell is built from user input. |
| Path traversal | Dockerfile paths must stay inside the repository; build contexts skip symlinks that escape the checkout. |

## Known limitations

- The sign-in rate limiter is in memory, so it resets when the API restarts. Account lockout is in the database and persists.
- Only one instance-wide role per user; per-project access isn't implemented yet.
- Published container ports bind on all interfaces (`0.0.0.0`) so you can reach apps from your network. Databases are private unless you choose to publish their port.
- There's no TLS termination built in; use a reverse proxy.
