# Configuration and Operations

`uniauth serve` reads and validates deployment environment variables, then reserves
`UNIAUTH_LISTEN_ADDR` before initializing the service. With no command, `serve` is
selected. `uniauth help` prints usage.

If listener binding fails, the command does not open or migrate SQLite, update
pending bootstrap configuration, reconcile the Console App, or rotate its private
credential. A second command rejected at an occupied address leaves the running
instance's Console login and identity linking usable, even when the rejected
command supplies different configuration. If service initialization fails after
binding, the reservation is closed and the original error remains available.
The initialized service owns the listener through serving and shutdown. This
ordering protects listener-bind failures; it does not guarantee rollback of every
later initialization failure or permit separate instances to share one database.

## Environment

| Variable | Required or default | Meaning |
| --- | --- | --- |
| `UNIAUTH_PUBLIC_URL` | Required | External absolute origin, such as `https://auth.example`; no path other than `/`, query, fragment, or user information. |
| `UNIAUTH_INITIAL_ADMIN_EMAIL` | Required | Plain email address for one-time administrator bootstrap, up to 254 bytes and without whitespace or display-name syntax. |
| `UNIAUTH_LISTEN_ADDR` | `:8080` | HTTP listener address in `host:port` form. |
| `UNIAUTH_DATABASE_PATH` | `uniauth.db` | SQLite database path; parent directory must exist. |
| `UNIAUTH_ALLOW_LOOPBACK_HTTP` | `false` | Set exactly `true` to allow HTTP for loopback public origins and registered product callbacks. |
| `UNIAUTH_MICROSOFT_CLIENT_ID` | Optional pair | Personal-account Microsoft application Client ID. |
| `UNIAUTH_MICROSOFT_CLIENT_SECRET` | Optional pair | Microsoft application Client Secret. |
| `UNIAUTH_GOOGLE_CLIENT_ID` | Optional pair | Google OAuth client ID. |
| `UNIAUTH_GOOGLE_CLIENT_SECRET` | Optional pair | Google OAuth client Secret. |
| `UNIAUTH_GITHUB_CLIENT_ID` | Optional pair | GitHub OAuth application Client ID. |
| `UNIAUTH_GITHUB_CLIENT_SECRET` | Optional pair | GitHub OAuth application Client Secret. |

At least one provider pair must be configured. Each pair is either complete or
omitted. Partial pairs or surrounding credential whitespace fail startup. Omitted providers appear
unavailable in the Console and cannot authorize product requests. Upstream
Secrets are deployment credentials and are not stored in SQLite.

The public origin and product callbacks require HTTPS except for explicitly
enabled `localhost` or loopback IP development addresses. The server listener
uses HTTP; terminate TLS at a reverse proxy and configure the actual external
origin. The service does not infer its public origin from forwarded headers.

The [Docker Compose deployment reference](deployment.md) defines image publication,
environment deployment directories, and VM-owned `.env` configuration.

Console request-origin validation derives the browser-serialized origin from the
configured URL. Domain names use IDNA ASCII conversion and lowercase; valid IPv6
literals are serialized canonically. Ports are decimal without leading zeros;
HTTPS `443` and HTTP `80` are omitted. Thus `https://AUTH.example:00443/` accepts
browser requests with `Origin: https://auth.example`. Invalid configured domains,
IPv6 literals, scoped IPv6 addresses, and ports outside 0–65535 fail Console
initialization. This configuration normalization does not normalize incoming
headers: Console mutations still require exactly one canonical Origin matching
the expected string. Registered product callback matching remains exact. The
protected Console App
callback preserves the configured public URL spelling, with its final slash
removed before `/console/auth/callback` is added; the generated OAuth callback
uses the same string. Browser navigation and mutation Origin use the canonical
origin. This keeps accepted uppercase hosts and explicit default ports usable
without weakening exact callback matching.

The Console reserves an App during configuration editing, enable/disable updates,
deletion, and Secret creation, detail updates, reset, and revocation. A new Secret
keeps its one-time plaintext display and ownership until the operator marks the
value saved and closes the dialog. A failed non-authentication write retains
ownership through a server-state reload; only that App is reconciled into the
current UI snapshot.
If the reload fails, `State unavailable` blocks new configuration actions until
explicit Refresh succeeds. An existing draft or delete confirmation may Retry or
Cancel under its retained owner, and a successful retry may conservatively leave
the unavailable marker until Refresh. A session-invalidating `401` returns to
OAuth login and discards idle drafts. Operations already in flight keep
their owners until their handlers settle: successful App or Secret issuance
retains its plaintext display through Saved and Done, and a failed operation
releases its retained owner. An unrelated request losing Console authentication
does not prove that pending issuance failed. These controls do not coordinate
separate Console browsers or API clients.

