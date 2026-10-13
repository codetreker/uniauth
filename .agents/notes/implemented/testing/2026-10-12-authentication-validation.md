# Agent Note: Authentication Validation Policy

Status: implemented

## Problem

An authentication gateway can appear correct in a successful login while failing
at identity validation, replay prevention, expiry, credential revocation, or
concurrent result consumption. Frontend rendering checks alone do not prove that
the Console can create and manage usable configuration against the Go service.
Validation must make these caller-visible properties observable and enforce the
same coverage requirements locally and in CI.

## Decision

[.go-cov.toml](../../../../.go-cov.toml) is the authoritative Go coverage policy:
90% total, 85% package, 80% function, uncovered critical blocks rejected, and race
detection enabled. There are no package, file, or function exclusions.
[The coverage script](../../../../scripts/coverage.sh) pins go-cov `v0.1.0`, clears
inherited policy override variables, uses repository-local Go caches, and writes
coverage artifacts under `.tmp/`. `make coverage` runs that gate with a
three-minute test timeout. Coverage runs separately for the service and standalone
Go SDK modules with the same policy and `GOWORK=off`, writing distinct profiles.
`make test` includes both gates, Console tests, JavaScript SDK tests, and consumers
of the external Go module and packed JavaScript package.

Go tests exercise HTTP and store behavior for provider validation, complete raw
identity fields, separate state and browser binding, expiry, failed and wrong-App
claims, concurrent consumption, capacity, credentials, sessions, and lifecycle
ordering. Credential metadata assertions cover validation limits and exact
multiline description preservation, SQL upgrades of active and revoked rows
without digest or lifecycle changes, and detail updates that do not reissue,
revoke, or expose credentials. Management API assertions cover required initial
metadata, active and revoked editing, record ownership, and `secret.update`
audit events without names or descriptions. Controlled upstream fixtures provide
token, signing-key, and profile responses, including invalid responses and faults. These tests preserve errors
and observe externally visible effects. Console origin regressions cover browser
serialization of configured uppercase and IDNA domains, IPv6 literals, and
default or zero-padded ports through login and CRUD requests. Foreign, null,
noncanonical, and multiple Origin values and invalid CSRF remain rejected.

Frontend configuration regressions hold mutation responses open after the
simulated backend commits. They verify that Edit, enable/disable, and deletion
cannot overlap for one App, repeated submissions start one request, and a later
editor uses the committed configuration. Secret creation, detail updates, and
revocation share that owner; the one-time credential dialog retains it until the
value is marked saved and the dialog is closed. Failure cases hold ownership
through server-state reconciliation and verify that only the affected App
changes, preserving other accepted mutations.
If reconciliation fails, new actions remain blocked under `State unavailable`;
already-owned drafts or confirmations may Retry or Cancel. Successful owned
retries may leave that marker until explicit Refresh. Assertions distinguish
server commit, response delivery, and verified current state rather than treating
a failed response as proof that no write occurred.

SDK assertions exercise callback ambiguity, state mismatches, exact callback
string preservation, HTTP Basic and JSON request construction, redirect rejection,
request deadlines and cancellation, typed errors, lost-response behavior, and
the mandatory seven-field normalized identity envelope and complete `origin`
JSON. Missing standard fields and incompatible types fail as protocol errors;
nullable profile values and explicit verification `false` survive decoding. Go
`Origin`/`RawJSON` and JavaScript `rawJSON` retain numeric representations that
ordinary parsed JavaScript numbers cannot. SDKs expose server values without
recomputing provider mappings or filling absent fields.
Consumers import the standalone Go module with `GOWORK=off` and install the
packed JavaScript tarball, then check bare-import runtime behavior and exported
TypeScript declarations. The temporary JavaScript consumer installs only the
local SDK tarball. Type checking uses the pinned compiler and Node ambient types
from frozen SDK setup, while SDK declarations and runtime imports resolve from
the consumer to the packed artifact. No source aliases or registry metadata are
needed, and repository dependency setup stays frozen. These checks validate
distributable content, not an unreleased registry version. The
[SDK decision](../feature/2026-10-13-product-sdks.md) owns the client package
contracts. The [Secret metadata decision](../feature/2026-10-13-secret-metadata.md)
owns credential metadata behavior and migration semantics.

The isolated `e2e/sdk/` module exercises Go and JavaScript clients against the
real authentication handler, SQLite App registry, browser-bound login transaction,
and fixture provider. Wrong Secrets and wrong-App claims leave results claimable;
replay is rejected, concurrent valid claims produce one success, and raw unknown
fields, nulls, false values, and unsafe JavaScript integers remain available. Go
integration assertions also reach the exact five-minute expiry boundary. The
module runs with race detection and a bounded two-minute deadline after the
JavaScript output has been built. It verifies client/gateway integration without
claiming live provider authentication.

Playwright uses one worker and no automatic retries. It starts a fresh local Go
service with isolated SQLite data and the production Console, and a separate
mockup using production components and styles. Scenarios cover administrator
login, App management, named one-time Secret display, exact multiline
descriptions, active and revoked detail editing, rotation and named revocation,
and responsive production-component mockup interaction. Existing browsers or repository-local temporary
browser installations prevent task tooling from leaving a permanent installation.

The [configuration reference](../../../../docs/reference/configuration.md) owns
validation commands and limits. The
[initial implementation receipt](../../../../docs/tasks/2026-10-12-authentication-gateway.md),
[Console correction receipt](../../../../docs/tasks/2026-10-12-console-review-fixes.md),
[original SDK receipt](../../../../docs/tasks/2026-10-13-product-sdks.md), and
[SDK/main integration receipt](../../../../docs/tasks/2026-10-13-sdk-main-integration.md)
retain their respective observed outcomes and distinguish fixture coverage from
live provider checks.

## Alternatives

No alternative validation policy was selected during this implementation.
Coverage thresholds supplement behavioral assertions; they do not replace
provider fault cases, race detection, or local browser verification.

## Consequences

Changes must satisfy the same checked-in gate, including error paths and critical
blocks. Repository-local caches keep tooling and generated artifacts out of
home-directory configuration folders. Bounded test and browser deadlines expose
hanging work, while failures require diagnosis rather than unchanged retries.

Automated provider fixtures cannot prove production registration, consent,
credentials, account eligibility, or upstream service availability. Real
personal Microsoft, Google, and GitHub authentication requires a deployment
with configured provider Apps and fresh interactive verification.
