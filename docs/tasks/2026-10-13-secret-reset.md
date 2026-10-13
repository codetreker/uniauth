# Secret Reset Validation Receipt

## Delivered behavior

An administrator can reset an active App Secret after confirming that its
current key will stop working immediately. One SQLite transaction revokes the
selected record and issues a new ID and 256-bit key with the selected record's
current name and description. The new plaintext is shown once until the
administrator acknowledges saving it. Other active Secrets keep working.
Missing or wrong-App IDs return `404`; revoked IDs return `409`.

The Console keeps the affected App reserved through confirmation and one-time
display. A lost reset response is reconciled against current Secret metadata;
if the old ID is revoked and plaintext was not received, the operator is directed
to create another Secret. Session invalidation retains dispatched reset ownership
until the handler settles: success exposes the new plaintext through Saved and
Done, while failure releases an owner whose confirmation was already discarded.
Reset operations emit the redacted `secret.reset` audit action. The
[decision](../../.agents/notes/implemented/feature/2026-10-13-secret-reset.md)
records the replacement-row rationale and response-loss cost.

## Validation records

The original reset delivery at `6e0ca2c` passed `make setup`,
`make build check test`, and Playwright. It ran 324 service Go tests with 96.9%
total coverage and 21 Console tests. Its independent frontend review found and
verified the fix for a session-expiry path that could hide received plaintext.

Integration with main `2c599354`, including Secret metadata and pending-issuance
ownership, passed these checks:

- `make build check test` passed: 366 service Go tests with 97.1% total coverage,
  Apps 92.0%, and Console 99.7%. The standalone Go SDK ran 106 tests with 99.3%
  coverage. All 37 Console, 16 JavaScript SDK, and 17 release fixture tests
  passed, as did external SDK consumers and the SDK/gateway integration.
- Focused Apps and Console Go tests passed with and without `-race`. Assertions
  covered exact edited name and multiline description carryover, old-key
  rejection, replacement authentication, unrelated active keys, same-ID
  concurrent resets, wrong-App and revoked targets, and transaction rollback
  when either write fails. Console tests covered Origin, CSRF, audit redaction,
  and no-store responses.
- All three Playwright scenarios passed against a fresh local service and the
  production Console or its production-component mockup. The browser canceled
  and confirmed reset, checked copied metadata and one-time display, and observed
  `/auth/result` reject the old key with `401 invalid_app` while the new key
  reached `410 result_unavailable` for an unknown code. Existing creation,
  detail editing, revocation, and 390px mobile preview behavior remained usable.
  Viewport captures of the reset confirmation and replacement display were
  generated at `.tmp/mockup-reset-confirm.png` and
  `.tmp/mockup-reset-credential.png`.
- Independent backend and frontend integration reviews found no remaining
  Critical/Major issue. Component regressions covered both successful and failed
  pending resets after an unrelated request invalidated the administrator
  session, retaining received plaintext and releasing failed-operation ownership.
- The active-document/link checker and conflict-marker checks passed. The
  browser harness released ports 18080 and 5173 after testing.

These local fixtures do not establish a live Microsoft, Google, or GitHub login.
The reset behavior is exercised through the real SQLite registry and product
result-authentication path.
