# Instance Admin API

Base URL: `/api/admin/v1`. Bearer instance admin tokens only. Create, rotate, and revoke tokens under Instance administration → Admin tokens. Tokens cannot authenticate browser endpoints or mint other tokens.

The machine-readable contract is `contracts/public-admin.openapi.yaml`; GET `/api/admin/v1/openapi.json` serves the same specification.

## Endpoint reference

| Method | Path | Required permission |
|---|---|---|
| GET | `/openapi.json` | `Valid admin token` |
| GET | `/instance` | `settings.view` |
| GET | `/defaults` | `settings.view` |
| GET | `/permissions` | `Valid admin token` |
| GET | `/token` | `Valid admin token` |
| GET | `/capacity` | `servers.view` |
| GET | `/usage` | `usage.view` |
| GET | `/operations/{id}` | `Valid admin token` |
| GET | `/users` | `users.view` |
| POST | `/users` | `users.create` |
| GET | `/users/{id}` | `users.view` |
| PATCH | `/users/{id}` | `users.update` |
| POST | `/users/{id}/suspend` | `users.update` |
| POST | `/users/{id}/resume` | `users.update` |
| POST | `/users/{id}/sessions/revoke` | `users.update` |
| POST | `/users/{id}/setup-link` | `users.setup` |
| POST | `/users/{id}/reset-link` | `users.setup` |
| GET | `/invitations` | `users.view` |
| POST | `/invitations` | `users.invite` |
| DELETE | `/invitations/{id}` | `users.invite` |
| GET | `/teams` | `teams.view` |
| POST | `/teams` | `teams.create` |
| GET | `/teams/{id}` | `teams.view` |
| PATCH | `/teams/{id}` | `teams.update` |
| GET | `/teams/{id}/members` | `teams.view` |
| POST | `/teams/{id}/members` | `teams.members` |
| PATCH | `/teams/{id}/members/{userId}` | `teams.members` |
| DELETE | `/teams/{id}/members/{userId}` | `teams.members` |
| GET | `/teams/{id}/invitations` | `teams.view` |
| POST | `/teams/{id}/invitations` | `teams.members` |
| DELETE | `/teams/{id}/invitations/{invitationId}` | `teams.members` |
| GET | `/teams/{id}/usage` | `usage.view` |
| GET | `/products` | `products.view` |
| POST | `/products` | `products.manage` |
| GET | `/products/{id}` | `products.view` |
| PATCH | `/products/{id}` | `products.manage` |
| DELETE | `/products/{id}` | `products.delete` |
| POST | `/products/{id}/duplicate` | `products.manage` |
| GET | `/products/{id}/availability` | `products.view` |
| GET | `/products/{id}/plans` | `products.view` |
| POST | `/products/{id}/plans` | `products.manage` |
| GET | `/products/{id}/plans/{planId}` | `products.view` |
| PATCH | `/products/{id}/plans/{planId}` | `products.manage` |
| DELETE | `/products/{id}/plans/{planId}` | `products.delete` |
| GET | `/resources` | `resources.view` |
| POST | `/resources` | `resources.provision` |
| GET | `/resources/{id}` | `resources.view` |
| PATCH | `/resources/{id}` | `resources.update` |
| GET | `/resources/{id}/usage` | `usage.view` |
| GET | `/resources/{id}/limits` | `resources.view` |
| GET | `/resources/{id}/operations` | `resources.view` |
| POST | `/resources/{id}/start` | `resources.control` |
| POST | `/resources/{id}/stop` | `resources.control` |
| POST | `/resources/{id}/restart` | `resources.control` |
| POST | `/resources/{id}/suspend` | `resources.suspend` |
| POST | `/resources/{id}/resume` | `resources.suspend` |
| POST | `/resources/{id}/change-plan` | `resources.change-plan` |
| POST | `/resources/{id}/redeploy` | `deployments.create` |
| DELETE | `/resources/{id}` | `resources.delete` |
| GET | `/resources/{id}/deployments` | `deployments.view` |
| GET | `/deployments/{id}` | `deployments.view` |
| POST | `/deployments/{id}/cancel` | `deployments.cancel` |
| GET | `/deployments/{id}/logs` | `logs.view` |
| GET | `/servers` | `servers.view` |
| GET | `/servers/{id}` | `servers.view` |
| GET | `/servers/{id}/capacity` | `servers.view` |
| GET | `/servers/{id}/allocations` | `servers.view` |
| POST | `/servers/{id}/allocations` | `servers.update` |
| PATCH | `/servers/{id}/allocations/{allocationId}` | `servers.update` |
| DELETE | `/servers/{id}/allocations/{allocationId}` | `servers.update` |
| PUT | `/servers/{id}/maintenance` | `servers.update` |
| GET | `/audit-events` | `audit.view` |
| GET | `/audit-events/{id}` | `audit.view` |
| GET | `/webhooks` | `webhooks.view` |
| POST | `/webhooks` | `webhooks.manage` |
| GET | `/webhooks/{id}` | `webhooks.view` |
| PATCH | `/webhooks/{id}` | `webhooks.manage` |
| DELETE | `/webhooks/{id}` | `webhooks.manage` |
| POST | `/webhooks/{id}/test` | `webhooks.manage` |
| GET | `/webhooks/{id}/deliveries` | `webhooks.view` |
| POST | `/webhooks/{id}/deliveries/{deliveryId}/retry` | `webhooks.manage` |