Protect runtime credentials and the database using deployment access controls.
The database contains application definitions and SHA-256 digests of randomly
generated 256-bit product Secrets, their names and descriptions, and lifecycle
metadata. It also stores Console platform accounts, linked normalized identity
profiles, and one-time bootstrap status. It stores no external product users, raw
provider `origin`, upstream tokens, identity results, login transactions, or
Console sessions.

Reset atomically revokes one active credential and issues a new ID and key with
the same name and description. Its old key stops authenticating after commit,
so update the product with the new value immediately. If the one-time response
is lost, create another Secret; the reset value cannot be recovered or replayed.

## Platform login and administrator bootstrap

The Console supports OAuth only through all configured personal Microsoft,
Google, and GitHub providers. Password login and its configuration variables are
removed. In the existing deployment `.env`, specify the initial administrator
email and provider credential pairs:

```dotenv
UNIAUTH_INITIAL_ADMIN_EMAIL=sychenjianjun@gmail.com
```

The [deployment template](../../deploy/.env.example) leaves this value empty;
set it explicitly before starting the service. It must be a plain email address
of at most 254 bytes, without whitespace, display names, or angle brackets.
Startup also requires at least one complete provider pair.

Startup records the pending bootstrap email without creating a seed account.
The first Google or GitHub normalized identity matching that email with
`email_verified=true` atomically claims administrator authority for its actual
platform account, through sign-in or explicit linking. Email matching is
case-insensitive; it never merges accounts. Microsoft's unknown verification value
cannot claim that authority. A deployment configured only for Microsoft can
serve ordinary users, but needs a Google or GitHub provider with matching verified
email evidence to claim its administrator.

Bootstrap trusts the provider's assertion that the selected email was verified.
This is not a fresh mailbox challenge or a guarantee of continued mailbox
control. After the claim, administrator authority remains bound to the stable
platform account regardless of later profile-email or deployment-email changes.
Before the claim, restarting with a different configured email updates the pending
bootstrap target. Changing this value after the claim does not transfer the role
or grant another administrator. No password or credential fallback exists.

Stable provider-qualified IDs identify accounts. Signing in with a new identity
creates a new account, even if its email matches another account. To use multiple
login methods for one account, sign in to that account and explicitly link each
additional identity before signing out. Linking requires the initiating session
to stay live until completion and rejects an identity owned by another account.

After listener reservation, service initialization provisions the protected
Console App with the exact callback `<UNIAUTH_PUBLIC_URL>/console/auth/callback`
and all configured providers. Its backend-only random Secret rotates during that
initialization, including on restart; only its digest persists. Ordinary
management cannot list, edit, delete, or issue credentials for that App. The
Console retrieves its result in process through the gateway's authenticated
handover operation, sharing the external HTTP result's expiry and single-use rules.

Administrators can view minimal user summaries and created-App counts only.
They manage their own Apps under the same ownership restrictions as other users.
The [platform decision](../../.agents/notes/implemented/architecture/2026-10-13-public-oauth-platform.md)
owns bootstrap trust, account linking, and restricted administrator authority;
the [API reference](api.md) defines login, linking, and user-list responses.

## Product SDK configuration

The product SDKs run in confidential product backends with a UniAuth base origin,
App ID, and App Secret from the Console. They do not use upstream provider Client
IDs or Secrets and do not load service environment configuration automatically.
Products choose how to inject these three values into their client constructors.

Both clients require HTTPS unless loopback HTTP is explicitly enabled. Go uses
`AllowLoopbackHTTP`; JavaScript uses `allowLoopbackHTTP`. The default request
deadline is 30 seconds. A Go caller-provided HTTP client controls its timeout;
JavaScript uses `timeoutMs` and composes the per-request caller AbortSignal with
its deadline. The SDK performs no automatic result retry or redirect following.
A failed response may mean the server already consumed the code, so the product
starts a new login rather than treating a repeated request as recovery.

