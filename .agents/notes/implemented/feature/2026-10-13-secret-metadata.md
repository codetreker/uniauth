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
value. Origin, administrator session, and CSRF checks apply as for other
management mutations. The audit action is `secret.update`; payload names and
descriptions are excluded from logs. The [API reference](../../../../docs/reference/api.md)
owns exact requests and responses.

Embedded SQL migration 2 rebuilds the Secret table with required metadata and
preserves its App lookup index. Existing active and revoked rows receive the
deterministic name `Secret <id>` and an empty description. IDs, digests, and
creation and revocation timestamps survive unchanged, so existing credentials
retain their authentication behavior. No operator-provided labels exist in the
old schema; using the stable ID gives those rows recognizable, nonconflicting
context without inventing their purpose. Operators can edit the generated labels.
The migrated schema is authoritative; application code has no old-row fallback
and this change adds no foreign key. The
[configuration reference](../../../../docs/reference/configuration.md) owns
operational upgrade and backup behavior.

The Console collects initial metadata with App creation, has separate creation
and detail-editing forms for later credentials, and lists names, descriptions,
IDs, and lifecycle state. Named revocation confirms the affected credential.
The one-time display retains plaintext and per-App ownership until the operator
marks it saved and closes the dialog. Secret editors and issuance, update, and
revocation share the existing per-App operation owner with configuration changes
and deletion. A session-invalidating `401` returns to login and discards idle
drafts, while owners of operations already in flight remain until their handlers
settle. An issuance authorized before expiry can return success after an
unrelated request reports `401`; that response is not evidence of pending-write
failure. Retaining ownership lets a successful App or Secret issuance expose its
plaintext and complete Saved and Done. A failed pending operation releases its
retained owner so login remains usable. This ordering changes local UI cleanup,
not server session lifetime or authentication requirements. Production-component
mockups use named active and revoked Secrets and representative multiline
descriptions.

The [gateway decision](../architecture/2026-10-12-authentication-gateway.md)
continues to own administrator authority, credential verification, product user
ownership, and one-time identity handover. The
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
administrator authority must protect them. Logs exclude that context.

Existing installations upgrade through a schema migration without credential
reissue. Automatically assigned old-row labels preserve identification but do not
recover historical purpose. API clients creating Apps must supply initial Secret
metadata, and clients issuing or editing credentials must send the metadata JSON
object. Product login, result retrieval, and SDK configuration remain unchanged.

Per-App UI ownership prevents overlapping local actions and retains one-time
plaintext until saved, including successful pending issuance after another
request invalidates the session. It does not coordinate separate administrator
browsers or introduce server-side version checks. The plaintext is still unavailable
through later metadata reads or edits.
