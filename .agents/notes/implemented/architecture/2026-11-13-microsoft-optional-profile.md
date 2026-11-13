# Agent Note: Microsoft Authentication with Optional Profile Enrichment

Status: implemented

## Problem

Microsoft profile and photo endpoints can become unavailable after code exchange
and identity verification have succeeded. Requiring those personal attributes
then blocks a complete identity proof behind a separate service's availability.
The gateway needs bounded completion, trustworthy identity keys, and observable
source failures when personal information is incomplete.

## Decision

Microsoft authentication requires authorization-code exchange and complete
ID-token verification: signature, personal issuer and tenant, audience, time,
nonce, authorized party, and access-token hash when present. A nonempty `oid`
and the fixed personal `tid` produce the unchanged `msft:<oid>@<tid>` key.
Token email/name retain strict nullable-string validation, including when Graph
would supply a higher-priority value. The existing 20-second core budget and
mandatory failure behavior remain in force.

After core authentication, Graph `/me` and photo retrieval share a separate
fixed 3-second enrichment context derived from the original caller context.
Profile runs first; photo runs while budget remains, including after a profile
failure. Network, HTTP, JSON/schema, and image-validation errors make only that
optional source unavailable. An incompatible known profile field rejects its
whole source. An accepted source survives the other source's failure. Original
caller cancellation, service shutdown, and the gateway's 30-second callback
deadline still terminate the flow; cancellation is checked before publication.
No background enrichment, retry, or new configuration setting is introduced.

Every successful identity retains all seven top-level fields and complete
validated `origin.id_token_claims`. Accepted Graph mail/displayName keep priority
over token email/name. Failed or unattempted `profile` and `photo` keys are
omitted. Only a confirmed photo `404` produces `photo: null`; accepted photos
retain their bytes and media type and supply the standard avatar data URL.
Unavailable standard attributes remain `null`. Usernames and historical profile
data never fill missing attributes, and accepted source JSON is not rewritten.

Real stage failures retain correlated, sanitized diagnostics. A successful
identity with unavailable enrichment emits one `WARN` record:
`Microsoft profile enrichment incomplete`, `component=provider`,
`stage=profile_enrichment`, `phase=completed`, `outcome=partial`, and
`unavailable_sources` in fixed `profile`, `photo` order. It retains `elapsed_ms`
and existing request, operation, App, and provider correlation. A confirmed photo
`404` alone emits no partial warning. Caller cancellation and shutdown remain
fatal and emit no partial summary. Tokens, identity values, bodies, and raw
upstream errors remain excluded from logs.

This decision partially supersedes the blanket upstream-retrieval failure policy
in the [gateway decision](2026-10-12-authentication-gateway.md), the Microsoft
source-availability and Graph schema-failure policy in the
[normalization decision](2026-10-13-normalized-identity.md), and the Microsoft
total-budget behavior retained by the
[diagnostics decision](../feature/2026-11-13-authentication-diagnostics.md).
Those notes retain their original rationale and continue to own handover,
standard-field mapping, diagnostic correlation, and bounded file logging.
The [Microsoft identity-key decision](2026-11-11-microsoft-identity-key.md)
continues to own identifier syntax and persisted-key migration. Google and
GitHub authentication policy is unchanged.

The [blueprint](../../../../docs/blueprint/README.md) owns the architecture;
the [API reference](../../../../docs/reference/api.md#microsoft-optional-profile-enrichment)
owns source-presence and normalization behavior, and the
[configuration reference](../../../../docs/reference/configuration.md#logs-and-readiness)
owns budgets and diagnostic fields.

## Alternatives

**Require every Graph source before completing authentication.** The earlier
policy guaranteed the mandatory source objects on successful Microsoft results
and rejected retrieval or schema errors. It also made an otherwise verified
login fail when a personal-information endpoint was unavailable. Complete
identity proof now permits incomplete optional attributes, while missing source
keys and correlated warnings expose that availability cost.

**Use ID-token claims exclusively and skip Graph.** This removes Graph requests
and their delay, but gives up available profile attributes, complete Graph source
data, and photo bytes. Bounded optional enrichment still obtains and preserves
those sources when available.

## Consequences

Verified personal Microsoft identities can complete sign-in with only ID-token
claims and nullable personal attributes. Consumers that assumed Graph source
keys were always present must check their presence. Explicit `photo: null`
continues to distinguish confirmed absence from unavailable or unattempted
enrichment. SDK types and implementations need no change; both SDKs retain the
seven-field result and complete accepted raw data.

Enrichment can consume up to 3 seconds after successful core work, subject to
the original caller deadline. A slow profile request can exhaust that shared
budget and leave photo unattempted. This bounds outage delays without extending
the gateway callback deadline. Partial warnings and existing failure timings
retain evidence for diagnosing unavailable sources.

Console keeps its existing latest-identity snapshot policy. A degraded sign-in
can replace stored email/name with token values or `null`, and clear a previous
avatar. It preserves account ID, existing administrator authority, and App
ownership. Linking refreshes linked metadata without replacing the account
display profile. The [platform decision](2026-10-13-public-oauth-platform.md)
owns that persistence policy. No database schema or identity-key migration is
needed. External products own their attribute persistence and account policy.

Automated fixtures establish mandatory verification, source acceptance and
absence, cancellation, budgets, diagnostics, and consumer behavior. They do not
prove that a particular upstream incident caused a production timeout or that
Microsoft services have recovered.

## Provider references

- [Microsoft ID-token validation](https://learn.microsoft.com/en-us/entra/identity-platform/id-tokens#validate-tokens)
- [Microsoft ID-token claims](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)
- [OpenID Connect scopes and nullable email](https://learn.microsoft.com/en-us/entra/identity-platform/scopes-oidc#openid-connect-scopes)
- [Microsoft Graph profile](https://learn.microsoft.com/en-us/graph/api/user-get?view=graph-rest-1.0)
- [Microsoft Graph photo and confirmed absence](https://learn.microsoft.com/en-us/graph/api/profilephoto-get?view=graph-rest-1.0)