[The SDK reference](sdks.md) describes constructor fields, callback validation,
the mandatory normalized identity fields, nullable values, complete `origin` and
raw JSON fidelity, and local Go/packed JavaScript use. Packages are delivered
as source and locally validated artifacts; no registry publication or Go release
tag is established by this task.

## Provider registration

Register these exact callbacks at each upstream provider, replacing the origin
with `UNIAUTH_PUBLIC_URL`:

| Provider | Callback | Requested scopes and identity sources |
| --- | --- | --- |
| Microsoft | `https://auth.example/auth/callback/microsoft` | `openid profile email https://graph.microsoft.com/User.Read`; validated ID claims, Graph `/me`, and photo bytes. |
| Google | `https://auth.example/auth/callback/google` | `openid profile email`; validated ID claims and UserInfo. |
| GitHub | `https://auth.example/auth/callback/github` | `read:user user:email`; authenticated user profile and all email pages. |

Microsoft registration must support personal Microsoft accounts only
(`PersonalMicrosoftAccount`). Authorization and token exchange use `consumers`
endpoints. Every Microsoft authorization request includes `prompt=select_account`
to request the account chooser, including remembered accounts and an option to
use another account. This fixed provider policy covers product login, Console
login, and identity linking through the shared gateway. It has no deployment,
App, or SDK setting. Signature and issuer validation use the fixed
personal-account tenant
`9188040d-6c67-4c5b-b112-36a304b66dad`; organizational accounts are rejected.
The validated token must contain that `tid` and a nonempty `oid`. Register the
delegated Graph `User.Read` permission. Graph photo `404` is represented as
`null`; other retrieval failures fail authentication.

Google performs OIDC discovery during startup and must have a reachable discovery
endpoint. Microsoft and Google fetch signing keys when verification needs them.
Products do not register their callback URLs with the upstream providers;
product callbacks are registered in the UniAuth Console.

Provider scopes define which identity information can be obtained. Adapters
return standard identity fields alongside complete raw source objects in
`origin`; tokens and authorization codes remain internal to the authentication
flow. Missing source attributes stay missing in `origin`, while unavailable
standard strings and unknown verification values are `null`. Microsoft photo
bytes also supply the standard avatar data URL without an additional request.
The [API reference](api.md) owns the response fields, selection rules, and errors.

## State, limits, and failure behavior

SQLite uses embedded Goose migrations, one database connection, WAL journaling,
full synchronous writes, and a five-second busy timeout. Application and Secret
configuration, platform accounts, and linked identities survive restarts. Embedded
migration 3 intentionally deletes every pre-platform App and Secret and creates
the final owner-scoped platform schema. Recreate product registrations and replace
product backend credentials after the upgrade. No legacy rows are assigned to an
administrator, and no old-data compatibility branch is retained. Later startups
preserve newly created user accounts and Apps while rotating the private Console
credential.

Back up the live SQLite database with a SQLite-aware method or stop the process
before copying its files. A backup preserves the old registration data for
reference; the new schema does not reuse those credentials. The
[platform decision](../../.agents/notes/implemented/architecture/2026-10-13-public-oauth-platform.md)
owns this deliberate reset. The [Secret metadata decision](../../.agents/notes/implemented/feature/2026-10-13-secret-metadata.md)
retains metadata rules and the prior migration's rationale.

The following limits are fixed by service composition:

| Resource | Limit |
| --- | --- |
| Provider login transaction lifetime | 10 minutes. |
| Completed identity result lifetime | Exactly 5 minutes from result creation. |
| Expired transaction/result cleanup | Every minute; claims also check expiry directly. |
| Transactions, including in-progress claimed callbacks | 10,000 and 32 MiB of accounted state. |
| Pending results | 10,000 and 64 MiB of accounted state. |
| Provider HTTP and exchange deadline | 20 seconds. |
| Callback processing deadline | 30 seconds. |
| Each upstream response | 8 MiB; combined GitHub email response pages also limited to 8 MiB. |
| Console session | 8 hours; at most 1,000 live sessions. |
| Pending Console login or link | 10 minutes; at most 1,000 pending flows. |
| HTTP request body | 1 MiB globally, with smaller endpoint-specific limits. |
| HTTP header size | 16 KiB. |
| HTTP read-header / read / write / idle timeout | 5 / 15 / 45 / 60 seconds. |
| Graceful shutdown deadline | 15 seconds. |

