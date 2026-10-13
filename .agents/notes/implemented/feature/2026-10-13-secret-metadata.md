# Agent Note: Secret Names and Descriptions

Status: implemented

## Problem

An App can have several active credentials during rotation and retain revoked
credentials for later reference. IDs and timestamps identify records but do not
record why a credential was issued or where it is used. That context must remain
available after the one-time plaintext value is no longer recoverable.

## Decision

Each credential record stores a required name and a description alongside its
verification digest and lifecycle metadata. Names contain 1–128 valid UTF-8
bytes, without surrounding whitespace, CR, or LF. Descriptions contain up to
4096 valid UTF-8 bytes and preserve whitespace and multiple lines. Omitted
descriptions become the empty string; supplied `null` is invalid. Names may
repeat because the generated Secret ID identifies the credential.

App creation includes required `initial_secret` metadata in the same operation
as the App and first credential. App configuration replacement remains independent
of Secret management. Additional credential creation accepts name and description,
and returns both with the one-time plaintext value. App creation retains its
existing `{app, secret, secret_id}` response; the Console already has the
submitted initial metadata for the one-time display. Every metadata read and
detail-update response includes name and description, including revoked records.

A separate Secret item `PUT` edits details for active or revoked credentials.
It changes neither the verification digest nor credential validity, creation or
revocation times, or App configuration. It does not recover or reissue a plaintext
value. Origin, Console session, and CSRF checks apply as for other
management mutations. The audit action is `secret.update`; payload names and
descriptions are excluded from logs. The [API reference](../../../../docs/reference/api.md)
owns exact requests and responses.

The [Secret reset decision](2026-10-13-secret-reset.md) owns immediate
replacement. It copies the selected active credential's current name and
description into the new record within the same transaction that revokes the
old key. Both records retain their metadata for later context, and the one-time
replacement display includes those copied values.

The original metadata delivery used SQL migration 2 to rebuild the Secret table
with required metadata and preserve its App lookup index. Existing active and
revoked rows received the
deterministic name `Secret <id>` and an empty description. IDs, digests, and
creation and revocation timestamps survived unchanged, preserving credential
authentication behavior during that metadata-only upgrade. No operator-provided
labels existed in the old schema; using the stable ID gave those rows recognizable,
nonconflicting context without inventing their purpose. The
[public platform decision](../architecture/2026-10-13-public-oauth-platform.md) now
supersedes that preservation policy: migration 3 removes all pre-platform Apps
and Secrets and establishes owner-scoped records. Metadata rules still apply
to all newly created credentials. The current schema is authoritative, with no
old-row fallback or added foreign key. The
[configuration reference](../../../../docs/reference/configuration.md) owns
operational upgrade and backup behavior.

The Console collects initial metadata with App creation, has separate creation
and detail-editing forms for later credentials, and lists names, descriptions,
IDs, and lifecycle state. Named revocation confirms the affected credential.
The one-time display retains plaintext and per-App ownership until the operator
marks it saved and closes the dialog. Secret editors and issuance, update,
reset, and revocation share the existing per-App operation owner with
configuration changes and deletion. A session-invalidating `401` returns to
login and discards idle drafts, while owners of operations already in flight
remain until their handlers
settle. An issuance authorized before expiry can return success after an
unrelated request reports `401`; that response is not evidence of pending-write
failure. Retaining ownership lets a successful App or Secret issuance expose its
plaintext and complete Saved and Done. A failed pending operation releases its
retained owner so login remains usable. This ordering changes local UI cleanup,
not server session lifetime or authentication requirements. Production-component
mockups use named active and revoked Secrets and representative multiline
descriptions.

The [gateway decision](../architecture/2026-10-12-authentication-gateway.md)
continues to own credential verification, external product user ownership, and
one-time identity handover; the linked platform decision owns Console authority.
The
[validation policy](../testing/2026-10-12-authentication-validation.md) owns the
unchanged repository coverage gates and caller-visible validation requirements.

## Alternatives

No separate metadata storage mechanism was considered. The existing credential
record owns issuance and revocation, so its name and description use that same
record and persistence lifecycle.

## Consequences

Operators can record credential purpose, distinguish rotation records, and
correct details after revocation without changing access. The name is descriptive
rather than unique; management operations still address an App-bound Secret ID.
Descriptions may contain deployment context, so database access and Console
owner-only access must protect them. Administrator authority grants no access
to another account's metadata. Logs exclude that context.

The metadata-only migration preserved old credentials and supplied labels that
identified records without recovering historical purpose. The subsequent platform
upgrade deliberately resets those records, requiring product registrations and
credentials to be recreated. API clients creating Apps must supply initial Secret
metadata, and clients issuing or editing credentials must send the metadata JSON
object. Product login, result retrieval, and SDK configuration remain unchanged.

Per-App UI ownership prevents overlapping local actions and retains one-time
plaintext until saved, including successful pending issuance after another
request invalidates the session. It does not coordinate separate Console
browsers or introduce server-side version checks. The plaintext is still unavailable
through later metadata reads or edits.
