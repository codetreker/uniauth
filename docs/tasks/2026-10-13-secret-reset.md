# Secret Reset Validation Receipt

## Delivered behavior

An administrator can reset an active App Secret after confirming that its
current key will stop working immediately. One SQLite transaction revokes the
selected record and issues a new ID and 256-bit key. The new plaintext is shown
once until the administrator acknowledges saving it. Other active Secrets keep
working. Missing or wrong-App IDs return `404`; revoked IDs return `409`.

The Console keeps the affected App reserved through confirmation and one-time
display. A lost reset response is reconciled against current Secret metadata;
if the old ID is revoked and plaintext was not received, the operator is directed
to create another Secret. A session failure during the metadata refresh does not
hide plaintext already received from a successful reset. Reset operations emit
the redacted `secret.reset` audit action. The
[decision](../../.agents/notes/implemented/feature/2026-10-13-secret-reset.md)
records the replacement-row rationale and response-loss cost.

## Observed validation

- `make setup`, `make build check test`, and a final `make check` passed.
  Service go-cov ran 324 tests with 96.9% total coverage; Apps reached 90.7%
  and Console 99.3%. The standalone Go SDK ran 106 tests with 99.3% coverage.
  The Console ran 21 tests, JavaScript SDK 16, and release fixtures 17; both
  external SDK consumers and the SDK/gateway integration passed.
- Focused Apps and Console Go tests passed with and without `-race`. They covered
  old-key rejection, replacement authentication, unrelated active keys,
  same-ID concurrent resets, wrong-App and revoked targets, and transaction
  rollback when either write fails. Console tests covered Origin, CSRF, audit
  redaction, and no-store responses.
- All three Playwright scenarios passed against a fresh local service and the
  production Console or its production-component mockup. The browser canceled
  and confirmed reset, checked one-time display, and observed `/auth/result`
  reject the old key with `401 invalid_app` while the new key reached
  `410 result_unavailable` for an unknown code. Desktop and 390px mobile mockups
  remained interactive. Viewport captures of the reset confirmation and
  replacement display were generated at `.tmp/mockup-reset-confirm.png` and
  `.tmp/mockup-reset-credential.png`.
- Independent backend and frontend reviews found no remaining Critical/Major
  issue. The frontend review found and verified the fix for a session-expiry
  path that could otherwise hide the new plaintext after reset.
- `git diff --check` and the active-document/link checker passed. Playwright
  services released ports 18080 and 5173 after testing; no task process remains.

These local fixtures do not establish a live Microsoft, Google, or GitHub login.
The reset behavior is exercised through the real SQLite registry and product
result-authentication path.