## Account defaults

POST /users creates a non-admin account. Local onboarding requires role viewer or developer; SSO can inherit ssoDefaultRole. It follows limits.personalTeams and creates the personal team, owner membership and Production environment in the same transaction. createdTeam is null when disabled. No automatic email delivery is provided. Issue /users/{id}/setup-link and deliver the single-use URL through your integration. SSO setup requires explicit possession of the setup link and verified matching provider identity; ordinary email matching never links existing accounts.

## Products and resources

Plans use cpus, memoryMb, storageGb, backupGb, ports, informational price text, currency and period. Product placement owns stock/perTeam/perServer/concurrency settings. Prices are informational. Public resource IDs are runtime UUIDs, with managementStatus separate from runtimeStatus. POST /resources accepts either productId plus planId, or configuration for a manual resource with no product/plan. These modes are mutually exclusive. Both require teamId and name and return 202 with resourceId, operationId and deploymentId. Manual resources return managed=false and null productId/planId. They use the existing team-scoped creation flow, including instance defaults, quotas, source access checks and deployment jobs. Manual creation uses the local server; serverId and answers belong to product provisioning. Omit projectId/environmentId in configuration to use the default team environment.

Product and plan:
```json
{"teamId":"<team UUID>","name":"customer-worker","productId":"<product UUID>","planId":"<plan UUID>"}
```

Manual creation:
```json
{"teamId":"<team UUID>","name":"custom-worker","configuration":{"type":"worker","sourceType":"image","image":"busybox:1.37","command":"sleep 3600","cpus":0.5,"memoryMb":256}}
```

Manual configuration supports applications, workers, databases, functions and game servers. Image/Git resources use sourceType and image/repoUrl; functions use type=function, engine and code; game servers use type=game, game, spec and acceptEula when required. The OpenAPI schema documents their fields. Product lifecycle actions (suspend, resume and change-plan) require a product-managed resource.

## Requests

All list endpoints use limit (default 50, maximum 100) and opaque cursors. Mutations accept Idempotency-Key (maximum 200 characters, retained 24 hours); replay uses the same token/method/path and request fingerprint. Different requests with the same key return 409. Stored replay bodies, including setup links, are encrypted. Public mutations and their queue records commit together.

## Lifecycle

Asynchronous resource operations return 202 with resourceId, deploymentId and operationId. Poll /operations/{id}; statuses are queued/running/succeeded/failed/cancelled. Failed operations expose their observed outcome without claiming rollback. Termination requires ?confirm=<current-name>; ?volumes=true additionally needs storage.delete. Data is retained by default. Do not retry a completed irreversible action without the original idempotency key.

## Webhooks

Subscriptions accept resource.provisioned, resource.provisioning_failed, resource.suspended, resource.resumed, resource.plan_changed, resource.terminated and operation.failed. Signing secrets are shown once. Verify HMAC-SHA256 over `<X-Ignition-Timestamp>.<raw-body>` against X-Ignition-Signature (v1=hex), reject old timestamps and deduplicate X-Ignition-Event-Id. HTTPS public destinations only; DNS addresses are validated and pinned on every delivery; redirects and proxies are disabled. Deliveries retry up to eight times and can be retried explicitly.

