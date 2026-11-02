# Teams as the ownership boundary

Status: implemented in the working tree. Migration `0025_teams.sql` applies at application startup. Verification used disposable databases; the running installation has not been migrated.

## Required behavior

1. Every resource has a non-null `team_id`, independent of whether it belongs to a project.
2. Projects group resources within a team. Creating, deploying, or opening a resource does not require a project.
3. Team membership and project assignments determine access. An instance-wide user role does not automatically grant access to team workloads.
4. A user can belong to several teams, with different permissions in each.
5. The active team scopes every workload list, count, search, notification, detail page, mutation, and live connection.
6. Infrastructure and Hosting are experiences of the same team and use the same memberships, projects, and resources.

## Current implementation and affected paths

| Area | Current behavior | Required change |
| --- | --- | --- |
| Schema | `0001_init.sql` requires resource `project_id` and `environment_id`; projects have globally unique names. | Add team ownership, make project assignment nullable, scope names appropriately. |
| Authorization | `api/server.go:requireUser` checks `users.role` and user-level overrides through `api/permissions.go`. | Resolve membership and target scope before evaluating team/project permissions. |
| Store | `store/store.go` lists projects and resources without an ownership filter; resource selects join environments and some aggregate queries join projects. | Require a team scope for application queries and preserve projectless resources in joins and aggregates. |
| Direct SQL | Dashboard, imports, access handlers, databases, games, and other handlers query `Store.DB` directly. | Audit these paths alongside store methods; changing only resource list methods is insufficient. |
| Creation | `api/handlers.go`, `provision.go`, database/static/game creation, and project imports pass mandatory project IDs. | Share a team-owned provisioning path with optional project assignment. |
| Networking | `0009_ignition_networks.sql` describes private networks per environment and shared networks across projects. | Preserve environment identity; restrict shared networks and their memberships to one team. |
| Imports | `api/projectimport.go` copies resource columns dynamically. | Explicitly exclude ownership columns from copying and derive destination ownership from the authenticated scope. |
| Navigation | `app/routing.js` nests resource URLs under projects; `App.jsx` and `AppSidebar.jsx` require project context for resource navigation. | Add canonical team/resource routes and independent resource navigation. |
| Creation UI | Dashboard quick actions use `ResourceQuickCreate.jsx`; Projects and Resources export shared creation hooks. | Reuse those forms with a required active team and an optional project selector. |
| Onboarding | `onboarding.go` stores a single instance `useCase`. | Create the first team and store enabled experiences on that team. |

The existing architecture and security documents contain older feature descriptions. The files above are the implementation references for this change.

## Ownership and schema

```text
User ── Team membership ── Team
                            ├── Experiences: Infrastructure, Hosting
                            ├── Projects ── Project assignments
                            ├── Environments
                            ├── Resources ── optional project_id
                            │     └── Deployments, variables, volumes, tasks, tokens…
                            └── Sources, shared networks, DNS integrations, automations…
```

Use snake_case database columns and the existing camelCase JSON convention (`team_id` / `teamId`). Proposed tables and changes:

| Entity | Fields and constraints |
| --- | --- |
| `teams` | `id`, `name`, unique stable `slug`, `created_by`, timestamps. Rename changes the display name, not ownership or IDs. |
| `team_memberships` | `(team_id, user_id)` primary key, team role, scoped permission overrides, timestamps. One user may have several memberships. |
| `team_experiences` | `(team_id, experience)` primary key; supported values `infrastructure`, `hosting`. Enabling one never creates another owner or duplicates resources. |
| `team_invitations` | Team, normalized email, offered role, inviter, hashed token, expiry, accepted/revoked state. Accepting an invitation adds membership to the existing global identity when present. |
| `projects` | Required `team_id`; unique `(team_id, lower(name))`; unique `(team_id, id)` for composite references. |
| `project_assignments` | Team, project, member user, project role, scoped overrides. Composite foreign keys require both the project and the membership to belong to the same team. |
| `environments` | Required `team_id`; deployment context independent of a project. Stable ID remains the network identity. |
| `project_environments` | Optional links from a project to team environments for project filters and creation defaults. Neither side owns the other. |
| `resources` | Required `team_id`; nullable `project_id`; required team-owned `environment_id`; optional project reference must match the resource's team. |
| Team-owned integrations | Explicit `team_id` on sources, shared networks, DNS providers, team automations, and other independently addressable team configuration. |
| Child records | Deployments, jobs, logs, imports, tasks, volumes, tokens, shares, and audit events carry or inherit team ownership through a required, checked relation. Persist `team_id` on queued work and audit history so ownership survives deletion. |

