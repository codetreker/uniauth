# OAuth Sign-in Transition Validation

## Delivered behavior

Starting OAuth no longer treats authentication progress as unfinished App work.
The login card keeps its layout while the selected provider shows `Connecting…`.
The Console owns progress for both sign-in and linking, prevents synchronous
competing starts, and retains that owner after `location.assign` returns.

Errors release the controls and remain visible for retry. In-page preview
completion releases the owner and reloads linked identities when appropriate.
External linking keeps the existing identity list visible until departure.
A persisted `pageshow` releases a navigation owner after history restoration;
it cannot release a start request that is still pending. Existing App operations
retain their separate Secret-saving warning and one-time credential ownership.

The [platform decision](../../.agents/notes/implemented/architecture/2026-10-13-public-oauth-platform.md),
[validation policy](../../.agents/notes/implemented/testing/2026-10-12-authentication-validation.md),
[blueprint](../blueprint/README.md), and [API reference](../reference/api.md)
describe the mechanism and its observable behavior.

## Observed validation

- The new ordinary-login warning assertion fails against original production
  source at `840655c`: a Secret-saving paragraph appears after clicking a provider.
  The corrected source passes that assertion.
- `make build check test` passed. Service Go coverage is 96.6% across 543 cases
  including subtests; standalone Go SDK coverage is 99.3% across 106 cases.
  The existing coverage thresholds, critical-block policy, and race detection
  remain unchanged, with no exclusions.
- All 63 Console tests passed, including synchronous duplicate prevention,
  start failure and retry, preview failure and completion, retained navigation
  ownership for login and linking, ordinary versus persisted `pageshow`, and
  pending issuance through unrelated session invalidation and acknowledgement.
- All 16 JavaScript SDK cases, packed JavaScript and standalone Go consumers,
  race-enabled SDK/gateway integration, and 17 release-script tests passed.
- All 10 Playwright scenarios passed in 42.5 seconds with one worker and no
  retries. New desktop and mobile cases compare card and button rectangles,
  require no Secret-saving warning during OAuth, hold start responses, verify
  failed-start retry, and reject repeated starts. Linking preserves identity
  text and performs no early identity reload. Existing platform ownership,
  credentials, provider fixtures, and responsive previews also passed.
- Independent review found no introduced in-scope Critical or Major issues.

## Browser observation limits

The transition tests preserve real API calls and capture their authorization
URLs. To observe the production `location.assign` return without blocked
renderer inspection, they substitute a same-document fragment destination in the
response. They then visit the captured real authorization URL, reach the
controlled provider, and verify usable controls after actual browser Back.
This observes assign-return state through an explicit probe; it is not a direct
measurement while a cross-document navigation request is paused.

Component tests inject persisted history events for both login and linking.
Actual Back usability does not establish that Chromium used its history cache.
Controlled OAuth adapters verify local gateway and Console composition, not live
Microsoft, Google, or GitHub registration, consent, or credentials. Validation
reused an existing browser, and task servers and browser processes were stopped.
