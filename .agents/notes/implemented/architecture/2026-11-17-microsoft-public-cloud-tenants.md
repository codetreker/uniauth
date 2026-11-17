# Agent Note: Microsoft Public-Cloud Tenant Verification

Status: implemented

## Problem

A public authentication gateway must accept eligible personal and organizational
Microsoft identities without a tenant catalog, while preventing an untrusted
issuer claim or a signing key restricted to one tenant from authenticating a
different tenant. Existing personal identity keys and Console ownership must
remain stable, and the product handover protocol must continue to work.

## Decision

The shared Microsoft adapter uses fixed public-cloud `common` authorization and
token endpoints. It validates a GUID `tid`, the exact
`https://login.microsoftonline.com/<tid>/v2.0` issuer, and an RS256 signature
from a key whose issuer is either the exact public-cloud tenant template or the
matching concrete issuer. The issuer metadata remains paired with the key; a
matching `kid` alone is insufficient. Signing keys come only from the fixed
common JWKS endpoint, never a token-derived URL. All existing audience, time,
nonce, subject, issued-at, authorized-party, and access-token-hash checks remain
mandatory before identity publication or Graph enrichment.

A lazy, immutable snapshot is shared across tenants. At most one synchronous
refresh is in flight per adapter. A newer published generation avoids a redundant
fetch; after refresh each caller verifies once. Failed fetches preserve the old
snapshot and their original causes. Waiters can cancel independently; a live
waiter may take over only when the leader's own context was cancelled. A client
timeout with a live leader context is shared as an ordinary failure. There is no
per-tenant cache, timer, or detached refresh worker. A call-local verifier bridge
preserves key-set failure causes through the OIDC library without shared request
error state.

Verified `oid` and `tid` form `msft:<oid>@<tid>` with provider `microsoft`.
The personal tenant and its identifiers remain unchanged; distinct tenants
remain distinct even when object IDs or email match. The existing handover,
seven-field identity, optional Graph policy, and explicit account linking remain
in force. The deployment registration must support both organizational and
personal accounts. Consent and organizational access policy remain upstream;
national clouds and tenant-directed login are outside this decision.

This extends the account scope in the [identity-key decision](2026-11-11-microsoft-identity-key.md)
without changing its key format or migration, and retains the account ownership
rules in the [public platform decision](2026-10-13-public-oauth-platform.md).
The [optional-profile decision](2026-11-13-microsoft-optional-profile.md) continues
to own enrichment behavior. The [blueprint](../../../../docs/blueprint/README.md)
owns the current architecture and the [configuration reference](../../../../docs/reference/configuration.md#provider-registration)
owns deployment prerequisites.

## Alternatives

**Plain remote key set with a later issuer check.** The OIDC library's ordinary
key decoding discards Microsoft's per-key issuer extension, so a valid signature
alone cannot establish that the signing key is permitted for the token's tenant.

**Per-tenant discovery or separate exact-tenant verifiers.** Tenant-derived
network destinations and per-tenant cache lifecycle add operational state and
still require issuer-aware key handling. They do not improve the agreed public
cloud flow.

**Fetch issuer metadata separately from the signing keys.** Independent fetches
can associate issuer restrictions with a different key snapshot. Decoding the
key and its issuer together preserves their relationship.

## Consequences

Personal and organizational identities use the same provider and protocol;
existing personal IDs retain their exact bytes. Product consumers remain
responsible for their own users, linking, and sessions, and email does not merge
accounts. Existing SDKs implementing the current documented contract need no new
API or tenant configuration. Controlled tests do not establish deployment
registration, organizational consent, or live Microsoft acceptance.

Cache misses and rotations depend on the fixed JWKS endpoint; a failed refresh
cannot authenticate the triggering token, even though the previous snapshot is
retained. A bounded shared refresh avoids multiplying requests across tenants,
but concurrent callers share an ordinary fetch failure. Optional profile data
and Microsoft email verification retain their established semantics, including no
Microsoft-only initial administrator claim.

## References

- [Microsoft multitenant token validation](https://learn.microsoft.com/en-us/entra/identity-platform/access-tokens#validate-the-issuer)
- [Microsoft common OIDC discovery](https://login.microsoftonline.com/common/v2.0/.well-known/openid-configuration)
- [Microsoft common signing keys](https://login.microsoftonline.com/common/discovery/v2.0/keys)
- [Microsoft ID-token claims](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)
