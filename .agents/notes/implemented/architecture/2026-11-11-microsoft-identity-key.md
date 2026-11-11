# Agent Note: Microsoft Identity Key and Account Preservation

Status: implemented

## Problem

Microsoft identity keys must distinguish an object within its validated tenant
and remain suitable for external products and Console account lookup. Changing
their public representation changes the exact strings already stored as Console
link keys and in product databases. Treating a renamed key as an unseen identity
would create another account and separate a person from existing ownership.

## Decision

The Microsoft adapter emits `msft:<oid>@<tid>`. It continues to report
`provider: "microsoft"` and requires the validated personal-account tenant
`9188040d-6c67-4c5b-b112-36a304b66dad`, its issuer, and a nonempty `oid`. The prefix
change and object-before-tenant ordering alter identity-key representation only;
they do not broaden supported Microsoft accounts or change provider selection,
profile normalization, or original source data.

Embedded SQLite migration 4 replaces the historical
`microsoft:9188040d-6c67-4c5b-b112-36a304b66dad:<oid>` keys with their new values
for Microsoft Console links. Only `account_identities.provider_id` changes.
Account IDs, nullable linked profiles, link timestamps, administrator and bootstrap
authority, App ownership, and credentials retain their values. The existing
`(provider, provider_id)` primary key rejects any target-key collision, including
two representations already linked to the same account. Goose runs the update
transactionally; a collision aborts startup and preserves all stored rows.
The reverse migration restores the historical key form with the same collision
behavior.

UniAuth emits the new form without an application-code alias lookup. External
products own their persisted identity mappings and must coordinate a migration
or explicitly normalize both wire forms before consuming the new output. The
gateway cannot migrate product databases. Its SDKs continue to transport identity
IDs as opaque strings.

This decision partially supersedes the key-value preservation in the
[normalized identity decision](2026-10-13-normalized-identity.md). That decision
still owns the seven-field identity representation, profile mapping, and complete
source data. The [public platform decision](2026-10-13-public-oauth-platform.md)
continues to own account linking and authority. The
[API reference](../../../../docs/reference/api.md#stable-identity-and-original-sources)
owns the emitted formats; the
[configuration reference](../../../../docs/reference/configuration.md#state-limits-and-failure-behavior)
owns startup and migration behavior.

## Alternatives

**Reformat only in Daynight.** This would leave the gateway's public format and
its Console links unchanged, while giving one product a separate representation.
The public format belongs to the gateway. Product-specific normalization can
support a transition but does not establish uniform gateway output.

**Change emitted values without migrating Console links.** Account lookup uses
the entire provider-qualified ID. A new string would miss the existing link and
create a separate account, making existing Apps and administrator authority
unavailable through that Microsoft login.

**Merge colliding links or accounts.** A collision may involve distinct owners;
even matching account IDs do not authorize deleting a link or its metadata.
The migration exposes the conflict and leaves explicit resolution to the
operator.

## Consequences

Microsoft identity keys keep both validated identity components in
`msft:<oid>@<tid>` form. Existing Console users retain their accounts, links, Apps,
credentials, and authority after migration. Provider routing remains `microsoft`;
Google and GitHub keys retain their formats.

This is a public stable-key change, so external product deployments need their
own coordinated data migration or explicitly supported normalization. A Console
key collision prevents service startup until the stored conflict is resolved.
Migration and provider tests cover preserved ownership, exact emitted identity,
transaction rollback, and unchanged tenant rejection; automated fixtures do not
establish live upstream consent or deployment credentials.
