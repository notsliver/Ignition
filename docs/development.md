# Development

## Requirements

Docker and `make`. Go 1.26 and Node 22 run in containers, so local versions don't matter.

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


### Account profile and security

Profile (`/teams/<team>/settings/profile`) edits the signed-in account across every
team. Name changes save directly. Email changes require recent identity confirmation
and revoke other sessions and pending password-reset links. Avatar files continue to
use the private uploads volume. Self-deletion requires explicit email confirmation and
recent authentication; it cannot remove the last active instance owner or leave a team
without an active owner. Shared workloads remain with their teams.

Security (`/teams/<team>/settings/security`) manages passwords, authenticator-based
2FA, recovery codes, passkeys, and the account's own sessions. Sensitive changes use
five-minute confirmation bound to the current session: password plus the configured
second factor, a user-verified passkey, or fresh sign-in through the linked SSO identity.
Passkeys require HTTPS or localhost and use the hostname in the configured public
URL as their relying-party ID. Changing that hostname requires registering new passkeys.
The browser retains the private key; PostgreSQL stores the public credential, counters,
and metadata. Challenges are stored server-side, bound to a session/browser, consumed
once, and expire after five minutes. Passkey login honors instance SSO restrictions
and continues through TOTP when the account or instance requires it.

Account Integrations uses the instance's existing OIDC provider. Configure it in
Instance administration > Authentication & Security. Connecting is an explicit,
recently authenticated action; issuer and subject together identify the account.
Existing local accounts must sign in normally and connect in Profile rather than being
automatically linked by a matching email. Disconnecting cannot remove the last usable
sign-in method or bypass an SSO-only policy. OIDC access/refresh tokens are not stored.

The account flow browser test is `e2e/account-profile.mjs`. Run it only with disposable
fixtures via `IGNITION_TEST_ACCOUNT_FIXTURE` (session, user UUID, team slug, password,
email), `IGNITION_URL`, and optionally `IGNITION_BROWSER_EXECUTABLE`. It uses Chromium's
virtual authenticator and deletes the fixture account after testing the complete flow.


### Instance-managed account and source integrations

Instance administration > Integrations (`/admin/integrations`) manages the eight
existing source providers. GitHub, GitLab and Bitbucket Cloud support OAuth for
account sign-in and team repository access. Generic Git, Docker Hub, GHCR, GitLab
Registry and private registries retain their supported token/password/deploy-key
methods; the UI does not claim they support repository OAuth.

Use **Developer** mode for team-managed credentials, or **Hosting** mode to hide
Sources and Team Sources from non-platform administrators. Hosting mode also rejects
manual credential mutations through the API, including team-owner requests. Existing
credentials remain usable. Platform administrators can provision credentials for a
team using its existing Sources page; credentials are never global across teams.
Profile and Git deployment/import pickers offer approved OAuth connections. Source
consent runs in an isolated popup, preserving the deployment draft.

For each OAuth provider, configure separate application client IDs/secrets for
identity-only sign-in and repository access, then copy the displayed callback URL to
the provider application. Secrets are encrypted and never returned by the API.
Organization OIDC configuration remains in Authentication & Security.

| Provider | Account sign-in scopes | Repository scopes |
| --- | --- | --- |
| GitHub / GitHub Enterprise | `read:user user:email` | `read:user` for public repositories; opt into `repo` for private access |
| GitLab / self-managed GitLab | `read_user` | `read_user read_api read_repository` |
| Bitbucket Cloud | `account email` | `account repository` on a separate OAuth consumer |

GitHub's classic OAuth `repo` scope includes **write** access. It cannot be described
as read-only private access; use a GitHub App or repository deploy key when granular
read-only access is required. Bitbucket consumer permissions determine grants and
cannot be narrowed reliably per request. Ignition rejects missing or unexpected
OAuth scopes instead of silently accepting additional permissions. Read-only pull
credentials remain the recommendation for registries.

Provider availability, manual credential permission, sign-in, repository OAuth and
verified-account registration are separate controls. Sign-in provisioning respects
instance registration policy, the SSO default role and SSO-only restrictions. Existing
accounts explicitly link their provider identity; matching email never auto-links.
Identity keys include provider origin and subject. Source connections include both
user and team ownership and keep stable source IDs when reauthorized.

OAuth state is browser/session-bound, consumed once, expires in ten minutes and
records the provider configuration revision. GitHub/GitLab use PKCE; Bitbucket uses
its confidential-client flow with browser-bound state. Login and account-link access
tokens are discarded after identity lookup. Deployment access/refresh tokens are
stored encrypted with their source ID as authenticated context. API and worker refresh
through the same row lock, preventing rotating refresh-token races. Changing repository
client credentials, scopes or provider origin requires affected sources to reconnect.

Disabling a provider prevents sign-in and credential use; disabling repository OAuth
also removes those connections from deployment pickers. Running containers stay up.
Managed tokens may authenticate only over HTTPS to their configured provider host;
Git authorization headers are scoped to that host and redirects are disabled. Removed
or suspended authorizing accounts cannot supply deployment credentials.

`e2e/integrations.mjs` exercises admin configuration, secret protection, hosting-mode
navigation/API restrictions, popup isolation and draft preservation. Use only disposable
fixtures via `IGNITION_TEST_INTEGRATIONS_FIXTURE` (adminSession, userSession, team,
teamId, password) and `IGNITION_URL`. Provider protocol, refresh concurrency and tenant
boundaries are additionally covered by local mocked-provider Go integration tests.

### SSO provider presets and branding

Authentication & Security > OpenID Connect offers Google, Discord, Microsoft Entra,
Okta, Authentik, Keycloak, Auth0 and Custom presets. Presets provide issuer guidance,
application setup instructions, brand icons and a sign-in button preview. One instance
OIDC provider remains active at a time. Discovery and signed token verification run
for every configuration; Google/Discord origins are fixed and Entra requires a specific
tenant. Discord requests `openid identify email`, other OIDC providers request
`openid email profile`. Changing a client ID or issuer requires a new secret.

Custom branding uses the existing uploads Docker volume, under `sso/`. Only instance
administrators with `security.manage` can upload or remove it. Images use the same
2 MB PNG/JPEG/WebP decoder, dimension limits and normalized 256px PNG encoding as
avatars. The database stores only its random filename in `sso_brand`. This instance
logo is intentionally public at `/api/auth/oidc/icon` so it can render before sign-in;
account and team images keep their existing authorization. Upload/removal saves
immediately, independently of the provider form, as indicated in the editor.