## Token operations

Management routes use signed-in browser sessions and security.manage. All writes require recent authentication. Expiry defaults to 30 days when expiresAt is omitted; send expiresAt: null for no expiry. Dated expiries cannot exceed one year. The manager offers 7, 30, 90 and 365 days, or Never. Rotation defaults to 30 days; the manager preserves Never for tokens without an expiry. Rotation immediately revokes the old token. Secrets are shown once and stored only as hashes; names, prefixes, scopes, CIDRs, expiry and last use remain visible. Effective token permissions are intersected with the active creator’s current permissions on every request. Revocation stops new authorization; work accepted before revocation may finish.

## Errors

Errors retain error.code, error.message, optional error.fields and requestId. 400 malformed request; 401 invalid credential; 403 permission/policy; 404 unavailable object; 409 state/capacity conflict; 422 validation; 429 rate limit (120 requests/minute/token, Retry-After: 60); 503 unavailable infrastructure. Secret values are never returned by ordinary reads or recorded in API audit payloads.

## Operational details

The API is deliberately separate from browser handlers: instance admin Bearer tokens
work only under `/api/admin/v1`. Resource tokens and session cookies do not authenticate
this API. Browser token management is `/api/settings/admin-tokens`, with rotation at
`/{id}/rotate` and revocation by DELETE `/{id}`. Management writes require recent
password, passkey or linked-provider authentication.

User statuses remain active/suspended. Onboarding state is separate. Local account
creation requires an explicit viewer/developer role; SSO-only onboarding can inherit
ssoDefaultRole. Account suspension preserves the existing sole-active-team-owner guard:
transfer ownership in the dashboard first, or suspend the customer's resources instead.
Completing setup invalidates the other outstanding setup links for that pending account.
No password setup can bypass a subsequently enabled SSO-only policy.

Public resources use `managementStatus` separately from `runtimeStatus`. Catalog reads
call their counts `assignedResources`; plan inputs retain the existing storageGb and
backupGb units and informational price text. Redacted template variables can be preserved
on update with unchanged=true and their original key. Capacity changes to in-use plans
are rejected; create another plan and use /resources/{id}/change-plan instead.

Runtime actions wait for an existing operation/job to finish (409 while busy); termination
can cancel pending work. Suspension cancels queued work and blocks customer deployment.
Successful lifecycle webhooks follow both job completion and observed runtime state.
Termination retains data unless volumes=true and storage.delete is granted. A resource
snapshot remains available after runtime deletion, including when its catalog entry is
later removed. Operations report the retained volume names observed before removal.

Use GET /audit-events filters tokenId, actorId, teamId, resourceId, action, target, since
and until; dates are RFC3339. GET /usage?teamId=UUID returns that team's quota usage.
All administrative mutations use a durable transaction and shared placement lock.
Admission checks do not make remote hosts deployable: the current worker provisions
locally. Tokens are permission-limited even when internal handlers operate as a team's
owner; no public action can grant platform administrator/owner roles.

Validation: PostgreSQL-backed API tests cover account defaults, one-time password/SSO
setup, scope enforcement, live permission removal, CIDRs, idempotent/concurrent retries,
transaction rollback, product provisioning, plan rules, membership protection, lifecycle
operations and termination history. Worker tests verify signed delivery and retry state.
`e2e/admin-tokens.mjs` checks the management UI with mocked API responses and performs
no real administrative writes.

The canonical schema is `contracts/public-admin.openapi.yaml`. After changing it,
run `python scripts/generate-public-admin.py` (requires PyYAML) to regenerate the
embedded JSON and dashboard contract paths. Contract tests compare the embedded
reference, canonical schemas, registered operations and required permissions.

Upgrade compatibility: installations with the earlier admin_tokens prototype retain
its metadata. Legacy credentials are revoked rather than guessing how their scopes
map to the versioned API; create replacement credentials in Admin tokens.

The legacy upgrade fixture runs with `IGNITION_TEST_ADMIN_MIGRATION_DATABASE_URL`
pointing to an empty disposable database: `go test ./internal/database -run
TestLegacyAdminTokenUpgrade`. It preserves legacy metadata, revokes unmapped old
credentials, verifies new inserts and checks that later startups retain new tokens.
