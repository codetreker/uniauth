# Configuration and Operations

`uniauth serve` reads deployment environment variables and starts one Go process.
With no command, `serve` is selected. `uniauth help` prints usage.
`uniauth hash-password` reads a password from stdin and prints an Argon2id PHC
hash without requiring service configuration. Arguments containing passwords
are not supported.

## Environment

| Variable | Required or default | Meaning |
| --- | --- | --- |
| `UNIAUTH_PUBLIC_URL` | Required | External absolute origin, such as `https://auth.example`; no path other than `/`, query, fragment, or user information. |
| `UNIAUTH_ADMIN_PASSWORD_HASH` | Required | Canonical Argon2id PHC hash for the administrator password. |
| `UNIAUTH_LISTEN_ADDR` | `:8080` | HTTP listener address in `host:port` form. |
| `UNIAUTH_DATABASE_PATH` | `uniauth.db` | SQLite database path; parent directory must exist. |
| `UNIAUTH_ALLOW_LOOPBACK_HTTP` | `false` | Set exactly `true` to allow HTTP for loopback public origins and registered product callbacks. |
| `UNIAUTH_MICROSOFT_CLIENT_ID` | Optional pair | Personal-account Microsoft application Client ID. |
| `UNIAUTH_MICROSOFT_CLIENT_SECRET` | Optional pair | Microsoft application Client Secret. |
| `UNIAUTH_GOOGLE_CLIENT_ID` | Optional pair | Google OAuth client ID. |
| `UNIAUTH_GOOGLE_CLIENT_SECRET` | Optional pair | Google OAuth client Secret. |
| `UNIAUTH_GITHUB_CLIENT_ID` | Optional pair | GitHub OAuth application Client ID. |
| `UNIAUTH_GITHUB_CLIENT_SECRET` | Optional pair | GitHub OAuth application Client Secret. |

Each provider pair is either completely configured or omitted. Partial pairs or
surrounding credential whitespace fail startup. Omitted providers appear
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
the expected string. Registered product callback matching remains exact.

The Console reserves an App during configuration editing, enable/disable updates,
and deletion. A failed non-authentication write retains ownership through a
server-state reload; only that App is reconciled into the current UI snapshot.
If the reload fails, `State unavailable` blocks new configuration actions until
explicit Refresh succeeds. An existing draft or delete confirmation may Retry or
Cancel under its retained owner, and a successful retry may conservatively leave
the unavailable marker until Refresh. Session expiry returns to administrator
login. These controls do not coordinate separate Console browsers or API clients.

Protect runtime credentials and the database using deployment access controls.
The database contains application definitions and SHA-256 digests of randomly
generated 256-bit product Secrets. It contains no end-user records, provider
tokens, identity results, login transactions, or administrator sessions.

The password-hashing command uses Argon2id with 64 MiB memory, three iterations,
two lanes, a 16-byte salt, and a 32-byte key. Startup accepts canonical Argon2id
hashes with bounded parameters: memory 19,456–131,072 KiB, iterations 1–10,
parallelism 1–16, salt 16–64 bytes, and key 32–64 bytes. Malformed or unsupported
hashes fail startup. Changing the deployment password requires a restart, which
also invalidates administrator sessions and all pending product logins.

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
endpoints. Signature and issuer validation use the fixed personal-account tenant
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
configuration survives restarts. Back up the live SQLite database with a
SQLite-aware backup method or stop the process before copying its files.

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
| Administrator session | 8 hours; at most 1,000 live sessions. |
| Administrator login rate | Burst of 5 attempts, then 1 per second globally. |
| Concurrent administrator password verifications | 2. |
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
transactions, pending results, and administrator sessions. Products start a fresh
login; durable App configuration remains.

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
`operation_id`, `request_id`, `action`, `affected_app_id`, `phase=completed`,
`outcome`, `error_category`, and HTTP `status`. Supported actions are `app.create`,
`app.update`, `app.delete`, `secret.create`, and `secret.revoke`. The response
header `X-Operation-ID` correlates the completed operation with its request;
successful creation logs the generated App ID. Failed mutations retain safe
status and error-category information without Secrets or display values.

Identity payloads, account identifiers, email addresses, Secrets, tokens, result
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
active documents and skill metadata, formatting, Go vet for service, SDK, and
integration modules,
frontend/SDK types, and frontend/SDK formatting.

`make browser` runs Playwright against a fresh local service and the interactive
mockup. Use an existing Chromium executable with
`PLAYWRIGHT_CHROMIUM_EXECUTABLE`, or place a temporary Playwright browser install
under `.tmp/`. The browser harness uses local loopback HTTP, fixture credentials,
isolated runtime data, one worker, and no automatic retries. Browser scenarios
do not prove an interactive Microsoft, Google, or GitHub login.
