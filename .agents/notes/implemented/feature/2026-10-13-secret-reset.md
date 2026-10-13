# Agent Note: Immediate App Secret Reset

Status: implemented

## Problem

An operator may need to invalidate one exposed product Secret immediately while
keeping the App and its other credentials available. Issuing a new Secret and
revoking the old one are separate operations; they leave an interval in which
both authenticate and require the operator to coordinate two mutations.

## Decision

Reset targets one active Secret ID within an App. One SQLite transaction revokes
that record and inserts a replacement with a new ID, independently generated
256-bit plaintext, and SHA-256 digest. A committed reset rejects later
authentication with the old value while retaining other active Secrets and the
revoked record for history. The replacement's creation time records its actual
issuance. Missing or wrong-App IDs return `404`; already revoked IDs return
`409`, and concurrent resets of the same ID cannot both issue replacements.

The authenticated Console exposes reset only for active Secrets. It identifies
the selected ID and requires explicit confirmation that the old key stops
working immediately. The new plaintext is returned once and remains visible
until the administrator acknowledges saving it. The management endpoint uses
the existing Origin, session, CSRF, no-store, and audit controls with action
`secret.reset`. The [API reference](../../../../docs/reference/api.md) owns the
exact endpoint and response.

## Alternatives

**Replace the digest in the existing record.** This would keep the Secret ID,
but the sole `created_at` value would no longer describe the key's issuance.
It would also discard the old credential record, obscuring which key was
invalidated. A new row preserves both meanings using the existing schema.

**Create then revoke through the existing controls.** This permits planned
rotation without interrupting a product. It does not meet the need to invalidate
the old key in the same committed operation that issues its replacement.

## Consequences

Reset is an immediate replacement, while planned rotation can keep both keys
active until the product has switched. Products using the old key must be
updated after the operation commits; an authentication request admitted before
commit may still finish under its earlier snapshot.

If the response is lost after commit, the replacement plaintext cannot be
recovered and retrying the revoked ID cannot generate another replacement. The
operator can create another Secret and update the product. This cost is stated
in the confirmation; reset is not a safe substitute for planned overlap when
continuous product access is required. The App registry needs no schema
migration for this lifecycle operation.