Accounted cache bytes include retained token strings and payloads, not all Go
allocator overhead. Count and byte limits reject new work explicitly rather than
evicting accepted live entries. Standard fields and complete `origin` sources
both count toward result size, including the Microsoft avatar data URL and its
retained source photo. No truncation or partial identity package is returned
when upstream or cache size limits are exceeded.

Deploy a single UniAuth instance. Authorization, provider callbacks, and result
retrieval must reach the process holding the transient state. Multiple replicas
require an explicit state or routing design. A restart loses outstanding login
transactions, pending results, and Console sessions. Products start a fresh
login; platform accounts, links, and owned App configuration remain.

On SIGINT or SIGTERM, the HTTP server stops accepting requests and drains
accepted requests within the shutdown deadline before releasing transient state
and closing SQLite. Provider/network failures propagate through the flow as
explicit failures. No retry store recovers an already consumed result.

## Logs and readiness

The service writes structured JSON logs to stderr. HTTP logs contain sanitized
operation names, request IDs, status, method, and duration. Authentication logs
also carry available login operation ID, App ID, provider, phase, and safe error
categories. A successful result claim inherits its operation ID from the live
result; later unavailable claims cannot recover that correlation after deletion.
Cleanup logs report aggregate expired-entry counts.

Authorized management mutations emit `console management operation` events with
`operation_id`, `request_id`, `action`, `actor_account_id`, `affected_app_id`,
`phase=completed`,
`outcome`, `error_category`, and HTTP `status`. Supported actions are `app.create`,
`app.update`, `app.delete`, `secret.create`, `secret.update`, `secret.reset`, and
`secret.revoke`. The response header `X-Operation-ID` correlates the completed
operation with its request; successful creation logs the generated App ID.
Failed mutations retain safe status and error-category information without
Secrets, App display values, or
Secret names and descriptions.

Management audit includes the opaque platform account ID for actor correlation.
Identity payloads, upstream identity IDs, email addresses, Secrets, tokens, result
codes, states, and full URLs with sensitive query parameters are excluded from
application logs. Configure reverse-proxy logs to preserve that policy as well.
Health and readiness routes are described in the [API reference](api.md).

## Build and validation

Run `make setup` once, then `make build`, `make check`, and `make test`. The build
embeds production Console assets in `.tmp/bin/uniauth`; Node.js and pnpm are
required for building, not serving that executable. Dependency versions are
pinned in [go.mod](../../go.mod), [root package.json](../../package.json), and
[Console package.json](../../web/console/package.json). Build caches, dependency
stores, test artifacts, and temporary browser data belong under `.tmp/`.

`make coverage` invokes go-cov `v0.1.0` using [.go-cov.toml](../../.go-cov.toml)
separately for the service module and standalone Go SDK module, with `GOWORK=off`.
The profiles are `.tmp/coverage.out` and `.tmp/sdk-go-coverage.out`.
It enforces 90% total, 85% package, and 80% function coverage, requires critical
blocks covered, and enables race detection with a three-minute test timeout.
No packages, files, or functions are excluded; inherited policy override
variables are cleared by the coverage script. `make test` also runs Console and
JavaScript SDK tests, a packed JavaScript consumer and declaration check, and a
standalone external Go consumer. The isolated `e2e/sdk/` module then runs both
clients against the Go authentication handler and SQLite configuration with a
fixture provider, race detection, and a two-minute timeout. `make sdks` builds the Go module and JavaScript
output independently; `make build` includes those builds. `make check` validates
active documents and skill metadata, formatting, Go vet for service, SDK,
integration, and browser-fixture modules, frontend/SDK types, and frontend/SDK
formatting.

`make test-release` runs Python standard-library tests for UTC release dates,
published-tag sequences, registry pagination, and failure handling. `make test`
includes this gate; these fixtures do not publish an image or deploy a service.

`make browser` runs Playwright against production server composition with
controlled OAuth providers from the standalone `e2e/browser/` fixture module,
fresh SQLite data, and the interactive mockup. Use an existing Chromium executable
with
`PLAYWRIGHT_CHROMIUM_EXECUTABLE`, or place a temporary Playwright browser install
under `.tmp/`. The browser harness uses local loopback HTTP, fixture OAuth providers,
isolated runtime data, one worker, and no automatic retries. It exercises the
Console through gateway authorization, provider and Console callbacks, one-time
handover, and session creation, as well as account linking and owner isolation.
Injected adapters do not prove interactive Microsoft, Google, or GitHub login.
