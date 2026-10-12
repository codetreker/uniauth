# Console Origin and Mutation Correction Receipt

Date: 2026-10-12
Reviewed revision: `0d74ce41c3bb007dcfcec9cb312e2d9d76c1a48e`

## Scope and behavior

This correction handles two Console defects in the reviewed implementation.
A configured uppercase hostname or explicit default port could mismatch the
browser's serialized Origin and reject legitimate administrator requests with
`403`. An enable/disable mutation could commit while its response was delayed,
allowing an editor to submit the prior App snapshot and reverse that change.

The configured expected Console origin now serializes lowercase IDNA domain
names, valid IPv6 literals, and decimal ports with defaults omitted. Incoming
Origin headers remain exactly compared and CSRF checks remain required. Invalid
configured host or port forms fail initialization.

Configuration editing, enabled-state changes, and deletion share synchronous
per-App operation ownership. Editor drafts and delete confirmations reserve the
App; conflicting actions cannot overlap while asynchronous work is pending.
Successful responses update the authoritative snapshot before another action can
start, and pending updates show `Updating…`.

After a non-authentication configuration failure, ownership remains held through
a server-state reload and only the affected App is reconciled into the current
snapshot. If the reload also fails, `State unavailable` blocks new configuration
actions until explicit Refresh succeeds. Existing drafts and delete confirmations
retain their owners and permit Retry or Cancel. A successful owned retry may
leave the unavailable marker until Refresh. Administrator session expiry returns
to login. These controls protect one UI instance without adding server-side or
cross-browser version coordination.

The [API reference](../reference/api.md),
[configuration reference](../reference/configuration.md), and
[blueprint](../blueprint/README.md) describe current behavior. The existing
[architecture note](../../.agents/notes/implemented/architecture/2026-10-12-authentication-gateway.md)
and [testing note](../../.agents/notes/implemented/testing/2026-10-12-authentication-validation.md)
retain the rationale and validation policy. The
[initial implementation receipt](2026-10-12-authentication-gateway.md) preserves
its original measured results.

## Observed validation

The complete `make build check test` run passed for the corrected implementation.

| Check | Observed result |
| --- | --- |
| Production build | Console bundle and Go executable passed. |
| Static and documentation checks | Go formatting and vet, TypeScript, Prettier, active document links, note lifecycle, and discovery aliases passed. |
| Go tests and coverage | 276 cases, including 75 top-level tests, passed with race detection; total coverage was 96.7%, and Console coverage was 98.7%. |
| Coverage policy | Total 90%, package 85%, function 80%, and critical-block requirements passed without exclusions. |
| Frontend tests | All 16 tests in three files passed. |
| Browser scenarios | All three passed in 14.9 seconds using a fresh production executable, the Go service, and desktop/mobile mockups. |
| Independent review | Backend and UI reviewers found no new critical or major issues; seven UI adversarial probes passed. |
| Cleanup | Browser, Go service, Vite, temporary database directories, and review probes were cleaned up. |

The browser harness configured the public origin with port `:018080` while the
browser sent `:18080`; login and management mutations succeeded with strict
Origin validation intact. Its delayed-disable scenario let the real Go `PUT`
commit `enabled=false` to SQLite before holding the reply. The UI displayed
`Updating…`, blocked Edit, and sent no second configuration request. Releasing
the response produced an editor with the enabled checkbox unchecked; renaming
then persisted the disabled state. Existing CRUD, Secret, and administrator
session scenarios also passed.

The seven independent UI probes covered simultaneous events, duplicate submits,
delete failure and navigation, lost-write reconciliation, unavailable-state
blocking, and delayed reconciliation that preserves a newly accepted mutation
on another App. Retained draft and delete-confirmation retries keep the same
owner and remain explicit full operations. The conservative unavailable marker
remaining after a successful owned retry was classified as a minor follow-up;
it does not permit overlapping configuration actions and can be cleared by
explicit Refresh.

## Verification limits

Incoming headers are not normalized, and strict Origin/CSRF rejection remains
required. The configured-origin serializer is not a promise to support all
legacy numeric-IP spellings accepted by browser URL parsers. Product callback
registration still uses exact string matching.

No interactive third-party provider login is established by this Console
correction. The existing single-process, memory-retention, and one-time identity
handover contracts remain unchanged. These local results do not claim CI success
for a revision that has not yet completed its remote checks.