Database constraints enforce matching teams for resource/project/environment/source relations and network attachments. Application validation returns a useful error first, but cannot replace those constraints. Resource `team_id` is immutable through ordinary update or move endpoints. A move between projects changes grouping within the same team. Cross-team transfers require a separate future workflow and are excluded from this implementation.

Project deletion removes grouping and assignments, not workloads: detach resources and remove `project_environments` links in a transaction. Retain environments, deployed containers, deployments, secrets, volumes, and jobs. Avoid cascading composite foreign keys that null `team_id` along with `project_id`; use an explicit detach transaction or a column-specific referential action supported by the deployed PostgreSQL version.

### Environments and resource names

Keep every existing environment ID and private network name during migration. Convert existing project/environment relationships into `project_environments` links. Display names such as Production may repeat; environment identity is its ID, with project context shown where needed to distinguish choices. Remove the assumption that an environment's identity depends on a parent project.

Provide a default Production environment for standalone resources in each team. Existing project creation can continue creating a Production environment and linking it to the project as a convenience. Git/Compose imports and duplication create separate environments by default so service names and private-network aliases do not collide.

Keep resource names unique within an environment, across project groupings. Reassigning a resource to a project does not change its environment or network. Changing deployment environments is a separate operation and may change connectivity; it must not happen as a side effect of changing project grouping.

### Physical infrastructure and instance administration

Keep global identities, credentials for signing in, sessions, MFA, installation settings, and platform administration separate from team membership. A team admin cannot suspend a global identity or change another team's membership by editing instance users.

Proposed shared-capacity model: the physical host registry remains platform-owned, with explicit `team_server_assignments` granting deployment and team-filtered monitoring capabilities. Existing singleton local-server behavior must not be duplicated into conflicting host records. Grant the migrated team access to its existing capacity. New teams receive no host access until assigned capacity.

Filter workload inventories and resource metrics on shared hosts by team. Raw host inspect, host-wide logs/metrics, cleanup, firewall/proxy administration, and unmanaged-container operations require explicit platform permissions; team admin is insufficient. Sources and team configuration remain team-owned even when the host is shared. Any host-wide conflict, such as a bound port or hostname/path route, remains globally exclusive and returns a generic conflict without revealing another team's object.

Team authorization provides application ownership isolation. Shared Docker hosts are not a hostile-tenant execution sandbox; do not advertise that stronger isolation as part of this change.

## Authorization rules

Retain the existing permission catalog and its default action thresholds where applicable, but evaluate workload permissions from the current membership and project assignment. Keep platform permissions in a separate catalog/scope; do not reuse `users.role` as a team role or turn team admin into a platform operator.

| Assignment | Default authority |
| --- | --- |
| Team owner | Full team control, including membership and ownership. Cannot remove/demote the last owner. |
| Team admin | Manage team resources, projects, integrations, and members, subject to owner-only restrictions. |
| Team developer | Existing deployment/development permissions across the team. |
| Team viewer | Read team workloads without modifying them. |
| Team member | No workload access by default; access comes from project assignments or explicit team grants. |
| Project admin/developer/viewer | Corresponding permissions inside the assigned project. Never grants team membership administration, standalone-resource access, host administration, or access to another project. |

Evaluation order:

1. Authenticate the global identity or bound service credential.
2. Resolve the requested team and require current membership; platform role alone does not satisfy this condition for workload access.
3. Load the target through the team scope, including child IDs such as deployment/job/import/token IDs. Return the existing generic not-found response for an out-of-scope object.
4. Compute allows from the team role/grants plus the applicable project role/grants.
5. Apply team denies and applicable project denies before allowing the action. Owner-only team authority cannot be granted through a project assignment or removed by an override.
6. Check every referenced object and every member of a bulk operation within the same scope before enqueueing or applying side effects.

For a `member` with only project assignments, lists, counts, search results, dashboard alerts, and monitoring include only accessible projects and their resources. Such a member cannot see standalone resources. Detaching a resource removes access inherited solely from its old project; attaching it grants access to its new project's assignments. These changes must be audited and clearly stated in the grouping UI.

