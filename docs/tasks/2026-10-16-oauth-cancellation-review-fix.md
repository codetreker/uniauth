# OAuth Navigation Cancellation Validation

## Scope and delivered behavior

[Review comment 4290353704](https://github.com/codetreker/uniauth/pull/15#discussion_r4290353704)
identified a locked Console when navigation was stopped before document commit.
At `e2e5253`, progress survived `location.assign`, but recovery depended on a
persisted `pageshow` that cancellation does not emit. Both sign-in and identity
linking were affected. This correction preserves the original transition scope
and the [initial validation receipt](2026-10-15-oauth-login-transition.md).

Each OAuth attempt now owns its navigation observer and abort listener. An abort
releases only that attempt; old signals and delayed preview completions cannot
change a newer attempt. Completion, cancellation, history restoration, and
unmount remove listeners. Pending API requests remain protected from unrelated
history events and competing actions.

A fixed `Cancel sign-in` control stops the browser load before releasing its
owner. It provides recovery without Navigation API and for navigations that
remain on the original document without a cancellation signal. The control does
not move the login card or provider buttons. Cancelled linking retains the
existing identity list and restores workspace navigation and sign-out.
Unfinished server transactions keep their existing expiry.

The [platform decision](../../.agents/notes/implemented/architecture/2026-10-13-public-oauth-platform.md),
[validation policy](../../.agents/notes/implemented/testing/2026-10-12-authentication-validation.md),
[blueprint](../blueprint/README.md), and [API reference](../reference/api.md)
own the current contracts. The HTML specification defines browser Stop through
[aborting a NavigateEvent](https://html.spec.whatwg.org/multipage/nav-history-apis.html#abort-a-navigateevent).

## Observed validation

- On original `e2e5253`, two real-browser regressions failed: after stopping held
  `/auth/authorize` navigation, the original login/account document remained and
  its controls stayed disabled. Document and URL checks passed before the failed
  recovery assertion.
- `make build check test` passed. All 69 Console cases passed, including abort
  recovery for login/linking, explicit recovery without Navigation API,
  stop-before-release ordering, stale navigation signals, delayed completion,
  history restoration, previews, and existing one-time credential ownership.
- The unchanged race and coverage gates passed: service Go coverage 96.6% across
  543 cases including subtests; standalone Go SDK coverage 99.3% across 106 cases.
  JavaScript SDK tests, packed/standalone consumers, SDK integration, and all 17
  release-script tests also passed.
- The final 14-scenario browser suite passed in 41.1 seconds with one worker and
  no retries. Four cancellation cases cover automatic recovery and explicit
  recovery without Navigation API for both login and linking. They require the
  original document, restored controls, duplicate prevention, and successful retry.
- Explicit recovery clicks the rendered Cancel button while the real initial
  authorization request is still held. The browser must report
  `net::ERR_ABORTED` before the route is released. Native-stop cases use
  `Page.stopLoading`; manual cases use actual mouse input without an earlier Stop.
- Desktop/mobile geometry checks and inspected screenshots confirm that the
  recovery control remains visible without shifting the card or provider buttons.
- Independent review found no introduced in-scope Critical or Major issues.

## Observation limits

Chromium establishes real pre-commit cancellation behavior. Shadowing Navigation
API exercises the explicit recovery path; it does not establish native behavior
in other browser engines. Successful-navigation probes still use the documented
same-document destination before following the captured real gateway URL.
Component history events do not prove a browser history-cache hit. OAuth adapters
remain controlled fixtures rather than live provider registrations. Existing
browser installations were reused, and task browser/server processes were stopped.
