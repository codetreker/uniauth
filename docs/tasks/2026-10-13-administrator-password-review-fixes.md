# Administrator Password Configuration Validation

Date: 2026-10-13

## Scope

The deployment template leaves `UNIAUTH_ADMIN_PASSWORD` empty, so copying it
unchanged cannot enable Console access with a publicly known password. Startup
requires a single-line password containing 1–1024 bytes and rejects CR or LF,
which the Console's HTML password control removes. Accepted values retain exact
spaces, dollar signs, and Unicode. Login input still returns `400 invalid_input`
for empty or oversized values and `401 unauthorized` for a mismatch.

These corrections address review findings
[4256220453](https://github.com/codetreker/uniauth/pull/9#discussion_r4256220453)
and [4256221411](https://github.com/codetreker/uniauth/pull/9#discussion_r4256221411).
The [administrator password decision](../../.agents/notes/implemented/simplification/2026-10-13-direct-administrator-password.md)
owns the current contract. The [earlier receipt](2026-10-13-direct-administrator-password.md)
retains its validation results for the preceding implementation, including its
then-accepted CRLF configuration; it does not establish current startup behavior.

## Observed validation

The final `make build check test` run passed for these configuration corrections.

| Check | Observed result |
| --- | --- |
| Production build and static checks | Console assets, both SDKs, and the Go executable built; formatting, Go vet, TypeScript, Prettier, and diff whitespace checks passed. |
| Service Go tests | 318 cases, including 77 top-level tests, passed with race detection; total coverage was 97.0%, and Console package coverage was 99.3%. |
| Go SDK tests | All 106 cases passed with race detection; coverage was 99.3%. |
| Coverage policy | Unchanged total 90%, package 85%, function 80%, and critical-block requirements passed without exclusions for both Go modules. |
| Console and JavaScript SDK tests | All 16 tests passed in each suite. |
| Package consumers | Packed JavaScript runtime/declaration checks and the external Go consumer passed. |
| Real SDK/gateway integration | Both race-enabled tests passed in 1.297 seconds. |
| Production Dockerfile | Fresh build passed; local image ID began with `3d022bafcb37`. |
| Copied Compose template | The unchanged template failed startup because the administrator password was empty. |
| Multiline Compose configuration | Explicit LF, CR, and CRLF values each failed startup with a diagnostic that omitted the password value. |
| Single-line Compose configuration | A synthetic password containing leading/trailing spaces, a dollar sign, a hash character, and Unicode started successfully; exact-password login and session retrieval each returned `200`. |
| Independent review | No confirmed critical or major issue and no substantive minor follow-up. |
| Documentation | All 37 active-document, link, skill metadata, note lifecycle, and discovery-alias checks passed. |
| Cleanup | Fixture containers, volumes, and network were removed; removal was verified. |

Independent public-caller probes checked `LoadConfig`, `server.New`, and
`console.New`: empty, oversized, and CR/LF passwords were rejected without
creating the database or leaking credential values in logs. One-byte and
1024-byte passwords remained valid and matched exactly; API `401` and `400`
behavior remained unchanged. A DOM probe and the
[WHATWG password-state rules](https://html.spec.whatwg.org/multipage/input.html#password-state-%28type=password%29)
confirmed CR/LF removal by HTML password controls.

## Verification limits

No fresh local browser suite was run for these corrections. The earlier receipt's
browser results belong to its recorded implementation. New-head remote CI,
including its browser checks, was pending when this receipt was written.
Local container checks do not establish image publication, release completion,
deployment to the OC VM, or live third-party provider login.