Assignments and grants cannot delegate authority outside the actor's scope. Project assignees must already be team members. Protect last-owner changes transactionally, including concurrent requests. Removing team membership removes its project assignments and invalidates dependent user-issued credentials. Removing one membership does not sign the user out of other teams or delete their global identity.

Resource tokens, deploy hooks, automation hooks, file shares, and service credentials resolve team/resource ownership from their stored credential record, never a caller-selected team. Their scope cannot exceed the issuer's delegated authority. Recheck revocation and target ownership at use time. Existing anonymous shares remain explicit bearer capabilities; they must not become general team credentials.

## API and request scope

Use explicit team IDs in canonical team-scoped routes. Global authentication/account routes remain outside that prefix.

| Route family | Purpose |
| --- | --- |
| `GET/POST /api/teams` | List the caller's memberships; create a team and its first owner atomically. |
| `GET/PATCH /api/teams/{teamId}` | Read/update team details. |
| `/api/teams/{teamId}/members`, `/invitations` | Manage team membership and invitations. |
| `/api/teams/{teamId}/projects` | Optional project groups and their settings. |
| `/api/teams/{teamId}/projects/{id}/assignments` | Project-scoped assignments. |
| `GET/POST /api/teams/{teamId}/resources` | List/create workloads, including those without a project. Creation accepts nullable `projectId` and a team-owned `environmentId`. |
| `/api/teams/{teamId}/resources/{id}/…` | Resource details and existing operational actions. |
| `/api/teams/{teamId}/dashboard`, `/environments`, `/sources`, `/networks`, etc. | Team-filtered aggregate and infrastructure experiences. |
| `/api/account/…`, `/api/auth/…` | Global identity, preferences, sessions, MFA, and sign-in. |

The route identifies scope; payload `teamId` cannot override it. Use one authorization helper for membership/target checks and typed store scopes requiring team and actor identity. Remove unscoped application store calls and direct SQL in handlers. Reserve explicitly named privileged store paths for authentication, migration, platform operations, credential resolution, and worker dispatch.

Add PostgreSQL row-level policies as defense in depth for team-owned and inherited records. Use a non-superuser, non-`BYPASSRLS` request role without table-owner bypass. Set request scope transaction-locally, test pooled connection reuse, and keep privileged worker/platform access explicit. Policies must protect updates and inserts as well as reads. RLS alone does not replace action-level permissions or checks before Docker/external side effects. If transaction-local scoping spans long streams, do not hold a database transaction or pool connection for the stream's lifetime.

Do not retain an ambiguous implicit-default-team fallback for old APIs. Maintain legacy resource links in the browser during transition, but callers of old unscoped workload API routes must migrate or receive a clear deprecation/error response. Update contract tests and `contracts/openapi.yaml` together with route changes, including nullable project IDs and team-scoped responses.

## Frontend and team switching

Use the existing compact sidebar identity menu for team selection. Keep team management in compact dialogs/settings within the existing canvas; no new surrounding workspace shell. Provide creation/invitation entry points without replacing the current application layout.

Canonical browser paths are `/teams/{teamId}/…`, with `/teams/{teamId}/resources/{resourceId}` valid for every resource. Project pages become filtered views, rather than necessary parents of resource detail. Resource navigation has its own team context even when `projectId` is null. Legacy project URLs resolve only after authorization and redirect to the canonical resource URL; IDs must never bypass membership.

Switching teams must:

- Validate membership before rendering the destination; redirect to that team's landing view.
- Clear project, environment, resource, server, selection, open creation dialogs, search, and pending form context. Apply the existing unsaved-change handling first.
- Abort old requests and close terminal/log/SSE/WebSocket connections. Ignore responses from the previous team, including successful responses that arrive after an abort.
- Remount or key the application subtree and data caches by team ID. `useQuery` currently retains prior data while fetching, so dependency changes alone are insufficient.
- Include team ID in every cache key and API/stream URL. Capture the team when starting a mutation; never read a newly selected team when completing an older operation.
- Keep active-team selection per browser tab in the URL. A last-used team preference is advisory; one tab must not silently switch another tab's API scope.
- Handle revoked membership by discarding its cached data and offering remaining teams. A user with no memberships sees a compact create/join-team state, not a default team's resources.

