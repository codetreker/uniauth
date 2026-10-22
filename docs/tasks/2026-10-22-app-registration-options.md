# App Registration Options Validation

Date: 2026-10-22

## Delivered behavior

App registration and both product SDKs accept HTTP and HTTPS product callbacks
for any host. Callback strings retain exact matching and existing restrictions
on credentials, fragments, reserved query parameters, and malformed URLs.
Gateway public-origin and SDK base-origin transport policies remain separate.

App creation may omit `initial_secret`, creating no credential and returning
only `app`. The Console defaults initial issuance to unchecked, opens the new
App directly, and supports later Secret creation. Choosing initial issuance
retains atomic creation and the one-time plaintext acknowledgement. Result
retrieval still requires an active App credential.

The [registration decision](../../.agents/notes/implemented/feature/2026-10-22-app-registration-options.md)
records the rationale and consequences. The [API reference](../reference/api.md)
owns requests and responses.

## Observed validation

- `make setup` completed with repository-local dependencies and caches.
- `make build check test` passed, including documentation checks, formatting,
  static analysis, race-enabled Go coverage gates, SDK package consumers, and
  real gateway integration for both SDKs. Service coverage was 96.6%; Go SDK
  coverage was 99.3%. Console tests passed 62 cases; JavaScript SDK tests passed
  16 cases.
- `pnpm test:browser` passed all eight tests against the production Console
  build and interactive mockup using preinstalled headless Chromium. The new
  service case creates an external HTTP callback App without a Secret, verifies
  the request and response omit credential fields, reloads persisted state,
  creates a Secret later, and deletes its App. Existing initial issuance,
  acknowledgement, reset, owner isolation, and mobile preview cases also pass.
- Independent review found no introduced Critical or Major issue.

The first browser launch failed before application execution because the task
worktree's temporary path exceeded Chromium's Unix-domain socket path limit.
Using a shorter task-owned temporary directory under the repository root's
`.tmp/` resolved the launch error. No browser installation was added. Browser
and fixture processes terminated after the suite.
