# Public OAuth Platform Delivery Receipt

Date: 2026-10-14

## Delivered behavior

The Console is a confidential UniAuth App and accepts every configured personal
Microsoft, Google, and GitHub sign-in method. Password login and its deployment
configuration are removed. Public sign-in creates a durable platform account
identified by the provider's stable identity. Matching emails do not merge
accounts. An authenticated member can explicitly link another OAuth identity;
an identity already owned by another account cannot move.

`UNIAUTH_INITIAL_ADMIN_EMAIL` supplies a plain bootstrap email in the deployment
configuration. Exactly one matching Google or GitHub identity with verified
email claims administration. Microsoft unknown verification cannot claim it.
The selection trusts the provider's verification assertion at verification time;
subsequent authority follows the stable account, not later email or configuration
changes. Ordinary accounts can register before bootstrap is claimed.

Every application and credential operation is scoped to the authenticated
owner, including administrator requests. Administrators can list account IDs,
names, emails, creation times, and application counts only. The protected Console
App and its private credentials are absent from member management and counts.
Startup reconciles its deployment-derived callback and providers and replaces
only its private credential. Console login uses the ordinary gateway flow and
the same authenticated, five-minute, single-consumption identity handover.

Migration 3 intentionally removes all existing Apps and Secrets and establishes
the platform schema. Later restarts preserve newly registered accounts, linked
identities, Apps, credentials, and bootstrap authority. Provider origins, tokens,
external-product users, and external-product sessions are not persisted by the
platform. The [platform decision](../../.agents/notes/implemented/architecture/2026-10-13-public-oauth-platform.md)
owns the rationale; the [API](../reference/api.md) and
[configuration](../reference/configuration.md) references own observable contracts.

## Observed validation

`make build check test` passed with the existing coverage policy and no exclusions.

| Check | Observed result |
| --- | --- |
| Service Go tests | 536 reported cases, including 116 top-level tests, passed with race detection; total coverage 96.6%. Apps 93.6%, Auth 99.5%, Console 97.8%, server 96.5%, CLI 96.8%, providers 95.7%, embedded Console 97.0%. |
| Go SDK | 106 reported cases passed with race detection; coverage 99.3%. |
| Coverage policy | Total 90%, package 85%, function 80%, and critical-block requirements passed unchanged for both Go modules. |
| Console and SDK tests | All 60 Console tests and 16 JavaScript SDK tests passed. |
| Release and package consumers | All 17 existing release tests, the packed JavaScript consumer, and the external Go consumer passed. |
| SDK/gateway integration | Both race-enabled integration tests passed in 1.424 seconds. |
| Browser suite | All seven scenarios passed in 45.4 seconds, covering the platform flows and existing Secret status tabs. Both desktop and mobile previews passed again in 16.4 seconds after the final mobile history assertions. |
| Independent reviews | Storage and server, Console and authentication, and frontend reviews found no remaining confirmed in-scope Critical/Major issue. Fourteen focused caller probes passed across those reviews. |
| Final assets | Final responsive CSS was rebuilt into the executable; TypeScript, formatting, and diff checks passed. Desktop and mobile login, account, and user-summary screenshots were inspected. |

Storage tests exercise concurrent signup, exactly one bootstrap winner, explicit
binding ownership, rollback, cancellation, closed storage, minimal keyset pages,
foreign and system-App denial, intentional upgrade reset, restart durability,
and private credential replacement. Console tests exercise all configured
providers, browser/state binding, replay and expiry, session rotation, pending
link cancellation, tenant isolation, audit redaction, and capacity bounds.

Browser scenarios use the actual service composition with deterministic OAuth
providers and fresh local SQLite. They retain Secret metadata, reset, revocation,
and one-time acknowledgement checks while exercising public signup, manual
binding, same-email separation, Microsoft missing email, administrator privacy,
and production-component previews. Secret tab assertions retain keyboard
navigation, Active defaults, and reset and revocation transitions into Disabled
history. Final mobile user summaries keep names and emails readable with
application counts visible.

## Deferred follow-up and limits

Console callback logs currently collapse storage and cancellation failures into
`login_failed`. Sanitized failure-stage and cause classification remains a minor
diagnostic follow-up; raw identities, tokens, codes, and emails must remain absent.

These results establish local fixture behavior, not live upstream registrations,
consent, remote CI, release publication, or deployment. The existing product
identity envelope and both SDK contracts remain unchanged. Task services, Vite,
browser processes, and temporary SQLite fixtures were cleaned up.