Dashboard, Monitor, Projects, Domains, Networking, Sources, server views, counts, and quick actions all use the active team. Resource creation displays the team, offers `No project`, and selects a team-owned deployment environment. Existing resource creation forms and shared hooks remain the implementation base. Project access management replaces the current unsupported placeholder with scoped assignments.

Infrastructure and Hosting entry points use `team_experiences`. Enabling Hosting keeps the same team ID and permissions. Preserve the existing indication that unimplemented Hosting features are unavailable; this change does not implement customer billing or provisioning.

## Migration and rollout

Implement as coordinated stages with explicit release gates. Keep multi-team creation and switching disabled until all application entry points enforce scope. Do not expose partially filtered teams while older API/worker processes can still write unowned records.

1. **Add ownership schema.** Add teams, memberships, assignments, experiences, invitations, and initially nullable ownership fields. Identify every root and inherited relation. Add indexes for team-scoped list queries and composite references. Leave existing IDs intact.
2. **Backfill existing installations.** Create one deterministic legacy/default team per installation. Copy all existing users' workload roles and overrides into memberships in that team, preserving current access. Preserve historical platform administration separately. Assign every project, environment, resource, integration, automation, invitation, credential, and retained job/audit record to that team or an explicit platform scope. Convert project/environment relationships to links. Map the saved onboarding use case to the team's experiences; Infrastructure remains available.
3. **Validate and constrain.** Assert zero unowned workloads and zero mismatched team references; compare pre/post resource, project, deployment, job, and membership counts. Make workload ownership non-null. Replace global project-name uniqueness with team uniqueness. Update copy/import code before enforcing new columns. Replace cascading project ownership with detach behavior. Preserve physical hostname/port constraints.
4. **Enforce backend scope.** Ship typed team-scoped reads/writes, membership/project policy, route contracts, RLS/request database role, protected bulk and streaming endpoints, and scoped integrations. Inventory every raw SQL call and every route, including public capability routes and secondary IDs. Disable unscoped workload APIs at the gate.
5. **Support standalone provisioning and workers.** Make `projectId` nullable end to end. Replace inner project joins and nullable string scans in aggregates. Persist/verify job team and target ownership, including retries, schedules, hooks, approvals, deployment history, imports, backups, and removal. Add the team ID to new runtime labels without renaming live containers, images, volumes, or networks. Never target another team's resource via automation selectors or shared-network membership.
6. **Ship UI scope.** Team selection, canonical URLs, request cancellation/cache separation, independent resource detail, standalone creation, project assignments, membership/invitations, and experience selection. Update all link producers, exports, dashboard alerts, token examples, and onboarding. Fresh setup creates the first global owner, team owner membership, default environment, and team experience configuration atomically where possible.
7. **Release multi-team behavior.** Enable team creation/switching only after the integration and browser gates below pass against a migrated installation and a fresh installation. New signups/SSO identities receive no legacy-team access unless invited or covered by an explicit team provisioning rule.

Back up and test restoration before the data migration. Re-running migrations must not create a second default team or duplicate memberships. Preserve resource IDs so existing secret encryption context remains valid. Do not rewrite historical ciphertext as part of this migration.

Rollback after enabling projectless resources or creating multiple teams cannot safely run an old binary that assumes mandatory projects and global access. Keep the new schema and deploy a compatible fix, or restore the coordinated pre-migration backup during a maintenance window. Document the cutover boundary; do not promise a destructive down migration as rollback.

## Verification and acceptance gates

Extend the existing PostgreSQL integration harness in `services/internal/store/*_test.go` and API tests; add focused team browser coverage under `e2e/`. These tests exercise real authorization and database constraints, not just menu visibility.

