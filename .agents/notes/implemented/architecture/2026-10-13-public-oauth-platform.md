# Agent Note: Public OAuth Console and Owned Applications

Status: implemented

## Problem

A Console protected by one deployment password gives every operator the same
management authority and cannot support unrelated people registering their own
products. A public platform needs durable account ownership, several login
methods, and limited administrative visibility without exposing another person's
App configuration or credentials. Its own authentication should exercise the
same gateway that products integrate.

## Decision

The Console is an OAuth-only UniAuth App using every configured personal
Microsoft, Google, and GitHub provider. Anyone can sign in and create Apps owned
by their platform account. Startup requires at least one configured provider.
The `serve` command validates configuration and reserves the configured TCP
listener before service initialization opens or migrates SQLite, updates pending
bootstrap configuration, or provisions the protected system Console App. Its
callback and allowed providers come from deployment configuration. Its random
backend-only Secret rotates during that initialization, including on restart;
SQLite stores only its verification digest. Management APIs cannot list, change,
delete, or issue credentials for that App.

Listener reservation precedes durable initialization because replacing the
private credential changes authority used by the running Console. A process
rejected at an occupied address must not rotate that credential or otherwise
change the database. Initialization errors close the reserved listener while
preserving their original cause; the initialized service owns the listener
through serving and shutdown. This ordering leaves the existing credential
lifecycle intact and makes no transactional guarantee for later initialization
failures.

The Console starts ordinary gateway authorization and, at callback, atomically
consumes its own browser-bound pending state before retrieving the result through
the gateway's shared in-process authenticated handover. The same live App
credential validation, five-minute expiry, and atomic single consumption apply
to that operation and `/auth/result`. An internal network request is unnecessary.
The external product HTTP protocol and SDK contracts remain unchanged.

One Console-owned provider and synchronous operation owner represent OAuth
progress for both sign-in and linking. The selected button shows `Connecting…`
while the start request and external navigation are pending. Unfinished App work
retains its separate Secret-saving warning; starting OAuth does not insert that
warning or change sign-in card geometry. Initiation failures and completed
in-page previews release the owner. External navigation retains ownership after
`location.assign` returns because the browser has not necessarily left the page.
A persisted `pageshow` releases that navigation owner when browser history
restores the document. Linking reloads identities only after a completed in-page
preview, preserving the displayed list throughout external navigation.

SQLite persists platform accounts and linked normalized profiles for Console
users only. Stable provider-qualified identity IDs identify accounts; emails are
attributes and never merge accounts. A signed-in account may explicitly link an
additional identity. Linking retains the initiating account and session, requires
that session to stay live through final attachment, and rejects an identity owned
by another account. Successful sign-in refreshes account email, name, and avatar from that identity,
preserving nullable attributes. Linking updates linked email, verification, and
name without replacing the account display profile. Existing links retain their
original link time. Tokens and raw `origin` identity data are not persisted.
External products still own their users, linking, permissions, and sessions;
the gateway creates no external user store or central SSO session.

`UNIAUTH_INITIAL_ADMIN_EMAIL` supplies a plain deployment email address. No seed
account is created. The first Google or GitHub normalized identity with matching
email, compared case-insensitively, and `email_verified=true` atomically claims
administrator authority on its
actual platform account, during sign-in or linking. Microsoft's unknown
verification cannot claim bootstrap authority. Startup can update the pending
email before the claim. Afterward neither a profile-email change nor a deployment
email change transfers the role or grants another administrator.

