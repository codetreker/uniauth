# Agent Note: Normalized Identity and Original Provider Data

Status: implemented

## Problem

Products need stable identity keys and common profile attributes after external
account authentication. Provider payloads use different field names, profile
sources, absence rules, and email-selection semantics. Repeating those mappings
in every product creates inconsistent display and account behavior. Products
also need complete obtained source data for policies and provider-specific
features that a common profile cannot represent.

## Decision

Each provider adapter returns an identity with seven always-present fields:
`provider`, `id`, `email`, `email_verified`, `name`, `avatar_url`, and `origin`.
The `id` value is the existing provider-qualified identifier, unchanged by the
response format. Missing, null, and empty standard strings become `null`.
Verification remains nullable: unknown, `false`, and `true` are distinct values.
Known fields used for normalization reject incompatible non-null JSON types,
including lower-priority fields; malformed data never becomes fabricated
profile data.

The standard fields are projections of the obtained provider sources. `origin`
retains those complete sources, including unknown fields, explicit nulls, false
values, nested objects, all email pages, and Microsoft photo bytes. The projection
does not mutate the sources or include authentication credentials.

Microsoft uses nonempty Graph `mail`, then ID-token `email`, for email; Graph
`displayName`, then ID-token `name`, for name. Neither `preferred_username` nor
`userPrincipalName` is substituted for email. Verification remains unknown.
The fetched photo supplies a data URL containing its parsed image media type
and Base64 bytes; photo `404` produces `null`. Google uses UserInfo `email`,
`email_verified`, `name`, and `picture`, with UserInfo as the canonical profile
source. Its verification flag is preserved independently of email presence.
GitHub selects a nonempty primary email from its complete email list, then
public profile `email` if no usable primary exists. Verification requires an
exact matching list entry for the selected email. Its name and avatar URL come
from the profile. No arbitrary non-primary email or username fills an absent
standard field.

The adapters already own upstream validation and source retrieval, so they also
own the provider-specific projection. The handover service receives the complete
identity and continues to own App binding, serialization, capacity, five-minute
expiry, and atomic single consumption. No second provider interface or profile
store is needed. The [API reference](../../../../docs/reference/api.md) owns the
exact consumer contract; the [blueprint](../../../../docs/blueprint/README.md)
owns component responsibilities.

This decision partially supersedes the response shape in the
[gateway ownership and handover decision](2026-10-12-authentication-gateway.md).
The prior `subject` field becomes `id`, and `sources` becomes `origin`, with no
compatibility aliases. Qualified identity values and raw-source meaning remain
unchanged. Gateway ownership, authentication, and transient-result semantics
remain owned by that decision.

## Alternatives

**Provider-specific results mapped by every product.** The original envelope
provided complete sources and a qualified subject, preserving provider data and
leaving product policies independent. It also required each integration to
understand source names and choose profile attributes. Standard projections
centralize that shared work while `origin` retains the information each product
needs to apply its own policies.

## Consequences

Products use one shape for profile display and identity keys, then inspect
`origin` when a provider-specific policy needs additional evidence. Normalized
email and name are attributes; only `id` is the stable identity key. An upstream
verification flag does not establish a universal product policy or automatic
cross-provider linking. Those decisions remain with the product.

Consumers must use the seven-field contract and handle nullable attributes. The
wire-field rename requires coordinated consumer updates, even though identity
keys retain their values. There is no persisted end-user data to migrate; future
changes to qualified ID values require an explicit product identity migration.

Microsoft photo data appears in both the standard data URL and `origin.photo`.
This gives products a directly usable avatar without changing the source photo
or fetching it again, and increases the serialized result size. Both copies
count toward the existing result memory limit. Capacity failures remain explicit;
results are neither truncated nor stripped of source fields.

Automated fixtures can verify mapping, absence, malformed types, complete source
preservation, and handover behavior. They cannot establish actual upstream
registration, consent, or credentials; fresh interactive provider verification
belongs to deployment validation.

## Provider references

- [Microsoft Graph user properties](https://learn.microsoft.com/en-us/graph/api/resources/user?view=graph-rest-1.0)
- [Microsoft ID-token claims](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)
- [Microsoft Graph profile photo](https://learn.microsoft.com/en-us/graph/api/profilephoto-get?view=graph-rest-1.0)
- [Google OpenID Connect and UserInfo](https://developers.google.com/identity/openid-connect/openid-connect)
- [GitHub authenticated user profile](https://docs.github.com/en/rest/users/users#get-the-authenticated-user)
- [GitHub authenticated email list](https://docs.github.com/en/rest/users/emails#list-email-addresses-for-the-authenticated-user)