| Scenario | Required assertion |
| --- | --- |
| Existing installation upgrade | All legacy objects and users retain access in one team; IDs, ciphertext, network identity, and running workloads remain intact. Migration replay does not duplicate ownership. |
| Fresh onboarding | One team and owner membership exist; Infrastructure/Hosting choice belongs to that team. Failed setup cannot leave an owner without a team. |
| Two teams, one user | Team A admin can be Team B viewer; writes in B are denied regardless of A's role or global workload history. |
| Cross-team references | Resource/project/environment/source/network mismatch is rejected by the database, including direct SQL and bulk requests. |
| Direct and secondary IDs | Another team's resource, deployment, job, import, token, task, share, or automation cannot be read or acted upon through guessed IDs. |
| Counts and discovery | Dashboard, monitoring, search, alerts, exports, server workload views, and logs contain no out-of-scope rows or counts. |
| Project assignments | Project-only members see only assigned projects; project grants never authorize standalone or unrelated resources; denial precedence is tested. |
| Projectless resources | Create, deploy, open, monitor, change, and remove application/database/worker/function/game resources without a project. URLs and nullable responses work throughout. |
| Grouping lifecycle | Attach/detach/move within a team changes project-based access without redeploying or changing networks. Deleting a project retains its workloads and storage. |
| Member lifecycle | Invite an existing global user into a second team; accept an expired/revoked invitation unsuccessfully; revoke one membership without affecting others; prevent concurrent last-owner removal. |
| Team switching | Delayed Team A results never render in B; pending mutations retain A scope; dialogs and streams close; browser Back/reload restores an authorized team; two tabs stay independent. |
| Jobs and capabilities | Hooks, resource tokens, scheduled jobs, import/automation targets, retries, and approvals cannot cross ownership boundaries; membership/credential revocation takes effect. |
| Pool reuse and RLS | Requests alternate teams on reused connections without scope leakage; request role cannot bypass policies; absent scope fails closed; privileged worker paths are explicit. |
| Shared host | Team workload views are filtered; raw inspect/cleanup and global settings remain platform-only; shared network and port conflicts do not reveal foreign objects. |
| Experiences | Both experiences use the same team identity, membership, and resource ownership. Switching experiences does not copy or transfer resources. |

Run the repository's Go formatting, vet, unit/integration suite, API contract checks, frontend production build, and browser flows. Update existing E2E helpers to capture a team scope instead of relying on global listing. Treat complete route/query coverage and the two-team isolation tests as the release gate, not merely a passing resource-list test.

## Proposed defaults to carry into implementation

- Preserve current users and workloads in one default team; do not automatically enroll future users into it.
- Team owner/admin/developer/viewer/member roles; project admin/developer/viewer assignments; explicit deny takes precedence over ordinary grants.
- Project deletion detaches workloads. Team deletion is excluded from the first release until workload, credential, capacity, and retention behavior is specified.
- Environments remain team-owned technical deployment contexts; project assignment alone never changes connectivity.
- Physical shared capacity remains platform-owned with explicit team assignments; host administration is separate from team membership.
- Infrastructure and Hosting are enabled on a team; Hosting product features beyond ownership and selection remain outside this change.

These are implementation choices made explicit for review, rather than additional changes to the six required behaviors.

## Implementation and cutover notes

- Team-scoped routes are registered centrally for all workload handlers. Old unscoped workload routes return `team_required`; account and platform routes remain global.
- Request queries run under the non-superuser `ignition_request` role with transaction-local scope. Migration requires an operator able to create/grant this role. Authentication, global proxy reconciliation, and worker dispatch use explicitly privileged paths.
- Membership/project changes increment an authorization version. The browser discards stale team state, and long-lived logs/terminals recheck authorization. Existing issuer-less tokens remain capabilities for their single resource; they do not become general team identities.
- Project deletion preserves workloads and deployment environments. Changing grouping is separate from changing environments/networks. Team settings manages invitations, roles, experiences, and operator-assigned capacity.
- Physical shared Docker network names remain installation-wide to preserve existing network identity. Ownership, discovery, and attachments are team-scoped; conflicting physical names remain unavailable across teams.
- The existing global `users.role` storage remains the platform/account role for compatibility. Team and project roles are separate assignments; resource editors use effective scoped permissions.
- Deploy the API and worker together after backing up PostgreSQL. An older binary cannot safely serve this schema once teams/projectless resources are in use. No production cutover was performed as part of this code change.

Validation performed: Go vet and the full Go test suite with PostgreSQL integration tests; an upgrade from migration 0024 with existing workload/ciphertext fixtures; frontend production build; targeted browser checks in `e2e/teams.mjs` for standalone resources, effective team permissions, switching, independent tabs, creation without a project, and revocation. Full container deployment E2E was not run; the isolated browser API had no Docker socket or worker.