Bootstrap trusts the upstream verification assertion at verification time.
[OIDC Standard Claims](https://openid.net/specs/openid-connect-core-1_0.html#StandardClaims)
defines this meaning; it is not a fresh mailbox challenge or a guarantee of
continuous mailbox control. [Google's token verification guidance](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token)
distinguishes Gmail/managed-domain authority from external email addresses whose
ownership may have changed after verification. The policy accepts configured
email addresses uniformly and retains that trust cost. The
[GitHub email API](https://docs.github.com/en/rest/users/emails#list-email-addresses-for-the-authenticated-user)
supplies primary and verified fields. [Microsoft's claim reference](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)
treats email as mutable and unsuitable by itself for authorization. Stable account
binding after the one-time verified claim limits later email changes' authority.

An administrator may list only user ID, nullable name and email, creation time,
and created-App count, with bounded cursor pagination. That role provides no
other user's identity details, App definitions, callback/provider settings,
Secret metadata or values, or management controls. Every App and Secret operation
checks the session's account ownership and system-App restriction in the same
storage query or transaction. A nonowned or protected App returns ordinary
not-found behavior even to administrators.

Migration 3 intentionally removes all existing Apps and Secrets and establishes
the final platform schema. There is no legacy owner, seed account, preserved
credential, or application-code compatibility branch. Products recreate their
registrations and backend credentials. Subsequent restarts preserve platform
accounts, links, and newly owned Apps.

Console sessions remain eight-hour in-memory state, using HttpOnly,
SameSite=Strict cookies and Secure under HTTPS. Pending login/link state is
bounded, expires after ten minutes, and has a separate random HttpOnly,
SameSite=Lax browser binding. Linking relies on that flow binding when a Strict
session cookie is absent on a cross-site redirect, while checking the initiating
session remains live. If a session cookie is present, it must match the initiating
session. Origin and CSRF protect authenticated mutations. Password configuration,
login input, and password verification are removed.

This decision partially supersedes the [gateway ownership decision](2026-10-12-authentication-gateway.md)
for Console account persistence and authority; external product ownership,
identity collection, and one-time handover remain with that note. It replaces
the login mechanism in the [direct administrator password decision](../simplification/2026-10-13-direct-administrator-password.md),
which retains the rationale for the earlier format simplification. It also
supersedes pre-platform credential preservation in the
[Secret metadata decision](../feature/2026-10-13-secret-metadata.md), while retaining
its metadata and credential lifecycle rules. The
[blueprint](../../../../docs/blueprint/README.md), [API](../../../../docs/reference/api.md),
and [configuration](../../../../docs/reference/configuration.md) own their current
requirements and consumer contracts.

## Alternatives

**Releasing OAuth progress when the start handler returns.** Returning from
`location.assign` only schedules navigation. Releasing progress there briefly
restores buttons and allows another start before departure; linking also reloads
its identity list unnecessarily. The navigation owner lasts until departure or
history restoration, while previews explicitly report in-page completion.

**Retaining password login.** Deployment password login had removed encoded-hash
setup errors, but one shared credential supplied administrator-level management
rather than public individual account ownership. The public platform requires
OAuth as its only sign-in mechanism.

**Email-based account merging.** Automatically sharing an account across provider
emails would avoid manual linking, but an email is a mutable profile attribute,
and Microsoft exposes no trusted verification flag here. Accounts use stable
provider IDs; authenticated explicit linking controls additional sign-in methods.

**Preserving pre-platform registrations.** Existing rows have no platform owner.
Preservation would require an ownership assignment rule and retained data
migration. The authorized database reset removes that ambiguity; operators
recreate registrations and product credentials.

**Central SSO for external products.** A shared Console account is appropriate for
platform ownership, but does not authorize UniAuth to merge product users or
manage their sessions. Those responsibilities remain with each product.

## Consequences

People can register unrelated products without sharing App configuration or
credentials. Administrators get the requested user inventory and counts while
using the same owner-only App controls as everyone else. Separate providers with
equal emails create separate accounts unless linked from an authenticated account;
an identity already owned by another account cannot be transferred by linking.

The one-time bootstrap requires Google or GitHub verified email evidence. A
Microsoft-only deployment remains usable for ordinary accounts but cannot claim
its initial administrator until a qualifying provider is configured. The role
is durable, so editing deployment email after claim does not recover or reassign
administrator access. The chosen verification trust does not establish present
mailbox control for all external email domains.

The database upgrade invalidates all old product registrations and credentials.
The final schema stores Console profiles, so database access now includes that
personal information as well as owned App configuration and digests. Complete
external handover payloads retain their five-minute in-memory policy. Restart
invalidates gateway flows/results and Console pending flows/sessions, while
preserving accounts, links, and product registrations created after migration.
All transient steps still require one process. A failed listener bind leaves the
running instance's Console credential, configuration, and bootstrap state intact.
Listener reservation is not a database or distributed singleton lock: instances
on different listener addresses sharing one database remain unsupported.

Automated tests use controlled identity and OAuth fixtures. They establish local
ownership, bootstrap, linking, browser binding, and handover behavior without
proving live provider registration, consent, or interactive authentication.
