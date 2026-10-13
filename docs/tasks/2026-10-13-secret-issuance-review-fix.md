# Secret Issuance Ownership Review-Fix Receipt

Date: 2026-10-13
Review thread: `4258593515`

## Delivered correction

A session-invalidating `401` discards unauthorized idle drafts while retaining
ownership tokens for already in-flight Console operations until they settle.
An App or Secret issuance that succeeds after another request reports `401`
keeps its one-time plaintext available through Saved and Done. A pending
operation that fails releases its retained owner, leaving login and subsequent
creation usable.

The [Secret metadata decision](../../.agents/notes/implemented/feature/2026-10-13-secret-metadata.md)
owns the ordering rationale: an already-authorized issuance can complete after
another request loses administrator authority, so that request's `401` is not
proof that the pending write failed. The fix changes Console ownership cleanup;
backend behavior, schema, API wire formats, SDKs, dependencies, and session
lifetime remain unchanged. The [original metadata receipt](2026-10-13-secret-metadata.md)
retains its initial delivery and validation history.

## Observed validation

The final `make build check test` run passed for the corrected Console.

| Check | Observed result |
| --- | --- |
| Production build and static checks | Console assets, both SDKs, and Go executable built; formatting, Go vet, TypeScript, Prettier, and diff whitespace checks passed. |
| Service Go tests | 360 reported cases, including 83 top-level tests, passed with race detection; total coverage was 97.2%, Apps 92.1%, and Console 99.7%. |
| Go SDK tests | 106 cases passed with race detection; coverage was 99.3%. |
| Coverage policy | Unchanged total 90%, package 85%, function 80%, and critical-block requirements passed without exclusions for both Go modules. |
| Console tests | All 30 passed, including six new regressions exercising public UI behavior. |
| JavaScript SDK and release fixtures | All 16 JavaScript SDK and 17 existing Python release tests passed. |
| Package consumers | Packed JavaScript runtime/declaration checks and the external Go consumer passed. |
| Real SDK/gateway integration | Both race-enabled tests passed in 1.285 seconds. |
| Browser scenarios | All three passed against a fresh executable in 16.2 seconds: real Console 5.3 seconds, desktop mockup 5.4 seconds, and mobile mockup 2.2 seconds. |
| Independent component review | Eight probes passed in 4.33 seconds; the final review found no critical or major issue. |
| Documentation | All 41 active-document, link, skill metadata, note lifecycle, and discovery-alias checks passed after adding this receipt. |
| Process cleanup | Probe, browser, service, and Vite processes finished and were cleaned up. |

The regressions and independent component probes exercise first and later
credential issuance, successful and failed responses, and reauthentication both
before settlement and after it. They reproduce the original failure against
revision `4a65b02`: an App A write and reconciliation fail; its recovery Refresh
is delayed while App B or a new App begins issuance; the Refresh returns `401`;
the successful issuance shows plaintext whose Done action is stranded. Both
initial and later credential creation exhibit that failure on the prior revision.

With the correction, Saved and Done remove the displayed plaintext, competing
modal actions and logout remain blocked during pending issuance and its
one-time display, and failed pending operations release ownership. Subsequent
creation remains usable. Browser scenarios separately verify the existing real
metadata-management flow and desktop/mobile production-component mockups; the
six Console regressions and eight independent probes verify the `401` race.

## Deferred review follow-up

Early reauthentication can leave a duplicate App entry when a pending creation
later appends its result. The same behavior was reproduced on base revision
`94d4a0d`; it is an inherited minor follow-up. Changing list reconciliation is
outside this correction to issuance ownership and plaintext recovery.

## Verification limits

These local results do not establish remote CI success for a new commit, image
publication, release completion, deployment, or live upstream provider login.
Browser scenarios use fixture credentials and local runtime data. Backend and
SDK checks verify that the Console correction preserves their existing behavior.
