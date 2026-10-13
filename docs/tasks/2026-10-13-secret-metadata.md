# Secret Metadata Validation Receipt

Date: 2026-10-13

## Delivered behavior

Every product Secret has a required name and an optional description. Names
contain 1–128 valid UTF-8 bytes without surrounding whitespace, CR, or LF.
Descriptions preserve whitespace and multiple lines within 4096 valid UTF-8
bytes; omission stores the empty string, and supplied `null` is rejected. Names
may repeat; generated IDs identify credentials.

App creation requires initial Secret metadata. Further issuance and detail
updates accept metadata JSON independently of App configuration replacement.
Details remain editable for active and revoked credentials without changing
credential validity, verification digests, lifecycle timestamps, or App data.
Reads and updates never recover plaintext. Console forms, credential lists,
named revocation confirmations, and one-time displays expose the saved context.
Per-App ownership includes Secret operations and retains the one-time display
until the operator marks the value saved and closes the dialog. The audit action
`secret.update` excludes names and descriptions from logs.

Embedded migration 2 upgrades existing active and revoked credentials with
`Secret <id>` names and empty descriptions, preserving identifiers, digests,
lifecycle timestamps, and the App lookup index. The
[Secret metadata decision](../../.agents/notes/implemented/feature/2026-10-13-secret-metadata.md)
records the metadata and migration rationale. The [API reference](../reference/api.md)
owns request shapes, limits, and response guarantees; the
[configuration reference](../reference/configuration.md) owns upgrade and backup
behavior. Earlier task receipts retain their original results.

## Observed validation

The final `make build check test` run passed for the completed implementation.

| Check | Observed result |
| --- | --- |
| Production build and static checks | Console assets, both SDKs, and Go executable built; Go formatting and vet, TypeScript, Prettier, and diff whitespace checks passed. |
| Service Go tests | 360 reported cases, including 83 top-level tests, passed with race detection; total coverage was 97.2%, Apps 92.1%, and Console 99.7%. |
| Go SDK tests | 106 cases, including 16 top-level tests, passed with race detection; coverage was 99.3%. |
| Coverage policy | Unchanged total 90%, package 85%, function 80%, and critical-block requirements passed without exclusions for both Go modules. |
| Console and JavaScript SDK tests | All 24 Console and 16 JavaScript SDK tests passed. |
| Release fixtures | All 17 existing Python release tests passed. |
| Package consumers | Packed JavaScript runtime/declaration checks and the external Go consumer passed. |
| Real SDK/gateway integration | Both race-enabled tests passed. |
| Browser scenarios | All three passed against a fresh executable in 17.5 seconds: real Console 5.6 seconds, desktop mockup 6.1 seconds, and mobile mockup 2.2 seconds. |
| Independent backend review | No confirmed critical or major issue; three additional race-enabled probes passed in 1.317 seconds. |
| Independent Console review | No final critical or major issue; ownership, stale reads, session expiry, and plaintext-retention assertions passed. |
| Documentation | All 40 active-document, link, skill metadata, note lifecycle, and discovery-alias checks passed after the receipt was added. |
| Process cleanup | Probe, service, Vite, and browser processes finished and were cleaned up. |

Backend checks validate required initial metadata, optional and nullable input
semantics, UTF-8 byte limits, exact multiline values, App-bound record lookup,
and active and revoked detail editing. A 12-credential migration probe upgraded,
downgraded, and upgraded the schema again while preserving IDs, App IDs, digests,
creation and revocation times, the index, and the absence of foreign keys.
Concurrent probes exercised 40 metadata writers with revocation, cancellation,
and record ownership. Rollback and audit assertions passed without logging
metadata or returning credentials from detail updates.

Console checks cover delayed response delivery after credential issuance commits,
one-time plaintext retention, detail editing and description clearing, revoked
editing, and reload. Name-validation regressions match Unicode whitespace
boundaries between the Console and Go validation. Browser scenarios exercise
initial named issuance, replacement credential metadata, these ownership and
editing behaviors, and the same forms in desktop and mobile production-component
mockups. Long multiline descriptions remain usable. At a 390-pixel mobile
viewport the document and editor measured 390 pixels wide and the credential
table 354 pixels; inspected screenshots confirmed the rendered detail and editor
states.

## Deferred review follow-up

The inherited Go JSON decoder behavior for duplicate `name` members followed
by `null` remains a minor follow-up. This feature does not introduce a
repository-wide duplicate-key decoding policy.

## Verification limits

These local results do not establish remote CI success, image publication,
release completion, deployment, or live Microsoft, Google, or GitHub login.
Product SDK contracts, dependency locks, and the authoritative coverage policy
remain unchanged. Browser scenarios use fixture credentials and local runtime
data; upstream authentication still requires configured provider registrations
and deployment verification.
